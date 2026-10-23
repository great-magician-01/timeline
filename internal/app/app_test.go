package app

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/platform/db"
)

// newTestApp 用可注入的 logger/bootstrap 构造组合根 App；不连接数据库。
func newTestApp(t *testing.T, bootstrap BootstrapStatusFn, buf *bytes.Buffer) *App {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return newApp(gin.New(), nil, db.NewReadiness(), clock.NewFake(), logger, bootstrap, nil)
}

// TestNewAppDefaults 校验组合根的默认值与访问器：nil logger 回退默认、nil clock 回退系统时钟、
// nil runner/readiness 不 panic。
func TestNewAppDefaults(t *testing.T) {
	app := newApp(nil, nil, nil, nil, nil, nil, nil)
	if app.Runner() != nil {
		t.Fatal("runner 应为 nil")
	}
	if app.Readiness() != nil {
		t.Fatal("readiness 应为 nil")
	}
	if app.Engine() != nil {
		t.Fatal("engine 应为 nil")
	}
	// Handler 返回 http.Handler 接口：nil *gin.Engine 装入接口后不等于 nil，
	// 这里只断言 Engine 为空，避免把 typed-nil 误判成真实 handler。
	if _, ok := app.Handler().(*gin.Engine); !ok {
		t.Fatal("Handler 应返回注入的引擎类型")
	}
	if app.clk == nil {
		t.Fatal("nil clock 必须回退为系统时钟")
	}
	if app.log == nil {
		t.Fatal("nil logger 必须回退为默认 logger")
	}
	if got := app.clk.Now().Year(); got < 2020 {
		t.Fatalf("系统时钟回退异常: year=%d", got)
	}
	// nil runner 时 Start 不启动后台任务，但会标记启动并布防退出信号监听。
	app.Start(context.Background())
	if !app.started {
		t.Fatal("Start 应标记为已启动")
	}
	app.Start(context.Background()) // 重复调用安全
	// Close 对 nil onClose 安全。
	app.Close()
}

// TestCloseCallsOnCloseOnce 校验 Close 只释放一次资源（重复调用不重复执行 onClose）。
func TestCloseCallsOnCloseOnce(t *testing.T) {
	calls := 0
	app := newApp(nil, nil, nil, nil, nil, nil, func() { calls++ })
	app.Close()
	app.Close()
	if calls != 1 {
		t.Fatalf("onClose 调用次数 = %d，期望 1", calls)
	}
}

// TestStartNilRunnerIsSafe 校验 Start 的幂等/空依赖保护：runner 为 nil 不启动后台任务，
// 重复调用安全。
func TestStartNilRunnerIsSafe(t *testing.T) {
	app := newApp(nil, nil, nil, nil, nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app.Start(ctx)
	app.Start(ctx)
	if !app.started {
		t.Fatal("Start 应标记为已启动")
	}
}

// TestStartClosesOnContextDone 校验退出信号监听：ctx 取消后触发 onClose，
// 与 Close() 共用同一个 Once，总共只执行一次。
func TestStartClosesOnContextDone(t *testing.T) {
	calls := 0
	app := newApp(nil, nil, nil, nil, nil, nil, func() { calls++ })
	ctx, cancel := context.WithCancel(context.Background())
	app.Start(ctx)
	cancel()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && calls == 0 {
		time.Sleep(time.Millisecond)
	}
	if calls != 1 {
		t.Fatalf("ctx 取消后 onClose 调用次数 = %d，期望 1", calls)
	}
	app.Close()
	if calls != 1 {
		t.Fatalf("Close 与退出监听应共用 Once，实际调用次数 = %d", calls)
	}
}

// fixedClock 固定时间，用于断言 Bootstrap 不修改状态、不依赖真实时间。
type fixedClock struct{ at time.Time }

func (f fixedClock) Now() time.Time { return f.at }

// TestBootstrapLogs 校验初始化状态日志的四个分支：
// 读取失败告警、空库提示（首注册成为超管）、无超管告警、正常信息。
func TestBootstrapLogs(t *testing.T) {
	cases := []struct {
		name    string
		status  BootstrapStatusFn
		want    string
		notWant string
	}{
		{
			name:   "nil 状态函数直接返回",
			status: nil,
			want:   "",
		},
		{
			name: "读取失败输出告警",
			status: func(context.Context) (int64, int64, error) {
				return 0, 0, errors.New("数据库不可用")
			},
			want: "读取初始化状态失败",
		},
		{
			name: "空库提示首个注册用户成为超管",
			status: func(context.Context) (int64, int64, error) {
				return 0, 0, nil
			},
			want: "数据库尚无人类用户",
		},
		{
			name: "无超管输出告警",
			status: func(context.Context) (int64, int64, error) {
				return 3, 0, nil
			},
			want: "当前没有超级管理员",
		},
		{
			name: "初始化状态正常",
			status: func(context.Context) (int64, int64, error) {
				return 3, 1, nil
			},
			want: "初始化状态正常",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			app := newTestApp(t, tc.status, &buf)
			app.Bootstrap(context.Background())
			out := buf.String()
			if tc.want == "" {
				if out != "" {
					t.Fatalf("不应输出日志，实际: %s", out)
				}
				return
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("日志缺少 %q，实际: %s", tc.want, out)
			}
		})
	}
}

// TestBootstrapNormalIncludesCounts 校验正常分支把人类用户/超管数量写入日志字段。
func TestBootstrapNormalIncludesCounts(t *testing.T) {
	var buf bytes.Buffer
	app := newTestApp(t, func(context.Context) (int64, int64, error) {
		return 7, 2, nil
	}, &buf)
	app.Bootstrap(context.Background())
	out := buf.String()
	if !strings.Contains(out, "human_users=7") || !strings.Contains(out, "super_admins=2") {
		t.Fatalf("正常分支日志缺少计数: %s", out)
	}
}

// TestBootstrapDoesNotPanicOnCanceledContext 校验 ctx 取消时仍按状态函数返回值处理（由函数自身决定）。
func TestBootstrapDoesNotPanicOnCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var buf bytes.Buffer
	app := newTestApp(t, func(context.Context) (int64, int64, error) { return 0, 0, nil }, &buf)
	app.Bootstrap(ctx)
	if !strings.Contains(buf.String(), "数据库尚无人类用户") {
		t.Fatalf("空库分支日志缺失: %s", buf.String())
	}
}

// TestAccessors 校验组合根访问器返回构造时注入的同一对象。
func TestAccessors(t *testing.T) {
	engine := gin.New()
	readiness := db.NewReadiness()
	clk := fixedClock{at: time.Date(2026, 10, 18, 12, 0, 0, 0, clock.AsiaShanghai)}
	app := newApp(engine, nil, readiness, clk, nil, nil, nil)
	if app.Engine() != engine || app.Handler() != engine {
		t.Fatal("Engine/Handler 必须返回注入的引擎")
	}
	if app.Readiness() != readiness {
		t.Fatal("Readiness 必须返回注入的就绪状态")
	}
	if app.clk.Now().UTC() != clk.at.UTC() {
		t.Fatal("Clock 必须返回注入的时钟")
	}
}
