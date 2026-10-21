package http

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/modules/content/app"
	"github.com/great-magician-01/timeline/internal/shared/format"
	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// ListBookmarks 返回当前用户的收藏列表（GET /me/bookmarks，ID 游标分页）。
// folder_id 缺省返回全部；"default" 只看未分组；其余必须是有效的收藏夹 ID。
func (h *Handler) ListBookmarks(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	scope, ok := bookmarkScopeParam(c)
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
	page, err := h.svc.ListBookmarks(c.Request.Context(), viewer, scope, cursorID, limit)
	if err != nil {
		h.fail(c, "查询收藏列表失败", err)
		return
	}
	c.JSON(http.StatusOK, httpx.ListResponse(
		bookmarkItemResponsesFor(page.Items), page.NextID, page.HasMore,
	))
}

// bookmarkScopeParam 解析收藏夹过滤参数；非法 ID 写回 400。
func bookmarkScopeParam(c *gin.Context) (app.BookmarkScope, bool) {
	switch raw := c.Query("folder_id"); raw {
	case "":
		return app.BookmarkScope{All: true}, true
	case "default":
		return app.BookmarkScope{DefaultOnly: true}, true
	default:
		folderID, err := httpx.ParseID(raw)
		if err != nil {
			httpx.Error(c, http.StatusBadRequest, "invalid_request", "folder_id 必须是有效的收藏夹 ID")
			return app.BookmarkScope{}, false
		}
		return app.BookmarkScope{FolderID: folderID}, true
	}
}

// bookmarkItemResponsesFor 批量映射收藏列表项，返回空切片而不是 nil（JSON 输出 []）。
func bookmarkItemResponsesFor(items []app.BookmarkListItem) []bookmarkItemResponse {
	out := make([]bookmarkItemResponse, 0, len(items))
	for _, item := range items {
		resp := bookmarkItemResponse{
			postBriefResponse: briefResponseFor(item.PostBrief),
			Bookmark: bookmarkStateResponse{
				FolderID:  formatIDPtr(item.Bookmark.FolderID),
				CreatedAt: format.FormatTime(item.Bookmark.CreatedAt),
			},
		}
		out = append(out, resp)
	}
	return out
}
