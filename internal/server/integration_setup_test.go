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

	"github.com/jackc/pgx/v5"
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
	ensureIntegrationAdmin(t, pool, ids)
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

// integrationShortSuffix 返回较短的数字后缀，用于受 32 位长度限制的用户名（如 grant_target_xxx）。
func integrationShortSuffix() string {
	suffix := integrationSuffix()
	if len(suffix) > 12 {
		return suffix[len(suffix)-12:]
	}
	return suffix
}

// 共享测试 schema 中固定测试账号的约定值；相关集成测试都使用这些账号。
const (
	integrationAdminUsername = "admin_integration"
	integrationAdminEmail    = "admin-integration@example.com"
	integrationAdminPassword = "admin-password-123"
	integrationModUsername   = "mod_integration"
	integrationModEmail      = "mod-integration@example.com"
)

// ensureIntegrationAdmin 幂等准备共享测试 schema（timeline_test/user_test）中的管理员账号。
// 共享 schema 持久存在且测试不做物理删除，因此这里只做“缺失则创建、存在则修复”：
//   - admin_integration：固定密码的管理员；若库内尚无超管，则提升为超管（满足单超管唯一索引）；
//   - mod_integration：普通管理员，供“普通管理员越权被拒”的用例使用。
func ensureIntegrationAdmin(t *testing.T, pool *pgxpool.Pool, ids auth.IDGenerator) {
	t.Helper()
	passwordHash, err := auth.HashPassword(integrationAdminPassword)
	if err != nil {
		t.Fatalf("生成测试管理员密码哈希失败: %v", err)
	}
	adminID := ensureIntegrationAccount(t, pool, ids, integrationAdminUsername, integrationAdminEmail, passwordHash)

	// 只在库内没有超管时提升，避免违反 users_single_super_admin 唯一索引。
	var isSuperAdmin bool
	if err := pool.QueryRow(context.Background(),
		`SELECT is_super_admin FROM users WHERE id = $1`, adminID).Scan(&isSuperAdmin); err != nil {
		t.Fatalf("读取测试超管标记失败: %v", err)
	}
	if !isSuperAdmin {
		if _, err := pool.Exec(context.Background(), `
			UPDATE users SET is_super_admin = TRUE, updated_at = now()
			WHERE id = $1 AND NOT EXISTS (SELECT 1 FROM users WHERE is_super_admin)`, adminID); err != nil {
			t.Fatalf("提升测试管理员为超管失败: %v", err)
		}
		if err := pool.QueryRow(context.Background(),
			`SELECT is_super_admin FROM users WHERE id = $1`, adminID).Scan(&isSuperAdmin); err != nil {
			t.Fatalf("复核测试超管标记失败: %v", err)
		}
		if !isSuperAdmin {
			t.Fatalf("测试 schema 已存在其他超级管理员，无法把 %s 提升为超管；请人工检查 user_test 数据",
				integrationAdminUsername)
		}
	}

	ensureIntegrationAccount(t, pool, ids, integrationModUsername, integrationModEmail, passwordHash)
}

// ensureIntegrationAccount 确保固定测试账号存在且处于可登录、可用于权限用例的状态，返回账号 ID。
// 已存在时只修复测试账号自身的已知字段（密码/角色/状态/邮箱），不删除任何数据。
func ensureIntegrationAccount(
	t *testing.T,
	pool *pgxpool.Pool,
	ids auth.IDGenerator,
	username, email, passwordHash string,
) int64 {
	t.Helper()
	ctx := context.Background()
	var accountID int64
	err := pool.QueryRow(ctx, `SELECT id FROM users WHERE LOWER(username) = LOWER($1)`, username).Scan(&accountID)
	if err == nil {
		// 上一次失败运行可能留下停用/角色变化等状态，这里修复固定测试账号。
		if _, err := pool.Exec(ctx, `
			UPDATE users
			SET email = $2, password_hash = $3, role = 'admin', is_ai = FALSE,
			    status = 'active', suspended_at = NULL, require_follow_approval = FALSE,
			    updated_at = now()
			WHERE id = $1`, accountID, email, passwordHash); err != nil {
			t.Fatalf("修复测试账号 %s 失败: %v", username, err)
		}
		return accountID
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("查询测试账号 %s 失败: %v", username, err)
	}

	newID, err := ids.Next()
	if err != nil {
		t.Fatalf("生成测试账号 %s 的 ID 失败: %v", username, err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (
			id, username, email, password_hash, nickname, role, is_super_admin,
			is_ai, status, require_follow_approval, token_version
		) VALUES ($1, $2, $3, $4, $2, 'admin', FALSE, FALSE, 'active', FALSE, 1)`,
		newID, username, email, passwordHash); err != nil {
		t.Fatalf("创建测试账号 %s 失败: %v", username, err)
	}
	return newID
}
