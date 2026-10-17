package testkit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/platform/config"
	"github.com/great-magician-01/timeline/internal/platform/migrate"
)

// 共享集成测试 schema 与固定测试账号约定值（与旧集成测试保持不变）。
const (
	// IntegrationTimelineSchema 是共享业务测试 schema。
	IntegrationTimelineSchema = "timeline_test"
	// IntegrationUserSchema 是共享账号测试 schema。
	IntegrationUserSchema = "user_test"

	// IntegrationAdminUsername 是共享测试超管账号。
	IntegrationAdminUsername = "admin_integration"
	// IntegrationAdminEmail 是共享测试超管邮箱。
	IntegrationAdminEmail = "admin-integration@example.com"
	// IntegrationAdminPassword 是共享测试账号密码（超管与普通管理员共用）。
	IntegrationAdminPassword = "admin-password-123"
	// IntegrationModUsername 是共享测试普通管理员账号。
	IntegrationModUsername = "mod_integration"
	// IntegrationModEmail 是共享测试普通管理员邮箱。
	IntegrationModEmail = "mod-integration@example.com"
)

// IntegrationEnabled 报告是否显式开启集成测试；未开启时调用方应 t.Skip。
func IntegrationEnabled() bool { return os.Getenv("TIMELINE_INTEGRATION") == "1" }

// SkipUnlessIntegration 在未设置 TIMELINE_INTEGRATION=1 时跳过测试。
func SkipUnlessIntegration(t *testing.T) {
	t.Helper()
	if !IntegrationEnabled() {
		t.Skip("设置 TIMELINE_INTEGRATION=1 后运行测试 schema 集成测试")
	}
}

// IDGenerator 生成雪花 ID；platform/id.Generator 直接满足。
type IDGenerator interface {
	Next() (int64, error)
}

// LoadConfig 从仓库根目录加载配置（读取 .env 与进程环境变量），并调整为测试取值：
// CookieSecure=false（httptest 使用 http）、LogLevel=info。
//
// 集成测试可能在 internal/server 或 internal/testkit 等不同目录运行，因此先切到
// 仓库根目录加载 .env，再恢复工作目录。
func LoadConfig(t *testing.T) *config.Config {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs(filepath.Join(wd, "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("切换到仓库根目录失败: %v", err)
	}
	defer func() {
		if err := os.Chdir(wd); err != nil {
			t.Errorf("恢复测试工作目录失败: %v", err)
		}
	}()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("加载集成测试配置失败: %v", err)
	}
	cfg.CookieSecure = false
	cfg.LogLevel = "info"
	return cfg
}

// IntegrationDSN 在开发实例 DSN 上覆盖 search_path 到隔离测试 schema。
func IntegrationDSN(productionDSN, timelineSchema, userSchema string) (string, error) {
	return integrationDSNForSchemas(productionDSN, []string{timelineSchema, userSchema})
}

// integrationDSNForSchemas 用给定 search_path 列表覆盖 DSN。额外 schema 用于把
// 数据库级扩展（如 pg_trgm）所在 schema 加入一次性 schema 迁移的 search_path。
func integrationDSNForSchemas(productionDSN string, schemas []string) (string, error) {
	parsed, err := url.Parse(productionDSN)
	if err != nil {
		return "", fmt.Errorf("解析测试 DSN 失败: %w", err)
	}
	query := parsed.Query()
	query.Set("search_path", strings.Join(schemas, ","))
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

// ExtensionSchema 查询数据库级扩展安装所在的 schema；扩展不存在时返回空串。
// 一次性隔离 schema 的 search_path 不含扩展所在 schema 时，0014 的 gin_trgm_ops
// 会解析失败（ISOLATED 用例在已安装 pg_trgm 的开发库上会误判为环境限制），
// 因此把该 schema 追加进迁移 search_path。
func ExtensionSchema(t *testing.T, ctx context.Context, productionDSN, extension string) string {
	t.Helper()
	conn, err := pgx.Connect(ctx, productionDSN)
	if err != nil {
		t.Fatalf("查询扩展 schema 时连接数据库失败: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var schema string
	err = conn.QueryRow(ctx, `
		SELECT n.nspname
		FROM pg_extension AS e
		JOIN pg_namespace AS n ON n.oid = e.extnamespace
		WHERE e.extname = $1`, extension).Scan(&schema)
	if errors.Is(err, pgx.ErrNoRows) {
		return ""
	}
	if err != nil {
		t.Fatalf("查询扩展 %s 所在 schema 失败: %v", extension, err)
	}
	return schema
}

// OpenPool 打开测试 schema 连接池并 Ping；调用方负责 Close（NewEnv 已注册 cleanup）。
func OpenPool(t *testing.T, ctx context.Context, dsn string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("连接测试 schema 失败: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("Ping 测试数据库失败: %v", err)
	}
	return pool
}

// MigrateSchemas 对隔离 schema 执行全部内嵌迁移。
func MigrateSchemas(t *testing.T, ctx context.Context, pool *pgxpool.Pool, timelineSchema, userSchema string) error {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return migrate.RunWithSchemas(ctx, pool, log, timelineSchema, userSchema)
}

// ResetRegistrationEnabled 把测试 schema 的注册开关恢复为开启（共享 schema 复用前清理）。
// 0011 迁移后 system_settings.updated_at 没有数据库默认值，必须显式传应用时间。
func ResetRegistrationEnabled(t *testing.T, ctx context.Context, pool *pgxpool.Pool, at time.Time) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO system_settings (key, value, updated_at) VALUES ('registration.enabled', 'true'::jsonb, $1)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`,
		at); err != nil {
		t.Fatalf("恢复测试注册开关失败: %v", err)
	}
}
