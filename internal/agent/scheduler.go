package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/great-magician-01/timeline/internal/access"
	"github.com/great-magician-01/timeline/internal/ai"
	"github.com/great-magician-01/timeline/internal/api"
)

// 调度约定（docs/WORKFLOWS.md 7.1）。
const (
	// defaultPostIntervalMinutes 是未配置 schedule.post_interval_minutes 时的定时原创间隔。
	defaultPostIntervalMinutes = 360
	// defaultProactiveIntervalMinutes 是未配置 schedule.proactive_interval_minutes 时的主动互动间隔。
	defaultProactiveIntervalMinutes = 60
	// triggerScanWindow 是提及与评论触发的扫描窗口。
	triggerScanWindow = 10 * time.Minute
	// triggerScanLimit 是单轮扫描处理的触发数量上限。
	triggerScanLimit = 20
)

// RunScheduler 是每 1 分钟执行一次的周期任务：
// 检查启用中的 AI 是否到期需要定时原创或主动互动，并补扫提及、评论触发的任务，
// 同时复核直接引用来源的记忆是否仍然有效。
func (s *Service) RunScheduler(ctx context.Context) {
	pool := s.pools.Pool()
	if pool == nil {
		return
	}
	// 对账先于开关判断：被取消的任务同样需要留痕，且对账本身不调用模型。
	if err := s.reconcilePendingTasks(ctx); err != nil {
		s.log.Error("对账游离的 AI 任务失败", "err", err)
	}

	enabled, err := s.globalAIEnabled(ctx, pool)
	if err != nil {
		s.log.Error("读取全局 AI 开关失败", "err", err)
		return
	}
	if !enabled {
		return
	}
	ids, err := s.ai.EnabledAIIDs(ctx, pool)
	if err != nil {
		s.log.Error("读取启用中的 AI 失败", "err", err)
		return
	}
	now := time.Now()
	for _, aiID := range ids {
		rt, err := s.ai.RuntimeByAI(ctx, pool, aiID)
		if err != nil {
			if !isNotFound(err) {
				s.log.Error("读取 AI 运行时配置失败", "err", err, "ai_id", aiID)
			}
			continue
		}
		if rt.Provider.ID == 0 || !rt.Provider.IsEnabled {
			continue
		}
		if !withinActiveHours(rt.Behavior.ActiveHoursStart, rt.Behavior.ActiveHoursEnd, now.Hour()) {
			continue
		}
		s.scheduleOriginalPost(ctx, pool, rt, now)
		s.scheduleProactive(ctx, pool, rt, now)
		if rt.Behavior.AllowPassiveReply {
			s.scanMentionTriggers(ctx, pool, rt, now)
			s.scanCommentTriggers(ctx, pool, rt, now)
		}
		if err := s.recheckSourceMemoriesLimit(ctx, aiID, memoryRefSchedulerBatch); err != nil {
			s.log.Warn("调度巡检复核来源记忆失败", "err", err, "ai_id", aiID)
		}
	}
}

// scheduleOriginalPost 按 schedule.post_interval_minutes 投递定时原创任务。
func (s *Service) scheduleOriginalPost(
	ctx context.Context,
	q access.Querier,
	rt ai.Runtime,
	now time.Time,
) {
	if !actionAllowed(rt.Behavior, actionOriginalPost) {
		return
	}
	interval := scheduleMinutes(rt.Behavior.Schedule, "post_interval_minutes", defaultPostIntervalMinutes)
	if interval <= 0 {
		return
	}
	due, err := s.scheduleDue(ctx, q, rt.AIID, triggerSchedule, actionOriginalPost, now, interval)
	if err != nil {
		s.log.Warn("判断定时原创是否到期失败", "err", err, "ai_id", rt.AIID)
		return
	}
	if !due {
		return
	}
	key := scheduleKey(rt.AIID, actionOriginalPost, now, interval)
	if _, reason, err := s.Enqueue(ctx, q, EnqueueInput{
		AID:            rt.AIID,
		TriggerType:    triggerSchedule,
		TriggerRefType: "ai",
		TriggerRefID:   &rt.AIID,
		ActionType:     actionOriginalPost,
		IdempotencyKey: key,
	}); err != nil {
		s.log.Error("投递定时原创任务失败", "err", err, "ai_id", rt.AIID)
	} else if reason != "" {
		s.log.Info("定时原创任务未入队", "ai_id", rt.AIID, "reason", reason)
	}
}

// scheduleProactive 按 schedule.proactive_interval_minutes 投递主动互动任务。
func (s *Service) scheduleProactive(
	ctx context.Context,
	q access.Querier,
	rt ai.Runtime,
	now time.Time,
) {
	if !proactiveAllowed(rt.Behavior) || !validCandidateScope(rt.Behavior.CandidateScopeType) {
		return
	}
	interval := scheduleMinutes(rt.Behavior.Schedule, "proactive_interval_minutes", defaultProactiveIntervalMinutes)
	if interval <= 0 {
		return
	}
	due, err := s.scheduleDue(ctx, q, rt.AIID, triggerProactive, "", now, interval)
	if err != nil {
		s.log.Warn("判断主动互动是否到期失败", "err", err, "ai_id", rt.AIID)
		return
	}
	if !due {
		return
	}
	key := scheduleKey(rt.AIID, triggerProactive, now, interval)
	if _, reason, err := s.Enqueue(ctx, q, EnqueueInput{
		AID:            rt.AIID,
		TriggerType:    triggerProactive,
		TriggerRefType: "ai",
		TriggerRefID:   &rt.AIID,
		ActionType:     actionNoop,
		IdempotencyKey: key,
	}); err != nil {
		s.log.Error("投递主动互动任务失败", "err", err, "ai_id", rt.AIID)
	} else if reason != "" {
		s.log.Info("主动互动任务未入队", "ai_id", rt.AIID, "reason", reason)
	}
}

// scanMentionTriggers 扫描最近的公开提及并补投入队（content/social 未直接调用时的兜底）。
func (s *Service) scanMentionTriggers(
	ctx context.Context,
	q access.Querier,
	rt ai.Runtime,
	now time.Time,
) {
	rows, err := q.Query(ctx, `
		SELECT id FROM mentions
		WHERE mentioned_user_id = $1 AND is_active AND created_at > $2
		ORDER BY id DESC LIMIT $3`, rt.AIID, now.Add(-triggerScanWindow), triggerScanLimit)
	if err != nil {
		s.log.Warn("扫描提及触发失败", "err", err, "ai_id", rt.AIID)
		return
	}
	ids := make([]int64, 0, triggerScanLimit)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			s.log.Warn("读取提及触发失败", "err", err, "ai_id", rt.AIID)
			return
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		s.log.Warn("遍历提及触发失败", "err", err, "ai_id", rt.AIID)
		return
	}
	for _, mentionID := range ids {
		if _, reason, err := s.EnqueueMentionTask(ctx, q, mentionID, rt.AIID); err != nil {
			s.log.Error("投递提及任务失败", "err", err, "ai_id", rt.AIID, "mention_id", mentionID)
		} else if reason != "" {
			s.log.Info("提及任务未入队", "ai_id", rt.AIID, "mention_id", mentionID, "reason", reason)
		}
	}
}

// scanCommentTriggers 扫描 AI 公开帖子下的新评论并补投入队。
func (s *Service) scanCommentTriggers(
	ctx context.Context,
	q access.Querier,
	rt ai.Runtime,
	now time.Time,
) {
	rows, err := q.Query(ctx, `
		SELECT c.id FROM comments c
		JOIN posts p ON p.id = c.post_id
		WHERE p.author_id = $1 AND p.status = 'published' AND p.visibility = 'public'
		  AND c.status = 'published' AND c.author_id <> $1 AND c.created_at > $2
		ORDER BY c.id DESC LIMIT $3`, rt.AIID, now.Add(-triggerScanWindow), triggerScanLimit)
	if err != nil {
		s.log.Warn("扫描评论触发失败", "err", err, "ai_id", rt.AIID)
		return
	}
	ids := make([]int64, 0, triggerScanLimit)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			s.log.Warn("读取评论触发失败", "err", err, "ai_id", rt.AIID)
			return
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		s.log.Warn("遍历评论触发失败", "err", err, "ai_id", rt.AIID)
		return
	}
	for _, commentID := range ids {
		if _, reason, err := s.EnqueueCommentTask(ctx, q, commentID, rt.AIID); err != nil {
			s.log.Error("投递评论任务失败", "err", err, "ai_id", rt.AIID, "comment_id", commentID)
		} else if reason != "" {
			s.log.Info("评论任务未入队", "ai_id", rt.AIID, "comment_id", commentID, "reason", reason)
		}
	}
}

// EnqueueMentionTask 投递“被提及”触发的决策任务（幂等键：mention:<mentionID>:<aiID>）。
// 供 content/social 在发布提及后直接调用；不存在、已失效或非公开来源都会被丢弃。
func (s *Service) EnqueueMentionTask(
	ctx context.Context,
	q access.Querier,
	mentionID, aiID int64,
) (int64, string, error) {
	if mentionID <= 0 || aiID <= 0 {
		return 0, "提及参数无效", nil
	}
	var (
		sourceType string
		sourceID   int64
	)
	err := q.QueryRow(ctx, `
		SELECT source_type, source_id FROM mentions
		WHERE id = $1 AND mentioned_user_id = $2 AND is_active`, mentionID, aiID).
		Scan(&sourceType, &sourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "提及不存在或已失效", nil
	}
	if err != nil {
		return 0, "", err
	}
	if sourceType != sourcePost && sourceType != sourceComment {
		return 0, "提及来源类型无效", nil
	}
	return s.Enqueue(ctx, q, EnqueueInput{
		AID:            aiID,
		TriggerType:    triggerMention,
		TriggerRefType: sourceType,
		TriggerRefID:   &sourceID,
		ActionType:     actionReply,
		IdempotencyKey: mentionKey(mentionID, aiID),
		Source:         &SourceRef{Type: sourceType, ID: sourceID},
	})
}

// EnqueueCommentTask 投递“自己的公开帖子收到评论”触发的决策任务
// （幂等键：comment-trigger:<commentID>:<aiID>）。
func (s *Service) EnqueueCommentTask(
	ctx context.Context,
	q access.Querier,
	commentID, aiID int64,
) (int64, string, error) {
	if commentID <= 0 || aiID <= 0 {
		return 0, "评论参数无效", nil
	}
	var (
		postID   int64
		authorID int64
	)
	err := q.QueryRow(ctx, `
		SELECT post_id, author_id FROM comments
		WHERE id = $1 AND status = 'published'`, commentID).Scan(&postID, &authorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "评论不存在或已删除", nil
	}
	if err != nil {
		return 0, "", err
	}
	if authorID == aiID {
		return 0, "自己的评论不触发回复决策", nil
	}
	var postAuthor int64
	err = q.QueryRow(ctx, `
		SELECT author_id FROM posts
		WHERE id = $1 AND status = 'published' AND visibility = 'public'`, postID).Scan(&postAuthor)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "评论所在内容不是公开内容", nil
	}
	if err != nil {
		return 0, "", err
	}
	if postAuthor != aiID {
		// 只有自己的公开内容收到评论才触发被动回复。
		return 0, "评论不属于该 AI 的公开内容", nil
	}
	return s.Enqueue(ctx, q, EnqueueInput{
		AID:            aiID,
		TriggerType:    triggerComment,
		TriggerRefType: sourceComment,
		TriggerRefID:   &commentID,
		ActionType:     actionReply,
		IdempotencyKey: commentTriggerKey(commentID, aiID),
		Source:         &SourceRef{Type: sourceComment, ID: commentID},
	})
}

// ── 纯函数：调度参数与幂等键 ──

// scheduleMinutes 读取 schedule JSON 中的分钟间隔配置；缺失或非法时使用默认值。
// 配置为 0 表示关闭该项调度。
func scheduleMinutes(raw json.RawMessage, key string, fallback int) int {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return fallback
	}
	values := map[string]any{}
	if err := json.Unmarshal([]byte(trimmed), &values); err != nil {
		return fallback
	}
	value, ok := values[key]
	if !ok {
		return fallback
	}
	minutes := 0
	switch typed := value.(type) {
	case float64:
		minutes = int(typed)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err != nil {
			return fallback
		}
		minutes = parsed
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return fallback
		}
		minutes = int(parsed)
	default:
		return fallback
	}
	if minutes < 0 {
		return fallback
	}
	return minutes
}

// scheduleKey 生成调度任务的幂等键：按时间桶截断，保证同一窗口不重复入队。
func scheduleKey(aiID int64, kind string, now time.Time, intervalMinutes int) string {
	bucket := scheduleBucket(now, intervalMinutes)
	return fmt.Sprintf("schedule:%s:%s:%s", api.FormatID(aiID), kind, api.FormatID(bucket))
}

// scheduleBucket 返回当前时间所在的时间桶（Unix 秒）。
func scheduleBucket(now time.Time, intervalMinutes int) int64 {
	if intervalMinutes <= 0 {
		return now.Unix()
	}
	interval := time.Duration(intervalMinutes) * time.Minute
	return now.Truncate(interval).Unix()
}

// mentionKey / commentTriggerKey 生成触发类任务的幂等键。
func mentionKey(mentionID, aiID int64) string {
	return fmt.Sprintf("mention:%s:%s", api.FormatID(mentionID), api.FormatID(aiID))
}

func commentTriggerKey(commentID, aiID int64) string {
	return fmt.Sprintf("comment-trigger:%s:%s", api.FormatID(commentID), api.FormatID(aiID))
}

// scheduleDue 判断距离上次同类触发是否已超过间隔（无历史记录时视为到期）。
func (s *Service) scheduleDue(
	ctx context.Context,
	q access.Querier,
	aiID int64,
	triggerType, actionType string,
	now time.Time,
	intervalMinutes int,
) (bool, error) {
	sql := `SELECT MAX(created_at) FROM ai_tasks WHERE ai_id = $1 AND trigger_type = $2`
	args := []any{aiID, triggerType}
	if strings.TrimSpace(actionType) != "" {
		sql += ` AND action_type = $3`
		args = append(args, actionType)
	}
	var last *time.Time
	if err := q.QueryRow(ctx, sql, args...).Scan(&last); err != nil {
		return false, err
	}
	if last == nil {
		return true, nil
	}
	interval := time.Duration(intervalMinutes) * time.Minute
	return !now.Before(last.Add(interval)), nil
}
