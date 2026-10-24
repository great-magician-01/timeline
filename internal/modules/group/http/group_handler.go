package http

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// ListGroups 处理 GET /api/v1/groups：我的群列表（有效成员、未隐藏、有消息）。
func (h *Handler) ListGroups(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	limit, cursor, ok := listParams(c)
	if !ok {
		return
	}
	page, err := h.svc.GroupList(c.Request.Context(), viewer, cursor, limit)
	if err != nil {
		h.fail(c, "查询群聊列表失败", err)
		return
	}
	c.JSON(http.StatusOK, httpx.ListResponse(groupResponsesFor(page.Items), page.NextCursor, page.HasMore))
}

// CreateGroup 处理 POST /api/v1/groups：建群（创建者即群主，可同时拉初始成员）。
func (h *Handler) CreateGroup(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	var in struct {
		Name      string   `json:"name"`
		Usernames []string `json:"usernames"`
	}
	if err := httpx.DecodeJSON(c, &in); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "请求体必须是有效的 JSON 对象")
		return
	}
	group, err := h.svc.CreateGroup(c.Request.Context(), viewer, in.Name, usernamesInput(in.Usernames))
	if err != nil {
		h.fail(c, "创建群聊失败", err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"group": groupResponseFor(group)})
}

// GroupDetail 处理 GET /api/v1/groups/:id：群详情。
func (h *Handler) GroupDetail(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	groupID, ok := idParam(c, "id")
	if !ok {
		return
	}
	group, err := h.svc.GroupDetail(c.Request.Context(), viewer, groupID)
	if err != nil {
		h.fail(c, "读取群聊失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"group": groupResponseFor(group)})
}

// GroupMembers 处理 GET /api/v1/groups/:id/members：成员分页（按加入时间正序）。
func (h *Handler) GroupMembers(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	groupID, ok := idParam(c, "id")
	if !ok {
		return
	}
	limit, _, ok := listParams(c)
	if !ok {
		return
	}
	cursorJoinedAt, cursorID, ok := memberCursorParam(c)
	if !ok {
		return
	}
	page, err := h.svc.GroupMembers(c.Request.Context(), viewer, groupID, cursorJoinedAt, cursorID, limit)
	if err != nil {
		h.fail(c, "查询群成员失败", err)
		return
	}
	// 成员游标是字符串（joinedAt+id），与统一 int64 游标形状保持一致手动组装。
	var nextCursor any
	if page.HasMore && page.NextCursor != "" {
		nextCursor = page.NextCursor
	}
	c.JSON(http.StatusOK, gin.H{
		"items":       memberResponsesFor(page.Items),
		"next_cursor": nextCursor,
		"has_more":    page.HasMore,
	})
}

// ListMessages 处理 GET /api/v1/groups/:id/messages：
// cursor 翻更早消息；after_id 增量拉取新消息。
func (h *Handler) ListMessages(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	groupID, ok := idParam(c, "id")
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
	page, err := h.svc.MessageList(c.Request.Context(), viewer, groupID, cursor, afterID, limit)
	if err != nil {
		h.fail(c, "查询群消息失败", err)
		return
	}
	c.JSON(http.StatusOK, httpx.ListResponse(messageResponsesFor(page.Items), page.NextCursor, page.HasMore))
}

// SendMessage 处理 POST /api/v1/groups/:id/messages：发送纯文本群消息。
func (h *Handler) SendMessage(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	groupID, ok := idParam(c, "id")
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
	message, err := h.svc.SendMessage(c.Request.Context(), viewer, groupID, in.ContentFormat, in.ContentSource)
	if err != nil {
		h.fail(c, "发送群消息失败", err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"message": messageResponseFor(message)})
}

// RecallMessage 处理 POST /api/v1/groups/messages/:id/recall：撤回自己的消息（时间窗内）。
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
		h.fail(c, "撤回群消息失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": messageResponseFor(message)})
}

// MarkRead 处理 POST /api/v1/groups/:id/read：把已读游标推进到群最后消息（幂等）。
func (h *Handler) MarkRead(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	groupID, ok := idParam(c, "id")
	if !ok {
		return
	}
	changed, err := h.svc.MarkRead(c.Request.Context(), viewer, groupID)
	if err != nil {
		h.fail(c, "标记群消息已读失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"updated": map[bool]int{true: 1, false: 0}[changed]})
}

// HideGroup 处理 POST /api/v1/groups/:id/hide：隐藏自己的群会话视图（新消息后重现）。
func (h *Handler) HideGroup(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	groupID, ok := idParam(c, "id")
	if !ok {
		return
	}
	if err := h.svc.HideGroup(c.Request.Context(), viewer, groupID); err != nil {
		h.fail(c, "隐藏群聊失败", err)
		return
	}
	c.Status(http.StatusNoContent)
}

// RenameGroup 处理 POST /api/v1/groups/:id/rename：群主改名。
func (h *Handler) RenameGroup(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	groupID, ok := idParam(c, "id")
	if !ok {
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := httpx.DecodeJSON(c, &in); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "请求体必须是有效的 JSON 对象")
		return
	}
	group, err := h.svc.Rename(c.Request.Context(), viewer, groupID, in.Name)
	if err != nil {
		h.fail(c, "群改名失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"group": groupResponseFor(group)})
}

// Invite 处理 POST /api/v1/groups/:id/invite：群主拉人（1–20 人，幂等）。
func (h *Handler) Invite(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	groupID, ok := idParam(c, "id")
	if !ok {
		return
	}
	var in struct {
		Usernames []string `json:"usernames"`
	}
	if err := httpx.DecodeJSON(c, &in); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "请求体必须是有效的 JSON 对象")
		return
	}
	members, err := h.svc.Invite(c.Request.Context(), viewer, groupID, usernamesInput(in.Usernames))
	if err != nil {
		h.fail(c, "拉人失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"members": memberResponsesFor(members)})
}

// RemoveMember 处理 POST /api/v1/groups/:id/members/:uid/remove：群主踢人。
func (h *Handler) RemoveMember(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	groupID, ok := idParam(c, "id")
	if !ok {
		return
	}
	targetUserID, ok := idParam(c, "uid")
	if !ok {
		return
	}
	if err := h.svc.RemoveMember(c.Request.Context(), viewer, groupID, targetUserID); err != nil {
		h.fail(c, "移除成员失败", err)
		return
	}
	c.Status(http.StatusNoContent)
}

// TransferOwner 处理 POST /api/v1/groups/:id/transfer：群主转让。
func (h *Handler) TransferOwner(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	groupID, ok := idParam(c, "id")
	if !ok {
		return
	}
	var in struct {
		UserID string `json:"user_id"`
	}
	if err := httpx.DecodeJSON(c, &in); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "请求体必须是有效的 JSON 对象")
		return
	}
	targetUserID, err := httpx.ParseID(in.UserID)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "user_id 无效")
		return
	}
	if err := h.svc.TransferOwner(c.Request.Context(), viewer, groupID, targetUserID); err != nil {
		h.fail(c, "转让群主失败", err)
		return
	}
	c.Status(http.StatusNoContent)
}

// LeaveGroup 处理 POST /api/v1/groups/:id/leave：成员退出（群主须先转让或解散）。
func (h *Handler) LeaveGroup(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	groupID, ok := idParam(c, "id")
	if !ok {
		return
	}
	if err := h.svc.Leave(c.Request.Context(), viewer, groupID); err != nil {
		h.fail(c, "退出群聊失败", err)
		return
	}
	c.Status(http.StatusNoContent)
}

// DissolveGroup 处理 POST /api/v1/groups/:id/dissolve：群主解散（只读保留）。
func (h *Handler) DissolveGroup(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	groupID, ok := idParam(c, "id")
	if !ok {
		return
	}
	if err := h.svc.Dissolve(c.Request.Context(), viewer, groupID); err != nil {
		h.fail(c, "解散群聊失败", err)
		return
	}
	c.Status(http.StatusNoContent)
}

// UnreadCount 处理 GET /api/v1/groups/unread-count：群聊未读总数（前端轮询）。
func (h *Handler) UnreadCount(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	unread, err := h.svc.UnreadCount(c.Request.Context(), viewer)
	if err != nil {
		h.fail(c, "查询群聊未读数失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"unread_count": unread})
}
