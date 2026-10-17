package http

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// httpxRespond 把领域错误直接写回响应（用于参数转换失败等不经过 app 的错误）。
func httpxRespond(c *gin.Context, err error) {
	httpx.Respond(c, err)
}

// ListVersions 列出帖子历史版本（ID 游标分页）。
func (h *Handler) ListVersions(c *gin.Context) {
	postID, ok := pathID(c, "id")
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
	page, err := h.svc.ListVersions(c.Request.Context(), currentViewer(c), postID, cursorID, limit)
	if err != nil {
		h.fail(c, "查询历史版本失败", err)
		return
	}
	c.JSON(http.StatusOK, httpx.ListResponse(versionItemResponsesFor(page.Items), page.NextID, page.HasMore))
}

// GetVersion 返回单个历史版本详情。
func (h *Handler) GetVersion(c *gin.Context) {
	postID, ok := pathID(c, "id")
	if !ok {
		return
	}
	versionID, ok := pathID(c, "version_id")
	if !ok {
		return
	}
	detail, err := h.svc.GetVersionDetail(c.Request.Context(), currentViewer(c), postID, versionID)
	if err != nil {
		h.fail(c, "读取历史版本失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"version": versionDetailResponseFor(detail)})
}

// RestoreVersion 恢复到历史版本。
func (h *Handler) RestoreVersion(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	postID, ok := pathID(c, "id")
	if !ok {
		return
	}
	versionID, ok := pathID(c, "version_id")
	if !ok {
		return
	}
	detail, err := h.svc.RestoreVersion(c.Request.Context(), viewer, postID, versionID)
	if err != nil {
		h.fail(c, "恢复历史版本失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"post": detailResponseFor(detail)})
}
