package http

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/richtext"
	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// ListComments 列出顶层评论（游客可读），每项带前两条回复预览。
func (h *Handler) ListComments(c *gin.Context) {
	postID, ok := idParam(c, "id")
	if !ok {
		return
	}
	limit, cursor, ok := listParams(c)
	if !ok {
		return
	}
	page, err := h.svc.ListComments(c.Request.Context(), currentViewer(c), postID, cursor, limit)
	if err != nil {
		h.fail(c, "查询评论列表失败", err)
		return
	}
	items := commentResponsesFor(page.Items)
	for i, item := range page.Items {
		if preview := commentResponsesFor(page.Previews[item.Row.ID]); len(preview) > 0 {
			items[i].ReplyPreview = preview
		}
	}
	c.JSON(http.StatusOK, httpx.ListResponse(items, page.NextCursor, page.HasMore))
}

// ListReplies 列出某条顶层评论下的全部回复（游客可读）。
func (h *Handler) ListReplies(c *gin.Context) {
	commentID, ok := idParam(c, "id")
	if !ok {
		return
	}
	limit, cursor, ok := listParams(c)
	if !ok {
		return
	}
	page, err := h.svc.ListReplies(c.Request.Context(), currentViewer(c), commentID, cursor, limit)
	if err != nil {
		h.fail(c, "查询回复列表失败", err)
		return
	}
	c.JSON(http.StatusOK, httpx.ListResponse(commentResponsesFor(page.Items), page.NextCursor, page.HasMore))
}

// createCommentRequest 是发表评论/回复的请求体。
type createCommentRequest struct {
	ContentFormat   string  `json:"content_format"`
	ContentSource   string  `json:"content_source"`
	ParentCommentID *string `json:"parent_comment_id"`
}

// CreateComment 发表评论或回复：正文由 richtext 渲染为安全 HTML 后入库。
func (h *Handler) CreateComment(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	postID, ok := idParam(c, "id")
	if !ok {
		return
	}
	var in createCommentRequest
	if err := httpx.DecodeJSON(c, &in); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "请求体必须是有效的 JSON 对象")
		return
	}
	// 与旧实现一致：格式校验先于父评论参数解析，错误带字段级 details。
	if !richtext.ValidFormat(in.ContentFormat) {
		httpx.Respond(c, apperr.Validation("评论内容格式无效").
			WithDetails(map[string]string{"content_format": "只支持 richtext 或 markdown"}))
		return
	}
	parentID, err := optionalIDFromBody(in.ParentCommentID)
	if err != nil {
		httpx.Respond(c, err)
		return
	}
	comment, err := h.svc.CreateComment(
		c.Request.Context(), viewer, postID, in.ContentFormat, in.ContentSource, parentID,
	)
	if err != nil {
		h.fail(c, "发表评论失败", err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"comment": commentResponseFor(comment)})
}

// DeleteComment 逻辑删除评论（评论作者、帖子作者或管理员）。
func (h *Handler) DeleteComment(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	commentID, ok := idParam(c, "id")
	if !ok {
		return
	}
	if err := h.svc.DeleteComment(c.Request.Context(), viewer, commentID); err != nil {
		h.fail(c, "删除评论失败", err)
		return
	}
	c.Status(http.StatusNoContent)
}

// optionalIDFromBody 解析请求体里的可选 ID：缺省、空串与空白视为未提供。
func optionalIDFromBody(raw *string) (*int64, error) {
	if raw == nil {
		return nil, nil
	}
	value := strings.TrimSpace(*raw)
	if value == "" {
		return nil, nil
	}
	id, err := httpx.ParseID(value)
	if err != nil {
		return nil, apperr.Validation("父评论参数无效").
			WithDetails(map[string]string{"parent_comment_id": "必须是有效的评论 ID"})
	}
	return &id, nil
}
