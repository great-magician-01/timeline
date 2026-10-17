package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/config"
	"github.com/great-magician-01/timeline/internal/jobs"
	"github.com/great-magician-01/timeline/internal/storage"
)

type testIDs struct{ next int64 }

func (g *testIDs) Next() (int64, error) {
	g.next++
	return g.next, nil
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := &config.Config{LogLevel: "debug", CookieSecure: false}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return newTestServerWith(t, cfg, log)
}

func newTestServerWith(t *testing.T, cfg *config.Config, log *slog.Logger) *Server {
	t.Helper()
	state := NewState()
	ids := &testIDs{}
	runner := jobs.NewRunner(state, ids, log)
	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatalf("初始化测试存储失败: %v", err)
	}
	return New(cfg, log, state, ids, runner, store)
}

func TestHealthDoesNotRequireDatabase(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	resp := httptest.NewRecorder()
	s.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("/healthz status=%d body=%s", resp.Code, resp.Body.String())
	}
}

func TestBusinessRoutesOnlyUseGetAndPost(t *testing.T) {
	s := newTestServer(t)
	required := map[string]bool{
		"POST:/api/v1/auth/login":                     false,
		"POST:/api/v1/auth/password":                  false,
		"POST:/api/v1/users/me":                       false,
		"POST:/api/v1/users/me/follow-settings":       false,
		"POST:/api/v1/admin/settings":                 false,
		"POST:/api/v1/admin/users/:id/suspend":        false,
		"POST:/api/v1/admin/users/:id/unsuspend":      false,
		"POST:/api/v1/admin/users/:id/reset-password": false,
		"POST:/api/v1/admin/users/:id/grant-admin":    false,
		"POST:/api/v1/admin/users/:id/revoke-admin":   false,
	}
	for _, route := range s.engine.Routes() {
		if !strings.HasPrefix(route.Path, "/api/v1") {
			continue
		}
		if route.Method != http.MethodGet && route.Method != http.MethodPost {
			t.Errorf("业务接口使用了禁用方法: %s %s", route.Method, route.Path)
		}
		key := route.Method + ":" + route.Path
		if _, ok := required[key]; ok {
			required[key] = true
		}
	}
	for route, found := range required {
		if !found {
			t.Errorf("缺少预期路由 %s", route)
		}
	}
}

func TestBusinessAPIWaitsForReadiness(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/alice", nil)
	resp := httptest.NewRecorder()
	s.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("未就绪业务接口 status=%d body=%s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "service_unavailable") {
		t.Fatalf("未就绪错误格式错误: %s", resp.Body.String())
	}
	if got := resp.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("API 响应必须禁止缓存，Cache-Control=%q", got)
	}
}

func TestTrustedProxyClientIP(t *testing.T) {
	cfg := &config.Config{LogLevel: "debug", TrustedProxies: []string{"127.0.0.1"}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := newTestServerWith(t, cfg, log)
	s.engine.GET("/client-ip-test", func(c *gin.Context) {
		c.String(http.StatusOK, c.ClientIP())
	})
	req := httptest.NewRequest(http.MethodGet, "/client-ip-test", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	resp := httptest.NewRecorder()
	s.Handler().ServeHTTP(resp, req)
	if got := resp.Body.String(); got != "203.0.113.9" {
		t.Fatalf("受信代理客户端 IP 解析错误: %q", got)
	}
}

func TestUntrustedProxyHeaderIgnored(t *testing.T) {
	s := newTestServer(t)
	s.engine.GET("/client-ip-test", func(c *gin.Context) {
		c.String(http.StatusOK, c.ClientIP())
	})
	req := httptest.NewRequest(http.MethodGet, "/client-ip-test", nil)
	req.RemoteAddr = "192.0.2.1:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	resp := httptest.NewRecorder()
	s.Handler().ServeHTTP(resp, req)
	if got := resp.Body.String(); got != "192.0.2.1" {
		t.Fatalf("未配置受信代理时必须忽略转发头，got=%q", got)
	}
}
