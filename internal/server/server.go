// Package server 提供 HTTP 服务（基于 Gin）：健康检查、就绪检查与前端静态托管（含 SPA 回退）。
// 路径约定（docs/API.md §11）：/healthz 与 /readyz 不参与业务，不被 SPA 回退覆盖；
// 未注册的 /api 路径返回 JSON 404，不落入前端回退。
package server

import (
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/config"
	"github.com/great-magician-01/timeline/internal/db"
	"github.com/great-magician-01/timeline/web"
)

// State 保存运行期就绪状态，供 /readyz 查询。
type State struct {
	mu             sync.RWMutex
	pool           *pgxpool.Pool
	migrationsDone bool
}

func NewState() *State { return &State{} }

func (s *State) SetPool(p *pgxpool.Pool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pool = p
}

func (s *State) Pool() *pgxpool.Pool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pool
}

func (s *State) MarkMigrationsDone() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.migrationsDone = true
}

func (s *State) MigrationsDone() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.migrationsDone
}

type Server struct {
	cfg    *config.Config
	log    *slog.Logger
	state  *State
	engine *gin.Engine
}

func New(cfg *config.Config, log *slog.Logger, state *State) *Server {
	if cfg.LogLevel != "debug" {
		gin.SetMode(gin.ReleaseMode)
	}

	s := &Server{cfg: cfg, log: log, state: state, engine: gin.New()}
	// 不信任任何代理头（单机直连场景）；接入反向代理后再按实际配置
	_ = s.engine.SetTrustedProxies(nil)
	s.engine.Use(gin.Recovery(), s.requestLogger())

	s.engine.GET("/healthz", s.handleHealthz)
	s.engine.GET("/readyz", s.handleReadyz)

	// 业务接口将挂载在 /api/v1 下；骨架期未注册的 /api 路径由 NoRoute 统一返回 JSON 404
	s.registerFrontend()

	return s
}

func (s *Server) Handler() http.Handler { return s.engine }

// requestLogger 用 slog 记录访问日志（5xx→Error，4xx→Warn，其余 Info）。
func (s *Server) requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		status := c.Writer.Status()
		attrs := []any{
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", status,
			"latency_ms", time.Since(start).Milliseconds(),
			"ip", c.ClientIP(),
		}
		if len(c.Errors) > 0 {
			attrs = append(attrs, "err", c.Errors.Last().Error())
		}
		switch {
		case status >= 500:
			s.log.Error("http", attrs...)
		case status >= 400:
			s.log.Warn("http", attrs...)
		default:
			s.log.Info("http", attrs...)
		}
	}
}

func (s *Server) handleHealthz(c *gin.Context) {
	// 存活检查：进程可响应即 200（容器健康检查用）
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (s *Server) handleReadyz(c *gin.Context) {
	pool := s.state.Pool()
	database := "disconnected"
	if pool != nil {
		if err := db.Ping(c.Request.Context(), pool); err == nil {
			database = "ok"
		}
	}
	migrations := "pending"
	if s.state.MigrationsDone() {
		migrations = "done"
	}

	if database == "ok" && migrations == "done" {
		c.JSON(http.StatusOK, gin.H{"status": "ready", "database": database, "migrations": migrations})
		return
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not-ready", "database": database, "migrations": migrations})
}

// registerFrontend 注册前端托管与 SPA 回退。
func (s *Server) registerFrontend() {
	dist, built := web.Dist()
	if !built {
		s.log.Warn("前端产物未构建（web/dist 无 index.html），使用内置占位页")
		s.engine.NoRoute(func(c *gin.Context) {
			c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(placeholderHTML))
		})
		return
	}

	s.engine.GET("/", func(c *gin.Context) { serveIndex(c, dist) })
	s.engine.NoRoute(func(c *gin.Context) {
		p := strings.TrimPrefix(path.Clean("/"+c.Request.URL.Path), "/")
		if p == "api" || strings.HasPrefix(p, "api/") {
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"code": "not_found", "message": "接口不存在"}})
			return
		}
		// 命中真实静态文件则直接返回（FileFromFS 按原始请求路径从 dist 读取）
		if f, err := fs.Stat(dist, p); err == nil && !f.IsDir() {
			c.FileFromFS(c.Request.URL.Path, http.FS(dist))
			return
		}
		// 其余路径回退到 SPA 入口
		serveIndex(c, dist)
	})
}

func serveIndex(c *gin.Context, dist fs.FS) {
	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		c.String(http.StatusInternalServerError, "index.html 缺失")
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", index)
}

const placeholderHTML = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><title>时间线</title></head>
<body style="font-family:sans-serif;padding:2rem">
<h1>时间线</h1>
<p>前端尚未构建（web/dist 为空）。开发模式请运行 <code>cd web && npm run dev</code>，或在 web 目录执行 <code>npm run build</code> 后重启本服务。</p>
<p>后端状态：<a href="/healthz">/healthz</a> · <a href="/readyz">/readyz</a></p>
</body></html>`
