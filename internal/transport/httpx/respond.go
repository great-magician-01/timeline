// Package httpx 提供 HTTP 传输层共用的 JSON 错误响应、错误映射与请求参数约定。
//
// 目标架构中 platform/apperr 表达领域错误，本包是把领域错误映射为 HTTP 响应的唯一入口：
// handler 与中间件统一使用 Error/ErrorWithDetails/Respond，不再各自拼装 JSON。
package httpx

import (
	"errors"
	"math"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
)

// Error 按 docs/API.md 的统一结构返回错误。业务写接口只使用 POST。
func Error(c *gin.Context, status int, code, message string) {
	ErrorWithDetails(c, status, code, message, nil)
}

// ErrorWithDetails 返回带字段详情的统一错误；details 为空时省略该字段。
func ErrorWithDetails(c *gin.Context, status int, code, message string, details any) {
	body := gin.H{"code": code, "message": message}
	if details != nil {
		body["details"] = details
	}
	c.AbortWithStatusJSON(status, gin.H{"error": body})
}

// Respond 把领域错误写回响应。
//
// 映射顺序：
//  1. platform/apperr.Error：按 Kind 映射 HTTP 状态与默认错误码，Code/Message/Details
//     优先使用领域错误携带的值，RetryAfter 写入 Retry-After 响应头；
//  2. 迁移期兼容旧 internal/api.Failure（通过 legacy.go 的字段形状识别，不 import 旧包）；
//  3. 无法识别的错误返回 false，由调用方按内部错误处理（500 + 访问日志）。
//
// 与旧实现一致：err 为 nil 或无法识别时返回 false。
func Respond(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	var appErr *apperr.Error
	if errors.As(err, &appErr) && appErr != nil {
		status := statusForKind(appErr.Kind)
		code := appErr.Code
		if code == "" {
			code = appErr.Kind.DefaultCode()
		}
		if code == "" {
			code = "internal_error"
		}
		if appErr.RetryAfter > 0 {
			retryAfter := int(math.Ceil(appErr.RetryAfter.Seconds()))
			if retryAfter < 1 {
				retryAfter = 1
			}
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		message := appErr.Message
		if message == "" {
			message = defaultMessage(status)
		}
		ErrorWithDetails(c, status, code, message, appErr.Details)
		return true
	}
	if failure, ok := legacyFailureOf(err); ok {
		ErrorWithDetails(c, failure.status, failure.code, failure.message, failure.details)
		return true
	}
	return false
}

// statusForKind 把领域错误类别映射为 HTTP 状态码（与旧 api.Failure 构造器一致）。
// 这里是 Kind→HTTP 的唯一映射点；错误码默认值由 apperr.Kind.DefaultCode 提供。
func statusForKind(kind apperr.Kind) int {
	switch kind {
	case apperr.KindNotFound:
		return http.StatusNotFound
	case apperr.KindForbidden, apperr.KindSuspended:
		return http.StatusForbidden
	case apperr.KindUnauthenticated:
		return http.StatusUnauthorized
	case apperr.KindInvalid, apperr.KindValidation:
		return http.StatusBadRequest
	case apperr.KindConflict:
		return http.StatusConflict
	case apperr.KindRateLimited:
		return http.StatusTooManyRequests
	case apperr.KindUnavailable:
		return http.StatusServiceUnavailable
	case apperr.KindInternal:
		return http.StatusInternalServerError
	default:
		return http.StatusInternalServerError
	}
}

// defaultMessage 为领域错误未携带 message 的兜底文案（简体中文，与旧实现风格一致）。
func defaultMessage(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return "请先登录"
	case http.StatusForbidden:
		return "操作不允许"
	case http.StatusNotFound:
		return "内容不存在或无权访问"
	case http.StatusTooManyRequests:
		return "操作过于频繁，请稍后重试"
	case http.StatusServiceUnavailable:
		return "服务尚未就绪，请稍后重试"
	case http.StatusBadRequest:
		return "请求参数无效"
	case http.StatusConflict:
		return "资源状态冲突"
	default:
		return "服务暂时异常，请稍后重试"
	}
}
