package http

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/modules/notice/app"
	"github.com/great-magician-01/timeline/internal/shared/format"
	"github.com/great-magician-01/timeline/internal/shared/view"
	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// NotificationResponse 是通知列表项。preview 在目标不可见或已删除时为 null。
type NotificationResponse struct {
	ID         string                 `json:"id"`
	Type       string                 `json:"type"`
	Actor      view.UserBriefResponse `json:"actor"`
	TargetType string                 `json:"target_type"`
	TargetID   string                 `json:"target_id"`
	Preview    *string                `json:"preview"`
	IsRead     bool                   `json:"is_read"`
	CreatedAt  string                 `json:"created_at"`
}

func notificationResponseFor(item app.Item) NotificationResponse {
	return NotificationResponse{
		ID:         format.FormatID(item.Notification.ID),
		Type:       item.Notification.Type,
		Actor:      view.UserBriefResponseFor(item.Actor),
		TargetType: item.Notification.TargetType,
		TargetID:   format.FormatID(item.Notification.TargetID),
		Preview:    item.Preview,
		IsRead:     item.Notification.IsRead,
		CreatedAt:  format.FormatTime(item.Notification.CreatedAt),
	}
}

// List 列出通知（支持 filter=all/unread 与 type 过滤，游标分页）。
func (h *Handler) List(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	limit, cursor, ok := listParams(c)
	if !ok {
		return
	}
	page, err := h.svc.List(c.Request.Context(), viewer, c.Query("filter"), c.Query("type"), cursor, limit)
	if err != nil {
		h.fail(c, "查询通知列表失败", err)
		return
	}
	items := make([]NotificationResponse, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, notificationResponseFor(item))
	}
	c.JSON(http.StatusOK, httpx.ListResponse(items, page.NextCursor, page.HasMore))
}

// UnreadCount 返回未读通知数。
func (h *Handler) UnreadCount(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	count, err := h.svc.UnreadCount(c.Request.Context(), viewer)
	if err != nil {
		h.fail(c, "统计未读通知失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"unread_count": count})
}

// MarkRead 标记单条通知已读。
func (h *Handler) MarkRead(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	if err := h.svc.MarkRead(c.Request.Context(), viewer, id); err != nil {
		h.fail(c, "标记通知已读失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": format.FormatID(id), "is_read": true})
}

// MarkAllRead 全部标记已读。
func (h *Handler) MarkAllRead(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	updated, err := h.svc.MarkAllRead(c.Request.Context(), viewer)
	if err != nil {
		h.fail(c, "全部标记已读失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"updated": updated})
}
