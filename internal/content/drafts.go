package content

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/model"
)

// DraftItem 是“我的草稿 / 我的定时发布”列表项。
// ContentHTML 返回完整的安全 HTML（按字符截断 HTML 会破坏标签），PlainText 为纯文本摘要。
// Status 默认取 posts.status；定时发布作业失败或暂停时展示 failed/paused，
// 并在定时列表附加 publish_failed / publish_error（草稿列表不返回这两个字段）。
type DraftItem struct {
	ID              string  `json:"id"`
	ContentHTML     string  `json:"content_html"`
	PlainText       string  `json:"plain_text"`
	Visibility      string  `json:"visibility"`
	Revision        int     `json:"revision"`
	Status          string  `json:"status"`
	UpdatedAt       string  `json:"updated_at"`
	ScheduledAt     *string `json:"scheduled_at,omitempty"`
	AttachmentCount int     `json:"attachment_count"`
	PublishFailed   *bool   `json:"publish_failed,omitempty"`
	PublishError    *string `json:"publish_error,omitempty"`
}

// publishStateFor 把帖子状态与最新一条定时发布作业状态合并为列表展示状态。
// 最新作业处于 failed / paused 时以作业状态为准，其余情况沿用 posts.status；
// publishFailed 只表示发布失败（暂停由 status=paused 单独展示），
// publishError 给出失败原因供前端提示与重试。
func publishStateFor(postStatus, jobStatus, jobError string) (status string, publishFailed bool, publishError string) {
	switch jobStatus {
	case "failed":
		return "failed", true, jobError
	case "paused":
		return "paused", false, ""
	default:
		return postStatus, false, ""
	}
}

// ListMyDrafts 返回当前用户的草稿列表。
func (s *Service) ListMyDrafts(c *gin.Context) {
	s.listMyPosts(c, model.PostStatusDraft)
}

// ListMyScheduled 返回当前用户的定时发布列表。
func (s *Service) ListMyScheduled(c *gin.Context) {
	s.listMyPosts(c, model.PostStatusScheduled)
}

func (s *Service) listMyPosts(c *gin.Context, status string) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	viewer := viewerOf(c)
	if !viewer.Authenticated {
		api.Respond(c, api.ErrUnauthenticated("请先登录"))
		return
	}
	if viewer.IsAI {
		api.Respond(c, api.ErrForbidden("AI 账号不能通过此接口操作"))
		return
	}
	limit, err := api.PageLimit(c)
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	cursor, err := api.TrimPageCursor(c)
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", "无效的游标")
		return
	}
	ctx := c.Request.Context()

	// 草稿与定时帖都保留草稿工作区，正文与版本号取自 post_drafts。
	// 定时列表额外关联最新一条发布作业（pending/running/failed/paused），
	// 用于展示失败或暂停状态；草稿没有发布作业，该列为空并沿用 posts.status。
	sql := `SELECT p.id, p.visibility, p.scheduled_at, p.updated_at, p.status,
			COALESCE(d.content_html, ''), COALESCE(d.plain_text, ''),
			COALESCE(d.draft_revision, 0), COALESCE(att.total, 0),
			COALESCE(job.status, ''), COALESCE(job.last_error, '')
		FROM posts p
		LEFT JOIN post_drafts d ON d.post_id = p.id
		LEFT JOIN LATERAL (
			SELECT COUNT(*) AS total FROM post_draft_attachments da
			WHERE da.post_id = p.id AND da.is_active
		) att ON TRUE
		LEFT JOIN LATERAL (
			SELECT status, last_error FROM background_jobs
			WHERE job_type = 'post_publish' AND ref_type = 'post' AND ref_id = p.id
			  AND status IN ('pending', 'running', 'failed', 'paused')
			ORDER BY id DESC LIMIT 1
		) job ON TRUE
		WHERE p.author_id = $1 AND p.status = $2`
	args := []any{viewer.ID, status}
	if cursor > 0 {
		args = append(args, cursor)
		sql += fmt.Sprintf(" AND p.id < $%d", len(args))
	}
	args = append(args, limit+1)
	sql += fmt.Sprintf(" ORDER BY p.id DESC LIMIT $%d", len(args))

	rows, err := pool.Query(ctx, sql, args...)
	if err != nil {
		s.internalError(c, "读取草稿列表失败", err)
		return
	}
	defer rows.Close()

	items := make([]DraftItem, 0, limit+1)
	ids := make([]int64, 0, limit+1)
	for rows.Next() {
		var id int64
		var visibility, postStatus string
		var scheduledAt *time.Time
		var updatedAt time.Time
		var contentHTML, plainText string
		var revision, attachmentCount int
		var jobStatus, jobError string
		if err := rows.Scan(&id, &visibility, &scheduledAt, &updatedAt, &postStatus,
			&contentHTML, &plainText, &revision, &attachmentCount, &jobStatus, &jobError); err != nil {
			s.internalError(c, "读取草稿列表失败", err)
			return
		}
		displayStatus, publishFailed, publishError := publishStateFor(postStatus, jobStatus, jobError)
		item := DraftItem{
			ID:              api.FormatID(id),
			ContentHTML:     contentHTML,
			PlainText:       summary(plainText),
			Visibility:      visibility,
			Revision:        revision,
			Status:          displayStatus,
			UpdatedAt:       api.FormatTime(updatedAt),
			AttachmentCount: attachmentCount,
		}
		// 只有定时发布列表返回失败/暂停信息，草稿列表字段保持原样。
		if status == model.PostStatusScheduled {
			item.PublishFailed = &publishFailed
			item.PublishError = &publishError
		}
		if scheduledAt != nil {
			scheduled := api.FormatTime(*scheduledAt)
			item.ScheduledAt = &scheduled
		}
		items = append(items, item)
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		s.internalError(c, "读取草稿列表失败", err)
		return
	}

	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	var lastID int64
	if len(items) > 0 {
		lastID = ids[len(items)-1]
	}
	c.JSON(http.StatusOK, api.ListResponse(items, lastID, hasMore))
}
