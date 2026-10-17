// Package clock 是业务代码唯一的取时入口。
//
// 目标架构中 time.Now() 只允许出现在 platform/clock（以及 platform/id、logging、
// transport 访问日志等明确白名单），所有 app 构造注入 Clock；会话过期、作业租约、
// 日统计窗口、限频与退避截止时间都由这里取时，测试用 Fake 精确控制。
package clock

import "time"

// Clock 提供当前时间。
type Clock interface {
	Now() time.Time
}

// AsiaShanghai 是全系统固定的北京时间时区（UTC+08:00，无夏令时）。
// 使用 FixedZone 而非 time.LoadLocation，避免运行环境缺少 tzdata 时行为漂移。
var AsiaShanghai = time.FixedZone("CST", 8*60*60)

// System 返回基于系统时钟的 Clock（生产环境唯一取时来源）。
func System() Clock { return systemClock{} }

// systemClock 是生产时钟实现。
type systemClock struct{}

// Now 返回系统当前时间。
func (systemClock) Now() time.Time { return time.Now() }

// 编译期断言：生产时钟与测试时钟都满足 Clock。
var (
	_ Clock = systemClock{}
	_ Clock = (*Fake)(nil)
)

// DayStart 返回 t 所在北京时间自然日的 00:00（北京时间）。
//
// 与 t 自身的 Location 无关：先换算到 AsiaShanghai 再截断，保证即使进程 TZ=UTC
// 也按北京日界计算，用于 ai_action_counters.counter_date 与「今日」范围参数。
func DayStart(t time.Time) time.Time {
	local := t.In(AsiaShanghai)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, AsiaShanghai)
}
