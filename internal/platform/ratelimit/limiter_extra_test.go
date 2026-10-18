package ratelimit

import (
	"strings"
	"testing"
	"time"
)

// TestNewPanicsOnInvalidParams 校验构造参数兜底：阈值/窗口非法时直接 panic，
// 避免静默创建“永不限频”的限频器。
func TestNewPanicsOnInvalidParams(t *testing.T) {
	cases := []struct {
		name   string
		limit  int
		window time.Duration
	}{
		{"阈值为零", 0, time.Minute},
		{"阈值为负", -1, time.Minute},
		{"窗口为零", 1, 0},
		{"窗口为负", 1, -time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("New(%d, %v) 应 panic", tc.limit, tc.window)
				}
			}()
			New(tc.limit, tc.window)
		})
	}
}

// TestConsumeLimitClampsInvalidParams 校验来自系统设置的非法参数被夹到安全值：
// limit<1 → 1，window<=0 → 1s，仍然生效而不是关闭限频。
func TestConsumeLimitClampsInvalidParams(t *testing.T) {
	limiter := New(5, time.Minute)
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return now }

	if ok, _ := limiter.ConsumeLimit("ip", 0, 0); !ok {
		t.Fatal("第一次请求应放行")
	}
	if ok, retry := limiter.ConsumeLimit("ip", 0, 0); ok || retry <= 0 {
		t.Fatalf("夹到 1 次/1s 后第二次应被限频，allowed=%v retry=%v", ok, retry)
	}
	now = now.Add(time.Second)
	if ok, _ := limiter.ConsumeLimit("ip", 0, 0); !ok {
		t.Fatal("1s 窗口过期后应恢复")
	}
}

// TestConsumeLimitUsesNewWindowImmediately 校验设置修改后即时生效：
// 已存在窗口按新窗口判断是否过期，且新阈值立即使用。
func TestConsumeLimitUsesNewWindowImmediately(t *testing.T) {
	limiter := New(10, time.Minute)
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return now }

	// 以 10 次/10 分钟建立窗口并消耗两次。
	for i := 0; i < 2; i++ {
		if ok, _ := limiter.ConsumeLimit("user", 10, 10*time.Minute); !ok {
			t.Fatalf("第 %d 次应放行", i+1)
		}
	}
	// 管理员改为 1 次/1 分钟：新窗口比已过时长（2 分钟）短，应视为过期并重新计数。
	now = now.Add(2 * time.Minute)
	if ok, _ := limiter.ConsumeLimit("user", 1, time.Minute); !ok {
		t.Fatal("按新窗口判断过期后应放行")
	}
	if ok, retry := limiter.ConsumeLimit("user", 1, time.Minute); ok || retry <= 0 {
		t.Fatalf("新阈值 1 次应立即生效，allowed=%v retry=%v", ok, retry)
	}
}

// TestFailWindowExpiryRestartsBucket 校验失败计数窗口过期后重新开始（而不是永久封禁）。
func TestFailWindowExpiryRestartsBucket(t *testing.T) {
	limiter := New(1, time.Minute)
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return now }

	limiter.Fail("account")
	if ok, _ := limiter.Check("account"); ok {
		t.Fatal("达到失败上限后应拒绝")
	}
	now = now.Add(time.Minute)
	if ok, _ := limiter.Check("account"); !ok {
		t.Fatal("窗口过期后应允许重试")
	}
	// 过期后的 Fail 重新开窗，计数为 1，不应立即再次触发上限。
	limiter.Fail("account")
	if ok, _ := limiter.Check("account"); ok {
		t.Fatal("新窗口内第 1 次失败后应仍拒绝（阈值 1）")
	}
}

// TestCheckRetryAfterRemainingWindow 校验 Check 的 RetryAfter 是窗口剩余时间。
func TestCheckRetryAfterRemainingWindow(t *testing.T) {
	limiter := New(1, time.Minute)
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return now }
	limiter.Fail("key")
	now = now.Add(20 * time.Second)
	ok, retry := limiter.Check("key")
	if ok {
		t.Fatal("应被限频")
	}
	if retry != 40*time.Second {
		t.Fatalf("RetryAfter=%v，期望窗口剩余 40s", retry)
	}
}

// TestCleanupDropsExpiredBucketsWhenMapGrows 校验键数量超过阈值时清理过期窗口，
// 防止内存随客户端数量无界增长。
func TestCleanupDropsExpiredBucketsWhenMapGrows(t *testing.T) {
	limiter := New(1, time.Minute)
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return now }

	// 直接填充过期窗口，制造超过清理阈值的键数量。
	for i := 0; i < 10001; i++ {
		limiter.buckets[strings.Join([]string{"ip", string(rune('a' + i%26)), time.Duration(i).String()}, "-")] =
			bucket{startedAt: now.Add(-2 * time.Minute), count: 1}
	}
	now = now.Add(time.Second)
	limiter.Consume("new-key")
	if got := len(limiter.buckets); got != 1 {
		t.Fatalf("清理后桶数量 = %d，期望只剩新键", got)
	}
}
