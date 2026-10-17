// Package middleware 提供与业务无关的通用 Gin 中间件：
// 就绪检查、安全响应头、访问日志与请求 ID。
package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// Readiness 表示运行时是否已就绪（连接池可用且迁移完成）。
// *platform/db.Readiness 直接满足该接口。
type Readiness interface {
	Ready() bool
}

// RequireReady 在服务尚未就绪时返回 503 service_unavailable，避免请求落到尚未创建的
// 业务表。readiness 为 nil 时同样视为未就绪。
func RequireReady(readiness Readiness) gin.HandlerFunc {
	return func(c *gin.Context) {
		if readiness == nil || !readiness.Ready() {
			httpx.Error(c, http.StatusServiceUnavailable, "service_unavailable", "服务尚未就绪，请稍后重试")
			return
		}
		c.Next()
	}
}
