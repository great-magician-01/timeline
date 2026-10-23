package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// transaction 是 TxManager 需要的事务能力；pgx.Tx 满足该接口。
type transaction interface {
	Executor
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// 编译期断言：pgx.Tx 满足 TxManager 的事务要求。
var _ transaction = (pgx.Tx)(nil)

// txBeginner 抽象「开启事务」，便于事务单测注入假实现而无需真实数据库。
type txBeginner interface {
	Begin(ctx context.Context) (transaction, error)
}

// poolTxBeginner 用连接池开启事务。
type poolTxBeginner struct {
	pool *pgxpool.Pool
}

// Begin 开启事务；连接池未就绪时返回明确错误而不是空指针崩溃。
func (b poolTxBeginner) Begin(ctx context.Context) (transaction, error) {
	if b.pool == nil {
		return nil, fmt.Errorf("数据库连接池未就绪")
	}
	return b.pool.Begin(ctx)
}

// TxManager 是 app 层唯一的事务入口：支持 ctx 传播与嵌套合并。
//
// 用法：app 用例用 WithTx 包裹整个写操作，repo 通过 ExecutorFrom 自动加入事务；
// 跨模块协作时内层 WithTx 检测到 ctx 已有事务即直接复用，不会重复开启。
type TxManager struct {
	pool     *pgxpool.Pool
	beginner txBeginner
}

// NewTxManager 用连接池创建事务入口；pool 应在数据库连接就绪后传入。
func NewTxManager(pool *pgxpool.Pool) *TxManager {
	return &TxManager{pool: pool, beginner: poolTxBeginner{pool: pool}}
}

// WithTx 在事务中执行 fn，并把事务写入 ctx 供 ExecutorFrom 使用。
// 若 ctx 已处于事务中（嵌套调用或跨模块协作），直接复用外层事务，不开启新事务。
// fn 返回错误时不提交并回滚；提交失败时返回包装后的错误（errors.Is/As 可穿透）。
// fn 内经 AfterCommit 注册的回调在 Commit 成功后按序执行（见 AfterCommit）。
func (m *TxManager) WithTx(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return fmt.Errorf("事务函数不能为空")
	}
	if InTx(ctx) {
		return fn(ctx)
	}
	if m == nil || m.beginner == nil {
		return fmt.Errorf("事务管理器未初始化")
	}

	tx, err := m.beginner.Begin(ctx)
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	ctx = withTx(ctx, tx)
	defer func() {
		// 回滚使用不随请求取消的 ctx：客户端断开或 ctx 超时后也必须释放事务。
		_ = tx.Rollback(context.WithoutCancel(ctx))
	}()

	if err := fn(ctx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("提交事务失败: %w", err)
	}
	runAfterCommit(ctx)
	return nil
}

// AfterCommitFunc 是事务提交成功后执行的回调。
type AfterCommitFunc func(ctx context.Context)

// AfterCommit 注册一个事务提交成功后执行的回调：
//
//   - ctx 处于 TxManager.WithTx 事务内时，回调入队，在外层事务 Commit 成功后按
//     注册顺序执行（嵌套事务共享同一队列）；回滚或提交失败不执行；
//   - ctx 不在事务内时立即同步执行，回调 ctx 不随调用方取消。
//
// 典型用途是「写库成功后对外发布事件」：保证消费方收到信号时数据必然已提交。
// 回调契约：不得阻塞（发布通道必须非阻塞）、不得 panic——提交成功后的 panic 会把
// 已成功的业务请求变成 500；platform/eventbus 的 Publish 自身 recover 兜底。
func AfterCommit(ctx context.Context, fn AfterCommitFunc) {
	if fn == nil {
		return
	}
	if state := stateFrom(ctx); state != nil {
		state.callbacks = append(state.callbacks, fn)
		return
	}
	fn(context.WithoutCancel(ctx))
}

// runAfterCommit 在事务提交成功后按注册顺序执行回调；回调 ctx 不随请求取消。
func runAfterCommit(ctx context.Context) {
	state := stateFrom(ctx)
	if state == nil || len(state.callbacks) == 0 {
		return
	}
	runCtx := context.WithoutCancel(ctx)
	for _, fn := range state.callbacks {
		fn(runCtx)
	}
}

// txState 是 ctx 携带的事务状态：事务执行器与提交后回调队列。
// 回调队列在事务 Begin 时随 txState 创建，嵌套 WithTx 复用外层事务即共享同一队列。
type txState struct {
	tx        transaction
	callbacks []AfterCommitFunc
}

// txStateKey 是事务状态在 context 中的私有键；保证只有本包能读写事务。
type txStateKey struct{}

// withTx 把事务写入 context，供 ExecutorFrom/InTx/AfterCommit 读取。
func withTx(ctx context.Context, tx transaction) context.Context {
	return context.WithValue(ctx, txStateKey{}, &txState{tx: tx})
}

// stateFrom 取出 ctx 中的事务状态；不在事务中返回 nil。
func stateFrom(ctx context.Context) *txState {
	if ctx == nil {
		return nil
	}
	state, ok := ctx.Value(txStateKey{}).(*txState)
	if !ok || state == nil {
		return nil
	}
	return state
}

// txFrom 取出 context 中的事务；不在事务中返回 (nil, false)。
func txFrom(ctx context.Context) (transaction, bool) {
	if state := stateFrom(ctx); state != nil {
		return state.tx, true
	}
	return nil, false
}

// InTx 报告 ctx 是否处于 TxManager.WithTx 开启的事务中。
// 用于断言事务边界（如测试中确认用例未在事务外写库）。
func InTx(ctx context.Context) bool {
	_, ok := txFrom(ctx)
	return ok
}

// ExecutorFrom 返回 ctx 中当前事务的执行器；不在事务中时返回给定连接池。
//
// repo 层统一通过它取执行器，从而自动加入 app 编排的事务；调用方应保证
// 数据库就绪后 pool 非 nil（启动早期请求会被 ready 中间件拒绝）。
func ExecutorFrom(ctx context.Context, pool *pgxpool.Pool) Executor {
	if tx, ok := txFrom(ctx); ok {
		return tx
	}
	return pool
}
