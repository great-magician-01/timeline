package ratelimit

import (
	"testing"
	"time"
)

func TestConsumeAndWindowExpiry(t *testing.T) {
	limiter := New(2, time.Minute)
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.Local)
	limiter.now = func() time.Time { return now }

	if ok, _ := limiter.Consume("ip"); !ok {
		t.Fatal("第一次请求应被允许")
	}
	if ok, _ := limiter.Consume("ip"); !ok {
		t.Fatal("第二次请求应被允许")
	}
	if ok, retry := limiter.Consume("ip"); ok || retry <= 0 {
		t.Fatalf("第三次请求应被限频，allowed=%v retry=%v", ok, retry)
	}

	now = now.Add(time.Minute)
	if ok, _ := limiter.Consume("ip"); !ok {
		t.Fatal("窗口过期后应恢复额度")
	}
}

func TestFailureCheckAndReset(t *testing.T) {
	limiter := New(1, 5*time.Minute)
	limiter.Fail("account")
	if ok, _ := limiter.Check("account"); ok {
		t.Fatal("达到失败上限后应拒绝")
	}
	limiter.Reset("account")
	if ok, _ := limiter.Check("account"); !ok {
		t.Fatal("重置后应允许")
	}
}
