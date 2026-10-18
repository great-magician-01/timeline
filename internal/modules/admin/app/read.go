package app

import (
	"context"
	"strings"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/shared/search"
)

// ===== 分页与错误辅助 =====

// pageOf 把 repo 返回的 limit+1 条结果裁剪为一页，并计算下一页游标值。
func pageOf[T any](items []T, limit int, id func(T) int64) Page[T] {
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	var lastID int64
	if len(items) > 0 {
		lastID = id(items[len(items)-1])
	}
	return Page[T]{Items: items, LastID: lastID, HasMore: hasMore}
}

// wrapInternal 把底层错误包装为内部错误：对外文案由 httpx 使用默认 500 文案，
// 底层原因保留在错误链中供访问日志检索。
func wrapInternal(err error) error {
	return apperr.Wrap(err, apperr.KindInternal, "")
}

// keyword 归一化搜索关键词（去空白 + 小写 + LIKE 通配符转义），并校验长度上限。
// repo 侧以 LOWER(x) LIKE '%' || $n || '%' ESCAPE '\' 做字面量子串匹配（可命中 trigram 索引）。
func normalizeKeyword(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	if len([]rune(trimmed)) > 100 {
		return "", apperr.Invalid("搜索关键词过长")
	}
	return search.EscapeLikePattern(strings.ToLower(trimmed)), nil
}

// ===== 概览 =====

// GetOverview 汇总账号、内容、AI 与后台任务概况。
func (s *Service) GetOverview(ctx context.Context) (Overview, error) {
	counts, err := s.read.Overview(ctx)
	if err != nil {
		return Overview{}, wrapInternal(err)
	}
	values, err := s.settings.Settings(ctx)
	if err != nil {
		return Overview{}, wrapInternal(err)
	}
	return Overview{Counts: counts, AIGlobalEnabled: values.AIGlobalEnabled}, nil
}

// ===== 用户读取 =====

// ListUsers 分页读取账号列表（用户名/昵称/邮箱关键词、状态、是否 AI 过滤）。
func (s *Service) ListUsers(ctx context.Context, query UsersQuery) (Page[model.User], error) {
	if query.Status != "" && query.Status != model.StatusActive && query.Status != model.StatusSuspended {
		return Page[model.User]{}, apperr.Invalid("status 只能是 active 或 suspended")
	}
	keyword, err := normalizeKeyword(query.Keyword)
	if err != nil {
		return Page[model.User]{}, err
	}
	query.Keyword = keyword
	users, err := s.read.ListUsers(ctx, query)
	if err != nil {
		return Page[model.User]{}, wrapInternal(err)
	}
	return pageOf(users, query.Limit, func(u model.User) int64 { return u.ID }), nil
}

// GetUser 读取用户详情：账号行、有效会话数与内容关系统计。
func (s *Service) GetUser(ctx context.Context, userID int64) (UserDetail, error) {
	user, err := s.read.GetUser(ctx, userID)
	if err != nil {
		return UserDetail{}, err
	}
	activeSessions, err := s.read.CountActiveSessions(ctx, userID, s.now())
	if err != nil {
		return UserDetail{}, wrapInternal(err)
	}
	stats, err := s.read.UserStats(ctx, userID)
	if err != nil {
		return UserDetail{}, wrapInternal(err)
	}
	return UserDetail{User: user, ActiveSessions: activeSessions, Stats: stats}, nil
}

// ===== 内容审核读取 =====

// ListPosts 分页读取全部可见性级别的帖子（管理员审核用）。
func (s *Service) ListPosts(ctx context.Context, query PostsQuery) (Page[PostItem], error) {
	switch query.Visibility {
	case "", model.VisibilityPublic, model.VisibilityFollowers, model.VisibilityPrivate:
	default:
		return Page[PostItem]{}, apperr.Invalid("visibility 取值非法")
	}
	switch query.Status {
	case "", model.PostStatusDraft, model.PostStatusScheduled, model.PostStatusPublished, model.PostStatusDeleted:
	default:
		return Page[PostItem]{}, apperr.Invalid("status 取值非法")
	}
	keyword, err := normalizeKeyword(query.Keyword)
	if err != nil {
		return Page[PostItem]{}, err
	}
	query.Keyword = keyword
	items, err := s.read.ListPosts(ctx, query)
	if err != nil {
		return Page[PostItem]{}, wrapInternal(err)
	}
	return pageOf(items, query.Limit, func(item PostItem) int64 { return item.Post.ID }), nil
}

// GetPost 读取帖子审核详情：正文（版本优先，草稿兜底）、历史版本、附件与评论上下文。
func (s *Service) GetPost(ctx context.Context, postID int64) (PostDetail, error) {
	post, err := s.read.GetPost(ctx, postID)
	if err != nil {
		return PostDetail{}, err
	}
	content, err := s.read.PostContentHTML(ctx, post.Post)
	if err != nil {
		return PostDetail{}, wrapInternal(err)
	}
	versions, err := s.read.PostVersions(ctx, postID)
	if err != nil {
		return PostDetail{}, wrapInternal(err)
	}
	attachments, err := s.read.PostAttachments(ctx, postID)
	if err != nil {
		return PostDetail{}, wrapInternal(err)
	}
	comments, err := s.read.PostCommentContext(ctx, postID, postContextCommentLimit)
	if err != nil {
		return PostDetail{}, wrapInternal(err)
	}
	return PostDetail{
		Post:        post,
		ContentHTML: content,
		Versions:    versions,
		Attachments: attachments,
		Comments:    comments,
		// 评论总数复用已读到的 posts.comment_count 冗余计数列，避免重复 COUNT（口径一致）。
		CommentCount: int64(post.Post.CommentCount),
	}, nil
}

// postContextCommentLimit 是审核详情展示的顶层评论上限（与旧实现一致）。
const postContextCommentLimit = 50

// ListComments 分页读取评论审核列表。
func (s *Service) ListComments(ctx context.Context, query CommentsQuery) (Page[CommentItem], error) {
	keyword, err := normalizeKeyword(query.Keyword)
	if err != nil {
		return Page[CommentItem]{}, err
	}
	query.Keyword = keyword
	items, err := s.read.ListComments(ctx, query)
	if err != nil {
		return Page[CommentItem]{}, wrapInternal(err)
	}
	return pageOf(items, query.Limit, func(item CommentItem) int64 { return item.ID }), nil
}

// ListAttachments 分页读取附件审核列表。
func (s *Service) ListAttachments(ctx context.Context, query AttachmentsQuery) (Page[AttachmentItem], error) {
	if query.Purpose != "" && query.Purpose != "post" && query.Purpose != "avatar" {
		return Page[AttachmentItem]{}, apperr.Invalid("purpose 只能是 post 或 avatar")
	}
	if query.Status != "" && query.Status != model.AttachmentAvailable && query.Status != model.AttachmentDeleted {
		return Page[AttachmentItem]{}, apperr.Invalid("status 只能是 available 或 deleted")
	}
	keyword, err := normalizeKeyword(query.Keyword)
	if err != nil {
		return Page[AttachmentItem]{}, err
	}
	query.Keyword = keyword
	items, err := s.read.ListAttachments(ctx, query)
	if err != nil {
		return Page[AttachmentItem]{}, wrapInternal(err)
	}
	return pageOf(items, query.Limit, func(item AttachmentItem) int64 { return item.ID }), nil
}

// ===== 后台任务与审计读取 =====

// ListTasks 分页读取后台作业列表。
func (s *Service) ListTasks(ctx context.Context, query TasksQuery) (Page[TaskItem], error) {
	items, err := s.read.ListTasks(ctx, query)
	if err != nil {
		return Page[TaskItem]{}, wrapInternal(err)
	}
	return pageOf(items, query.Limit, func(item TaskItem) int64 { return item.ID }), nil
}

// ListAuditLogs 分页读取管理审计日志。
func (s *Service) ListAuditLogs(ctx context.Context, query AuditQuery) (Page[AuditItem], error) {
	items, err := s.read.ListAuditLogs(ctx, query)
	if err != nil {
		return Page[AuditItem]{}, wrapInternal(err)
	}
	return pageOf(items, query.Limit, func(item AuditItem) int64 { return item.ID }), nil
}
