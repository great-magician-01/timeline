package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/auth"
	"github.com/great-magician-01/timeline/internal/id"
	"github.com/great-magician-01/timeline/internal/jobs"
	"github.com/great-magician-01/timeline/internal/migrate"
	"github.com/great-magician-01/timeline/internal/storage"
)

// setupIntegrationServer 在 timeline_test / user_test 隔离 schema 上启动完整服务，
// 供集成测试复用（账号集成测试保留其历史实现，未合并）。
// 调用方负责在未设置 TIMELINE_INTEGRATION=1 时先 Skip。
func setupIntegrationServer(t *testing.T) (*pgxpool.Pool, *Server, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)

	cfg := loadIntegrationConfig(t)
	testCfg := *cfg
	testCfg.CookieSecure = false
	testCfg.LogLevel = "info"
	dsn, err := integrationDSN(testCfg.DSN(), "timeline_test", "user_test")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("连接测试 schema 失败: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("Ping 测试数据库失败: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := migrate.RunWithSchemas(ctx, pool, log, "timeline_test", "user_test"); err != nil {
		t.Fatalf("执行测试 schema 迁移失败: %v", err)
	}
	// 上次失败可能停在关闭注册步骤；每次只在测试 schema 恢复默认开关。
	if _, err := pool.Exec(ctx, `
		INSERT INTO system_settings (key, value) VALUES ('registration.enabled', 'true'::jsonb)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`); err != nil {
		t.Fatalf("恢复测试注册开关失败: %v", err)
	}
	ids, err := id.NewGenerator(1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.CreateInitialAdmin(ctx, pool, ids, auth.RegisterInput{
		Username: "admin_integration",
		Email:    "admin-integration@example.com",
		Password: "admin-password-123",
	}); err != nil && !errors.Is(err, auth.ErrAdminExists) {
		t.Fatalf("初始化测试管理员失败: %v", err)
	}
	state := NewState()
	state.SetPool(pool)
	state.MarkMigrationsDone()
	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatalf("初始化测试附件目录失败: %v", err)
	}
	srv := New(&testCfg, log, state, ids, jobs.NewRunner(state, ids, log), store)
	httpServer := httptest.NewServer(srv.Handler())
	t.Cleanup(httpServer.Close)
	return pool, srv, httpServer.URL
}

// integrationSuffix 生成用于隔离本次测试数据的唯一后缀。
func integrationSuffix() string { return fmt.Sprintf("%d", time.Now().UnixNano()) }
