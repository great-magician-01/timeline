package app

import (
	"context"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/agent/domain"
	"github.com/great-magician-01/timeline/internal/modules/agent/repo"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
)

// 调度参数默认值（分钟），与 docs/WORKFLOWS.md 记录的默认值一致：
// post_interval_minutes=360、proactive_interval_minutes=60（0 关闭）。
const (
	defaultPostIntervalMinutes      = 360
	defaultProactiveIntervalMinutes = 60
)

// RunScheduler 是 AI 调度周期任务（由组合根注册为每分钟一次）：
// 对每个启用中的 AI：定时原创、主动互动、提及/评论触发扫描，并补投缺失作业。
func (s *Service) RunScheduler(ctx context.Context) {
	if err := s.Reconcile(ctx); err != nil {
		s.deps.Log.Warn("AI 任务 reconcile 失败", "err", err)
	}
	values, err := s.deps.Settings.Settings(ctx)
	if err != nil {
		s.deps.Log.Warn("读取全局 AI 开关失败", "err", err)
		return
	}
	if !values.AIGlobalEnabled {
		return
	}
	aiIDs, err := s.deps.AI.EnabledAIIDs(ctx)
	if err != nil {
		s.deps.Log.Error("读取启用中的 AI 失败", "err", err)
		return
	}
	now := s.now()
	for _, aiID := range aiIDs {
		rt, runtimeErr := s.deps.AI.RuntimeByAI(ctx, aiID)
		if runtimeErr != nil {
			s.deps.Log.Warn("读取 AI 运行时配置失败", "err", runtimeErr, "ai_id", aiID)
			continue
		}
		s.scheduleOriginalPost(ctx, rt, now)
		s.scheduleProactive(ctx, rt, now)
		s.scanMentionTriggers(ctx, rt, now)
		s.scanCommentTriggers(ctx, rt, now)
		// 调度巡检复核记忆来源：来源已删除/转非公开时停用相关记忆，避免继续注入上下文。
		if err := s.recheckSourceMemoriesLimit(ctx, aiID, memoryRefSchedulerBatch); err != nil {
			s.deps.Log.Warn("调度巡检复核来源记忆失败", "err", err, "ai_id", aiID)
		}
	}
}

// scheduleDue 判断某触发类型是否到达调度时间：
// 最近成功完成与最近任务创建都超过 interval 才允许再次投递。
func (s *Service) scheduleDue(ctx context.Context, aiID int64, trigger string, interval int, now time.Time) (bool, error) {
	if interval <= 0 {
		return false, nil
	}
	window := time.Duration(interval) * time.Minute
	last, err := s.deps.Counters.LastSucceededAt(ctx, aiID, trigger)
	if err != nil {
		return false, err
	}
	if last != nil && now.Sub(*last) < window {
		return false, nil
	}
	tasks, _, err := s.deps.Tasks.ListTasks(ctx, repo.TaskFilter{AIID: aiID, TriggerType: trigger, Limit: 1})
	if err != nil {
		return false, err
	}
	if len(tasks) > 0 && now.Sub(tasks[0].CreatedAt) < window {
		return false, nil
	}
	return true, nil
}

func (s *Service) scheduleOriginalPost(ctx context.Context, rt contracts.Runtime, now time.Time) {
	if !domain.ActionAllowed(rt.Behavior, domain.ActionOriginalPost) {
		return
	}
	interval := domain.ScheduleMinutes(rt.Behavior.Schedule, "post_interval_minutes", defaultPostIntervalMinutes)
	if interval <= 0 {
		return
	}
	due, err := s.scheduleDue(ctx, rt.AIID, domain.TriggerSchedule, interval, now)
	if err != nil || !due {
		return
	}
	key := "ai_schedule:post:" + apiID(rt.AIID) + ":" + apiID(scheduleBucket(now, interval))
	if _, _, err := s.Enqueue(ctx, EnqueueInput{
		AID:            rt.AIID,
		TriggerType:    domain.TriggerSchedule,
		TriggerRefType: domain.RefAI,
		TriggerRefID:   &rt.AIID,
		ActionType:     domain.ActionOriginalPost,
		IdempotencyKey: key,
	}); err != nil {
		s.deps.Log.Warn("投递定时原创任务失败", "err", err, "ai_id", rt.AIID)
	}
}

func (s *Service) scheduleProactive(ctx context.Context, rt contracts.Runtime, now time.Time) {
	if !domain.ProactiveAllowed(rt.Behavior) || !domain.ValidCandidateScope(rt.Behavior.CandidateScopeType) {
		return
	}
	interval := domain.ScheduleMinutes(rt.Behavior.Schedule, "proactive_interval_minutes", defaultProactiveIntervalMinutes)
	if interval <= 0 {
		return
	}
	due, err := s.scheduleDue(ctx, rt.AIID, domain.TriggerProactive, interval, now)
	if err != nil || !due {
		return
	}
	key := "ai_schedule:proactive:" + apiID(rt.AIID) + ":" + apiID(scheduleBucket(now, interval))
	if _, _, err := s.Enqueue(ctx, EnqueueInput{
		AID:            rt.AIID,
		TriggerType:    domain.TriggerProactive,
		TriggerRefType: domain.RefAI,
		TriggerRefID:   &rt.AIID,
		ActionType:     domain.ActionNoop,
		IdempotencyKey: key,
	}); err != nil {
		s.deps.Log.Warn("投递主动互动任务失败", "err", err, "ai_id", rt.AIID)
	}
}

// scanMentionTriggers 扫描最近的公开提及并补投任务（幂等键：mention:<id>:<aiID>）。
func (s *Service) scanMentionTriggers(ctx context.Context, rt contracts.Runtime, now time.Time) {
	mentions, err := s.deps.Notices.ListActiveMentions(ctx, contracts.MentionQuery{
		MentionedUserID: rt.AIID,
		Since:           now.Add(-schedulerTriggerWindow),
		Limit:           schedulerTriggerLimit,
	})
	if err != nil {
		s.deps.Log.Warn("扫描提及触发失败", "err", err, "ai_id", rt.AIID)
		return
	}
	for _, mention := range mentions {
		sourceID := mention.SourceID
		if _, _, err := s.Enqueue(ctx, EnqueueInput{
			AID:            rt.AIID,
			TriggerType:    domain.TriggerMention,
			TriggerRefType: mention.SourceType,
			TriggerRefID:   &sourceID,
			ActionType:     domain.ActionReply,
			IdempotencyKey: "mention:" + apiID(mention.ID) + ":" + apiID(rt.AIID),
		}); err != nil {
			s.deps.Log.Warn("投递提及任务失败", "err", err, "ai_id", rt.AIID)
		}
	}
}

// scanCommentTriggers 扫描 AI 公开帖子下的新评论并补投被动回复任务。
func (s *Service) scanCommentTriggers(ctx context.Context, rt contracts.Runtime, now time.Time) {
	comments, err := s.deps.Social.ListNewCommentsOnAuthorPosts(ctx, contracts.CommentTriggerQuery{
		AuthorID: rt.AIID,
		// 排除 AI 自己刚发表的评论：AI 回复用户评论后，自己在自己帖子下的评论
		// 不应再次触发“收到评论”任务（避免自回复循环）。
		ExcludeAuthorID: rt.AIID,
		Since:           now.Add(-schedulerTriggerWindow),
		Limit:           schedulerTriggerLimit,
	})
	if err != nil {
		s.deps.Log.Warn("扫描评论触发失败", "err", err, "ai_id", rt.AIID)
		return
	}
	for _, comment := range comments {
		commentID := comment.ID
		if _, _, err := s.Enqueue(ctx, EnqueueInput{
			AID:            rt.AIID,
			TriggerType:    domain.TriggerComment,
			TriggerRefType: domain.RefComment,
			TriggerRefID:   &commentID,
			ActionType:     domain.ActionReply,
			IdempotencyKey: "comment-trigger:" + apiID(commentID) + ":" + apiID(rt.AIID),
		}); err != nil {
			s.deps.Log.Warn("投递评论回复任务失败", "err", err, "ai_id", rt.AIID)
		}
	}
}

// scheduleBucket 把当前时间按间隔取整为幂等桶。
func scheduleBucket(now time.Time, intervalMinutes int) int64 {
	if intervalMinutes <= 0 {
		return now.Unix()
	}
	return now.Unix() / (int64(intervalMinutes) * 60)
}
