package websearch

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

// TestRetryableMatrix 校验重试判定：仅搜索服务错误且状态为 0/408/429/5xx 时可重试；
// 上下文取消、非领域错误、501 与 4xx 永不重试。
func TestRetryableMatrix(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil 不重试", nil, false},
		{"上下文取消不重试", context.Canceled, false},
		{"超时不重试", context.DeadlineExceeded, false},
		{"普通错误不重试", errors.New("network down"), false},
		{"无状态码（网络层）可重试", &Error{Code: "web_search_failed"}, true},
		{"408 可重试", &Error{StatusCode: http.StatusRequestTimeout}, true},
		{"429 可重试", &Error{StatusCode: http.StatusTooManyRequests}, true},
		{"500 可重试", &Error{StatusCode: http.StatusInternalServerError}, true},
		{"503 可重试", &Error{StatusCode: http.StatusServiceUnavailable}, true},
		{"501 永久错误不重试", &Error{StatusCode: http.StatusNotImplemented}, false},
		{"400 不重试", &Error{StatusCode: http.StatusBadRequest}, false},
		{"401 不重试", &Error{StatusCode: http.StatusUnauthorized}, false},
	}
	for _, tc := range cases {
		if got := retryable(tc.err); got != tc.want {
			t.Errorf("%s: retryable = %v，期望 %v", tc.name, got, tc.want)
		}
	}
	// 包装后的领域错误仍按错误链识别。
	wrapped := errors.Join(errors.New("外层"), &Error{StatusCode: http.StatusBadGateway})
	if !retryable(wrapped) {
		t.Fatal("包装后的 502 领域错误应可重试")
	}
}

// TestStatusErrorMapping 校验 HTTP 状态到稳定搜索错误码的映射。
func TestStatusErrorMapping(t *testing.T) {
	cases := []struct {
		status     int
		wantCode   string
		wantStatus int
	}{
		{http.StatusUnauthorized, "web_search_unauthorized", http.StatusUnauthorized},
		{http.StatusForbidden, "web_search_unauthorized", http.StatusForbidden},
		{http.StatusPaymentRequired, "web_search_insufficient_balance", http.StatusPaymentRequired},
		{http.StatusTooManyRequests, "web_search_rate_limited", http.StatusTooManyRequests},
		{http.StatusNotFound, "web_search_not_supported", http.StatusNotFound},
		{http.StatusMethodNotAllowed, "web_search_not_supported", http.StatusMethodNotAllowed},
		{http.StatusNotImplemented, "web_search_not_supported", http.StatusNotImplemented},
		{http.StatusBadGateway, "web_search_failed", http.StatusBadGateway},
	}
	for _, tc := range cases {
		searchErr := statusError(tc.status)
		if searchErr.Code != tc.wantCode || searchErr.StatusCode != tc.wantStatus {
			t.Errorf("statusError(%d) = %+v，期望 code=%s status=%d",
				tc.status, searchErr, tc.wantCode, tc.wantStatus)
		}
		if searchErr.Message == "" {
			t.Errorf("statusError(%d) 缺少用户文案", tc.status)
		}
	}
}

// TestRetryDelayBackoff 校验重试退避：首次为步长，指数增长并不超过上限。
func TestRetryDelayBackoff(t *testing.T) {
	first := retryDelay(1)
	if first != retryBackoffStep {
		t.Fatalf("retryDelay(1) = %v，期望步长 %v", first, retryBackoffStep)
	}
	if retryDelay(0) != first {
		t.Fatal("attempt<1 应归一到首次退避")
	}
	previous := first
	for attempt := 2; attempt <= 10; attempt++ {
		current := retryDelay(attempt)
		if current < previous && current != retryBackoffMax {
			t.Fatalf("retryDelay(%d) = %v 小于前一次 %v 且未到上限", attempt, current, previous)
		}
		if current > retryBackoffMax {
			t.Fatalf("retryDelay(%d) = %v 超过上限 %v", attempt, current, retryBackoffMax)
		}
		previous = current
	}
	if retryDelay(100) != retryBackoffMax {
		t.Fatalf("大 attempt 应封顶，实际 %v", retryDelay(100))
	}
}

// TestSleepContext 校验带上下文的退避等待：非正时长立即返回，取消的上下文立即失败，
// 正常时长到点返回 nil。
func TestSleepContext(t *testing.T) {
	if err := sleepContext(context.Background(), 0); err != nil {
		t.Fatalf("0 时长应立即成功: %v", err)
	}
	if err := sleepContext(context.Background(), -time.Second); err != nil {
		t.Fatalf("负时长应立即成功: %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepContext(canceled, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消的上下文应返回 context.Canceled，实际 %v", err)
	}
	if err := sleepContext(context.Background(), time.Millisecond); err != nil {
		t.Fatalf("短时长等待应成功: %v", err)
	}
}
