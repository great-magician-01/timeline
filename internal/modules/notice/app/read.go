package app

import (
	"context"

	"github.com/great-magician-01/timeline/internal/modules/notice/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/access"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// List 列出当前登录用户的通知（ID 倒序游标分页）；预览按当前权限解析，不可见时为 nil。
func (s *Service) List(
	ctx context.Context, viewer model.Viewer, filter, noticeType string, cursor int64, limit int,
) (Page, error) {
	if !viewer.Authenticated {
		return Page{}, apperr.Unauthenticated("请先登录")
	}
	normalizedFilter, err := domain.NormalizeFilter(filter)
	if err != nil {
		return Page{}, err
	}
	normalizedType, err := domain.NormalizeType(noticeType)
	if err != nil {
		return Page{}, err
	}
	rows, err := s.deps.Notices.List(ctx, viewer.ID, normalizedFilter, normalizedType, cursor, limit+1)
	if err != nil {
		return Page{}, err
	}
	trimmed, hasMore := trimPage(rows, limit)

	actorIDs := make([]int64, 0, len(trimmed))
	for _, row := range trimmed {
		actorIDs = append(actorIDs, row.ActorID)
	}
	briefs, err := s.deps.Users.GetUserBriefs(ctx, actorIDs)
	if err != nil {
		return Page{}, err
	}
	previews, err := s.previews(ctx, viewer, trimmed)
	if err != nil {
		return Page{}, err
	}

	page := Page{Items: make([]Item, 0, len(trimmed)), HasMore: hasMore}
	for i, row := range trimmed {
		page.Items = append(page.Items, Item{
			Notification: row,
			Actor:        briefs[row.ActorID],
			Preview:      previews[i],
		})
		page.NextCursor = row.ID
	}
	return page, nil
}

// UnreadCount 返回当前登录用户的未读通知数。
func (s *Service) UnreadCount(ctx context.Context, viewer model.Viewer) (int64, error) {
	if !viewer.Authenticated {
		return 0, apperr.Unauthenticated("请先登录")
	}
	return s.deps.Notices.UnreadCount(ctx, viewer.ID)
}

// MarkRead 标记单条通知已读；不存在或不属于当前用户时返回 not_found。
func (s *Service) MarkRead(ctx context.Context, viewer model.Viewer, id int64) error {
	if !viewer.Authenticated {
		return apperr.Unauthenticated("请先登录")
	}
	ok, err := s.deps.Notices.MarkRead(ctx, viewer.ID, id)
	if err != nil {
		return err
	}
	if !ok {
		return apperr.NotFound("通知不存在")
	}
	return nil
}

// MarkAllRead 标记当前用户的全部通知已读，返回更新行数。
func (s *Service) MarkAllRead(ctx context.Context, viewer model.Viewer) (int64, error) {
	if !viewer.Authenticated {
		return 0, apperr.Unauthenticated("请先登录")
	}
	return s.deps.Notices.MarkAllRead(ctx, viewer.ID)
}

// previews 按当前权限解析每条通知的预览文本：
// 帖子取 content 模块的搜索投影纯文本；评论取 social 模块的评论正文；
// 目标不可见或已删除时返回 nil（不复制正文权限，docs/API.md §9）。
func (s *Service) previews(
	ctx context.Context, viewer model.Viewer, rows []model.Notification,
) ([]*string, error) {
	result := make([]*string, len(rows))
	if len(rows) == 0 {
		return result, nil
	}

	postIDSet := map[int64]bool{}
	commentIDs := make([]int64, 0, len(rows))
	for _, row := range rows {
		if row.PostID != nil && *row.PostID > 0 {
			postIDSet[*row.PostID] = true
		}
		if row.CommentID != nil {
			commentIDs = append(commentIDs, *row.CommentID)
		}
	}
	// 纯关注类通知（无帖子/评论目标）没有正文可展示，直接短路，省掉后续全部批量查询
	// （评论、帖子、纯文本与关注集合），这是通知列表最常见的「关注/申请」场景。
	if len(postIDSet) == 0 && len(commentIDs) == 0 {
		return result, nil
	}

	var comments map[int64]model.Comment
	if len(commentIDs) > 0 {
		var err error
		comments, err = s.deps.Comments.GetComments(ctx, commentIDs)
		if err != nil {
			return nil, err
		}
		for _, comment := range comments {
			postIDSet[comment.PostID] = true
		}
	}

	postIDs := make([]int64, 0, len(postIDSet))
	for id := range postIDSet {
		postIDs = append(postIDs, id)
	}
	posts := map[int64]model.Post{}
	texts := map[int64]string{}
	if len(postIDs) > 0 {
		var err error
		if posts, err = s.deps.Posts.GetPosts(ctx, postIDs); err != nil {
			return nil, err
		}
		if texts, err = s.deps.Posts.GetPostPlainTexts(ctx, postIDs); err != nil {
			return nil, err
		}
	}
	following := map[int64]bool{}
	if needsFollowingSet(viewer, posts) {
		var err error
		following, err = s.deps.Relations.FollowingSet(ctx, viewer.ID)
		if err != nil {
			return nil, err
		}
	}
	visible := make(map[int64]bool, len(posts))
	for id, post := range posts {
		visible[id] = access.CanViewPostWithFollowing(viewer, post, following)
	}

	for i, row := range rows {
		switch {
		case row.CommentID != nil:
			comment, ok := comments[*row.CommentID]
			if !ok || comment.Deleted() || !visible[comment.PostID] {
				continue
			}
			result[i] = domain.TruncatePreview(comment.PlainText)
		case row.PostID != nil && *row.PostID > 0:
			if !visible[*row.PostID] {
				continue
			}
			result[i] = domain.TruncatePreview(texts[*row.PostID])
		default:
			// 关注类通知不需要正文预览，前端使用 actor 信息展示。
			continue
		}
	}
	return result, nil
}

// needsFollowingSet 判断这批帖子中是否存在必须依赖关注关系才能判定的可见性。
//
// 只有「已发布 + 仅关注者可见 + 非本人」的帖子需要关注集合：公开帖直接可见；
// 私密帖/草稿/已删除帖与关注关系无关；管理员与 AI 不会因关注关系获得 followers 可见性。
// 绝大多数通知目标是公开帖子或自己的内容，因此多数页面不需要加载（可能无上限的）关注集合。
func needsFollowingSet(viewer model.Viewer, posts map[int64]model.Post) bool {
	if !viewer.Authenticated || viewer.IsAdmin || viewer.IsAI {
		return false
	}
	for _, post := range posts {
		if post.Status == model.PostStatusPublished &&
			post.Visibility == model.VisibilityFollowers &&
			!post.SelfPosted(viewer) {
			return true
		}
	}
	return false
}
