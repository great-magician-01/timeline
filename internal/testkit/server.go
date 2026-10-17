package testkit

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/app"
	"github.com/great-magician-01/timeline/internal/platform/db"
	"github.com/great-magician-01/timeline/internal/server"
)

// ServerOptions 控制测试服务装配。
type ServerOptions struct {
	// StartRunner 为 true 时启动作业消费与周期任务（旧集成测试不启动，默认 false）。
	StartRunner bool
}

// Server 是完整测试服务：隔离 schema 基座 + 组合根 App + httptest 服务。
type Server struct {
	*Env
	App     *app.App
	BaseURL string
	Engine  *gin.Engine

	httpServer *httptest.Server
}

// NewIntegrationServer 用共享测试 schema（timeline_test/user_test）构造完整 HTTP 服务。
// 未设置 TIMELINE_INTEGRATION=1 时跳过；共享 schema 会自动迁移、恢复注册开关并幂等准备测试管理员。
func NewIntegrationServer(t *testing.T) *Server {
	t.Helper()
	SkipUnlessIntegration(t)
	env := NewEnv(t, SharedEnvOptions())
	return NewServerFromEnv(t, env, ServerOptions{})
}

// NewIsolatedServer 用一次性空库 schema 构造完整 HTTP 服务（首注册引导等用例）。
// schema 创建需要数据库级 CREATE 权限；权限不足时按环境限制 Skip。
func NewIsolatedServer(t *testing.T, prefix string) *Server {
	t.Helper()
	SkipUnlessIntegration(t)
	env := NewEnv(t, IsolatedEnvOptions(prefix))
	return NewServerFromEnv(t, env, ServerOptions{})
}

// NewServerFromEnv 用给定 Env 构造完整 HTTP 服务：
// readiness → slim server 引擎（全局中间件/健康检查/前端托管）→ app.New（装配模块与路由）→ httptest。
func NewServerFromEnv(t *testing.T, env *Env, options ServerOptions) *Server {
	t.Helper()
	readiness := db.NewReadiness()
	readiness.SetPool(env.Pool)
	readiness.MarkMigrationsDone()

	srv := server.New(env.Config, env.Log, readiness)
	application, err := app.New(env.Config, env.Log, readiness, env.IDs, env.Store, srv.Engine())
	if err != nil {
		t.Fatalf("装配测试应用失败: %v", err)
	}
	t.Cleanup(application.Close)
	srv.SwapHandler(application.Handler())

	httpServer := httptest.NewServer(srv.Handler())
	t.Cleanup(httpServer.Close)

	server := &Server{
		Env:        env,
		App:        application,
		BaseURL:    httpServer.URL,
		Engine:     srv.Engine(),
		httpServer: httpServer,
	}
	if options.StartRunner {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		application.Start(ctx)
	}
	return server
}

// Client 创建一个新的测试 HTTP 客户端（独立 Cookie jar 与 CSRF 令牌）。
func (s *Server) Client(t *testing.T) *Client {
	t.Helper()
	return NewClient(t, s.BaseURL)
}

// Routes 返回已注册的全部 Gin 路由，供路由矩阵/CSRF 覆盖断言使用。
func (s *Server) Routes() []gin.RouteInfo { return s.Engine.Routes() }

// HTTPClient 返回底层 httptest 服务，供需要自定义 handler 的测试使用。
func (s *Server) HTTPClient() *httptest.Server { return s.httpServer }
