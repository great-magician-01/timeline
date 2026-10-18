// Package app 编排 auth 模块用例：注册/登录/会话解析/资料/角色管理。
// 只依赖仓储与平台端口（不出现 gin/net/http/pgx），时间统一取自注入的 clock。
package app

import (
	"context"
	"log/slog"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/great-magician-01/timeline/internal/modules/auth/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/platform/ratelimit"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
)

// Deps 是组合根注入 auth app 的全部依赖。
type Deps struct {
	Users     UserRepo
	Sessions  SessionRepo
	Locks     BootstrapLocker
	Tx        TxManager
	IDs       IDGenerator
	Clock     clock.Clock
	Logger    *slog.Logger
	Settings  contracts.SettingsReader
	Audit     contracts.AuditWriter
	Relations contracts.RelationReader
	Stats     ProfileStatsReader

	// CookieSecure 决定会话/游客 CSRF Cookie 是否带 Secure 属性（生产为 true）。
	CookieSecure bool
}

// Service 是 auth 模块的应用服务；实现 contracts.AuthService（UserReader+UserWriter）。
type Service struct {
	users     UserRepo
	sessions  SessionRepo
	locks     BootstrapLocker
	tx        TxManager
	ids       IDGenerator
	clock     clock.Clock
	log       *slog.Logger
	settings  contracts.SettingsReader
	audit     contracts.AuditWriter
	relations contracts.RelationReader
	stats     ProfileStatsReader

	cookieSecure bool
	dummyHash    string

	loginAccountLimiter *ratelimit.Limiter
	loginPairLimiter    *ratelimit.Limiter
	loginIPLimiter      *ratelimit.Limiter
	registerLimiter     *ratelimit.Limiter
}

// 编译期断言：auth app 实现跨模块 AuthService（UserReader + UserWriter）。
var _ contracts.AuthService = (*Service)(nil)

// New 构造 auth 应用服务。Clock/Logger 为空时使用系统实现，便于测试与最小装配。
func New(deps Deps) *Service {
	clk := deps.Clock
	if clk == nil {
		clk = clock.System()
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	// 用户不存在时也执行一次同成本比较，降低通过响应时间枚举账号的可能。
	dummy, _ := bcrypt.GenerateFromPassword([]byte("timeline-dummy-password"), domain.PasswordBcryptCost)
	return &Service{
		users:               deps.Users,
		sessions:            deps.Sessions,
		locks:               deps.Locks,
		tx:                  deps.Tx,
		ids:                 deps.IDs,
		clock:               clk,
		log:                 logger,
		settings:            deps.Settings,
		audit:               deps.Audit,
		relations:           deps.Relations,
		stats:               deps.Stats,
		cookieSecure:        deps.CookieSecure,
		dummyHash:           string(dummy),
		loginAccountLimiter: ratelimit.New(30, 15*time.Minute),
		loginPairLimiter:    ratelimit.New(10, 5*time.Minute),
		loginIPLimiter:      ratelimit.New(30, 5*time.Minute),
		registerLimiter:     ratelimit.New(5, 10*time.Minute),
	}
}

// Logger 返回应用日志器，供 http 层记录内部错误。
func (s *Service) Logger() *slog.Logger { return s.log }

// CookieSecure 报告会话 Cookie 是否使用 Secure 属性。
func (s *Service) CookieSecure() bool { return s.cookieSecure }

func (s *Service) now() time.Time { return s.clock.Now() }

// settingsSnapshot 读取系统设置快照；未注入读取端口时回退默认开放注册。
func (s *Service) settingsSnapshot(ctx context.Context) (contracts.SystemSettings, error) {
	if s.settings == nil {
		return contracts.SystemSettings{RegistrationEnabled: true}, nil
	}
	return s.settings.Settings(ctx)
}

// settingsSnapshotOrDefaults 读取系统设置快照；数据库等读取失败时记录告警并回退
// 默认值（注册开放），与旧 settings.LoadOrDefaults 语义一致，供公开配置接口使用。
func (s *Service) settingsSnapshotOrDefaults(ctx context.Context) contracts.SystemSettings {
	settings, err := s.settingsSnapshot(ctx)
	if err != nil {
		s.log.Warn("读取系统设置失败，回退默认值", "err", err)
		return contracts.SystemSettings{RegistrationEnabled: true}
	}
	return settings
}

// internal 把非领域错误包装为 Internal Kind（http 层据此记录日志并返回 500）。
func internal(err error, message string) error {
	if _, ok := apperr.AsKind(err); ok {
		return err
	}
	return apperr.Wrap(err, apperr.KindInternal, message)
}

// rateLimited 构造带 Retry-After 的限频领域错误。
func rateLimited(retryAfter time.Duration) error {
	err := apperr.New(apperr.KindRateLimited, "rate_limited", "尝试过于频繁，请稍后重试")
	if retryAfter > 0 {
		err.RetryAfter = retryAfter
	}
	return err
}

// requireRepos 检查核心仓储已装配；缺失视为装配错误而不是用户错误。
func (s *Service) requireRepos() error {
	if s.users == nil || s.sessions == nil {
		return apperr.Internal("auth 模块仓储未初始化")
	}
	return nil
}

// isNotFound 判断错误链上是否为 NotFound 领域错误。
func isNotFound(err error) bool { return apperr.IsKind(err, apperr.KindNotFound) }
