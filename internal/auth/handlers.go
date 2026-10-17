package auth

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/great-magician-01/timeline/internal/api"
)

func (s *Service) Register(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	if ok, retry := s.registerLimiter.Consume("ip:" + c.ClientIP()); !ok {
		rateLimited(c, retry)
		return
	}

	var in RegisterInput
	if err := api.DecodeJSON(c, &in); err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", invalidInputError(err))
		return
	}
	in, details := normalizeRegistration(in)
	if details != nil {
		api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", "请检查注册信息", details)
		return
	}
	enabled, err := RegistrationEnabled(c.Request.Context(), pool)
	if err != nil {
		s.internalError(c, "读取注册开关失败", err)
		return
	}
	// 注册关闭时先判断是否处于未初始化状态（空库例外）：无人类用户仍允许首个注册。
	// 这里只是快速拒绝路径，事务内持锁后会再次复核，避免并发下状态变化。
	if !enabled {
		bootstrap, err := BootstrapRequired(c.Request.Context(), pool)
		if err != nil {
			s.internalError(c, "读取初始化状态失败", err)
			return
		}
		if !bootstrap {
			api.Error(c, http.StatusForbidden, "forbidden", "当前未开放注册")
			return
		}
	}

	// bcrypt 成本较高，放在事务外执行，锁内只做计数、INSERT 与开关复核。
	passwordHash, err := HashPassword(in.Password)
	if err != nil {
		api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", "密码不符合要求", gin.H{"password": err.Error()})
		return
	}
	userID, err := s.ids.Next()
	if err != nil {
		s.internalError(c, "生成用户 ID 失败", err)
		return
	}

	tx, err := pool.Begin(c.Request.Context())
	if err != nil {
		s.internalError(c, "开启注册事务失败", err)
		return
	}
	defer func() { _ = tx.Rollback(c.Request.Context()) }()
	if _, err := tx.Exec(c.Request.Context(),
		`SELECT pg_advisory_xact_lock($1)`, bootstrapAdvisoryLockKey,
	); err != nil {
		s.internalError(c, "获取注册初始化锁失败", err)
		return
	}
	var bootstrap bool
	if err := tx.QueryRow(c.Request.Context(),
		`SELECT NOT EXISTS (SELECT 1 FROM users WHERE NOT is_ai)`,
	).Scan(&bootstrap); err != nil {
		s.internalError(c, "读取初始化状态失败", err)
		return
	}
	if !bootstrap && !enabled {
		api.Error(c, http.StatusForbidden, "forbidden", "当前未开放注册")
		return
	}

	// 空库首个注册用户成为唯一超级管理员；已有人类用户时按普通用户创建。
	role := RoleUser
	if bootstrap {
		role = RoleAdmin
	}
	u, err := ScanUser(tx.QueryRow(c.Request.Context(), `
		INSERT INTO users (
			id, username, email, password_hash, nickname, role, is_super_admin, is_ai, status,
			require_follow_approval, token_version
		) VALUES ($1, $2, $3, $4, $2, $5, $6, FALSE, 'active', FALSE, 1)
		RETURNING `+UserColumns,
		userID, in.Username, in.Email, passwordHash, role, bootstrap,
	))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			details := gin.H{}
			switch pgErr.ConstraintName {
			case "users_username_unique":
				details["username"] = "用户名已存在"
			case "users_email_unique":
				details["email"] = "邮箱已存在"
			}
			api.ErrorWithDetails(c, http.StatusConflict, "duplicate", "用户名或邮箱已被使用", details)
			return
		}
		s.internalError(c, "创建用户失败", err)
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		s.internalError(c, "提交注册事务失败", err)
		return
	}
	if bootstrap {
		s.log.Info("系统首个注册用户已创建为超级管理员", "user_id", u.ID)
	}
	c.JSON(http.StatusCreated, gin.H{"user": PrivateUser(u)})
}

func (s *Service) Login(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	var in struct {
		Identifier string `json:"identifier"`
		Password   string `json:"password"`
	}
	if err := api.DecodeJSON(c, &in); err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", invalidInputError(err))
		return
	}
	in.Identifier = strings.TrimSpace(in.Identifier)
	if in.Identifier == "" || in.Password == "" || len(in.Identifier) > 254 {
		api.Error(c, http.StatusBadRequest, "invalid_request", "请输入用户名或邮箱以及密码")
		return
	}

	ipKey := "ip:" + c.ClientIP()
	accountKey := "account:" + strings.ToLower(in.Identifier)
	pairKey := accountKey + "|" + c.ClientIP()
	allowedIP, retryIP := s.loginIPLimiter.Check(ipKey)
	allowedPair, retryPair := s.loginPairLimiter.Check(pairKey)
	allowedAccount, retryAccount := s.loginAccountLimiter.Check(accountKey)
	if !allowedIP || !allowedPair || !allowedAccount {
		rateLimited(c, max(retryIP, retryPair, retryAccount))
		return
	}

	u, err := ScanUser(pool.QueryRow(c.Request.Context(),
		`SELECT `+UserColumns+` FROM users
		 WHERE LOWER(username) = LOWER($1) OR LOWER(email) = LOWER($1)`, in.Identifier,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		passwordMatches(s.dummyHash, in.Password)
		s.recordLoginFailure(ipKey, accountKey, pairKey)
		api.Error(c, http.StatusUnauthorized, "unauthenticated", "用户名/邮箱或密码错误")
		return
	}
	if err != nil {
		s.internalError(c, "查询登录账号失败", err)
		return
	}
	hash := u.PasswordHash
	if u.IsAI || hash == "" {
		hash = s.dummyHash
	}
	if u.IsAI || !passwordMatches(hash, in.Password) {
		s.recordLoginFailure(ipKey, accountKey, pairKey)
		api.Error(c, http.StatusUnauthorized, "unauthenticated", "用户名/邮箱或密码错误")
		return
	}
	if u.IsSuspended() {
		api.Error(c, http.StatusForbidden, "account_suspended", "账号已停用，不能登录")
		return
	}

	sessionID, err := s.ids.Next()
	if err != nil {
		s.internalError(c, "生成会话 ID 失败", err)
		return
	}
	rawToken, err := randomToken()
	if err != nil {
		s.internalError(c, "生成会话令牌失败", err)
		return
	}
	csrfToken, err := randomToken()
	if err != nil {
		s.internalError(c, "生成 CSRF 令牌失败", err)
		return
	}
	expiresAt := time.Now().Add(sessionTTL)
	if _, err = pool.Exec(c.Request.Context(), `
		INSERT INTO auth_sessions (
			id, user_id, token_hash, csrf_token, token_version, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6)`,
		sessionID, u.ID, hashSessionToken(rawToken), csrfToken, u.TokenVersion, expiresAt,
	); err != nil {
		s.internalError(c, "创建登录会话失败", err)
		return
	}

	// 成功登录只重置该账号及账号+IP 维度；IP 失败窗口继续保留，防止用已知账号刷新攻击额度。
	s.loginPairLimiter.Reset(pairKey)
	s.loginAccountLimiter.Reset(accountKey)
	s.setSessionCookie(c, rawToken, expiresAt)
	c.JSON(http.StatusOK, gin.H{"user": PrivateUser(u), "csrf_token": csrfToken})
}

func (s *Service) recordLoginFailure(ipKey, accountKey, pairKey string) {
	s.loginIPLimiter.Fail(ipKey)
	s.loginAccountLimiter.Fail(accountKey)
	s.loginPairLimiter.Fail(pairKey)
}

func rateLimited(c *gin.Context, retry time.Duration) {
	seconds := int(math.Ceil(retry.Seconds()))
	seconds = max(seconds, 1)
	c.Header("Retry-After", strconv.Itoa(seconds))
	api.Error(c, http.StatusTooManyRequests, "rate_limited", "尝试过于频繁，请稍后重试")
}

func (s *Service) Me(c *gin.Context) {
	u, _ := CurrentUser(c)
	session, _ := CurrentSession(c)
	c.JSON(http.StatusOK, gin.H{"user": PrivateUser(u), "csrf_token": session.CSRFToken})
}

func (s *Service) Logout(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	session, _ := CurrentSession(c)
	if _, err := pool.Exec(c.Request.Context(),
		`UPDATE auth_sessions SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`,
		session.ID,
	); err != nil {
		s.internalError(c, "退出登录失败", err)
		return
	}
	s.clearSessionCookie(c)
	c.Status(http.StatusNoContent)
}

func (s *Service) ChangePassword(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	var in struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := api.DecodeJSON(c, &in); err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", invalidInputError(err))
		return
	}
	if err := ValidatePassword(in.NewPassword); err != nil {
		api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", "新密码不符合要求", gin.H{"new_password": err.Error()})
		return
	}
	current, _ := CurrentUser(c)
	session, _ := CurrentSession(c)
	tx, err := pool.Begin(c.Request.Context())
	if err != nil {
		s.internalError(c, "开启修改密码事务失败", err)
		return
	}
	defer func() { _ = tx.Rollback(c.Request.Context()) }()

	u, err := UserByID(c.Request.Context(), tx, current.ID, true)
	if err != nil {
		s.internalError(c, "读取账号密码失败", err)
		return
	}
	if u.IsSuspended() {
		api.Error(c, http.StatusForbidden, "account_suspended", "账号已停用，不能修改密码")
		return
	}
	if !passwordMatches(u.PasswordHash, in.OldPassword) {
		api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", "原密码不正确", gin.H{"old_password": "原密码不正确"})
		return
	}
	newHash, err := HashPassword(in.NewPassword)
	if err != nil {
		api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", "新密码不符合要求", gin.H{"new_password": err.Error()})
		return
	}
	newVersion := u.TokenVersion + 1
	if _, err = tx.Exec(c.Request.Context(), `
		UPDATE users
		SET password_hash = $2, token_version = $3, updated_at = now()
		WHERE id = $1`, u.ID, newHash, newVersion); err != nil {
		s.internalError(c, "更新密码失败", err)
		return
	}
	if _, err = tx.Exec(c.Request.Context(), `
		UPDATE auth_sessions SET revoked_at = now()
		WHERE user_id = $1 AND id <> $2 AND revoked_at IS NULL`, u.ID, session.ID); err != nil {
		s.internalError(c, "吊销其他会话失败", err)
		return
	}
	if _, err = tx.Exec(c.Request.Context(), `
		UPDATE auth_sessions SET token_version = $2 WHERE id = $1`,
		session.ID, newVersion); err != nil {
		s.internalError(c, "保留当前会话失败", err)
		return
	}
	if err = tx.Commit(c.Request.Context()); err != nil {
		s.internalError(c, "提交修改密码事务失败", err)
		return
	}
	c.Status(http.StatusNoContent)
}
