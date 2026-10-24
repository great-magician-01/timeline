package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/agent/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
)

// 群聊任务参数：上下文消息条数与自主发言的调度默认值。
const (
	groupContextMessages        = 20
	defaultGroupIntervalMinutes = 60
)

// OnGroupMention 实现 contracts.GroupAITrigger：群消息 @提及 成员 AI 时投递被动
// 回复任务。实现方保证幂等（重复信号不重复投递）；AI 未启用被动回复/群聊发言、
// 非本群成员、账号停用或全局关闭时不产生任务。
func (s *Service) OnGroupMention(ctx context.Context, signal contracts.GroupMentionSignal) error {
	if signal.AIID <= 0 || signal.GroupID <= 0 || signal.MessageID <= 0 {
		return apperr.Validation("群提及信号不完整")
	}
	values, err := s.deps.Settings.Settings(ctx)
	if err != nil {
		return err
	}
	if !values.AIGlobalEnabled {
		return nil
	}
	rt, err := s.deps.AI.RuntimeByAI(ctx, signal.AIID)
	if err != nil {
		if isNotFound(err) {
			return nil
		}
		return err
	}
	if !rt.Profile.IsEnabled {
		return nil
	}
	// 被 @ 回复属于被动行为：需同时开启 allow_passive_reply 与 allow_group_message。
	if !domain.ActionAllowedForTrigger(rt.Behavior, domain.TriggerGroupMention, domain.ActionGroupMessage) {
		return nil
	}
	member, err := s.deps.Groups.IsMemberActive(ctx, signal.GroupID, signal.AIID)
	if err != nil {
		return err
	}
	if !member {
		return nil
	}
	groupID := signal.GroupID
	_, _, err = s.Enqueue(ctx, EnqueueInput{
		AID:            signal.AIID,
		TriggerType:    domain.TriggerGroupMention,
		TriggerRefType: domain.RefGroup,
		TriggerRefID:   &groupID,
		ActionType:     domain.ActionGroupMessage,
		IdempotencyKey: "group_mention:" + apiID(signal.MessageID) + ":" + apiID(signal.AIID),
	})
	return err
}

// scheduleGroupMessage 为开启群聊自主发言的 AI 投递定时群发言任务：
// 每个间隔桶（group_message_interval_minutes，默认 60、0 关闭）至多投递一个群，
// 按桶序号在所在群之间轮转选择，保证多群不被固定首个群饿死；
// scheduleDue 以 AI 为维度挡住桶内重复投递（幂等键含桶序号双保险）。
func (s *Service) scheduleGroupMessage(ctx context.Context, rt contracts.Runtime, now time.Time) {
	if !domain.ActionAllowed(rt.Behavior, domain.ActionGroupMessage) {
		return
	}
	interval := domain.ScheduleMinutes(rt.Behavior.Schedule, "group_message_interval_minutes", defaultGroupIntervalMinutes)
	if interval <= 0 {
		return
	}
	groupIDs, err := s.deps.Groups.ListActiveGroupIDs(ctx, rt.AIID)
	if err != nil {
		s.deps.Log.Warn("读取 AI 群成员列表失败", "err", err, "ai_id", rt.AIID)
		return
	}
	if len(groupIDs) == 0 {
		return
	}
	due, err := s.scheduleDue(ctx, rt.AIID, domain.TriggerGroupSchedule, interval, now)
	if err != nil || !due {
		return
	}
	// 桶序号轮转：同一桶内多群只取一个，跨桶依次覆盖不同群。
	bucket := scheduleBucket(now, interval)
	groupID := groupIDs[int(bucket)%len(groupIDs)]
	key := "ai_group_schedule:" + apiID(rt.AIID) + ":" + apiID(bucket)
	if _, _, err := s.Enqueue(ctx, EnqueueInput{
		AID:            rt.AIID,
		TriggerType:    domain.TriggerGroupSchedule,
		TriggerRefType: domain.RefGroup,
		TriggerRefID:   &groupID,
		ActionType:     domain.ActionGroupMessage,
		IdempotencyKey: key,
	}); err != nil {
		s.deps.Log.Warn("投递群聊自主发言任务失败", "err", err, "ai_id", rt.AIID, "group_id", groupID)
	}
}

// groupPromptContext 是注入决策上下文的群聊视图。
type groupPromptContext struct {
	ID       int64
	Name     string
	Messages []contracts.GroupContextMessage
}

// buildGroupPrompt 组装群聊任务的上下文：群名 + 最近消息（任务执行时重新授权，
// AI 已不是成员或群已解散时按来源不可见拒绝）。
func (s *Service) buildGroupPrompt(ctx context.Context, groupID int64) (groupPromptContext, error) {
	snapshot, err := s.deps.Groups.Snapshot(ctx, groupID, 0, groupContextMessages)
	if err != nil {
		return groupPromptContext{}, err
	}
	if !snapshot.Active {
		return groupPromptContext{}, apperr.NotFound("群聊已解散")
	}
	return groupPromptContext{ID: snapshot.GroupID, Name: snapshot.Name, Messages: snapshot.RecentMessages}, nil
}

// groupPromptSection 渲染群聊上下文（assemblePrompts 的群聊分支）。
func groupPromptSection(group groupPromptContext) string {
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("【群聊「%s」的最近消息】（你在本群发言，输出 group_message 动作与正文）\n", group.Name))
	for _, message := range group.Messages {
		builder.WriteString("- " + truncateRunes(message.PlainText, 200) + "\n")
	}
	return builder.String()
}
