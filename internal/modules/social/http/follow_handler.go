package http

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/shared/view"
	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// ListFollowers 列出目标用户的粉丝（游客可读，游标按关注行 ID 倒序）。
func (h *Handler) ListFollowers(c *gin.Context) {
	limit, cursor, ok := listParams(c)
	if !ok {
		return
	}
	page, err := h.svc.ListFollowers(c.Request.Context(), c.Param("username"), cursor, limit)
	if err != nil {
		h.fail(c, "查询粉丝列表失败", err)
		return
	}
	items := make([]view.UserBriefResponse, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, view.UserBriefResponseFor(item.User))
	}
	c.JSON(http.StatusOK, httpx.ListResponse(items, page.NextCursor, page.HasMore))
}

// ListFollowing 列出目标用户的关注（游客可读，游标按关注行 ID 倒序）。
func (h *Handler) ListFollowing(c *gin.Context) {
	limit, cursor, ok := listParams(c)
	if !ok {
		return
	}
	page, err := h.svc.ListFollowing(c.Request.Context(), c.Param("username"), cursor, limit)
	if err != nil {
		h.fail(c, "查询关注列表失败", err)
		return
	}
	items := make([]view.UserBriefResponse, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, view.UserBriefResponseFor(item.User))
	}
	c.JSON(http.StatusOK, httpx.ListResponse(items, page.NextCursor, page.HasMore))
}

// Follow 关注目标用户（对方开启审批时创建申请）。
func (h *Handler) Follow(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	state, err := h.svc.Follow(c.Request.Context(), viewer, c.Param("username"))
	if err != nil {
		h.fail(c, "关注失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"follow_state": state})
}

// Unfollow 取关或取消自己发出的待审批申请。
func (h *Handler) Unfollow(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	state, err := h.svc.Unfollow(c.Request.Context(), viewer, c.Param("username"))
	if err != nil {
		h.fail(c, "取关失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"follow_state": state})
}

// RemoveFollower 移除自己的粉丝。
func (h *Handler) RemoveFollower(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	state, err := h.svc.RemoveFollower(c.Request.Context(), viewer, c.Param("username"))
	if err != nil {
		h.fail(c, "移除粉丝失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"follow_state": state})
}
