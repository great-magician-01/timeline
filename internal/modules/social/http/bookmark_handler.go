package http

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/shared/format"
	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// bookmarkBody 是收藏请求的可选体：folder_id 缺省/null/空串表示收藏到默认收藏。
type bookmarkBody struct {
	FolderID *string `json:"folder_id"`
}

// BookmarkPost 收藏帖子（POST /posts/:id/bookmark）。
func (h *Handler) BookmarkPost(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	postID, ok := idParam(c, "id")
	if !ok {
		return
	}
	var body bookmarkBody
	if c.Request.ContentLength > 0 {
		if err := httpx.DecodeJSON(c, &body); err != nil {
			httpx.Error(c, http.StatusBadRequest, "invalid_request", "请求体必须是有效的 JSON 对象")
			return
		}
	}
	folderID, ok := parseFolderIDParam(c, body.FolderID)
	if !ok {
		return
	}
	result, err := h.svc.BookmarkPost(c.Request.Context(), viewer, postID, folderID)
	if err != nil {
		h.fail(c, "收藏帖子失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"bookmarked": result.Bookmarked, "folder_id": formatIDPtr(result.FolderID)})
}

// UnbookmarkPost 取消收藏帖子（POST /posts/:id/unbookmark）。
func (h *Handler) UnbookmarkPost(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	postID, ok := idParam(c, "id")
	if !ok {
		return
	}
	result, err := h.svc.UnbookmarkPost(c.Request.Context(), viewer, postID)
	if err != nil {
		h.fail(c, "取消收藏失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"bookmarked": result.Bookmarked})
}

// parseFolderIDParam 把可空的 folder_id 字符串解析为收藏夹 ID；
// 缺省/空串返回 (nil, true)；非法 ID 写回 400。
func parseFolderIDParam(c *gin.Context, raw *string) (*int64, bool) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil, true
	}
	id, err := httpx.ParseID(strings.TrimSpace(*raw))
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "folder_id 必须是有效的收藏夹 ID")
		return nil, false
	}
	return &id, true
}

// formatIDPtr 输出可空雪花 ID 字符串（JSON 中以字符串传输，避免 JS 精度丢失）。
func formatIDPtr(id *int64) *string {
	if id == nil {
		return nil
	}
	formatted := format.FormatID(*id)
	return &formatted
}
