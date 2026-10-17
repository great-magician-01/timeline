package repo

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/platform/db"
)

// BootstrapLockKey 取 "timeline" 的 ASCII 字节作为首注册初始化锁标识；
// 首个注册用户判定与 INSERT 在同一事务内持锁，保证并发注册只产生一个超级管理员。
const BootstrapLockKey int64 = 0x74696D656C696E61

// LockRepo 提供注册初始化所需的 advisory lock。
type LockRepo struct {
	pool *pgxpool.Pool
}

// NewLockRepo 构造锁仓储。
func NewLockRepo(pool *pgxpool.Pool) *LockRepo { return &LockRepo{pool: pool} }

// AcquireBootstrapLock 在当前事务内获取初始化锁；必须在 db.WithTx 内调用，
// 事务结束（提交或回滚）时锁自动释放。
func (r *LockRepo) AcquireBootstrapLock(ctx context.Context) error {
	_, err := db.ExecutorFrom(ctx, r.pool).Exec(ctx,
		`SELECT pg_advisory_xact_lock($1)`, BootstrapLockKey)
	return err
}
