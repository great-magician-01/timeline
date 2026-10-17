package testkit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/modules/auth/domain"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/platform/config"
	platformid "github.com/great-magician-01/timeline/internal/platform/id"
	"github.com/great-magician-01/timeline/internal/platform/storage"
)

// SchemaNames 是一对隔离测试 schema。
type SchemaNames struct {
	Timeline string
	User     string
}

// SharedSchemas 返回共享集成测试 schema（持久复用，不做物理删除）。
func SharedSchemas() SchemaNames {
	return SchemaNames{Timeline: IntegrationTimelineSchema, User: IntegrationUserSchema}
}

// IsolatedSchemas 以唯一后缀生成一次性 schema 名称；名称只含字母、数字与下划线。
func IsolatedSchemas(prefix string) SchemaNames {
	suffix := uniqueSuffix()
	return SchemaNames{
		Timeline: fmt.Sprintf("timeline_%s_%s", prefix, suffix),
		User:     fmt.Sprintf("user_%s_%s", prefix, suffix),
	}
}

// EnvOptions 控制测试环境创建行为。
type EnvOptions struct {
	// Schemas 是要使用的隔离 schema 对。
	Schemas SchemaNames
	// DropSchemasOnCleanup 为 true 时在 cleanup 中 DROP 本次创建的一次性 schema；
	// 共享测试 schema 必须为 false（测试不做物理删除）。
	DropSchemasOnCleanup bool
	// SkipIfSchemaDenied 在账号缺少数据库级 CREATE 权限时 Skip 而不是 Fatal，
	// 供需要一次性空库 schema 的用例在环境受限时优雅跳过。
	SkipIfSchemaDenied bool
	// ResetRegistrationEnabled 在迁移后把注册开关恢复为开启。
	ResetRegistrationEnabled bool
	// SeedIntegrationAdmins 准备共享测试管理员账号。
	SeedIntegrationAdmins bool
	// Clock 是注入的时钟；nil 时使用系统时钟。
	Clock clock.Clock
}

// SharedEnvOptions 返回共享测试 schema 的标准环境选项。
func SharedEnvOptions() EnvOptions {
	return EnvOptions{
		Schemas:                  SharedSchemas(),
		ResetRegistrationEnabled: true,
		SeedIntegrationAdmins:    true,
	}
}

// IsolatedEnvOptions 返回一次性空库 schema 的环境选项（首注册引导用例）。
func IsolatedEnvOptions(prefix string) EnvOptions {
	return EnvOptions{
		Schemas:              IsolatedSchemas(prefix),
		DropSchemasOnCleanup: true,
		SkipIfSchemaDenied:   true,
	}
}

// Env 是一次测试的基础资源：配置、测试 schema 连接池、ID 生成器、附件存储与时钟。
// 服务装配（app.New + 路由）在 server.go 中完成，便于按需注入 fake 时钟。
type Env struct {
	Config  *config.Config
	Pool    *pgxpool.Pool
	IDs     *platformid.Generator
	Store   *storage.Store
	Log     *slog.Logger
	Clock   clock.Clock
	Schemas SchemaNames
}

// NewEnv 建立测试环境：加载配置 → 连接测试 schema → 迁移 → 可选的开关恢复与账号准备，
// 并注册 pool/schema 清理。调用方未开启集成测试时应先 SkipUnlessIntegration。
func NewEnv(t *testing.T, options EnvOptions) *Env {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)

	cfg := LoadConfig(t)
	clk := options.Clock
	if clk == nil {
		clk = clock.System()
	}
	// 一次性 schema 的 search_path 默认只有新 schema；数据库级扩展（pg_trgm）若已由
	// 之前共享 schema 迁移安装到别的 schema，0014 的 gin_trgm_ops 会解析失败。
	// 这里把扩展所在 schema 追加到 search_path（迁移与测试连接共用）。
	searchPath := []string{options.Schemas.Timeline, options.Schemas.User}
	if options.DropSchemasOnCleanup {
		if extSchema := ExtensionSchema(t, ctx, cfg.DSN(), "pg_trgm"); extSchema != "" {
			searchPath = appendIfMissing(searchPath, extSchema)
		}
	}
	dsn, err := integrationDSNForSchemas(cfg.DSN(), searchPath)
	if err != nil {
		t.Fatal(err)
	}
	pool := OpenPool(t, ctx, dsn)

	if err := MigrateSchemas(t, ctx, pool, options.Schemas.Timeline, options.Schemas.User); err != nil {
		if options.SkipIfSchemaDenied && schemaPermissionDenied(err) {
			dropSchemas(t, ctx, pool, options.Schemas)
			pool.Close()
			t.Skipf("测试数据库账号缺少创建独立 schema 的权限，跳过该用例: %v", err)
		}
		if options.DropSchemasOnCleanup {
			dropSchemas(t, ctx, pool, options.Schemas)
		}
		pool.Close()
		t.Fatalf("执行测试 schema 迁移失败: %v", err)
	}
	t.Cleanup(func() {
		if options.DropSchemasOnCleanup {
			dropSchemas(t, context.Background(), pool, options.Schemas)
		}
		pool.Close()
	})

	if options.ResetRegistrationEnabled {
		ResetRegistrationEnabled(t, ctx, pool, clk.Now())
	}
	ids, err := platformid.NewGenerator(1)
	if err != nil {
		t.Fatalf("初始化雪花 ID 生成器失败: %v", err)
	}
	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatalf("初始化测试附件目录失败: %v", err)
	}
	env := &Env{
		Config:  cfg,
		Pool:    pool,
		IDs:     ids,
		Store:   store,
		Log:     testLogger(),
		Clock:   clk,
		Schemas: options.Schemas,
	}
	if options.SeedIntegrationAdmins {
		SeedIntegrationAdmins(t, env)
	}
	return env
}

// testLogger 返回测试日志器；设置 TIMELINE_TESTKIT_DEBUG=1 时输出到 stderr，
// 便于定位集成测试中的 500 等内部错误，默认丢弃日志。
func testLogger() *slog.Logger {
	if os.Getenv("TIMELINE_TESTKIT_DEBUG") == "1" {
		return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// SeedIntegrationAdmins 幂等准备共享测试 schema 中的管理员账号：
//   - admin_integration：固定密码的管理员；若库内尚无超管则提升为超管（满足单超管唯一索引）；
//   - mod_integration：普通管理员，供“普通管理员越权被拒”的用例使用。
//
// 共享 schema 持久存在且测试不做物理删除，因此这里只做“缺失则创建、存在则修复”。
func SeedIntegrationAdmins(t *testing.T, env *Env) {
	t.Helper()
	ctx := context.Background()
	passwordHash, err := domain.HashPassword(IntegrationAdminPassword)
	if err != nil {
		t.Fatalf("生成测试管理员密码哈希失败: %v", err)
	}
	adminID := EnsureIntegrationAccount(t, ctx, env.Pool, env.IDs, env.Clock.Now(),
		IntegrationAdminUsername, IntegrationAdminEmail, passwordHash)

	// 只在库内没有超管时提升，避免违反 users_single_super_admin 唯一索引。
	var isSuperAdmin bool
	if err := env.Pool.QueryRow(ctx,
		`SELECT is_super_admin FROM users WHERE id = $1`, adminID).Scan(&isSuperAdmin); err != nil {
		t.Fatalf("读取测试超管标记失败: %v", err)
	}
	if !isSuperAdmin {
		if _, err := env.Pool.Exec(ctx, `
			UPDATE users SET is_super_admin = TRUE, updated_at = $2
			WHERE id = $1 AND NOT EXISTS (SELECT 1 FROM users WHERE is_super_admin)`,
			adminID, env.Clock.Now()); err != nil {
			t.Fatalf("提升测试管理员为超管失败: %v", err)
		}
		if err := env.Pool.QueryRow(ctx,
			`SELECT is_super_admin FROM users WHERE id = $1`, adminID).Scan(&isSuperAdmin); err != nil {
			t.Fatalf("复核测试超管标记失败: %v", err)
		}
		if !isSuperAdmin {
			t.Fatalf("测试 schema 已存在其他超级管理员，无法把 %s 提升为超管；请人工检查 %s 数据",
				IntegrationAdminUsername, env.Schemas.User)
		}
	}

	EnsureIntegrationAccount(t, ctx, env.Pool, env.IDs, env.Clock.Now(),
		IntegrationModUsername, IntegrationModEmail, passwordHash)
}

// EnsureIntegrationAccount 确保固定测试账号存在且处于可登录、可用于权限用例的状态，返回账号 ID。
// 已存在时只修复测试账号自身的已知字段（密码/角色/状态/邮箱），不删除任何数据；at 为应用时间。
func EnsureIntegrationAccount(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	ids IDGenerator,
	at time.Time,
	username, email, passwordHash string,
) int64 {
	t.Helper()
	var accountID int64
	err := pool.QueryRow(ctx, `SELECT id FROM users WHERE LOWER(username) = LOWER($1)`, username).Scan(&accountID)
	if err == nil {
		// 上一次失败运行可能留下停用/角色变化等状态，这里修复固定测试账号。
		if _, err := pool.Exec(ctx, `
			UPDATE users
			SET email = $2, password_hash = $3, role = 'admin', is_ai = FALSE,
			    status = 'active', suspended_at = NULL, require_follow_approval = FALSE,
			    updated_at = $4
			WHERE id = $1`, accountID, email, passwordHash, at); err != nil {
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
	// 0011 迁移后 users.created_at/updated_at 没有数据库默认值，必须显式传应用时间。
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (
			id, username, email, password_hash, nickname, role, is_super_admin,
			is_ai, status, require_follow_approval, token_version, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $2, 'admin', FALSE, FALSE, 'active', FALSE, 1, $5, $5)`,
		newID, username, email, passwordHash, at); err != nil {
		t.Fatalf("创建测试账号 %s 失败: %v", username, err)
	}
	return newID
}

// dropSchemas 只清理测试自建的 schema（不触碰共享测试 schema）。
func dropSchemas(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schemas SchemaNames) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// 先删账号 schema，再删业务 schema；CASCADE 兼顾迁移产生的依赖。
	for _, schema := range []string{schemas.User, schemas.Timeline} {
		if _, err := pool.Exec(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS %q CASCADE`, schema)); err != nil {
			t.Logf("清理一次性测试 schema %s 失败（可人工清理）: %v", schema, err)
		}
	}
}

// appendIfMissing 追加不重复的 search_path 项。
func appendIfMissing(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

// schemaPermissionDenied 判断迁移失败是否由缺少 schema 创建权限导致（环境限制而非业务失败）。
// 只匹配权限类文案；包含 "CREATE" 的错误可能是 0014 的 CREATE INDEX CONCURRENTLY，
// 不能据此判定为权限问题（否则 pg_trgm 环境问题会被误 Skip）。
func schemaPermissionDenied(err error) bool {
	message := err.Error()
	if strings.Contains(message, "operator class") || strings.Contains(message, "gin_trgm_ops") {
		return false
	}
	return strings.Contains(message, "permission denied") ||
		strings.Contains(message, "权限") ||
		strings.Contains(message, "must be owner")
}
