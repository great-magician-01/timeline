package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/platform/config"
	"github.com/great-magician-01/timeline/web"
)

// stubReadiness 是 server.Readiness 的假实现：连接池始终为 nil，只控制迁移标记，
// 用于覆盖 /readyz 的未就绪分支与未知 /api 的 503/404 分支（不需要真实数据库）。
type stubReadiness struct {
	ready      bool
	migrations bool
}

func (s stubReadiness) Ready() bool          { return s.ready }
func (s stubReadiness) Pool() *pgxpool.Pool  { return nil }
func (s stubReadiness) MigrationsDone() bool { return s.migrations }

// discardLogger 返回丢弃输出的测试日志器。
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestContext 构造独立的 Gin 上下文与响应记录器。
func newTestContext(method, target string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, target, nil)
	return ctx, recorder
}

// TestIsAPIRequest 校验 /api 前缀判定：只有 api 及其子路径算 API，
// 相似前缀（/apifoo）、清理后越出 api 的路径都不算，避免 SPA 回退吞掉接口。
func TestIsAPIRequest(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/api", true},
		{"/api/", true},
		{"/api/v1", true},
		{"/api/v1/auth/config", true},
		{"api/v1", true},
		{"/", false},
		{"/posts/1", false},
		{"/healthz", false},
		{"/apifoo", false},
		{"/apiary/users", false},
		{"/api/../users", false},
		{"/assets/app.js", false},
	}
	for _, tc := range cases {
		if got := isAPIRequest(tc.path); got != tc.want {
			t.Errorf("isAPIRequest(%q) = %v，期望 %v", tc.path, got, tc.want)
		}
	}
}

// TestServeIndex 校验 SPA 入口读取：存在 index.html 时按 HTML 返回，缺失时 500。
func TestServeIndex(t *testing.T) {
	ctx, recorder := newTestContext(http.MethodGet, "/")
	serveIndex(ctx, fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html>timeline</html>")},
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d，期望 200", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Fatalf("Content-Type=%q，期望 text/html", got)
	}
	if body := recorder.Body.String(); body != "<html>timeline</html>" {
		t.Fatalf("index.html 内容不符: %q", body)
	}

	ctx, recorder = newTestContext(http.MethodGet, "/")
	serveIndex(ctx, fstest.MapFS{})
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("缺少 index.html 时 status=%d，期望 500", recorder.Code)
	}
}

// TestNotFoundAPI 校验未知 API 的 JSON 404 形状（错误码与中文提示）。
func TestNotFoundAPI(t *testing.T) {
	ctx, recorder := newTestContext(http.MethodGet, "/api/v1/nope")
	notFoundAPI(ctx)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status=%d，期望 404", recorder.Code)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"not_found"`) || !strings.Contains(body, "接口不存在") {
		t.Fatalf("404 响应形状不符: %s", body)
	}
}

// TestHandleUnknownAPIReadiness 校验未知 /api 路径：未就绪 503、就绪 404、nil 就绪状态按未就绪。
func TestHandleUnknownAPIReadiness(t *testing.T) {
	cases := []struct {
		name      string
		readiness Readiness
		wantCode  int
		wantCode2 string
	}{
		{"nil readiness 视为未就绪", nil, http.StatusServiceUnavailable, "service_unavailable"},
		{"未就绪 503", stubReadiness{}, http.StatusServiceUnavailable, "service_unavailable"},
		{"就绪后 404", stubReadiness{ready: true, migrations: true}, http.StatusNotFound, "not_found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := &Server{readiness: tc.readiness, log: discardLogger()}
			ctx, recorder := newTestContext(http.MethodGet, "/api/v1/nope")
			srv.handleUnknownAPI(ctx)
			if recorder.Code != tc.wantCode {
				t.Fatalf("status=%d，期望 %d，body=%s", recorder.Code, tc.wantCode, recorder.Body.String())
			}
			if !strings.Contains(recorder.Body.String(), tc.wantCode2) {
				t.Fatalf("错误码不符，body=%s", recorder.Body.String())
			}
		})
	}
}

// TestReadyzReportsNotReady 校验 /readyz 未就绪分支：连接池缺失或迁移未完成时 503，
// 并分别报告 database/migrations 状态。
func TestReadyzReportsNotReady(t *testing.T) {
	cases := []struct {
		name      string
		readiness Readiness
		wantDB    string
		wantMigr  string
	}{
		{"nil readiness", nil, "disconnected", "pending"},
		{"未连接未迁移", stubReadiness{}, "disconnected", "pending"},
		{"未连接但迁移完成", stubReadiness{migrations: true}, "disconnected", "done"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := &Server{readiness: tc.readiness, log: discardLogger()}
			ctx, recorder := newTestContext(http.MethodGet, "/readyz")
			srv.handleReadyz(ctx)
			if recorder.Code != http.StatusServiceUnavailable {
				t.Fatalf("status=%d，期望 503", recorder.Code)
			}
			body := recorder.Body.String()
			if !strings.Contains(body, tc.wantDB) || !strings.Contains(body, tc.wantMigr) {
				t.Fatalf("readyz 状态不符: %s", body)
			}
		})
	}
}

// TestBuiltFrontendServesSPAAndAssets 覆盖前端已构建时的托管行为：
// / 与 SPA 路由回退到 index.html，dist 内静态资源按文件返回。
// 未构建（无 index.html）时跳过，由占位页分支覆盖。
func TestBuiltFrontendServesSPAAndAssets(t *testing.T) {
	if _, built := web.Dist(); !built {
		t.Skip("web/dist 未构建（缺少 index.html），本用例只覆盖已构建分支")
	}
	srv := New(&config.Config{LogLevel: "debug"}, discardLogger(), stubReadiness{ready: true})
	do := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		resp := httptest.NewRecorder()
		srv.Handler().ServeHTTP(resp, req)
		return resp
	}

	for _, path := range []string{"/", "/posts/123", "/users/alice"} {
		resp := do(path)
		if resp.Code != http.StatusOK {
			t.Fatalf("%s status=%d，期望 SPA 回退 200", path, resp.Code)
		}
		if contentType := resp.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/html") {
			t.Fatalf("%s Content-Type=%q，期望 text/html", path, contentType)
		}
	}

	// 已构建产物中的静态文件直接由 dist 提供（favicon 是仓库内固定文件）。
	resp := do("/favicon.svg")
	if resp.Code != http.StatusOK {
		t.Fatalf("/favicon.svg status=%d，期望 200", resp.Code)
	}
	if contentType := resp.Header().Get("Content-Type"); !strings.Contains(contentType, "svg") {
		t.Fatalf("/favicon.svg Content-Type=%q，期望 svg", contentType)
	}
}
