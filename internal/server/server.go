// Package server 负责 Gin 引擎与全局中间件、健康检查与前端静态托管。
// 业务路由不在这里注册：由组合根（internal/app）在 Engine() 上调用 transport/router。
package server

import (
	"log/slog"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/platform/config"
	"github.com/great-magician-01/timeline/internal/transport/middleware"
)

// Readiness 是 server 需要的运行时就绪状态能力；*platform/db.Readiness 直接满足，
// 测试可注入假实现（无需真实连接池）。
type Readiness interface {
	// Ready 报告连接池已设置且迁移已完成。
	Ready() bool
	// Pool 返回当前连接池；未连接时返回 nil。
	Pool() *pgxpool.Pool
	// MigrationsDone 报告迁移是否完成（或按配置跳过）。
	MigrationsDone() bool
}

// Server 持有 Gin 引擎与当前对外 handler（支持启动期切换）。
type Server struct {
	cfg       *config.Config
	log       *slog.Logger
	readiness Readiness
	engine    *gin.Engine

	mu      sync.RWMutex
	handler http.Handler
}

// New 构造 slim server：引擎 + 全局中间件 + 健康检查 + 前端托管。
// 业务路由由组合根通过 Engine() 注册后再 SwapHandler(app.Handler())。
func New(cfg *config.Config, log *slog.Logger, readiness Readiness) *Server {
	if log == nil {
		log = slog.Default()
	}
	if cfg.LogLevel != "debug" {
		gin.SetMode(gin.ReleaseMode)
	}
	s := &Server{cfg: cfg, log: log, readiness: readiness, engine: gin.New()}
	if err := s.engine.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		log.Error("受信代理配置无效，已回退为不信任代理头", "err", err)
		_ = s.engine.SetTrustedProxies(nil)
	}
	s.engine.Use(gin.Recovery(), middleware.AccessLog(log), middleware.SecurityHeaders(), middleware.RequestID())
	s.registerHealth()
	s.registerFrontend()
	s.handler = s.engine
	return s
}

// Engine 返回 Gin 引擎；组合根用它注册 /api/v1 路由。
func (s *Server) Engine() *gin.Engine { return s.engine }

// Handler 返回当前对外 handler（bootstrap 阶段为引擎本身，app 就绪后为完整引擎）。
func (s *Server) Handler() http.Handler {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.handler
}

// SwapHandler 原子替换对外 handler（app.New 完成后由 main 调用）。
func (s *Server) SwapHandler(handler http.Handler) {
	if handler == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handler = handler
}
