package auth

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/great-magician-01/timeline/internal/api"
)

// RequireAuth 校验服务端会话、账号状态与 token_version，并将当前身份注入请求上下文。
func (s *Service) RequireAuth(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	cookie, err := c.Cookie(sessionCookieName)
	if err != nil || cookie == "" {
		api.Error(c, http.StatusUnauthorized, "unauthenticated", "请先登录")
		return
	}

	var session Session
	var u User
	err = pool.QueryRow(c.Request.Context(), `
		SELECT s.id, s.user_id, s.csrf_token, s.token_version, s.expires_at, s.revoked_at,
		       u.id, u.username, COALESCE(u.email, ''), COALESCE(u.password_hash, ''),
		       u.nickname, u.avatar_url, u.bio, u.role, u.is_super_admin, u.is_ai, u.status,
		       u.require_follow_approval, u.token_version, u.suspended_at, u.created_at, u.updated_at
		FROM auth_sessions AS s
		JOIN users AS u ON u.id = s.user_id
		WHERE s.token_hash = $1`, hashSessionToken(cookie)).Scan(
		&session.ID, &session.UserID, &session.CSRFToken, &session.TokenVersion,
		&session.ExpiresAt, &session.RevokedAt,
		&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.Nickname, &u.AvatarURL,
		&u.Bio, &u.Role, &u.IsSuperAdmin, &u.IsAI, &u.Status, &u.RequireFollowApproval,
		&u.TokenVersion, &u.SuspendedAt, &u.CreatedAt, &u.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		s.clearSessionCookie(c)
		api.Error(c, http.StatusUnauthorized, "unauthenticated", "会话已失效，请重新登录")
		return
	}
	if err != nil {
		s.internalError(c, "读取登录会话失败", err)
		return
	}
	if u.IsSuspended() {
		if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead {
			s.clearSessionCookie(c)
			api.Error(c, http.StatusUnauthorized, "unauthenticated", "账号已停用，请重新登录")
		} else {
			api.Error(c, http.StatusForbidden, "account_suspended", "账号已停用，不能执行此操作")
		}
		return
	}
	if session.RevokedAt != nil || !time.Now().Before(session.ExpiresAt) || session.TokenVersion != u.TokenVersion || u.IsAI {
		s.clearSessionCookie(c)
		api.Error(c, http.StatusUnauthorized, "unauthenticated", "会话已失效，请重新登录")
		return
	}

	c.Set(userContextKey, u)
	c.Set(sessionContextKey, session)
	c.Next()
}

// OptionalAuth 在存在有效会话时注入当前身份，不存在或已失效时按游客继续。
// 公开接口（时间线、帖子、评论、标签、搜索、个人主页）靠它获得 viewer_state
// 与 follow_state，同时绝不因为缺少会话而拒绝请求。
func (s *Service) OptionalAuth(c *gin.Context) {
	pool := s.pools.Pool()
	if pool == nil {
		c.Next()
		return
	}
	cookie, err := c.Cookie(sessionCookieName)
	if err != nil || cookie == "" {
		c.Next()
		return
	}

	var session Session
	var u User
	err = pool.QueryRow(c.Request.Context(), `
		SELECT s.id, s.user_id, s.csrf_token, s.token_version, s.expires_at, s.revoked_at,
		       u.id, u.username, COALESCE(u.email, ''), COALESCE(u.password_hash, ''),
		       u.nickname, u.avatar_url, u.bio, u.role, u.is_super_admin, u.is_ai, u.status,
		       u.require_follow_approval, u.token_version, u.suspended_at, u.created_at, u.updated_at
		FROM auth_sessions AS s
		JOIN users AS u ON u.id = s.user_id
		WHERE s.token_hash = $1`, hashSessionToken(cookie)).Scan(
		&session.ID, &session.UserID, &session.CSRFToken, &session.TokenVersion,
		&session.ExpiresAt, &session.RevokedAt,
		&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.Nickname, &u.AvatarURL,
		&u.Bio, &u.Role, &u.IsSuperAdmin, &u.IsAI, &u.Status, &u.RequireFollowApproval,
		&u.TokenVersion, &u.SuspendedAt, &u.CreatedAt, &u.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		s.clearSessionCookie(c)
		c.Next()
		return
	}
	if err != nil {
		// 数据库异常时按游客继续，公开内容仍可浏览；写接口由 RequireAuth 单独把关。
		c.Next()
		return
	}
	if u.IsSuspended() || session.RevokedAt != nil || !time.Now().Before(session.ExpiresAt) ||
		session.TokenVersion != u.TokenVersion || u.IsAI {
		c.Next()
		return
	}
	c.Set(userContextKey, u)
	c.Set(sessionContextKey, session)
	c.Next()
}

// RequireCSRF 校验与当前服务端会话绑定的令牌。
func (s *Service) RequireCSRF(c *gin.Context) {
	session, ok := CurrentSession(c)
	if !ok || !tokensEqual(c.GetHeader(csrfHeader), session.CSRFToken) {
		api.Error(c, http.StatusForbidden, "csrf_failed", "CSRF 校验失败，请刷新页面后重试")
		return
	}
	c.Next()
}

// GuestCSRF 为登录和注册提供双提交校验，使所有非 GET 业务请求都受 CSRF 防护。
func (s *Service) GuestCSRF(c *gin.Context) {
	cookie, err := c.Cookie(guestCSRFCookie)
	if err != nil || !tokensEqual(c.GetHeader(csrfHeader), cookie) {
		api.Error(c, http.StatusForbidden, "csrf_failed", "CSRF 校验失败，请刷新页面后重试")
		return
	}
	c.Next()
}

func (s *Service) RequireAdmin(c *gin.Context) {
	u, ok := CurrentUser(c)
	if !ok {
		api.Error(c, http.StatusUnauthorized, "unauthenticated", "请先登录")
		return
	}
	if u.IsAI || u.Role != RoleAdmin {
		api.Error(c, http.StatusForbidden, "forbidden", "需要管理员权限")
		return
	}
	c.Next()
}

// RequireSuperAdmin 在 RequireAdmin 之后使用；要求当前账号是超级管理员。
func (s *Service) RequireSuperAdmin(c *gin.Context) {
	u, ok := CurrentUser(c)
	if !ok {
		api.Error(c, http.StatusUnauthorized, "unauthenticated", "请先登录")
		return
	}
	if u.IsAI || u.Role != RoleAdmin || !u.IsSuperAdmin {
		api.Error(c, http.StatusForbidden, "forbidden", "需要超级管理员权限")
		return
	}
	c.Next()
}

// CSRF 优先返回当前有效会话的令牌；游客或失效会话则签发登录/注册用双提交令牌。
func (s *Service) CSRF(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	if cookie, err := c.Cookie(sessionCookieName); err == nil && cookie != "" {
		var csrfToken string
		err = pool.QueryRow(c.Request.Context(), `
			SELECT s.csrf_token
			FROM auth_sessions AS s
			JOIN users AS u ON u.id = s.user_id
			WHERE s.token_hash = $1
			  AND s.revoked_at IS NULL
			  AND s.expires_at > now()
			  AND s.token_version = u.token_version
			  AND u.status = 'active'
			  AND NOT u.is_ai`, hashSessionToken(cookie)).Scan(&csrfToken)
		if err == nil {
			c.JSON(http.StatusOK, gin.H{"csrf_token": csrfToken})
			return
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			s.internalError(c, "刷新会话 CSRF 令牌失败", err)
			return
		}
	}

	token, err := randomToken()
	if err != nil {
		s.internalError(c, "生成 CSRF 令牌失败", err)
		return
	}
	s.setGuestCSRFCookie(c, token)
	c.JSON(http.StatusOK, gin.H{"csrf_token": token})
}
