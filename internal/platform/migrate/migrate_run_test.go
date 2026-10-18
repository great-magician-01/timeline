package migrate

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// 本文件覆盖 RunWithSchemas 在数据库不可用时的错误路径。使用 pgxpool 的连接拨号钩子
// 直接返回错误：不会建立任何网络连接，因此测试不依赖数据库，也不需要等待网络超时。

// newUnreachablePool 返回一个所有连接尝试都会立即失败的连接池。
func newUnreachablePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	config, err := pgxpool.ParseConfig("postgres://test:test@127.0.0.1:1/test?sslmode=disable")
	if err != nil {
		t.Fatalf("解析测试连接串失败: %v", err)
	}
	config.ConnConfig.DialFunc = func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("测试禁止建立数据库连接")
	}
	config.MinConns = 0
	config.MaxConns = 1

	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("创建测试连接池失败: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestRunWithSchemasReportsAcquireError(t *testing.T) {
	pool := newUnreachablePool(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	err := RunWithSchemas(context.Background(), pool, logger, "timeline", "user")
	if err == nil {
		t.Fatal("数据库不可用时应返回错误")
	}
	if !strings.Contains(err.Error(), "获取数据库连接失败") {
		t.Fatalf("错误信息应说明获取连接失败，实际 %v", err)
	}
}
