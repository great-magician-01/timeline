// Package http 提供内容域的 Gin handler：参数解析、错误映射与 DTO 组装。
// 业务规则全部在 domain/app；本层不写 SQL、不直接使用 pgx。
package http

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/modules/content/app"
	"github.com/great-magician-01/timeline/internal/modules/content/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/shared/page"
	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// Handler 承载内容域全部 HTTP 接口。
type Handler struct {
	svc *app.Service
	log *slog.Logger
}

// New 构造内容域 handler；logger 来自 app.Service.Log()。
func New(svc *app.Service) *Handler {
	log := svc.Log()
	if log == nil {
		log = slog.Default()
	}
	return &Handler{svc: svc, log: log}
}

// fail 把领域错误写回响应；无法识别的错误按内部错误处理（500 + 访问日志）。
func (h *Handler) fail(c *gin.Context, message string, err error) {
	if httpx.Respond(c, err) {
		return
	}
	h.log.Error(message, "err", err, "path", c.Request.URL.Path)
	httpx.Error(c, http.StatusInternalServerError, "internal_error", "服务暂时异常，请稍后重试")
}

// currentViewer 读取可选认证身份；未写入上下文时按游客处理。
func currentViewer(c *gin.Context) model.Viewer {
	viewer, ok := httpx.Viewer(c)
	if !ok {
		return model.GuestViewer()
	}
	return viewer
}

// requireViewer 要求登录；未登录时已写回 401。
func (h *Handler) requireViewer(c *gin.Context) (model.Viewer, bool) {
	return httpx.RequireViewer(c)
}

// pathID 解析路径参数中的雪花 ID；失败时写回 400。
func pathID(c *gin.Context, name string) (int64, bool) {
	id, err := httpx.ParseID(c.Param(name))
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "资源 ID 无效")
		return 0, false
	}
	return id, true
}

// pageLimit 解析通用 limit（默认 20，取值 1-50）。
func pageLimit(c *gin.Context) (int, bool) {
	limit, err := page.ParseLimit(c.Query("limit"))
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return 0, false
	}
	return limit, true
}

// timeCursor 解析时间+ID 游标；空表示首页。
func timeCursor(c *gin.Context) (page.TimeCursor, bool) {
	cursor, err := page.DecodeTimeCursor(c.Query("cursor"))
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "无效的游标")
		return page.TimeCursor{}, false
	}
	return cursor, true
}

// idCursor 解析纯 ID 游标；空表示首页。
func idCursor(c *gin.Context) (int64, bool) {
	cursor, err := page.DecodeIDCursor(c.Query("cursor"))
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "无效的游标")
		return 0, false
	}
	return cursor.ID, true
}

// decodeBody 解析 JSON 请求体；失败时写回统一 400。
func decodeBody(c *gin.Context, dst any) bool {
	if err := httpx.DecodeJSON(c, dst); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request",
			"请求体必须是有效的 JSON 对象，且不能包含未知字段")
		return false
	}
	return true
}

// timeListResponse 组装时间游标列表响应；没有下一页时一律不返回游标。
func timeListResponse(items any, next page.TimeCursor, hasMore bool) gin.H {
	var nextCursor any
	if hasMore && next.Valid() {
		nextCursor = page.EncodeTimeCursor(next.Time, next.ID)
	} else {
		hasMore = false
	}
	return gin.H{"items": items, "next_cursor": nextCursor, "has_more": hasMore}
}

// invalidField 生成带字段详情的 validation 错误（与旧实现细节形状一致）。
func invalidField(field, message string) error {
	return apperr.Validation(message).WithDetails(map[string]string{field: message})
}

// parseAttachments 把 JSON 中的字符串附件 ID 转为 int64 列表。
func parseAttachments(raw []string) ([]int64, error) {
	ids, err := domain.ParseAttachmentIDs(raw)
	if err != nil {
		return nil, invalidField("attachment_ids", "附件 ID 无效")
	}
	return ids, nil
}

// parseQuotedPostID 把 JSON 中的字符串引帖 ID 转为 *int64。
func parseQuotedPostID(raw *string) (*int64, error) {
	id, err := domain.ParseQuotedPostID(raw)
	if err != nil {
		return nil, invalidField("quoted_post_id", "引用的帖子 ID 无效")
	}
	return id, nil
}
