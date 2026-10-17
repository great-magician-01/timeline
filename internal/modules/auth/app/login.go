package app

import (
	"context"
	"strings"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/auth/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// LoginInput 是登录用例输入；IP 由 http 层传入，用于三维限频。
type LoginInput struct {
	Identifier string
	Password   string
	IP         string
}

// LoginResult 是登录成功结果：明文令牌只在此处返回给 http 层写 Cookie，
// 数据库只保存会话令牌摘要。
type LoginResult struct {
	User         model.User
	SessionToken string
	CSRFToken    string
	ExpiresAt    time.Time
}

// Login 校验凭据并创建服务端会话。
// 限频维度：IP、账号、账号+IP；密码错误与账号不存在返回同一文案，降低枚举风险。
func (s *Service) Login(ctx context.Context, in LoginInput) (LoginResult, error) {
	if err := s.requireRepos(); err != nil {
		return LoginResult{}, err
	}
	if s.ids == nil || s.tx == nil {
		return LoginResult{}, apperr.Internal("auth 登录依赖未初始化")
	}

	identifier := strings.TrimSpace(in.Identifier)
	if identifier == "" || in.Password == "" || len(identifier) > 254 {
		return LoginResult{}, apperr.Invalid("请输入用户名或邮箱以及密码")
	}

	ipKey := "ip:" + in.IP
	accountKey := "account:" + strings.ToLower(identifier)
	pairKey := accountKey + "|" + in.IP
	allowedIP, retryIP := s.loginIPLimiter.Check(ipKey)
	allowedPair, retryPair := s.loginPairLimiter.Check(pairKey)
	allowedAccount, retryAccount := s.loginAccountLimiter.Check(accountKey)
	if !allowedIP || !allowedPair || !allowedAccount {
		return LoginResult{}, rateLimited(maxDuration(retryIP, retryPair, retryAccount))
	}

	user, err := s.users.ByLoginIdentifier(ctx, identifier)
	if err != nil {
		if isNotFound(err) {
			// 账号不存在也执行一次同成本比较，降低响应时间侧信道。
			domain.PasswordMatches(s.dummyHash, in.Password)
			s.recordLoginFailure(ipKey, accountKey, pairKey)
			return LoginResult{}, apperr.Unauthenticated("用户名/邮箱或密码错误")
		}
		return LoginResult{}, internal(err, "查询登录账号失败")
	}
	hash := user.PasswordHash
	if user.IsAI || hash == "" {
		hash = s.dummyHash
	}
	if user.IsAI || !domain.PasswordMatches(hash, in.Password) {
		s.recordLoginFailure(ipKey, accountKey, pairKey)
		return LoginResult{}, apperr.Unauthenticated("用户名/邮箱或密码错误")
	}
	if user.IsSuspended() {
		return LoginResult{}, apperr.Suspended("账号已停用，不能登录")
	}

	sessionID, err := s.ids.Next()
	if err != nil {
		return LoginResult{}, internal(err, "生成会话 ID 失败")
	}
	rawToken, err := domain.RandomToken()
	if err != nil {
		return LoginResult{}, internal(err, "生成会话令牌失败")
	}
	csrfToken, err := domain.RandomToken()
	if err != nil {
		return LoginResult{}, internal(err, "生成 CSRF 令牌失败")
	}
	now := s.now()
	expiresAt := now.Add(domain.SessionTTL)
	session := model.Session{
		ID:           sessionID,
		UserID:       user.ID,
		CSRFToken:    csrfToken,
		TokenVersion: user.TokenVersion,
		ExpiresAt:    expiresAt,
	}
	if err := s.sessions.Insert(ctx, session, domain.HashSessionToken(rawToken), now); err != nil {
		return LoginResult{}, internal(err, "创建登录会话失败")
	}

	// 成功登录只重置该账号及账号+IP 维度；IP 失败窗口继续保留，防止用已知账号
	// 刷新攻击额度（与旧实现一致）。
	s.loginPairLimiter.Reset(pairKey)
	s.loginAccountLimiter.Reset(accountKey)
	return LoginResult{User: user, SessionToken: rawToken, CSRFToken: csrfToken, ExpiresAt: expiresAt}, nil
}

func (s *Service) recordLoginFailure(ipKey, accountKey, pairKey string) {
	s.loginIPLimiter.Fail(ipKey)
	s.loginAccountLimiter.Fail(accountKey)
	s.loginPairLimiter.Fail(pairKey)
}

func maxDuration(values ...time.Duration) time.Duration {
	var max time.Duration
	for _, value := range values {
		if value > max {
			max = value
		}
	}
	return max
}
