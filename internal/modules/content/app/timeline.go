package app

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/content/domain"
	"github.com/great-magician-01/timeline/internal/modules/content/repo"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/access"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/shared/page"
)

// timelineParams 描述一次时间线用例的过滤条件。
type timelineParams struct {
	includePosts   bool
	includeReposts bool
	quotedOnly     bool
	followingOnly  bool
	authorID       int64
	filter         string
	tagName        string
	searchText     string
}

// timelineCandidate 是时间线候选：候选行 + 已加载的帖子行（含纯转发原帖）。
type timelineCandidate struct {
	row  repo.CandidateRow
	post model.Post
}

func (c timelineCandidate) key() (time.Time, int64) {
	return c.row.SortAt, c.row.ItemID
}

// Timeline 返回全站时间线，按访问者权限过滤。
func (s *Service) Timeline(
	ctx context.Context,
	viewer model.Viewer,
	filter string,
	cursor page.TimeCursor,
	limit int,
) (TimelinePage, error) {
	normalized, err := domain.NormalizeFilter(filter)
	if err != nil {
		return TimelinePage{}, err
	}
	return s.listTimeline(ctx, viewer, timelineParams{
		includePosts:   true,
		includeReposts: true,
		filter:         normalized,
	}, cursor, limit)
}

// FollowingTimeline 返回关注时间线：作者必须是我当前生效关注的账号。
func (s *Service) FollowingTimeline(
	ctx context.Context,
	viewer model.Viewer,
	filter string,
	cursor page.TimeCursor,
	limit int,
) (TimelinePage, error) {
	if !viewer.Authenticated {
		return TimelinePage{}, apperr.Unauthenticated("请先登录")
	}
	if viewer.IsAI {
		return TimelinePage{}, apperr.Forbidden("AI 账号不能通过此接口操作")
	}
	normalized, err := domain.NormalizeFilter(filter)
	if err != nil {
		return TimelinePage{}, err
	}
	return s.listTimeline(ctx, viewer, timelineParams{
		includePosts:   true,
		includeReposts: true,
		followingOnly:  true,
		filter:         normalized,
	}, cursor, limit)
}

// UserTimeline 返回个人主页时间线，支持 posts/replies/reposts 三种页签。
func (s *Service) UserTimeline(
	ctx context.Context,
	viewer model.Viewer,
	username, tab string,
	cursor page.TimeCursor,
	limit int,
) (TimelinePage, error) {
	normalizedTab, err := domain.NormalizeTab(tab)
	if err != nil {
		return TimelinePage{}, err
	}
	if s.deps.Users == nil {
		return TimelinePage{}, apperr.Internal("账号服务未就绪")
	}
	author, err := s.deps.Users.GetUserByUsername(ctx, strings.TrimSpace(username))
	if err != nil {
		if apperr.IsKind(err, apperr.KindNotFound) {
			return TimelinePage{}, apperr.NotFound("用户不存在")
		}
		return TimelinePage{}, s.wrap(err, "读取主页作者失败")
	}
	if normalizedTab == domain.TabReplies {
		return s.userTimelineReplies(ctx, viewer, author.ID, cursor, limit)
	}
	params := timelineParams{
		includePosts: true,
		authorID:     author.ID,
		filter:       domain.FilterAll,
	}
	if normalizedTab == domain.TabReposts {
		// 纯转发与引用转发合并按时间倒序展示。
		params.includeReposts = true
		params.quotedOnly = true
	}
	return s.listTimeline(ctx, viewer, params, cursor, limit)
}

// TagTimeline 返回标签聚合：只包含当前访问者有权查看的已发布帖子。
// 标签不存在时返回空列表，而不是 404，避免暴露标签是否被非公开内容使用。
func (s *Service) TagTimeline(
	ctx context.Context,
	viewer model.Viewer,
	name string,
	cursor page.TimeCursor,
	limit int,
) (TimelinePage, error) {
	tagName := domain.NormalizeTagName(name)
	if tagName == "" {
		return TimelinePage{}, nil
	}
	exists, err := s.repo.TagExists(ctx, tagName)
	if err != nil {
		return TimelinePage{}, s.wrap(err, "读取标签失败")
	}
	if !exists {
		return TimelinePage{}, nil
	}
	return s.listTimeline(ctx, viewer, timelineParams{
		includePosts: true,
		tagName:      tagName,
		filter:       domain.FilterAll,
	}, cursor, limit)
}

// SearchPosts 按纯文本投影搜索已发布帖子；结果仍按访问者权限过滤。
func (s *Service) SearchPosts(
	ctx context.Context,
	viewer model.Viewer,
	keyword string,
	cursor page.TimeCursor,
	limit int,
) (TimelinePage, error) {
	normalized, err := domain.NormalizeSearchKeyword(keyword)
	if err != nil {
		return TimelinePage{}, err
	}
	return s.listTimeline(ctx, viewer, timelineParams{
		includePosts: true,
		searchText:   strings.ToLower(normalized),
		filter:       domain.FilterAll,
	}, cursor, limit)
}

// SuggestTags 按前缀匹配标签，且只统计当前访问者可见的已发布帖子。
func (s *Service) SuggestTags(
	ctx context.Context,
	viewer model.Viewer,
	keyword string,
) ([]TagSuggestion, error) {
	normalized := strings.ToLower(strings.TrimSpace(keyword))
	if normalized == "" || len([]rune(normalized)) > domain.MaxTagRunes {
		return []TagSuggestion{}, nil
	}
	following, err := s.followingSet(ctx, viewer)
	if err != nil {
		return nil, err
	}
	rows, err := s.repo.SuggestTags(ctx, viewer.ID, domain.SortedIDs(following),
		viewer.IsAI, normalized, domain.MaxSuggestCount)
	if err != nil {
		return nil, s.wrap(err, "读取标签补全失败")
	}
	items := make([]TagSuggestion, 0, len(rows))
	for _, row := range rows {
		items = append(items, TagSuggestion{Name: row.Name, DisplayName: row.DisplayName, PostCount: row.PostCount})
	}
	return items, nil
}

// listTimeline 是时间线类接口的公共流程：取候选、组装、按时间游标返回。
func (s *Service) listTimeline(
	ctx context.Context,
	viewer model.Viewer,
	params timelineParams,
	cursor page.TimeCursor,
	limit int,
) (TimelinePage, error) {
	following, err := s.followingSet(ctx, viewer)
	if err != nil {
		return TimelinePage{}, err
	}
	authorIDs, err := s.filteredAuthorIDs(ctx, params.filter)
	if err != nil {
		return TimelinePage{}, err
	}
	followingIDs := domain.SortedIDs(following)

	fetch := func(fctx context.Context, after page.TimeCursor, batchLimit int) ([]timelineCandidate, error) {
		return s.fetchTimelineCandidates(fctx, viewer, params, followingIDs, authorIDs, after, batchLimit)
	}
	keep := func(item timelineCandidate) bool {
		if item.post.ID == 0 {
			return false
		}
		if item.row.Kind == domain.KindRepost {
			// 纯转发只能指向公开的已发布原帖。
			return item.post.Status == model.PostStatusPublished &&
				item.post.Visibility == model.VisibilityPublic
		}
		return access.CanViewPostWithFollowing(viewer, item.post, following)
	}
	collected, _, hasMore, err := domain.CollectPage(ctx, cursor, limit, fetch, timelineCandidate.key, keep)
	if err != nil {
		return TimelinePage{}, s.wrap(err, "读取时间线失败")
	}
	next := page.TimeCursor{}
	if len(collected) > 0 {
		lastTime, lastID := collected[len(collected)-1].key()
		next = page.TimeCursor{Time: lastTime, ID: lastID}
	} else {
		hasMore = false
	}
	briefs, err := s.assembleTimeline(ctx, viewer, following, collected)
	if err != nil {
		return TimelinePage{}, err
	}
	return TimelinePage{Kind: domain.TabPosts, Posts: briefs, Next: next, HasMore: hasMore}, nil
}

// filteredAuthorIDs 把 human/ai 过滤解析为作者 ID 集合（跨模块经 auth 端口）。
func (s *Service) filteredAuthorIDs(ctx context.Context, filter string) ([]int64, error) {
	if filter != domain.FilterHuman && filter != domain.FilterAI {
		return nil, nil
	}
	if s.deps.Users == nil {
		return nil, apperr.Internal("账号服务未就绪")
	}
	ids, err := s.deps.Users.ListUserIDsByAIFlag(ctx, filter == domain.FilterAI)
	if err != nil {
		return nil, s.wrap(err, "读取作者类型过滤集合失败")
	}
	return ids, nil
}

// fetchTimelineCandidates 拉取一批候选：帖子分支（content 自有表）+ 纯转发分支（social 端口），
// 合并排序后附带帖子行，供可见性复核与组装复用。
func (s *Service) fetchTimelineCandidates(
	ctx context.Context,
	viewer model.Viewer,
	params timelineParams,
	followingIDs, authorIDs []int64,
	after page.TimeCursor,
	batchLimit int,
) ([]timelineCandidate, error) {
	rows, err := s.repo.TimelinePostCandidates(ctx, repo.TimelineQuery{
		ViewerID:      viewer.ID,
		ViewerIsAI:    viewer.IsAI,
		FollowingIDs:  followingIDs,
		AuthorIDs:     authorIDs,
		IncludePosts:  params.includePosts,
		QuotedOnly:    params.quotedOnly,
		FollowingOnly: params.followingOnly,
		AuthorID:      params.authorID,
		Filter:        params.filter,
		TagName:       params.tagName,
		SearchText:    params.searchText,
		AfterTime:     after.Time,
		AfterID:       after.ID,
		Limit:         batchLimit,
	})
	if err != nil {
		return nil, err
	}
	candidates := make([]timelineCandidate, 0, len(rows)+batchLimit)
	for _, row := range rows {
		candidates = append(candidates, timelineCandidate{row: row})
	}
	if params.includeReposts && s.deps.Social != nil {
		events, err := s.deps.Social.ListTimelineReposts(ctx, contracts.RepostTimelineQuery{
			AuthorID:     params.authorID,
			FollowingIDs: selectFollowingIDs(params.followingOnly, followingIDs),
			Filter:       params.filter,
			AfterAt:      after.Time,
			AfterID:      after.ID,
			Limit:        batchLimit,
		})
		if err != nil {
			return nil, s.wrap(err, "读取转发时间线失败")
		}
		for _, event := range events {
			createdAt := event.CreatedAt
			candidates = append(candidates, timelineCandidate{row: repo.CandidateRow{
				Kind:       domain.KindRepost,
				ItemID:     event.ID,
				SortAt:     createdAt,
				ReposterID: event.UserID,
				RepostedAt: &createdAt,
				PostID:     event.PostID,
			}})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if !candidates[i].row.SortAt.Equal(candidates[j].row.SortAt) {
			return candidates[i].row.SortAt.After(candidates[j].row.SortAt)
		}
		return candidates[i].row.ItemID > candidates[j].row.ItemID
	})
	if len(candidates) > batchLimit {
		candidates = candidates[:batchLimit]
	}
	postIDs := make([]int64, 0, len(candidates))
	for _, item := range candidates {
		postIDs = append(postIDs, item.row.PostID)
	}
	posts, err := s.repo.GetPosts(ctx, postIDs)
	if err != nil {
		return nil, s.wrap(err, "读取时间线帖子失败")
	}
	for i := range candidates {
		candidates[i].post = posts[candidates[i].row.PostID]
	}
	return candidates, nil
}

// selectFollowingIDs 关注时间线需要把关注集合传给 social 的转发查询。
func selectFollowingIDs(followingOnly bool, followingIDs []int64) []int64 {
	if !followingOnly {
		return nil
	}
	return followingIDs
}

// assembleTimeline 把候选批量组装成时间线列表项。
func (s *Service) assembleTimeline(
	ctx context.Context,
	viewer model.Viewer,
	following map[int64]bool,
	items []timelineCandidate,
) ([]PostBrief, error) {
	postIDs := make([]int64, 0, len(items))
	userIDs := make([]int64, 0, len(items))
	for _, item := range items {
		postIDs = append(postIDs, item.row.PostID)
		if item.row.Kind == domain.KindRepost && item.row.ReposterID > 0 {
			userIDs = append(userIDs, item.row.ReposterID)
		}
	}
	if len(items) == 0 {
		return []PostBrief{}, nil
	}
	data, err := s.loadBriefData(ctx, viewer, following, postIDs, userIDs)
	if err != nil {
		return nil, err
	}
	out := make([]PostBrief, 0, len(items))
	for _, item := range items {
		post, ok := data.posts[item.row.PostID]
		if !ok {
			continue
		}
		if item.row.Kind == domain.KindRepost {
			outer := data.brief(post, briefOptions{
				kind:       domain.KindRepost,
				authorID:   item.row.ReposterID,
				blankBody:  true,
				repostedAt: item.row.RepostedAt,
			})
			outer.ID = item.row.ItemID
			inner := data.brief(post, briefOptions{
				kind: domain.KindPost, authorID: post.AuthorID, nested: true,
			})
			outer.RepostOf = &inner
			out = append(out, outer)
			continue
		}
		out = append(out, data.brief(post, briefOptions{kind: domain.KindPost, authorID: post.AuthorID}))
	}
	return out, nil
}

// userTimelineReplies 组装个人主页「回复」页签。
func (s *Service) userTimelineReplies(
	ctx context.Context,
	viewer model.Viewer,
	authorID int64,
	cursor page.TimeCursor,
	limit int,
) (TimelinePage, error) {
	if s.deps.Social == nil {
		return TimelinePage{}, apperr.Internal("互动服务未就绪")
	}
	following, err := s.followingSet(ctx, viewer)
	if err != nil {
		return TimelinePage{}, err
	}
	fetch := func(fctx context.Context, after page.TimeCursor, batchLimit int) ([]commentCandidate, error) {
		comments, err := s.deps.Social.ListUserReplies(fctx, contracts.ReplyQuery{
			AuthorID: authorID,
			AfterAt:  after.Time,
			AfterID:  after.ID,
			Limit:    batchLimit,
		})
		if err != nil {
			return nil, s.wrap(err, "读取回复时间线失败")
		}
		postIDs := make([]int64, 0, len(comments))
		for _, comment := range comments {
			postIDs = append(postIDs, comment.PostID)
		}
		posts, err := s.repo.GetPosts(fctx, postIDs)
		if err != nil {
			return nil, s.wrap(err, "读取回复所属帖子失败")
		}
		items := make([]commentCandidate, 0, len(comments))
		for _, comment := range comments {
			items = append(items, commentCandidate{comment: comment, post: posts[comment.PostID]})
		}
		return items, nil
	}
	keep := func(item commentCandidate) bool {
		return item.post.ID != 0 && domain.CanViewPublished(viewer, item.post, following)
	}
	collected, _, hasMore, err := domain.CollectPage(
		ctx, cursor, limit, fetch,
		func(item commentCandidate) (time.Time, int64) { return item.comment.CreatedAt, item.comment.ID },
		keep,
	)
	if err != nil {
		return TimelinePage{}, err
	}
	next := page.TimeCursor{}
	if len(collected) > 0 {
		last := collected[len(collected)-1].comment
		next = page.TimeCursor{Time: last.CreatedAt, ID: last.ID}
	} else {
		hasMore = false
	}
	replies, err := s.assembleReplies(ctx, viewer, following, collected)
	if err != nil {
		return TimelinePage{}, err
	}
	return TimelinePage{Kind: domain.TabReplies, Replies: replies, Next: next, HasMore: hasMore}, nil
}

// commentCandidate 是回复时间线候选。
type commentCandidate struct {
	comment model.Comment
	post    model.Post
}

// assembleReplies 把回复候选组装成列表项，并附带所属帖子的列表项。
func (s *Service) assembleReplies(
	ctx context.Context,
	viewer model.Viewer,
	following map[int64]bool,
	items []commentCandidate,
) ([]CommentTimelineItem, error) {
	if len(items) == 0 {
		return []CommentTimelineItem{}, nil
	}
	postIDs := make([]int64, 0, len(items))
	userIDs := make([]int64, 0, len(items))
	for _, item := range items {
		postIDs = append(postIDs, item.comment.PostID)
		userIDs = append(userIDs, item.comment.AuthorID)
	}
	data, err := s.loadBriefData(ctx, viewer, following, postIDs, userIDs)
	if err != nil {
		return nil, err
	}
	out := make([]CommentTimelineItem, 0, len(items))
	for _, item := range items {
		post, ok := data.posts[item.comment.PostID]
		if !ok {
			continue
		}
		brief := data.brief(post, briefOptions{kind: domain.KindPost, authorID: post.AuthorID})
		author := model.UserBrief{ID: item.comment.AuthorID}
		if found, ok := data.authors[item.comment.AuthorID]; ok {
			author = found
		}
		out = append(out, CommentTimelineItem{
			Kind:        "comment",
			ID:          item.comment.ID,
			Author:      author,
			ContentHTML: item.comment.ContentHTML,
			CreatedAt:   item.comment.CreatedAt,
			Post:        &brief,
		})
	}
	return out, nil
}
