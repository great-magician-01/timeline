package clock

import (
	"sync"
	"time"
)

// defaultFakeStart 是 NewFake 未传起点时的固定初始时间。
// 使用固定值而非零值时间，避免测试出现公元 1 年等意外边界。
var defaultFakeStart = time.Date(2026, 1, 1, 0, 0, 0, 0, AsiaShanghai)

// Fake 是测试用可手动推进的 Clock，并发安全。
//
// 用法：clk := clock.NewFake(start); clk.Advance(2 * time.Hour)
type Fake struct {
	mu  sync.RWMutex
	now time.Time
}

// NewFake 创建测试时钟；可选参数 start 为初始时间，不传时使用固定默认起点
// （2026-01-01 00:00:00 北京时间）。同时支持 clock.NewFake() 与 clock.NewFake(start)。
func NewFake(start ...time.Time) *Fake {
	from := defaultFakeStart
	if len(start) > 0 {
		from = start[0]
	}
	return &Fake{now: from}
}

// Now 返回当前（伪造）时间。
func (f *Fake) Now() time.Time {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.now
}

// Advance 前进 d（d 为负则后退），返回推进后的时间。
func (f *Fake) Advance(d time.Duration) time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
	return f.now
}

// Set 直接设置当前时间（用于跨日、跨租约等边界用例）。
func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = t
}
