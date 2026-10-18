package app

import (
	"context"

	"github.com/great-magician-01/timeline/internal/modules/auth/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// RegisterInput 是注册用例输入；IP 由 http 层传入，用于按 IP 限频。
type RegisterInput struct {
	Username string
	Email    string
	Password string
	IP       string
}

// Register 注册账号：IP 限频 → 校验 → bcrypt（事务外）→ WithTx 内 advisory lock、
// 人类用户计数与 INSERT。空库首个注册用户成为唯一超级管理员（role=admin 且
// is_super_admin=true），且此时忽略注册开关（初始化安全窗口）。
func (s *Service) Register(ctx context.Context, in RegisterInput) (model.User, error) {
	if err := s.requireRepos(); err != nil {
		return model.User{}, err
	}
	if s.tx == nil || s.locks == nil || s.ids == nil {
		return model.User{}, apperr.Internal("auth 注册依赖未初始化")
	}
	if ok, retry := s.registerLimiter.Consume("ip:" + in.IP); !ok {
		return model.User{}, rateLimited(retry)
	}

	normalized, details := domain.NormalizeRegistration(domain.RegisterInput{
		Username: in.Username,
		Email:    in.Email,
		Password: in.Password,
	})
	if details != nil {
		return model.User{}, apperr.Validation("请检查注册信息").WithDetails(details)
	}

	settings, err := s.settingsSnapshot(ctx)
	if err != nil {
		return model.User{}, internal(err, "读取注册开关失败")
	}
	// 快速拒绝路径：只有注册关闭时才需要读取初始化状态（EXISTS 短路）。
	// 注册开启（默认）时跳过该查询：空库/非空库都会继续走事务，由事务内 EXISTS 复核。
	if !settings.RegistrationEnabled {
		bootstrap, err := s.BootstrapRequired(ctx)
		if err != nil {
			return model.User{}, err
		}
		if !bootstrap {
			return model.User{}, apperr.Forbidden("当前未开放注册")
		}
	}

	// bcrypt 成本较高，放在事务外执行，锁内只做计数、INSERT 与开关复核。
	passwordHash, err := domain.HashPassword(normalized.Password)
	if err != nil {
		return model.User{}, apperr.Validation("密码不符合要求").
			WithDetails(map[string]string{"password": err.Error()})
	}
	userID, err := s.ids.Next()
	if err != nil {
		return model.User{}, internal(err, "生成用户 ID 失败")
	}

	var created model.User
	err = s.tx.WithTx(ctx, func(txCtx context.Context) error {
		if err := s.locks.AcquireBootstrapLock(txCtx); err != nil {
			return internal(err, "获取注册初始化锁失败")
		}
		hasHuman, err := s.users.HasHumanUser(txCtx)
		if err != nil {
			return internal(err, "读取初始化状态失败")
		}
		bootstrapTx := !hasHuman
		if !bootstrapTx && !settings.RegistrationEnabled {
			return apperr.Forbidden("当前未开放注册")
		}

		role, isSuperAdmin := domain.RoleUser, false
		if bootstrapTx {
			role, isSuperAdmin = domain.BootstrapRole()
		}
		if err := domain.ValidateSuperAdminInvariant(role, isSuperAdmin, false); err != nil {
			return err
		}
		now := s.now()
		created = model.User{
			ID:           userID,
			Username:     normalized.Username,
			Email:        normalized.Email,
			PasswordHash: passwordHash,
			Nickname:     normalized.Username,
			Role:         role,
			IsSuperAdmin: isSuperAdmin,
			Status:       domain.StatusActive,
			TokenVersion: 1,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		if err := s.users.Insert(txCtx, created); err != nil {
			return err
		}
		if bootstrapTx {
			s.log.Info("系统首个注册用户已创建为超级管理员", "user_id", created.ID)
		}
		return nil
	})
	if err != nil {
		return model.User{}, err
	}
	return created, nil
}
