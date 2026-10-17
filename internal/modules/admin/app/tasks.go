package app

import (
	"context"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// RetryTask 人工重试失败的后台作业：校验状态、重置为 pending 并写审计，返回最新快照。
func (s *Service) RetryTask(ctx context.Context, actor model.Viewer, jobID int64) (JobSnapshot, error) {
	now := s.now()
	var snapshot JobSnapshot
	err := s.tx.WithTx(ctx, func(txCtx context.Context) error {
		current, err := s.jobs.GetJob(txCtx, jobID)
		if err != nil {
			return err
		}
		if current.Status != contracts.JobStatusFailed {
			return apperr.Validation("只有失败的作业可以重试").WithDetails(map[string]string{
				"status": "当前状态为 " + current.Status,
			})
		}
		updated, err := s.jobs.RetryJob(txCtx, contracts.JobActionInput{JobID: jobID, At: now})
		if err != nil {
			return err
		}
		snapshot = updated
		return s.audit.Write(txCtx, contracts.AuditEntry{
			AdminID:    actor.ID,
			Action:     contracts.AuditActionTaskRetry,
			TargetType: "background_job",
			TargetID:   &jobID,
			Detail: map[string]any{
				"previous_status": current.Status,
				"status":          updated.Status,
			},
			At: now,
		})
	})
	if err != nil {
		return JobSnapshot{}, err
	}
	return snapshot, nil
}

// CancelTask 取消待执行/运行中/暂停的后台作业，并写审计。
func (s *Service) CancelTask(ctx context.Context, actor model.Viewer, jobID int64) (JobSnapshot, error) {
	now := s.now()
	var snapshot JobSnapshot
	err := s.tx.WithTx(ctx, func(txCtx context.Context) error {
		current, err := s.jobs.GetJob(txCtx, jobID)
		if err != nil {
			return err
		}
		switch current.Status {
		case contracts.JobStatusPending, contracts.JobStatusRunning, contracts.JobStatusPaused:
		default:
			return apperr.Validation("该作业当前不能取消").WithDetails(map[string]string{
				"status": "当前状态为 " + current.Status,
			})
		}
		updated, err := s.jobs.CancelJob(txCtx, contracts.JobActionInput{JobID: jobID, At: now})
		if err != nil {
			return err
		}
		snapshot = updated
		return s.audit.Write(txCtx, contracts.AuditEntry{
			AdminID:    actor.ID,
			Action:     contracts.AuditActionTaskCancel,
			TargetType: "background_job",
			TargetID:   &jobID,
			Detail: map[string]any{
				"previous_status": current.Status,
				"status":          updated.Status,
			},
			At: now,
		})
	})
	if err != nil {
		return JobSnapshot{}, err
	}
	return snapshot, nil
}
