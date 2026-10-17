package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// SecurityHeaders 为所有响应追加基础安全响应头，并对 /api/ 关闭缓存。
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "same-origin")
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.Header("Cache-Control", "no-store")
		}
		c.Next()
	}
}
