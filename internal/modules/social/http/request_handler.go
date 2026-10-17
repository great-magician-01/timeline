package http

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/modules/social/app"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// ListFollowRequests 列出关注申请：direction=incoming（默认）为发给我的，
// outgoing 为我发出的；只包含待处理申请，按 ID 倒序。
func (h *Handler) ListFollowRequests(c *gin.Context) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	limit, cursor, ok := listParams(c)
	if !ok {
		return
	}
	page, err := h.svc.ListFollowRequests(c.Request.Context(), viewer, c.Query("direction"), cursor, limit)
	if err != nil {
		h.fail(c, "查询关注申请失败", err)
		return
	}
	items := make([]followRequestResponse, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, followRequestResponseFor(item))
	}
	c.JSON(http.StatusOK, httpx.ListResponse(items, page.NextCursor, page.HasMore))
}

// ApproveFollowRequest 批准发给自己的关注申请。
func (h *Handler) ApproveFollowRequest(c *gin.Context) {
	h.resolveFollowRequest(c, h.svc.ApproveFollowRequest)
}

// RejectFollowRequest 拒绝发给自己的关注申请。
func (h *Handler) RejectFollowRequest(c *gin.Context) {
	h.resolveFollowRequest(c, h.svc.RejectFollowRequest)
}

// CancelFollowRequest 取消自己发出的待审批申请。
func (h *Handler) CancelFollowRequest(c *gin.Context) {
	h.resolveFollowRequest(c, h.svc.CancelFollowRequest)
}

// requestAction 是关注申请处理用例的函数形状。
type requestAction func(ctx context.Context, viewer model.Viewer, requestID int64) (app.RequestResult, error)

func (h *Handler) resolveFollowRequest(c *gin.Context, action requestAction) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	requestID, ok := idParam(c, "id")
	if !ok {
		return
	}
	result, err := action(c.Request.Context(), viewer, requestID)
	if err != nil {
		h.fail(c, "处理关注申请失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": result.Status, "follow_state": result.FollowState})
}
