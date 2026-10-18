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
// 每轮按 (source_type, source_id) keyset 游标读取一批来源，游标记录在 Service 内
// （按 AI 隔离，用尽后归零从头轮转），避免每分钟重复复核同一批来源。
func (s *Service) recheckSourceMemoriesLimit(ctx context.Context, aiID int64, limit int) error {
	if limit <= 0 {
		limit = memoryRefBatch
	}
	after := s.memoryRecheckAfter(aiID)
	sources, err := s.deps.AI.ListActiveMemorySourceRefs(ctx, aiID, after, limit)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		// 已扫到尾部：下一轮从头开始轮转，保证新增来源也会被复核。
		s.resetMemoryRecheckCursor(aiID)
		return nil
	}
	at := s.now()
	unavailable, err := s.unavailableSources(ctx, sources)
	if err != nil {
		// 读取失败不推进游标，下一轮重试同一批（失效写入是幂等的）。
		return err
	}
	for _, source := range unavailable {
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
	// 整批处理成功后推进游标；不足一批说明已取尽，归零轮转。
	s.advanceMemoryRecheckCursor(aiID, sources, len(sources) < limit)
	return nil
}

// unavailableSources 批量判定一批来源是否仍公开可用：
// 评论一次批量读取，帖子（直接引用的帖子 + 评论所属帖子）再一次批量读取，共 2 条查询。
// 判定规则与旧实现一致：post 需要已发布且公开；comment 需要未删除且所属帖子已发布且公开。
func (s *Service) unavailableSources(
	ctx context.Context,
	sources []contracts.MemorySourceRef,
) ([]contracts.MemorySourceRef, error) {
	commentIDs := make([]int64, 0, len(sources))
	for _, source := range sources {
		if source.SourceType == domain.RefComment && source.SourceID > 0 {
			commentIDs = append(commentIDs, source.SourceID)
		}
	}
	comments := map[int64]model.Comment{}
	if len(commentIDs) > 0 {
		loaded, err := s.deps.Social.GetComments(ctx, commentIDs)
		if err != nil {
			return nil, err
		}
		comments = loaded
	}
	postIDs := make([]int64, 0, len(sources))
	seenPost := make(map[int64]bool, len(sources))
	addPostID := func(postID int64) {
		if postID <= 0 || seenPost[postID] {
			return
		}
		seenPost[postID] = true
		postIDs = append(postIDs, postID)
	}
	for _, source := range sources {
		if source.SourceType == domain.RefPost {
			addPostID(source.SourceID)
		}
	}
	for _, comment := range comments {
		addPostID(comment.PostID)
	}
	posts := map[int64]model.Post{}
	if len(postIDs) > 0 {
		loaded, err := s.deps.Content.GetPosts(ctx, postIDs)
		if err != nil {
			return nil, err
		}
		posts = loaded
	}
	postAvailable := func(postID int64) bool {
		post, ok := posts[postID]
		return ok && post.Status == model.PostStatusPublished && post.Visibility == model.VisibilityPublic
	}
	unavailable := make([]contracts.MemorySourceRef, 0, len(sources))
	for _, source := range sources {
		switch source.SourceType {
		case domain.RefPost:
			if !postAvailable(source.SourceID) {
				unavailable = append(unavailable, source)
			}
		case domain.RefComment:
			comment, ok := comments[source.SourceID]
			if !ok || comment.Deleted() || !postAvailable(comment.PostID) {
				unavailable = append(unavailable, source)
			}
		default:
			// 未知来源类型不参与复核（与原实现一致）。
		}
	}
	return unavailable, nil
}

// memoryRecheckAfter 读取某 AI 的来源复核游标；零值表示从头开始。
func (s *Service) memoryRecheckAfter(aiID int64) contracts.MemorySourceRef {
	s.memoryRecheckMu.Lock()
	defer s.memoryRecheckMu.Unlock()
	return s.memoryRecheckCursor[aiID]
}

// advanceMemoryRecheckCursor 推进某 AI 的来源复核游标；exhausted=true 时归零，下一轮从头轮转。
func (s *Service) advanceMemoryRecheckCursor(
	aiID int64,
	sources []contracts.MemorySourceRef,
	exhausted bool,
) {
	s.memoryRecheckMu.Lock()
	defer s.memoryRecheckMu.Unlock()
	if exhausted {
		delete(s.memoryRecheckCursor, aiID)
		return
	}
	if s.memoryRecheckCursor == nil {
		s.memoryRecheckCursor = map[int64]contracts.MemorySourceRef{}
	}
	s.memoryRecheckCursor[aiID] = sources[len(sources)-1]
}

// resetMemoryRecheckCursor 把某 AI 的游标归零，下一轮从头开始轮转。
func (s *Service) resetMemoryRecheckCursor(aiID int64) {
	s.memoryRecheckMu.Lock()
	defer s.memoryRecheckMu.Unlock()
	delete(s.memoryRecheckCursor, aiID)
}
