package app

import (
	"context"

	"github.com/great-magician-01/timeline/internal/modules/agent/domain"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

const (
	// memoryRefBatch 是动作成功后单次来源复核的扫描上限。
	memoryRefBatch = 100
	// memoryRefSchedulerBatch 是调度巡检单轮来源复核的扫描上限。
	memoryRefSchedulerBatch = 20
)

// recheckSourceMemories 复核该 AI 生效记忆引用的公开来源：来源被删除或改为非公开时，
// 同时停用直接引用（source_ref_*）与沉淀来源关联（ai_memory_sources）的记忆，
// 处理前不再注入模型上下文（docs/REQUIREMENTS.md 14.2 第 7 条）。
// 失败只记日志，不阻塞已提交的动作。
func (s *Service) recheckSourceMemories(ctx context.Context, aiID int64) error {
	return s.recheckSourceMemoriesLimit(ctx, aiID, memoryRefBatch)
}

// recheckSourceMemoriesLimit 是带扫描上限的来源复核，供调度巡检批量使用。
func (s *Service) recheckSourceMemoriesLimit(ctx context.Context, aiID int64, limit int) error {
	if limit <= 0 {
		limit = memoryRefBatch
	}
	sources, err := s.deps.AI.ListActiveMemorySourceRefs(ctx, aiID, limit)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		return nil
	}
	at := s.now()
	for _, source := range sources {
		available, err := s.sourcePubliclyAvailable(ctx, source)
		if err != nil {
			return err
		}
		if available {
			continue
		}
		if err := s.deps.AI.InvalidateBySource(ctx, contracts.InvalidateBySourceInput{
			AIID: aiID, SourceType: source.SourceType, SourceID: source.SourceID, At: at,
		}); err != nil {
			return err
		}
		affected, err := s.deps.AI.InvalidateMemoriesBySources(ctx, contracts.InvalidateMemoriesBySourcesInput{
			AIID: aiID,
			Sources: []contracts.MemorySourceInput{{
				SourceType: source.SourceType, SourceID: source.SourceID, At: at,
			}},
			At: at,
		})
		if err != nil {
			return err
		}
		s.deps.Log.Info("来源已不可用，已停用相关记忆",
			"ai_id", aiID, "source_type", source.SourceType,
			"source_id", source.SourceID, "affected", affected)
	}
	return nil
}

// sourcePubliclyAvailable 判断记忆来源是否仍是公开且已发布内容：
// post 需要公开已发布；comment 需要未删除且所在帖子公开已发布。
func (s *Service) sourcePubliclyAvailable(ctx context.Context, source contracts.MemorySourceRef) (bool, error) {
	switch source.SourceType {
	case domain.RefPost:
		post, err := s.deps.Content.GetVisiblePost(ctx, model.GuestViewer(), source.SourceID)
		if err != nil {
			if isNotFound(err) {
				return false, nil
			}
			return false, err
		}
		return post.Visibility == model.VisibilityPublic, nil
	case domain.RefComment:
		comment, err := s.deps.Social.GetComment(ctx, source.SourceID)
		if err != nil {
			if isNotFound(err) {
				return false, nil
			}
			return false, err
		}
		if comment.Deleted() {
			return false, nil
		}
		post, err := s.deps.Content.GetVisiblePost(ctx, model.GuestViewer(), comment.PostID)
		if err != nil {
			if isNotFound(err) {
				return false, nil
			}
			return false, err
		}
		return post.Visibility == model.VisibilityPublic, nil
	default:
		return true, nil
	}
}
