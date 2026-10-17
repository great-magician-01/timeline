package app

import (
	"context"
	"strings"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/content/domain"
	"github.com/great-magician-01/timeline/internal/modules/content/repo"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// CreatePostForActor 在调用方事务内创建帖子：草稿、直接发布或定时发布
// （含版本、标签、搜索、提及）。作者账号停用时返回领域 Suspended；
// 引用的原帖必须公开、已发布且作者未停用。
func (s *Service) CreatePostForActor(
	ctx context.Context,
	in contracts.ActorPostInput,
) (model.Post, error) {
	if in.AuthorID <= 0 {
		return model.Post{}, apperr.Validation("缺少作者账号")
	}
	author, err := s.loadUser(ctx, in.AuthorID, "作者账号不存在")
	if err != nil {
		return model.Post{}, err
	}
	if author.IsSuspended() {
		return model.Post{}, apperr.Suspended("账号已停用，不能创建内容")
	}
	now := in.At
	if now.IsZero() {
		now = s.Now()
	}
	// 定时发布优先于草稿/直接发布；时间校验必须在写入前完成，避免留下半成品。
	action := domain.ActionPublish
	if in.AsDraft {
		action = domain.ActionDraft
	}
	var scheduledAt *time.Time
	if in.ScheduledAt != nil {
		if err := domain.ValidateFutureScheduledAt(*in.ScheduledAt, s.Now()); err != nil {
			return model.Post{}, err
		}
		scheduledAt = in.ScheduledAt
		action = domain.ActionSchedule
	}
	content, err := s.prepareContent(ctx, author, contentInput{
		Format:        in.ContentFormat,
		Source:        in.ContentSource,
		Visibility:    in.Visibility,
		AttachmentIDs: in.AttachmentIDs,
		QuotedPostID:  in.QuotedPostID,
	}, true)
	if err != nil {
		return model.Post{}, err
	}
	postID, err := s.nextID()
	if err != nil {
		return model.Post{}, err
	}
	err = s.withTx(ctx, func(txCtx context.Context) error {
		post := model.Post{
			ID:           postID,
			AuthorID:     in.AuthorID,
			Status:       model.PostStatusDraft,
			Visibility:   content.Visibility,
			QuotedPostID: content.QuotedPostID,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		if err := s.repo.InsertPost(txCtx, post); err != nil {
			return s.wrap(err, "写入帖子失败")
		}
		if err := s.repo.InsertDraft(txCtx, repo.Draft{
			PostID:    postID,
			Format:    content.Format,
			Source:    content.Source,
			HTML:      content.HTML,
			Plain:     content.Plain,
			Revision:  1,
			UpdatedAt: now,
		}); err != nil {
			return s.wrap(err, "写入草稿失败")
		}
		if err := s.repo.ReplaceDraftAttachments(txCtx, postID, content.AttachmentIDs); err != nil {
			return s.wrap(err, "保存草稿附件失败")
		}
		return s.applyCreateAction(txCtx, postID, in.AuthorID, action, scheduledAt, now)
	})
	if err != nil {
		return model.Post{}, err
	}
	return s.repo.GetPost(ctx, postID)
}

// PublishPostForActor 立即发布指定草稿；已发布时幂等返回 nil。
func (s *Service) PublishPostForActor(
	ctx context.Context,
	in contracts.PublishPostForActorInput,
) error {
	return s.withTx(ctx, func(txCtx context.Context) error {
		post, err := s.repo.GetPost(txCtx, in.PostID)
		if err != nil {
			return err
		}
		if post.Status == model.PostStatusPublished {
			return nil
		}
		if !domain.CanPublish(post.Status) {
			return apperr.Forbidden("当前内容状态不能发布")
		}
		return s.publishLocked(txCtx, in.PostID, in.ActorID)
	})
}

// UpdateDraftForActor 以指定作者身份覆盖草稿工作区内容（管理员编辑 AI 草稿用），
// 不做 revision 校验；已发布内容会追加一个新版本，可见性与附件沿用当前状态。
func (s *Service) UpdateDraftForActor(
	ctx context.Context,
	in contracts.UpdateDraftForActorInput,
) error {
	now := in.At
	if now.IsZero() {
		now = s.Now()
	}
	html, plain, err := domain.RenderContent(in.ContentFormat, in.ContentSource)
	if err != nil {
		return err
	}
	normalizedFormat, err := domain.NormalizeFormat(in.ContentFormat)
	if err != nil {
		return err
	}
	post, err := s.repo.GetPost(ctx, in.PostID)
	if err != nil {
		return err
	}
	if post.Status == model.PostStatusDeleted {
		return apperr.NotFound("内容不存在")
	}
	if post.Status == model.PostStatusPublished {
		return s.withTx(ctx, func(txCtx context.Context) error {
			attachments, err := s.repo.DraftAttachmentIDs(txCtx, in.PostID)
			if err != nil {
				return s.wrap(err, "读取草稿附件失败")
			}
			_, err = s.appendVersion(txCtx, post, domain.Content{
				Format:        normalizedFormat,
				Source:        in.ContentSource,
				HTML:          html,
				Plain:         plain,
				Visibility:    post.Visibility,
				AttachmentIDs: attachments,
			})
			return err
		})
	}
	return s.repo.SaveDraftContent(ctx, in.PostID, normalizedFormat, in.ContentSource,
		html, plain, now, true)
}

// DeletePostAsAdmin 管理员逻辑删除帖子；审计在同一事务内写入。
func (s *Service) DeletePostAsAdmin(ctx context.Context, in contracts.DeletePostInput) error {
	viewer := model.Viewer{ID: in.AdminID, IsAdmin: true, Authenticated: true}
	return s.deletePost(ctx, viewer, in.PostID, true)
}

// BumpPostCounter 原子增减帖子计数器并返回最新值；计数不允许为负由条件更新兜底。
func (s *Service) BumpPostCounter(
	ctx context.Context,
	in contracts.PostCounterInput,
) (int, error) {
	return s.adjustPostCounter(ctx, in)
}

// AdjustPostCounter 与 BumpPostCounter 同义（social 模块使用的命名）。
func (s *Service) AdjustPostCounter(
	ctx context.Context,
	in contracts.PostCounterInput,
) (int, error) {
	return s.adjustPostCounter(ctx, in)
}

func (s *Service) adjustPostCounter(
	ctx context.Context,
	in contracts.PostCounterInput,
) (int, error) {
	if err := domain.ValidateCounterDelta(in.Delta); err != nil {
		return 0, err
	}
	at := in.At
	if at.IsZero() {
		at = s.Now()
	}
	value, err := s.repo.BumpCounter(ctx, in.PostID, in.Counter, in.Delta, at)
	if err != nil {
		return 0, s.wrap(err, "更新帖子计数器失败")
	}
	return value, nil
}

// GetPost 按 ID 读取帖子行（不过滤状态/可见性）。
func (s *Service) GetPost(ctx context.Context, postID int64) (model.Post, error) {
	return s.repo.GetPost(ctx, postID)
}

// GetPosts 批量读取帖子行，返回 id → Post；缺失的 ID 不出现在 map 中。
func (s *Service) GetPosts(ctx context.Context, postIDs []int64) (map[int64]model.Post, error) {
	posts, err := s.repo.GetPosts(ctx, postIDs)
	if err != nil {
		return nil, s.wrap(err, "读取帖子失败")
	}
	return posts, nil
}

// GetVisiblePost 读取帖子并按访问者做可见性校验；不可见返回领域 NotFound。
func (s *Service) GetVisiblePost(
	ctx context.Context,
	viewer model.Viewer,
	postID int64,
) (model.Post, error) {
	post, err := s.repo.GetPost(ctx, postID)
	if err != nil {
		return model.Post{}, err
	}
	if _, err := s.requireVisible(ctx, viewer, post); err != nil {
		return model.Post{}, err
	}
	return post, nil
}

// GetPostExcerpt 读取已发布帖子的纯文本摘录（版本正文优先，搜索投影兜底）。
func (s *Service) GetPostExcerpt(ctx context.Context, postID int64) (string, error) {
	excerpt, err := s.repo.PostExcerpt(ctx, postID)
	if err != nil {
		return "", s.wrap(err, "读取帖子摘录失败")
	}
	return excerpt, nil
}

// GetPostPlainTexts 批量读取帖子搜索投影的纯文本（通知预览使用）。
func (s *Service) GetPostPlainTexts(
	ctx context.Context,
	postIDs []int64,
) (map[int64]string, error) {
	texts, err := s.repo.PostPlainTexts(ctx, postIDs)
	if err != nil {
		return nil, s.wrap(err, "读取帖子纯文本失败")
	}
	return texts, nil
}

// ListCandidatePosts 按候选范围读取公开已发布内容候选（AI 主动互动决策）。
func (s *Service) ListCandidatePosts(
	ctx context.Context,
	query contracts.CandidateQuery,
) ([]contracts.PostCandidate, error) {
	if query.ScopeType == "" || query.ScopeType == "none" {
		return nil, nil
	}
	if query.Limit <= 0 {
		query.Limit = 8
	}
	if query.Limit > 8 {
		query.Limit = 8
	}
	fetchLimit := query.Limit * 3
	repoQuery := repo.CandidatePostsQuery{
		ExcludeAuthorID: query.ExcludeAuthorID,
		Limit:           fetchLimit,
	}
	switch query.ScopeType {
	case "followed":
		if s.deps.Social == nil {
			return nil, nil
		}
		following, err := s.deps.Social.FollowingSet(ctx, query.ExcludeAuthorID)
		if err != nil {
			return nil, s.wrap(err, "读取关注集合失败")
		}
		repoQuery.UseAuthorFilter = true
		repoQuery.AuthorIDs = domain.SortedIDs(following)
	case "tags":
		repoQuery.Tags = normalizeScopeTerms(query.ScopeTags)
		if len(repoQuery.Tags) == 0 {
			return nil, nil
		}
	case "authors":
		if s.deps.Users == nil {
			return nil, nil
		}
		briefs, err := s.deps.Users.FindActiveUsersByUsernames(ctx, normalizeScopeTerms(query.ScopeUsernames))
		if err != nil {
			return nil, s.wrap(err, "解析候选作者失败")
		}
		repoQuery.UseAuthorFilter = true
		for _, brief := range briefs {
			repoQuery.AuthorIDs = append(repoQuery.AuthorIDs, brief.ID)
		}
		if len(repoQuery.AuthorIDs) == 0 {
			return nil, nil
		}
	default:
		return nil, nil
	}
	rows, err := s.repo.ListCandidatePosts(ctx, repoQuery)
	if err != nil {
		return nil, s.wrap(err, "读取候选内容失败")
	}
	if len(rows) == 0 {
		return nil, nil
	}
	authorIDs := make([]int64, 0, len(rows))
	for _, row := range rows {
		authorIDs = append(authorIDs, row.Post.AuthorID)
	}
	users := map[int64]model.User{}
	if s.deps.Users != nil {
		loaded, err := s.deps.Users.GetUsers(ctx, domain.UniqueIDs(authorIDs))
		if err != nil {
			return nil, s.wrap(err, "读取候选作者失败")
		}
		users = loaded
	}
	candidates := make([]contracts.PostCandidate, 0, query.Limit)
	for _, row := range rows {
		if len(candidates) >= query.Limit {
			break
		}
		author, ok := users[row.Post.AuthorID]
		if ok {
			// 只使用仍生效的账号；AI 间互动未开启时排除其他 AI 作者。
			if !author.IsActive() {
				continue
			}
			if author.IsAI && !query.AllowAIToAI {
				continue
			}
		}
		candidates = append(candidates, contracts.PostCandidate{
			Post:       row.Post,
			AuthorName: displayName(users[row.Post.AuthorID]),
			Username:   users[row.Post.AuthorID].Username,
			Summary:    row.Summary,
			Tags:       row.Tags,
		})
	}
	return candidates, nil
}

// ListDraftsByAuthor 按作者读取草稿列表（agent AI 草稿管理）。
func (s *Service) ListDraftsByAuthor(
	ctx context.Context,
	query contracts.DraftListQuery,
) ([]contracts.DraftSummary, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.repo.ListDraftsByAuthor(ctx, query.AuthorID, query.Cursor, limit)
	if err != nil {
		return nil, s.wrap(err, "读取草稿列表失败")
	}
	items := make([]contracts.DraftSummary, 0, len(rows))
	for _, row := range rows {
		items = append(items, contracts.DraftSummary{
			Post:          row.Post,
			ContentHTML:   row.ContentHTML,
			DraftRevision: row.DraftRevision,
		})
	}
	return items, nil
}

// ListPostIDsByAuthor 读取某作者的全部帖子 ID（含草稿/定时/已发布）。
func (s *Service) ListPostIDsByAuthor(ctx context.Context, authorID int64) ([]int64, error) {
	ids, err := s.repo.ListPostIDsByAuthor(ctx, authorID)
	if err != nil {
		return nil, s.wrap(err, "读取作者帖子失败")
	}
	return ids, nil
}

// DetachAttachmentFromDrafts 解除附件与草稿工作区的生效关联，返回是否发生变更。
// 关联表没有时间列，at 仅用于接口一致性（保留跨模块契约形状）。
func (s *Service) DetachAttachmentFromDrafts(
	ctx context.Context,
	attachmentID int64,
	at time.Time,
) (bool, error) {
	changed, err := s.repo.DetachAttachmentFromDrafts(ctx, attachmentID)
	if err != nil {
		return false, s.wrap(err, "解除草稿附件关联失败")
	}
	return changed, nil
}

// AttachmentReferencedByVersion 判断附件是否仍被任何历史版本引用。
func (s *Service) AttachmentReferencedByVersion(
	ctx context.Context,
	attachmentID int64,
) (bool, error) {
	referenced, err := s.repo.AttachmentReferencedByVersion(ctx, attachmentID)
	if err != nil {
		return false, s.wrap(err, "读取附件版本引用失败")
	}
	return referenced, nil
}

// normalizeScopeTerms 归一化候选范围参数：去空白、去重、最多 20 个。
func normalizeScopeTerms(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		term := strings.TrimSpace(value)
		if term == "" || seen[term] {
			continue
		}
		seen[term] = true
		out = append(out, term)
		if len(out) >= 20 {
			break
		}
	}
	return out
}

// displayName 返回账号的展示名：昵称优先，回退用户名。
func displayName(user model.User) string {
	if strings.TrimSpace(user.Nickname) != "" {
		return user.Nickname
	}
	return user.Username
}
