package http

// 本文补充 auth/http 会话中间件全链路状态映射（停用/吊销/过期/版本不匹配/AI/
// 解析内部错误/未登录）与 handler 直调的鉴权保护分支（缺少 Viewer/Session）。
// 已有 middleware_test.go 用合成上下文覆盖了状态到状态码的映射矩阵，此处验证
// 真实 ResolveSession 解析结果驱动同一套映射，避免中间件与 app 状态定义漂移。

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/modules/auth/domain"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

func TestResolveSessionMiddlewareStates(t *testing.T) {
	h := newHarness(t, true)
	engine := h.engine()

	active := h.seedUser(t, model.User{ID: 1, Username: "alice"})
	h.seedSession(active, "active-token", "active-csrf")

	suspended := h.seedUser(t, model.User{ID: 2, Username: "suspended", Status: model.StatusSuspended})
	h.seedSession(suspended, "suspended-token", "suspended-csrf")

	aiUser := h.seedUser(t, model.User{ID: 3, Username: "ai_bot", IsAI: true})
	h.seedSession(aiUser, "ai-token", "ai-csrf")

	revoked := h.seedUser(t, model.User{ID: 4, Username: "revoked_user"})
	revokedSession := h.seedSession(revoked, "revoked-token", "revoked-csrf")
	if err := h.sessions.Revoke(context.Background(), revokedSession.ID, testNow); err != nil {
		t.Fatalf("吊销会话失败: %v", err)
	}

	versionMismatch := h.seedUser(t, model.User{ID: 5, Username: "version_user", TokenVersion: 2})
	h.seedSession(versionMismatch, "version-token", "version-csrf")

	expired := h.seedUser(t, model.User{ID: 6, Username: "expired_user"})
	h.seedSession(expired, "expired-token", "expired-csrf")
	expiredKey := domain.HashSessionToken("expired-token")
	expiredEntry := h.sessions.sessions[expiredKey]
	expiredEntry.session.ExpiresAt = testNow.Add(-time.Minute)
	h.sessions.sessions[expiredKey] = expiredEntry

	t.Run("有效会话放行", func(t *testing.T) {
		recorder := h.performAs(t, engine, http.MethodGet, "/api/v1/auth/me", "", "active-token", "")
		if recorder.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("未登录返回 401", func(t *testing.T) {
		recorder := h.perform(t, engine, http.MethodGet, "/api/v1/auth/me", "")
		if recorder.Code != http.StatusUnauthorized || errorCode(t, recorder) != "unauthenticated" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("停用账号 GET 返回 401 并清 Cookie", func(t *testing.T) {
		recorder := h.perform(t, engine, http.MethodGet, "/api/v1/auth/me", "",
			withCookie(SessionCookieName, "suspended-token"))
		if recorder.Code != http.StatusUnauthorized || errorCode(t, recorder) != "unauthenticated" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
		if cookie := responseCookie(t, recorder, SessionCookieName); cookie == nil || cookie.Value != "" {
			t.Fatal("停用账号 GET 应清除会话 Cookie")
		}
	})

	t.Run("停用账号写请求返回 403 account_suspended", func(t *testing.T) {
		recorder := h.perform(t, engine, http.MethodPost, "/api/v1/users/me", `{"nickname":"x"}`,
			withCookie(SessionCookieName, "suspended-token"), withHeader(CSRFHeader, "suspended-csrf"))
		if recorder.Code != http.StatusForbidden || errorCode(t, recorder) != "account_suspended" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("已吊销会话返回 401 并清 Cookie", func(t *testing.T) {
		recorder := h.perform(t, engine, http.MethodGet, "/api/v1/auth/me", "",
			withCookie(SessionCookieName, "revoked-token"))
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("状态码 = %d", recorder.Code)
		}
		if cookie := responseCookie(t, recorder, SessionCookieName); cookie == nil || cookie.Value != "" {
			t.Fatal("失效会话应清除 Cookie")
		}
	})

	t.Run("token_version 不匹配返回 401", func(t *testing.T) {
		key := domain.HashSessionToken("version-token")
		entry := h.sessions.sessions[key]
		entry.session.TokenVersion = versionMismatch.TokenVersion - 1
		h.sessions.sessions[key] = entry
		recorder := h.perform(t, engine, http.MethodGet, "/api/v1/auth/me", "",
			withCookie(SessionCookieName, "version-token"))
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("状态码 = %d", recorder.Code)
		}
		if cookie := responseCookie(t, recorder, SessionCookieName); cookie == nil || cookie.Value != "" {
			t.Fatal("token_version 不匹配应清除 Cookie")
		}
	})

	t.Run("过期会话返回 401", func(t *testing.T) {
		recorder := h.perform(t, engine, http.MethodGet, "/api/v1/auth/me", "",
			withCookie(SessionCookieName, "expired-token"))
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("状态码 = %d", recorder.Code)
		}
	})

	t.Run("AI 账号会话返回 401", func(t *testing.T) {
		recorder := h.perform(t, engine, http.MethodGet, "/api/v1/auth/me", "",
			withCookie(SessionCookieName, "ai-token"))
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("状态码 = %d", recorder.Code)
		}
	})

	t.Run("会话解析内部错误：公开读继续、登录路由 500", func(t *testing.T) {
		h.sessions.lookupErr = fakePortErr
		t.Cleanup(func() { h.sessions.lookupErr = nil })

		publicRead := h.perform(t, engine, http.MethodGet, "/api/v1/public/ping", "",
			withCookie(SessionCookieName, "any-token"))
		if publicRead.Code != http.StatusOK {
			t.Fatalf("解析内部错误时公开读应继续，实际 %d", publicRead.Code)
		}

		loginRoute := h.perform(t, engine, http.MethodGet, "/api/v1/auth/me", "",
			withCookie(SessionCookieName, "any-token"))
		if loginRoute.Code != http.StatusInternalServerError || errorCode(t, loginRoute) != "internal_error" {
			t.Fatalf("解析内部错误时登录路由应返回 500，实际 %d body=%s", loginRoute.Code, loginRoute.Body.String())
		}
		if cookie := responseCookie(t, loginRoute, SessionCookieName); cookie != nil {
			t.Fatal("内部错误不应清除会话 Cookie")
		}
	})
}

func TestMeAndSessionHandlersRequireContext(t *testing.T) {
	h := newHarness(t, true)
	user := h.seedUser(t, model.User{ID: 1, Username: "alice"})

	t.Run("Me 缺少 Viewer 返回 401", func(t *testing.T) {
		c, recorder := newTestContext(http.MethodGet, "/api/v1/auth/me")
		h.handler.Me(c)
		if recorder.Code != http.StatusUnauthorized || errorCode(t, recorder) != "unauthenticated" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("Me 缺少 Session 返回 401", func(t *testing.T) {
		c, recorder := newTestContext(http.MethodGet, "/api/v1/auth/me")
		httpx.SetViewer(c, model.Viewer{ID: user.ID, Authenticated: true})
		h.handler.Me(c)
		if recorder.Code != http.StatusUnauthorized || errorCode(t, recorder) != "unauthenticated" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("Me 读取账号失败返回 404", func(t *testing.T) {
		c, recorder := newTestContext(http.MethodGet, "/api/v1/auth/me")
		httpx.SetViewer(c, model.Viewer{ID: 999, Authenticated: true})
		httpx.SetSession(c, model.Session{ID: 1, UserID: 999, CSRFToken: "csrf"})
		h.handler.Me(c)
		if recorder.Code != http.StatusNotFound || errorCode(t, recorder) != "not_found" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("ChangePassword 缺少 Viewer/Session 返回 401", func(t *testing.T) {
		c, recorder := newJSONContext(http.MethodPost, "/api/v1/auth/password", `{"old_password":"a","new_password":"b"}`)
		h.handler.ChangePassword(c)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("缺少 Viewer 状态码 = %d", recorder.Code)
		}

		c, recorder = newJSONContext(http.MethodPost, "/api/v1/auth/password", `{"old_password":"a","new_password":"b"}`)
		httpx.SetViewer(c, model.Viewer{ID: user.ID, Authenticated: true})
		h.handler.ChangePassword(c)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("缺少 Session 状态码 = %d", recorder.Code)
		}
	})

	t.Run("Logout 缺少 Session 返回 401", func(t *testing.T) {
		c, recorder := newTestContext(http.MethodPost, "/api/v1/auth/logout")
		httpx.SetViewer(c, model.Viewer{ID: user.ID, Authenticated: true})
		h.handler.Logout(c)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})
}

// TestResolveSessionSetsViewerAndSession 直接验证中间件把有效会话写入 httpx 上下文，
// 并断言游客状态不注入身份。
func TestResolveSessionSetsViewerAndSession(t *testing.T) {
	h := newHarness(t, true)
	user := h.seedUser(t, model.User{ID: 1, Username: "alice", Role: model.RoleAdmin, IsSuperAdmin: true})
	h.seedSession(user, "ctx-token", "ctx-csrf")

	engine := gin.New()
	engine.Use(h.handler.ResolveSession)
	var gotViewer model.Viewer
	var gotSession model.Session
	engine.GET("/api/v1/probe", func(c *gin.Context) {
		gotViewer, _ = httpx.Viewer(c)
		gotSession, _ = httpx.Session(c)
		c.Status(http.StatusNoContent)
	})

	recorder := h.perform(t, engine, http.MethodGet, "/api/v1/probe", "",
		withCookie(SessionCookieName, "ctx-token"))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("状态码 = %d", recorder.Code)
	}
	if !gotViewer.Authenticated || !gotViewer.IsAdmin || !gotViewer.IsSuperAdmin {
		t.Fatalf("Viewer 注入不符: %+v", gotViewer)
	}
	if gotSession.CSRFToken != "ctx-csrf" || gotSession.UserID != user.ID {
		t.Fatalf("Session 注入不符: %+v", gotSession)
	}

	gotViewer = model.Viewer{}
	recorder = h.perform(t, engine, http.MethodGet, "/api/v1/probe", "")
	if recorder.Code != http.StatusNoContent || gotViewer.Authenticated {
		t.Fatalf("游客请求不应注入身份: %+v", gotViewer)
	}
}

// TestSessionCookieAttributesOnClearing 验证清 Cookie 时保留 HttpOnly/Secure/SameSite，
// 避免前端脚本读取或经明文传输。
func TestSessionCookieAttributesOnClearing(t *testing.T) {
	h := newHarness(t, true)
	c, recorder := newTestContext(http.MethodGet, "/api/v1/auth/me")
	h.handler.clearSessionCookie(c)
	raw := recorder.Header().Get("Set-Cookie")
	for _, attr := range []string{SessionCookieName + "=", "HttpOnly", "Secure", "Path=/", "SameSite=Lax", "Max-Age=0"} {
		if !strings.Contains(raw, attr) {
			t.Fatalf("清除 Cookie 缺少属性 %q: %s", attr, raw)
		}
	}
}
