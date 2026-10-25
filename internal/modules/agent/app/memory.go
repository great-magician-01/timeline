package app

import (
	"context"
	"strings"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/agent/domain"
	"github.com/great-magician-01/timeline/internal/modules/agent/repo"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
)

// formMemory 在动作成功后形成一条近期记忆并记录来源。
func (s *Service) formMemory(ctx context.Context, task repo.Task, rt contracts.Runtime, result taskResult, at time.Time) error {
	if result.Outcome == nil || result.Outcome.MemoryNote == "" {
		return nil
	}
	keywords := domainKeywords(result.Outcome.MemoryNote)
	memory, err := s.deps.AI.CreateMemory(ctx, contracts.CreateMemoryInput{
		AIID:           rt.AIID,
		MemoryType:     "recent",
		Content:        truncateRunes(result.Outcome.MemoryNote, 1000),
		Category:       "event",
		Keywords:       keywords,
		Importance:     3,
		SourceType:     "action",
		SourceRefType:  result.Outcome.ResultRefType,
		SourceRefID:    result.Outcome.ResultRefID,
		InferenceLevel: "fact",
		At:             at,
	})
	if err != nil {
		return err
	}
	if result.Outcome.ResultRefType != "" && result.Outcome.ResultRefID != nil {
		if err := s.deps.AI.AddMemorySources(ctx, memory.ID, []contracts.MemorySourceInput{{
			MemoryID:   memory.ID,
			SourceType: result.Outcome.ResultRefType,
			SourceID:   *result.Outcome.ResultRefID,
			At:         at,
		}}); err != nil {
			return err
		}
	}
	return nil
}

// maybeEnqueueConsolidation 近期记忆达到阈值时投递沉淀作业（按 AI + 日期幂等）。
func (s *Service) maybeEnqueueConsolidation(ctx context.Context, aiID int64, at time.Time) error {
	count, err := s.deps.AI.CountRecentMemories(ctx, aiID)
	if err != nil || count < consolidationThreshold {
		return err
	}
	key := "memory_consolidation:" + apiID(aiID) + ":" + at.UTC().Format("20060102")
	return s.deps.Jobs.Enqueue(ctx, contracts.JobEnqueue{
		Type:           contracts.JobTypeMemoryConsolidate,
		RefType:        domain.RefAI,
		RefID:          &aiID,
		IdempotencyKey: key,
		Payload:        jobPayload{AIID: aiID},
		RunAt:          at,
		MaxAttempts:    3,
		At:             at,
	})
}

// maxConsolidationItemRunes 是沉淀输入中单条近期记忆的字符上限：10 条近期记忆
// 拼成不超过 ai 模块记忆正文上限（1000）的摘要，避免沉淀写入因超长被拒绝。
const maxConsolidationItemRunes = 96

// memoryContentRuneLimit 与 modules/ai/domain.MaxMemoryContentRunes 保持一致（1000）。
const memoryContentRuneLimit = 1000

// consolidateMemories 把近期记忆沉淀为一条长期记忆，并标记被取代的近期记忆。
// 沉淀正文必须满足 ai 模块的记忆约束（分类只能是 fact/preference/relationship/event/topic，
// 正文不超过 1000 字符），来源关联只允许 post/comment（0007 CHECK）。
func (s *Service) consolidateMemories(ctx context.Context, aiID int64) error {
	recent, err := s.deps.AI.RecentMemories(ctx, aiID, consolidationThreshold)
	if err != nil || len(recent) == 0 {
		return err
	}
	at := s.now()
	var builder strings.Builder
	builder.WriteString("近期经历摘要：")
	ids := make([]int64, 0, len(recent))
	for index, memory := range recent {
		if index > 0 {
			builder.WriteString("；")
		}
		builder.WriteString(truncateRunes(memory.Content, maxConsolidationItemRunes))
		ids = append(ids, memory.ID)
	}
	content := truncateRunes(builder.String(), memoryContentRuneLimit)
	longTerm, err := s.deps.AI.CreateMemory(ctx, contracts.CreateMemoryInput{
		AIID:       aiID,
		MemoryType: "long_term",
		Content:    content,
		// ai_memories.category 没有 summary；沉淀语义由 inference_level=summary 表达。
		Category:       "event",
		Keywords:       domainKeywords(content),
		Importance:     5,
		SourceType:     "consolidation",
		InferenceLevel: "summary",
		At:             at,
	})
	if err != nil {
		return err
	}
	if err := s.deps.AI.SupersedeMemories(ctx, contracts.SupersedeMemoryInput{
		AIID: aiID, MemoryIDs: ids, SupersededByID: longTerm.ID, At: at,
	}); err != nil {
		return err
	}
	// 聚合被取代近期记忆的内容来源（post/comment），写入长期记忆的来源关联供后续失效复核；
	// 没有可关联来源时跳过；来源关联失败只告警，不回滚已写入的长期记忆。
	sources := s.consolidationSources(ctx, recent)
	if len(sources) == 0 {
		return nil
	}
	inputs := make([]contracts.MemorySourceInput, 0, len(sources))
	for _, source := range sources {
		inputs = append(inputs, contracts.MemorySourceInput{
			MemoryID: longTerm.ID, SourceType: source.SourceType, SourceID: source.SourceID, At: at,
		})
	}
	if err := s.deps.AI.AddMemorySources(ctx, longTerm.ID, inputs); err != nil {
		s.deps.Log.Warn("写入沉淀记忆来源失败", "err", err, "ai_id", aiID, "memory_id", longTerm.ID)
	}
	return nil
}

// consolidationSources 聚合一批近期记忆的公开内容来源：直接引用（source_ref_*）
// 与 ai_memory_sources 关联的 post/comment，去重后返回。
func (s *Service) consolidationSources(
	ctx context.Context,
	memories []contracts.Memory,
) []contracts.MemorySourceRef {
	seen := map[string]bool{}
	refs := make([]contracts.MemorySourceRef, 0, len(memories))
	add := func(sourceType string, sourceID int64) {
		if sourceType != domain.RefPost && sourceType != domain.RefComment {
			return
		}
		if sourceID <= 0 {
			return
		}
		key := sourceType + ":" + apiID(sourceID)
		if seen[key] {
			return
		}
		seen[key] = true
		refs = append(refs, contracts.MemorySourceRef{SourceType: sourceType, SourceID: sourceID})
	}
	ids := make([]int64, 0, len(memories))
	for _, memory := range memories {
		ids = append(ids, memory.ID)
		if memory.SourceRefID != nil {
			add(memory.SourceRefType, *memory.SourceRefID)
		}
	}
	linked, err := s.deps.AI.ListMemorySources(ctx, ids)
	if err != nil {
		s.deps.Log.Warn("读取记忆来源失败，沉淀只使用直接引用来源", "err", err)
		return refs
	}
	for _, source := range linked {
		add(source.SourceType, source.SourceID)
	}
	return refs
}

// domainKeywords 从文本提取去重关键词（长度 2-32 的非空片段）。
func domainKeywords(text string) []string {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return r == ' ' || r == '\n' || r == '\t' || r == '，' || r == '。' || r == '；' ||
			r == '：' || r == ',' || r == '.' || r == ';' || r == ':' || r == '!' || r == '?'
	})
	seen := map[string]bool{}
	keywords := make([]string, 0, 8)
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if len([]rune(field)) < 2 || len([]rune(field)) > 32 {
			continue
		}
		if seen[field] {
			continue
		}
		seen[field] = true
		keywords = append(keywords, field)
		if len(keywords) >= 8 {
			break
		}
	}
	return keywords
}

// memoryIDs 提取记忆 ID。
func memoryIDs(refs []repo.MemoryRef) []int64 {
	ids := make([]int64, 0, len(refs))
	for _, ref := range refs {
		ids = append(ids, ref.MemoryID)
	}
	return ids
}
