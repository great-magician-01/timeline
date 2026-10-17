package app

import (
	"context"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/content/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/jobs"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// jobStatusSetter 是 platform/jobs 适配器可选提供的能力：作业处理器显式写入终态。
// contracts.JobsQueue 暂无该入口；缺失时处理器退化为由运行器按返回值决定状态。
type jobStatusSetter interface {
	SetJobStatus(ctx context.Context, jobID int64, status string, at time.Time) error
}

// HandlePublishJob 执行定时发布作业（contracts.JobTypePostPublish）。
// 重复执行、帖子已被取消或已发布时都视为处理成功；作者停用时把作业置为 paused，
// 计划时间已被作者改掉的过期作业置为 canceled 并返回 jobs.ErrHandled。
func (s *Service) HandlePublishJob(ctx context.Context, job jobs.Job) error {
	if job.RefID == nil {
		return nil
	}
	postID := *job.RefID
	now := s.Now()
	handled := false
	err := s.withTx(ctx, func(txCtx context.Context) error {
		post, err := s.repo.GetPostForUpdate(txCtx, postID)
		if err != nil {
			if apperr.IsKind(err, apperr.KindNotFound) {
				// 帖子行不存在：重试没有意义，视为已处理。
				return nil
			}
			return err
		}
		if post.Status != model.PostStatusScheduled {
			// 已发布、已取消或已删除：不再发布，幂等成功。
			return nil
		}
		// 作者在作业被领取之后又修改了发布时间（或重新入队了新作业）时，本作业已过期。
		if post.ScheduledAt == nil || !domain.SameScheduleTime(job.RunAt, *post.ScheduledAt) {
			written, err := s.setJobStatus(txCtx, job.ID, contracts.JobStatusCanceled, now)
			if err != nil {
				return err
			}
			handled = written
			s.logInfo("定时发布作业的计划时间已变更，取消过期作业", "job_id", job.ID, "post_id", postID)
			return nil
		}
		author, err := s.loadUser(txCtx, post.AuthorID, "作者账号不存在")
		if err != nil {
			return err
		}
		if author.IsSuspended() {
			written, err := s.setJobStatus(txCtx, job.ID, contracts.JobStatusPaused, now)
			if err != nil {
				return err
			}
			handled = written
			s.logInfo("作者账号已停用，已暂停定时发布作业", "job_id", job.ID, "post_id", postID)
			return nil
		}
		if err := s.publishLocked(txCtx, postID, post.AuthorID); err != nil {
			return err
		}
		s.logInfo("定时发布完成", "job_id", job.ID, "post_id", postID)
		return nil
	})
	if err != nil {
		return err
	}
	if handled {
		return jobs.ErrHandled
	}
	return nil
}

// setJobStatus 写作业终态，返回是否成功写入（适配器不支持时返回 false）。
func (s *Service) setJobStatus(
	ctx context.Context,
	jobID int64,
	status string,
	at time.Time,
) (bool, error) {
	setter, ok := s.deps.Jobs.(jobStatusSetter)
	if !ok || setter == nil {
		return false, nil
	}
	if err := setter.SetJobStatus(ctx, jobID, status, at); err != nil {
		return false, s.wrap(err, "写入作业状态失败")
	}
	return true, nil
}

// logInfo 在日志器可用时输出结构化日志。
func (s *Service) logInfo(message string, args ...any) {
	if s.deps.Log == nil {
		return
	}
	s.deps.Log.Info(message, args...)
}
