package http

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// ListConversations 处理 GET /api/v1/dm/conversations：会话列表（未隐藏且有消息）。
func (h *Handler) ListConversations(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	limit, cursor, ok := listParams(c)
	if !ok {
		return
	}
	page, err := h.svc.ConversationList(c.Request.Context(), viewer, cursor, limit)
	if err != nil {
		h.fail(c, "查询私信会话列表失败", err)
		return
	}
	items := make([]conversationResponse, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, conversationResponseFor(item))
	}
	c.JSON(http.StatusOK, httpx.ListResponse(items, page.NextCursor, page.HasMore))
}

// CreateConversation 处理 POST /api/v1/dm/conversations：创建或返回既有会话（幂等）。
func (h *Handler) CreateConversation(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	var in struct {
		Username string `json:"username"`
	}
	if err := httpx.DecodeJSON(c, &in); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "请求体必须是有效的 JSON 对象")
		return
	}
	username := strings.TrimSpace(in.Username)
	if username == "" {
		httpx.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", "用户名不能为空",
			map[string]string{"username": "该字段不能为空"})
		return
	}
	conv, err := h.svc.CreateOrGetConversation(c.Request.Context(), viewer, username)
	if err != nil {
		h.fail(c, "创建私信会话失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"conversation": conversationResponseFor(conv)})
}

// ConversationDetail 处理 GET /api/v1/dm/conversations/:id：会话详情。
func (h *Handler) ConversationDetail(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	conversationID, ok := idParam(c, "id")
	if !ok {
		return
	}
	conv, err := h.svc.ConversationDetail(c.Request.Context(), viewer, conversationID)
	if err != nil {
		h.fail(c, "读取私信会话失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"conversation": conversationResponseFor(conv)})
}

// MarkRead 处理 POST /api/v1/dm/conversations/:id/read：会话内自己的未读全部已读。
func (h *Handler) MarkRead(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	conversationID, ok := idParam(c, "id")
	if !ok {
		return
	}
	updated, err := h.svc.MarkRead(c.Request.Context(), viewer, conversationID)
	if err != nil {
		h.fail(c, "标记私信已读失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"updated": updated})
}

// HideConversation 处理 POST /api/v1/dm/conversations/:id/hide：删除自己的会话视图。
func (h *Handler) HideConversation(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	conversationID, ok := idParam(c, "id")
	if !ok {
		return
	}
	if err := h.svc.HideConversation(c.Request.Context(), viewer, conversationID); err != nil {
		h.fail(c, "删除私信会话失败", err)
		return
	}
	c.Status(http.StatusNoContent)
}

// UnreadCount 处理 GET /api/v1/dm/unread-count：私信未读总数（前端轮询）。
func (h *Handler) UnreadCount(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	unread, err := h.svc.UnreadCount(c.Request.Context(), viewer)
	if err != nil {
		h.fail(c, "查询私信未读数失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"unread_count": unread})
}

// ListMessages 处理 GET /api/v1/dm/conversations/:id/messages：
// cursor 翻更早消息；after_id 增量拉取新消息。
func (h *Handler) ListMessages(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	conversationID, ok := idParam(c, "id")
	if !ok {
		return
	}
	limit, cursor, ok := listParams(c)
	if !ok {
		return
	}
	afterID, ok := afterIDParam(c)
	if !ok {
		return
	}
	page, err := h.svc.MessageList(c.Request.Context(), viewer, conversationID, cursor, afterID, limit)
	if err != nil {
		h.fail(c, "查询私信消息失败", err)
		return
	}
	c.JSON(http.StatusOK, httpx.ListResponse(messageResponsesFor(page.Items), page.NextCursor, page.HasMore))
}

// SendMessage 处理 POST /api/v1/dm/conversations/:id/messages：发送纯文本私信。
func (h *Handler) SendMessage(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	conversationID, ok := idParam(c, "id")
	if !ok {
		return
	}
	var in struct {
		ContentFormat string `json:"content_format"`
		ContentSource string `json:"content_source"`
	}
	if err := httpx.DecodeJSON(c, &in); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "请求体必须是有效的 JSON 对象")
		return
	}
	message, err := h.svc.SendMessage(c.Request.Context(), viewer, conversationID, in.ContentFormat, in.ContentSource)
	if err != nil {
		h.fail(c, "发送私信失败", err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"message": messageResponseFor(message)})
}

// RecallMessage 处理 POST /api/v1/dm/messages/:id/recall：撤回自己的消息（时间窗内）。
func (h *Handler) RecallMessage(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	messageID, ok := idParam(c, "id")
	if !ok {
		return
	}
	message, err := h.svc.RecallMessage(c.Request.Context(), viewer, messageID)
	if err != nil {
		h.fail(c, "撤回私信失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": messageResponseFor(message)})
}
