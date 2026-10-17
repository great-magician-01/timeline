package app

import (
	"context"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/auth/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// SetAdminRole 供 HTTP 管理员授权/撤销接口使用：用应用时钟取当前时间。
func (s *Service) SetAdminRole(ctx context.Context, actorID, targetID int64, grant bool) (model.User, error) {
	return s.changeAdminRole(ctx, actorID, targetID, grant, s.now())
}

// ChangeAdminRole 实现 contracts.UserWriter：授予或撤销目标人类账号的管理员角色。
// 超管保护等不变式由 domain/shared.access 判定；角色变更与审计在同一事务内提交；
// 目标角色已是期望值时幂等返回且不写审计。
func (s *Service) ChangeAdminRole(ctx context.Context, in contracts.AdminRoleInput) (model.User, error) {
	return s.changeAdminRole(ctx, in.ActorID, in.TargetID, in.Grant, in.At)
}

func (s *Service) changeAdminRole(
	ctx context.Context, actorID, targetID int64, grant bool, at time.Time,
) (model.User, error) {
	if err := s.requireRepos(); err != nil {
		return model.User{}, err
	}
	if s.tx == nil {
		return model.User{}, apperr.Internal("auth 事务管理器未初始化")
	}
	if actorID <= 0 || targetID <= 0 {
		return model.User{}, apperr.Invalid("无效资源 ID")
	}

	var updated model.User
	err := s.tx.WithTx(ctx, func(txCtx context.Context) error {
		target, err := s.users.ByIDForUpdate(txCtx, targetID)
		if err != nil {
			return err
		}
		// 事务内复核操作者：中间件之外的双保险，避免权限在请求处理期间变化。
		actor, err := s.users.ByID(txCtx, actorID)
		if err != nil {
			if isNotFound(err) {
				return apperr.Forbidden("需要超级管理员权限")
			}
			return internal(err, "复核操作者权限失败")
		}
		if actor.IsAI || actor.Role != domain.RoleAdmin || !actor.IsSuperAdmin {
			return apperr.Forbidden("需要超级管理员权限")
		}

		var ruleErr error
		if grant {
			ruleErr = domain.CanGrantAdminRole(actor, target)
		} else {
			ruleErr = domain.CanChangeAdminRole(actor, target)
		}
		if ruleErr != nil {
			return ruleErr
		}

		desiredRole := domain.RoleUser
		action := contracts.AuditActionUserRevokeAdmin
		if grant {
			desiredRole = domain.RoleAdmin
			action = contracts.AuditActionUserGrantAdmin
		}
		updated = target
		if target.Role == desiredRole {
			// 幂等：已经是期望角色，直接返回当前账号且不写审计。
			return nil
		}
		updated, err = s.users.UpdateRole(txCtx, targetID, desiredRole, at)
		if err != nil {
			return internal(err, "更新管理员角色失败")
		}
		if s.audit != nil {
			auditTargetID := targetID
			if err := s.audit.Write(txCtx, contracts.AuditEntry{
				AdminID:    actor.ID,
				Action:     action,
				TargetType: "user",
				TargetID:   &auditTargetID,
				Detail: map[string]string{
					"previous_role": target.Role,
					"new_role":      desiredRole,
				},
				At: at,
			}); err != nil {
				return internal(err, "写入管理员角色审计失败")
			}
		}
		return nil
	})
	if err != nil {
		return model.User{}, err
	}
	return updated, nil
}
