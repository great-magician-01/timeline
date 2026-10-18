package app

// 本文补充 auth/app 会话与改密用例：会话解析的全部失效状态、会话仓储故障、
// 游客 CSRF、退出登录幂等，以及修改密码的校验/停用/仓储错误分支。
// 已有 app_test.go 覆盖 valid/missing/not_found/expired 与改密成功主路径，此处不重复。

import (
	"context"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/auth/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

func TestResolveSessionEdgeStates(t *testing.T) {
	ctx := context.Background()
	svc, users, sessions, _, clk := newTestService(t, true)
	now := clk.Now()

	seed := func(u model.User) model.User {
		t.Helper()
		return mustCreateUser(t, users, u)
	}
	insertSession := func(session model.Session, rawToken string) {
		t.Helper()
		if err := sessions.Insert(ctx, session, hashSessionTokenForTest(rawToken), now); err != nil {
			t.Fatalf("写入会话失败: %v", err)
		}
	}

	t.Run("会话不存在的仓储故障返回 Internal", func(t *testing.T) {
		svc.sessions = &stubSessionRepo{fakeSessionRepo: sessions, lookupErr: errInjected}
		t.Cleanup(func() { svc.sessions = sessions })
		if _, err := svc.ResolveSession(ctx, "any-token"); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("仓储故障应返回 Internal，实际: %v", err)
		}
	})

	t.Run("会话仓储未装配", func(t *testing.T) {
		if _, err := (&Service{}).ResolveSession(ctx, "any-token"); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("缺少会话仓储应返回 Internal，实际: %v", err)
		}
	})

	t.Run("已吊销会话", func(t *testing.T) {
		user := seed(model.User{ID: 1, Username: "revoked_user"})
		const rawToken = "revoked-token"
		insertSession(model.Session{ID: 11, UserID: user.ID, TokenVersion: user.TokenVersion, ExpiresAt: now.Add(time.Hour)}, rawToken)
		if err := sessions.Revoke(ctx, 11, now); err != nil {
			t.Fatalf("吊销会话失败: %v", err)
		}
		result, err := svc.ResolveSession(ctx, rawToken)
		if err != nil {
			t.Fatalf("ResolveSession: %v", err)
		}
		if result.State != domain.SessionRevoked || result.Viewer.Authenticated {
			t.Fatalf("已吊销会话应返回 revoked 且不带身份，实际: state=%v viewer=%+v", result.State, result.Viewer)
		}
	})

	t.Run("token_version 不匹配", func(t *testing.T) {
		user := seed(model.User{ID: 2, Username: "version_user", TokenVersion: 3})
		const rawToken = "version-mismatch-token"
		insertSession(model.Session{ID: 12, UserID: user.ID, TokenVersion: 2, ExpiresAt: now.Add(time.Hour)}, rawToken)
		result, err := svc.ResolveSession(ctx, rawToken)
		if err != nil {
			t.Fatalf("ResolveSession: %v", err)
		}
		if result.State != domain.SessionVersionMismatch {
			t.Fatalf("token_version 不匹配应返回 version_mismatch，实际: %v", result.State)
		}
	})

	t.Run("AI 账号不能持有 Web 会话", func(t *testing.T) {
		user := seed(model.User{ID: 3, Username: "ai_session", IsAI: true})
		const rawToken = "ai-session-token"
		insertSession(model.Session{ID: 13, UserID: user.ID, TokenVersion: user.TokenVersion, ExpiresAt: now.Add(time.Hour)}, rawToken)
		result, err := svc.ResolveSession(ctx, rawToken)
		if err != nil {
			t.Fatalf("ResolveSession: %v", err)
		}
		if result.State != domain.SessionAIAccount || result.Viewer.Authenticated {
			t.Fatalf("AI 会话应返回 ai_account，实际: state=%v viewer=%+v", result.State, result.Viewer)
		}
	})

	t.Run("有效会话返回管理员身份", func(t *testing.T) {
		user := seed(model.User{ID: 4, Username: "root_user", Role: model.RoleAdmin, IsSuperAdmin: true})
		const rawToken = "admin-token"
		insertSession(model.Session{
			ID: 14, UserID: user.ID, CSRFToken: "csrf-admin",
			TokenVersion: user.TokenVersion, ExpiresAt: now.Add(time.Hour),
		}, rawToken)
		result, err := svc.ResolveSession(ctx, rawToken)
		if err != nil {
			t.Fatalf("ResolveSession: %v", err)
		}
		if result.State != domain.SessionValid || !result.Viewer.Authenticated {
			t.Fatalf("有效会话应返回 valid，实际: %+v", result)
		}
		if !result.Viewer.IsAdmin || !result.Viewer.IsSuperAdmin {
			t.Fatalf("管理员身份未注入: %+v", result.Viewer)
		}
		if result.Session.CSRFToken != "csrf-admin" || result.User.ID != 4 {
			t.Fatalf("会话解析结果不符: %+v", result)
		}
	})
}

func TestGuestCSRFTokenAndLogout(t *testing.T) {
	ctx := context.Background()

	t.Run("游客 CSRF 令牌随机且非空", func(t *testing.T) {
		svc := &Service{}
		first, err := svc.GuestCSRFToken()
		if err != nil || first == "" {
			t.Fatalf("生成游客 CSRF 失败: token=%q err=%v", first, err)
		}
		second, err := svc.GuestCSRFToken()
		if err != nil || second == "" {
			t.Fatalf("生成游客 CSRF 失败: token=%q err=%v", second, err)
		}
		if first == second {
			t.Fatal("两次游客 CSRF 令牌不应相同")
		}
	})

	t.Run("会话仓储未装配", func(t *testing.T) {
		if err := (&Service{}).Logout(ctx, 1); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("缺少会话仓储应返回 Internal，实际: %v", err)
		}
	})

	svc, users, sessions, _, clk := newTestService(t, true)
	user := mustCreateUser(t, users, model.User{ID: 1, Username: "alice"})
	now := clk.Now()
	const rawToken = "logout-token"
	if err := sessions.Insert(ctx, model.Session{
		ID: 20, UserID: user.ID, TokenVersion: user.TokenVersion, ExpiresAt: now.Add(time.Hour),
	}, hashSessionTokenForTest(rawToken), now); err != nil {
		t.Fatalf("写入会话失败: %v", err)
	}

	t.Run("退出登录吊销会话且幂等", func(t *testing.T) {
		if err := svc.Logout(ctx, 20); err != nil {
			t.Fatalf("退出登录失败: %v", err)
		}
		revokedAt := sessions.sessions[hashSessionTokenForTest(rawToken)].session.RevokedAt
		if revokedAt == nil || !revokedAt.Equal(now) {
			t.Fatalf("会话应以应用时钟吊销，实际: %v", revokedAt)
		}
		// 幂等：重复退出不应报错，也不应改变吊销时间。
		if err := svc.Logout(ctx, 20); err != nil {
			t.Fatalf("重复退出登录应幂等: %v", err)
		}
		if again := sessions.sessions[hashSessionTokenForTest(rawToken)].session.RevokedAt; again == nil || !again.Equal(now) {
			t.Fatalf("重复退出不应改变吊销时间: %v", again)
		}
	})

	t.Run("吊销失败返回 Internal", func(t *testing.T) {
		svc.sessions = &stubSessionRepo{fakeSessionRepo: sessions, revokeErr: errInjected}
		t.Cleanup(func() { svc.sessions = sessions })
		if err := svc.Logout(ctx, 20); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("吊销失败应返回 Internal，实际: %v", err)
		}
	})
}

func TestChangePasswordErrorAndGuards(t *testing.T) {
	ctx := context.Background()
	svc, users, sessions, _, clk := newTestService(t, true)
	password := "old-password"
	user := mustCreateUser(t, users, model.User{ID: 1, Username: "alice", PasswordHash: cheapHash(t, password)})
	now := clk.Now()
	current := model.Session{ID: 30, UserID: user.ID, TokenVersion: user.TokenVersion, ExpiresAt: now.Add(time.Hour)}
	other := model.Session{ID: 31, UserID: user.ID, TokenVersion: user.TokenVersion, ExpiresAt: now.Add(time.Hour)}
	if err := sessions.Insert(ctx, current, "hash-current", now); err != nil {
		t.Fatalf("写入当前会话失败: %v", err)
	}
	if err := sessions.Insert(ctx, other, "hash-other", now); err != nil {
		t.Fatalf("写入其他会话失败: %v", err)
	}

	cases := []struct {
		name  string
		in    ChangePasswordInput
		kinds []apperr.Kind
	}{
		{
			name:  "新密码过短",
			in:    ChangePasswordInput{UserID: user.ID, SessionID: 30, OldPassword: password, NewPassword: "short"},
			kinds: []apperr.Kind{apperr.KindValidation},
		},
		{
			name:  "新密码过长",
			in:    ChangePasswordInput{UserID: user.ID, SessionID: 30, OldPassword: password, NewPassword: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
			kinds: []apperr.Kind{apperr.KindValidation},
		},
		{
			name:  "目标账号不存在",
			in:    ChangePasswordInput{UserID: 999, SessionID: 30, OldPassword: password, NewPassword: "new-password"},
			kinds: []apperr.Kind{apperr.KindNotFound},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := svc.ChangePassword(ctx, tc.in)
			appErr, ok := apperr.AsKind(err)
			if !ok {
				t.Fatalf("应返回领域错误，实际: %v", err)
			}
			matched := false
			for _, kind := range tc.kinds {
				if appErr.Kind == kind {
					matched = true
				}
			}
			if !matched {
				t.Fatalf("错误 Kind = %v，期望 %v", appErr.Kind, tc.kinds)
			}
		})
	}

	t.Run("停用账号不能修改密码", func(t *testing.T) {
		suspended := mustCreateUser(t, users, model.User{
			ID: 2, Username: "suspended_user", PasswordHash: cheapHash(t, password), Status: model.StatusSuspended,
		})
		err := svc.ChangePassword(ctx, ChangePasswordInput{
			UserID: suspended.ID, SessionID: 30, OldPassword: password, NewPassword: "new-password",
		})
		if !apperr.IsKind(err, apperr.KindSuspended) {
			t.Fatalf("停用账号修改密码应返回 Suspended，实际: %v", err)
		}
	})

	t.Run("依赖未装配", func(t *testing.T) {
		if err := (&Service{}).ChangePassword(ctx, ChangePasswordInput{}); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("缺少仓储应返回 Internal，实际: %v", err)
		}
		noTx := &Service{users: users, sessions: sessions}
		if err := noTx.ChangePassword(ctx, ChangePasswordInput{UserID: user.ID, NewPassword: "new-password"}); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("缺少事务管理器应返回 Internal，实际: %v", err)
		}
	})

	t.Run("更新密码失败返回 Internal", func(t *testing.T) {
		restoreUser := users.users[user.ID]
		t.Cleanup(func() { users.users[user.ID] = restoreUser })
		svc.users = &stubUserRepo{fakeUserRepo: users, updatePasswordErr: errInjected}
		t.Cleanup(func() { svc.users = users })
		if err := svc.ChangePassword(ctx, ChangePasswordInput{
			UserID: user.ID, SessionID: 30, OldPassword: password, NewPassword: "new-password",
		}); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("更新密码失败应返回 Internal，实际: %v", err)
		}
	})

	t.Run("吊销其他会话失败返回 Internal", func(t *testing.T) {
		restoreUser := users.users[user.ID]
		t.Cleanup(func() { users.users[user.ID] = restoreUser })
		svc.sessions = &stubSessionRepo{fakeSessionRepo: sessions, revokeAllExceptErr: errInjected}
		t.Cleanup(func() { svc.sessions = sessions })
		if err := svc.ChangePassword(ctx, ChangePasswordInput{
			UserID: user.ID, SessionID: 30, OldPassword: password, NewPassword: "new-password",
		}); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("吊销其他会话失败应返回 Internal，实际: %v", err)
		}
	})

	t.Run("同步当前会话失败返回 Internal", func(t *testing.T) {
		restoreUser := users.users[user.ID]
		t.Cleanup(func() { users.users[user.ID] = restoreUser })
		svc.sessions = &stubSessionRepo{fakeSessionRepo: sessions, updateVersionErr: errInjected}
		t.Cleanup(func() { svc.sessions = sessions })
		if err := svc.ChangePassword(ctx, ChangePasswordInput{
			UserID: user.ID, SessionID: 30, OldPassword: password, NewPassword: "new-password",
		}); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("同步当前会话失败应返回 Internal，实际: %v", err)
		}
	})
}
