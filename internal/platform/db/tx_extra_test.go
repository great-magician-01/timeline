package db

import (
	"context"
	"strings"
	"testing"
)

// TestPoolTxBeginnerNilPool 校验连接池未就绪时开启事务返回明确错误而不是空指针崩溃。
func TestPoolTxBeginnerNilPool(t *testing.T) {
	_, err := poolTxBeginner{}.Begin(context.Background())
	if err == nil || !strings.Contains(err.Error(), "连接池未就绪") {
		t.Fatalf("nil pool 应返回明确错误，实际 %v", err)
	}
}

// TestTxFromNilAndEmptyContext 校验 nil/无事务上下文的安全判定。
func TestTxFromNilAndEmptyContext(t *testing.T) {
	//lint:ignore SA1012 测试 nil context 的防御分支
	if _, ok := txFrom(nil); ok {
		t.Fatal("nil context 不应包含事务")
	}
	if InTx(context.Background()) {
		t.Fatal("普通 context 不应处于事务中")
	}
	if _, ok := txFrom(context.Background()); ok {
		t.Fatal("普通 context 不应包含事务")
	}
}

// TestWithTxNilFn 校验事务函数为空时返回明确错误（防误装配导致 panic）。
func TestWithTxNilFn(t *testing.T) {
	manager := &TxManager{}
	err := manager.WithTx(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "事务函数不能为空") {
		t.Fatalf("nil 事务函数应报错，实际 %v", err)
	}
}
