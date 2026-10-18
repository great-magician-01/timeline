package app

// 本文补充 auth/app 注册、初始化状态与服务级辅助用例：
// 注册限频/设置读取失败/初始化锁/唯一冲突/事务内二次复核等分支、BootstrapStatus
// 的统计错误、Config 的默认回退，以及 internal/rateLimited/requireRepos 等辅助。
// 已有 app_test.go 已覆盖空库首个超管、开放/关闭注册与首注册不变式，此处不重复。

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

func validRegisterInput(ip string) RegisterInput {
	return RegisterInput{Username: "new_user", Email: "new@example.com", Password: "password123", IP: ip}
}

func TestRegisterDependencyGuards(t *testing.T) {
	ctx := context.Background()
	if _, err := (&Service{}).Register(ctx, validRegisterInput("203.0.113.1")); !apperr.IsKind(err, apperr.KindInternal) {
		t.Fatalf("缺少仓储应返回 Internal，实际: %v", err)
	}
	users := newFakeUserRepo()
	sessions := newFakeSessionRepo(users)
	guarded := []*Service{
		{users: users, sessions: sessions},
		{users: users, sessions: sessions, tx: fakeTx{}, ids: &fakeIDs{}},
		{users: users, sessions: sessions, tx: fakeTx{}, locks: fakeLocker{}},
	}
	for _, svc := range guarded {
		if _, err := svc.Register(ctx, validRegisterInput("203.0.113.2")); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("缺少注册依赖应返回 Internal，实际: %v", err)
		}
	}
}

func TestRegisterRateLimitAndEarlyErrors(t *testing.T) {
	ctx := context.Background()
	svc, users, _, _, _ := newTestService(t, true)

	t.Run("超过 IP 注册限频阈值", func(t *testing.T) {
		const ip = "203.0.113.11"
		for i := 0; i < 5; i++ {
			svc.registerLimiter.Consume("ip:" + ip)
		}
		_, err := svc.Register(ctx, validRegisterInput(ip))
		if !apperr.IsKind(err, apperr.KindRateLimited) {
			t.Fatalf("超过注册限频应返回 rate_limited，实际: %v", err)
		}
	})

	t.Run("读取注册开关失败返回 Internal", func(t *testing.T) {
		svc.settings = fakeSettings{err: errInjected}
		t.Cleanup(func() {
			svc.settings = fakeSettings{values: contracts.SystemSettings{RegistrationEnabled: true}}
		})
		if _, err := svc.Register(ctx, validRegisterInput("203.0.113.12")); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("读取注册开关失败应返回 Internal，实际: %v", err)
		}
	})

	t.Run("读取初始化状态失败返回 Internal", func(t *testing.T) {
		users.countErr = errInjected
		t.Cleanup(func() { users.countErr = nil })
		if _, err := svc.Register(ctx, validRegisterInput("203.0.113.13")); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("读取初始化状态失败应返回 Internal，实际: %v", err)
		}
	})
}

func TestRegisterFailureMappings(t *testing.T) {
	ctx := context.Background()
	svc, users, _, _, _ := newTestService(t, true)
	restoreSettings := func() {
		svc.settings = fakeSettings{values: contracts.SystemSettings{RegistrationEnabled: true}}
	}

	t.Run("用户名重复映射 Conflict", func(t *testing.T) {
		mustCreateUser(t, users, model.User{ID: 1, Username: "new_user", Email: "other@example.com"})
		if _, err := svc.Register(ctx, validRegisterInput("203.0.113.21")); !apperr.IsKind(err, apperr.KindConflict) {
			t.Fatalf("重复用户名应返回 Conflict，实际: %v", err)
		}
	})

	t.Run("初始化锁获取失败返回 Internal", func(t *testing.T) {
		svc.locks = failingLocker{}
		t.Cleanup(func() { svc.locks = fakeLocker{} })
		if _, err := svc.Register(ctx, validRegisterInput("203.0.113.22")); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("初始化锁失败应返回 Internal，实际: %v", err)
		}
	})

	t.Run("用户 ID 生成失败返回 Internal", func(t *testing.T) {
		svc.ids = failingIDs{}
		t.Cleanup(func() { svc.ids = &fakeIDs{} })
		if _, err := svc.Register(ctx, validRegisterInput("203.0.113.23")); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("ID 生成失败应返回 Internal，实际: %v", err)
		}
	})

	t.Run("事务内统计失败返回 Internal", func(t *testing.T) {
		svc.users = &errorAfterCountRepo{fakeUserRepo: users, okCalls: 1, err: errInjected}
		t.Cleanup(func() { svc.users = users })
		if _, err := svc.Register(ctx, validRegisterInput("203.0.113.24")); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("事务内统计失败应返回 Internal，实际: %v", err)
		}
	})

	t.Run("事务内二次复核关闭注册", func(t *testing.T) {
		// 预检查看到空库（bootstrap=true）因此跳过关闭判断；事务内看到已有用户，
		// 且注册开关关闭，必须再次拒绝，防止并发初始化竞态。
		svc.users = &sequenceCountRepo{fakeUserRepo: users, counts: []int64{0, 1}}
		t.Cleanup(func() { svc.users = users })
		svc.settings = fakeSettings{values: contracts.SystemSettings{RegistrationEnabled: false}}
		t.Cleanup(restoreSettings)
		if _, err := svc.Register(ctx, validRegisterInput("203.0.113.25")); !apperr.IsKind(err, apperr.KindForbidden) {
			t.Fatalf("事务内二次复核应返回 Forbidden，实际: %v", err)
		}
	})
}

func TestBootstrapStatusAndConfig(t *testing.T) {
	ctx := context.Background()
	svc, users, _, _, _ := newTestService(t, true)
	mustCreateUser(t, users, model.User{ID: 1, Username: "root", Role: model.RoleAdmin, IsSuperAdmin: true})
	mustCreateUser(t, users, model.User{ID: 2, Username: "alice"})

	t.Run("统计人类用户与超级管理员", func(t *testing.T) {
		status, err := svc.BootstrapStatus(ctx)
		if err != nil {
			t.Fatalf("BootstrapStatus: %v", err)
		}
		if status.HumanUsers != 2 || status.SuperAdmins != 1 {
			t.Fatalf("初始化状态不符: %+v", status)
		}
	})

	t.Run("人类用户统计失败", func(t *testing.T) {
		users.countErr = errInjected
		t.Cleanup(func() { users.countErr = nil })
		if _, err := svc.BootstrapStatus(ctx); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("人类用户统计失败应返回 Internal，实际: %v", err)
		}
		if _, err := svc.BootstrapRequired(ctx); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("初始化状态读取失败应返回 Internal，实际: %v", err)
		}
	})

	t.Run("超级管理员统计失败", func(t *testing.T) {
		svc.users = &stubUserRepo{fakeUserRepo: users, countSuperAdminsErr: errInjected}
		t.Cleanup(func() { svc.users = users })
		if _, err := svc.BootstrapStatus(ctx); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("超管统计失败应返回 Internal，实际: %v", err)
		}
	})

	t.Run("仓储未装配", func(t *testing.T) {
		empty := &Service{}
		if _, err := empty.BootstrapStatus(ctx); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("缺少仓储应返回 Internal，实际: %v", err)
		}
		if _, err := empty.BootstrapRequired(ctx); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("缺少仓储应返回 Internal，实际: %v", err)
		}
	})

	t.Run("Config 在设置/初始化读取失败时回退默认值", func(t *testing.T) {
		// 未装配设置与账号端口：设置回退开放注册，初始化状态失败回退 false 且不报错。
		config, err := (&Service{log: slog.Default()}).Config(ctx)
		if err != nil {
			t.Fatalf("Config 不应因缺少端口报错: %v", err)
		}
		if !config.RegistrationEnabled || config.BootstrapRequired {
			t.Fatalf("默认配置不符: %+v", config)
		}
	})
}

func TestServiceAccessorsAndErrorHelpers(t *testing.T) {
	clk := clock.NewFake()
	svc := New(Deps{Clock: clk, CookieSecure: true})
	if svc.Logger() == nil {
		t.Fatal("Logger 不应为 nil")
	}
	if svc.Logger() != slog.Default() {
		t.Fatal("未注入 Logger 时应使用默认日志器")
	}
	if !svc.CookieSecure() {
		t.Fatal("CookieSecure 应反映注入配置")
	}
	if got := svc.now(); !got.Equal(clk.Now()) {
		t.Fatalf("now() = %v，期望 %v", got, clk.Now())
	}

	cause := errors.New("底层原因")
	wrapped := internal(cause, "内部错误")
	appErr, ok := apperr.AsKind(wrapped)
	if !ok || appErr.Kind != apperr.KindInternal || !errors.Is(wrapped, cause) {
		t.Fatalf("internal 应包装为 Internal 并保留错误链: %v", wrapped)
	}
	notFound := apperr.NotFound("不存在")
	if preserved := internal(notFound, "忽略文案"); preserved != error(notFound) {
		t.Fatalf("internal 不应重复包装领域错误: %v", preserved)
	}

	if err := rateLimited(0); err.(*apperr.Error).RetryAfter != 0 {
		t.Fatalf("retryAfter<=0 时不应设置 RetryAfter: %v", err)
	}
	if err := rateLimited(1500 * time.Millisecond); err.(*apperr.Error).RetryAfter != 1500*time.Millisecond {
		t.Fatalf("RetryAfter 不符: %v", err)
	}

	if err := (&Service{}).requireRepos(); !apperr.IsKind(err, apperr.KindInternal) {
		t.Fatalf("requireRepos 缺少仓储应返回 Internal，实际: %v", err)
	}
	users := newFakeUserRepo()
	if err := (&Service{users: users}).requireRepos(); !apperr.IsKind(err, apperr.KindInternal) {
		t.Fatalf("requireRepos 缺少会话仓储应返回 Internal，实际: %v", err)
	}
	if err := (&Service{users: users, sessions: newFakeSessionRepo(users)}).requireRepos(); err != nil {
		t.Fatalf("仓储完整时 requireRepos 应返回 nil，实际: %v", err)
	}

	if !isNotFound(apperr.NotFound("x")) || isNotFound(errors.New("x")) || isNotFound(nil) {
		t.Fatal("isNotFound 判定不符")
	}
}
