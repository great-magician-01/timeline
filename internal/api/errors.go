package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

// Failure 是可以直接映射为统一错误响应的业务错误（docs/API.md §2.6）。
// 业务包抛出 Failure，由 Handler 统一交给 Respond 输出，避免每个分支重复组装 JSON。
type Failure struct {
	Status  int
	Code    string
	Message string
	Details any
}

func (f *Failure) Error() string { return f.Code + ": " + f.Message }

func NewFailure(status int, code, message string) *Failure {
	return &Failure{Status: status, Code: code, Message: message}
}

// ErrNotFound 用于不存在或不应暴露存在性的资源。
func ErrNotFound(message string) *Failure {
	return NewFailure(http.StatusNotFound, "not_found", message)
}

// ErrForbidden 用于已登录但操作不允许的场景。
func ErrForbidden(message string) *Failure {
	return NewFailure(http.StatusForbidden, "forbidden", message)
}

// ErrUnauthenticated 用于未登录。
func ErrUnauthenticated(message string) *Failure {
	return NewFailure(http.StatusUnauthorized, "unauthenticated", message)
}

// ErrInvalid 用于参数缺失或格式错误。
func ErrInvalid(message string) *Failure {
	return NewFailure(http.StatusBadRequest, "invalid_request", message)
}

// ErrValidation 用于业务校验失败。
func ErrValidation(message string) *Failure {
	return NewFailure(http.StatusBadRequest, "validation_failed", message)
}

// ErrConflict 用于业务冲突（如草稿版本冲突）。
func ErrConflict(code, message string) *Failure {
	return NewFailure(http.StatusConflict, code, message)
}

// ErrSuspended 用于停用账号的写操作。
func ErrSuspended(message string) *Failure {
	return NewFailure(http.StatusForbidden, "account_suspended", message)
}

// ErrRateLimited 用于限频。
func ErrRateLimited() *Failure {
	return NewFailure(http.StatusTooManyRequests, "rate_limited", "操作过于频繁，请稍后重试")
}

// WithDetails 附加字段级详情（供 validation_failed 使用）。
func (f *Failure) WithDetails(details any) *Failure {
	clone := *f
	clone.Details = details
	return &clone
}

// Respond 把业务错误写回响应。错误不是 Failure 时返回 false，由调用方按内部错误处理。
func Respond(c *gin.Context, err error) bool {
	var failure *Failure
	if !errors.As(err, &failure) {
		return false
	}
	ErrorWithDetails(c, failure.Status, failure.Code, failure.Message, failure.Details)
	return true
}
