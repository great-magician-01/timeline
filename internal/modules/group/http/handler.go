// Package http 提供群聊域的 Gin handler：参数解析、错误映射与 DTO 组装。
// 业务规则全部在 app/domain；本层不写 SQL、不直接使用 pgx。
package http

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/modules/group/app"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// Handler 承载群聊域全部 HTTP 接口。
type Handler struct {
	svc *app.Service
	log *slog.Logger
}

// New 构造群聊 handler；nil logger 回退默认 logger。
func New(svc *app.Service, log *slog.Logger) *Handler {
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

// requireViewer 要求登录；未登录时已写回 401。
func (h *Handler) requireViewer(c *gin.Context) (model.Viewer, bool) {
	return httpx.RequireViewer(c)
}

// idParam 解析路径参数中的雪花 ID；失败时写回 400。
func idParam(c *gin.Context, name string) (int64, bool) {
	id, err := httpx.ParseID(c.Param(name))
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return 0, false
	}
	return id, true
}

// listParams 解析游标分页参数（limit 1-50，cursor 可空）；失败时写回 400。
func listParams(c *gin.Context) (limit int, cursor int64, ok bool) {
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

// afterIDParam 解析增量拉取参数 after_id（可空，空表示不使用增量模式）。
func afterIDParam(c *gin.Context) (int64, bool) {
	raw := strings.TrimSpace(c.Query("after_id"))
	if raw == "" {
		return 0, true
	}
	id, err := httpx.ParseID(raw)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return 0, false
	}
	return id, true
}

// memberCursorParam 解析成员分页游标（joinedAt RFC3339 纳秒 + ":" + id）；
// 空值表示首页。
func memberCursorParam(c *gin.Context) (time.Time, int64, bool) {
	raw := strings.TrimSpace(c.Query("cursor"))
	if raw == "" {
		return time.Time{}, 0, true
	}
	sep := strings.LastIndex(raw, ":")
	if sep <= 0 || sep == len(raw)-1 {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "成员分页游标无效")
		return time.Time{}, 0, false
	}
	joinedAt, err := time.Parse(time.RFC3339Nano, raw[:sep])
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "成员分页游标无效")
		return time.Time{}, 0, false
	}
	id, err := httpx.ParseID(raw[sep+1:])
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "成员分页游标无效")
		return time.Time{}, 0, false
	}
	return joinedAt, id, true
}

// usernamesInput 解析并清洗 username 列表输入（每项去首尾空白，去空项）。
func usernamesInput(raws []string) []string {
	out := make([]string, 0, len(raws))
	seen := make(map[string]struct{}, len(raws))
	for _, raw := range raws {
		username := strings.TrimSpace(raw)
		if username == "" {
			continue
		}
		if _, ok := seen[username]; ok {
			continue
		}
		seen[username] = struct{}{}
		out = append(out, username)
	}
	return out
}
