// bookmarks.go 组装「我的收藏」列表：social 提供收藏 refs，content 负责帖子 brief
// 组装与可见性复核。已删除或当前不可见的帖子（如仅关注者帖子在取关后）被过滤，
// 收藏记录本身保留，恢复可见后重新出现。
package app

import (
	"context"

	"github.com/great-magician-01/timeline/internal/modules/content/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/access"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// BookmarkScope 是收藏列表的夹过滤三态（语义同 contracts.BookmarkListQuery）：
// All=true 返回全部；否则 DefaultOnly=true 只看未分组；否则按 FolderID 精确过滤。
type BookmarkScope struct {
	All         bool
	DefaultOnly bool
	FolderID    int64
}

// ListBookmarks 返回访问者自己的收藏列表（bookmark 行 ID 游标，服务端按可见性过滤后凑页）。
func (s *Service) ListBookmarks(
	ctx context.Context,
	viewer model.Viewer,
	scope BookmarkScope,
	cursorID int64,
	limit int,
) (BookmarkListPage, error) {
	if s.deps.Bookmarks == nil {
		return BookmarkListPage{}, apperr.Internal("收藏服务未就绪")
	}
	// 指定收藏夹时先校验属主：非本人/已删除/不存在的夹一律按 404，不暴露存在性。
	if scope.FolderID > 0 && !scope.All && !scope.DefaultOnly {
		exists, err := s.deps.Bookmarks.HasBookmarkFolder(ctx, viewer.ID, scope.FolderID)
		if err != nil {
			return BookmarkListPage{}, s.wrap(err, "校验收藏夹失败")
		}
		if !exists {
			return BookmarkListPage{}, apperr.NotFound("收藏夹不存在")
		}
	}
	following, err := s.followingSet(ctx, viewer)
	if err != nil {
		return BookmarkListPage{}, err
	}
	fetch := func(fctx context.Context, afterID int64, batchLimit int) ([]bookmarkCandidate, error) {
		items, err := s.deps.Bookmarks.ListBookmarks(fctx, contracts.BookmarkListQuery{
			UserID:      viewer.ID,
			All:         scope.All,
			DefaultOnly: scope.DefaultOnly,
			FolderID:    scope.FolderID,
			AfterID:     afterID,
			Limit:       batchLimit,
		})
		if err != nil {
			return nil, s.wrap(err, "读取收藏列表失败")
		}
		postIDs := make([]int64, 0, len(items))
		for _, item := range items {
			postIDs = append(postIDs, item.PostID)
		}
		posts, err := s.repo.GetPosts(fctx, postIDs)
		if err != nil {
			return nil, s.wrap(err, "读取收藏帖子失败")
		}
		candidates := make([]bookmarkCandidate, 0, len(items))
		for _, item := range items {
			candidates = append(candidates, bookmarkCandidate{item: item, post: posts[item.PostID]})
		}
		return candidates, nil
	}
	// keep 与时间线一致：帖子行缺失（无痕清理）或当前不可见即过滤；
	// 不做作者停用过滤（列表照常展示，卡片标注停用状态）。
	keep := func(item bookmarkCandidate) bool {
		return item.post.ID != 0 && access.CanViewPostWithFollowing(viewer, item.post, following)
	}
	collected, nextID, hasMore, err := domain.CollectIDPage(
		ctx, cursorID, limit, fetch,
		func(item bookmarkCandidate) int64 { return item.item.ID },
		keep,
	)
	if err != nil {
		return BookmarkListPage{}, err
	}
	if len(collected) == 0 {
		hasMore = false
	}
	items, err := s.assembleBookmarkItems(ctx, viewer, following, collected)
	if err != nil {
		return BookmarkListPage{}, err
	}
	return BookmarkListPage{Items: items, NextID: nextID, HasMore: hasMore}, nil
}

// bookmarkCandidate 是收藏列表候选：收藏行 + 帖子行。
type bookmarkCandidate struct {
	item contracts.BookmarkItem
	post model.Post
}

// assembleBookmarkItems 批量组装收藏列表项（brief + 收藏归属元数据）。
// 候选已带帖子行，组装时直接复用，避免对同一批 ID 重复执行 GetPosts。
func (s *Service) assembleBookmarkItems(
	ctx context.Context,
	viewer model.Viewer,
	following map[int64]bool,
	candidates []bookmarkCandidate,
) ([]BookmarkListItem, error) {
	if len(candidates) == 0 {
		return []BookmarkListItem{}, nil
	}
	postIDs := make([]int64, 0, len(candidates))
	preloaded := make(map[int64]model.Post, len(candidates))
	for _, candidate := range candidates {
		postIDs = append(postIDs, candidate.item.PostID)
		preloaded[candidate.item.PostID] = candidate.post
	}
	data, err := s.loadBriefData(ctx, viewer, following, postIDs, preloaded, nil)
	if err != nil {
		return nil, err
	}
	out := make([]BookmarkListItem, 0, len(candidates))
	for _, candidate := range candidates {
		post, ok := data.posts[candidate.item.PostID]
		if !ok {
			continue
		}
		item := BookmarkListItem{
			PostBrief: data.brief(post, briefOptions{kind: domain.KindPost, authorID: post.AuthorID}),
			Bookmark: BookmarkState{
				FolderID:  candidate.item.FolderID,
				CreatedAt: candidate.item.CreatedAt,
			},
		}
		out = append(out, item)
	}
	return out, nil
}
