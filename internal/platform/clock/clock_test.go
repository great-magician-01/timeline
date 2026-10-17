package clock_test

import (
	"sync"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/platform/clock"
)

// 编译期断言：Fake 与系统时钟都是 Clock。
var (
	_ clock.Clock = clock.NewFake()
	_ clock.Clock = clock.System()
)

// TestAsiaShanghai 覆盖固定北京时区定义。
func TestAsiaShanghai(t *testing.T) {
	if clock.AsiaShanghai == nil {
		t.Fatal("AsiaShanghai 为 nil")
	}
	if got, want := clock.AsiaShanghai.String(), "CST"; got != want {
		t.Errorf("时区名 = %q, want %q", got, want)
	}
	_, offset := time.Date(2026, 10, 18, 12, 0, 0, 0, clock.AsiaShanghai).Zone()
	if offset != 8*60*60 {
		t.Errorf("时区偏移 = %d 秒, want %d", offset, 8*60*60)
	}

	// 把 +08:00 时间换算为 UTC 应与手算一致，证明固定偏移而非依赖本地时区。
	local := time.Date(2026, 10, 18, 0, 0, 0, 0, clock.AsiaShanghai)
	wantUTC := time.Date(2026, 10, 17, 16, 0, 0, 0, time.UTC)
	if !local.UTC().Equal(wantUTC) {
		t.Errorf("UTC = %v, want %v", local.UTC(), wantUTC)
	}
}

// TestSystem 覆盖系统时钟返回接近当前的时间。
func TestSystem(t *testing.T) {
	start := time.Now()
	got := clock.System().Now()
	if got.IsZero() {
		t.Fatal("System().Now() 返回零值时间")
	}
	if d := got.Sub(start); d < -time.Minute || d > time.Minute {
		t.Errorf("System().Now() 与 time.Now() 偏差过大: %v", d)
	}
}

// TestDayStart 覆盖北京自然日 00:00 的跨日与跨时区行为。
func TestDayStart(t *testing.T) {
	cst := time.FixedZone("CST", 8*60*60)
	cases := []struct {
		name string
		t    time.Time
		want time.Time
	}{
		{
			name: "北京午夜零时",
			t:    time.Date(2026, 10, 18, 0, 0, 0, 0, cst),
			want: time.Date(2026, 10, 18, 0, 0, 0, 0, cst),
		},
		{
			name: "北京白天带纳秒",
			t:    time.Date(2026, 10, 18, 13, 45, 12, 987654321, cst),
			want: time.Date(2026, 10, 18, 0, 0, 0, 0, cst),
		},
		{
			name: "北京日末最后一刻",
			t:    time.Date(2026, 10, 18, 23, 59, 59, 999999999, cst),
			want: time.Date(2026, 10, 18, 0, 0, 0, 0, cst),
		},
		{
			name: "UTC 时间仍属北京同一天",
			t:    time.Date(2026, 10, 17, 15, 59, 59, 0, time.UTC),
			want: time.Date(2026, 10, 17, 0, 0, 0, 0, cst),
		},
		{
			name: "UTC 16:00 已跨到北京次日",
			t:    time.Date(2026, 10, 17, 16, 0, 0, 0, time.UTC),
			want: time.Date(2026, 10, 18, 0, 0, 0, 0, cst),
		},
		{
			name: "其他时区输入",
			t:    time.Date(2026, 10, 19, 3, 0, 0, 0, time.FixedZone("EST", -5*60*60)),
			want: time.Date(2026, 10, 19, 0, 0, 0, 0, cst), // 北京 16:00
		},
		{
			name: "跨年日",
			t:    time.Date(2027, 1, 1, 0, 30, 0, 0, cst),
			want: time.Date(2027, 1, 1, 0, 0, 0, 0, cst),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := clock.DayStart(tc.t)
			if !got.Equal(tc.want) {
				t.Fatalf("DayStart(%v) = %v, want %v", tc.t, got, tc.want)
			}
			if got.Hour() != 0 || got.Minute() != 0 || got.Second() != 0 || got.Nanosecond() != 0 {
				t.Errorf("日界未截断到 00:00:00: %v", got)
			}
			if _, offset := got.Zone(); offset != 8*60*60 {
				t.Errorf("日界时区偏移 = %d, want %d", offset, 8*60*60)
			}
			if !got.Add(24 * time.Hour).After(tc.t) {
				t.Errorf("日界 %v 加一天后不在输入时间之后", got)
			}
		})
	}
}

// TestDayStartIdempotent 验证重复调用同一个结果不变。
func TestDayStartIdempotent(t *testing.T) {
	input := time.Now() // 跟随运行环境时区，TZ=UTC 时也必须按北京日界截断
	once := clock.DayStart(input)
	twice := clock.DayStart(once)
	if !once.Equal(twice) {
		t.Fatalf("DayStart 不幂等: %v -> %v", once, twice)
	}
	if _, offset := once.Zone(); offset != 8*60*60 {
		t.Errorf("日界时区偏移 = %d, want %d", offset, 8*60*60)
	}
}

// TestNewFake 覆盖默认起点、显式起点与可预测的 Now。
func TestNewFake(t *testing.T) {
	// 无参时使用固定默认起点。
	def := clock.NewFake()
	if got := def.Now(); got.IsZero() || got.Year() < 2000 {
		t.Fatalf("默认起点异常: %v", got)
	}

	start := time.Date(2026, 10, 18, 9, 30, 0, 0, clock.AsiaShanghai)
	clk := clock.NewFake(start)
	if !clk.Now().Equal(start) {
		t.Fatalf("NewFake 初始时间 = %v, want %v", clk.Now(), start)
	}
}

// TestFakeAdvanceSet 覆盖推进、后退与直接设置。
func TestFakeAdvanceSet(t *testing.T) {
	start := time.Date(2026, 10, 18, 23, 59, 0, 0, clock.AsiaShanghai)
	clk := clock.NewFake(start)

	if got, want := clk.Advance(2*time.Minute), start.Add(2*time.Minute); !got.Equal(want) {
		t.Fatalf("Advance 返回值 = %v, want %v", got, want)
	}
	if got, want := clk.Now(), time.Date(2026, 10, 19, 0, 1, 0, 0, clock.AsiaShanghai); !got.Equal(want) {
		t.Fatalf("推进后 Now = %v, want %v", got, want)
	}
	// 推进跨日后日界应变为次日 00:00。
	if got, want := clock.DayStart(clk.Now()), time.Date(2026, 10, 19, 0, 0, 0, 0, clock.AsiaShanghai); !got.Equal(want) {
		t.Fatalf("跨日后 DayStart = %v, want %v", got, want)
	}

	clk.Advance(-3 * time.Hour)
	if got, want := clk.Now(), time.Date(2026, 10, 18, 21, 1, 0, 0, clock.AsiaShanghai); !got.Equal(want) {
		t.Fatalf("后退后 Now = %v, want %v", got, want)
	}

	explicit := time.Date(2027, 2, 28, 12, 0, 0, 0, time.UTC)
	clk.Set(explicit)
	if !clk.Now().Equal(explicit) {
		t.Fatalf("Set 后 Now = %v, want %v", clk.Now(), explicit)
	}
}

// TestFakeConcurrent 验证并发读写不竞态、不死锁（配合 -race 使用）。
func TestFakeConcurrent(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, clock.AsiaShanghai))

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				clk.Advance(time.Millisecond)
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if clk.Now().IsZero() {
					t.Error("Now 返回零值时间")
					return
				}
			}
		}()
		go func(n int) {
			defer wg.Done()
			base := time.Date(2026, 6, 1, 0, 0, 0, 0, clock.AsiaShanghai)
			for j := 0; j < 200; j++ {
				clk.Set(base.Add(time.Duration(n*1000+j) * time.Second))
			}
		}(i)
	}
	wg.Wait()

	// 并发推进 8*200ms 与多次 Set 交错，最终时间取决于调度；只要求可用且非零。
	if clk.Now().IsZero() {
		t.Fatal("并发操作后 Now 为零值")
	}
}
