package server

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/auth"
	"github.com/great-magician-01/timeline/internal/id"
)

// TestAdminGrantIntegration 覆盖超级管理员授权/撤销管理员与超管保护：
//   - 已有用户时注册的新账号仍是普通用户，不会自动成为超级管理员；
//   - 超管授权后目标立即获得管理接口权限（无需重新登录、不吊销会话），撤销后立即失去；
//   - 普通管理员无权授权/撤销管理员，也不能 suspend/unsuspend/reset-password 超管账号；
//   - AI 账号、本人（单超管约束下“目标超管”只能是本人）不能作为授权/撤销目标；
//   - 授权/撤销幂等且只在真实变更时写审计；审计查询接口可按动作筛选到记录；
//   - 已停用账号不能授权为管理员，但撤销管理员不限制目标状态。
//
// 复用 integration_setup_test.go 的共享测试 schema 与固定账号：
// admin_integration 已被 ensureIntegrationAdmin 提升为唯一超管，mod_integration 是普通管理员。
func TestAdminGrantIntegration(t *testing.T) {
	if os.Getenv("TIMELINE_INTEGRATION") != "1" {
		t.Skip("设置 TIMELINE_INTEGRATION=1 后运行测试 schema 集成测试")
	}
	pool, _, base := setupIntegrationServer(t)
	ctx := context.Background()

	// --- 前置：登录超管与普通管理员，并确认 helper 赋予的角色正确 ---
	superAdmin := newIntegrationClient(t, base)
	loginIntegrationUser(t, superAdmin, integrationAdminUsername, integrationAdminPassword)
	status, _, payload, raw := superAdmin.call(http.MethodGet, "/api/v1/auth/me", false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	superUser := integrationUser(payload, t)
	if got := superUser["role"]; got != auth.RoleAdmin {
		t.Fatalf("测试超管角色应为 admin，实际 %v: %s", got, raw)
	}
	if isSuper, _ := superUser["is_super_admin"].(bool); !isSuper {
		t.Fatalf("测试账号 %s 必须是超管（检查 ensureIntegrationAdmin 是否成功提升）: %s",
			integrationAdminUsername, raw)
	}
	superAdminID := integrationString(superUser, t, "id")

	modAdmin := newIntegrationClient(t, base)
	loginIntegrationUser(t, modAdmin, integrationModUsername, integrationAdminPassword)
	status, _, payload, raw = modAdmin.call(http.MethodGet, "/api/v1/auth/me", false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	modUser := integrationUser(payload, t)
	if got := modUser["role"]; got != auth.RoleAdmin {
		t.Fatalf("普通测试管理员角色应为 admin，实际 %v: %s", got, raw)
	}
	if isSuper, _ := modUser["is_super_admin"].(bool); isSuper {
		t.Fatalf("普通测试管理员 %s 不应带超管标记: %s", integrationModUsername, raw)
	}

	// --- 目标普通用户：共享 schema 已有人类用户，注册必须仍是普通用户 ---
	// 用户名有 32 位上限，使用较短后缀；邮箱保留完整后缀以隔离历史数据。
	suffix := integrationShortSuffix()
	target := newIntegrationClient(t, base)
	targetName := "grant_target_" + suffix
	targetEmail := "grant-target-" + suffix + "@example.com"
	target.refreshCSRF()
	status, _, payload, raw = target.call(http.MethodPost, "/api/v1/auth/register", true, map[string]any{
		"username": targetName,
		"email":    targetEmail,
		"password": "user-password-123",
	})
	expectIntegrationStatus(t, status, http.StatusCreated, raw)
	targetUser := integrationUser(payload, t)
	if got := targetUser["role"]; got != auth.RoleUser {
		t.Fatalf("已有用户时注册的新账号角色应为 user，实际 %v: %s", got, raw)
	}
	if _, ok := targetUser["is_super_admin"]; ok {
		t.Fatalf("普通注册账号响应不应包含 is_super_admin: %s", raw)
	}
	targetID := integrationString(targetUser, t, "id")
	loginIntegrationUser(t, target, targetName, "user-password-123")

	// 基线：普通用户不能访问管理接口。
	status, _, _, raw = target.call(http.MethodGet, "/api/v1/admin/users", false, nil)
	expectIntegrationStatus(t, status, http.StatusForbidden, raw)

	// --- 超管授权：目标立即获得后台权限（同一会话，无需重新登录） ---
	status, _, payload, raw = superAdmin.call(http.MethodPost,
		"/api/v1/admin/users/"+targetID+"/grant-admin", true, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	grantedUser := integrationUser(payload, t)
	if got := grantedUser["role"]; got != auth.RoleAdmin {
		t.Fatalf("授权后响应角色应为 admin，实际 %v: %s", got, raw)
	}
	if _, ok := grantedUser["is_super_admin"]; ok {
		t.Fatalf("被授权的管理员不应带超管标记: %s", raw)
	}
	if role, isSuper := integrationRole(t, pool, targetID); role != auth.RoleAdmin || isSuper {
		t.Fatalf("授权后数据库角色不符: role=%s is_super_admin=%v", role, isSuper)
	}
	status, _, _, raw = target.call(http.MethodGet, "/api/v1/admin/users", false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)

	// 重新登录后仍具备管理员权限（用新客户端模拟重新登录；已登录会话的 CSRF 令牌
	// 属于会话令牌，而登录接口使用游客双提交 Cookie，混用会得到 csrf_failed）。
	targetRelogin := newIntegrationClient(t, base)
	loginIntegrationUser(t, targetRelogin, targetName, "user-password-123")
	status, _, payload, raw = targetRelogin.call(http.MethodGet, "/api/v1/auth/me", false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if got := integrationUser(payload, t)["role"]; got != auth.RoleAdmin {
		t.Fatalf("重新登录后角色应为 admin，实际 %v: %s", got, raw)
	}
	status, _, _, raw = targetRelogin.call(http.MethodGet, "/api/v1/admin/users", false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)

	// --- 普通管理员越权：授权/撤销都被拒，且不改变目标状态 ---
	status, _, payload, raw = modAdmin.call(http.MethodPost,
		"/api/v1/admin/users/"+targetID+"/grant-admin", true, nil)
	expectIntegrationStatus(t, status, http.StatusForbidden, raw)
	if code := integrationErrorCode(payload); code != "forbidden" {
		t.Fatalf("普通管理员授权应返回 forbidden，实际 %q: %s", code, raw)
	}
	status, _, payload, raw = modAdmin.call(http.MethodPost,
		"/api/v1/admin/users/"+targetID+"/revoke-admin", true, nil)
	expectIntegrationStatus(t, status, http.StatusForbidden, raw)
	if code := integrationErrorCode(payload); code != "forbidden" {
		t.Fatalf("普通管理员撤销应返回 forbidden，实际 %q: %s", code, raw)
	}
	if role, _ := integrationRole(t, pool, targetID); role != auth.RoleAdmin {
		t.Fatalf("越权请求不应改变目标角色，实际 %s", role)
	}

	// --- 幂等授权：第二次授权返回 200 且不重复写审计 ---
	status, _, payload, raw = superAdmin.call(http.MethodPost,
		"/api/v1/admin/users/"+targetID+"/grant-admin", true, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if got := integrationUser(payload, t)["role"]; got != auth.RoleAdmin {
		t.Fatalf("幂等授权响应角色应为 admin，实际 %v: %s", got, raw)
	}
	if got := integrationAuditCount(t, pool, "user_grant_admin", targetID); got != 1 {
		t.Fatalf("幂等授权不应重复写审计，user_grant_admin 记录数 = %d", got)
	}

	// --- 超管撤销：目标立即失去后台权限，但仍保留有效会话（后续请求返回 403） ---
	status, _, payload, raw = superAdmin.call(http.MethodPost,
		"/api/v1/admin/users/"+targetID+"/revoke-admin", true, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if got := integrationUser(payload, t)["role"]; got != auth.RoleUser {
		t.Fatalf("撤销后响应角色应为 user，实际 %v: %s", got, raw)
	}
	if role, isSuper := integrationRole(t, pool, targetID); role != auth.RoleUser || isSuper {
		t.Fatalf("撤销后数据库角色不符: role=%s is_super_admin=%v", role, isSuper)
	}
	status, _, _, raw = target.call(http.MethodGet, "/api/v1/admin/users", false, nil)
	expectIntegrationStatus(t, status, http.StatusForbidden, raw)
	if got := integrationAuditCount(t, pool, "user_revoke_admin", targetID); got != 1 {
		t.Fatalf("撤销管理员应写入一条审计，user_revoke_admin 记录数 = %d", got)
	}

	// 幂等撤销：不重复写审计。
	status, _, payload, raw = superAdmin.call(http.MethodPost,
		"/api/v1/admin/users/"+targetID+"/revoke-admin", true, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if got := integrationUser(payload, t)["role"]; got != auth.RoleUser {
		t.Fatalf("幂等撤销响应用户应仍是 user，实际 %v: %s", got, raw)
	}
	if got := integrationAuditCount(t, pool, "user_revoke_admin", targetID); got != 1 {
		t.Fatalf("幂等撤销不应重复写审计，user_revoke_admin 记录数 = %d", got)
	}

	// 普通管理员对普通用户再次越权，审计数量不变。
	status, _, _, raw = modAdmin.call(http.MethodPost,
		"/api/v1/admin/users/"+targetID+"/grant-admin", true, nil)
	expectIntegrationStatus(t, status, http.StatusForbidden, raw)
	if got := integrationAuditCount(t, pool, "user_grant_admin", targetID); got != 1 {
		t.Fatalf("越权请求不应写入 user_grant_admin 审计，实际 %d 条", got)
	}

	// --- 审计查询接口：按动作筛选能查到本次授权/撤销记录 ---
	status, _, audits, raw := superAdmin.call(http.MethodGet,
		"/api/v1/admin/audit-logs?action_type=user_grant_admin&limit=50", false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if !integrationListContains(audits, "target_id", targetID) {
		t.Fatalf("审计日志缺少 user_grant_admin 目标 %s: %s", targetID, raw)
	}
	status, _, audits, raw = superAdmin.call(http.MethodGet,
		"/api/v1/admin/audit-logs?action_type=user_revoke_admin&limit=50", false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if !integrationListContains(audits, "target_id", targetID) {
		t.Fatalf("审计日志缺少 user_revoke_admin 目标 %s: %s", targetID, raw)
	}

	// --- 目标校验：本人/超管（单超管约束下只能与本人重合） ---
	status, _, payload, raw = superAdmin.call(http.MethodPost,
		"/api/v1/admin/users/"+superAdminID+"/grant-admin", true, nil)
	expectIntegrationStatus(t, status, http.StatusBadRequest, raw)
	if code := integrationErrorCode(payload); code != "validation_failed" {
		t.Fatalf("超管修改自己权限应返回 validation_failed，实际 %q: %s", code, raw)
	}
	status, _, payload, raw = superAdmin.call(http.MethodPost,
		"/api/v1/admin/users/"+superAdminID+"/revoke-admin", true, nil)
	expectIntegrationStatus(t, status, http.StatusBadRequest, raw)
	if code := integrationErrorCode(payload); code != "validation_failed" {
		t.Fatalf("超管撤销自己权限应返回 validation_failed，实际 %q: %s", code, raw)
	}

	// --- 目标校验：AI 账号不能成为管理员 ---
	aiIDs, err := id.NewGenerator(11)
	if err != nil {
		t.Fatal(err)
	}
	aiID, err := aiIDs.Next()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, username, nickname, role, is_ai, status, token_version)
		VALUES ($1, $2, $3, 'user', TRUE, 'active', 1)`,
		aiID, "grant_ai_"+suffix, "授权测试 AI"); err != nil {
		t.Fatalf("准备 AI 账号失败: %v", err)
	}
	aiTargetID := strconv.FormatInt(aiID, 10)
	status, _, payload, raw = superAdmin.call(http.MethodPost,
		"/api/v1/admin/users/"+aiTargetID+"/grant-admin", true, nil)
	expectIntegrationStatus(t, status, http.StatusBadRequest, raw)
	if code := integrationErrorCode(payload); code != "validation_failed" {
		t.Fatalf("授权 AI 为管理员应返回 validation_failed，实际 %q: %s", code, raw)
	}

	// --- 目标校验：已停用账号不能授权；撤销管理员不限制目标状态 ---
	disabled := newIntegrationClient(t, base)
	disabledName := "grant_disabled_" + suffix
	disabled.refreshCSRF()
	status, _, payload, raw = disabled.call(http.MethodPost, "/api/v1/auth/register", true, map[string]any{
		"username": disabledName,
		"email":    "grant-disabled-" + suffix + "@example.com",
		"password": "user-password-123",
	})
	expectIntegrationStatus(t, status, http.StatusCreated, raw)
	disabledID := integrationString(integrationUser(payload, t), t, "id")

	status, _, _, raw = superAdmin.call(http.MethodPost,
		"/api/v1/admin/users/"+disabledID+"/suspend", true, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	status, _, payload, raw = superAdmin.call(http.MethodPost,
		"/api/v1/admin/users/"+disabledID+"/grant-admin", true, nil)
	expectIntegrationStatus(t, status, http.StatusBadRequest, raw)
	if code := integrationErrorCode(payload); code != "validation_failed" {
		t.Fatalf("停用账号授权应返回 validation_failed，实际 %q: %s", code, raw)
	}

	// 恢复后授权成功，再停用；撤销管理员不限制状态，应成功。
	status, _, _, raw = superAdmin.call(http.MethodPost,
		"/api/v1/admin/users/"+disabledID+"/unsuspend", true, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	status, _, _, raw = superAdmin.call(http.MethodPost,
		"/api/v1/admin/users/"+disabledID+"/grant-admin", true, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	status, _, _, raw = superAdmin.call(http.MethodPost,
		"/api/v1/admin/users/"+disabledID+"/suspend", true, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	status, _, payload, raw = superAdmin.call(http.MethodPost,
		"/api/v1/admin/users/"+disabledID+"/revoke-admin", true, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if got := integrationUser(payload, t)["role"]; got != auth.RoleUser {
		t.Fatalf("停用账号撤销管理员后角色应为 user，实际 %v: %s", got, raw)
	}

	// --- 超管保护：普通管理员不能 suspend/unsuspend/reset-password 超管账号 ---
	status, _, payload, raw = modAdmin.call(http.MethodPost,
		"/api/v1/admin/users/"+superAdminID+"/suspend", true, nil)
	expectIntegrationStatus(t, status, http.StatusForbidden, raw)
	if code := integrationErrorCode(payload); code != "forbidden" {
		t.Fatalf("普通管理员停用超管应返回 forbidden，实际 %q: %s", code, raw)
	}
	status, _, _, raw = modAdmin.call(http.MethodPost,
		"/api/v1/admin/users/"+superAdminID+"/unsuspend", true, nil)
	expectIntegrationStatus(t, status, http.StatusForbidden, raw)
	status, _, _, raw = modAdmin.call(http.MethodPost,
		"/api/v1/admin/users/"+superAdminID+"/reset-password", true, map[string]any{
			"new_password": "should-not-apply-123",
		})
	expectIntegrationStatus(t, status, http.StatusForbidden, raw)

	// 超管账号仍可正常登录（未被停用/改密），说明保护请求没有产生副作用。
	stillSuper := newIntegrationClient(t, base)
	loginIntegrationUser(t, stillSuper, integrationAdminUsername, integrationAdminPassword)
}

// integrationRole 从数据库读取账号角色与超管标记。
func integrationRole(t *testing.T, pool *pgxpool.Pool, rawID string) (role string, isSuperAdmin bool) {
	t.Helper()
	idValue, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil {
		t.Fatalf("解析测试用户 ID %q 失败: %v", rawID, err)
	}
	if err := pool.QueryRow(context.Background(),
		`SELECT role, is_super_admin FROM users WHERE id = $1`, idValue).Scan(&role, &isSuperAdmin); err != nil {
		t.Fatalf("读取测试用户 %s 的角色失败: %v", rawID, err)
	}
	return role, isSuperAdmin
}

// integrationAuditCount 统计指定动作针对目标账号的审计记录数（测试 schema 持久，需按目标过滤）。
func integrationAuditCount(t *testing.T, pool *pgxpool.Pool, actionType, rawTargetID string) int64 {
	t.Helper()
	targetValue, err := strconv.ParseInt(rawTargetID, 10, 64)
	if err != nil {
		t.Fatalf("解析审计目标 ID %q 失败: %v", rawTargetID, err)
	}
	var count int64
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM admin_audit_logs
		WHERE action_type = $1 AND target_id = $2`, actionType, targetValue).Scan(&count); err != nil {
		t.Fatalf("统计审计记录失败: %v", err)
	}
	return count
}
