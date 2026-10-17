// Package apperr 定义跨模块共享的领域错误模型。
//
// domain 与 app 层只返回 *Error（或包装它的标准 error），由 transport/httpx
// 统一映射为 HTTP 状态码、错误码与 JSON 响应。本包不依赖 net/http，也不包含
// 任何业务规则：Kind 表达错误类别，Code 表达稳定的对外错误码。
package apperr

import (
	"errors"
	"strings"
	"time"
)

// Kind 是领域错误类别。HTTP 状态码映射由 transport/httpx 负责；本包只提供
// 默认对外错误码，保证与 docs/API.md 的既有错误码约定一致。
type Kind string

// Kind 枚举。Kind 字符串表达语义分类，具体对外错误码见 DefaultCode。
const (
	// KindNotFound：资源不存在，或不应对外暴露其存在性。
	KindNotFound Kind = "not_found"
	// KindForbidden：已认证但无权执行该操作。
	KindForbidden Kind = "forbidden"
	// KindUnauthenticated：未登录或会话已失效。
	KindUnauthenticated Kind = "unauthenticated"
	// KindInvalid：请求参数缺失或格式错误。
	KindInvalid Kind = "invalid"
	// KindValidation：业务校验失败。
	KindValidation Kind = "validation"
	// KindConflict：业务状态冲突（如草稿版本冲突、申请已处理）。
	KindConflict Kind = "conflict"
	// KindSuspended：账号已停用，拒绝一切写操作。
	KindSuspended Kind = "suspended"
	// KindRateLimited：触发限频。
	KindRateLimited Kind = "rate_limited"
	// KindUnavailable：依赖不可用或服务尚未就绪（如数据库未就绪）。
	KindUnavailable Kind = "unavailable"
	// KindInternal：未预期的内部错误，不向用户暴露细节。
	KindInternal Kind = "internal"
)

// Error 是领域错误。
//
//   - Kind：错误类别，决定统一响应中的 HTTP 状态码（映射在 transport/httpx）；
//   - Code：稳定的对外错误码，默认由 Kind 推导，也可用于业务细分码；
//   - Message：可直接展示给用户的简体中文文案；
//   - Details：字段级校验等结构化补充（JSON 形状由 transport 决定）；
//   - RetryAfter：限频/暂不可用场景的建议重试间隔；
//   - Err：底层原因，供日志与 errors.Is/As 使用。
type Error struct {
	Kind       Kind
	Code       string
	Message    string
	Details    any
	RetryAfter time.Duration
	Err        error
}

// Error 实现 error；格式为「错误码: 用户文案[: 底层原因]」，便于日志检索。
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	code := e.Code
	if code == "" {
		code = e.Kind.DefaultCode()
	}

	parts := make([]string, 0, 3)
	if code != "" {
		parts = append(parts, code)
	}
	if e.Message != "" {
		parts = append(parts, e.Message)
	} else if code == "" && e.Kind != "" {
		parts = append(parts, string(e.Kind))
	}
	if e.Err != nil {
		parts = append(parts, e.Err.Error())
	}
	return strings.Join(parts, ": ")
}

// Unwrap 暴露底层原因，使 errors.Is/errors.As 能穿透包装链。
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// DefaultCode 返回 Kind 对应的默认对外错误码；未知 Kind 返回空字符串。
// 这些错误码与 docs/API.md 及旧 internal/api 的既有约定保持一致。
func (k Kind) DefaultCode() string {
	switch k {
	case KindNotFound:
		return "not_found"
	case KindForbidden:
		return "forbidden"
	case KindUnauthenticated:
		return "unauthenticated"
	case KindInvalid:
		return "invalid_request"
	case KindValidation:
		return "validation_failed"
	case KindConflict:
		return "conflict"
	case KindSuspended:
		return "account_suspended"
	case KindRateLimited:
		return "rate_limited"
	case KindUnavailable:
		return "service_unavailable"
	case KindInternal:
		return "internal_error"
	default:
		return ""
	}
}

// New 创建领域错误；code 为空时使用 Kind 的默认错误码。
// 需要自定义错误码时优先用 New 或 WithCode，而不是直接拼装结构体。
func New(kind Kind, code, message string) *Error {
	if code == "" {
		code = kind.DefaultCode()
	}
	return &Error{Kind: kind, Code: code, Message: message}
}

// NotFound 用于资源不存在或不应暴露存在性的场景。
func NotFound(message string) *Error { return New(KindNotFound, "", message) }

// Forbidden 用于已登录但操作不允许的场景。
func Forbidden(message string) *Error { return New(KindForbidden, "", message) }

// Unauthenticated 用于未登录或会话失效的场景。
func Unauthenticated(message string) *Error { return New(KindUnauthenticated, "", message) }

// Invalid 用于参数缺失或格式错误。
func Invalid(message string) *Error { return New(KindInvalid, "", message) }

// Validation 用于业务校验失败。
func Validation(message string) *Error { return New(KindValidation, "", message) }

// Conflict 用于业务冲突；code 为具体冲突错误码（如 draft_version_conflict）。
func Conflict(code, message string) *Error { return New(KindConflict, code, message) }

// Suspended 用于停用账号的写操作。
func Suspended(message string) *Error { return New(KindSuspended, "", message) }

// RateLimited 用于限频；可选参数 retryAfter 为建议重试间隔，不传表示未提供。
func RateLimited(retryAfter ...time.Duration) *Error {
	e := New(KindRateLimited, "", "操作过于频繁，请稍后重试")
	if len(retryAfter) > 0 {
		e.RetryAfter = retryAfter[0]
	}
	return e
}

// Unavailable 用于依赖不可用或服务尚未就绪（如数据库未连接、迁移未完成）。
func Unavailable(message string) *Error { return New(KindUnavailable, "", message) }

// Internal 用于未预期的内部错误；返回给用户的文案不应包含敏感细节，底层原因用 Wrap 保留。
func Internal(message string) *Error { return New(KindInternal, "", message) }

// Wrap 把底层错误包装为指定 Kind 的领域错误，errors.Is/As 可穿透到 err。
// err 为 nil 时返回 nil；message 为空时 Error() 只输出错误码与底层原因。
func Wrap(err error, kind Kind, message string) *Error {
	if err == nil {
		return nil
	}
	e := New(kind, "", message)
	e.Err = err
	return e
}

// WithDetails 返回附加字段级详情的新错误，不修改原错误（可安全共享构造器结果）。
func (e *Error) WithDetails(details any) *Error {
	if e == nil {
		return nil
	}
	clone := *e
	clone.Details = details
	return &clone
}

// WithCode 返回覆盖对外错误码的新错误，不修改原错误。
func (e *Error) WithCode(code string) *Error {
	if e == nil {
		return nil
	}
	clone := *e
	clone.Code = code
	return &clone
}

// AsKind 从错误链中提取 *Error；err 为 nil、不含领域错误或只含 typed-nil 时返回 (nil, false)。
func AsKind(err error) (*Error, bool) {
	var target *Error
	if !errors.As(err, &target) || target == nil {
		return nil, false
	}
	return target, true
}

// IsKind 判断错误链上是否存在指定 Kind 的领域错误。
func IsKind(err error, kind Kind) bool {
	e, ok := AsKind(err)
	return ok && e.Kind == kind
}
