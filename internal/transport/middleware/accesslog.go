package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// AccessLog 用 slog 记录访问日志（5xx→Error，4xx→Warn，其余 Info）。
// msg 固定为 "http"：text 格式把它压缩为「方法 路径 状态 耗时ms」摘要，json 格式保留完整结构化字段。
// 字段包含 method/path/status/latency_ms/ip/request_id；已登录请求额外带 user_id。
func AccessLog(logger *slog.Logger) gin.HandlerFunc {
	if logger == nil {
		logger = slog.Default()
	}
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
			"request_id", RequestIDFrom(c),
		}
		if viewer, ok := httpx.Viewer(c); ok && viewer.Authenticated {
			attrs = append(attrs, "user_id", httpx.FormatID(viewer.ID))
		}
		if len(c.Errors) > 0 {
			attrs = append(attrs, "err", c.Errors.Last().Error())
		}

		switch {
		case status >= 500:
			logger.Error("http", attrs...)
		case status >= 400:
			logger.Warn("http", attrs...)
		default:
			logger.Info("http", attrs...)
		}
	}
}
