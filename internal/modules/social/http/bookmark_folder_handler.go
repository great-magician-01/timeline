package http

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/modules/social/app"
	"github.com/great-magician-01/timeline/internal/shared/format"
	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// BookmarkFolderResponse 是收藏夹的对外 DTO（docs/API.md §8）。
type BookmarkFolderResponse struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	BookmarkCount int    `json:"bookmark_count"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

// bookmarkFolderResponseFor 组装单条收藏夹 DTO。
func bookmarkFolderResponseFor(item app.BookmarkFolder) BookmarkFolderResponse {
	return BookmarkFolderResponse{
		ID:            format.FormatID(item.ID),
		Name:          item.Name,
		BookmarkCount: item.BookmarkCount,
		CreatedAt:     format.FormatTime(item.CreatedAt),
		UpdatedAt:     format.FormatTime(item.UpdatedAt),
	}
}

// ListBookmarkFolders 返回当前用户的收藏夹列表（GET /me/bookmark-folders）。
func (h *Handler) ListBookmarkFolders(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	items, err := h.svc.ListBookmarkFolders(c.Request.Context(), viewer)
	if err != nil {
		h.fail(c, "读取收藏夹失败", err)
		return
	}
	responses := make([]BookmarkFolderResponse, 0, len(items))
	for _, item := range items {
		responses = append(responses, bookmarkFolderResponseFor(item))
	}
	c.JSON(http.StatusOK, gin.H{"items": responses})
}

// folderNameBody 是创建/重命名收藏夹的请求体。
type folderNameBody struct {
	Name string `json:"name"`
}

// CreateBookmarkFolder 创建收藏夹（POST /me/bookmark-folders）。
func (h *Handler) CreateBookmarkFolder(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	var body folderNameBody
	if err := httpx.DecodeJSON(c, &body); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "请求体必须是有效的 JSON 对象")
		return
	}
	item, err := h.svc.CreateBookmarkFolder(c.Request.Context(), viewer, body.Name)
	if err != nil {
		h.fail(c, "创建收藏夹失败", err)
		return
	}
	c.JSON(http.StatusCreated, bookmarkFolderResponseFor(item))
}

// RenameBookmarkFolder 重命名收藏夹（POST /me/bookmark-folders/:id）。
func (h *Handler) RenameBookmarkFolder(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	folderID, ok := idParam(c, "id")
	if !ok {
		return
	}
	var body folderNameBody
	if err := httpx.DecodeJSON(c, &body); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "请求体必须是有效的 JSON 对象")
		return
	}
	item, err := h.svc.RenameBookmarkFolder(c.Request.Context(), viewer, folderID, body.Name)
	if err != nil {
		h.fail(c, "重命名收藏夹失败", err)
		return
	}
	c.JSON(http.StatusOK, bookmarkFolderResponseFor(item))
}

// DeleteBookmarkFolder 删除收藏夹（POST /me/bookmark-folders/:id/delete）；
// 夹内生效收藏自动归还默认收藏。
func (h *Handler) DeleteBookmarkFolder(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	folderID, ok := idParam(c, "id")
	if !ok {
		return
	}
	if err := h.svc.DeleteBookmarkFolder(c.Request.Context(), viewer, folderID); err != nil {
		h.fail(c, "删除收藏夹失败", err)
		return
	}
	c.Status(http.StatusNoContent)
}
