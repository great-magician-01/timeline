package app

// 本文补充 auth/app 登录用例：依赖装配保护、输入校验、AI/无密码账号拒绝、
// 三维登录限频（账号/IP/账号+IP）与成功登录的会话写入参数（TokenVersion/过期时间/时钟）。
// 已有 app_test.go 已覆盖成功/密码错误/不存在/停用与基础会话解析，此处不重复。

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/auth/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// loginTestFixture 构造可断言会话写入细节的登录测试装配。
func loginTestFixture(t *testing.T) (*Service, *fakeUserRepo, *fakeSessionRepo) {
	t.Helper()
	users := newFakeUserRepo()
	sessions := newFakeSessionRepo(users)
	svc := New(Deps{
		Users:    users,
		Sessions: sessions,
		Locks:    fakeLocker{},
		Tx:       fakeTx{},
		IDs:      &fakeIDs{},
		Clock:    clock.NewFake(),
	})
	return svc, users, sessions
}

func TestLoginDependencyGuards(t *testing.T) {
	ctx := context.Background()

	t.Run("仓储未装配", func(t *testing.T) {
		_, err := (&Service{}).Login(ctx, LoginInput{Identifier: "alice", Password: "password123"})
		if !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("缺少仓储应返回 Internal，实际: %v", err)
		}
	})

	t.Run("登录依赖未初始化", func(t *testing.T) {
		users := newFakeUserRepo()
		for _, deps := range []Deps{
			{Users: users, Sessions: newFakeSessionRepo(users)},
			{Users: users, Sessions: newFakeSessionRepo(users), IDs: &fakeIDs{}},
			{Users: users, Sessions: newFakeSessionRepo(users), Tx: fakeTx{}},
		} {
			svc := &Service{users: deps.Users, sessions: deps.Sessions, ids: deps.IDs, tx: deps.Tx}
			if _, err := svc.Login(ctx, LoginInput{Identifier: "alice", Password: "password123"}); !apperr.IsKind(err, apperr.KindInternal) {
				t.Fatalf("缺少 ids/tx 应返回 Internal，实际: %v", err)
			}
		}
	})
}

func TestLoginInputValidation(t *testing.T) {
	ctx := context.Background()
	users := newFakeUserRepo()
	sessions := newFakeSessionRepo(users)
	// 校验分支在访问限频器之前返回，因此这里不需要装配限频器（不触发 nil）。
	svc := &Service{users: users, sessions: sessions, ids: &fakeIDs{}, tx: fakeTx{}}

	cases := []struct {
		name string
		in   LoginInput
	}{
		{"账号为空", LoginInput{Identifier: "", Password: "password123"}},
		{"账号仅有空白", LoginInput{Identifier: "   ", Password: "password123"}},
		{"密码为空", LoginInput{Identifier: "alice", Password: ""}},
		{"账号超长", LoginInput{Identifier: strings.Repeat("a", 255), Password: "password123"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := svc.Login(ctx, tc.in)
			if !apperr.IsKind(err, apperr.KindInvalid) {
				t.Fatalf("应返回 Invalid，实际: %v", err)
			}
			if result.SessionToken != "" || result.CSRFToken != "" {
				t.Fatalf("校验失败不应返回令牌: %+v", result)
			}
			if len(sessions.sessions) != 0 {
				t.Fatalf("校验失败不应创建会话: %d", len(sessions.sessions))
			}
		})
	}
}

// TestLoginRejectsAIAndPasswordlessAccounts 确认 AI 身份与没有密码哈希的账号
// 都走同一条 Unauthenticated 拒绝路径（账号不可用于密码登录）。
func TestLoginRejectsAIAndPasswordlessAccounts(t *testing.T) {
	ctx := context.Background()
	svc, users, sessions, _, _ := newTestService(t, true)
	hash := cheapHash(t, "password123")
	mustCreateUser(t, users, model.User{ID: 1, Username: "ai_bot", IsAI: true, PasswordHash: hash})
	mustCreateUser(t, users, model.User{ID: 2, Username: "no_hash", PasswordHash: ""})

	for _, identifier := range []string{"ai_bot", "no_hash"} {
		_, err := svc.Login(ctx, LoginInput{Identifier: identifier, Password: "password123", IP: "198.51.100.1"})
		if !apperr.IsKind(err, apperr.KindUnauthenticated) {
			t.Fatalf("%s 登录应返回 Unauthenticated，实际: %v", identifier, err)
		}
	}
	if len(sessions.sessions) != 0 {
		t.Fatalf("被拒绝的账号不应创建会话: %d", len(sessions.sessions))
	}
}

// TestLoginRateLimitDimensions 覆盖三维登录限频：账号、IP、账号+IP 任一维度达到
// 阈值都应返回 rate_limited（带 RetryAfter），且不创建会话。这里直接向服务内的
// 限频器填入与生产代码一致的键，避免为凑阈值执行大量 bcrypt 比对。
func TestLoginRateLimitDimensions(t *testing.T) {
	ctx := context.Background()
	svc, users, sessions, _, _ := newTestService(t, true)
	mustCreateUser(t, users, model.User{ID: 1, Username: "alice", PasswordHash: cheapHash(t, "password123")})

	const (
		pairWindow    = 5 * time.Minute
		accountWindow = 15 * time.Minute
		ipWindow      = 5 * time.Minute
	)
	cases := []struct {
		name       string
		identifier string
		ip         string
		maxRetry   time.Duration
		fill       func()
	}{
		{
			name:       "账号+IP 组合维度",
			identifier: "pair_case",
			ip:         "198.51.100.11",
			maxRetry:   pairWindow,
			fill: func() {
				for i := 0; i < 10; i++ {
					svc.loginPairLimiter.Fail("account:pair_case|198.51.100.11")
				}
			},
		},
		{
			name:       "账号维度",
			identifier: "account_case",
			ip:         "198.51.100.12",
			maxRetry:   accountWindow,
			fill: func() {
				for i := 0; i < 30; i++ {
					svc.loginAccountLimiter.Fail("account:account_case")
				}
			},
		},
		{
			name:       "IP 维度",
			identifier: "ip_case",
			ip:         "198.51.100.13",
			maxRetry:   ipWindow,
			fill: func() {
				for i := 0; i < 30; i++ {
					svc.loginIPLimiter.Fail("ip:198.51.100.13")
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.fill()
			_, err := svc.Login(ctx, LoginInput{Identifier: tc.identifier, Password: "password123", IP: tc.ip})
			appErr, ok := apperr.AsKind(err)
			if !ok || appErr.Kind != apperr.KindRateLimited {
				t.Fatalf("达到限频阈值应返回 rate_limited，实际: %v", err)
			}
			if appErr.RetryAfter <= 0 || appErr.RetryAfter > tc.maxRetry {
				t.Fatalf("RetryAfter = %v，应落在 (0, %v]", appErr.RetryAfter, tc.maxRetry)
			}
			if len(sessions.sessions) != 0 {
				t.Fatalf("限频拒绝不应创建会话: %d", len(sessions.sessions))
			}
		})
	}
}

// TestLoginSuccessResetsAccountAndPairLimitersButKeepsIP 覆盖登录成功后的限频重置语义：
// 成功只重置账号与账号+IP 维度，IP 失败窗口继续保留（防止用已知账号刷新攻击额度）。
func TestLoginSuccessResetsAccountAndPairLimitersButKeepsIP(t *testing.T) {
	ctx := context.Background()
	svc, users, _, _, _ := newTestService(t, true)
	password := "password123"
	for _, u := range []model.User{
		{ID: 1, Username: "pair_reset", PasswordHash: cheapHash(t, password)},
		{ID: 2, Username: "account_reset", PasswordHash: cheapHash(t, password)},
		{ID: 3, Username: "ip_keep", PasswordHash: cheapHash(t, password)},
	} {
		mustCreateUser(t, users, u)
	}

	t.Run("成功重置账号+IP 维度", func(t *testing.T) {
		ip := "198.51.100.21"
		for i := 0; i < 9; i++ {
			svc.loginPairLimiter.Fail("account:pair_reset|" + ip)
			svc.loginAccountLimiter.Fail("account:pair_reset")
		}
		if _, err := svc.Login(ctx, LoginInput{Identifier: "pair_reset", Password: password, IP: ip}); err != nil {
			t.Fatalf("成功登录应通过（重置前计数未达阈值）: %v", err)
		}
		// 若 pair 维度未重置：第 1 次失败后计数到 10，第 2 次会被限频。
		for attempt := 1; attempt <= 2; attempt++ {
			_, err := svc.Login(ctx, LoginInput{Identifier: "pair_reset", Password: "wrong-password", IP: ip})
			if !apperr.IsKind(err, apperr.KindUnauthenticated) {
				t.Fatalf("第 %d 次失败应返回 Unauthenticated（说明 pair 计数已重置），实际: %v", attempt, err)
			}
		}
	})

	t.Run("成功重置账号维度", func(t *testing.T) {
		ip := "198.51.100.22"
		for i := 0; i < 29; i++ {
			svc.loginAccountLimiter.Fail("account:account_reset")
		}
		if _, err := svc.Login(ctx, LoginInput{Identifier: "account_reset", Password: password, IP: ip}); err != nil {
			t.Fatalf("成功登录应通过: %v", err)
		}
		// 若账号维度未重置：第 1 次失败后计数到 30，第 2 次会被限频。
		for attempt := 1; attempt <= 2; attempt++ {
			_, err := svc.Login(ctx, LoginInput{Identifier: "account_reset", Password: "wrong-password", IP: ip})
			if !apperr.IsKind(err, apperr.KindUnauthenticated) {
				t.Fatalf("第 %d 次失败应返回 Unauthenticated（说明账号计数已重置），实际: %v", attempt, err)
			}
		}
	})

	t.Run("成功不重置 IP 维度", func(t *testing.T) {
		ip := "198.51.100.23"
		for i := 0; i < 29; i++ {
			svc.loginIPLimiter.Fail("ip:" + ip)
		}
		if _, err := svc.Login(ctx, LoginInput{Identifier: "ip_keep", Password: password, IP: ip}); err != nil {
			t.Fatalf("成功登录应通过: %v", err)
		}
		// IP 计数为 29，本次失败后到 30；下一次请求必须被 IP 维度拒绝。
		if _, err := svc.Login(ctx, LoginInput{Identifier: "ip_keep", Password: "wrong-password", IP: ip}); !apperr.IsKind(err, apperr.KindUnauthenticated) {
			t.Fatalf("第一次失败应返回 Unauthenticated，实际: %v", err)
		}
		if _, err := svc.Login(ctx, LoginInput{Identifier: "ip_keep", Password: "wrong-password", IP: ip}); !apperr.IsKind(err, apperr.KindRateLimited) {
			t.Fatalf("IP 失败窗口未重置时应返回 rate_limited，实际: %v", err)
		}
	})
}

// TestLoginWritesSessionWithClockAndTokenVersion 断言成功登录写入会话仓储的参数：
// 令牌只保存摘要、TokenVersion 跟随账号、过期时间 = 应用时钟 now + SessionTTL、
// created_at 使用同一 now，且登录结果返回明文令牌与 CSRF 令牌。
func TestLoginWritesSessionWithClockAndTokenVersion(t *testing.T) {
	ctx := context.Background()
	svc, users, sessions, _, clk := newTestService(t, true)
	recorder := &recordingSessionRepo{fakeSessionRepo: sessions}
	svc.sessions = recorder
	mustCreateUser(t, users, model.User{
		ID: 7, Username: "alice", Email: "alice@example.com",
		PasswordHash: cheapHash(t, "password123"), TokenVersion: 5,
	})
	now := clk.Now()

	result, err := svc.Login(ctx, LoginInput{Identifier: "alice", Password: "password123", IP: "198.51.100.31"})
	if err != nil {
		t.Fatalf("登录应成功: %v", err)
	}
	if result.SessionToken == "" || result.CSRFToken == "" {
		t.Fatalf("登录结果缺少令牌: %+v", result)
	}
	if got, want := result.ExpiresAt, now.Add(domain.SessionTTL); !got.Equal(want) {
		t.Fatalf("ExpiresAt = %v，期望 %v", got, want)
	}

	hash := hashSessionTokenForTest(result.SessionToken)
	stored, ok := sessions.sessions[hash]
	if !ok {
		t.Fatalf("会话未按令牌摘要保存；keys=%v", keysOf(sessions.sessions))
	}
	if stored.session.TokenVersion != 5 {
		t.Fatalf("会话 TokenVersion = %d，期望 5", stored.session.TokenVersion)
	}
	if stored.session.UserID != 7 || stored.session.CSRFToken != result.CSRFToken {
		t.Fatalf("会话字段不符: %+v", stored.session)
	}
	if !stored.session.ExpiresAt.Equal(now.Add(domain.SessionTTL)) {
		t.Fatalf("会话过期时间 = %v", stored.session.ExpiresAt)
	}
	if stored.session.RevokedAt != nil {
		t.Fatal("新会话不应标记吊销")
	}
	if recorder.lastInsertAt == nil || !recorder.lastInsertAt.Equal(now) {
		t.Fatalf("会话 created_at 应使用应用时钟 now=%v，实际: %v", now, recorder.lastInsertAt)
	}
}

func keysOf[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}

// TestLoginInternalErrorBranches 覆盖登录内部错误：会话 ID 生成失败、会话写入失败
// 都应映射为 Internal 而不是 401。
func TestLoginInternalErrorBranches(t *testing.T) {
	ctx := context.Background()
	svc, users, sessions, _, _ := newTestService(t, true)
	mustCreateUser(t, users, model.User{ID: 1, Username: "alice", PasswordHash: cheapHash(t, "password123")})

	originalIDs := svc.ids
	svc.ids = failingIDs{}
	if _, err := svc.Login(ctx, LoginInput{Identifier: "alice", Password: "password123", IP: "198.51.100.41"}); !apperr.IsKind(err, apperr.KindInternal) {
		t.Fatalf("会话 ID 生成失败应返回 Internal，实际: %v", err)
	}
	svc.ids = originalIDs

	svc.sessions = &stubSessionRepo{fakeSessionRepo: sessions, insertErr: errInjected}
	if _, err := svc.Login(ctx, LoginInput{Identifier: "alice", Password: "password123", IP: "198.51.100.42"}); !apperr.IsKind(err, apperr.KindInternal) {
		t.Fatalf("会话写入失败应返回 Internal，实际: %v", err)
	}
}
