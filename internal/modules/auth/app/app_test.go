package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

func TestRegisterFirstUserBecomesSuperAdminEvenWhenRegistrationClosed(t *testing.T) {
	svc, users, _, _, _ := newTestService(t, false)

	created, err := svc.Register(context.Background(), RegisterInput{
		Username: "admin_user", Email: "admin@example.com", Password: "password123", IP: "203.0.113.1",
	})
	if err != nil {
		t.Fatalf("空库首注册应成功: %v", err)
	}
	if created.Role != model.RoleAdmin || !created.IsSuperAdmin {
		t.Fatalf("首注册应为超管: role=%q super=%v", created.Role, created.IsSuperAdmin)
	}
	if len(users.users) != 1 {
		t.Fatalf("用户数=%d", len(users.users))
	}
	stored := users.users[created.ID]
	if stored.PasswordHash == "" || stored.PasswordHash == "password123" {
		t.Fatal("必须保存 bcrypt 哈希而不是明文")
	}
	if stored.CreatedAt.IsZero() || stored.UpdatedAt.IsZero() {
		t.Fatal("created_at/updated_at 必须由应用时钟显式写入")
	}
}

func TestRegisterNormalUserAndRejectClosedRegistration(t *testing.T) {
	svc, users, _, _, _ := newTestService(t, true)
	mustCreateUser(t, users, model.User{ID: 1, Username: "existing", Email: "e@example.com"})

	created, err := svc.Register(context.Background(), RegisterInput{
		Username: "normal_user", Email: "normal@example.com", Password: "password123", IP: "203.0.113.2",
	})
	if err != nil {
		t.Fatalf("已初始化后开放注册应成功: %v", err)
	}
	if created.Role != model.RoleUser || created.IsSuperAdmin {
		t.Fatalf("普通注册不应是超管: %+v", created)
	}

	closedSvc, closedUsers, _, _, _ := newTestService(t, false)
	mustCreateUser(t, closedUsers, model.User{ID: 1, Username: "existing"})
	if _, err := closedSvc.Register(context.Background(), RegisterInput{
		Username: "other", Email: "o@example.com", Password: "password123", IP: "203.0.113.3",
	}); !apperr.IsKind(err, apperr.KindForbidden) {
		t.Fatalf("关闭注册应返回 Forbidden，实际: %v", err)
	}
}

func TestRegisterValidationErrors(t *testing.T) {
	svc, _, _, _, _ := newTestService(t, true)
	_, err := svc.Register(context.Background(), RegisterInput{
		Username: "ab", Email: "bad", Password: "short", IP: "203.0.113.4",
	})
	appErr, ok := apperr.AsKind(err)
	if !ok || appErr.Kind != apperr.KindValidation {
		t.Fatalf("应返回 Validation，实际: %v", err)
	}
	if appErr.Details == nil {
		t.Fatal("校验错误应带字段详情")
	}
}

func TestLoginSuccessAndFailures(t *testing.T) {
	svc, users, sessions, _, _ := newTestService(t, true)
	hash, _ := hashPasswordForTest("password123")
	mustCreateUser(t, users, model.User{
		ID: 1, Username: "alice", Email: "alice@example.com",
		PasswordHash: hash, TokenVersion: 1,
	})

	result, err := svc.Login(context.Background(), LoginInput{
		Identifier: "ALICE", Password: "password123", IP: "203.0.113.5",
	})
	if err != nil {
		t.Fatalf("登录应成功: %v", err)
	}
	if result.User.ID != 1 || result.SessionToken == "" || result.CSRFToken == "" {
		t.Fatalf("登录结果不完整: %+v", result)
	}
	if result.ExpiresAt.Sub(result.ExpiresAt.Add(-time.Hour)) != time.Hour {
		t.Fatalf("会话过期时间异常: %v", result.ExpiresAt)
	}
	if len(sessions.sessions) != 1 {
		t.Fatalf("应创建 1 个会话，实际 %d", len(sessions.sessions))
	}

	if _, err := svc.Login(context.Background(), LoginInput{
		Identifier: "alice", Password: "wrong-password", IP: "203.0.113.6",
	}); !apperr.IsKind(err, apperr.KindUnauthenticated) {
		t.Fatalf("错误密码应返回 Unauthenticated，实际: %v", err)
	}
	if _, err := svc.Login(context.Background(), LoginInput{
		Identifier: "nobody", Password: "password123", IP: "203.0.113.7",
	}); !apperr.IsKind(err, apperr.KindUnauthenticated) {
		t.Fatalf("不存在账号应返回 Unauthenticated，实际: %v", err)
	}

	suspended := mustCreateUser(t, users, model.User{
		ID: 2, Username: "bob", Email: "bob@example.com", PasswordHash: hash,
		Status: model.StatusSuspended,
	})
	suspendErr := func() error {
		_, e := svc.Login(context.Background(), LoginInput{
			Identifier: "bob", Password: "password123", IP: "203.0.113.8",
		})
		return e
	}()
	if !apperr.IsKind(suspendErr, apperr.KindSuspended) {
		t.Fatalf("停用账号登录应返回 Suspended，实际: %v（user=%+v）", suspendErr, suspended)
	}
}

func TestResolveSessionMatrix(t *testing.T) {
	svc, users, sessions, _, clk := newTestService(t, true)
	hash, _ := hashPasswordForTest("password123")
	user := mustCreateUser(t, users, model.User{
		ID: 1, Username: "alice", Email: "alice@example.com", PasswordHash: hash, TokenVersion: 1,
	})
	now := clk.Now()
	session := model.Session{
		ID: 10, UserID: user.ID, CSRFToken: "csrf", TokenVersion: 1,
		ExpiresAt: now.Add(time.Hour),
	}
	const rawToken = "session-token"
	if err := sessions.Insert(context.Background(), session, hashSessionTokenForTest(rawToken), now); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	result, err := svc.ResolveSession(context.Background(), rawToken)
	if err != nil || result.State != "valid" || !result.Viewer.Authenticated {
		t.Fatalf("有效会话解析失败: %+v err=%v", result, err)
	}
	if result.Session.CSRFToken != "csrf" {
		t.Fatalf("CSRF token=%q", result.Session.CSRFToken)
	}

	if result, err := svc.ResolveSession(context.Background(), ""); err != nil || result.State != "missing" {
		t.Fatalf("缺失会话: %+v err=%v", result, err)
	}
	if result, err := svc.ResolveSession(context.Background(), "unknown"); err != nil || result.State != "not_found" {
		t.Fatalf("未知会话: %+v err=%v", result, err)
	}

	// 过期
	clk.Advance(2 * time.Hour)
	if result, _ := svc.ResolveSession(context.Background(), rawToken); result.State != "expired" {
		t.Fatalf("过期会话 state=%v", result.State)
	}
}

func TestChangePassword(t *testing.T) {
	svc, users, sessions, _, clk := newTestService(t, true)
	hash, _ := hashPasswordForTest("old-password")
	user := mustCreateUser(t, users, model.User{
		ID: 1, Username: "alice", PasswordHash: hash, TokenVersion: 1,
	})
	now := clk.Now()
	current := model.Session{ID: 100, UserID: 1, TokenVersion: 1, ExpiresAt: now.Add(time.Hour)}
	other := model.Session{ID: 101, UserID: 1, TokenVersion: 1, ExpiresAt: now.Add(time.Hour)}
	_ = sessions.Insert(context.Background(), current, "hash-current", now)
	_ = sessions.Insert(context.Background(), other, "hash-other", now)

	if err := svc.ChangePassword(context.Background(), ChangePasswordInput{
		UserID: 1, SessionID: 100, OldPassword: "wrong", NewPassword: "new-password",
	}); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("错误旧密码应返回 Validation，实际: %v", err)
	}

	if err := svc.ChangePassword(context.Background(), ChangePasswordInput{
		UserID: 1, SessionID: 100, OldPassword: "old-password", NewPassword: "new-password",
	}); err != nil {
		t.Fatalf("修改密码失败: %v", err)
	}
	updated := users.users[user.ID]
	if updated.TokenVersion != 2 {
		t.Fatalf("token_version=%d want 2", updated.TokenVersion)
	}
	if !passwordMatchesForTest(updated.PasswordHash, "new-password") {
		t.Fatal("新密码未生效")
	}
	if sessions.sessions["hash-other"].session.RevokedAt == nil {
		t.Fatal("其他会话必须被吊销")
	}
	if sessions.sessions["hash-current"].session.RevokedAt != nil {
		t.Fatal("当前会话必须保留")
	}
	if sessions.sessions["hash-current"].session.TokenVersion != 2 {
		t.Fatal("当前会话 token_version 必须同步")
	}
}

func TestSetAdminRoleGrantRevokeAndProtection(t *testing.T) {
	svc, users, _, audit, _ := newTestService(t, true)
	super := mustCreateUser(t, users, model.User{
		ID: 1, Username: "root", Role: model.RoleAdmin, IsSuperAdmin: true,
	})
	target := mustCreateUser(t, users, model.User{ID: 2, Username: "bob"})
	plainAdmin := mustCreateUser(t, users, model.User{ID: 3, Username: "mod", Role: model.RoleAdmin})

	updated, err := svc.SetAdminRole(context.Background(), super.ID, target.ID, true)
	if err != nil {
		t.Fatalf("超管授权失败: %v", err)
	}
	if updated.Role != model.RoleAdmin {
		t.Fatalf("目标角色=%q", updated.Role)
	}
	if len(audit.entries) != 1 || audit.entries[0].Action != contracts.AuditActionUserGrantAdmin {
		t.Fatalf("授权审计不符: %+v", audit.entries)
	}

	// 幂等：再次授权不写审计
	if _, err := svc.SetAdminRole(context.Background(), super.ID, target.ID, true); err != nil {
		t.Fatalf("重复授权应幂等: %v", err)
	}
	if len(audit.entries) != 1 {
		t.Fatalf("幂等授权不应新增审计: %d", len(audit.entries))
	}

	// 撤销
	if _, err := svc.SetAdminRole(context.Background(), super.ID, target.ID, false); err != nil {
		t.Fatalf("超管撤销失败: %v", err)
	}
	if users.users[target.ID].Role != model.RoleUser {
		t.Fatal("撤销后应回到普通用户")
	}

	// 普通管理员不能授权
	if _, err := svc.SetAdminRole(context.Background(), plainAdmin.ID, target.ID, true); !apperr.IsKind(err, apperr.KindForbidden) {
		t.Fatalf("普通管理员授权应 Forbidden，实际: %v", err)
	}
	// 不能修改自己
	if _, err := svc.SetAdminRole(context.Background(), super.ID, super.ID, false); err == nil {
		t.Fatal("不能修改自己的管理员权限")
	}
	// 不能修改超管
	otherSuper := mustCreateUser(t, users, model.User{
		ID: 4, Username: "root2", Role: model.RoleAdmin, IsSuperAdmin: true,
	})
	if _, err := svc.SetAdminRole(context.Background(), super.ID, otherSuper.ID, false); err == nil {
		t.Fatal("不能修改超级管理员的管理员权限")
	}
	// 不能授权 AI
	ai := mustCreateUser(t, users, model.User{ID: 5, Username: "ai_bot", IsAI: true})
	if _, err := svc.SetAdminRole(context.Background(), super.ID, ai.ID, true); err == nil {
		t.Fatal("AI 账号不能成为管理员")
	}
	if got := users.users[1]; got.Username != "root" || !got.IsSuperAdmin {
		t.Fatalf("操作者不应被修改: %+v", got)
	}
}

func TestSetUserStatusSuspendRevokesSessions(t *testing.T) {
	svc, users, sessions, audit, clk := newTestService(t, true)
	admin := mustCreateUser(t, users, model.User{
		ID: 1, Username: "mod", Role: model.RoleAdmin,
	})
	target := mustCreateUser(t, users, model.User{ID: 2, Username: "bob", TokenVersion: 1})
	now := clk.Now()
	_ = sessions.Insert(context.Background(), model.Session{
		ID: 5, UserID: target.ID, TokenVersion: 1, ExpiresAt: now.Add(time.Hour),
	}, "hash", now)

	updated, err := svc.SetUserStatus(context.Background(), contracts.UserStatusInput{
		ActorID: admin.ID, TargetID: target.ID, Suspend: true, At: now,
	})
	if err != nil {
		t.Fatalf("停用失败: %v", err)
	}
	if updated.Status != model.StatusSuspended || updated.SuspendedAt == nil {
		t.Fatalf("停用后状态不符: %+v", updated)
	}
	if updated.TokenVersion != 2 {
		t.Fatalf("停用应递增 token_version，实际 %d", updated.TokenVersion)
	}
	if sessions.sessions["hash"].session.RevokedAt == nil {
		t.Fatal("停用必须吊销会话")
	}
	if len(audit.entries) != 1 || audit.entries[0].Action != contracts.AuditActionUserSuspend {
		t.Fatalf("停用审计不符: %+v", audit.entries)
	}

	// 不能停用自己
	if _, err := svc.SetUserStatus(context.Background(), contracts.UserStatusInput{
		ActorID: admin.ID, TargetID: admin.ID, Suspend: true, At: now,
	}); err == nil {
		t.Fatal("不能停用自己")
	}
}

func TestResetPasswordAsAdmin(t *testing.T) {
	svc, users, sessions, audit, clk := newTestService(t, true)
	admin := mustCreateUser(t, users, model.User{ID: 1, Username: "mod", Role: model.RoleAdmin})
	target := mustCreateUser(t, users, model.User{ID: 2, Username: "bob", TokenVersion: 1})
	oldHash, _ := hashPasswordForTest("old-password")
	target.PasswordHash = oldHash
	users.users[target.ID] = target
	now := clk.Now()
	_ = sessions.Insert(context.Background(), model.Session{
		ID: 6, UserID: target.ID, TokenVersion: 1, ExpiresAt: now.Add(time.Hour),
	}, "hash2", now)

	if err := svc.ResetPassword(context.Background(), contracts.ResetPasswordInput{
		ActorID: admin.ID, TargetID: target.ID, NewPassword: "reset-password", At: now,
	}); err != nil {
		t.Fatalf("重置密码失败: %v", err)
	}
	updated := users.users[target.ID]
	if !passwordMatchesForTest(updated.PasswordHash, "reset-password") {
		t.Fatal("新密码未生效")
	}
	if updated.TokenVersion != 2 {
		t.Fatalf("token_version=%d want 2", updated.TokenVersion)
	}
	if sessions.sessions["hash2"].session.RevokedAt == nil {
		t.Fatal("重置密码必须吊销会话")
	}
	if len(audit.entries) != 1 || audit.entries[0].Action != contracts.AuditActionUserResetPwd {
		t.Fatalf("重置密码审计不符: %+v", audit.entries)
	}

	ai := mustCreateUser(t, users, model.User{ID: 3, Username: "ai", IsAI: true})
	if err := svc.ResetPassword(context.Background(), contracts.ResetPasswordInput{
		ActorID: admin.ID, TargetID: ai.ID, NewPassword: "reset-password", At: now,
	}); err == nil {
		t.Fatal("AI 账号不能重置密码")
	}
}

func TestConfigAndBootstrap(t *testing.T) {
	svc, users, _, _, _ := newTestService(t, true)
	config, err := svc.Config(context.Background())
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if !config.RegistrationEnabled || !config.BootstrapRequired {
		t.Fatalf("空库配置不符: %+v", config)
	}

	mustCreateUser(t, users, model.User{ID: 1, Username: "alice"})
	required, err := svc.BootstrapRequired(context.Background())
	if err != nil || required {
		t.Fatalf("已有人类用户后 bootstrap=%v err=%v", required, err)
	}
	status, err := svc.BootstrapStatus(context.Background())
	if err != nil || status.HumanUsers != 1 {
		t.Fatalf("BootstrapStatus=%+v err=%v", status, err)
	}
}

// TestResolveSessionSuspendedAfterAdminSuspend 复现基线语义：管理员停用账号会吊销
// 全部会话，但会话解析必须优先报告「账号停用」，否则写请求会从 403 account_suspended
// 退化为 401 unauthenticated（集成 TestAccountIntegration 发现的问题）。
func TestResolveSessionSuspendedAfterAdminSuspend(t *testing.T) {
	svc, users, sessions, _, clk := newTestService(t, true)
	hash, _ := hashPasswordForTest("password123")
	admin := mustCreateUser(t, users, model.User{
		ID: 1, Username: "mod", Role: model.RoleAdmin, PasswordHash: hash, TokenVersion: 1,
	})
	target := mustCreateUser(t, users, model.User{
		ID: 2, Username: "bob", PasswordHash: hash, TokenVersion: 1,
	})
	now := clk.Now()
	const rawToken = "target-session-token"
	_ = sessions.Insert(context.Background(), model.Session{
		ID: 20, UserID: target.ID, CSRFToken: "csrf", TokenVersion: 1, ExpiresAt: now.Add(time.Hour),
	}, hashSessionTokenForTest(rawToken), now)

	// 停用前：会话有效。
	if result, err := svc.ResolveSession(context.Background(), rawToken); err != nil || result.State != "valid" {
		t.Fatalf("停用前会话应有效: state=%v err=%v", result.State, err)
	}

	if _, err := svc.SetUserStatus(context.Background(), contracts.UserStatusInput{
		ActorID: admin.ID, TargetID: target.ID, Suspend: true, At: now,
	}); err != nil {
		t.Fatalf("停用账号失败: %v", err)
	}
	if sessions.sessions[hashSessionTokenForTest(rawToken)].session.RevokedAt == nil {
		t.Fatal("停用必须吊销会话（基线行为）")
	}

	result, err := svc.ResolveSession(context.Background(), rawToken)
	if err != nil {
		t.Fatalf("ResolveSession: %v", err)
	}
	if result.State != "suspended" {
		t.Fatalf("停用账号的旧会话必须报告 suspended（停用优先），实际 %v", result.State)
	}
	if result.Viewer.Authenticated {
		t.Fatal("停用账号不得得到已认证 Viewer")
	}
}

func TestSearchUsers(t *testing.T) {
	svc, users, _, _, _ := newTestService(t, true)
	mustCreateUser(t, users, model.User{ID: 1, Username: "alice", Nickname: "爱丽丝"})
	mustCreateUser(t, users, model.User{ID: 2, Username: "bob", Nickname: "鲍勃"})

	result, err := svc.SearchUsers(context.Background(), "ali", 0, 20)
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if len(result.Users) != 1 || result.Users[0].Username != "alice" {
		t.Fatalf("搜索结果不符: %+v", result.Users)
	}
	if _, err := svc.SearchUsers(context.Background(), strings.Repeat("x", 101), 0, 20); !apperr.IsKind(err, apperr.KindInvalid) {
		t.Fatalf("超长关键词应 Invalid，实际: %v", err)
	}
}
