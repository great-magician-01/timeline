package http

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// ListMyDrafts 返回当前用户的草稿列表（ID 游标分页）。
func (h *Handler) ListMyDrafts(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	cursorID, ok := idCursor(c)
	if !ok {
		return
	}
	limit, ok := pageLimit(c)
	if !ok {
		return
	}
	page, err := h.svc.ListMyDrafts(c.Request.Context(), viewer, cursorID, limit)
	if err != nil {
		h.fail(c, "查询草稿列表失败", err)
		return
	}
	c.JSON(http.StatusOK, httpx.ListResponse(draftItemResponsesFor(page.Items), page.NextID, page.HasMore))
}

// ListMyScheduled 返回当前用户的定时发布列表（ID 游标分页）。
func (h *Handler) ListMyScheduled(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	cursorID, ok := idCursor(c)
	if !ok {
		return
	}
	limit, ok := pageLimit(c)
	if !ok {
		return
	}
	page, err := h.svc.ListMyScheduled(c.Request.Context(), viewer, cursorID, limit)
	if err != nil {
		h.fail(c, "查询定时发布列表失败", err)
		return
	}
	c.JSON(http.StatusOK, httpx.ListResponse(draftItemResponsesFor(page.Items), page.NextID, page.HasMore))
}
