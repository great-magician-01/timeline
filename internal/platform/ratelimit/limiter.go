// Package ratelimit 提供单实例应用使用的内存固定窗口限频器。
package ratelimit

import (
	"sync"
	"time"
)

type bucket struct {
	startedAt time.Time
	count     int
}

// Limiter 的计数只保存在当前应用进程内；项目当前明确采用单实例部署。
// 它用于安全兜底，不替代数据库层的幂等与业务频率约束。
type Limiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	now     func() time.Time
	buckets map[string]bucket
}

func New(limit int, window time.Duration) *Limiter {
	if limit < 1 {
		panic("限频阈值必须大于零")
	}
	if window <= 0 {
		panic("限频窗口必须大于零")
	}
	return &Limiter{
		limit:   limit,
		window:  window,
		now:     time.Now,
		buckets: make(map[string]bucket),
	}
}

// Check 只检查当前窗口是否仍有额度，不增加计数。
func (l *Limiter) Check(key string) (allowed bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.cleanupLocked(now)
	b, ok := l.buckets[key]
	if !ok || now.Sub(b.startedAt) >= l.window {
		return true, 0
	}
	if b.count < l.limit {
		return true, 0
	}
	return false, l.window - now.Sub(b.startedAt)
}

// Consume 在允许时消耗一个额度；用于注册等每次尝试都需要计数的接口。
func (l *Limiter) Consume(key string) (allowed bool, retryAfter time.Duration) {
	return l.consume(key, l.limit, l.window)
}

// ConsumeLimit 使用调用方提供的阈值与窗口消耗一个额度。
// 限频参数来自系统设置时使用该入口，保证管理后台修改后即时生效；
// 已存在的窗口会按新窗口重新判断是否过期。
func (l *Limiter) ConsumeLimit(key string, limit int, window time.Duration) (allowed bool, retryAfter time.Duration) {
	if limit < 1 {
		limit = 1
	}
	if window <= 0 {
		window = time.Second
	}
	return l.consume(key, limit, window)
}

func (l *Limiter) consume(key string, limit int, window time.Duration) (allowed bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.cleanupLocked(now)
	b, ok := l.buckets[key]
	if !ok || now.Sub(b.startedAt) >= window {
		l.buckets[key] = bucket{startedAt: now, count: 1}
		return true, 0
	}
	if b.count >= limit {
		return false, window - now.Sub(b.startedAt)
	}
	b.count++
	l.buckets[key] = b
	return true, 0
}

// Fail 记录一次失败；用于登录密码校验失败后的计数。
func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	b, ok := l.buckets[key]
	if !ok || now.Sub(b.startedAt) >= l.window {
		b = bucket{startedAt: now}
	}
	b.count++
	l.buckets[key] = b
}

// Reset 清除某个维度的失败记录，通常在认证成功后调用。
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, key)
}

func (l *Limiter) cleanupLocked(now time.Time) {
	// 常规请求无需每次遍历；仅在键数量异常增长时清理过期窗口。
	if len(l.buckets) < 10000 {
		return
	}
	for key, b := range l.buckets {
		if now.Sub(b.startedAt) >= l.window {
			delete(l.buckets, key)
		}
	}
}
