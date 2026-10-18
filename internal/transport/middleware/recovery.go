package middleware

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"syscall"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// Recovery 捕获请求处理过程中的 panic，用项目统一的 slog 日志器记录完整调用栈。
//
// 与 gin.Recovery 的差异：gin 默认把栈写到标准错误（进程 stderr），不进入应用日志
// 的文件通道；本中间件把 panic 值、请求标识（request_id/method/path/ip）和
// debug.Stack() 的完整栈（含 goroutine 头与 panic 现场帧）写入 ERROR 日志，
// 保证文件日志与标准输出都能直接排查。
//
// 行为约定：
//   - 响应尚未写出时返回统一 500 JSON（internal_error），不向客户端泄露 panic 细节；
//   - 响应已写出时只中止后续处理，不重复写状态码；
//   - 客户端断开（EPIPE/ECONNRESET）与 http.ErrAbortHandler 不是程序缺陷，
//     按 WARN 记录摘要、不打印栈。
//
// 必须位于全局中间件链最外层，才能兜住后续所有中间件与 handler 的 panic。
func Recovery(logger *slog.Logger) gin.HandlerFunc {
	if logger == nil {
		logger = slog.Default()
	}
	return func(c *gin.Context) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			attrs := requestLogAttrs(c)

			if err, ok := rec.(error); ok && isBrokenConnection(err) {
				logger.Warn("HTTP 请求处理中断（客户端连接已断开）", append(attrs, "err", err)...)
				_ = c.Error(err) // 与 gin.Recovery 保持一致：保留到上下文错误通道
				c.Abort()
				return
			}

			stack := string(debug.Stack())
			logger.Error("HTTP 请求处理 panic",
				append(attrs, "panic", formatPanicValue(rec), "stack", stack)...)
			if c.Writer.Written() {
				c.Abort()
				return
			}
			httpx.Error(c, http.StatusInternalServerError, "internal_error", "服务暂时异常，请稍后重试")
		}()
		c.Next()
	}
}

// maxPanicValueRunes 限制 panic 值写入日志的长度：panic 值不是诊断依据（栈才是），
// 异常大的 panic 值不应放大日志体积。
const maxPanicValueRunes = 2000

// formatPanicValue 把 panic 值格式化为有长度上限的文本。
func formatPanicValue(rec any) string {
	runes := []rune(fmt.Sprint(rec))
	if len(runes) <= maxPanicValueRunes {
		return string(runes)
	}
	return string(runes[:maxPanicValueRunes]) + "…（已截断）"
}

// requestLogAttrs 返回 panic 日志所需的请求摘要字段（与访问日志字段命名一致）。
func requestLogAttrs(c *gin.Context) []any {
	return []any{
		"method", c.Request.Method,
		"path", c.Request.URL.Path,
		"request_id", RequestIDFrom(c),
		"ip", c.ClientIP(),
	}
}

// isBrokenConnection 判断 panic 是否源于客户端连接断开；这类情况无法再写响应，
// 也不代表程序缺陷，不采集调用栈。
func isBrokenConnection(err error) bool {
	return errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, http.ErrAbortHandler)
}
