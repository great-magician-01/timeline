package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrAdminExists    = errors.New("管理员已存在，不能重复执行首个管理员初始化")
	ErrIdentityExists = errors.New("用户名或邮箱已被使用")
)

const adminInitLockKey int64 = 0x74696D656C696E61

// CreateInitialAdmin 通过交互式命令创建首个管理员；不从环境变量读取密码。
// 无外键约束下，账号字段一致性由本事务和 users 表 CHECK 约束共同保证。
func CreateInitialAdmin(ctx context.Context, pool *pgxpool.Pool, ids IDGenerator, in RegisterInput) (User, error) {
	in, details := normalizeRegistration(in)
	if details != nil {
		return User{}, fmt.Errorf("注册信息校验失败: %s", validationMessage(details))
	}
	passwordHash, err := HashPassword(in.Password)
	if err != nil {
		return User{}, err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return User{}, fmt.Errorf("开启管理员初始化事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, adminInitLockKey); err != nil {
		return User{}, fmt.Errorf("获取管理员初始化锁失败: %w", err)
	}
	var adminCount int64
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE role = 'admin'`).Scan(&adminCount); err != nil {
		return User{}, fmt.Errorf("检查既有管理员失败: %w", err)
	}
	if adminCount > 0 {
		return User{}, ErrAdminExists
	}
	userID, err := ids.Next()
	if err != nil {
		return User{}, fmt.Errorf("生成管理员 ID 失败: %w", err)
	}
	u, err := ScanUser(tx.QueryRow(ctx, `
		INSERT INTO users (
			id, username, email, password_hash, nickname, role, is_ai, status,
			require_follow_approval, token_version
		) VALUES ($1, $2, $3, $4, $2, 'admin', FALSE, 'active', FALSE, 1)
		RETURNING `+UserColumns, userID, in.Username, in.Email, passwordHash,
	))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return User{}, ErrIdentityExists
		}
		return User{}, fmt.Errorf("创建首个管理员失败: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, fmt.Errorf("提交管理员初始化事务失败: %w", err)
	}
	return u, nil
}
