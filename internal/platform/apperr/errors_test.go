package apperr_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
)

// TestKindDefaultCode 保证每个 Kind 都有非空且唯一的默认错误码。
func TestKindDefaultCode(t *testing.T) {
	cases := []struct {
		kind apperr.Kind
		code string
	}{
		{apperr.KindNotFound, "not_found"},
		{apperr.KindForbidden, "forbidden"},
		{apperr.KindUnauthenticated, "unauthenticated"},
		{apperr.KindInvalid, "invalid_request"},
		{apperr.KindValidation, "validation_failed"},
		{apperr.KindConflict, "conflict"},
		{apperr.KindSuspended, "account_suspended"},
		{apperr.KindRateLimited, "rate_limited"},
		{apperr.KindUnavailable, "service_unavailable"},
		{apperr.KindInternal, "internal_error"},
	}
	seen := make(map[string]apperr.Kind, len(cases))
	for _, tc := range cases {
		if tc.kind == "" {
			t.Fatal("Kind 常量为空")
		}
		if got := tc.kind.DefaultCode(); got != tc.code {
			t.Errorf("%s.DefaultCode() = %q, want %q", tc.kind, got, tc.code)
		}
		if prev, ok := seen[tc.code]; ok {
			t.Errorf("错误码 %q 同时属于 %s 与 %s", tc.code, prev, tc.kind)
		}
		seen[tc.code] = tc.kind
	}
	if got := apperr.Kind("unknown").DefaultCode(); got != "" {
		t.Errorf("未知 Kind 的 DefaultCode() = %q, want 空字符串", got)
	}
}

// TestConstructors 覆盖构造器的 Kind/Code/Message 及附加字段默认值。
func TestConstructors(t *testing.T) {
	cases := []struct {
		name    string
		err     *apperr.Error
		kind    apperr.Kind
		code    string
		message string
	}{
		{"NotFound", apperr.NotFound("资源不存在"), apperr.KindNotFound, "not_found", "资源不存在"},
		{"Forbidden", apperr.Forbidden("无权操作"), apperr.KindForbidden, "forbidden", "无权操作"},
		{"Unauthenticated", apperr.Unauthenticated("请先登录"), apperr.KindUnauthenticated, "unauthenticated", "请先登录"},
		{"Invalid", apperr.Invalid("缺少参数"), apperr.KindInvalid, "invalid_request", "缺少参数"},
		{"Validation", apperr.Validation("内容不能为空"), apperr.KindValidation, "validation_failed", "内容不能为空"},
		{"Conflict", apperr.Conflict("draft_version_conflict", "草稿已被更新"), apperr.KindConflict, "draft_version_conflict", "草稿已被更新"},
		{"Suspended", apperr.Suspended("账号已停用"), apperr.KindSuspended, "account_suspended", "账号已停用"},
		{"Unavailable", apperr.Unavailable("服务尚未就绪"), apperr.KindUnavailable, "service_unavailable", "服务尚未就绪"},
		{"Internal", apperr.Internal("服务内部错误"), apperr.KindInternal, "internal_error", "服务内部错误"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err == nil {
				t.Fatal("构造器返回 nil")
			}
			if tc.err.Kind != tc.kind {
				t.Errorf("Kind = %q, want %q", tc.err.Kind, tc.kind)
			}
			if tc.err.Code != tc.code {
				t.Errorf("Code = %q, want %q", tc.err.Code, tc.code)
			}
			if tc.err.Message != tc.message {
				t.Errorf("Message = %q, want %q", tc.err.Message, tc.message)
			}
			if tc.err.Details != nil || tc.err.RetryAfter != 0 || tc.err.Err != nil {
				t.Errorf("构造器不应设置附加字段: %+v", tc.err)
			}
		})
	}
}

// TestNew 覆盖自定义错误码与空码回退。
func TestNew(t *testing.T) {
	custom := apperr.New(apperr.KindConflict, "custom_conflict", "冲突")
	if custom.Code != "custom_conflict" || custom.Kind != apperr.KindConflict {
		t.Fatalf("New 结果不符: %+v", custom)
	}
	fallback := apperr.New(apperr.KindNotFound, "", "不存在")
	if fallback.Code != "not_found" {
		t.Fatalf("空 code 应回退为默认错误码, got %q", fallback.Code)
	}
}

// TestRateLimited 覆盖限频构造器的默认文案与可选重试间隔。
func TestRateLimited(t *testing.T) {
	plain := apperr.RateLimited()
	if plain.Kind != apperr.KindRateLimited || plain.Code != "rate_limited" {
		t.Fatalf("RateLimited 结果不符: %+v", plain)
	}
	if plain.Message == "" {
		t.Fatal("RateLimited 默认文案为空")
	}
	if plain.RetryAfter != 0 {
		t.Fatalf("未传 retryAfter 时应为 0, got %v", plain.RetryAfter)
	}

	withHint := apperr.RateLimited(3 * time.Second)
	if withHint.RetryAfter != 3*time.Second {
		t.Fatalf("RetryAfter = %v, want 3s", withHint.RetryAfter)
	}
}

// TestErrorString 覆盖 Error() 的「错误码: 文案[: 原因]」格式。
func TestErrorString(t *testing.T) {
	if got, want := apperr.NotFound("帖子不存在").Error(), "not_found: 帖子不存在"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if got, want := apperr.Conflict("draft_conflict", "冲突").Error(), "draft_conflict: 冲突"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	cause := errors.New("连接超时")
	wrapped := apperr.Wrap(cause, apperr.KindUnavailable, "数据库不可用")
	if got, want := wrapped.Error(), "service_unavailable: 数据库不可用: 连接超时"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	// 无消息时只输出错误码与底层原因。
	bare := apperr.Wrap(cause, apperr.KindInternal, "")
	if got, want := bare.Error(), "internal_error: 连接超时"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	// 直接构造、未设置 Code 时用 Kind 默认码，不 panic。
	raw := &apperr.Error{Kind: apperr.KindForbidden, Message: "禁止"}
	if got, want := raw.Error(), "forbidden: 禁止"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	var nilErr *apperr.Error
	if got := nilErr.Error(); got != "<nil>" {
		t.Errorf("nil Error() = %q, want \"<nil>\"", got)
	}
}

// TestWrap 覆盖包装、nil 处理与 errors.Is 穿透。
func TestWrap(t *testing.T) {
	if got := apperr.Wrap(nil, apperr.KindInternal, "忽略"); got != nil {
		t.Fatalf("Wrap(nil) = %v, want nil", got)
	}

	cause := errors.New("驱动失败")
	err := apperr.Wrap(cause, apperr.KindUnavailable, "数据库不可用")
	if !errors.Is(err, cause) {
		t.Fatal("errors.Is 应穿透到包装的底层错误")
	}
	if err.Err != cause {
		t.Fatalf("Err 未保留底层原因: %v", err.Err)
	}
	if !apperr.IsKind(err, apperr.KindUnavailable) {
		t.Fatal("包装后 IsKind 应为 true")
	}
}

// TestWithDetails 覆盖详情附加的不可变性。
func TestWithDetails(t *testing.T) {
	base := apperr.Validation("字段错误")
	details := map[string]string{"username": "格式不正确"}
	withDetails := base.WithDetails(details)

	if withDetails == base {
		t.Fatal("WithDetails 应返回副本")
	}
	if base.Details != nil {
		t.Fatalf("原错误被修改: %+v", base.Details)
	}
	if withDetails.Details == nil {
		t.Fatal("副本 Details 为空")
	}
	if withDetails.Kind != base.Kind || withDetails.Code != base.Code || withDetails.Message != base.Message {
		t.Fatalf("WithDetails 未保留原有字段: %+v", withDetails)
	}

	var nilErr *apperr.Error
	if got := nilErr.WithDetails(details); got != nil {
		t.Fatalf("nil 错误 WithDetails 应返回 nil, got %v", got)
	}
}

// TestWithCode 覆盖错误码覆盖的不可变性。
func TestWithCode(t *testing.T) {
	base := apperr.NotFound("不存在")
	overridden := base.WithCode("post_not_found")
	if base.Code != "not_found" {
		t.Fatalf("原错误被修改: %q", base.Code)
	}
	if overridden.Code != "post_not_found" {
		t.Fatalf("Code = %q, want post_not_found", overridden.Code)
	}
	if overridden.Kind != base.Kind || overridden.Message != base.Message {
		t.Fatalf("WithCode 未保留原有字段: %+v", overridden)
	}

	var nilErr *apperr.Error
	if got := nilErr.WithCode("x"); got != nil {
		t.Fatalf("nil 错误 WithCode 应返回 nil, got %v", got)
	}
}

// TestAsKindAndIsKind 覆盖错误链提取与 Kind 判断。
func TestAsKindAndIsKind(t *testing.T) {
	if _, ok := apperr.AsKind(nil); ok {
		t.Fatal("AsKind(nil) 应为 false")
	}
	if _, ok := apperr.AsKind(errors.New("普通错误")); ok {
		t.Fatal("普通错误不应被识别为领域错误")
	}
	// typed-nil 错误值不应导致 panic。
	var typedNil *apperr.Error
	var typedNilErr error = typedNil
	if _, ok := apperr.AsKind(typedNilErr); ok {
		t.Fatal("typed-nil 领域错误应视为不存在")
	}
	if apperr.IsKind(typedNilErr, apperr.KindNotFound) {
		t.Fatal("typed-nil 错误 IsKind 应为 false")
	}
	if apperr.IsKind(nil, apperr.KindNotFound) {
		t.Fatal("IsKind(nil) 应为 false")
	}

	original := apperr.NotFound("帖子不存在")
	got, ok := apperr.AsKind(original)
	if !ok || got != original {
		t.Fatalf("AsKind 未返回原错误: %v, %v", got, ok)
	}

	// fmt.Errorf("%w") 与 apperr.Wrap 的混合包装链。
	cause := errors.New("数据库断连")
	wrapped := apperr.Wrap(cause, apperr.KindUnavailable, "查询失败")
	chained := fmt.Errorf("加载帖子: %w", wrapped)

	extracted, ok := apperr.AsKind(chained)
	if !ok {
		t.Fatal("包装链中应能提取 *Error")
	}
	if extracted.Kind != apperr.KindUnavailable || extracted.Message != "查询失败" {
		t.Fatalf("提取结果不符: %+v", extracted)
	}
	if !errors.Is(chained, cause) {
		t.Fatal("混合包装链应可穿透到底层原因")
	}

	if !apperr.IsKind(chained, apperr.KindUnavailable) {
		t.Fatal("IsKind 应识别包装链中的 Kind")
	}
	if apperr.IsKind(chained, apperr.KindNotFound) {
		t.Fatal("IsKind 不应误判为其它 Kind")
	}

	// 多个领域错误嵌套时，errors.As 取最外层。
	outer := apperr.Wrap(original, apperr.KindUnavailable, "外层")
	extracted, ok = apperr.AsKind(outer)
	if !ok || extracted.Kind != apperr.KindUnavailable {
		t.Fatalf("应提取最外层领域错误: %+v, %v", extracted, ok)
	}
	if !errors.As(outer, new(*apperr.Error)) {
		t.Fatal("errors.As 应能匹配 *Error")
	}
}
