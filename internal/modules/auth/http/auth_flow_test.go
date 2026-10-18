package http

// 本文补充 auth/http 认证接口全链路用例（ResolveSession + 策略中间件 + handler）：
// 注册参数校验/开关/限频/成功/冲突、登录成功 Cookie 属性、错误映射、
// /auth/me 与退出登录/修改密码的 CSRF 与失效语义、公开配置与游客 CSRF。
// 已有 auth_handler_test.go 覆盖无效 JSON、游客 CSRF 与零装配服务分支，此处不重复。

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/auth/domain"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

func TestRegisterHandlerValidationAndCSRF(t *testing.T) {
	h := newHarness(t, true)
	engine := h.engine()

	t.Run("缺少游客 CSRF 双提交返回 403", func(t *testing.T) {
		recorder := h.perform(t, engine, http.MethodPost, "/api/v1/auth/register", `{"username":"alice"}`)
		if recorder.Code != http.StatusForbidden || errorCode(t, recorder) != "csrf_failed" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("无效 JSON 返回 400", func(t *testing.T) {
		recorder := h.perform(t, engine, http.MethodPost, "/api/v1/auth/register", `{`,
			guestCSRFHeaders(withRemoteAddr("203.0.113.60"))...)
		if recorder.Code != http.StatusBadRequest || errorCode(t, recorder) != "invalid_request" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("字段校验失败返回 400 与字段详情", func(t *testing.T) {
		body := `{"username":"ab","email":"bad","password":"short"}`
		recorder := h.perform(t, engine, http.MethodPost, "/api/v1/auth/register", body,
			guestCSRFHeaders(withRemoteAddr("203.0.113.61"))...)
		if recorder.Code != http.StatusBadRequest || errorCode(t, recorder) != "validation_failed" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
		details := errorDetails(t, recorder)
		for _, field := range []string{"username", "email", "password"} {
			if details[field] == nil {
				t.Fatalf("details 缺少字段 %q: %s", field, recorder.Body.String())
			}
		}
	})
}

func TestRegisterHandlerClosedRateLimitAndSuccess(t *testing.T) {
	h := newHarness(t, false)
	engine := h.engine()
	h.seedUser(t, model.User{ID: 100, Username: "existing", Email: "existing@example.com"})
	validBody := `{"username":"new_user","email":"new@example.com","password":"password123"}`

	t.Run("注册关闭返回 403", func(t *testing.T) {
		recorder := h.perform(t, engine, http.MethodPost, "/api/v1/auth/register", validBody,
			guestCSRFHeaders(withRemoteAddr("203.0.113.70"))...)
		if recorder.Code != http.StatusForbidden || errorCode(t, recorder) != "forbidden" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("超过 IP 注册限频返回 429 与 Retry-After", func(t *testing.T) {
		const ip = "203.0.113.71"
		for i := 0; i < 5; i++ {
			recorder := h.perform(t, engine, http.MethodPost, "/api/v1/auth/register", validBody,
				guestCSRFHeaders(withRemoteAddr(ip))...)
			if recorder.Code != http.StatusForbidden {
				t.Fatalf("第 %d 次尝试应在注册关闭时返回 403，实际 %d", i+1, recorder.Code)
			}
		}
		recorder := h.perform(t, engine, http.MethodPost, "/api/v1/auth/register", validBody,
			guestCSRFHeaders(withRemoteAddr(ip))...)
		if recorder.Code != http.StatusTooManyRequests || errorCode(t, recorder) != "rate_limited" {
			t.Fatalf("超过限频应返回 429 rate_limited，实际 %d body=%s", recorder.Code, recorder.Body.String())
		}
		if retryAfter := recorder.Header().Get("Retry-After"); retryAfter == "" {
			t.Fatal("限频响应应带 Retry-After 头")
		}
	})

	t.Run("开放注册成功返回 201 与安全用户 DTO", func(t *testing.T) {
		h.settings.enabled = true
		recorder := h.perform(t, engine, http.MethodPost, "/api/v1/auth/register", validBody,
			guestCSRFHeaders(withRemoteAddr("203.0.113.72"))...)
		if recorder.Code != http.StatusCreated {
			t.Fatalf("注册应返回 201，实际 %d body=%s", recorder.Code, recorder.Body.String())
		}
		if cookie := responseCookie(t, recorder, SessionCookieName); cookie != nil {
			t.Fatalf("注册不应写会话 Cookie: %+v", cookie)
		}
		body := bodyMap(t, recorder)
		user, ok := body["user"].(map[string]any)
		if !ok {
			t.Fatalf("响应缺少 user 对象: %s", recorder.Body.String())
		}
		if id, _ := user["id"].(string); id == "" {
			t.Fatalf("user.id 必须为字符串（雪花 ID 精度安全）: %v", user["id"])
		}
		if user["username"] != "new_user" || user["nickname"] != "new_user" || user["email"] != "new@example.com" {
			t.Fatalf("user DTO 字段不符: %v", user)
		}
		if user["role"] != model.RoleUser {
			t.Fatalf("已初始化系统注册应为普通用户，实际 role=%v", user["role"])
		}
		if _, leaked := user["password_hash"]; leaked {
			t.Fatal("响应不得包含密码哈希")
		}
	})

	t.Run("用户名冲突返回 409", func(t *testing.T) {
		h.settings.enabled = true
		recorder := h.perform(t, engine, http.MethodPost, "/api/v1/auth/register", validBody,
			guestCSRFHeaders(withRemoteAddr("203.0.113.73"))...)
		if recorder.Code != http.StatusConflict || errorCode(t, recorder) != "duplicate" {
			t.Fatalf("重复用户名应返回 409 duplicate，实际 %d body=%s", recorder.Code, recorder.Body.String())
		}
	})
}

func TestLoginHandlerSuccessAndCookieAttributes(t *testing.T) {
	h := newHarness(t, true)
	engine := h.engine()
	user := h.seedUser(t, model.User{
		ID: 1, Username: "alice", Email: "alice@example.com",
		PasswordHash: cheapPasswordHash(t, "password123"),
	})

	recorder, sessionToken, csrfToken := h.login(t, engine, "alice", "password123", "203.0.113.80")

	body := bodyMap(t, recorder)
	userDTO, ok := body["user"].(map[string]any)
	if !ok {
		t.Fatalf("登录响应缺少 user: %s", recorder.Body.String())
	}
	if userDTO["username"] != "alice" || userDTO["email"] != "alice@example.com" {
		t.Fatalf("user DTO 不符: %v", userDTO)
	}
	if body["csrf_token"] != csrfToken {
		t.Fatalf("响应 csrf_token 与会话 CSRF 不一致: %v vs %q", body["csrf_token"], csrfToken)
	}
	if strings.Contains(recorder.Body.String(), sessionToken) {
		t.Fatal("会话明文令牌不得出现在响应体中（只允许 HttpOnly Cookie）")
	}

	cookie := responseCookie(t, recorder, SessionCookieName)
	if cookie == nil {
		t.Fatalf("登录必须写入 %s Cookie: %v", SessionCookieName, recorder.Header())
	}
	if cookie.Value != sessionToken {
		t.Fatal("会话 Cookie 值应等于登录返回的明文令牌")
	}
	if !cookie.HttpOnly {
		t.Fatal("会话 Cookie 必须 HttpOnly")
	}
	if !cookie.Secure {
		t.Fatal("CookieSecure 配置为 true 时会话 Cookie 必须带 Secure")
	}
	if cookie.Path != "/" {
		t.Fatalf("会话 Cookie Path = %q，期望 /", cookie.Path)
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("会话 Cookie SameSite = %v，期望 Lax", cookie.SameSite)
	}
	if cookie.MaxAge != int(domain.SessionTTL.Seconds()) {
		t.Fatalf("会话 Cookie MaxAge = %d，期望 %d", cookie.MaxAge, int(domain.SessionTTL.Seconds()))
	}
	if got, want := cookie.Expires, testNow.Add(domain.SessionTTL); !got.Equal(want) {
		t.Fatalf("会话 Cookie Expires = %v，期望 %v", got, want)
	}

	stored, ok := h.sessions.sessions[domain.HashSessionToken(sessionToken)]
	if !ok {
		t.Fatal("会话必须按令牌摘要写入仓储")
	}
	if stored.session.UserID != user.ID || stored.session.TokenVersion != user.TokenVersion {
		t.Fatalf("会话字段不符: %+v", stored.session)
	}
	if h.sessions.lastInsertAt == nil || !h.sessions.lastInsertAt.Equal(testNow) {
		t.Fatalf("会话 created_at 应取应用时钟 %v", testNow)
	}

	t.Run("登录后 /auth/me 返回私有资料与会话 CSRF", func(t *testing.T) {
		me := h.performAs(t, engine, http.MethodGet, "/api/v1/auth/me", "", sessionToken, "")
		if me.Code != http.StatusOK {
			t.Fatalf("/auth/me 状态码 = %d，body=%s", me.Code, me.Body.String())
		}
		meBody := bodyMap(t, me)
		if meBody["csrf_token"] != csrfToken {
			t.Fatalf("me.csrf_token = %v，期望 %q", meBody["csrf_token"], csrfToken)
		}
		meUser, _ := meBody["user"].(map[string]any)
		if meUser["role"] != model.RoleUser || meUser["status"] != model.StatusActive {
			t.Fatalf("me.user 私有字段不符: %v", meUser)
		}
	})

	t.Run("密码错误返回 401 unauthenticated", func(t *testing.T) {
		body := `{"identifier":"alice","password":"wrong-password"}`
		recorder := h.perform(t, engine, http.MethodPost, "/api/v1/auth/login", body,
			guestCSRFHeaders(withRemoteAddr("203.0.113.81"))...)
		if recorder.Code != http.StatusUnauthorized || errorCode(t, recorder) != "unauthenticated" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
		if cookie := responseCookie(t, recorder, SessionCookieName); cookie != nil {
			t.Fatal("登录失败不应写会话 Cookie")
		}
	})

	t.Run("停用账号登录返回 403 account_suspended", func(t *testing.T) {
		h.seedUser(t, model.User{
			ID: 2, Username: "suspended_user", Email: "suspended@example.com",
			PasswordHash: cheapPasswordHash(t, "password123"), Status: model.StatusSuspended,
		})
		body := `{"identifier":"suspended_user","password":"password123"}`
		recorder := h.perform(t, engine, http.MethodPost, "/api/v1/auth/login", body,
			guestCSRFHeaders(withRemoteAddr("203.0.113.82"))...)
		if recorder.Code != http.StatusForbidden || errorCode(t, recorder) != "account_suspended" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})
}

func TestLoginHandlerRateLimited(t *testing.T) {
	h := newHarness(t, true)
	engine := h.engine()
	h.seedUser(t, model.User{
		ID: 1, Username: "bob", PasswordHash: cheapPasswordHash(t, "password123"),
	})

	const ip = "203.0.113.90"
	body := `{"identifier":"bob","password":"wrong-password"}`
	for attempt := 1; attempt <= 10; attempt++ {
		recorder := h.perform(t, engine, http.MethodPost, "/api/v1/auth/login", body,
			guestCSRFHeaders(withRemoteAddr(ip))...)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("第 %d 次失败应返回 401，实际 %d", attempt, recorder.Code)
		}
	}
	recorder := h.perform(t, engine, http.MethodPost, "/api/v1/auth/login", body,
		guestCSRFHeaders(withRemoteAddr(ip))...)
	if recorder.Code != http.StatusTooManyRequests || errorCode(t, recorder) != "rate_limited" {
		t.Fatalf("触发账号+IP 限频应返回 429，实际 %d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Retry-After") == "" {
		t.Fatal("429 响应必须带 Retry-After 头")
	}
}

func TestMeLogoutAndChangePasswordFlow(t *testing.T) {
	h := newHarness(t, true)
	engine := h.engine()
	user := h.seedUser(t, model.User{
		ID: 1, Username: "alice", Email: "alice@example.com",
		PasswordHash: cheapPasswordHash(t, "password123"),
	})
	h.seedSession(user, "session-a", "csrf-a")
	second := h.sessions.sessions[domain.HashSessionToken("session-a")]
	second.session.ID = 111
	h.sessions.sessions[domain.HashSessionToken("session-b")] = second

	t.Run("退出登录需要 CSRF", func(t *testing.T) {
		recorder := h.performAs(t, engine, http.MethodPost, "/api/v1/auth/logout", "", "session-a", "")
		if recorder.Code != http.StatusForbidden || errorCode(t, recorder) != "csrf_failed" {
			t.Fatalf("缺少 CSRF 应返回 403 csrf_failed，实际 %d body=%s", recorder.Code, recorder.Body.String())
		}
		recorder = h.performAs(t, engine, http.MethodPost, "/api/v1/auth/logout", "", "session-a", "wrong-csrf")
		if recorder.Code != http.StatusForbidden || errorCode(t, recorder) != "csrf_failed" {
			t.Fatalf("错误 CSRF 应返回 403 csrf_failed，实际 %d", recorder.Code)
		}
	})

	t.Run("修改密码需要 CSRF 且校验新密码", func(t *testing.T) {
		recorder := h.performAs(t, engine, http.MethodPost, "/api/v1/auth/password",
			`{"old_password":"password123","new_password":"new-password"}`, "session-a", "")
		if recorder.Code != http.StatusForbidden || errorCode(t, recorder) != "csrf_failed" {
			t.Fatalf("缺少 CSRF 应返回 403，实际 %d", recorder.Code)
		}

		shortPassword := `{"old_password":"password123","new_password":"short"}`
		recorder = h.performAs(t, engine, http.MethodPost, "/api/v1/auth/password", shortPassword, "session-a", "csrf-a")
		if recorder.Code != http.StatusBadRequest || errorCode(t, recorder) != "validation_failed" {
			t.Fatalf("过短新密码应返回 400，实际 %d body=%s", recorder.Code, recorder.Body.String())
		}
		if errorDetails(t, recorder)["new_password"] == nil {
			t.Fatal("应返回 new_password 字段详情")
		}

		wrongOld := `{"old_password":"wrong-password","new_password":"new-password"}`
		recorder = h.performAs(t, engine, http.MethodPost, "/api/v1/auth/password", wrongOld, "session-a", "csrf-a")
		if recorder.Code != http.StatusBadRequest || errorCode(t, recorder) != "validation_failed" {
			t.Fatalf("错误旧密码应返回 400，实际 %d body=%s", recorder.Code, recorder.Body.String())
		}
		details := errorDetails(t, recorder)
		if details["old_password"] == nil {
			t.Fatal("应返回 old_password 字段详情")
		}
	})

	t.Run("修改密码成功保留当前会话并吊销其他会话", func(t *testing.T) {
		valid := `{"old_password":"password123","new_password":"new-password"}`
		recorder := h.performAs(t, engine, http.MethodPost, "/api/v1/auth/password", valid, "session-a", "csrf-a")
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("修改密码应返回 204，实际 %d body=%s", recorder.Code, recorder.Body.String())
		}
		updated := h.users.users[user.ID]
		if updated.TokenVersion != user.TokenVersion+1 {
			t.Fatalf("token_version = %d，期望 %d", updated.TokenVersion, user.TokenVersion+1)
		}
		current := h.sessions.sessions[domain.HashSessionToken("session-a")].session
		if current.RevokedAt != nil {
			t.Fatal("当前会话必须保留")
		}
		if current.TokenVersion != updated.TokenVersion {
			t.Fatalf("当前会话 token_version 未同步: %d", current.TokenVersion)
		}
		if revoked := h.sessions.sessions[domain.HashSessionToken("session-b")].session; revoked.RevokedAt == nil {
			t.Fatal("其他会话必须被吊销")
		}

		// 当前会话在改密后仍可解析（会话 token_version 已同步）。
		me := h.performAs(t, engine, http.MethodGet, "/api/v1/auth/me", "", "session-a", "")
		if me.Code != http.StatusOK {
			t.Fatalf("改密后当前会话应继续有效，实际 %d body=%s", me.Code, me.Body.String())
		}
	})

	t.Run("退出登录吊销当前会话并清 Cookie", func(t *testing.T) {
		recorder := h.performAs(t, engine, http.MethodPost, "/api/v1/auth/logout", "", "session-a", "csrf-a")
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("退出登录应返回 204，实际 %d body=%s", recorder.Code, recorder.Body.String())
		}
		if revoked := h.sessions.sessions[domain.HashSessionToken("session-a")].session; revoked.RevokedAt == nil {
			t.Fatal("退出登录必须吊销当前会话")
		}
		cookie := responseCookie(t, recorder, SessionCookieName)
		if cookie == nil || cookie.Value != "" {
			t.Fatalf("退出登录应清空会话 Cookie: %+v", cookie)
		}
		if !strings.Contains(recorder.Header().Get("Set-Cookie"), "Max-Age=0") {
			t.Fatalf("清 Cookie 应使用 Max-Age=0: %s", recorder.Header().Get("Set-Cookie"))
		}
		if cookie.Expires.After(time.Now()) {
			t.Fatalf("清 Cookie 的 Expires 必须早于当前时间: %v", cookie.Expires)
		}

		// 已吊销会话仍带 Cookie 访问受保护接口：401 并再次清 Cookie。
		me := h.performAs(t, engine, http.MethodGet, "/api/v1/auth/me", "", "session-a", "")
		if me.Code != http.StatusUnauthorized || errorCode(t, me) != "unauthenticated" {
			t.Fatalf("已吊销会话应返回 401，实际 %d", me.Code)
		}
		if cleared := responseCookie(t, me, SessionCookieName); cleared == nil || cleared.Value != "" {
			t.Fatal("失效会话应清除会话 Cookie")
		}
	})

	t.Run("退出登录仓储失败返回 500", func(t *testing.T) {
		currentUser := h.users.users[user.ID]
		h.seedSession(currentUser, "logout-error-token", "logout-error-csrf")
		h.sessions.revokeErr = fakePortErr
		t.Cleanup(func() { h.sessions.revokeErr = nil })
		recorder := h.performAs(t, engine, http.MethodPost, "/api/v1/auth/logout", "", "logout-error-token", "logout-error-csrf")
		if recorder.Code != http.StatusInternalServerError || errorCode(t, recorder) != "internal_error" {
			t.Fatalf("内部错误应返回 500，实际 %d body=%s", recorder.Code, recorder.Body.String())
		}
	})
}

func TestConfigAndCSRFHandlers(t *testing.T) {
	h := newHarness(t, true)
	engine := h.engine()

	t.Run("空库配置提示初始化", func(t *testing.T) {
		recorder := h.perform(t, engine, http.MethodGet, "/api/v1/auth/config", "")
		if recorder.Code != http.StatusOK {
			t.Fatalf("状态码 = %d", recorder.Code)
		}
		body := bodyMap(t, recorder)
		if body["registration_enabled"] != true || body["bootstrap_required"] != true {
			t.Fatalf("空库配置不符: %v", body)
		}
	})

	t.Run("已初始化与关闭注册配置", func(t *testing.T) {
		h.seedUser(t, model.User{ID: 100, Username: "existing"})
		recorder := h.perform(t, engine, http.MethodGet, "/api/v1/auth/config", "")
		body := bodyMap(t, recorder)
		if body["bootstrap_required"] != false || body["registration_enabled"] != true {
			t.Fatalf("已初始化配置不符: %v", body)
		}
		h.settings.enabled = false
		recorder = h.perform(t, engine, http.MethodGet, "/api/v1/auth/config", "")
		if body := bodyMap(t, recorder); body["registration_enabled"] != false {
			t.Fatalf("关闭注册后配置不符: %v", body)
		}
	})

	t.Run("游客 CSRF 签发双提交 Cookie", func(t *testing.T) {
		recorder := h.perform(t, engine, http.MethodGet, "/api/v1/auth/csrf", "")
		if recorder.Code != http.StatusOK {
			t.Fatalf("状态码 = %d", recorder.Code)
		}
		body := bodyMap(t, recorder)
		token, _ := body["csrf_token"].(string)
		if token == "" {
			t.Fatal("游客 CSRF 令牌不能为空")
		}
		cookie := responseCookie(t, recorder, GuestCSRFCookieName)
		if cookie == nil {
			t.Fatalf("应写入游客 CSRF Cookie: %v", recorder.Header())
		}
		if cookie.Value != token || cookie.Path != guestCSRFCookiePath || !cookie.HttpOnly || !cookie.Secure {
			t.Fatalf("游客 CSRF Cookie 属性不符: %+v", cookie)
		}
		if cookie.SameSite != http.SameSiteLaxMode || cookie.MaxAge != guestCSRFCookieMaxAge {
			t.Fatalf("游客 CSRF Cookie 作用域/有效期不符: %+v", cookie)
		}
	})

	t.Run("已登录 CSRF 返回会话令牌且不签发游客 Cookie", func(t *testing.T) {
		user := h.seedUser(t, model.User{ID: 101, Username: "csrf_user"})
		h.seedSession(user, "csrf-session", "session-csrf")
		recorder := h.perform(t, engine, http.MethodGet, "/api/v1/auth/csrf", "",
			withCookie(SessionCookieName, "csrf-session"))
		if recorder.Code != http.StatusOK {
			t.Fatalf("状态码 = %d", recorder.Code)
		}
		if body := bodyMap(t, recorder); body["csrf_token"] != "session-csrf" {
			t.Fatalf("应返回会话 CSRF: %v", body)
		}
		if cookie := responseCookie(t, recorder, GuestCSRFCookieName); cookie != nil {
			t.Fatal("已登录请求不应再签发游客 CSRF Cookie")
		}
	})
}
