package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync/atomic"

	"github.com/gin-gonic/gin"
)

const (
	// HeaderRequestID 是请求 ID 的回写响应头。
	HeaderRequestID = "X-Request-ID"
	// requestIDContextKey 是请求 ID 的 gin.Context 键。
	requestIDContextKey = "timeline.middleware.request_id"
)

// requestIDFallback 在熵源异常时提供进程内唯一兜底序号（不使用时间，满足时间位置约束）。
var requestIDFallback atomic.Uint64

// RequestID 为每个请求生成新的请求 ID，写入上下文并回写 X-Request-ID 响应头。
// 始终由服务端生成，不信任客户端传入值，避免日志注入与跨请求伪造。
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := newRequestID()
		c.Set(requestIDContextKey, id)
		c.Header(HeaderRequestID, id)
		c.Next()
	}
}

// RequestIDFrom 读取当前请求的请求 ID；未经过 RequestID 中间件时返回空字符串。
func RequestIDFrom(c *gin.Context) string {
	value, ok := c.Get(requestIDContextKey)
	if !ok {
		return ""
	}
	id, _ := value.(string)
	return id
}

// newRequestID 生成 32 位十六进制请求 ID；随机源失败时退化为进程内自增序号。
func newRequestID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err == nil {
		return hex.EncodeToString(buf[:])
	}
	return fmt.Sprintf("fallback-%d", requestIDFallback.Add(1))
}
