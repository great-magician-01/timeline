package http

import (
	stdhttp "net/http"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/modules/auth/domain"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// sessionStateContextKey 保存 ResolveSession 的解析结果，供 RequireLogin 区分
// 「未登录」与「停用/失效会话」并映射为不同状态码。
const sessionStateContextKey = "timeline.auth.session_state"

// sessionUserContextKey 保存 ResolveSession 已读取的完整账号行，供 /auth/me 复用，
// 避免同一请求内再按 ID 读取一次 users。
const sessionUserContextKey = "timeline.auth.session_user"

func setSessionState(c *gin.Context, state domain.SessionState) {
	c.Set(sessionStateContextKey, state)
}

func sessionStateOf(c *gin.Context) (domain.SessionState, bool) {
	value, ok := c.Get(sessionStateContextKey)
	if !ok {
		return "", false
	}
	state, ok := value.(domain.SessionState)
	return state, ok
}

// setSessionUser 保存会话解析已读到的账号行（仅有效会话写入）。
func setSessionUser(c *gin.Context, user model.User) {
	c.Set(sessionUserContextKey, user)
}

// sessionUser 读取会话解析已读到的账号行；不存在时返回零值与 false。
func sessionUser(c *gin.Context) (model.User, bool) {
	value, ok := c.Get(sessionUserContextKey)
	if !ok {
		return model.User{}, false
	}
	user, ok := value.(model.User)
	return user, ok
}

// ResolveSession 在每个请求最多解析一次服务端会话，并把身份写入 httpx 上下文：
//   - 有效会话：注入 Viewer 与 Session；
//   - 会话不存在/已吊销/过期/token_version 不符/AI 账号：主动清除会话 Cookie 后按游客继续
//     （避免公开读反复查询失效会话）；
//   - 账号停用：按游客继续但保留 Cookie，由 RequireLogin 对 GET 返回 401、写请求 403；
//   - 解析内部错误：记录 SessionResolveError 后按游客继续，公开读不受影响；登录路由
//     由 RequireLogin 返回 500 internal_error（与旧 RequireAuth 语义一致）。
func (h *Handler) ResolveSession(c *gin.Context) {
	rawToken, err := c.Cookie(SessionCookieName)
	if err != nil || rawToken == "" {
		setSessionState(c, domain.SessionMissing)
		c.Next()
		return
	}
	result, resolveErr := h.svc.ResolveSession(c.Request.Context(), rawToken)
	if resolveErr != nil {
		h.log.Warn("解析登录会话失败，按游客继续", "err", resolveErr, "path", c.Request.URL.Path)
		setSessionState(c, domain.SessionResolveError)
		c.Next()
		return
	}
	setSessionState(c, result.State)
	if result.State.IsAuthenticated() {
		httpx.SetViewer(c, result.Viewer)
		httpx.SetSession(c, result.Session)
		// 复用 JOIN 已取回的完整账号行，供 /auth/me 等处理器使用。
		setSessionUser(c, result.User)
		c.Next()
		return
	}
	if result.State != domain.SessionSuspended {
		h.clearSessionCookie(c)
	}
	c.Next()
}

// RequireLogin 要求有效登录身份：未登录/失效返回 401，停用账号 GET/HEAD 401、写请求 403，
// 会话解析内部错误返回 500 internal_error（不把故障误报为未登录）。
func (h *Handler) RequireLogin(c *gin.Context) {
	if viewer, ok := httpx.Viewer(c); ok && viewer.Authenticated {
		c.Next()
		return
	}
	state, _ := sessionStateOf(c)
	switch state {
	case domain.SessionResolveError:
		// 内部故障：保留 Cookie，恢复后同一会话仍可继续使用（旧 RequireAuth 语义）。
		httpx.Error(c, stdhttp.StatusInternalServerError, "internal_error", internalErrorMessage)
	case domain.SessionSuspended:
		// GET/HEAD 清 Cookie 并要求重新登录；写请求返回 account_suspended（与旧语义一致）。
		if isReadMethod(c.Request.Method) {
			h.clearSessionCookie(c)
			httpx.Error(c, stdhttp.StatusUnauthorized, "unauthenticated", "账号已停用，请重新登录")
			return
		}
		httpx.Error(c, stdhttp.StatusForbidden, "account_suspended", "账号已停用，不能执行此操作")
	case domain.SessionNotFound, domain.SessionRevoked, domain.SessionExpired,
		domain.SessionVersionMismatch, domain.SessionAIAccount:
		h.clearSessionCookie(c)
		httpx.Error(c, stdhttp.StatusUnauthorized, "unauthenticated", "会话已失效，请重新登录")
	default:
		httpx.Error(c, stdhttp.StatusUnauthorized, "unauthenticated", "请先登录")
	}
}

// RequireAdmin 要求当前账号是未停用的人类管理员。
func (h *Handler) RequireAdmin(c *gin.Context) {
	viewer, ok := httpx.Viewer(c)
	if !ok || !viewer.Authenticated {
		httpx.Error(c, stdhttp.StatusUnauthorized, "unauthenticated", "请先登录")
		return
	}
	if viewer.IsAI || !viewer.IsAdmin {
		httpx.Error(c, stdhttp.StatusForbidden, "forbidden", "需要管理员权限")
		return
	}
	c.Next()
}

// RequireSuperAdmin 要求当前账号是超级管理员（在 RequireAdmin 之后使用）。
func (h *Handler) RequireSuperAdmin(c *gin.Context) {
	viewer, ok := httpx.Viewer(c)
	if !ok || !viewer.Authenticated {
		httpx.Error(c, stdhttp.StatusUnauthorized, "unauthenticated", "请先登录")
		return
	}
	if viewer.IsAI || !viewer.IsAdmin || !viewer.IsSuperAdmin {
		httpx.Error(c, stdhttp.StatusForbidden, "forbidden", "需要超级管理员权限")
		return
	}
	c.Next()
}

// RequireCSRF 校验与当前服务端会话绑定的 CSRF 令牌（X-CSRF-Token 请求头）。
func (h *Handler) RequireCSRF(c *gin.Context) {
	session, ok := httpx.Session(c)
	if !ok || !domain.TokensEqual(c.GetHeader(CSRFHeader), session.CSRFToken) {
		httpx.Error(c, stdhttp.StatusForbidden, "csrf_failed", "CSRF 校验失败，请刷新页面后重试")
		return
	}
	c.Next()
}

// GuestCSRF 为登录与注册提供双提交校验，使未登录的非 GET 请求也受 CSRF 防护。
func (h *Handler) GuestCSRF(c *gin.Context) {
	cookie, err := c.Cookie(GuestCSRFCookieName)
	if err != nil || !domain.TokensEqual(c.GetHeader(CSRFHeader), cookie) {
		httpx.Error(c, stdhttp.StatusForbidden, "csrf_failed", "CSRF 校验失败，请刷新页面后重试")
		return
	}
	c.Next()
}

// isReadMethod 判断请求是否为只读方法（GET/HEAD）。
func isReadMethod(method string) bool {
	return method == stdhttp.MethodGet || method == stdhttp.MethodHead
}
