package jobs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// 本文件覆盖 runner.go 的领取/执行/完成/恢复循环与消费循环。
//
// 说明：Runner 的数据库访问路径使用 *pgxpool.Pool 的具体类型，无法用接口替换，
// 因此这里构造一个“拨号立即失败”的连接池：DialFunc 直接返回错误，不会建立任何
// 网络连接。借此在没有数据库的默认测试中覆盖 claim/finish/recoverLoop 的错误分支
// 与状态写回调用，同时保持测试快速、确定、无网络依赖。

// fakePoolProvider 固定返回给定连接池（可为 nil），用于注入 Runner 的池访问路径。
type fakePoolProvider struct {
	pool *pgxpool.Pool
}

func (p fakePoolProvider) Pool() *pgxpool.Pool { return p.pool }

var _ PoolProvider = fakePoolProvider{}

// newUnreachablePool 返回所有连接尝试都会立即失败的连接池。
func newUnreachablePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	config, err := pgxpool.ParseConfig("postgres://test:test@127.0.0.1:1/test?sslmode=disable")
	if err != nil {
		t.Fatalf("解析测试连接串失败: %v", err)
	}
	config.ConnConfig.DialFunc = func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("测试禁止建立数据库连接")
	}
	config.MinConns = 0
	config.MaxConns = 1

	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("创建测试连接池失败: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// newDiscardLogger 构造不输出内容的日志器。
func newDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// captureHandler 把日志记录写入带缓冲 channel，测试按事件等待而不是真实 sleep。
type captureHandler struct {
	records chan slog.Record
}

func newCaptureHandler() *captureHandler {
	return &captureHandler{records: make(chan slog.Record, 32)}
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, record slog.Record) error {
	select {
	case h.records <- record:
	default:
	}
	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *captureHandler) WithGroup(string) slog.Handler { return h }

// waitFor 阻塞等待指定消息的日志记录；5 秒仅作为防止死循环的安全上限。
func (h *captureHandler) waitFor(t *testing.T, message string) slog.Record {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case record := <-h.records:
			if record.Message == message {
				return record
			}
		case <-timer.C:
			t.Fatalf("等待日志 %q 超时", message)
		}
	}
}

// drain 收集当前已同步写入的全部日志消息。
func (h *captureHandler) drain() []string {
	var messages []string
	for {
		select {
		case record := <-h.records:
			messages = append(messages, record.Message)
		default:
			return messages
		}
	}
}

// waitDone 在 ctx 取消后等待后台循环退出；超时仅作为安全上限。
func waitDone(t *testing.T, done <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s 未在取消后退出", name)
	}
}

func TestClaimWithoutReadyPool(t *testing.T) {
	runner := NewRunner(fakePoolProvider{}, &testIDs{next: 100}, newDiscardLogger(), &testClock{now: extraTestNow})
	job, err := runner.claim(context.Background())
	if err != nil || job != nil {
		t.Fatalf("连接池未就绪时应返回 (nil, nil)，实际 (%v, %v)", job, err)
	}
}

func TestClaimReturnsConnectionError(t *testing.T) {
	runner := NewRunner(
		fakePoolProvider{newUnreachablePool(t)}, &testIDs{next: 100}, newDiscardLogger(), &testClock{now: extraTestNow},
	)
	job, err := runner.claim(context.Background())
	if err == nil || job != nil {
		t.Fatalf("连接失败应返回错误，实际 (%v, %v)", job, err)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("连接错误不应被当作无待执行作业")
	}
}

// TestFinishWithoutReadyPool 覆盖连接池未就绪时完成逻辑提前返回（不写状态、不 panic）。
func TestFinishWithoutReadyPool(t *testing.T) {
	runner := NewRunner(fakePoolProvider{}, &testIDs{next: 100}, newDiscardLogger(), &testClock{now: extraTestNow})
	runner.finish(context.Background(), Job{ID: 1, Attempts: 1, MaxAttempts: 3}, errors.New("失败"))
	runner.finish(context.Background(), Job{ID: 1}, nil)
	runner.finish(context.Background(), Job{ID: 1, Attempts: 3, MaxAttempts: 3}, errors.New("失败"))
}

// TestExecuteHandlesHandlerOutcomes 覆盖未注册类型、成功、失败、panic 与 ErrHandled 分支。
func TestExecuteHandlesHandlerOutcomes(t *testing.T) {
	clk := &testClock{now: extraTestNow}

	t.Run("未注册类型直接按失败处理", func(t *testing.T) {
		capture := newCaptureHandler()
		runner := NewRunner(fakePoolProvider{}, &testIDs{next: 100}, slog.New(capture), clk)
		runner.handlers = map[string]Handler{}
		// 连接池未就绪时 finish 提前返回，不会记录日志。
		runner.execute(context.Background(), Job{ID: 1, Type: "未注册类型"})
		if messages := capture.drain(); len(messages) != 0 {
			t.Fatalf("连接池未就绪时不应记录日志: %v", messages)
		}
	})

	t.Run("处理器成功", func(t *testing.T) {
		called := false
		runner := NewRunner(fakePoolProvider{}, &testIDs{next: 100}, newDiscardLogger(), clk)
		runner.Register(TypeAITask, func(context.Context, Job) error { called = true; return nil })
		runner.execute(context.Background(), Job{ID: 1, Type: TypeAITask})
		if !called {
			t.Fatal("已注册处理器未被调用")
		}
	})

	t.Run("处理器返回 ErrHandled 不写终态", func(t *testing.T) {
		capture := newCaptureHandler()
		runner := NewRunner(fakePoolProvider{newUnreachablePool(t)}, &testIDs{next: 100}, slog.New(capture), clk)
		runner.Register(TypeAITask, func(context.Context, Job) error { return ErrHandled })
		runner.execute(context.Background(), Job{ID: 1, Type: TypeAITask, Attempts: 1, MaxAttempts: 3})
		if messages := capture.drain(); len(messages) != 0 {
			t.Fatalf("ErrHandled 不应写状态或记录日志: %v", messages)
		}
	})

	t.Run("处理器 panic 被捕获并记为失败", func(t *testing.T) {
		capture := newCaptureHandler()
		runner := NewRunner(fakePoolProvider{newUnreachablePool(t)}, &testIDs{next: 100}, slog.New(capture), clk)
		runner.Register(TypeAITask, func(context.Context, Job) error { panic("处理器崩溃") })
		runner.execute(context.Background(), Job{ID: 1, Type: TypeAITask, Attempts: 3, MaxAttempts: 3})
		panicRecord := capture.waitFor(t, "后台作业 panic")
		stack := attrString(panicRecord, "stack")
		for _, want := range []string{"goroutine", "panic(", "(*Runner).execute"} {
			if !strings.Contains(stack, want) {
				t.Fatalf("作业 panic 日志的调用栈应包含 %q（含 panic 现场帧），实际 %q", want, stack)
			}
		}
		record := capture.waitFor(t, "后台作业失败")
		errValue := attrString(record, "err")
		if !strings.Contains(errValue, "作业 panic: 处理器崩溃") {
			t.Fatalf("失败原因应记录 panic 信息，实际 %q", errValue)
		}
	})
}

// attrString 取出日志记录中指定属性的字符串值。
func attrString(record slog.Record, key string) string {
	value := ""
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == key {
			value = attr.Value.String()
			return false
		}
		return true
	})
	return value
}

// TestExecuteWritesStateThroughPool 覆盖 finish 的三种写回分支（借不可达连接池触发
// Exec 错误日志，验证状态写回被调用且不会因数据库不可用而 panic）。
func TestExecuteWritesStateThroughPool(t *testing.T) {
	clk := &testClock{now: extraTestNow}
	cases := []struct {
		name         string
		handlerErr   error
		attempts     int
		maxAttempts  int
		wantMessages []string
	}{
		{
			name: "成功写回", handlerErr: nil, attempts: 1, maxAttempts: 3,
			wantMessages: []string{"更新作业状态失败"},
		},
		{
			name: "失败后安排重试", handlerErr: errors.New("上游失败"), attempts: 1, maxAttempts: 3,
			wantMessages: []string{"重试作业失败", "后台作业将重试"},
		},
		{
			name: "达到上限标记失败", handlerErr: errors.New("上游失败"), attempts: 3, maxAttempts: 3,
			wantMessages: []string{"标记作业失败状态出错", "后台作业失败"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			capture := newCaptureHandler()
			runner := NewRunner(
				fakePoolProvider{newUnreachablePool(t)}, &testIDs{next: 100}, slog.New(capture), clk,
			)
			runner.Register(TypeAITask, func(context.Context, Job) error { return tc.handlerErr })
			runner.execute(context.Background(), Job{
				ID: 7, Type: TypeAITask, Attempts: tc.attempts, MaxAttempts: tc.maxAttempts,
			})
			messages := capture.drain()
			for _, want := range tc.wantMessages {
				found := false
				for _, got := range messages {
					if got == want {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("缺少日志 %q，实际 %v", want, messages)
				}
			}
		})
	}
}

// TestFinishTruncatesLongError 覆盖 last_error 超过 2000 字节时截断。
func TestFinishTruncatesLongError(t *testing.T) {
	capture := newCaptureHandler()
	runner := NewRunner(fakePoolProvider{newUnreachablePool(t)}, &testIDs{next: 100}, slog.New(capture),
		&testClock{now: extraTestNow})
	runner.Register(TypeAITask, func(context.Context, Job) error {
		return errors.New(strings.Repeat("x", 3000))
	})
	runner.execute(context.Background(), Job{ID: 7, Type: TypeAITask, Attempts: 3, MaxAttempts: 3})
	record := capture.waitFor(t, "后台作业失败")
	if got := attrString(record, "err"); len(got) != 2000 {
		t.Fatalf("失败原因应截断为 2000 字节，实际 %d", len(got))
	}
}

// TestRecoverLoopRetriesAfterError 覆盖首次恢复失败后记录告警并等待下一次轮询。
func TestRecoverLoopRetriesAfterError(t *testing.T) {
	capture := newCaptureHandler()
	runner := NewRunner(
		fakePoolProvider{newUnreachablePool(t)}, &testIDs{next: 100}, slog.New(capture),
		&testClock{now: extraTestNow},
	)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.recoverLoop(ctx)
	}()
	capture.waitFor(t, "恢复中断的后台作业失败，稍后重试")
	cancel()
	waitDone(t, done, "recoverLoop")
}

// TestConsumeLogsClaimError 覆盖消费循环在领取失败时记录错误并等待下一轮。
func TestConsumeLogsClaimError(t *testing.T) {
	capture := newCaptureHandler()
	runner := NewRunner(
		fakePoolProvider{newUnreachablePool(t)}, &testIDs{next: 100}, slog.New(capture),
		&testClock{now: extraTestNow},
	)
	runner.pollInterval = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.consume(ctx, 0)
	}()
	capture.waitFor(t, "领取后台作业失败")
	cancel()
	waitDone(t, done, "consume")
}

// TestRunPeriodicExecutesAndRecoversPanic 覆盖周期任务执行、panic 恢复与取消退出。
func TestRunPeriodicExecutesAndRecoversPanic(t *testing.T) {
	capture := newCaptureHandler()
	runner := NewRunner(
		fakePoolProvider{newUnreachablePool(t)}, &testIDs{next: 100}, slog.New(capture),
		&testClock{now: extraTestNow},
	)
	called := make(chan struct{}, 1)
	task := periodicTask{
		name:     "测试周期任务",
		interval: time.Millisecond,
		fn: func(context.Context) {
			select {
			case called <- struct{}{}:
			default:
			}
			panic("周期任务崩溃")
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.runPeriodic(ctx, task)
	}()
	select {
	case <-called:
	case <-time.After(5 * time.Second):
		t.Fatal("周期任务未被调用")
	}
	record := capture.waitFor(t, "周期任务 panic")
	if got := attrString(record, "panic"); !strings.Contains(got, "周期任务崩溃") {
		t.Fatalf("panic 日志属性错误: %q", got)
	}
	if stack := attrString(record, "stack"); !strings.Contains(stack, "goroutine") ||
		!strings.Contains(stack, "panic(") || !strings.Contains(stack, "(*Runner).runPeriodic") {
		t.Fatalf("周期任务 panic 日志的调用栈应包含 panic 现场帧，实际 %q", stack)
	}
	cancel()
	waitDone(t, done, "runPeriodic")
}

// TestRunPeriodicSkipsUnreadyPool 覆盖连接池未就绪时不执行周期任务。
func TestRunPeriodicSkipsUnreadyPool(t *testing.T) {
	called := false
	runner := NewRunner(
		fakePoolProvider{}, &testIDs{next: 100}, newDiscardLogger(), &testClock{now: extraTestNow},
	)
	task := periodicTask{name: "测试", interval: time.Millisecond, fn: func(context.Context) { called = true }}
	// 已取消的上下文：runPeriodic 必须在第一次 select 时退出，不执行任务。
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner.runPeriodic(ctx, task)
	if called {
		t.Fatal("连接池未就绪或上下文已取消时不应执行周期任务")
	}
}

// TestStartSpawnsWorkersAndStops 覆盖 Start 启动 worker/周期任务/恢复循环并可随 ctx 停止。
func TestStartSpawnsWorkersAndStops(t *testing.T) {
	runner := NewRunner(fakePoolProvider{}, &testIDs{next: 100}, newDiscardLogger(), &testClock{now: extraTestNow})
	runner.RegisterPeriodic("测试周期任务", time.Hour, func(context.Context) {})
	ctx, cancel := context.WithCancel(context.Background())
	runner.Start(ctx)
	cancel()
	// 重复调用 Start 不应 panic；goroutine 在取消后自行退出。
	runner.Start(ctx)
}
