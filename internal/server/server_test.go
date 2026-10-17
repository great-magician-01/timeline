package server_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/platform/config"
	"github.com/great-magician-01/timeline/internal/platform/db"
	"github.com/great-magician-01/timeline/internal/server"
	"github.com/great-magician-01/timeline/internal/transport/middleware"
	"github.com/great-magician-01/timeline/internal/transport/router"
)

// newSlimServer 构造不注册业务路由的 slim server（全局中间件/健康检查/前端托管），
// 用于不依赖数据库的 HTTP 行为测试。
func newSlimServer(t *testing.T, cfg *config.Config) *server.Server {
	t.Helper()
	return server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), db.NewReadiness())
}

func TestHealthDoesNotRequireDatabase(t *testing.T) {
	srv := newSlimServer(t, &config.Config{LogLevel: "debug"})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	resp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("/healthz status=%d body=%s", resp.Code, resp.Body.String())
	}
}

// TestBusinessRoutesOnlyUseGetAndPost 校验声明式路由表的基线与方法约束：
// 业务接口只允许 GET/POST，且关键写路由必须存在。
func TestBusinessRoutesOnlyUseGetAndPost(t *testing.T) {
	required := map[string]bool{
		"POST:/auth/login":                     false,
		"POST:/auth/password":                  false,
		"POST:/users/me":                       false,
		"POST:/users/me/follow-settings":       false,
		"POST:/admin/settings":                 false,
		"POST:/admin/users/:id/suspend":        false,
		"POST:/admin/users/:id/unsuspend":      false,
		"POST:/admin/users/:id/reset-password": false,
		"POST:/admin/users/:id/grant-admin":    false,
		"POST:/admin/users/:id/revoke-admin":   false,
	}
	for _, route := range router.Routes() {
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

// TestBusinessAPIWaitsForReadiness 校验就绪中间件：数据库/迁移未就绪时业务接口
// 统一返回 503 service_unavailable，且 API 响应禁止缓存。
func TestBusinessAPIWaitsForReadiness(t *testing.T) {
	readiness := db.NewReadiness() // 未 SetPool / 未 MarkMigrationsDone
	srv := server.New(&config.Config{LogLevel: "debug"},
		slog.New(slog.NewTextHandler(io.Discard, nil)), readiness)
	// 以与 transport/router 策略组一致的中间件链注册一条业务路径（同一引擎/同一全局中间件）。
	handled := false
	srv.Engine().Group("/api/v1", middleware.RequireReady(readiness)).
		GET("/users/alice", func(c *gin.Context) {
			handled = true
			c.Status(http.StatusOK)
		})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/alice", nil)
	resp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("未就绪业务接口 status=%d body=%s", resp.Code, resp.Body.String())
	}
	if handled {
		t.Fatal("未就绪时不应进入业务 handler")
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
	srv := newSlimServer(t, cfg)
	srv.Engine().GET("/client-ip-test", func(c *gin.Context) {
		c.String(http.StatusOK, c.ClientIP())
	})
	req := httptest.NewRequest(http.MethodGet, "/client-ip-test", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	resp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(resp, req)
	if got := resp.Body.String(); got != "203.0.113.9" {
		t.Fatalf("受信代理客户端 IP 解析错误: %q", got)
	}
}

func TestUntrustedProxyHeaderIgnored(t *testing.T) {
	srv := newSlimServer(t, &config.Config{LogLevel: "debug"})
	srv.Engine().GET("/client-ip-test", func(c *gin.Context) {
		c.String(http.StatusOK, c.ClientIP())
	})
	req := httptest.NewRequest(http.MethodGet, "/client-ip-test", nil)
	req.RemoteAddr = "192.0.2.1:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	resp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(resp, req)
	if got := resp.Body.String(); got != "192.0.2.1" {
		t.Fatalf("未配置受信代理时必须忽略转发头，got=%q", got)
	}
}
