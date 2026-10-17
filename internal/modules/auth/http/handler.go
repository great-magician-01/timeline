// Package http 是 auth 模块的 Gin 传输层：认证接口、资料接口、角色管理接口与
// 会话/角色/CSRF 中间件。本层只做参数解析、Cookie 读写、DTO 组装与错误映射；
// 业务规则全部在 app/domain，错误统一经 httpx.Respond 映射为 JSON 响应。
package http

import (
	"errors"
	"log/slog"
	stdhttp "net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/modules/auth/app"
	"github.com/great-magician-01/timeline/internal/modules/auth/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// Cookie 与请求头约定（与旧实现保持一致，前端依赖这些名字与作用域）。
const (
	// SessionCookieName 是服务端会话 Cookie 名。
	SessionCookieName = "timeline_session"
	// GuestCSRFCookieName 是游客双提交 CSRF Cookie 名。
	GuestCSRFCookieName = "timeline_csrf"
	// CSRFHeader 是 CSRF 令牌请求头名。
	CSRFHeader = "X-CSRF-Token"

	// guestCSRFCookiePath 限定游客 CSRF Cookie 只随 /api/v1/auth 前缀发送。
	guestCSRFCookiePath = "/api/v1/auth"
	// guestCSRFCookieMaxAge 是游客 CSRF Cookie 有效期（24 小时）。
	guestCSRFCookieMaxAge = 86400
)

// internalErrorMessage 是内部错误的统一对外文案，不暴露内部细节。
const internalErrorMessage = "服务暂时异常，请稍后重试"

// invalidJSONMessage 与旧实现的请求体解析错误文案一致。
const invalidJSONMessage = "请求体必须是有效的 JSON 对象，且不能包含未知字段"

// Handler 承载 auth 模块全部 HTTP 接口与中间件。
type Handler struct {
	svc *app.Service
	log *slog.Logger
}

// New 构造 auth HTTP handler；日志器取自 app.Service（为空时回退默认 logger）。
func New(service *app.Service) *Handler {
	var log *slog.Logger
	if service != nil {
		log = service.Logger()
	}
	if log == nil {
		log = slog.Default()
	}
	return &Handler{svc: service, log: log}
}

// fail 把领域错误写回响应；Internal Kind 与无法识别的错误记录日志后返回统一 500。
func (h *Handler) fail(c *gin.Context, message string, err error) {
	if err == nil {
		return
	}
	var appErr *apperr.Error
	if errors.As(err, &appErr) && appErr.Kind == apperr.KindInternal {
		h.log.Error(message, "err", err, "path", c.Request.URL.Path)
		httpx.Error(c, stdhttp.StatusInternalServerError, "internal_error", internalErrorMessage)
		return
	}
	if httpx.Respond(c, err) {
		return
	}
	h.log.Error(message, "err", err, "path", c.Request.URL.Path)
	httpx.Error(c, stdhttp.StatusInternalServerError, "internal_error", internalErrorMessage)
}

// decodeJSON 解析请求体；失败时写回 400 invalid_request（与旧实现文案一致）。
func (h *Handler) decodeJSON(c *gin.Context, dst any) bool {
	if err := httpx.DecodeJSON(c, dst); err != nil {
		httpx.Error(c, stdhttp.StatusBadRequest, "invalid_request", invalidJSONMessage)
		return false
	}
	return true
}

// clientIP 返回限频使用的客户端 IP（gin 已按 TRUSTED_PROXIES 解析）。
func clientIP(c *gin.Context) string { return c.ClientIP() }

// ===== Cookie 读写（app 层不写 Cookie） =====

func (h *Handler) setSessionCookie(c *gin.Context, token string, expires time.Time) {
	stdhttp.SetCookie(c.Writer, &stdhttp.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(domain.SessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   h.cookieSecure(),
		SameSite: stdhttp.SameSiteLaxMode,
	})
}

func (h *Handler) clearSessionCookie(c *gin.Context) {
	stdhttp.SetCookie(c.Writer, &stdhttp.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
		HttpOnly: true,
		Secure:   h.cookieSecure(),
		SameSite: stdhttp.SameSiteLaxMode,
	})
}

func (h *Handler) setGuestCSRFCookie(c *gin.Context, token string) {
	stdhttp.SetCookie(c.Writer, &stdhttp.Cookie{
		Name:     GuestCSRFCookieName,
		Value:    token,
		Path:     guestCSRFCookiePath,
		MaxAge:   guestCSRFCookieMaxAge,
		HttpOnly: true,
		Secure:   h.cookieSecure(),
		SameSite: stdhttp.SameSiteLaxMode,
	})
}

// cookieSecure 报告 Cookie 是否带 Secure 属性（nil 安全，便于中间件单测）。
func (h *Handler) cookieSecure() bool {
	if h.svc == nil {
		return false
	}
	return h.svc.CookieSecure()
}
