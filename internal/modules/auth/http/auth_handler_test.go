package http

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/modules/auth/app"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

var (
	zeroOnce sync.Once
	zeroSvc  *app.Service
)

// zeroService 是没有装配仓储的 app.Service：用于只触发参数校验/Cookie 路径的测试。
// 复用同一实例，避免重复构造 bcrypt dummy 哈希拖慢测试。
func zeroService() *app.Service {
	zeroOnce.Do(func() { zeroSvc = app.New(app.Deps{}) })
	return zeroSvc
}

func TestRegisterRejectsInvalidJSON(t *testing.T) {
	h := New(zeroService())
	for _, body := range []string{`{`, `{"username":"a","unknown":1}`} {
		c, recorder := newJSONContext(http.MethodPost, "/api/v1/auth/register", body)
		h.Register(c)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("body=%s 状态码 = %d，期望 400", body, recorder.Code)
		}
		if code := errorCode(t, recorder); code != "invalid_request" {
			t.Fatalf("错误码 = %q，期望 invalid_request", code)
		}
	}
}

func TestLoginRejectsInvalidJSON(t *testing.T) {
	h := New(zeroService())
	c, recorder := newJSONContext(http.MethodPost, "/api/v1/auth/login", `not-json`)
	h.Login(c)
	if recorder.Code != http.StatusBadRequest || errorCode(t, recorder) != "invalid_request" {
		t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestCSRFGuestIssuesDoubleSubmitCookie(t *testing.T) {
	h := New(zeroService())
	c, recorder := newTestContext(http.MethodGet, "/api/v1/auth/csrf")
	h.CSRF(c)
	if recorder.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", recorder.Code)
	}
	var body struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if body.CSRFToken == "" {
		t.Fatal("游客 CSRF 令牌不能为空")
	}
	cookie := recorder.Header().Get("Set-Cookie")
	if !strings.Contains(cookie, GuestCSRFCookieName+"="+body.CSRFToken) {
		t.Fatalf("Set-Cookie 未写入游客令牌: %s", cookie)
	}
	for _, attr := range []string{"HttpOnly", "Path=" + guestCSRFCookiePath, "SameSite=Lax"} {
		if !strings.Contains(cookie, attr) {
			t.Fatalf("Set-Cookie 缺少属性 %q: %s", attr, cookie)
		}
	}
}

func TestCSRFReturnsSessionTokenWhenLoggedIn(t *testing.T) {
	h := New(zeroService())
	c, recorder := newTestContext(http.MethodGet, "/api/v1/auth/csrf")
	httpx.SetViewer(c, model.Viewer{ID: 1, Authenticated: true})
	httpx.SetSession(c, model.Session{ID: 1, UserID: 1, CSRFToken: "session-csrf"})
	h.CSRF(c)
	if recorder.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "session-csrf") {
		t.Fatalf("应返回会话 CSRF 令牌，body=%s", recorder.Body.String())
	}
	if strings.Contains(recorder.Header().Get("Set-Cookie"), GuestCSRFCookieName) {
		t.Fatal("已登录请求不应再签发游客 CSRF Cookie")
	}
}

func TestConfigFallsBackToDefaults(t *testing.T) {
	h := New(zeroService())
	c, recorder := newTestContext(http.MethodGet, "/api/v1/auth/config")
	h.Config(c)
	if recorder.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200，body=%s", recorder.Code, recorder.Body.String())
	}
	var body configResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if !body.RegistrationEnabled {
		t.Fatal("设置缺失时应回退默认开放注册")
	}
	if body.BootstrapRequired {
		t.Fatal("初始化状态查询失败时应回退 false")
	}
}

func TestMeRequiresViewer(t *testing.T) {
	h := New(zeroService())
	c, recorder := newTestContext(http.MethodGet, "/api/v1/auth/me")
	h.Me(c)
	if recorder.Code != http.StatusUnauthorized || errorCode(t, recorder) != "unauthenticated" {
		t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestLogoutRequiresSession(t *testing.T) {
	h := New(zeroService())
	c, recorder := newTestContext(http.MethodPost, "/api/v1/auth/logout")
	h.Logout(c)
	if recorder.Code != http.StatusUnauthorized || errorCode(t, recorder) != "unauthenticated" {
		t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestUpdateFollowSettingsRequiresField(t *testing.T) {
	h := New(zeroService())
	c, recorder := newJSONContext(http.MethodPost, "/api/v1/users/me/follow-settings", `{}`)
	httpx.SetViewer(c, model.Viewer{ID: 1, Authenticated: true})
	h.UpdateFollowSettings(c)
	if recorder.Code != http.StatusBadRequest || errorCode(t, recorder) != "validation_failed" {
		t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestGrantAdminRejectsInvalidID(t *testing.T) {
	h := New(zeroService())
	c, recorder := newTestContext(http.MethodPost, "/api/v1/admin/users/abc/grant-admin")
	c.Params = gin.Params{{Key: "id", Value: "abc"}}
	httpx.SetViewer(c, model.Viewer{ID: 1, Authenticated: true, IsAdmin: true, IsSuperAdmin: true})
	h.GrantAdmin(c)
	if recorder.Code != http.StatusBadRequest || errorCode(t, recorder) != "invalid_request" {
		t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestGetProfileTreatsGuestAsViewer(t *testing.T) {
	h := New(zeroService())
	c, recorder := newTestContext(http.MethodGet, "/api/v1/users/alice")
	// 无 viewer 上下文；app 未装配仓储会返回 Internal，这里只验证不会 panic 且走内部错误路径。
	h.GetProfile(c)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("状态码 = %d，期望 500（未装配仓储）", recorder.Code)
	}
}
