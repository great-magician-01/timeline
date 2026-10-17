package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/modules/auth/domain"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

func init() { gin.SetMode(gin.TestMode) }

// newTestContext 构造一个只含请求与响应记录器的 gin 上下文。
func newTestContext(method, path string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, path, nil)
	return c, recorder
}

// newJSONContext 构造带 JSON 请求体的上下文。
func newJSONContext(method, path, body string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, recorder
}

func errorCode(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析错误响应失败: %v，body=%s", err, recorder.Body.String())
	}
	return body.Error.Code
}

func TestRequireLoginMatrix(t *testing.T) {
	h := New(nil)

	t.Run("游客 401", func(t *testing.T) {
		c, recorder := newTestContext(http.MethodGet, "/api/v1/auth/me")
		setSessionState(c, domain.SessionMissing)
		h.RequireLogin(c)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("状态码 = %d，期望 401", recorder.Code)
		}
		if code := errorCode(t, recorder); code != "unauthenticated" {
			t.Fatalf("错误码 = %q，期望 unauthenticated", code)
		}
	})

	t.Run("有效会话放行", func(t *testing.T) {
		c, recorder := newTestContext(http.MethodGet, "/api/v1/auth/me")
		httpx.SetViewer(c, model.Viewer{ID: 1, Authenticated: true})
		h.RequireLogin(c)
		if recorder.Body.Len() != 0 {
			t.Fatalf("不应写出响应: %s", recorder.Body.String())
		}
	})

	t.Run("停用账号 GET 401 并清 Cookie", func(t *testing.T) {
		c, recorder := newTestContext(http.MethodGet, "/api/v1/auth/me")
		c.Request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "stale"})
		setSessionState(c, domain.SessionSuspended)
		h.RequireLogin(c)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("状态码 = %d，期望 401", recorder.Code)
		}
		if !strings.Contains(recorder.Header().Get("Set-Cookie"), SessionCookieName+"=") {
			t.Fatal("失效会话应清除会话 Cookie")
		}
	})

	t.Run("停用账号写请求 403 account_suspended", func(t *testing.T) {
		c, recorder := newTestContext(http.MethodPost, "/api/v1/users/me")
		setSessionState(c, domain.SessionSuspended)
		h.RequireLogin(c)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("状态码 = %d，期望 403", recorder.Code)
		}
		if code := errorCode(t, recorder); code != "account_suspended" {
			t.Fatalf("错误码 = %q，期望 account_suspended", code)
		}
	})

	t.Run("失效会话 401 并清 Cookie", func(t *testing.T) {
		c, recorder := newTestContext(http.MethodGet, "/api/v1/auth/me")
		setSessionState(c, domain.SessionRevoked)
		h.RequireLogin(c)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("状态码 = %d，期望 401", recorder.Code)
		}
		if !strings.Contains(recorder.Header().Get("Set-Cookie"), SessionCookieName+"=") {
			t.Fatal("失效会话应清除会话 Cookie")
		}
	})
}

func TestRequireAdminMatrix(t *testing.T) {
	h := New(nil)
	cases := []struct {
		name   string
		viewer model.Viewer
		want   int
		wantOK bool
	}{
		{"游客", model.GuestViewer(), http.StatusUnauthorized, false},
		{"普通用户", model.Viewer{ID: 2, Authenticated: true}, http.StatusForbidden, false},
		{"管理员", model.Viewer{ID: 3, Authenticated: true, IsAdmin: true}, http.StatusOK, true},
		{"AI 管理员", model.Viewer{ID: 4, Authenticated: true, IsAdmin: true, IsAI: true}, http.StatusForbidden, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, recorder := newTestContext(http.MethodGet, "/api/v1/admin/users")
			if tc.viewer.Authenticated {
				httpx.SetViewer(c, tc.viewer)
			}
			h.RequireAdmin(c)
			if recorder.Code != tc.want {
				t.Fatalf("状态码 = %d，期望 %d", recorder.Code, tc.want)
			}
			if tc.wantOK && recorder.Body.Len() != 0 {
				t.Fatalf("放行时不应写响应: %s", recorder.Body.String())
			}
		})
	}
}

func TestRequireSuperAdminMatrix(t *testing.T) {
	h := New(nil)
	cases := []struct {
		name   string
		viewer model.Viewer
		want   int
	}{
		{"游客", model.GuestViewer(), http.StatusUnauthorized},
		{"普通管理员", model.Viewer{ID: 3, Authenticated: true, IsAdmin: true}, http.StatusForbidden},
		{"超级管理员", model.Viewer{ID: 5, Authenticated: true, IsAdmin: true, IsSuperAdmin: true}, http.StatusOK},
		{"AI 超管", model.Viewer{ID: 6, Authenticated: true, IsAdmin: true, IsSuperAdmin: true, IsAI: true}, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, recorder := newTestContext(http.MethodPost, "/api/v1/admin/users/1/grant-admin")
			if tc.viewer.Authenticated {
				httpx.SetViewer(c, tc.viewer)
			}
			h.RequireSuperAdmin(c)
			if recorder.Code != tc.want {
				t.Fatalf("状态码 = %d，期望 %d", recorder.Code, tc.want)
			}
			if tc.want == http.StatusForbidden {
				if code := errorCode(t, recorder); code != "forbidden" {
					t.Fatalf("错误码 = %q，期望 forbidden", code)
				}
			}
		})
	}
}

func TestRequireCSRFMatrix(t *testing.T) {
	h := New(nil)
	t.Run("无会话 403 csrf_failed", func(t *testing.T) {
		c, recorder := newTestContext(http.MethodPost, "/api/v1/auth/logout")
		h.RequireCSRF(c)
		if recorder.Code != http.StatusForbidden || errorCode(t, recorder) != "csrf_failed" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})
	t.Run("令牌不匹配 403 csrf_failed", func(t *testing.T) {
		c, recorder := newTestContext(http.MethodPost, "/api/v1/auth/logout")
		c.Request.Header.Set(CSRFHeader, "wrong")
		httpx.SetSession(c, model.Session{CSRFToken: "right"})
		h.RequireCSRF(c)
		if recorder.Code != http.StatusForbidden || errorCode(t, recorder) != "csrf_failed" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})
	t.Run("令牌匹配放行", func(t *testing.T) {
		c, recorder := newTestContext(http.MethodPost, "/api/v1/auth/logout")
		c.Request.Header.Set(CSRFHeader, "right")
		httpx.SetSession(c, model.Session{CSRFToken: "right"})
		h.RequireCSRF(c)
		if recorder.Body.Len() != 0 {
			t.Fatalf("放行时不应写响应: %s", recorder.Body.String())
		}
	})
}

func TestGuestCSRFMatrix(t *testing.T) {
	h := New(nil)
	t.Run("缺少 Cookie 403 csrf_failed", func(t *testing.T) {
		c, recorder := newTestContext(http.MethodPost, "/api/v1/auth/login")
		c.Request.Header.Set(CSRFHeader, "token")
		h.GuestCSRF(c)
		if recorder.Code != http.StatusForbidden || errorCode(t, recorder) != "csrf_failed" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})
	t.Run("双提交令牌匹配放行", func(t *testing.T) {
		c, recorder := newTestContext(http.MethodPost, "/api/v1/auth/login")
		c.Request.AddCookie(&http.Cookie{Name: GuestCSRFCookieName, Value: "token"})
		c.Request.Header.Set(CSRFHeader, "token")
		h.GuestCSRF(c)
		if recorder.Body.Len() != 0 {
			t.Fatalf("放行时不应写响应: %s", recorder.Body.String())
		}
	})
}

func TestResolveSessionWithoutCookie(t *testing.T) {
	h := New(nil)
	c, recorder := newTestContext(http.MethodGet, "/api/v1/timeline")
	h.ResolveSession(c)
	if recorder.Body.Len() != 0 {
		t.Fatalf("无 Cookie 时应按游客继续: %s", recorder.Body.String())
	}
	if state, ok := sessionStateOf(c); !ok || state != domain.SessionMissing {
		t.Fatalf("会话状态 = %q, ok=%v，期望 missing", state, ok)
	}
}
