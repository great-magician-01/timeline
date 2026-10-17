package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/great-magician-01/callable"

	"github.com/great-magician-01/timeline/internal/ai"
)

// fakeResponse 构造一个用于重试测试的成功响应。
func fakeResponse(text string) *callable.Response {
	return &callable.Response{Text: text}
}

func TestWithOutboundRetryRetriesTransientOnly(t *testing.T) {
	var (
		attempts []int
		sleeps   []time.Duration
		calls    int
	)
	resp, err := withOutboundRetry(context.Background(), 2,
		func(_ context.Context, delay time.Duration) error {
			sleeps = append(sleeps, delay)
			return nil
		},
		func(_ context.Context, attempt int) (*callable.Response, error) {
			attempts = append(attempts, attempt)
			calls++
			switch calls {
			case 1:
				return nil, &callable.APIError{StatusCode: 429}
			case 2:
				return nil, &callable.APIError{StatusCode: 503}
			default:
				return fakeResponse("ok"), nil
			}
		})
	if err != nil {
		t.Fatalf("第三次尝试成功时不应返回错误: %v", err)
	}
	if resp == nil || resp.Text != "ok" {
		t.Fatalf("应返回成功响应: %+v", resp)
	}
	if len(attempts) != 3 || attempts[0] != 1 || attempts[1] != 2 || attempts[2] != 3 {
		t.Fatalf("attempt 序号应从 1 递增: %v", attempts)
	}
	if len(sleeps) != 2 || sleeps[0] != 3*time.Second || sleeps[1] != 9*time.Second {
		t.Fatalf("退避应为 3s/9s: %v", sleeps)
	}
}

func TestWithOutboundRetryStopsOnClientError(t *testing.T) {
	calls := 0
	_, err := withOutboundRetry(context.Background(), 2,
		func(context.Context, time.Duration) error {
			t.Fatal("4xx 不应触发退避等待")
			return nil
		},
		func(context.Context, int) (*callable.Response, error) {
			calls++
			return nil, &callable.APIError{StatusCode: 400}
		})
	if err == nil {
		t.Fatal("4xx 应返回错误")
	}
	if calls != 1 {
		t.Fatalf("4xx 不应重试，实际调用 %d 次", calls)
	}
}

func TestWithOutboundRetryExhaustedReturnsLastError(t *testing.T) {
	calls := 0
	var lastErr error
	_, err := withOutboundRetry(context.Background(), 2,
		func(context.Context, time.Duration) error { return nil },
		func(context.Context, int) (*callable.Response, error) {
			calls++
			lastErr = &callable.APIError{StatusCode: 500}
			return nil, lastErr
		})
	if calls != 3 {
		t.Fatalf("应有 3 次尝试，实际 %d 次", calls)
	}
	if !errors.Is(err, lastErr) {
		t.Fatalf("应返回最后一次错误，实际 %v", err)
	}
}

func TestWithOutboundRetryStopsWhenContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	_, err := withOutboundRetry(ctx, 2,
		func(context.Context, time.Duration) error {
			cancel()
			return ctx.Err()
		},
		func(context.Context, int) (*callable.Response, error) {
			calls++
			return nil, &callable.APIError{StatusCode: 503}
		})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("上下文取消应返回 context.Canceled: %v", err)
	}
	if calls != 1 {
		t.Fatalf("取消后不应继续尝试，实际 %d 次", calls)
	}
}

func TestRetryableCallError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"429", &callable.APIError{StatusCode: 429}, true},
		{"500", &callable.APIError{StatusCode: 500}, true},
		{"503", &callable.APIError{StatusCode: 503}, true},
		{"network", &callable.APIError{StatusCode: 0}, true},
		{"400", &callable.APIError{StatusCode: 400}, false},
		{"401", &callable.APIError{StatusCode: 401}, false},
		{"404", &callable.APIError{StatusCode: 404}, false},
		{"canceled", context.Canceled, false},
		{"deadline", context.DeadlineExceeded, false},
		{"other", errors.New("boom"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		if got := retryableCallError(tc.err); got != tc.want {
			t.Errorf("%s: retryableCallError = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestOutboundRetryDelay(t *testing.T) {
	cases := map[int]time.Duration{
		0: 3 * time.Second,
		1: 3 * time.Second,
		2: 9 * time.Second,
		3: 9 * time.Second,
	}
	for attempt, want := range cases {
		if got := outboundRetryDelay(attempt); got != want {
			t.Errorf("attempt %d: delay = %v, want %v", attempt, got, want)
		}
	}
}

func TestTaskClaimable(t *testing.T) {
	for _, status := range []string{statusPending, statusFailed, statusRunning} {
		if !taskClaimable(status) {
			t.Errorf("状态 %s 应允许尝试领取", status)
		}
	}
	for _, status := range []string{statusSucceeded, statusNoop, statusRejected, statusCanceled, "unknown"} {
		if taskClaimable(status) {
			t.Errorf("状态 %s 不应尝试领取", status)
		}
	}
}

func TestEvaluateActionGate(t *testing.T) {
	all := actionGateState{GlobalEnabled: true, AIExists: true, ProfileEnabled: true, ActorExists: true, ActorActive: true}
	if status, _ := evaluateActionGate(all); status != "" {
		t.Fatalf("全部开启时不应拦截: %s", status)
	}
	cases := []struct {
		name   string
		mutate func(*actionGateState)
	}{
		{"全局关闭", func(s *actionGateState) { s.GlobalEnabled = false }},
		{"AI 不存在", func(s *actionGateState) { s.AIExists = false }},
		{"AI 停用", func(s *actionGateState) { s.ProfileEnabled = false }},
		{"账号不存在", func(s *actionGateState) { s.ActorExists = false }},
		{"账号停用", func(s *actionGateState) { s.ActorActive = false }},
	}
	for _, tc := range cases {
		state := all
		tc.mutate(&state)
		status, reason := evaluateActionGate(state)
		if status != statusCanceled || reason == "" {
			t.Errorf("%s: 应返回 canceled 与非空原因，实际 %q/%q", tc.name, status, reason)
		}
	}
	// 全局开关优先，便于后台把“紧急停止”记录成统一原因。
	state := actionGateState{}
	if _, reason := evaluateActionGate(state); reason != "全局 AI 开关已关闭" {
		t.Errorf("全局关闭应优先，实际 %q", reason)
	}
}

func TestParseFutureScheduledAt(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.Local)
	future := now.Add(time.Hour).Format(time.RFC3339)
	at, err := parseFutureScheduledAt(future, now)
	if err != nil {
		t.Fatalf("未来时间应通过: %v", err)
	}
	if !at.Equal(now.Add(time.Hour)) {
		t.Fatalf("解析结果不一致: %v", at)
	}
	if _, err := parseFutureScheduledAt(now.Format(time.RFC3339), now); err == nil {
		t.Fatal("当前时间应被拒绝")
	}
	if _, err := parseFutureScheduledAt("2026-10-17 20:00", now); err == nil {
		t.Fatal("非 RFC3339 应被拒绝")
	}
	if _, err := parseFutureScheduledAt("   ", now); err == nil {
		t.Fatal("空值应被拒绝")
	}
}

func TestCollectMemorySources(t *testing.T) {
	postID := int64(101)
	commentID := int64(202)
	zero := int64(0)
	memories := []ai.Memory{
		{SourceRefType: sourcePost, SourceRefID: &postID},
		{SourceRefType: sourcePost, SourceRefID: &postID}, // 重复
		{SourceRefType: sourceComment, SourceRefID: &commentID},
		{SourceRefType: "user", SourceRefID: &commentID}, // 非内容来源
		{SourceRefType: sourcePost},                      // 缺少来源 ID
		{SourceRefType: sourcePost, SourceRefID: &zero},
		{SourceRefType: "", SourceRefID: &postID},
	}
	sources := collectMemorySources(memories)
	if len(sources) != 2 {
		t.Fatalf("应保留 2 个去重来源，实际 %d: %+v", len(sources), sources)
	}
	if sources[0].Type != sourcePost || sources[0].ID != postID {
		t.Fatalf("第一个来源应为 post/%d: %+v", postID, sources[0])
	}
	if sources[1].Type != sourceComment || sources[1].ID != commentID {
		t.Fatalf("第二个来源应为 comment/%d: %+v", commentID, sources[1])
	}
}

func TestCollectMemorySourcesEmpty(t *testing.T) {
	if sources := collectMemorySources(nil); len(sources) != 0 {
		t.Fatalf("空输入应返回空来源: %+v", sources)
	}
}
