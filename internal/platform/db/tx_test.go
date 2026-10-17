package db

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fakeTx 是只实现 TxManager 所需能力的假事务。
type fakeTx struct {
	commitCalls    int
	rollbackCalls  int
	rollbackCtxErr error
	commitErr      error
	rollbackErr    error
}

func (f *fakeTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (f *fakeTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("fakeTx.Query 不应被调用")
}

func (f *fakeTx) QueryRow(context.Context, string, ...any) pgx.Row {
	return nil
}

func (f *fakeTx) Commit(context.Context) error {
	f.commitCalls++
	return f.commitErr
}

func (f *fakeTx) Rollback(ctx context.Context) error {
	f.rollbackCalls++
	f.rollbackCtxErr = ctx.Err()
	return f.rollbackErr
}

// fakeBeginner 是假的事务开启器，记录调用次数并可注入错误。
type fakeBeginner struct {
	tx         *fakeTx
	beginErr   error
	beginCalls int
}

func (f *fakeBeginner) Begin(context.Context) (transaction, error) {
	f.beginCalls++
	if f.beginErr != nil {
		return nil, f.beginErr
	}
	return f.tx, nil
}

// newFakeManager 用假事务开启器构造 TxManager（生产路径由 NewTxManager 包装连接池）。
func newFakeManager(tx *fakeTx) (*TxManager, *fakeBeginner) {
	beginner := &fakeBeginner{tx: tx}
	return &TxManager{beginner: beginner}, beginner
}

// TestWithTxCommit 覆盖成功路径：开启一次、提交一次，函数拿到可用的 ctx。
func TestWithTxCommit(t *testing.T) {
	tx := &fakeTx{rollbackErr: errors.New("已提交事务再回滚会失败")}
	m, beginner := newFakeManager(tx)

	var called bool
	err := m.WithTx(context.Background(), func(ctx context.Context) error {
		called = true
		if !InTx(ctx) {
			t.Fatal("事务回调内 InTx 应为 true")
		}
		if got := ExecutorFrom(ctx, nil); got != Executor(tx) {
			t.Fatalf("ExecutorFrom 未返回当前事务: %v", got)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithTx 返回错误: %v", err)
	}
	if !called {
		t.Fatal("事务回调未执行")
	}
	if beginner.beginCalls != 1 || tx.commitCalls != 1 {
		t.Fatalf("begin=%d commit=%d, want 1/1", beginner.beginCalls, tx.commitCalls)
	}
	// 计划中的实现始终 defer Rollback（提交后再回滚由驱动忽略错误），保证异常路径安全。
	if tx.rollbackCalls != 1 {
		t.Fatalf("rollback=%d, want 1（提交后仍尝试回滚且忽略错误）", tx.rollbackCalls)
	}
}

// TestWithTxFnError 覆盖回调返回错误：不提交、回滚并原样返回业务错误。
func TestWithTxFnError(t *testing.T) {
	tx := &fakeTx{}
	m, _ := newFakeManager(tx)

	sentinel := errors.New("业务校验失败")
	err := m.WithTx(context.Background(), func(context.Context) error {
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("WithTx 错误 = %v, want %v", err, sentinel)
	}
	if tx.commitCalls != 0 {
		t.Fatalf("commit=%d, want 0", tx.commitCalls)
	}
	if tx.rollbackCalls != 1 {
		t.Fatalf("rollback=%d, want 1", tx.rollbackCalls)
	}
}

// TestWithTxBeginError 覆盖开启事务失败。
func TestWithTxBeginError(t *testing.T) {
	sentinel := errors.New("连接池耗尽")
	beginner := &fakeBeginner{beginErr: sentinel}
	m := &TxManager{beginner: beginner}

	called := false
	err := m.WithTx(context.Background(), func(context.Context) error {
		called = true
		return nil
	})
	if called {
		t.Fatal("开启事务失败时不应执行回调")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("WithTx 错误 = %v, want %v", err, sentinel)
	}
	if err == nil || !strings.Contains(err.Error(), "开启事务失败") {
		t.Fatalf("错误缺少包装信息: %v", err)
	}
}

// TestWithTxCommitError 覆盖提交失败：返回包装错误，errors.Is 可穿透。
func TestWithTxCommitError(t *testing.T) {
	sentinel := errors.New("提交冲突")
	tx := &fakeTx{commitErr: sentinel}
	m, _ := newFakeManager(tx)

	err := m.WithTx(context.Background(), func(context.Context) error { return nil })
	if !errors.Is(err, sentinel) {
		t.Fatalf("WithTx 错误 = %v, want %v", err, sentinel)
	}
	if !strings.Contains(err.Error(), "提交事务失败") {
		t.Fatalf("错误缺少包装信息: %v", err)
	}
}

// TestWithTxNested 覆盖嵌套合并：内层不开启新事务，只提交一次。
func TestWithTxNested(t *testing.T) {
	tx := &fakeTx{}
	m, beginner := newFakeManager(tx)

	var innerRan bool
	err := m.WithTx(context.Background(), func(ctx context.Context) error {
		if ExecutorFrom(ctx, nil) != Executor(tx) {
			t.Fatal("外层 ExecutorFrom 未返回事务")
		}
		return m.WithTx(ctx, func(innerCtx context.Context) error {
			innerRan = true
			if !InTx(innerCtx) {
				t.Fatal("内层 InTx 应为 true")
			}
			if innerCtx != ctx {
				t.Fatal("嵌套调用应复用同一 ctx（不重新包装）")
			}
			if ExecutorFrom(innerCtx, nil) != Executor(tx) {
				t.Fatal("内层 ExecutorFrom 未返回外层事务")
			}
			return nil
		})
	})
	if err != nil {
		t.Fatalf("嵌套 WithTx 返回错误: %v", err)
	}
	if !innerRan {
		t.Fatal("内层回调未执行")
	}
	if beginner.beginCalls != 1 {
		t.Fatalf("begin=%d, want 1（嵌套合并）", beginner.beginCalls)
	}
	if tx.commitCalls != 1 {
		t.Fatalf("commit=%d, want 1", tx.commitCalls)
	}
}

// TestWithTxRollbackDetachedContext 覆盖请求取消后仍用未取消的 ctx 回滚。
func TestWithTxRollbackDetachedContext(t *testing.T) {
	tx := &fakeTx{}
	m, _ := newFakeManager(tx)

	ctx, cancel := context.WithCancel(context.Background())
	err := m.WithTx(ctx, func(fnCtx context.Context) error {
		cancel()
		return fnCtx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WithTx 错误 = %v, want context.Canceled", err)
	}
	if tx.rollbackCalls != 1 {
		t.Fatalf("rollback=%d, want 1", tx.rollbackCalls)
	}
	if tx.rollbackCtxErr != nil {
		t.Fatalf("回滚 ctx 不应被请求取消: %v", tx.rollbackCtxErr)
	}
}

// TestWithTxRollbackOnPanic 覆盖 panic 时先回滚再把 panic 抛给上层。
func TestWithTxRollbackOnPanic(t *testing.T) {
	tx := &fakeTx{}
	m, _ := newFakeManager(tx)

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("panic 应向上传播")
			}
		}()
		_ = m.WithTx(context.Background(), func(context.Context) error {
			panic("用例内 panic")
		})
	}()

	if tx.rollbackCalls != 1 {
		t.Fatalf("panic 后 rollback=%d, want 1", tx.rollbackCalls)
	}
	if tx.commitCalls != 0 {
		t.Fatalf("panic 后 commit=%d, want 0", tx.commitCalls)
	}
}

// TestWithTxInvalidManager 覆盖未初始化管理器与空回调。
func TestWithTxInvalidManager(t *testing.T) {
	var nilManager *TxManager
	if err := nilManager.WithTx(context.Background(), func(context.Context) error { return nil }); err == nil {
		t.Fatal("nil TxManager 应返回错误")
	}

	m := &TxManager{}
	if err := m.WithTx(context.Background(), func(context.Context) error { return nil }); err == nil {
		t.Fatal("未初始化 TxManager 应返回错误")
	}

	m2, beginner := newFakeManager(&fakeTx{})
	if err := m2.WithTx(context.Background(), nil); err == nil {
		t.Fatal("nil 回调应返回错误")
	}
	if beginner.beginCalls != 0 {
		t.Fatalf("nil 回调不应开启事务, begin=%d", beginner.beginCalls)
	}
}

// TestWithTxNilPool 覆盖连接池未就绪时的明确错误（而不是空指针崩溃）。
func TestWithTxNilPool(t *testing.T) {
	m := NewTxManager(nil)
	called := false
	err := m.WithTx(context.Background(), func(context.Context) error {
		called = true
		return nil
	})
	if called {
		t.Fatal("连接池未就绪时不应执行回调")
	}
	if err == nil || !strings.Contains(err.Error(), "数据库连接池未就绪") {
		t.Fatalf("WithTx 错误 = %v, want 含「数据库连接池未就绪」", err)
	}
}

// TestInTx 覆盖事务状态判断与事务外行为。
func TestInTx(t *testing.T) {
	ctx := context.Background()
	if InTx(ctx) {
		t.Fatal("普通 ctx 不应处于事务中")
	}

	tx := &fakeTx{}
	m, _ := newFakeManager(tx)
	if err := m.WithTx(ctx, func(fnCtx context.Context) error {
		if !InTx(fnCtx) {
			t.Fatal("事务回调内 InTx 应为 true")
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTx 返回错误: %v", err)
	}
	if InTx(ctx) {
		t.Fatal("外层 ctx 不应被注入事务")
	}

	// 直接构造带事务的 ctx（同包测试可访问私有键）。
	injected := withTx(context.Background(), tx)
	if !InTx(injected) {
		t.Fatal("withTx 注入后 InTx 应为 true")
	}
	if ExecutorFrom(injected, nil) != Executor(tx) {
		t.Fatal("ExecutorFrom 应优先返回 ctx 中的事务")
	}
}

// TestExecutorFromWithoutTx 覆盖事务外回退到连接池。
func TestExecutorFromWithoutTx(t *testing.T) {
	pool := &pgxpool.Pool{}
	got := ExecutorFrom(context.Background(), pool)
	if got != Executor(pool) {
		t.Fatalf("ExecutorFrom 未回退到连接池: %v", got)
	}
	if InTx(context.Background()) {
		t.Fatal("事务外 InTx 不应为 true")
	}
}
