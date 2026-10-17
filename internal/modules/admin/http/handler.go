// Package http 是 admin 编排模块的 Gin 适配层：只做参数解析、用例调用、错误映射与
// DTO 组装；不含 SQL、pgx 与事务原语。
package http

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/modules/admin/app"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// Handler 是 admin 管理接口的 Gin 处理器集合。
type Handler struct {
	service *app.Service
	log     *slog.Logger
}

// New 构造 admin handler；日志器复用 app.Service 持有的实例。
func New(service *app.Service) *Handler {
	return &Handler{service: service, log: service.Logger()}
}

// fail 把错误映射为统一 JSON 响应；无法识别的错误按 500 记录日志。
func (h *Handler) fail(c *gin.Context, message string, err error) {
	if !httpx.Respond(c, err) {
		h.log.Error(message, "err", err, "path", c.Request.URL.Path)
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "服务暂时异常，请稍后重试")
	}
}

// pageParams 解析通用分页参数（limit 1-50、ID 游标）；失败时已写回 400。
func (h *Handler) pageParams(c *gin.Context) (limit int, cursor int64, ok bool) {
	limit, err := httpx.PageLimit(c)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return 0, 0, false
	}
	cursor, err = httpx.PageCursor(c)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return 0, 0, false
	}
	return limit, cursor, true
}

// pathID 解析路径参数 :id；失败时已写回 400。
func (h *Handler) pathID(c *gin.Context, param string) (int64, bool) {
	id, err := httpx.ParseID(c.Param(param))
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return 0, false
	}
	return id, true
}

// decodeJSON 解析请求体；失败时已写回 400（与旧管理接口文案一致）。
func (h *Handler) decodeJSON(c *gin.Context, dst any) bool {
	if err := httpx.DecodeJSON(c, dst); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "请求体必须是有效的 JSON 对象")
		return false
	}
	return true
}

// actor 读取已登录访问者（路由策略已保证管理员身份）。
func (h *Handler) actor(c *gin.Context) (model.Viewer, bool) {
	return httpx.RequireViewer(c)
}

// noContent 返回 204。
func noContent(c *gin.Context) { c.Status(http.StatusNoContent) }

// listResponse 组装统一列表响应。
func listResponse[T any](page app.Page[T], mapItem func(T) any) gin.H {
	items := make([]any, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, mapItem(item))
	}
	return httpx.ListResponse(items, page.LastID, page.HasMore)
}
