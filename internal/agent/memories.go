package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/great-magician-01/timeline/internal/access"
	"github.com/great-magician-01/timeline/internal/ai"
	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/jobs"
)

// 记忆写入与沉淀的约定（docs/WORKFLOWS.md 第 8 章）。
const (
	// maxMemoryContentRunes 与 internal/ai 的记忆校验保持一致。
	maxMemoryContentRunes = 1000
	maxMemoryKeywords     = 20
	maxMemoryKeywordRunes = 32
	// longTermMemoryRunes 是长期记忆正文的期望长度上限（超出即截断）。
	longTermMemoryRunes = 300
	// recentMemoryThreshold 是触发长期记忆沉淀的近期记忆条数。
	recentMemoryThreshold = 40
	// recentMemoryRetention 是沉淀后保留的近期记忆条数上限。
	recentMemoryRetention = 60
	// consolidationBatch 是单次沉淀读取的近期记忆条数。
	consolidationBatch = 40
	// maxLongTermPerConsolidation 是单次沉淀最多写入的长期记忆条数。
	maxLongTermPerConsolidation = 10
	// memorySourceAction / memorySourceConsolidation 是记忆来源类型。
	memorySourceAction        = "action"
	memorySourceConsolidation = "consolidation"
	// memoryRefBatch / memoryRefSchedulerBatch 是来源复核的扫描批量。
	memoryRefBatch          = 50
	memoryRefSchedulerBatch = 200
)

// memoryCandidate 是归一化后的记忆正文与元数据。
type memoryCandidate struct {
	Content        string
	Category       string
	Keywords       []string
	Importance     int
	InferenceLevel string
}

// longTermCandidate 是归一化后的长期记忆候选。
type longTermCandidate struct {
	memoryCandidate
	Supersedes []int64
}

// normalizeMemoryCandidate 校验并归一化模型返回的记忆内容：
// 分类、推断级别取枚举，关键词去重并限制数量与单条长度，重要性裁剪到 1-5。
func normalizeMemoryCandidate(
	content, category string,
	keywords []string,
	importance int,
	inferenceLevel string,
	maxRunes int,
) (memoryCandidate, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return memoryCandidate{}, errEmptyMemory
	}
	if maxRunes > 0 {
		trimmed = truncateRunes(trimmed, maxRunes)
	}
	if utf8.RuneCountInString(trimmed) > maxMemoryContentRunes {
		trimmed = truncateRunes(trimmed, maxMemoryContentRunes)
	}

	finalCategory := strings.TrimSpace(category)
	if !validMemoryCategory(finalCategory) {
		finalCategory = "event"
	}
	normalized := make([]string, 0, len(keywords))
	seen := map[string]bool{}
	for _, keyword := range keywords {
		term := strings.TrimSpace(keyword)
		if term == "" || utf8.RuneCountInString(term) > maxMemoryKeywordRunes {
			continue
		}
		key := strings.ToLower(term)
		if seen[key] {
			continue
		}
		seen[key] = true
		normalized = append(normalized, term)
		if len(normalized) >= maxMemoryKeywords {
			break
		}
	}
	finalImportance := importance
	if finalImportance < 1 || finalImportance > 5 {
		finalImportance = 3
	}
	finalInference := strings.TrimSpace(inferenceLevel)
	if !validInferenceLevel(finalInference) {
		finalInference = "fact"
	}
	return memoryCandidate{
		Content:        trimmed,
		Category:       finalCategory,
		Keywords:       normalized,
		Importance:     finalImportance,
		InferenceLevel: finalInference,
	}, nil
}

// errEmptyMemory 表示模型没有给出可保存的记忆内容。
var errEmptyMemory = errors.New("记忆正文为空")

// normalizeMemoryDraft 归一化近期记忆候选。
func normalizeMemoryDraft(draft MemoryDraft) (memoryCandidate, error) {
	return normalizeMemoryCandidate(draft.Content, draft.Category, draft.Keywords,
		draft.Importance, draft.InferenceLevel, 0)
}

// normalizeLongTermDraft 归一化长期记忆候选并解析被替代的记忆 ID。
func normalizeLongTermDraft(draft LongTermMemoryDraft) (longTermCandidate, error) {
	base, err := normalizeMemoryCandidate(draft.Content, draft.Category, draft.Keywords,
		draft.Importance, draft.InferenceLevel, longTermMemoryRunes)
	if err != nil {
		return longTermCandidate{}, err
	}
	supersedes := make([]int64, 0, len(draft.Supersedes))
	seen := map[int64]bool{}
	for _, raw := range draft.Supersedes {
		id, err := parseTargetID(raw)
		if err != nil || seen[id] {
			continue
		}
		seen[id] = true
		supersedes = append(supersedes, id)
		if len(supersedes) >= 5 {
			break
		}
	}
	return longTermCandidate{memoryCandidate: base, Supersedes: supersedes}, nil
}

// validMemoryCategory / validInferenceLevel 与 ai_memories 的 CHECK 约束一致。
func validMemoryCategory(category string) bool {
	switch category {
	case "fact", "preference", "relationship", "event", "topic":
		return true
	default:
		return false
	}
}

func validInferenceLevel(level string) bool {
	switch level {
	case "fact", "summary", "inference":
		return true
	default:
		return false
	}
}

// ── 记忆引用与触碰 ──

// recordMemoryRefs 记录本次任务注入过的记忆并更新 last_used_at。
// 失败只记日志，不影响任务结果。
func (s *Service) recordMemoryRefs(ctx context.Context, taskID int64, refs []memoryRef) {
	if len(refs) == 0 {
		return
	}
	pool := s.pools.Pool()
	if pool == nil {
		return
	}
	ids := make([]int64, 0, len(refs))
	for _, ref := range refs {
		refID, err := s.ids.Next()
		if err != nil {
			s.log.Warn("生成记忆引用 ID 失败", "err", err, "task_id", taskID)
			return
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO ai_task_memory_refs (id, task_id, memory_id, memory_type)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (task_id, memory_id, memory_type) DO NOTHING`,
			refID, taskID, ref.ID, ref.MemoryType); err != nil {
			s.log.Warn("写入任务记忆引用失败", "err", err, "task_id", taskID, "memory_id", ref.ID)
			return
		}
		ids = append(ids, ref.ID)
	}
	if err := s.ai.TouchMemories(ctx, pool, ids); err != nil {
		s.log.Warn("更新记忆使用时间失败", "err", err, "task_id", taskID)
	}
}

// ── 近期记忆形成 ──

// formRecentMemory 在动作成功后生成一条近期记忆。
// 失败只记日志，不回滚已经成功的动作；模型判断无价值时返回空记忆，不写库。
func (s *Service) formRecentMemory(ctx context.Context, task aiTask, result taskResult) error {
	outcome := result.Outcome
	if outcome == nil || strings.TrimSpace(outcome.MemoryNote) == "" {
		return nil
	}
	pool := s.pools.Pool()
	if pool == nil {
		return fmt.Errorf("数据库尚未就绪")
	}
	rt, err := s.ai.RuntimeByAI(ctx, pool, task.AID)
	if err != nil {
		return err
	}
	if rt.Provider.ID == 0 || !rt.Provider.IsEnabled {
		return nil
	}

	system := strings.Join([]string{
		"你在为时间线社区的 AI 用户整理短期记忆。",
		"只根据给定的公开事件与自身动作写一条不超过 200 字的摘要，不要编造未发生的事实。",
		"重要信息用 fact，互动经过用 summary，推测用 inference；没有值得记录的内容时返回空 content。",
	}, "\n")
	user := strings.Join([]string{
		"【AI 身份】" + firstNonEmpty(rt.Nickname, rt.Username),
		"【本次动作】" + outcome.MemoryNote,
		"【任务摘要】" + truncateRunes(result.Decision, 300),
	}, "\n")

	resp, err := s.callModel(ctx, rt, callRecord{
		TaskID:  &task.ID,
		AID:     rt.AIID,
		Attempt: task.Attempts,
		Params:  rt.Params,
	}, system, user, memorySchema())
	if err != nil {
		return err
	}
	if resp == nil {
		return fmt.Errorf("模型返回为空")
	}
	var draft MemoryDraft
	if err := resp.DecodeJSON(&draft); err != nil {
		return fmt.Errorf("解析记忆输出失败: %w", err)
	}
	candidate, err := normalizeMemoryDraft(draft)
	if err != nil {
		// 模型认为无需记忆，属于正常情况。
		return nil
	}

	sourceRefID := outcome.SourceRefID
	_, err = s.insertMemory(ctx, pool, rt.AIID, "recent", candidate,
		memorySourceAction, outcome.SourceRefType, sourceRefID)
	return err
}

// insertMemory 写入一条记忆（近期或长期），返回记忆 ID。
func (s *Service) insertMemory(
	ctx context.Context,
	q access.Querier,
	aiID int64,
	memoryType string,
	candidate memoryCandidate,
	sourceType, sourceRefType string,
	sourceRefID *int64,
) (int64, error) {
	memoryID, err := s.ids.Next()
	if err != nil {
		return 0, fmt.Errorf("生成记忆 ID 失败: %w", err)
	}
	if _, err := q.Exec(ctx, `
		INSERT INTO ai_memories (
			id, ai_id, memory_type, status, content, category, keywords, importance,
			source_type, source_ref_type, source_ref_id, inference_level
		) VALUES ($1, $2, $3, 'active', $4, $5, $6, $7, $8, $9, $10, $11)`,
		memoryID, aiID, memoryType, candidate.Content, candidate.Category, candidate.Keywords,
		candidate.Importance, sourceType, nullString(sourceRefType), sourceRefID,
		candidate.InferenceLevel,
	); err != nil {
		return 0, err
	}
	return memoryID, nil
}

// maybeEnqueueConsolidation 在近期记忆达到阈值时投递沉淀作业（按小时桶幂等）。
func (s *Service) maybeEnqueueConsolidation(ctx context.Context, aiID int64, now time.Time) error {
	pool := s.pools.Pool()
	if pool == nil || s.runner == nil {
		return nil
	}
	count, err := s.ai.CountRecentMemories(ctx, pool, aiID)
	if err != nil {
		return err
	}
	if count < recentMemoryThreshold {
		return nil
	}
	return s.runner.Enqueue(ctx, pool, jobs.EnqueueParams{
		Type:           jobs.TypeMemoryConsolidate,
		RefType:        memoryJobRefType,
		RefID:          &aiID,
		IdempotencyKey: consolidationKey(aiID, now),
		RunAt:          now,
		MaxAttempts:    maxTaskAttempts,
	})
}

// consolidationKey 生成沉淀作业的幂等键；同一小时只投递一次。
func consolidationKey(aiID int64, now time.Time) string {
	return fmt.Sprintf("memory-consolidate:%s:%s", api.FormatID(aiID), now.Format("2006010215"))
}

// ── 长期记忆沉淀 ──

// HandleMemoryJob 执行 jobs.TypeMemoryConsolidate 作业：
// 读取近期记忆与相关长期记忆，让模型输出长期记忆候选，写入或替代旧记忆，
// 随后按保留上限把最旧的近期记忆标记为 superseded。整理失败不删除输入记忆。
func (s *Service) HandleMemoryJob(ctx context.Context, job jobs.Job) error {
	aiID, ok := jobRefID(job, "ai_id")
	if !ok {
		return nil
	}
	pool := s.pools.Pool()
	if pool == nil {
		return fmt.Errorf("数据库尚未就绪")
	}
	// 全局紧急停止同样覆盖记忆沉淀：执行时重新校验，避免作业在停止后才被领取
	// （docs/REQUIREMENTS.md 15.6）。暂停由全局开关写入路径负责，这里只做兜底。
	// 注意：被暂停的作业不会再被领取，因此仅在作业已领取后才被停止时走到这里。
	if enabled, err := s.globalAIEnabled(ctx, pool); err != nil {
		return err
	} else if !enabled {
		if _, execErr := pool.Exec(ctx, `
			UPDATE background_jobs SET status = 'paused', updated_at = now() WHERE id = $1`,
			job.ID); execErr != nil {
			return execErr
		}
		s.log.Info("全局 AI 已停止，暂停记忆沉淀作业", "job_id", job.ID, "ai_id", aiID)
		return jobs.ErrHandled
	}
	rt, err := s.ai.RuntimeByAI(ctx, pool, aiID)
	if err != nil {
		if isNotFound(err) {
			return nil
		}
		return err
	}
	if !rt.Profile.IsEnabled || rt.Provider.ID == 0 || !rt.Provider.IsEnabled {
		// 配置不可用时不做无意义的反复重试。
		s.log.Info("AI 配置不可用，跳过记忆沉淀", "ai_id", aiID)
		return nil
	}

	recent, err := s.ai.RecentMemories(ctx, pool, aiID, consolidationBatch)
	if err != nil {
		return err
	}
	if len(recent) == 0 {
		return nil
	}
	keywords := memorySearchKeywords(recent)
	longTerm, err := s.ai.SearchLongTerm(ctx, pool, aiID, nil, keywords, maxLongTermMemoryCount)
	if err != nil {
		return err
	}

	system := strings.Join([]string{
		"你在为时间线社区的 AI 用户整理长期记忆。",
		"只保留跨越短期窗口后仍有价值的信息：稳定兴趣、重要关系、反复出现的主题与关键经历。",
		"忽略普通点赞、关注与一次性互动；重复内容合并为一条，并把被替代的记忆 ID 写入 supersedes。",
		"新记忆不超过 300 字；没有值得长期保存的内容时返回空 memories 数组。",
	}, "\n")
	user := renderConsolidationInput(recent, longTerm)

	resp, err := s.callModel(ctx, rt, callRecord{
		AID:     aiID,
		Attempt: job.Attempts,
		Params:  rt.Params,
	}, system, user, consolidationSchema())
	if err != nil {
		status, errorType, message := classifyCallError(err)
		s.log.Warn("记忆沉淀调用失败", "ai_id", aiID, "status", status, "error_type", errorType,
			"err", truncateRunes(message, 200))
		if status == statusFailed {
			return err
		}
		return nil
	}

	if resp == nil {
		return fmt.Errorf("模型返回为空")
	}
	var draft ConsolidationDraft
	if err := resp.DecodeJSON(&draft); err != nil {
		s.log.Warn("记忆沉淀输出无法解析", "ai_id", aiID, "err", err)
		return nil
	}
	written, superseded, err := s.applyConsolidation(ctx, aiID, draft)
	if err != nil {
		return err
	}
	trimmed, err := s.trimRecentMemories(ctx, pool, aiID, recentMemoryRetention)
	if err != nil {
		return err
	}
	s.log.Info("记忆沉淀完成", "ai_id", aiID, "written", written, "superseded", superseded, "trimmed", trimmed)
	return nil
}

// renderConsolidationInput 渲染沉淀输入：近期记忆与相关长期记忆（带 ID 便于替代）。
func renderConsolidationInput(recent, longTerm []ai.Memory) string {
	var builder strings.Builder
	builder.WriteString("【近期记忆】\n")
	for _, memory := range recent {
		builder.WriteString(fmt.Sprintf("- id=%s [%s|重要性%d] %s\n",
			api.FormatID(memory.ID), memory.Category, memory.Importance, memory.Content))
	}
	builder.WriteString("\n【已有长期记忆】\n")
	if len(longTerm) == 0 {
		builder.WriteString("（无）\n")
	}
	for _, memory := range longTerm {
		builder.WriteString(fmt.Sprintf("- id=%s [%s|重要性%d] %s\n",
			api.FormatID(memory.ID), memory.Category, memory.Importance, memory.Content))
	}
	return builder.String()
}

// applyConsolidation 写入长期记忆并替代被覆盖的旧记忆。
func (s *Service) applyConsolidation(
	ctx context.Context,
	aiID int64,
	draft ConsolidationDraft,
) (int, int, error) {
	pool := s.pools.Pool()
	if pool == nil {
		return 0, 0, fmt.Errorf("数据库尚未就绪")
	}
	written, superseded := 0, 0
	for _, item := range draft.Memories {
		if written >= maxLongTermPerConsolidation {
			break
		}
		candidate, err := normalizeLongTermDraft(item)
		if err != nil {
			continue
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			return written, superseded, err
		}
		memoryID, err := s.insertMemory(ctx, tx, aiID, "long_term", candidate.memoryCandidate,
			memorySourceConsolidation, "", nil)
		if err != nil {
			_ = tx.Rollback(ctx)
			return written, superseded, err
		}
		if len(candidate.Supersedes) > 0 {
			tag, err := tx.Exec(ctx, `
				UPDATE ai_memories
				SET status = 'superseded', superseded_by_id = $3, updated_at = now()
				WHERE ai_id = $1 AND id = ANY($2) AND status = 'active'`,
				aiID, candidate.Supersedes, memoryID)
			if err != nil {
				_ = tx.Rollback(ctx)
				return written, superseded, err
			}
			superseded += int(tag.RowsAffected())
		}
		if err := tx.Commit(ctx); err != nil {
			return written, superseded, err
		}
		written++
	}
	return written, superseded, nil
}

// trimRecentMemories 把超出保留上限的最旧近期记忆标记为 superseded（逻辑失效，不删除）。
func (s *Service) trimRecentMemories(
	ctx context.Context,
	q access.Querier,
	aiID int64,
	retain int,
) (int, error) {
	if retain <= 0 {
		return 0, nil
	}
	tag, err := q.Exec(ctx, `
		UPDATE ai_memories
		SET status = 'superseded', updated_at = now()
		WHERE ai_id = $1 AND memory_type = 'recent' AND status = 'active'
		  AND id NOT IN (
			SELECT id FROM ai_memories
			WHERE ai_id = $1 AND memory_type = 'recent' AND status = 'active'
			ORDER BY id DESC LIMIT $2
		  )`, aiID, retain)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// memorySearchKeywords 从近期记忆中提取长期记忆检索关键词。
func memorySearchKeywords(memories []ai.Memory) []string {
	terms := make([]string, 0, maxContextKeywords)
	seen := map[string]bool{}
	for _, memory := range memories {
		candidates := append([]string{}, memory.Keywords...)
		candidates = append(candidates, keywordTerms(memory.Content, 3)...)
		for _, term := range candidates {
			trimmed := strings.TrimSpace(term)
			if trimmed == "" {
				continue
			}
			key := strings.ToLower(trimmed)
			if seen[key] {
				continue
			}
			seen[key] = true
			terms = append(terms, trimmed)
			if len(terms) >= maxContextKeywords {
				return terms
			}
		}
	}
	return terms
}

// ── 来源权限变化 ──

// recheckSourceMemories 复核直接引用公开来源的记忆：
// 来源被删除或改为非公开时置为 invalidated，处理前不再注入模型上下文。
func (s *Service) recheckSourceMemories(ctx context.Context, aiID int64) error {
	return s.recheckSourceMemoriesLimit(ctx, aiID, memoryRefBatch)
}

// recheckSourceMemoriesLimit 是带扫描上限的来源复核，供调度巡检批量使用。
func (s *Service) recheckSourceMemoriesLimit(ctx context.Context, aiID int64, limit int) error {
	pool := s.pools.Pool()
	if pool == nil {
		return fmt.Errorf("数据库尚未就绪")
	}
	if limit <= 0 {
		limit = memoryRefBatch
	}
	rows, err := pool.Query(ctx, `
		SELECT DISTINCT source_ref_type, source_ref_id
		FROM ai_memories
		WHERE ai_id = $1 AND status = 'active'
		  AND source_ref_type IN ('post', 'comment') AND source_ref_id IS NOT NULL
		LIMIT $2`, aiID, limit)
	if err != nil {
		return err
	}
	type sourcePair struct {
		sourceType string
		sourceID   int64
	}
	pairs := make([]sourcePair, 0, limit)
	for rows.Next() {
		var pair sourcePair
		if err := rows.Scan(&pair.sourceType, &pair.sourceID); err != nil {
			rows.Close()
			return err
		}
		pairs = append(pairs, pair)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, pair := range pairs {
		available, err := s.sourcePubliclyAvailable(ctx, pool, pair.sourceType, pair.sourceID)
		if err != nil {
			return err
		}
		if available {
			continue
		}
		if err := s.ai.InvalidateBySource(ctx, pool, aiID, pair.sourceType, pair.sourceID); err != nil {
			return err
		}
		s.log.Info("来源已不可用，已停用相关记忆",
			"ai_id", aiID, "source_type", pair.sourceType, "source_id", pair.sourceID)
	}
	return nil
}

// sourcePubliclyAvailable 判断来源内容是否仍是公开且已发布。
func (s *Service) sourcePubliclyAvailable(
	ctx context.Context,
	q access.Querier,
	sourceType string,
	sourceID int64,
) (bool, error) {
	switch sourceType {
	case sourcePost:
		var available bool
		err := q.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM posts
				WHERE id = $1 AND status = 'published' AND visibility = 'public'
			)`, sourceID).Scan(&available)
		return available, err
	case sourceComment:
		var available bool
		err := q.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM comments c
				JOIN posts p ON p.id = c.post_id
				WHERE c.id = $1 AND c.status = 'published'
				  AND p.status = 'published' AND p.visibility = 'public'
			)`, sourceID).Scan(&available)
		return available, err
	default:
		return true, nil
	}
}
