// Package db 负责 PostgreSQL 连接（外部独立实例）。
// 行为约定（ENV-07）：数据库不可达时按退避重试并输出明确日志；
// 服务保持运行，/readyz 反映真实就绪状态，恢复连接后自动变为就绪。
package db

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	maxBackoff  = 30 * time.Second
	pingTimeout = 5 * time.Second
)

// Wait 带退避重试地连接数据库，直到成功或 ctx 取消。
func Wait(ctx context.Context, dsn string, log *slog.Logger) (*pgxpool.Pool, error) {
	backoff := time.Second
	for {
		pool, err := open(ctx, dsn)
		if err == nil {
			log.Info("数据库连接成功")
			return pool, nil
		}
		log.Warn("数据库暂不可达，退避后重试", "err", err, "backoff", backoff.String())

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func open(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 8

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}

	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// Ping 就绪检查用：轻量验证连接可用。
func Ping(ctx context.Context, pool *pgxpool.Pool) error {
	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	return pool.Ping(pingCtx)
}
