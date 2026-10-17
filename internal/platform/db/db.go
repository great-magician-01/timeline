// Package db 提供 PostgreSQL 连接等待、就绪状态与事务原语。
//
// 行为约定（ENV-07）：数据库不可达时按退避重试并输出明确日志；服务保持运行，
// /readyz 反映真实就绪状态，恢复连接后自动变为就绪。
//
// 分层约束：SQL 与事务只在平台层与 repo 层；app 层通过 TxManager.WithTx 编排事务，
// repo 层通过 ExecutorFrom(ctx, pool) 自动加入当前事务。
package db

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// maxBackoff 是连接数据库时的最大退避间隔。
	maxBackoff = 30 * time.Second
	// pingTimeout 是单次连接/就绪探测的超时。
	pingTimeout = 5 * time.Second
)

// Wait 带退避重试地连接数据库，直到成功或 ctx 取消。
// log 为 nil 时回退到 slog.Default()；ctx 取消时返回 ctx.Err()。
func Wait(ctx context.Context, dsn string, log *slog.Logger) (*pgxpool.Pool, error) {
	if log == nil {
		log = slog.Default()
	}
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

// open 建立连接池并 Ping 验证可用；失败时关闭已创建的池，避免连接泄漏。
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

// Pinger 是连接池的最小探测能力；*pgxpool.Pool 满足该接口，单测可注入假实现。
type Pinger interface {
	Ping(ctx context.Context) error
}

// Ping 就绪检查用：轻量验证连接可用；单次探测最多等待 pingTimeout。
// pool 不应为 nil（调用方先从 Readiness.Pool/PoolProvider 取池并判空）。
func Ping(ctx context.Context, pool Pinger) error {
	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	return pool.Ping(pingCtx)
}
