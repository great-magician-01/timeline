package server_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/platform/config"
	"github.com/great-magician-01/timeline/internal/platform/db"
	"github.com/great-magician-01/timeline/internal/server"
	"github.com/great-magician-01/timeline/internal/transport/middleware"
	"github.com/great-magician-01/timeline/internal/transport/router"
)

// discardLogger 返回丢弃输出的测试日志器。
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeReadiness 是 server.Readiness 的假实现，用于不连接数据库地覆盖就绪分支。
type fakeReadiness struct {
	ready      bool
	pool       *pgxpool.Pool
	migrations bool
}

func (f fakeReadiness) Ready() bool          { return f.ready }
func (f fakeReadiness) Pool() *pgxpool.Pool  { return f.pool }
func (f fakeReadiness) MigrationsDone() bool { return f.migrations }

// newSlimServer 构造不注册业务路由的 slim server（全局中间件/健康检查/前端托管），
// 用于不依赖数据库的 HTTP 行为测试。
func newSlimServer(t *testing.T, cfg *config.Config) *server.Server {
	t.Helper()
	return server.New(cfg, discardLogger(), db.NewReadiness())
}

// TestBootstrapAPIUnavailableReturns503 覆盖启动早期（DB/迁移未就绪）未知 /api 路径
// 返回 503 service_unavailable，而不是误导性的 404。
func TestBootstrapAPIUnavailableReturns503(t *testing.T) {
	srv := server.New(&config.Config{LogLevel: "debug"}, discardLogger(), fakeReadiness{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/config", nil)
	resp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("未就绪 /api status=%d body=%s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "service_unavailable") {
		t.Fatalf("未就绪 /api 错误码不符: %s", resp.Body.String())
	}
}

// TestUnknownAPIAfterReadyReturns404 覆盖就绪后未知 /api 路径仍返回 JSON 404。
func TestUnknownAPIAfterReadyReturns404(t *testing.T) {
	srv := server.New(&config.Config{LogLevel: "debug"}, discardLogger(), fakeReadiness{ready: true, migrations: true})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/not-exists", nil)
	resp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("就绪后未知 /api status=%d body=%s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "not_found") {
		t.Fatalf("就绪后未知 /api 错误码不符: %s", resp.Body.String())
	}
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

// TestSwapHandlerActivatesFullEngine 覆盖启动期 handler 切换：
// bootstrap 引擎只含健康检查/前端托管，SwapHandler 后对外切换为组合根装配的完整引擎，
// 且原引擎上的路由不会被意外暴露（避免"边服务边注册路由"的竞态设计）。
func TestSwapHandlerActivatesFullEngine(t *testing.T) {
	cfg := &config.Config{LogLevel: "debug"}
	bootstrap := server.New(cfg, discardLogger(), fakeReadiness{})
	full := server.New(cfg, discardLogger(), fakeReadiness{ready: true, migrations: true})
	full.Engine().GET("/api/v1/full-only", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	do := func(handler http.Handler, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		return resp
	}

	if got := do(bootstrap.Handler(), "/api/v1/full-only").Code; got == http.StatusOK {
		t.Fatalf("切换前 bootstrap 不应提供完整引擎的业务路由，status=%d", got)
	}
	bootstrap.SwapHandler(full.Handler())
	if got := do(bootstrap.Handler(), "/api/v1/full-only").Code; got != http.StatusOK {
		t.Fatalf("SwapHandler 后应使用完整引擎，status=%d", got)
	}
}

// TestHTTPHandlerFollowsHandlerSwap 复现启动期真实监听路径：http.Server 拿到的是
// Server 本身（动态读取当前 handler），而不是构造时的 Handler() 快照。
// 快照 bug 会让 SwapHandler 后的业务接口全部 404（bootstrap 引擎的未知 /api 兜底）。
func TestHTTPHandlerFollowsHandlerSwap(t *testing.T) {
	cfg := &config.Config{LogLevel: "debug"}
	bootstrap := server.New(cfg, discardLogger(), fakeReadiness{})
	full := server.New(cfg, discardLogger(), fakeReadiness{ready: true, migrations: true})
	full.Engine().GET("/api/v1/full-only", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	ts := httptest.NewServer(bootstrap)
	defer ts.Close()

	get := func(path string) int {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("请求 %s 失败: %v", path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode
	}

	if got := get("/api/v1/full-only"); got != http.StatusServiceUnavailable {
		t.Fatalf("切换前 bootstrap 引擎未就绪应返回 503，status=%d", got)
	}
	bootstrap.SwapHandler(full.Handler())
	if got := get("/api/v1/full-only"); got != http.StatusOK {
		t.Fatalf("SwapHandler 后应命中完整引擎，status=%d（404 说明 http.Server 持有旧 Handler 快照）", got)
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

// TestPanicRecoveredAsJSONInternalError 覆盖全局 Recovery 接线：请求处理 panic 被捕获，
// 返回统一 JSON 500（gin.Recovery 只会返回空 500），错误栈由中间件写入应用日志。
func TestPanicRecoveredAsJSONInternalError(t *testing.T) {
	srv := newSlimServer(t, &config.Config{LogLevel: "debug"})
	srv.Engine().GET("/panic-test", func(*gin.Context) { panic("接线验证") })

	req := httptest.NewRequest(http.MethodGet, "/panic-test", nil)
	resp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("panic 应返回 500，实际 %d body=%s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "internal_error") {
		t.Fatalf("panic 应返回统一 JSON 错误: %s", resp.Body.String())
	}
}
