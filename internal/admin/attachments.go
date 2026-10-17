package admin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/auth"
	"github.com/great-magician-01/timeline/internal/model"
)

// adminAttachmentResponse 是后台附件列表项；含上传者与所属内容，便于审核处置。
type adminAttachmentResponse struct {
	ID               string                  `json:"id"`
	Uploader         model.UserBriefResponse `json:"uploader"`
	PostID           *string                 `json:"post_id"`
	Kind             string                  `json:"kind"`
	Purpose          string                  `json:"purpose"`
	OriginalFilename string                  `json:"original_filename"`
	MediaType        string                  `json:"media_type"`
	SizeBytes        int64                   `json:"size_bytes"`
	Width            *int                    `json:"width"`
	Height           *int                    `json:"height"`
	DurationMS       *int64                  `json:"duration_ms"`
	Status           string                  `json:"status"`
	ContentURL       string                  `json:"content_url"`
	CreatedAt        string                  `json:"created_at"`
	UpdatedAt        string                  `json:"updated_at"`
}

// ListAttachments 处理 GET /api/v1/admin/attachments：
// 支持 uploader_id、post_id、purpose、status、q（原文件名）过滤与游标分页。
func (s *Service) ListAttachments(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	limit, err := api.PageLimit(c)
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	cursor, err := api.TrimPageCursor(c)
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	where := []string{"TRUE"}
	args := make([]any, 0, 6)
	addArg := func(value any) string {
		args = append(args, value)
		return "$" + strconv.Itoa(len(args))
	}
	// uploader_id / post_id 是 BIGINT 列：先校验再传参，非法输入返回 400。
	for _, filter := range []struct{ column, query string }{
		{"a.uploader_id", "uploader_id"},
		{"a.post_id", "post_id"},
	} {
		raw := strings.TrimSpace(c.Query(filter.query))
		if raw == "" {
			continue
		}
		id, parseErr := api.ParseID(raw)
		if parseErr != nil {
			api.Error(c, http.StatusBadRequest, "invalid_request", filter.query+" 必须是有效的 ID")
			return
		}
		where = append(where, filter.column+" = "+addArg(id))
	}
	if purpose := strings.TrimSpace(c.Query("purpose")); purpose != "" {
		if purpose != "post" && purpose != "avatar" {
			api.Error(c, http.StatusBadRequest, "invalid_request", "purpose 只能是 post 或 avatar")
			return
		}
		where = append(where, "a.purpose = "+addArg(purpose))
	}
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		if status != "available" && status != "deleted" {
			api.Error(c, http.StatusBadRequest, "invalid_request", "status 只能是 available 或 deleted")
			return
		}
		where = append(where, "a.status = "+addArg(status))
	}
	if q := strings.TrimSpace(c.Query("q")); q != "" {
		if len([]rune(q)) > 100 {
			api.Error(c, http.StatusBadRequest, "invalid_request", "搜索关键词过长")
			return
		}
		where = append(where, "STRPOS(LOWER(a.original_filename), LOWER("+addArg(q)+")) > 0")
	}
	if cursor > 0 {
		where = append(where, "a.id < "+addArg(cursor))
	}
	args = append(args, limit+1)

	rows, err := pool.Query(c.Request.Context(), `
		SELECT a.id, a.post_id, a.kind, a.purpose, a.original_filename, a.media_type,
		       a.size_bytes, a.width, a.height, a.duration_ms, a.status,
		       a.created_at, a.updated_at,
		       u.id, u.username, u.nickname, u.avatar_url, u.is_ai, u.status
		FROM attachments AS a
		JOIN users AS u ON u.id = a.uploader_id
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY a.id DESC LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		s.internalError(c, "查询后台附件列表失败", err)
		return
	}
	defer rows.Close()

	items := make([]adminAttachmentResponse, 0, limit+1)
	var lastID int64
	for rows.Next() {
		var (
			item        adminAttachmentResponse
			id          int64
			postID      *int64
			uploaderID  int64
			authorState string
			createdAt   time.Time
			updatedAt   time.Time
		)
		if err := rows.Scan(&id, &postID, &item.Kind, &item.Purpose, &item.OriginalFilename,
			&item.MediaType, &item.SizeBytes, &item.Width, &item.Height, &item.DurationMS,
			&item.Status, &createdAt, &updatedAt,
			&uploaderID, &item.Uploader.Username, &item.Uploader.Nickname,
			&item.Uploader.AvatarURL, &item.Uploader.IsAI, &authorState); err != nil {
			s.internalError(c, "读取后台附件列表失败", err)
			return
		}
		item.ID = api.FormatID(id)
		item.Uploader.ID = api.FormatID(uploaderID)
		item.Uploader.IsSuspended = authorState != auth.StatusActive
		item.PostID = optionalIDString(postID)
		item.ContentURL = "/api/v1/attachments/" + api.FormatID(id) + "/content"
		item.CreatedAt = api.FormatTime(createdAt)
		item.UpdatedAt = api.FormatTime(updatedAt)
		lastID = id
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		s.internalError(c, "遍历后台附件列表失败", err)
		return
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	c.JSON(http.StatusOK, api.ListResponse(items, lastID, hasMore))
}

// DeleteAttachment 处理 POST /api/v1/admin/attachments/:id/delete：
// 复用 media.DeleteAsAdmin 的引用规则与审计写入（同事务），只做逻辑删除。
func (s *Service) DeleteAttachment(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	attachmentID, err := api.ParseID(c.Param("id"))
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if s.media == nil {
		s.internalError(c, "附件处置服务未配置", errors.New("media service is nil"))
		return
	}
	adminUser, _ := auth.CurrentUser(c)
	ctx := c.Request.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		s.internalError(c, "开启附件处置事务失败", err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := s.media.DeleteAsAdmin(ctx, tx, adminUser.ID, attachmentID); err != nil {
		if !api.Respond(c, err) {
			s.internalError(c, "删除附件失败", err)
		}
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.internalError(c, "提交附件处置事务失败", err)
		return
	}
	c.Status(http.StatusNoContent)
}

// optionalIDString 把可空雪花 ID 序列化为字符串；空值输出 null。
func optionalIDString(value *int64) *string {
	if value == nil {
		return nil
	}
	formatted := api.FormatID(*value)
	return &formatted
}
