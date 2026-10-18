package domain

import (
	"time"

	"github.com/great-magician-01/timeline/internal/shared/model"
)

// SessionTTL 是服务端会话有效期（30 天）；登录与测试共用该常量。
const SessionTTL = 30 * 24 * time.Hour

// SessionState 表示一次请求的会话解析结果。
// 解析失败不直接决定 HTTP 状态：ResolveSession 记录原因，RequireLogin 再按方法
// 与原因映射为 401/403（停用）/500（内部错误），与旧中间件语义一致。
type SessionState string

const (
	// SessionMissing 表示请求未携带会话 Cookie。
	SessionMissing SessionState = "missing"
	// SessionNotFound 表示 Cookie 存在但服务端无对应会话。
	SessionNotFound SessionState = "not_found"
	// SessionRevoked 表示会话已被吊销（登出/停用/改密）。
	SessionRevoked SessionState = "revoked"
	// SessionExpired 表示会话已过期。
	SessionExpired SessionState = "expired"
	// SessionVersionMismatch 表示 token_version 不匹配（密码/停用使会话失效）。
	SessionVersionMismatch SessionState = "version_mismatch"
	// SessionSuspended 表示账号已停用。
	SessionSuspended SessionState = "suspended"
	// SessionAIAccount 表示 AI 账号不能持有 Web 会话。
	SessionAIAccount SessionState = "ai_account"
	// SessionResolveError 表示会话解析遇到内部错误（数据库不可用等）。
	// 公开读仍按游客继续；登录路由由 RequireLogin 返回 500 internal_error，
	// 与旧 RequireAuth 的内部错误语义一致，避免把故障误报成“未登录”。
	SessionResolveError SessionState = "resolve_error"
	// SessionValid 表示会话有效。
	SessionValid SessionState = "valid"
)

// IsAuthenticated 表示该状态是否代表有效登录身份。
func (s SessionState) IsAuthenticated() bool { return s == SessionValid }

// ValidateSession 纯判定会话是否仍有效，并返回访问者身份。
//
// 判定顺序与旧 RequireAuth 保持一致：**先判断账号停用**，再判断会话自身状态。
// 原因：管理员停用账号时会在同一事务内吊销该账号全部会话，若先命中“已吊销”再
// 判断停用，写请求会从基线语义的 403 account_suspended 退化为 401 unauthenticated。
// 其余顺序：吊销 → 过期 → token_version 不匹配 → AI 身份。
func ValidateSession(user model.User, session model.Session, now time.Time) (model.Viewer, SessionState) {
	switch {
	case user.IsSuspended():
		return model.GuestViewer(), SessionSuspended
	case session.RevokedAt != nil:
		return model.GuestViewer(), SessionRevoked
	case !now.Before(session.ExpiresAt):
		return model.GuestViewer(), SessionExpired
	case session.TokenVersion != user.TokenVersion:
		return model.GuestViewer(), SessionVersionMismatch
	case user.IsAI:
		return model.GuestViewer(), SessionAIAccount
	default:
		return model.ViewerFrom(user), SessionValid
	}
}
