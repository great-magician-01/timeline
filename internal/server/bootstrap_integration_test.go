package server_test

// 首注册引导（空库首个注册用户自动成为唯一超级管理员）的隔离集成测试。
//
// 该用例必须在“人类用户数为 0”的库上验证，而共享的 timeline_test/user_test
// 是持久复用的测试 schema（测试不做物理删除），无法清空。因此 testkit 为每个
// 子测试创建一对带唯一后缀的一次性 schema，跑完全部迁移后启动完整 HTTP 服务；
// cleanup 时 DROP 掉仅由本测试创建的 schema（不是业务数据）。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"sync"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/testkit"
)

const bootstrapTestPassword = "bootstrap-password-123"

func TestBootstrapIntegration(t *testing.T) {
	t.Run("空库公开配置提示需要初始化", func(t *testing.T) {
		srv := testkit.NewIsolatedServer(t, "bootstrap")
		guest := srv.Client(t)

		status, _, payload, raw := guest.Call(http.MethodGet, "/api/v1/auth/config", false, nil)
		testkit.ExpectStatus(t, status, http.StatusOK, raw)
		if payload["bootstrap_required"] != true {
			t.Fatalf("空库时 bootstrap_required 应为 true: %s", raw)
		}
		if payload["registration_enabled"] != true {
			t.Fatalf("默认注册开关应为开启: %s", raw)
		}
	})

	t.Run("首个注册成为唯一超管且第二个注册为普通用户", func(t *testing.T) {
		srv := testkit.NewIsolatedServer(t, "bootstrap")
		ctx := context.Background()

		// 首个注册：201，返回 role=admin + is_super_admin=true。
		status, payload, raw := registerBootstrapUser(t, srv, "boot_first", "boot-first@example.com")
		testkit.ExpectStatus(t, status, http.StatusCreated, raw)
		firstUser := testkit.MustUser(t, payload)
		if got := firstUser["role"]; got != model.RoleAdmin {
			t.Fatalf("首个注册用户角色应为 admin，实际 %v: %s", got, raw)
		}
		if isSuper, _ := firstUser["is_super_admin"].(bool); !isSuper {
			t.Fatalf("首个注册用户 is_super_admin 应为 true: %s", raw)
		}
		firstID := testkit.MustString(t, firstUser, "id")

		// 首个人类用户创建后，未初始化标志消失。
		guest := srv.Client(t)
		status, _, configPayload, raw := guest.Call(http.MethodGet, "/api/v1/auth/config", false, nil)
		testkit.ExpectStatus(t, status, http.StatusOK, raw)
		if configPayload["bootstrap_required"] != false {
			t.Fatalf("已有人类用户后 bootstrap_required 应为 false: %s", raw)
		}

		// 首个用户登录后可访问管理接口；公开资料不暴露超管标记。
		guest.LoginAndSetCSRF("boot_first", bootstrapTestPassword)
		status, _, mePayload, raw := guest.Call(http.MethodGet, "/api/v1/auth/me", false, nil)
		testkit.ExpectStatus(t, status, http.StatusOK, raw)
		if isSuper, _ := testkit.MustUser(t, mePayload)["is_super_admin"].(bool); !isSuper {
			t.Fatalf("首个用户登录后应为超管: %s", raw)
		}
		status, _, _, raw = guest.Call(http.MethodGet, "/api/v1/admin/users", false, nil)
		testkit.ExpectStatus(t, status, http.StatusOK, raw)

		anonymous := srv.Client(t)
		status, _, publicPayload, raw := anonymous.Call(http.MethodGet, "/api/v1/users/boot_first", false, nil)
		testkit.ExpectStatus(t, status, http.StatusOK, raw)
		if publicUser, ok := publicPayload["user"].(map[string]any); ok {
			if _, exists := publicUser["is_super_admin"]; exists {
				t.Fatalf("公开资料不应返回 is_super_admin: %s", raw)
			}
		}

		// 第二个注册：普通用户，不能访问管理接口。
		second := srv.Client(t)
		status, _, payload, raw = second.Register("boot_second", "boot-second@example.com", bootstrapTestPassword)
		testkit.ExpectStatus(t, status, http.StatusCreated, raw)
		secondUser := testkit.MustUser(t, payload)
		if got := secondUser["role"]; got != model.RoleUser {
			t.Fatalf("第二个注册用户角色应为 user，实际 %v: %s", got, raw)
		}
		if _, ok := secondUser["is_super_admin"]; ok {
			t.Fatalf("第二个注册用户不应带 is_super_admin: %s", raw)
		}
		second.LoginAndSetCSRF("boot_second", bootstrapTestPassword)
		status, _, _, raw = second.Call(http.MethodGet, "/api/v1/admin/users", false, nil)
		testkit.ExpectStatus(t, status, http.StatusForbidden, raw)

		// 数据库唯一超管 + 计数辅助断言。
		var superAdmins, humanUsers int
		if err := srv.Pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM users WHERE is_super_admin`).Scan(&superAdmins); err != nil {
			t.Fatalf("统计超管失败: %v", err)
		}
		if err := srv.Pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM users WHERE NOT is_ai`).Scan(&humanUsers); err != nil {
			t.Fatalf("统计人类用户失败: %v", err)
		}
		if superAdmins != 1 || humanUsers != 2 {
			t.Fatalf("初始化统计不符: humanUsers=%d superAdmins=%d", humanUsers, superAdmins)
		}
		if role, isSuper := integrationRole(t, srv, firstID); role != model.RoleAdmin || !isSuper {
			t.Fatalf("首个用户数据库角色不符: role=%s is_super_admin=%v", role, isSuper)
		}
	})

	t.Run("空库忽略注册开关", func(t *testing.T) {
		srv := testkit.NewIsolatedServer(t, "bootstrap")
		ctx := context.Background()
		if _, err := srv.Pool.Exec(ctx, `
			INSERT INTO system_settings (key, value, updated_at)
			VALUES ('registration.enabled', 'false'::jsonb, $1)
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`,
			srv.Clock.Now()); err != nil {
			t.Fatalf("关闭测试注册开关失败: %v", err)
		}

		guest := srv.Client(t)
		status, _, configPayload, raw := guest.Call(http.MethodGet, "/api/v1/auth/config", false, nil)
		testkit.ExpectStatus(t, status, http.StatusOK, raw)
		if configPayload["registration_enabled"] != false {
			t.Fatalf("注册开关应已关闭: %s", raw)
		}
		if configPayload["bootstrap_required"] != true {
			t.Fatalf("空库时 bootstrap_required 应为 true: %s", raw)
		}

		// 空库例外：注册开关关闭也允许首个注册，并成为超管。
		status, payload, raw := registerBootstrapUser(t, srv, "boot_closed", "boot-closed@example.com")
		testkit.ExpectStatus(t, status, http.StatusCreated, raw)
		user := testkit.MustUser(t, payload)
		if got := user["role"]; got != model.RoleAdmin {
			t.Fatalf("注册关闭时首个用户角色应为 admin，实际 %v: %s", got, raw)
		}
		if isSuper, _ := user["is_super_admin"].(bool); !isSuper {
			t.Fatalf("注册关闭时首个用户应为超管: %s", raw)
		}

		// 已有人类用户后，注册开关恢复普通语义：第二个注册应 403。
		status, payload, raw = registerBootstrapUser(t, srv, "boot_closed_second", "boot-closed-second@example.com")
		testkit.ExpectStatus(t, status, http.StatusForbidden, raw)
		if code := testkit.ErrorCode(payload); code != "forbidden" {
			t.Fatalf("注册关闭时第二个注册应返回 forbidden，实际 %q: %s", code, raw)
		}
	})

	t.Run("并发首注册只产生一个超管", func(t *testing.T) {
		srv := testkit.NewIsolatedServer(t, "bootstrap")
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
				results <- registerBootstrapUserHTTP(srv.BaseURL, username, email)
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
			case result.role == model.RoleAdmin && result.isSuper:
				superAdminCount++
			case result.role == model.RoleUser && !result.isSuper:
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
		if err := srv.Pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM users WHERE is_super_admin`).Scan(&dbSuperAdmins); err != nil {
			t.Fatalf("统计超管失败: %v", err)
		}
		if err := srv.Pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM users WHERE NOT is_ai`).Scan(&humanUsers); err != nil {
			t.Fatalf("统计人类用户失败: %v", err)
		}
		if dbSuperAdmins != 1 || humanUsers != concurrentUsers {
			t.Fatalf("并发注册后数据不符: superAdmins=%d humanUsers=%d", dbSuperAdmins, humanUsers)
		}

		// 唯一超管可登录并访问管理接口，其余注册账号为普通用户。
		var superAdminUsername string
		if err := srv.Pool.QueryRow(ctx,
			`SELECT username FROM users WHERE is_super_admin`).Scan(&superAdminUsername); err != nil {
			t.Fatalf("读取超管用户名失败: %v", err)
		}
		winner := srv.Client(t)
		winner.LoginAndSetCSRF(superAdminUsername, bootstrapTestPassword)
		status, _, _, raw := winner.Call(http.MethodGet, "/api/v1/admin/users", false, nil)
		testkit.ExpectStatus(t, status, http.StatusOK, raw)
	})
}

// registerBootstrapUser 通过游客 CSRF + 注册接口创建账号，返回状态、JSON 响应与原始响应体。
func registerBootstrapUser(t *testing.T, srv *testkit.Server, username, email string) (int, map[string]any, string) {
	t.Helper()
	client := srv.Client(t)
	client.RefreshCSRF()
	status, _, payload, raw := client.Call(http.MethodPost, "/api/v1/auth/register", true, map[string]any{
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
