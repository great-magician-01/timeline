package server

// 首注册引导（空库首个注册用户自动成为唯一超级管理员）的隔离集成测试。
//
// 该用例必须在“人类用户数为 0”的库上验证，而共享的 timeline_test/user_test
// 是持久复用的测试 schema（测试不做物理删除），无法清空。因此这里为每个子测试
// 创建一对带唯一后缀的一次性 schema，跑完全部迁移后启动完整 HTTP 服务；
// cleanup 时 DROP 掉仅由本测试创建的 schema（不是业务数据）。
//
// 若测试数据库账号没有数据库级 CREATE 权限（集成测试文档只要求两个测试 schema
// 的权限），则无法创建一次性 schema，按要求 t.Skip 并说明原因。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/auth"
	"github.com/great-magician-01/timeline/internal/id"
	"github.com/great-magician-01/timeline/internal/jobs"
	"github.com/great-magician-01/timeline/internal/migrate"
	"github.com/great-magician-01/timeline/internal/storage"
)

const bootstrapTestPassword = "bootstrap-password-123"

type bootstrapTestEnv struct {
	pool *pgxpool.Pool
	base string
}

// setupBootstrapTestEnv 创建一对唯一命名的一次性 schema、执行全部迁移并启动完整服务。
func setupBootstrapTestEnv(t *testing.T) *bootstrapTestEnv {
	t.Helper()
	ctx := context.Background()
	cfg := loadIntegrationConfig(t)
	testCfg := *cfg
	testCfg.CookieSecure = false
	testCfg.LogLevel = "info"

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	timelineSchema := "timeline_bootstrap_" + suffix
	userSchema := "user_bootstrap_" + suffix
	dsn, err := integrationDSN(testCfg.DSN(), timelineSchema, userSchema)
	if err != nil {
		t.Fatalf("构造隔离 schema DSN 失败: %v", err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Skipf("无法创建测试数据库连接池，跳过失隔离首注册测试: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("测试数据库不可达，跳过失隔离首注册测试: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := migrate.RunWithSchemas(ctx, pool, log, timelineSchema, userSchema); err != nil {
		dropBootstrapSchemas(t, pool, timelineSchema, userSchema)
		pool.Close()
		// 账号缺少数据库级 CREATE 权限时无法创建一次性 schema；这属于环境限制，不是业务失败。
		if strings.Contains(err.Error(), "CREATE") || strings.Contains(err.Error(), "创建 schema") {
			t.Skipf("测试数据库账号缺少创建独立 schema 的权限，跳过失隔离首注册测试: %v", err)
		}
		t.Fatalf("执行隔离 schema 迁移失败: %v", err)
	}
	t.Cleanup(func() {
		dropBootstrapSchemas(t, pool, timelineSchema, userSchema)
		pool.Close()
	})

	ids, err := id.NewGenerator(1)
	if err != nil {
		t.Fatal(err)
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
	return &bootstrapTestEnv{pool: pool, base: httpServer.URL}
}

// dropBootstrapSchemas 只清理本测试自建的一次性 schema（不触碰共享测试 schema）。
func dropBootstrapSchemas(t *testing.T, pool *pgxpool.Pool, timelineSchema, userSchema string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// 先删账号 schema，再删业务 schema；CASCADE 兼顾迁移产生的依赖。
	for _, schema := range []string{userSchema, timelineSchema} {
		if _, err := pool.Exec(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS %q CASCADE`, schema)); err != nil {
			t.Logf("清理一次性测试 schema %s 失败（可人工清理）: %v", schema, err)
		}
	}
}

func TestBootstrapIntegration(t *testing.T) {
	if os.Getenv("TIMELINE_INTEGRATION") != "1" {
		t.Skip("设置 TIMELINE_INTEGRATION=1 后运行测试 schema 集成测试")
	}

	t.Run("空库公开配置提示需要初始化", func(t *testing.T) {
		env := setupBootstrapTestEnv(t)
		guest := newIntegrationClient(t, env.base)

		status, _, payload, raw := guest.call(http.MethodGet, "/api/v1/auth/config", false, nil)
		expectIntegrationStatus(t, status, http.StatusOK, raw)
		if payload["bootstrap_required"] != true {
			t.Fatalf("空库时 bootstrap_required 应为 true: %s", raw)
		}
		if payload["registration_enabled"] != true {
			t.Fatalf("默认注册开关应为开启: %s", raw)
		}
	})

	t.Run("首个注册成为唯一超管且第二个注册为普通用户", func(t *testing.T) {
		env := setupBootstrapTestEnv(t)
		ctx := context.Background()

		// 首个注册：201，返回 role=admin + is_super_admin=true。
		status, payload, raw := registerBootstrapUser(t, env.base, "boot_first", "boot-first@example.com")
		expectIntegrationStatus(t, status, http.StatusCreated, raw)
		firstUser := integrationUser(payload, t)
		if got := firstUser["role"]; got != auth.RoleAdmin {
			t.Fatalf("首个注册用户角色应为 admin，实际 %v: %s", got, raw)
		}
		if isSuper, _ := firstUser["is_super_admin"].(bool); !isSuper {
			t.Fatalf("首个注册用户 is_super_admin 应为 true: %s", raw)
		}
		firstID := integrationString(firstUser, t, "id")

		// 首个人类用户创建后，未初始化标志消失。
		guest := newIntegrationClient(t, env.base)
		status, _, configPayload, raw := guest.call(http.MethodGet, "/api/v1/auth/config", false, nil)
		expectIntegrationStatus(t, status, http.StatusOK, raw)
		if configPayload["bootstrap_required"] != false {
			t.Fatalf("已有人类用户后 bootstrap_required 应为 false: %s", raw)
		}

		// 首个用户登录后可访问管理接口；公开资料不暴露超管标记。
		loginIntegrationUser(t, guest, "boot_first", bootstrapTestPassword)
		status, _, mePayload, raw := guest.call(http.MethodGet, "/api/v1/auth/me", false, nil)
		expectIntegrationStatus(t, status, http.StatusOK, raw)
		if isSuper, _ := integrationUser(mePayload, t)["is_super_admin"].(bool); !isSuper {
			t.Fatalf("首个用户登录后应为超管: %s", raw)
		}
		status, _, _, raw = guest.call(http.MethodGet, "/api/v1/admin/users", false, nil)
		expectIntegrationStatus(t, status, http.StatusOK, raw)

		anonymous := newIntegrationClient(t, env.base)
		status, _, publicPayload, raw := anonymous.call(http.MethodGet, "/api/v1/users/boot_first", false, nil)
		expectIntegrationStatus(t, status, http.StatusOK, raw)
		if publicUser, ok := publicPayload["user"].(map[string]any); ok {
			if _, exists := publicUser["is_super_admin"]; exists {
				t.Fatalf("公开资料不应返回 is_super_admin: %s", raw)
			}
		}

		// 第二个注册：普通用户，不能访问管理接口。
		second := newIntegrationClient(t, env.base)
		second.refreshCSRF()
		status, _, payload, raw = second.call(http.MethodPost, "/api/v1/auth/register", true, map[string]any{
			"username": "boot_second",
			"email":    "boot-second@example.com",
			"password": bootstrapTestPassword,
		})
		expectIntegrationStatus(t, status, http.StatusCreated, raw)
		secondUser := integrationUser(payload, t)
		if got := secondUser["role"]; got != auth.RoleUser {
			t.Fatalf("第二个注册用户角色应为 user，实际 %v: %s", got, raw)
		}
		if _, ok := secondUser["is_super_admin"]; ok {
			t.Fatalf("第二个注册用户不应带 is_super_admin: %s", raw)
		}
		loginIntegrationUser(t, second, "boot_second", bootstrapTestPassword)
		status, _, _, raw = second.call(http.MethodGet, "/api/v1/admin/users", false, nil)
		expectIntegrationStatus(t, status, http.StatusForbidden, raw)

		// 数据库唯一超管 + 计数辅助函数。
		var superAdmins int
		if err := env.pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM users WHERE is_super_admin`).Scan(&superAdmins); err != nil {
			t.Fatalf("统计超管失败: %v", err)
		}
		if superAdmins != 1 {
			t.Fatalf("空库首注册后超管数量应为 1，实际 %d", superAdmins)
		}
		if role, isSuper := integrationRole(t, env.pool, firstID); role != auth.RoleAdmin || !isSuper {
			t.Fatalf("首个用户数据库角色不符: role=%s is_super_admin=%v", role, isSuper)
		}
		bootstrapRequired, err := auth.BootstrapRequired(ctx, env.pool)
		if err != nil {
			t.Fatalf("读取初始化状态失败: %v", err)
		}
		if bootstrapRequired {
			t.Fatal("已有人类用户时 BootstrapRequired 应为 false")
		}
		humanUsers, statusSuperAdmins, err := auth.BootstrapStatus(ctx, env.pool)
		if err != nil {
			t.Fatalf("读取初始化统计失败: %v", err)
		}
		if humanUsers != 2 || statusSuperAdmins != 1 {
			t.Fatalf("初始化统计不符: humanUsers=%d superAdmins=%d", humanUsers, statusSuperAdmins)
		}
	})

	t.Run("空库忽略注册开关", func(t *testing.T) {
		env := setupBootstrapTestEnv(t)
		ctx := context.Background()
		if _, err := env.pool.Exec(ctx, `
			INSERT INTO system_settings (key, value) VALUES ('registration.enabled', 'false'::jsonb)
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`); err != nil {
			t.Fatalf("关闭测试注册开关失败: %v", err)
		}

		guest := newIntegrationClient(t, env.base)
		status, _, configPayload, raw := guest.call(http.MethodGet, "/api/v1/auth/config", false, nil)
		expectIntegrationStatus(t, status, http.StatusOK, raw)
		if configPayload["registration_enabled"] != false {
			t.Fatalf("注册开关应已关闭: %s", raw)
		}
		if configPayload["bootstrap_required"] != true {
			t.Fatalf("空库时 bootstrap_required 应为 true: %s", raw)
		}

		// 空库例外：注册开关关闭也允许首个注册，并成为超管。
		status, payload, raw := registerBootstrapUser(t, env.base, "boot_closed", "boot-closed@example.com")
		expectIntegrationStatus(t, status, http.StatusCreated, raw)
		user := integrationUser(payload, t)
		if got := user["role"]; got != auth.RoleAdmin {
			t.Fatalf("注册关闭时首个用户角色应为 admin，实际 %v: %s", got, raw)
		}
		if isSuper, _ := user["is_super_admin"].(bool); !isSuper {
			t.Fatalf("注册关闭时首个用户应为超管: %s", raw)
		}

		// 已有人类用户后，注册开关恢复普通语义：第二个注册应 403。
		status, payload, raw = registerBootstrapUser(t, env.base, "boot_closed_second", "boot-closed-second@example.com")
		expectIntegrationStatus(t, status, http.StatusForbidden, raw)
		if code := integrationErrorCode(payload); code != "forbidden" {
			t.Fatalf("注册关闭时第二个注册应返回 forbidden，实际 %q: %s", code, raw)
		}
	})

	t.Run("并发首注册只产生一个超管", func(t *testing.T) {
		env := setupBootstrapTestEnv(t)
		ctx := context.Background()
		const concurrentUsers = 4 // 注册限频为 5 次/10 分钟/IP，并发数需小于上限。

		results := make(chan bootstrapRegisterResult, concurrentUsers)
		var wg sync.WaitGroup
		for index := 0; index < concurrentUsers; index++ {
			username := fmt.Sprintf("boot_race_%d", index)
			email := fmt.Sprintf("boot-race-%d@example.com", index)
			wg.Add(1)
			go func() {
				defer wg.Done()
				results <- registerBootstrapUserHTTP(env.base, username, email)
			}()
		}
		wg.Wait()
		close(results)

		superAdminCount := 0
		successCount := 0
		for result := range results {
			if result.err != nil {
				t.Errorf("并发注册 %s 请求失败: %v", result.username, result.err)
				continue
			}
			if result.status != http.StatusCreated {
				t.Errorf("并发注册 %s 应返回 201，实际 %d: %s", result.username, result.status, result.body)
				continue
			}
			successCount++
			switch {
			case result.role == auth.RoleAdmin && result.isSuper:
				superAdminCount++
			case result.role == auth.RoleUser && !result.isSuper:
			default:
				t.Errorf("并发注册 %s 返回了意外角色 role=%q is_super_admin=%v",
					result.username, result.role, result.isSuper)
			}
		}
		if successCount != concurrentUsers {
			t.Fatalf("并发首注册应全部成功，成功 %d/%d", successCount, concurrentUsers)
		}
		if superAdminCount != 1 {
			t.Fatalf("并发首注册应只产生 1 个超管，实际 %d", superAdminCount)
		}

		var dbSuperAdmins, humanUsers int
		if err := env.pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM users WHERE is_super_admin`).Scan(&dbSuperAdmins); err != nil {
			t.Fatalf("统计超管失败: %v", err)
		}
		if err := env.pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM users WHERE NOT is_ai`).Scan(&humanUsers); err != nil {
			t.Fatalf("统计人类用户失败: %v", err)
		}
		if dbSuperAdmins != 1 || humanUsers != concurrentUsers {
			t.Fatalf("并发注册后数据不符: superAdmins=%d humanUsers=%d", dbSuperAdmins, humanUsers)
		}

		// 唯一超管可登录并访问管理接口，其余注册账号为普通用户。
		var superAdminUsername string
		if err := env.pool.QueryRow(ctx,
			`SELECT username FROM users WHERE is_super_admin`).Scan(&superAdminUsername); err != nil {
			t.Fatalf("读取超管用户名失败: %v", err)
		}
		winner := newIntegrationClient(t, env.base)
		loginIntegrationUser(t, winner, superAdminUsername, bootstrapTestPassword)
		status, _, _, raw := winner.call(http.MethodGet, "/api/v1/admin/users", false, nil)
		expectIntegrationStatus(t, status, http.StatusOK, raw)
	})
}

// registerBootstrapUser 通过游客 CSRF + 注册接口创建账号，返回状态、JSON 响应与原始响应体。
func registerBootstrapUser(t *testing.T, base, username, email string) (int, map[string]any, string) {
	t.Helper()
	client := newIntegrationClient(t, base)
	client.refreshCSRF()
	status, _, payload, raw := client.call(http.MethodPost, "/api/v1/auth/register", true, map[string]any{
		"username": username,
		"email":    email,
		"password": bootstrapTestPassword,
	})
	return status, payload, raw
}

type bootstrapRegisterResult struct {
	username string
	status   int
	role     string
	isSuper  bool
	body     string
	err      error
}

// registerBootstrapUserHTTP 供并发子测试在 goroutine 中使用，不调用 *testing.T 的断言方法
// （testing.T 的 Fatal 系列不允许在非测试 goroutine 调用），错误通过返回值上报。
func registerBootstrapUserHTTP(base, username, email string) bootstrapRegisterResult {
	result := bootstrapRegisterResult{username: username}
	jar, err := cookiejar.New(nil)
	if err != nil {
		result.err = err
		return result
	}
	client := &http.Client{Jar: jar, Timeout: 30 * time.Second}

	resp, err := client.Get(base + "/api/v1/auth/csrf")
	if err != nil {
		result.err = err
		return result
	}
	var csrfPayload struct {
		CSRFToken string `json:"csrf_token"`
	}
	err = json.NewDecoder(resp.Body).Decode(&csrfPayload)
	resp.Body.Close()
	if err != nil || csrfPayload.CSRFToken == "" {
		result.err = fmt.Errorf("获取游客 CSRF 令牌失败: %v", err)
		return result
	}

	body, err := json.Marshal(map[string]string{
		"username": username,
		"email":    email,
		"password": bootstrapTestPassword,
	})
	if err != nil {
		result.err = err
		return result
	}
	req, err := http.NewRequest(http.MethodPost, base+"/api/v1/auth/register", bytes.NewReader(body))
	if err != nil {
		result.err = err
		return result
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrfPayload.CSRFToken)
	resp, err = client.Do(req)
	if err != nil {
		result.err = err
		return result
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		result.err = err
		return result
	}
	result.status = resp.StatusCode
	result.body = string(data)
	var payload struct {
		User struct {
			Role         string `json:"role"`
			IsSuperAdmin bool   `json:"is_super_admin"`
		} `json:"user"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		result.err = fmt.Errorf("注册响应不是合法 JSON: %v，原文: %s", err, result.body)
		return result
	}
	result.role = payload.User.Role
	result.isSuper = payload.User.IsSuperAdmin
	return result
}
