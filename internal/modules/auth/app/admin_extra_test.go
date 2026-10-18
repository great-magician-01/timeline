package app

// 本文补充 auth/app 管理员角色与账号处置用例：ChangeAdminRole 契约入口、
// 目标/操作者复核失败、停用/启用（unsuspend）与重置密码的错误分支与审计。
// 已有 app_test.go 已覆盖超管授权/撤销主路径、自我/超管/AI 保护与停用主路径，
// 此处聚焦幂等、方向性校验、仓储/审计故障等未覆盖分支。

import (
	"context"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

func auditDetailBool(t *testing.T, entry contracts.AuditEntry, key string) bool {
	t.Helper()
	detail, ok := entry.Detail.(map[string]any)
	if !ok {
		t.Fatalf("审计详情类型不符: %#v", entry.Detail)
	}
	value, ok := detail[key].(bool)
	if !ok {
		t.Fatalf("审计详情缺少布尔字段 %q: %#v", key, detail)
	}
	return value
}

func TestChangeAdminRoleContractEntry(t *testing.T) {
	ctx := context.Background()
	svc, users, _, audit, _ := newTestService(t, true)
	super := mustCreateUser(t, users, model.User{ID: 1, Username: "root", Role: model.RoleAdmin, IsSuperAdmin: true})
	target := mustCreateUser(t, users, model.User{ID: 2, Username: "bob"})
	at := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

	updated, err := svc.ChangeAdminRole(ctx, contracts.AdminRoleInput{ActorID: super.ID, TargetID: target.ID, Grant: true, At: at})
	if err != nil {
		t.Fatalf("ChangeAdminRole 授权失败: %v", err)
	}
	if updated.Role != model.RoleAdmin {
		t.Fatalf("授权后角色 = %q", updated.Role)
	}
	if len(audit.entries) != 1 {
		t.Fatalf("授权应写一条审计: %+v", audit.entries)
	}
	if audit.entries[0].At != at {
		t.Fatalf("审计时间 = %v，期望 %v", audit.entries[0].At, at)
	}

	updated, err = svc.ChangeAdminRole(ctx, contracts.AdminRoleInput{ActorID: super.ID, TargetID: target.ID, Grant: false, At: at})
	if err != nil || updated.Role != model.RoleUser {
		t.Fatalf("ChangeAdminRole 撤销失败: %+v err=%v", updated, err)
	}
	if len(audit.entries) != 2 || audit.entries[1].Action != contracts.AuditActionUserRevokeAdmin {
		t.Fatalf("撤销审计不符: %+v", audit.entries)
	}
}

func TestChangeAdminRoleEdgeCases(t *testing.T) {
	ctx := context.Background()
	svc, users, _, audit, _ := newTestService(t, true)
	super := mustCreateUser(t, users, model.User{ID: 1, Username: "root", Role: model.RoleAdmin, IsSuperAdmin: true})
	other := mustCreateUser(t, users, model.User{ID: 2, Username: "bob"})
	aiActor := mustCreateUser(t, users, model.User{ID: 4, Username: "ai_admin", Role: model.RoleAdmin, IsAI: true})
	otherSuper := mustCreateUser(t, users, model.User{ID: 5, Username: "root2", Role: model.RoleAdmin, IsSuperAdmin: true})

	t.Run("无效资源 ID", func(t *testing.T) {
		for _, in := range []struct{ actor, target int64 }{{0, other.ID}, {super.ID, 0}, {-1, other.ID}} {
			if _, err := svc.ChangeAdminRole(ctx, contracts.AdminRoleInput{ActorID: in.actor, TargetID: in.target, Grant: true}); !apperr.IsKind(err, apperr.KindInvalid) {
				t.Fatalf("actor=%d target=%d 应返回 Invalid，实际: %v", in.actor, in.target, err)
			}
		}
	})

	t.Run("事务管理器未装配", func(t *testing.T) {
		noTx := &Service{users: users, sessions: newFakeSessionRepo(users)}
		if _, err := noTx.ChangeAdminRole(ctx, contracts.AdminRoleInput{ActorID: super.ID, TargetID: other.ID, Grant: true}); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("缺少事务管理器应返回 Internal，实际: %v", err)
		}
	})

	t.Run("目标账号不存在", func(t *testing.T) {
		if _, err := svc.SetAdminRole(ctx, super.ID, 999, true); !apperr.IsKind(err, apperr.KindNotFound) {
			t.Fatalf("目标不存在应返回 NotFound，实际: %v", err)
		}
	})

	t.Run("操作者不存在返回 Forbidden", func(t *testing.T) {
		if _, err := svc.SetAdminRole(ctx, 999, other.ID, true); !apperr.IsKind(err, apperr.KindForbidden) {
			t.Fatalf("操作者不存在应返回 Forbidden，实际: %v", err)
		}
	})

	t.Run("操作者仓储故障返回 Internal", func(t *testing.T) {
		svc.users = &stubUserRepo{fakeUserRepo: users, byIDErr: errInjected}
		t.Cleanup(func() { svc.users = users })
		if _, err := svc.SetAdminRole(ctx, super.ID, other.ID, true); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("操作者仓储故障应返回 Internal，实际: %v", err)
		}
	})

	t.Run("AI 管理员不能授权", func(t *testing.T) {
		if _, err := svc.SetAdminRole(ctx, aiActor.ID, other.ID, true); !apperr.IsKind(err, apperr.KindForbidden) {
			t.Fatalf("AI 管理员授权应返回 Forbidden，实际: %v", err)
		}
	})

	t.Run("不能授权 AI 账号", func(t *testing.T) {
		aiTarget := mustCreateUser(t, users, model.User{ID: 6, Username: "ai_target", IsAI: true})
		if _, err := svc.SetAdminRole(ctx, super.ID, aiTarget.ID, true); !apperr.IsKind(err, apperr.KindValidation) {
			t.Fatalf("授权 AI 账号应返回 Validation，实际: %v", err)
		}
	})

	t.Run("不能修改其他超级管理员", func(t *testing.T) {
		if _, err := svc.SetAdminRole(ctx, super.ID, otherSuper.ID, false); !apperr.IsKind(err, apperr.KindValidation) {
			t.Fatalf("修改其他超管应返回 Validation，实际: %v", err)
		}
	})

	t.Run("撤销方向幂等且不写审计", func(t *testing.T) {
		before := len(audit.entries)
		updated, err := svc.SetAdminRole(ctx, super.ID, other.ID, false)
		if err != nil {
			t.Fatalf("对普通用户重复撤销应幂等成功: %v", err)
		}
		if updated.Role != model.RoleUser || len(audit.entries) != before {
			t.Fatalf("幂等撤销不应改变角色或写审计: role=%q entries=%d", updated.Role, len(audit.entries))
		}
	})

	t.Run("更新角色仓储故障返回 Internal", func(t *testing.T) {
		svc.users = &stubUserRepo{fakeUserRepo: users, updateRoleErr: errInjected}
		t.Cleanup(func() { svc.users = users })
		if _, err := svc.SetAdminRole(ctx, super.ID, other.ID, true); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("更新角色失败应返回 Internal，实际: %v", err)
		}
	})

	t.Run("审计写入失败返回 Internal", func(t *testing.T) {
		svc.users = users
		svc.audit = failingAudit{}
		t.Cleanup(func() { svc.audit = audit })
		if _, err := svc.SetAdminRole(ctx, super.ID, other.ID, true); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("审计失败应返回 Internal，实际: %v", err)
		}
	})

	t.Run("未注入审计时角色变更仍成功", func(t *testing.T) {
		svc.audit = nil
		t.Cleanup(func() { svc.audit = audit })
		if _, err := svc.SetAdminRole(ctx, super.ID, other.ID, true); err != nil {
			t.Fatalf("无审计端口时角色变更应成功: %v", err)
		}
	})
}

func TestSetUserStatusEdgeCases(t *testing.T) {
	ctx := context.Background()
	svc, users, sessions, audit, clk := newTestService(t, true)
	admin := mustCreateUser(t, users, model.User{ID: 1, Username: "mod", Role: model.RoleAdmin})
	super := mustCreateUser(t, users, model.User{ID: 2, Username: "root", Role: model.RoleAdmin, IsSuperAdmin: true})
	now := clk.Now()

	t.Run("启用账号不增减 token_version 且写 unsuspend 审计", func(t *testing.T) {
		target := mustCreateUser(t, users, model.User{
			ID: 3, Username: "bob", Status: model.StatusSuspended, TokenVersion: 4,
		})
		suspendedAt := now.Add(-time.Hour)
		users.users[target.ID] = model.User{
			ID: target.ID, Username: target.Username, Status: model.StatusSuspended,
			TokenVersion: 4, SuspendedAt: &suspendedAt,
		}
		updated, err := svc.SetUserStatus(ctx, contracts.UserStatusInput{
			ActorID: admin.ID, TargetID: target.ID, Suspend: false, At: now,
		})
		if err != nil {
			t.Fatalf("启用账号失败: %v", err)
		}
		if updated.Status != model.StatusActive || updated.SuspendedAt != nil {
			t.Fatalf("启用后状态不符: %+v", updated)
		}
		if updated.TokenVersion != 4 {
			t.Fatalf("启用不应改变 token_version，实际 %d", updated.TokenVersion)
		}
		last := audit.entries[len(audit.entries)-1]
		if last.Action != contracts.AuditActionUserUnsuspend {
			t.Fatalf("审计动作 = %q，期望 unsuspend", last.Action)
		}
		if !auditDetailBool(t, last, "changed") {
			t.Fatal("状态实际变化时审计 changed 应为 true")
		}
	})

	t.Run("重复停用幂等但吊销会话并记录 changed=false", func(t *testing.T) {
		target := mustCreateUser(t, users, model.User{
			ID: 4, Username: "carol", Status: model.StatusSuspended, TokenVersion: 6,
		})
		if err := sessions.Insert(ctx, model.Session{
			ID: 40, UserID: target.ID, TokenVersion: 6, ExpiresAt: now.Add(time.Hour),
		}, "hash-carol", now); err != nil {
			t.Fatalf("写入会话失败: %v", err)
		}
		updated, err := svc.SetUserStatus(ctx, contracts.UserStatusInput{
			ActorID: admin.ID, TargetID: target.ID, Suspend: true, At: now,
		})
		if err != nil {
			t.Fatalf("重复停用应成功: %v", err)
		}
		if updated.Status != model.StatusSuspended || updated.TokenVersion != 6 {
			t.Fatalf("重复停用不应改变状态/token_version: %+v", updated)
		}
		if sessions.sessions["hash-carol"].session.RevokedAt == nil {
			t.Fatal("停用请求即使状态未变化也必须吊销会话")
		}
		last := audit.entries[len(audit.entries)-1]
		if last.Action != contracts.AuditActionUserSuspend || auditDetailBool(t, last, "changed") {
			t.Fatalf("重复停用审计不符: %+v", last)
		}
	})

	t.Run("普通管理员不能处置超级管理员", func(t *testing.T) {
		_, err := svc.SetUserStatus(ctx, contracts.UserStatusInput{
			ActorID: admin.ID, TargetID: super.ID, Suspend: true, At: now,
		})
		if !apperr.IsKind(err, apperr.KindForbidden) {
			t.Fatalf("处置超管应返回 Forbidden，实际: %v", err)
		}
	})

	t.Run("超级管理员不能停用自己", func(t *testing.T) {
		_, err := svc.SetUserStatus(ctx, contracts.UserStatusInput{
			ActorID: super.ID, TargetID: super.ID, Suspend: true, At: now,
		})
		if !apperr.IsKind(err, apperr.KindValidation) {
			t.Fatalf("停用自己应返回 Validation，实际: %v", err)
		}
	})

	t.Run("目标/操作者不存在", func(t *testing.T) {
		if _, err := svc.SetUserStatus(ctx, contracts.UserStatusInput{ActorID: admin.ID, TargetID: 999, Suspend: true, At: now}); !apperr.IsKind(err, apperr.KindNotFound) {
			t.Fatalf("目标不存在应返回 NotFound，实际: %v", err)
		}
		if _, err := svc.SetUserStatus(ctx, contracts.UserStatusInput{ActorID: 999, TargetID: admin.ID, Suspend: true, At: now}); !apperr.IsKind(err, apperr.KindForbidden) {
			t.Fatalf("操作者不存在应返回 Forbidden，实际: %v", err)
		}
	})

	t.Run("操作者仓储故障返回 Internal", func(t *testing.T) {
		target := mustCreateUser(t, users, model.User{ID: 5, Username: "dave"})
		svc.users = &stubUserRepo{fakeUserRepo: users, byIDErr: errInjected}
		t.Cleanup(func() { svc.users = users })
		if _, err := svc.SetUserStatus(ctx, contracts.UserStatusInput{ActorID: admin.ID, TargetID: target.ID, Suspend: true, At: now}); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("操作者仓储故障应返回 Internal，实际: %v", err)
		}
	})

	t.Run("非管理员操作者被拒绝", func(t *testing.T) {
		target := mustCreateUser(t, users, model.User{ID: 6, Username: "erin"})
		plain := mustCreateUser(t, users, model.User{ID: 7, Username: "frank"})
		if _, err := svc.SetUserStatus(ctx, contracts.UserStatusInput{ActorID: plain.ID, TargetID: target.ID, Suspend: true, At: now}); !apperr.IsKind(err, apperr.KindForbidden) {
			t.Fatalf("非管理员操作应返回 Forbidden，实际: %v", err)
		}
	})

	t.Run("依赖未装配", func(t *testing.T) {
		if _, err := (&Service{}).SetUserStatus(ctx, contracts.UserStatusInput{}); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("缺少仓储应返回 Internal，实际: %v", err)
		}
		noTx := &Service{users: users, sessions: sessions}
		if _, err := noTx.SetUserStatus(ctx, contracts.UserStatusInput{ActorID: admin.ID, TargetID: admin.ID, At: now}); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("缺少事务管理器应返回 Internal，实际: %v", err)
		}
	})

	t.Run("更新状态/吊销会话/审计失败返回 Internal", func(t *testing.T) {
		target := mustCreateUser(t, users, model.User{ID: 8, Username: "grace"})

		svc.users = &stubUserRepo{fakeUserRepo: users, updateStatusErr: errInjected}
		if _, err := svc.SetUserStatus(ctx, contracts.UserStatusInput{ActorID: admin.ID, TargetID: target.ID, Suspend: true, At: now}); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("更新状态失败应返回 Internal，实际: %v", err)
		}
		svc.users = users

		svc.sessions = &stubSessionRepo{fakeSessionRepo: sessions, revokeAllErr: errInjected}
		if _, err := svc.SetUserStatus(ctx, contracts.UserStatusInput{ActorID: admin.ID, TargetID: target.ID, Suspend: true, At: now}); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("吊销会话失败应返回 Internal，实际: %v", err)
		}
		svc.sessions = sessions

		svc.audit = failingAudit{}
		t.Cleanup(func() { svc.audit = audit })
		if _, err := svc.SetUserStatus(ctx, contracts.UserStatusInput{ActorID: admin.ID, TargetID: target.ID, Suspend: true, At: now}); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("审计失败应返回 Internal，实际: %v", err)
		}
	})
}

func TestResetPasswordEdgeCases(t *testing.T) {
	ctx := context.Background()
	svc, users, sessions, audit, clk := newTestService(t, true)
	admin := mustCreateUser(t, users, model.User{ID: 1, Username: "mod", Role: model.RoleAdmin})
	super := mustCreateUser(t, users, model.User{ID: 2, Username: "root", Role: model.RoleAdmin, IsSuperAdmin: true})
	now := clk.Now()

	t.Run("新密码不符合要求", func(t *testing.T) {
		err := svc.ResetPassword(ctx, contracts.ResetPasswordInput{ActorID: admin.ID, TargetID: admin.ID, NewPassword: "short", At: now})
		appErr, ok := apperr.AsKind(err)
		if !ok || appErr.Kind != apperr.KindValidation {
			t.Fatalf("非法新密码应返回 Validation，实际: %v", err)
		}
		if details, ok := appErr.Details.(map[string]string); !ok || details["new_password"] == "" {
			t.Fatalf("应返回 new_password 字段详情: %+v", appErr.Details)
		}
	})

	t.Run("依赖未装配", func(t *testing.T) {
		if err := (&Service{}).ResetPassword(ctx, contracts.ResetPasswordInput{NewPassword: "new-password"}); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("缺少仓储应返回 Internal，实际: %v", err)
		}
		noTx := &Service{users: users, sessions: sessions}
		if err := noTx.ResetPassword(ctx, contracts.ResetPasswordInput{NewPassword: "new-password"}); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("缺少事务管理器应返回 Internal，实际: %v", err)
		}
	})

	t.Run("目标/操作者不存在", func(t *testing.T) {
		if err := svc.ResetPassword(ctx, contracts.ResetPasswordInput{ActorID: admin.ID, TargetID: 999, NewPassword: "new-password", At: now}); !apperr.IsKind(err, apperr.KindNotFound) {
			t.Fatalf("目标不存在应返回 NotFound，实际: %v", err)
		}
		target := mustCreateUser(t, users, model.User{ID: 10, Username: "target"})
		if err := svc.ResetPassword(ctx, contracts.ResetPasswordInput{ActorID: 999, TargetID: target.ID, NewPassword: "new-password", At: now}); !apperr.IsKind(err, apperr.KindForbidden) {
			t.Fatalf("操作者不存在应返回 Forbidden，实际: %v", err)
		}
	})

	t.Run("普通管理员不能重置超级管理员密码", func(t *testing.T) {
		err := svc.ResetPassword(ctx, contracts.ResetPasswordInput{ActorID: admin.ID, TargetID: super.ID, NewPassword: "new-password", At: now})
		if !apperr.IsKind(err, apperr.KindForbidden) {
			t.Fatalf("重置超管密码应返回 Forbidden，实际: %v", err)
		}
	})

	t.Run("仓储/吊销/审计失败返回 Internal", func(t *testing.T) {
		target := mustCreateUser(t, users, model.User{ID: 11, Username: "reset_target"})

		svc.users = &stubUserRepo{fakeUserRepo: users, updatePasswordErr: errInjected}
		if err := svc.ResetPassword(ctx, contracts.ResetPasswordInput{ActorID: admin.ID, TargetID: target.ID, NewPassword: "new-password", At: now}); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("更新密码失败应返回 Internal，实际: %v", err)
		}
		svc.users = users

		svc.sessions = &stubSessionRepo{fakeSessionRepo: sessions, revokeAllErr: errInjected}
		if err := svc.ResetPassword(ctx, contracts.ResetPasswordInput{ActorID: admin.ID, TargetID: target.ID, NewPassword: "new-password", At: now}); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("吊销会话失败应返回 Internal，实际: %v", err)
		}
		svc.sessions = sessions

		svc.audit = failingAudit{}
		t.Cleanup(func() { svc.audit = audit })
		if err := svc.ResetPassword(ctx, contracts.ResetPasswordInput{ActorID: admin.ID, TargetID: target.ID, NewPassword: "new-password", At: now}); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("审计失败应返回 Internal，实际: %v", err)
		}
	})
}
