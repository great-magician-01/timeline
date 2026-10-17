package repo

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/great-magician-01/timeline/internal/modules/content/domain"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// GetPost 读取帖子行（不过滤状态/可见性）。
func (r *Repo) GetPost(ctx context.Context, postID int64) (model.Post, error) {
	post, err := scanPost(r.ex(ctx).QueryRow(ctx,
		`SELECT `+postColumns+` FROM posts WHERE id = $1`, postID))
	return post, notFoundIfNoRows(err, "内容不存在")
}

// GetPostForUpdate 锁定帖子行；调用方必须在事务内使用。
func (r *Repo) GetPostForUpdate(ctx context.Context, postID int64) (model.Post, error) {
	post, err := scanPost(r.ex(ctx).QueryRow(ctx,
		`SELECT `+postColumns+` FROM posts WHERE id = $1 FOR UPDATE`, postID))
	return post, notFoundIfNoRows(err, "内容不存在或无权访问")
}

// GetPosts 批量读取帖子行，返回 id → Post；缺失的 ID 不出现在结果中。
func (r *Repo) GetPosts(ctx context.Context, postIDs []int64) (map[int64]model.Post, error) {
	result := make(map[int64]model.Post, len(postIDs))
	ids := domain.UniqueIDs(postIDs)
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := r.ex(ctx).Query(ctx,
		`SELECT `+postColumns+` FROM posts WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		post, err := scanPost(rows)
		if err != nil {
			return nil, err
		}
		result[post.ID] = post
	}
	return result, rows.Err()
}

// InsertPost 写入帖子行；创建与更新时间由调用方显式传入。
func (r *Repo) InsertPost(ctx context.Context, post model.Post) error {
	_, err := r.ex(ctx).Exec(ctx, `
		INSERT INTO posts (
			id, author_id, status, visibility, quoted_post_id, scheduled_at,
			published_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		post.ID, post.AuthorID, post.Status, post.Visibility, post.QuotedPostID,
		post.ScheduledAt, post.PublishedAt, post.CreatedAt, post.UpdatedAt)
	return err
}

// UpdateVisibility 更新帖子当前可见性。
func (r *Repo) UpdateVisibility(ctx context.Context, postID int64, visibility string, at time.Time) error {
	_, err := r.ex(ctx).Exec(ctx, `
		UPDATE posts SET visibility = $2, updated_at = $3 WHERE id = $1`,
		postID, visibility, at)
	return err
}

// SetScheduled 把帖子置为定时发布状态。
func (r *Repo) SetScheduled(ctx context.Context, postID int64, scheduledAt, at time.Time) error {
	_, err := r.ex(ctx).Exec(ctx, `
		UPDATE posts SET status = $2, scheduled_at = $3, updated_at = $4 WHERE id = $1`,
		postID, model.PostStatusScheduled, scheduledAt, at)
	return err
}

// RevertToDraft 取消定时，回到草稿状态。
func (r *Repo) RevertToDraft(ctx context.Context, postID int64, at time.Time) error {
	_, err := r.ex(ctx).Exec(ctx, `
		UPDATE posts SET status = $2, scheduled_at = NULL, updated_at = $3 WHERE id = $1`,
		postID, model.PostStatusDraft, at)
	return err
}

// SetPublished 把帖子置为已发布：当前版本、首次发布时间（应用侧保留首次值）与更新时间。
// publishedAt 由调用方计算：已有发布时间时保持首次发布时间，否则使用本次时钟。
func (r *Repo) SetPublished(ctx context.Context, postID, versionID int64, publishedAt, at time.Time) error {
	_, err := r.ex(ctx).Exec(ctx, `
		UPDATE posts
		SET status = $2, current_version_id = $3, published_at = $4,
		    scheduled_at = NULL, updated_at = $5
		WHERE id = $1`,
		postID, model.PostStatusPublished, versionID, publishedAt, at)
	return err
}

// SetPublishedWithoutVersion 用于重试/重复投递：只推进状态与首次发布时间，不追加版本。
func (r *Repo) SetPublishedWithoutVersion(ctx context.Context, postID int64, publishedAt, at time.Time) error {
	_, err := r.ex(ctx).Exec(ctx, `
		UPDATE posts
		SET status = $2, published_at = $3, scheduled_at = NULL, updated_at = $4
		WHERE id = $1`,
		postID, model.PostStatusPublished, publishedAt, at)
	return err
}

// SetCurrentVersionAndVisibility 恢复历史版本后更新当前版本与可见性。
func (r *Repo) SetCurrentVersionAndVisibility(
	ctx context.Context,
	postID, versionID int64,
	visibility string,
	at time.Time,
) error {
	_, err := r.ex(ctx).Exec(ctx, `
		UPDATE posts SET current_version_id = $2, visibility = $3, updated_at = $4 WHERE id = $1`,
		postID, versionID, visibility, at)
	return err
}

// SoftDelete 逻辑删除帖子：状态 deleted + deleted_at，并清空定时时间。
func (r *Repo) SoftDelete(ctx context.Context, postID int64, at time.Time) error {
	_, err := r.ex(ctx).Exec(ctx, `
		UPDATE posts
		SET status = $2, deleted_at = $3, scheduled_at = NULL, updated_at = $3
		WHERE id = $1`,
		postID, model.PostStatusDeleted, at)
	return err
}

// BumpCounter 原子增减帖子聚合计数器并返回最新值。
//
// 不修改 posts.updated_at（互动不改变内容编辑时间，与旧实现一致），也不使用 GREATEST：
// 负向变更通过「col + delta >= 0」条件更新保证计数不会被减到负数；条件不满足时
// 视为已夹取到 0，返回当前值（已删除/不存在帖子返回 NotFound）。
func (r *Repo) BumpCounter(ctx context.Context, postID int64, counter string, delta int, at time.Time) (int, error) {
	column, err := domain.CounterColumn(counter)
	if err != nil {
		return 0, err
	}
	_ = at // 计数变更不写时间列；参数保留以维持跨模块契约形状
	if delta >= 0 {
		var value int
		err = r.ex(ctx).QueryRow(ctx, `
			UPDATE posts SET `+column+` = `+column+` + $2
			WHERE id = $1
			RETURNING `+column, postID, delta).Scan(&value)
		if err != nil {
			return 0, notFoundIfNoRows(err, "内容不存在")
		}
		return value, nil
	}
	var value int
	err = r.ex(ctx).QueryRow(ctx, `
		UPDATE posts SET `+column+` = `+column+` + $2
		WHERE id = $1 AND `+column+` + $2 >= 0
		RETURNING `+column, postID, delta).Scan(&value)
	if err == nil {
		return value, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	// 条件未命中：帖子不存在或已到 0，读取当前值区分两种情况。
	err = r.ex(ctx).QueryRow(ctx, `SELECT `+column+` FROM posts WHERE id = $1`, postID).Scan(&value)
	if err != nil {
		return 0, notFoundIfNoRows(err, "内容不存在")
	}
	return value, nil
}

// CountVersions 统计帖子的历史版本数量。
func (r *Repo) CountVersions(ctx context.Context, postID int64) (int, error) {
	var count int
	err := r.ex(ctx).QueryRow(ctx,
		`SELECT COUNT(*) FROM post_versions WHERE post_id = $1`, postID).Scan(&count)
	return count, err
}

// ListPostIDsByAuthor 读取某作者的全部帖子 ID（含草稿/定时/已发布）。
func (r *Repo) ListPostIDsByAuthor(ctx context.Context, authorID int64) ([]int64, error) {
	rows, err := r.ex(ctx).Query(ctx,
		`SELECT id FROM posts WHERE author_id = $1 ORDER BY id DESC`, authorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]int64, 0, 16)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// PostExcerpt 读取已发布帖子的纯文本摘录：当前版本正文优先，搜索投影兜底。
func (r *Repo) PostExcerpt(ctx context.Context, postID int64) (string, error) {
	var excerpt string
	err := r.ex(ctx).QueryRow(ctx, `
		SELECT COALESCE(NULLIF(v.plain_text, ''), s.plain_text, '')
		FROM posts p
		LEFT JOIN post_versions v ON v.id = p.current_version_id
		LEFT JOIN post_search s ON s.post_id = p.id
		WHERE p.id = $1`, postID).Scan(&excerpt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return excerpt, nil
}

// PostPlainTexts 批量读取帖子搜索投影的纯文本（通知预览使用）。
func (r *Repo) PostPlainTexts(ctx context.Context, postIDs []int64) (map[int64]string, error) {
	result := make(map[int64]string, len(postIDs))
	ids := domain.UniqueIDs(postIDs)
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := r.ex(ctx).Query(ctx,
		`SELECT post_id, plain_text FROM post_search WHERE post_id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var postID int64
		var plain string
		if err := rows.Scan(&postID, &plain); err != nil {
			return nil, err
		}
		result[postID] = plain
	}
	return result, rows.Err()
}
