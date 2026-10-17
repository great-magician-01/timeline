package db

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

// fakePinger 模拟连接池探测行为。
type fakePinger struct {
	err      error
	deadline time.Time
	hasDL    bool
}

func (f *fakePinger) Ping(ctx context.Context) error {
	f.deadline, f.hasDL = ctx.Deadline()
	return f.err
}

// blockingPinger 一直阻塞到 ctx 结束，用于验证 Ping 的上下文超时生效。
type blockingPinger struct{}

func (blockingPinger) Ping(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

// TestPingSuccess 覆盖探测成功的路径。
func TestPingSuccess(t *testing.T) {
	fake := &fakePinger{}
	if err := Ping(context.Background(), fake); err != nil {
		t.Fatalf("Ping 成功场景返回错误: %v", err)
	}
	if !fake.hasDL {
		t.Fatal("Ping 未给探测设置超时上下文")
	}
}

// TestPingError 覆盖底层错误原样返回。
func TestPingError(t *testing.T) {
	sentinel := errors.New("连接已断开")
	fake := &fakePinger{err: sentinel}
	if err := Ping(context.Background(), fake); !errors.Is(err, sentinel) {
		t.Fatalf("Ping 错误未透传: %v", err)
	}
}

// TestPingAppliesPingTimeout 验证探测上下文受 pingTimeout 上限约束。
func TestPingAppliesPingTimeout(t *testing.T) {
	fake := &fakePinger{}
	start := time.Now()
	if err := Ping(context.Background(), fake); err != nil {
		t.Fatalf("Ping 返回错误: %v", err)
	}
	remaining := fake.deadline.Sub(start)
	if remaining <= 0 {
		t.Fatalf("探测超时时间已过期: %v", remaining)
	}
	if remaining > pingTimeout+time.Second {
		t.Fatalf("探测超时 %v 超出上限 %v", remaining, pingTimeout)
	}
}

// TestPingRespectsParentTimeout 覆盖父 ctx 超时优先于 pingTimeout。
func TestPingRespectsParentTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := Ping(ctx, blockingPinger{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Ping 错误 = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Ping 未及时返回: %v", elapsed)
	}
}

// TestWaitCanceledContext 覆盖 ctx 取消时 Wait 快速退出，不无限重试。
func TestWaitCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pool, err := Wait(ctx, "postgres://user:pass@127.0.0.1:1/timeline?sslmode=disable", log)
	if pool != nil {
		t.Fatalf("ctx 已取消但返回了连接池: %v", pool)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait 错误 = %v, want context.Canceled", err)
	}
}

// TestWaitNilLogger 验证未传 logger 时回退默认 logger 不 panic。
func TestWaitNilLogger(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	pool, err := Wait(ctx, "postgres://%zz", nil)
	if pool != nil {
		t.Fatalf("ctx 已取消但返回了连接池: %v", pool)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait 错误 = %v, want context.Canceled", err)
	}
}

// TestOpenInvalidDSN 覆盖非法 DSN 直接报错且不返回连接池。
func TestOpenInvalidDSN(t *testing.T) {
	pool, err := open(context.Background(), "://不是一个合法的 DSN")
	if err == nil {
		pool.Close()
		t.Fatal("非法 DSN 应返回错误")
	}
	if pool != nil {
		t.Fatalf("非法 DSN 不应返回连接池: %v", pool)
	}
}
