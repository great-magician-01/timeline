package server

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/platform/db"
)

// registerHealth 注册 /health、/healthz 与 /readyz。
func (s *Server) registerHealth() {
	s.engine.GET("/health", s.handleHealthz)
	s.engine.GET("/healthz", s.handleHealthz)
	s.engine.GET("/readyz", s.handleReadyz)
}

// handleHealthz 存活检查：进程可响应即 200，不依赖数据库。
func (s *Server) handleHealthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// handleReadyz 就绪检查：连接池可 Ping 且迁移完成时 200，否则 503。
func (s *Server) handleReadyz(c *gin.Context) {
	database := "disconnected"
	migrations := "pending"
	if s.readiness != nil {
		if pool := s.readiness.Pool(); pool != nil {
			ctx, cancel := context.WithTimeout(c.Request.Context(), 3_000_000_000)
			if err := db.Ping(ctx, pool); err == nil {
				database = "ok"
			}
			cancel()
		}
		if s.readiness.MigrationsDone() {
			migrations = "done"
		}
	}
	if database == "ok" && migrations == "done" {
		c.JSON(http.StatusOK, gin.H{"status": "ready", "database": database, "migrations": migrations})
		return
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not-ready", "database": database, "migrations": migrations})
}
