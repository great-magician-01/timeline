package http

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/modules/content/app"
)

// postBody 是创建/编辑帖子的请求体；ID 以字符串传递（雪花 ID 精度）。
type postBody struct {
	ContentFormat string   `json:"content_format"`
	ContentSource string   `json:"content_source"`
	Visibility    string   `json:"visibility"`
	AttachmentIDs []string `json:"attachment_ids"`
	QuotedPostID  *string  `json:"quoted_post_id"`
	Action        string   `json:"action"`
	ScheduledAt   *string  `json:"scheduled_at"`
	Revision      *int     `json:"revision"`
}

// scheduleBody 是设置定时发布的请求体。
type scheduleBody struct {
	ScheduledAt *string `json:"scheduled_at"`
}

// GetPost 返回帖子详情；不可见时按不存在处理（app 已返回 not_found）。
func (h *Handler) GetPost(c *gin.Context) {
	postID, ok := pathID(c, "id")
	if !ok {
		return
	}
	detail, err := h.svc.ViewPost(c.Request.Context(), currentViewer(c), postID)
	if err != nil {
		h.fail(c, "读取帖子详情失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"post": detailResponseFor(detail)})
}

// CreatePost 创建草稿、立即发布或设置定时发布。
func (h *Handler) CreatePost(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	var body postBody
	if !decodeBody(c, &body) {
		return
	}
	attachmentIDs, err := parseAttachments(body.AttachmentIDs)
	if err != nil {
		httpxRespond(c, err)
		return
	}
	quotedPostID, err := parseQuotedPostID(body.QuotedPostID)
	if err != nil {
		httpxRespond(c, err)
		return
	}
	detail, err := h.svc.CreatePost(c.Request.Context(), viewer, app.PostInput{
		ContentFormat: body.ContentFormat,
		ContentSource: body.ContentSource,
		Visibility:    body.Visibility,
		AttachmentIDs: attachmentIDs,
		QuotedPostID:  quotedPostID,
		Action:        body.Action,
		ScheduledAt:   body.ScheduledAt,
	})
	if err != nil {
		h.fail(c, "创建帖子失败", err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"post": detailResponseFor(detail)})
}

// UpdatePost 编辑帖子（草稿工作区或已发布内容追加版本）。
func (h *Handler) UpdatePost(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	postID, ok := pathID(c, "id")
	if !ok {
		return
	}
	var body postBody
	if !decodeBody(c, &body) {
		return
	}
	attachmentIDs, err := parseAttachments(body.AttachmentIDs)
	if err != nil {
		httpxRespond(c, err)
		return
	}
	detail, err := h.svc.UpdatePost(c.Request.Context(), viewer, postID, app.UpdatePostInput{
		ContentFormat: body.ContentFormat,
		ContentSource: body.ContentSource,
		Visibility:    body.Visibility,
		AttachmentIDs: attachmentIDs,
		ScheduledAt:   body.ScheduledAt,
		Revision:      body.Revision,
	})
	if err != nil {
		h.fail(c, "编辑帖子失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"post": detailResponseFor(detail)})
}

// PublishPost 立即发布草稿或定时帖。
func (h *Handler) PublishPost(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	postID, ok := pathID(c, "id")
	if !ok {
		return
	}
	detail, err := h.svc.PublishPost(c.Request.Context(), viewer, postID)
	if err != nil {
		h.fail(c, "发布帖子失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"post": detailResponseFor(detail)})
}

// SchedulePost 设置定时发布。
func (h *Handler) SchedulePost(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	postID, ok := pathID(c, "id")
	if !ok {
		return
	}
	var body scheduleBody
	if !decodeBody(c, &body) {
		return
	}
	detail, err := h.svc.SchedulePost(c.Request.Context(), viewer, postID, app.SchedulePostInput{
		ScheduledAt: body.ScheduledAt,
	})
	if err != nil {
		h.fail(c, "设置定时发布失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"post": detailResponseFor(detail)})
}

// CancelSchedule 取消定时发布。
func (h *Handler) CancelSchedule(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	postID, ok := pathID(c, "id")
	if !ok {
		return
	}
	detail, err := h.svc.CancelSchedule(c.Request.Context(), viewer, postID)
	if err != nil {
		h.fail(c, "取消定时发布失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"post": detailResponseFor(detail)})
}

// DeletePost 逻辑删除帖子。
func (h *Handler) DeletePost(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	postID, ok := pathID(c, "id")
	if !ok {
		return
	}
	if err := h.svc.DeletePost(c.Request.Context(), viewer, postID); err != nil {
		h.fail(c, "删除帖子失败", err)
		return
	}
	c.Status(http.StatusNoContent)
}
