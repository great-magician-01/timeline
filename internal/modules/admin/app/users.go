package app

import (
	"context"
	"time"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/access"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// SuspendUser 停用账号：auth 端口更新状态并吊销会话，编排暂停其待执行作业；
// 停用 AI 账号时额外暂停 AI 作业并取消待执行 AI 任务。审计与业务同事务。
func (s *Service) SuspendUser(ctx context.Context, actor model.Viewer, targetID int64) (model.User, error) {
	return s.setUserStatus(ctx, actor, targetID, true)
}

// UnsuspendUser 启用账号；不自动补发被暂停的定时发布作业（与旧行为一致）。
func (s *Service) UnsuspendUser(ctx context.Context, actor model.Viewer, targetID int64) (model.User, error) {
	return s.setUserStatus(ctx, actor, targetID, false)
}

func (s *Service) setUserStatus(ctx context.Context, actor model.Viewer, targetID int64, suspend bool) (model.User, error) {
	if suspend && actor.ID == targetID {
		return model.User{}, apperr.Validation("不能停用自己的管理员账号")
	}
	now := s.now()
	var updated model.User
	err := s.tx.WithTx(ctx, func(txCtx context.Context) error {
		target, err := s.auth.GetUser(txCtx, targetID)
		if err != nil {
			return err
		}
		if err := access.CanModerateUser(actor, target); err != nil {
			return err
		}
		// 超管账号只能由超管本人处置；auth 端口会在事务内再次复核。
		updated, err = s.auth.SetUserStatus(txCtx, contracts.UserStatusInput{
			ActorID:  actor.ID,
			TargetID: targetID,
			Suspend:  suspend,
			At:       now,
		})
		if err != nil {
			return err
		}
		if suspend {
			if err := s.pauseUserPendingJobs(txCtx, actor.ID, target, now); err != nil {
				return err
			}
		}
		action := contracts.AuditActionUserUnsuspend
		if suspend {
			action = contracts.AuditActionUserSuspend
		}
		return s.audit.Write(txCtx, contracts.AuditEntry{
			AdminID:    actor.ID,
			Action:     action,
			TargetType: "user",
			TargetID:   &targetID,
			Detail: map[string]any{
				"previous_status": target.Status,
				"changed":         target.Status != updated.Status,
			},
			At: now,
		})
	})
	if err != nil {
		return model.User{}, err
	}
	return updated, nil
}

// pauseUserPendingJobs 暂停/取消停用账号名下的待执行作业：
//   - 该账号帖子的定时发布作业置为 paused（逻辑状态，不删除、不自动补发）；
//   - AI 账号：AI 相关作业置为 paused，并取消待执行 AI 任务。
func (s *Service) pauseUserPendingJobs(ctx context.Context, actorID int64, target model.User, at time.Time) error {
	if s.content != nil && s.jobs != nil {
		postIDs, err := s.content.ListPostIDsByAuthor(ctx, target.ID)
		if err != nil {
			return err
		}
		if len(postIDs) > 0 {
			if _, err := s.jobs.PausePending(ctx, contracts.PausePendingJobsInput{
				JobType: contracts.JobTypePostPublish,
				RefType: "post",
				RefIDs:  postIDs,
				At:      at,
			}); err != nil {
				return err
			}
		}
	}
	if !target.IsAI {
		return nil
	}
	if s.jobs != nil {
		for _, jobType := range []string{
			contracts.JobTypeAITask,
			contracts.JobTypeAISchedule,
			contracts.JobTypeMemoryConsolidate,
		} {
			if _, err := s.jobs.PausePending(ctx, contracts.PausePendingJobsInput{
				JobType: jobType,
				RefType: "ai",
				RefIDs:  []int64{target.ID},
				At:      at,
			}); err != nil {
				return err
			}
		}
	}
	if s.agent != nil {
		if _, err := s.agent.CancelPendingTasksForActor(ctx, contracts.CancelTasksInput{
			ActorID: actorID,
			AIID:    target.ID,
			At:      at,
		}); err != nil {
			return err
		}
	}
	return nil
}

// ResetPassword 管理员重置密码：密码校验、哈希、会话吊销与 token_version 递增由 auth 实现。
func (s *Service) ResetPassword(ctx context.Context, actor model.Viewer, targetID int64, newPassword string) error {
	now := s.now()
	return s.tx.WithTx(ctx, func(txCtx context.Context) error {
		target, err := s.auth.GetUser(txCtx, targetID)
		if err != nil {
			return err
		}
		if err := access.CanModerateUser(actor, target); err != nil {
			return err
		}
		if target.IsAI {
			return apperr.Validation("AI 账号没有密码，不能重置")
		}
		if err := s.auth.ResetPassword(txCtx, contracts.ResetPasswordInput{
			ActorID:     actor.ID,
			TargetID:    targetID,
			NewPassword: newPassword,
			At:          now,
		}); err != nil {
			return err
		}
		return s.audit.Write(txCtx, contracts.AuditEntry{
			AdminID:    actor.ID,
			Action:     contracts.AuditActionUserResetPwd,
			TargetType: "user",
			TargetID:   &targetID,
			Detail:     map[string]any{},
			At:         now,
		})
	})
}
