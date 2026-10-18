package settings

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// 本文件覆盖 Store 包装方法在数据库不可用时的行为。
// 连接池使用“拨号立即失败”的构造：DialFunc 直接返回错误，不建立任何网络连接。

// newUnreachablePool 返回所有连接尝试都会立即失败的连接池。
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

// TestStoreMethodsWithUnavailableDatabase 覆盖 Store 四个方法的连接失败路径。
func TestStoreMethodsWithUnavailableDatabase(t *testing.T) {
	ctx := context.Background()
	store := NewStore(newUnreachablePool(t))

	if _, err := store.Load(ctx); err == nil {
		t.Fatal("数据库不可用时 Store.Load 应返回错误")
	}
	if got := store.LoadOrDefaults(ctx); got != Defaults() {
		t.Fatalf("数据库不可用时 LoadOrDefaults 应返回默认值: %+v", got)
	}
	if _, err := store.LoadWebSearch(ctx); err == nil {
		t.Fatal("数据库不可用时 Store.LoadWebSearch 应返回错误")
	}
	if err := store.Set(ctx, KeyRegistrationEnabled, false, 9,
		time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("数据库不可用时 Store.Set 应返回错误")
	}
}
