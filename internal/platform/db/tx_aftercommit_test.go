package db

import (
	"context"
	"testing"
)

// TestAfterCommitWithoutTx 覆盖事务外注册：立即同步执行，回调 ctx 不随调用方取消。
func TestAfterCommitWithoutTx(t *testing.T) {
	called := false
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 调用方 ctx 已取消也不影响回调执行

	AfterCommit(ctx, func(fnCtx context.Context) {
		called = true
		if fnCtx.Err() != nil {
			t.Fatalf("回调 ctx 不应随调用方取消: %v", fnCtx.Err())
		}
	})
	if !called {
		t.Fatal("事务外注册的回调应立即执行")
	}
}

// TestAfterCommitRunsAfterCommit 覆盖事务内注册：提交后按注册顺序执行。
func TestAfterCommitRunsAfterCommit(t *testing.T) {
	tx := &fakeTx{}
	m, _ := newFakeManager(tx)

	var order []int
	requestCtx, cancel := context.WithCancel(context.Background())
	err := m.WithTx(requestCtx, func(fnCtx context.Context) error {
		cancel() // 模拟回调执行前请求 ctx 已取消
		AfterCommit(fnCtx, func(context.Context) { order = append(order, 1) })
		AfterCommit(fnCtx, func(fnCtx context.Context) {
			order = append(order, 2)
			if fnCtx.Err() != nil {
				t.Fatalf("提交后回调 ctx 不应随请求取消: %v", fnCtx.Err())
			}
		})
		return nil
	})
	if err != nil {
		t.Fatalf("WithTx 返回错误: %v", err)
	}
	if len(order) != 2 || order[0] != 1 || order[1] != 2 {
		t.Fatalf("回调执行顺序 = %v, want [1 2]", order)
	}
}

// TestAfterCommitSkippedOnRollback 覆盖回滚路径：回调不执行。
func TestAfterCommitSkippedOnRollback(t *testing.T) {
	tx := &fakeTx{}
	m, _ := newFakeManager(tx)

	called := false
	err := m.WithTx(context.Background(), func(fnCtx context.Context) error {
		AfterCommit(fnCtx, func(context.Context) { called = true })
		return context.Canceled
	})
	if err == nil {
		t.Fatal("回滚路径应返回错误")
	}
	if called {
		t.Fatal("回滚后不应执行提交后回调")
	}
}

// TestAfterCommitSkippedOnCommitError 覆盖提交失败路径：回调不执行。
func TestAfterCommitSkippedOnCommitError(t *testing.T) {
	tx := &fakeTx{commitErr: context.DeadlineExceeded}
	m, _ := newFakeManager(tx)

	called := false
	err := m.WithTx(context.Background(), func(fnCtx context.Context) error {
		AfterCommit(fnCtx, func(context.Context) { called = true })
		return nil
	})
	if err == nil {
		t.Fatal("提交失败应返回错误")
	}
	if called {
		t.Fatal("提交失败后不应执行提交后回调")
	}
}

// TestAfterCommitNestedSharesQueue 覆盖嵌套事务：内层注册的回调在外层提交后执行一次。
func TestAfterCommitNestedSharesQueue(t *testing.T) {
	tx := &fakeTx{}
	m, beginner := newFakeManager(tx)

	calls := 0
	err := m.WithTx(context.Background(), func(outerCtx context.Context) error {
		return m.WithTx(outerCtx, func(innerCtx context.Context) error {
			AfterCommit(innerCtx, func(context.Context) { calls++ })
			return nil
		})
	})
	if err != nil {
		t.Fatalf("嵌套 WithTx 返回错误: %v", err)
	}
	if calls != 1 {
		t.Fatalf("回调执行次数 = %d, want 1（嵌套共享同一队列）", calls)
	}
	if beginner.beginCalls != 1 {
		t.Fatalf("begin=%d, want 1", beginner.beginCalls)
	}
}

// TestAfterCommitNilCallback 覆盖 nil 回调在各路径下都不 panic、不执行。
func TestAfterCommitNilCallback(t *testing.T) {
	AfterCommit(context.Background(), nil)

	tx := &fakeTx{}
	m, _ := newFakeManager(tx)
	if err := m.WithTx(context.Background(), func(fnCtx context.Context) error {
		AfterCommit(fnCtx, nil)
		return nil
	}); err != nil {
		t.Fatalf("WithTx 返回错误: %v", err)
	}
}
