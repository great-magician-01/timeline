package content

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/great-magician-01/timeline/internal/access"
	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/auth"
	"github.com/great-magician-01/timeline/internal/jobs"
	"github.com/great-magician-01/timeline/internal/model"
)

// publishLocked 把草稿或定时帖发布为已发布内容：写入首个版本、更新当前版本与发布时间，
// 并重建标签、搜索、提及与引用通知。
// 调用方必须在同一事务内调用；重复执行不会产生第二个版本
// （post_versions(post_id, version_number) 唯一索引 + 行锁双重保证）。
func (s *Service) publishLocked(ctx context.Context, q access.Querier, postID, actorID int64) error {
	post, err := loadPostForUpdate(ctx, q, postID)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.ErrNotFound("内容不存在或无权访问")
	}
	if err != nil {
		return err
	}
	if post.Status == model.PostStatusPublished {
		return nil
	}
	if post.Status != model.PostStatusDraft && post.Status != model.PostStatusScheduled {
		return api.ErrConflict("invalid_state", "当前状态不能发布")
	}

	var format, source, html, plain string
	err = q.QueryRow(ctx, `
		SELECT content_format, content_source, content_html, plain_text
		FROM post_drafts WHERE post_id = $1`, postID,
	).Scan(&format, &source, &html, &plain)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.ErrValidation("草稿内容不存在，无法发布")
	}
	if err != nil {
		return err
	}
	// 发布前重新校验正文与账号可见性限制，避免停用或非法状态被发布。
	if strings.TrimSpace(plain) == "" {
		return api.ErrValidation("正文不能为空").
			WithDetails(gin.H{"content_source": "正文不能为空"})
	}
	if err := ensureVisibilityAllowed(ctx, q, post.AuthorID, post.Visibility); err != nil {
		return err
	}

	var versionCount int
	if err := q.QueryRow(ctx, `
		SELECT COUNT(*) FROM post_versions WHERE post_id = $1`, postID).Scan(&versionCount); err != nil {
		return err
	}
	if versionCount == 0 {
		attachments, err := activeDraftAttachmentIDs(ctx, q, postID)
		if err != nil {
			return err
		}
		content := normalizedPost{
			format:        format,
			source:        source,
			html:          html,
			plain:         plain,
			visibility:    post.Visibility,
			attachmentIDs: attachments,
		}
		versionID, err := s.insertVersion(ctx, q, postID, actorID, 1, content, nil)
		if err != nil {
			return err
		}
		if err := linkAttachments(ctx, q, postID, attachments); err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `
			UPDATE posts
			SET status = 'published', current_version_id = $2,
			    published_at = COALESCE(published_at, now()), scheduled_at = NULL, updated_at = now()
			WHERE id = $1`, postID, versionID); err != nil {
			return err
		}
	} else {
		// 重试或重复投递：只推进状态，不追加版本，也不再改动 published_at。
		if _, err := q.Exec(ctx, `
			UPDATE posts
			SET status = 'published', published_at = COALESCE(published_at, now()),
			    scheduled_at = NULL, updated_at = now()
			WHERE id = $1`, postID); err != nil {
			return err
		}
	}

	updated, err := loadPost(ctx, q, postID)
	if err != nil {
		return err
	}
	return s.rebuildDerived(ctx, q, updated, plain)
}

// sameScheduleTime 比较作业计划时间与帖子当前定时时间。
// 精度只取到秒：作业幂等键同样按秒生成，避免因为数据库/JSON 的亚秒精度差异误判为“已变更”。
func sameScheduleTime(jobRunAt, scheduledAt time.Time) bool {
	return jobRunAt.Unix() == scheduledAt.Unix()
}

// HandlePublishJob 执行定时发布作业（jobs.TypePostPublish）。
// 重复执行、帖子已被取消或已发布时都视为处理成功；作者停用时把作业置为 paused。
func (s *Service) HandlePublishJob(ctx context.Context, job jobs.Job) error {
	if job.RefID == nil {
		return nil
	}
	postID := *job.RefID
	pool := s.pools.Pool()
	if pool == nil {
		return fmt.Errorf("数据库尚未就绪")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	post, err := loadPostForUpdate(ctx, tx, postID)
	if errors.Is(err, pgx.ErrNoRows) {
		// 帖子行不存在：重试没有意义，视为已处理。
		return nil
	}
	if err != nil {
		return err
	}
	if post.Status != model.PostStatusScheduled {
		// 已发布、已取消或已删除：不再发布，幂等成功。
		return nil
	}
	// 作者在作业被领取之后又修改了发布时间（或重新入队了新的作业）时，本作业已过期
	// （docs/WORKFLOWS.md 3.3.4“旧任务不能再次生效”）：取消本作业，等新作业到点执行。
	if post.ScheduledAt == nil || !sameScheduleTime(job.RunAt, *post.ScheduledAt) {
		if _, err := tx.Exec(ctx, `
			UPDATE background_jobs SET status = 'canceled', updated_at = now() WHERE id = $1`,
			job.ID); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		s.log.Info("定时发布作业的计划时间已变更，取消过期作业", "job_id", job.ID, "post_id", postID)
		return jobs.ErrHandled
	}

	var authorStatus string
	if err := tx.QueryRow(ctx, `SELECT status FROM users WHERE id = $1`, post.AuthorID).Scan(&authorStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if authorStatus != auth.StatusActive {
		if _, err := tx.Exec(ctx, `
			UPDATE background_jobs SET status = 'paused', updated_at = now() WHERE id = $1`,
			job.ID); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		s.log.Info("作者账号已停用，已暂停定时发布作业", "job_id", job.ID, "post_id", postID)
		return jobs.ErrHandled
	}

	if err := s.publishLocked(ctx, tx, postID, post.AuthorID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.log.Info("定时发布完成", "job_id", job.ID, "post_id", postID)
	return nil
}
