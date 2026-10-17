package app

import (
	"context"

	"github.com/great-magician-01/timeline/internal/modules/auth/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// ResolveResult 是一次会话解析结果：State 表达缺失/失效原因，Viewer 与 Session
// 只在 SessionValid 时有效。中间件据此决定是否向上下文注入身份。
type ResolveResult struct {
	User    model.User
	Viewer  model.Viewer
	Session model.Session
	State   domain.SessionState
}

// ResolveSession 用会话 Cookie 明文解析当前身份（每个请求至多一次 JOIN 查询）。
// 会话不存在返回 SessionNotFound 而不是错误，保证公开读可以按游客继续。
func (s *Service) ResolveSession(ctx context.Context, rawToken string) (ResolveResult, error) {
	if rawToken == "" {
		return ResolveResult{Viewer: model.GuestViewer(), State: domain.SessionMissing}, nil
	}
	if s.sessions == nil {
		return ResolveResult{}, apperr.Internal("auth 会话仓储未初始化")
	}
	user, session, err := s.sessions.ByTokenHash(ctx, domain.HashSessionToken(rawToken))
	if err != nil {
		if isNotFound(err) {
			return ResolveResult{Viewer: model.GuestViewer(), State: domain.SessionNotFound}, nil
		}
		return ResolveResult{}, internal(err, "读取登录会话失败")
	}
	viewer, state := domain.ValidateSession(user, session, s.now())
	return ResolveResult{User: user, Viewer: viewer, Session: session, State: state}, nil
}

// GuestCSRFToken 生成游客双提交令牌；Cookie 由 http 层写入。
func (s *Service) GuestCSRFToken() (string, error) {
	token, err := domain.RandomToken()
	if err != nil {
		return "", internal(err, "生成 CSRF 令牌失败")
	}
	return token, nil
}

// Logout 吊销当前会话（幂等：已吊销时不报错）。
func (s *Service) Logout(ctx context.Context, sessionID int64) error {
	if s.sessions == nil {
		return apperr.Internal("auth 会话仓储未初始化")
	}
	if err := s.sessions.Revoke(ctx, sessionID, s.now()); err != nil {
		return internal(err, "退出登录失败")
	}
	return nil
}

// ChangePasswordInput 是修改密码用例输入。
type ChangePasswordInput struct {
	UserID      int64
	SessionID   int64
	OldPassword string
	NewPassword string
}

// ChangePassword 校验旧密码、更新密码并吊销其他会话；当前会话保留并同步
// token_version。bcrypt 哈希在事务外计算，事务内只做校验与写入。
func (s *Service) ChangePassword(ctx context.Context, in ChangePasswordInput) error {
	if err := s.requireRepos(); err != nil {
		return err
	}
	if s.tx == nil {
		return apperr.Internal("auth 事务管理器未初始化")
	}
	if err := domain.ValidatePassword(in.NewPassword); err != nil {
		return apperr.Validation("新密码不符合要求").
			WithDetails(map[string]string{"new_password": err.Error()})
	}
	newHash, err := domain.HashPassword(in.NewPassword)
	if err != nil {
		return apperr.Validation("新密码不符合要求").
			WithDetails(map[string]string{"new_password": err.Error()})
	}

	return s.tx.WithTx(ctx, func(txCtx context.Context) error {
		user, err := s.users.ByIDForUpdate(txCtx, in.UserID)
		if err != nil {
			return err
		}
		if user.IsSuspended() {
			return apperr.Suspended("账号已停用，不能修改密码")
		}
		if !domain.PasswordMatches(user.PasswordHash, in.OldPassword) {
			return apperr.Validation("原密码不正确").
				WithDetails(map[string]string{"old_password": "原密码不正确"})
		}
		newVersion := user.TokenVersion + 1
		now := s.now()
		if err := s.users.UpdatePassword(txCtx, user.ID, newHash, newVersion, now); err != nil {
			return internal(err, "更新密码失败")
		}
		if err := s.sessions.RevokeAllForUserExcept(txCtx, user.ID, in.SessionID, now); err != nil {
			return internal(err, "吊销其他会话失败")
		}
		if err := s.sessions.UpdateTokenVersion(txCtx, in.SessionID, newVersion); err != nil {
			return internal(err, "保留当前会话失败")
		}
		return nil
	})
}
