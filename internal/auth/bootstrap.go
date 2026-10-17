package auth

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// bootstrapAdvisoryLockKey 取 "timeline" 的 ASCII 字节作为注册初始化锁标识；
// 首个注册用户判定与 INSERT 在同一事务内持锁，保证并发注册下只产生一个超级管理员。
const bootstrapAdvisoryLockKey int64 = 0x74696D656C696E61

// BootstrapRequired 表示系统尚无人类用户（未初始化）：首个注册者将成为超级管理员。
func BootstrapRequired(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	var required bool
	err := pool.QueryRow(ctx,
		`SELECT NOT EXISTS (SELECT 1 FROM users WHERE NOT is_ai)`,
	).Scan(&required)
	return required, err
}

// BootstrapStatus 返回人类用户数与超级管理员数，供启动日志判断初始化状态。
func BootstrapStatus(ctx context.Context, pool *pgxpool.Pool) (humanUsers, superAdmins int64, err error) {
	err = pool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM users WHERE NOT is_ai),
			(SELECT COUNT(*) FROM users WHERE is_super_admin)`,
	).Scan(&humanUsers, &superAdmins)
	return humanUsers, superAdmins, err
}
