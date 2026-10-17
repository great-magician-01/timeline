package app

import (
	"context"
	"strings"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/content/domain"
	"github.com/great-magician-01/timeline/internal/modules/content/repo"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/access"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/format"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/shared/richtext"
)

// PostInput 是创建帖子的应用层输入（HTTP 层完成 JSON 解析与 ID 转换）。
type PostInput struct {
	ContentFormat string
	ContentSource string
	Visibility    string
	AttachmentIDs []int64
	QuotedPostID  *int64
	Action        string
	ScheduledAt   *string
}

// UpdatePostInput 是编辑帖子的应用层输入。
type UpdatePostInput struct {
	ContentFormat string
	ContentSource string
	Visibility    string
	AttachmentIDs []int64
	ScheduledAt   *string
	Revision      *int
}

// SchedulePostInput 是设置定时发布的输入。
type SchedulePostInput struct {
	ScheduledAt *string
}

// contentInput 是创建与更新共用、尚未渲染校验的内容输入。
type contentInput struct {
	Format        string
	Source        string
	Visibility    string
	AttachmentIDs []int64
	QuotedPostID  *int64
}

// CreatePost 创建草稿、立即发布或设置定时发布。
func (s *Service) CreatePost(ctx context.Context, viewer model.Viewer, in PostInput) (PostDetail, error) {
	if err := s.requireWritableViewer(viewer); err != nil {
		return PostDetail{}, err
	}
	action, err := domain.NormalizeAction(in.Action)
	if err != nil {
		return PostDetail{}, err
	}
	now := s.Now()
	var scheduledAt *time.Time
	if action == domain.ActionSchedule {
		raw := ""
		if in.ScheduledAt != nil {
			raw = *in.ScheduledAt
		}
		at, err := domain.ParseScheduledAt(raw, now)
		if err != nil {
			return PostDetail{}, err
		}
		scheduledAt = &at
	}
	if err := s.consumePostQuota(ctx, viewer.ID); err != nil {
		return PostDetail{}, err
	}
	author, err := s.loadUser(ctx, viewer.ID, "作者账号不存在")
	if err != nil {
		return PostDetail{}, err
	}
	content, err := s.prepareContent(ctx, author, contentInput{
		Format:        in.ContentFormat,
		Source:        in.ContentSource,
		Visibility:    in.Visibility,
		AttachmentIDs: in.AttachmentIDs,
		QuotedPostID:  in.QuotedPostID,
	}, true)
	if err != nil {
		return PostDetail{}, err
	}

	postID, err := s.nextID()
	if err != nil {
		return PostDetail{}, err
	}
	following, err := s.followingSet(ctx, viewer)
	if err != nil {
		return PostDetail{}, err
	}
	var detail PostDetail
	err = s.withTx(ctx, func(txCtx context.Context) error {
		post := model.Post{
			ID:           postID,
			AuthorID:     viewer.ID,
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
		if err := s.applyCreateAction(txCtx, postID, viewer.ID, action, scheduledAt, now); err != nil {
			return err
		}
		created, err := s.repo.GetPost(txCtx, postID)
		if err != nil {
			return err
		}
		detail, err = s.loadDetail(txCtx, viewer, following, created)
		return err
	})
	if err != nil {
		return PostDetail{}, err
	}
	return detail, nil
}

// applyCreateAction 按创建动作推进帖子状态；调用方必须已处于事务中。
func (s *Service) applyCreateAction(
	ctx context.Context,
	postID, authorID int64,
	action string,
	scheduledAt *time.Time,
	at time.Time,
) error {
	switch action {
	case domain.ActionDraft:
		return nil
	case domain.ActionPublish:
		return s.publishLocked(ctx, postID, authorID)
	case domain.ActionSchedule:
		if scheduledAt == nil {
			return apperr.Validation("请填写定时发布时间")
		}
		if err := s.repo.SetScheduled(ctx, postID, *scheduledAt, at); err != nil {
			return s.wrap(err, "设置定时发布失败")
		}
		return s.enqueuePublishJob(ctx, postID, *scheduledAt)
	default:
		return apperr.Validation("缺少或无效的操作类型")
	}
}

// ViewPost 返回帖子详情；不可见一律返回 not_found。
func (s *Service) ViewPost(ctx context.Context, viewer model.Viewer, postID int64) (PostDetail, error) {
	post, err := s.repo.GetPost(ctx, postID)
	if err != nil {
		return PostDetail{}, publicContentNotFound(err)
	}
	following, err := s.requireVisible(ctx, viewer, post)
	if err != nil {
		return PostDetail{}, err
	}
	return s.loadDetail(ctx, viewer, following, post)
}

// UpdatePost 保存草稿（自动保存）或为已发布内容追加新版本。
func (s *Service) UpdatePost(
	ctx context.Context,
	viewer model.Viewer,
	postID int64,
	in UpdatePostInput,
) (PostDetail, error) {
	if err := s.requireWritableViewer(viewer); err != nil {
		return PostDetail{}, err
	}
	if err := s.consumePostQuota(ctx, viewer.ID); err != nil {
		return PostDetail{}, err
	}
	author, err := s.loadUser(ctx, viewer.ID, "作者账号不存在")
	if err != nil {
		return PostDetail{}, err
	}
	// 引用目标在编辑阶段不可变更：历史版本不记录该字段，改动会导致时间线语义不一致。
	content, err := s.prepareContent(ctx, author, contentInput{
		Format:        in.ContentFormat,
		Source:        in.ContentSource,
		Visibility:    in.Visibility,
		AttachmentIDs: in.AttachmentIDs,
	}, false)
	if err != nil {
		return PostDetail{}, err
	}

	var detail PostDetail
	err = s.withTx(ctx, func(txCtx context.Context) error {
		post, err := s.repo.GetPostForUpdate(txCtx, postID)
		if err != nil {
			return err
		}
		if domain.IsDeleted(post.Status) {
			return apperr.NotFound("内容不存在或无权访问")
		}
		following, err := s.requireAuthor(txCtx, viewer, post)
		if err != nil {
			return err
		}
		if domain.Editable(post.Status) {
			post, err = s.updateDraftWorkspace(txCtx, post, content, in)
		} else {
			post, err = s.appendVersion(txCtx, post, content)
		}
		if err != nil {
			return err
		}
		detail, err = s.loadDetail(txCtx, viewer, following, post)
		return err
	})
	if err != nil {
		return PostDetail{}, err
	}
	return detail, nil
}

// updateDraftWorkspace 保存草稿/定时帖的编辑内容，并处理草稿版本冲突与定时时间调整。
func (s *Service) updateDraftWorkspace(
	ctx context.Context,
	post model.Post,
	content domain.Content,
	in UpdatePostInput,
) (model.Post, error) {
	if in.Revision == nil {
		return post, apperr.Validation("缺少草稿版本号").
			WithDetails(map[string]any{"revision": "草稿保存需要携带 revision"})
	}
	now := s.Now()
	draft, err := s.repo.GetDraftForUpdate(ctx, post.ID)
	if err != nil {
		return post, err
	}
	if *in.Revision != draft.Revision {
		return post, apperr.Conflict("draft_conflict", "草稿已被更新").WithDetails(map[string]any{
			"revision":       draft.Revision,
			"content_source": draft.Source,
			"content_html":   draft.HTML,
			"updated_at":     format.FormatTime(draft.UpdatedAt),
		})
	}
	if err := s.repo.SaveDraftContent(ctx, post.ID, content.Format, content.Source,
		content.HTML, content.Plain, now, true); err != nil {
		return post, s.wrap(err, "保存草稿失败")
	}
	if err := s.repo.ReplaceDraftAttachments(ctx, post.ID, content.AttachmentIDs); err != nil {
		return post, s.wrap(err, "保存草稿附件失败")
	}
	if err := s.repo.UpdateVisibility(ctx, post.ID, content.Visibility, now); err != nil {
		return post, s.wrap(err, "更新可见性失败")
	}
	// 定时帖允许在同一接口内调整发布时间；时间未变时不重建作业。
	if post.Status == model.PostStatusScheduled && in.ScheduledAt != nil &&
		strings.TrimSpace(*in.ScheduledAt) != "" {
		at, err := domain.ParseScheduledAt(*in.ScheduledAt, now)
		if err != nil {
			return post, err
		}
		if post.ScheduledAt == nil || post.ScheduledAt.Unix() != at.Unix() {
			if err := s.repo.SetScheduled(ctx, post.ID, at, now); err != nil {
				return post, s.wrap(err, "更新定时发布时间失败")
			}
			if err := s.cancelPublishJobs(ctx, post.ID); err != nil {
				return post, err
			}
			if err := s.enqueuePublishJob(ctx, post.ID, at); err != nil {
				return post, err
			}
		}
	}
	return s.repo.GetPost(ctx, post.ID)
}

// appendVersion 为已发布内容追加一个新版本，并重建标签、搜索与提及。
func (s *Service) appendVersion(
	ctx context.Context,
	post model.Post,
	content domain.Content,
) (model.Post, error) {
	now := s.Now()
	maxVersion, err := s.repo.MaxVersionNumber(ctx, post.ID)
	if err != nil {
		return post, s.wrap(err, "读取当前版本号失败")
	}
	versionID, err := s.nextID()
	if err != nil {
		return post, err
	}
	if err := s.repo.InsertVersion(ctx, model.PostVersion{
		ID:            versionID,
		PostID:        post.ID,
		VersionNumber: domain.NextVersionNumber(maxVersion),
		ContentFormat: content.Format,
		ContentSource: content.Source,
		ContentHTML:   content.HTML,
		PlainText:     content.Plain,
		Visibility:    content.Visibility,
		CreatedBy:     post.AuthorID,
		CreatedAt:     now,
	}); err != nil {
		return post, s.wrap(err, "写入新版本失败")
	}
	if err := s.repo.InsertVersionAttachments(ctx, versionID, content.AttachmentIDs); err != nil {
		return post, s.wrap(err, "写入版本附件失败")
	}
	if err := s.linkAttachments(ctx, post.ID, content.AttachmentIDs, now); err != nil {
		return post, err
	}
	if err := s.repo.SetCurrentVersionAndVisibility(ctx, post.ID, versionID, content.Visibility, now); err != nil {
		return post, s.wrap(err, "更新当前版本失败")
	}
	if err := s.repo.SyncDraftAfterPublish(ctx, post.ID, content.Format, content.Source,
		content.HTML, content.Plain, now); err != nil {
		return post, s.wrap(err, "同步草稿工作区失败")
	}
	updated, err := s.repo.GetPost(ctx, post.ID)
	if err != nil {
		return post, err
	}
	if err := s.rebuildDerived(ctx, updated, content.Plain, now); err != nil {
		return post, err
	}
	return updated, nil
}

// PublishPost 立即发布草稿或定时帖；已发布时幂等返回当前详情。
func (s *Service) PublishPost(
	ctx context.Context,
	viewer model.Viewer,
	postID int64,
) (PostDetail, error) {
	if err := s.requireWritableViewer(viewer); err != nil {
		return PostDetail{}, err
	}
	if err := s.consumePostQuota(ctx, viewer.ID); err != nil {
		return PostDetail{}, err
	}
	var detail PostDetail
	err := s.withTx(ctx, func(txCtx context.Context) error {
		post, err := s.repo.GetPostForUpdate(txCtx, postID)
		if err != nil {
			return err
		}
		if domain.IsDeleted(post.Status) {
			return apperr.NotFound("内容不存在或无权访问")
		}
		following, err := s.requireAuthor(txCtx, viewer, post)
		if err != nil {
			return err
		}
		if err := s.publishLocked(txCtx, postID, viewer.ID); err != nil {
			return err
		}
		if err := s.cancelPublishJobs(txCtx, postID); err != nil {
			return err
		}
		updated, err := s.repo.GetPost(txCtx, postID)
		if err != nil {
			return err
		}
		detail, err = s.loadDetail(txCtx, viewer, following, updated)
		return err
	})
	if err != nil {
		return PostDetail{}, err
	}
	return detail, nil
}

// publishLocked 把草稿或定时帖发布为已发布内容：写入首个版本、更新当前版本与发布时间，
// 并重建标签、搜索、提及与引用通知。调用方必须已处于事务中且已锁定帖子行。
func (s *Service) publishLocked(ctx context.Context, postID int64, actorID int64) error {
	post, err := s.repo.GetPostForUpdate(ctx, postID)
	if err != nil {
		return err
	}
	if post.Status == model.PostStatusPublished {
		return nil
	}
	if err := domain.CheckPublishState(post.Status); err != nil {
		return err
	}
	draft, err := s.repo.GetDraft(ctx, postID)
	if err != nil {
		return err
	}
	if draft.Plain == "" {
		return apperr.Validation("正文不能为空").
			WithDetails(map[string]any{"content_source": "正文不能为空"})
	}
	author, err := s.loadUser(ctx, post.AuthorID, "作者账号不存在")
	if err != nil {
		return err
	}
	if err := domain.CheckVisibilityPolicy(author.IsAI, post.Visibility); err != nil {
		return err
	}
	now := s.Now()
	publishedAt := now
	if post.PublishedAt != nil {
		publishedAt = *post.PublishedAt
	}
	versionCount, err := s.repo.CountVersions(ctx, postID)
	if err != nil {
		return s.wrap(err, "统计历史版本失败")
	}
	if versionCount == 0 {
		attachments, err := s.repo.DraftAttachmentIDs(ctx, postID)
		if err != nil {
			return s.wrap(err, "读取草稿附件失败")
		}
		versionID, err := s.nextID()
		if err != nil {
			return err
		}
		if err := s.repo.InsertVersion(ctx, model.PostVersion{
			ID:                    versionID,
			PostID:                postID,
			VersionNumber:         1,
			ContentFormat:         draft.Format,
			ContentSource:         draft.Source,
			ContentHTML:           draft.HTML,
			PlainText:             draft.Plain,
			Visibility:            post.Visibility,
			RestoredFromVersionID: nil,
			CreatedBy:             actorID,
			CreatedAt:             now,
		}); err != nil {
			return s.wrap(err, "写入首个版本失败")
		}
		if err := s.repo.InsertVersionAttachments(ctx, versionID, attachments); err != nil {
			return s.wrap(err, "写入版本附件失败")
		}
		if err := s.linkAttachments(ctx, postID, attachments, now); err != nil {
			return err
		}
		if err := s.repo.SetPublished(ctx, postID, versionID, publishedAt, now); err != nil {
			return s.wrap(err, "更新发布状态失败")
		}
	} else {
		// 重试或重复投递：只推进状态，不追加版本，也不再改动首次发布时间。
		if err := s.repo.SetPublishedWithoutVersion(ctx, postID, publishedAt, now); err != nil {
			return s.wrap(err, "更新发布状态失败")
		}
	}
	updated, err := s.repo.GetPost(ctx, postID)
	if err != nil {
		return err
	}
	return s.rebuildDerived(ctx, updated, draft.Plain, now)
}

// SchedulePost 设置或修改定时发布时间。
func (s *Service) SchedulePost(
	ctx context.Context,
	viewer model.Viewer,
	postID int64,
	in SchedulePostInput,
) (PostDetail, error) {
	if err := s.requireWritableViewer(viewer); err != nil {
		return PostDetail{}, err
	}
	raw := ""
	if in.ScheduledAt != nil {
		raw = *in.ScheduledAt
	}
	at, err := domain.ParseScheduledAt(raw, s.Now())
	if err != nil {
		return PostDetail{}, err
	}
	if err := s.consumePostQuota(ctx, viewer.ID); err != nil {
		return PostDetail{}, err
	}
	var detail PostDetail
	err = s.withTx(ctx, func(txCtx context.Context) error {
		post, err := s.repo.GetPostForUpdate(txCtx, postID)
		if err != nil {
			return err
		}
		if domain.IsDeleted(post.Status) {
			return apperr.NotFound("内容不存在或无权访问")
		}
		following, err := s.requireAuthor(txCtx, viewer, post)
		if err != nil {
			return err
		}
		if !domain.CanSetSchedule(post.Status) {
			return apperr.Forbidden("已发布的内容不能设置定时发布")
		}
		now := s.Now()
		if err := s.repo.SetScheduled(txCtx, postID, at, now); err != nil {
			return s.wrap(err, "更新定时发布时间失败")
		}
		if err := s.cancelPublishJobs(txCtx, postID); err != nil {
			return err
		}
		if err := s.enqueuePublishJob(txCtx, postID, at); err != nil {
			return err
		}
		updated, err := s.repo.GetPost(txCtx, postID)
		if err != nil {
			return err
		}
		detail, err = s.loadDetail(txCtx, viewer, following, updated)
		return err
	})
	if err != nil {
		return PostDetail{}, err
	}
	return detail, nil
}

// CancelSchedule 取消定时，帖子回到草稿状态。
func (s *Service) CancelSchedule(
	ctx context.Context,
	viewer model.Viewer,
	postID int64,
) (PostDetail, error) {
	if err := s.requireWritableViewer(viewer); err != nil {
		return PostDetail{}, err
	}
	var detail PostDetail
	err := s.withTx(ctx, func(txCtx context.Context) error {
		post, err := s.repo.GetPostForUpdate(txCtx, postID)
		if err != nil {
			return err
		}
		if domain.IsDeleted(post.Status) {
			return apperr.NotFound("内容不存在或无权访问")
		}
		following, err := s.requireAuthor(txCtx, viewer, post)
		if err != nil {
			return err
		}
		if !domain.CanCancelSchedule(post.Status) {
			return apperr.Forbidden("已发布的内容不能取消定时")
		}
		if post.Status == model.PostStatusScheduled {
			if err := s.repo.RevertToDraft(txCtx, postID, s.Now()); err != nil {
				return s.wrap(err, "取消定时失败")
			}
			if err := s.cancelPublishJobs(txCtx, postID); err != nil {
				return err
			}
		}
		updated, err := s.repo.GetPost(txCtx, postID)
		if err != nil {
			return err
		}
		detail, err = s.loadDetail(txCtx, viewer, following, updated)
		return err
	})
	if err != nil {
		return PostDetail{}, err
	}
	return detail, nil
}

// DeletePost 逻辑删除帖子：作者或管理员。管理员删除时在同一事务内写审计。
func (s *Service) DeletePost(ctx context.Context, viewer model.Viewer, postID int64) error {
	if !viewer.Authenticated {
		return apperr.Unauthenticated("请先登录")
	}
	if viewer.IsSuspended {
		return apperr.Suspended("账号已停用，不能执行此操作")
	}
	if viewer.IsAI {
		return apperr.Forbidden("AI 账号不能通过此接口操作")
	}
	return s.deletePost(ctx, viewer, postID, viewer.IsAdmin)
}

// deletePost 是删除用例内核：author 自行删除不写审计，管理员删除写审计。
func (s *Service) deletePost(ctx context.Context, viewer model.Viewer, postID int64, asAdmin bool) error {
	return s.withTx(ctx, func(txCtx context.Context) error {
		post, err := s.repo.GetPostForUpdate(txCtx, postID)
		if err != nil {
			return err
		}
		if !asAdmin && post.AuthorID != viewer.ID {
			following, err := s.followingSet(txCtx, viewer)
			if err != nil {
				return err
			}
			if err := access.RequireVisiblePost(viewer, post, following); err != nil {
				return err
			}
			return apperr.Forbidden("只有作者或管理员可以删除内容")
		}
		if post.Status == model.PostStatusDeleted {
			return nil
		}
		now := s.Now()
		if err := s.repo.SoftDelete(txCtx, postID, now); err != nil {
			return s.wrap(err, "删除帖子失败")
		}
		if err := s.cancelPublishJobs(txCtx, postID); err != nil {
			return err
		}
		// 搜索投影同步清空（逻辑删除，不物理删除行）。
		if err := s.repo.ClearSearchText(txCtx, postID, now); err != nil {
			return s.wrap(err, "清理搜索投影失败")
		}
		if s.deps.Notice != nil {
			if err := s.deps.Notice.DeactivateMentions(txCtx, model.NoticeSourcePost, postID, now); err != nil {
				return s.wrap(err, "停用提及失败")
			}
		}
		if asAdmin && s.deps.Audit != nil {
			authorID := post.AuthorID
			targetID := postID
			if err := s.deps.Audit.Write(txCtx, contracts.AuditEntry{
				AdminID:    viewer.ID,
				Action:     contracts.AuditActionPostDelete,
				TargetType: "post",
				TargetID:   &targetID,
				Detail: map[string]any{
					"author_id": format.FormatID(authorID),
					"source":    "post_delete",
				},
				At: now,
			}); err != nil {
				return s.wrap(err, "写入删除审计失败")
			}
		}
		return nil
	})
}

// prepareContent 校验并渲染正文、可见性、附件与引用目标。
// allowQuote 为真时校验 quoted_post_id 必须指向公开的已发布帖子。
func (s *Service) prepareContent(
	ctx context.Context,
	author model.User,
	in contentInput,
	allowQuote bool,
) (domain.Content, error) {
	html, plain, err := domain.RenderContent(in.Format, in.Source)
	if err != nil {
		return domain.Content{}, err
	}
	formatValue, err := domain.NormalizeFormat(in.Format)
	if err != nil {
		return domain.Content{}, err
	}
	visibility, err := domain.NormalizeVisibility(in.Visibility)
	if err != nil {
		return domain.Content{}, err
	}
	if err := domain.CheckVisibilityPolicy(author.IsAI, visibility); err != nil {
		return domain.Content{}, err
	}
	attachmentIDs := domain.UniqueIDs(in.AttachmentIDs)
	if len(attachmentIDs) > 0 {
		if s.deps.Media == nil {
			return domain.Content{}, apperr.Internal("附件服务未就绪")
		}
		if err := s.deps.Media.CheckAttachmentsUsable(ctx, contracts.AttachmentCheckInput{
			UploaderID:    author.ID,
			AttachmentIDs: attachmentIDs,
			Purpose:       domain.AttachmentPurposePost,
		}); err != nil {
			return domain.Content{}, s.wrap(err, "校验附件使用权限失败")
		}
	}
	content := domain.Content{
		Format:        formatValue,
		Source:        in.Source,
		HTML:          html,
		Plain:         plain,
		Visibility:    visibility,
		AttachmentIDs: attachmentIDs,
	}
	if allowQuote && in.QuotedPostID != nil {
		if err := s.ensureQuotable(ctx, *in.QuotedPostID); err != nil {
			return domain.Content{}, err
		}
		content.QuotedPostID = in.QuotedPostID
	}
	return content, nil
}

// ensureQuotable 校验引用目标只能是公开的已发布帖子，且原帖作者未停用。
func (s *Service) ensureQuotable(ctx context.Context, quotedID int64) error {
	quoted, err := s.repo.GetPost(ctx, quotedID)
	if err != nil {
		if apperr.IsKind(err, apperr.KindNotFound) {
			return apperr.NotFound("引用的内容不存在")
		}
		return err
	}
	if quoted.Status != model.PostStatusPublished || quoted.Visibility != model.VisibilityPublic {
		return apperr.Validation("只能引用公开的已发布内容").
			WithDetails(map[string]any{"quoted_post_id": "只能引用公开的已发布内容"})
	}
	if s.deps.Users == nil {
		return nil
	}
	suspended, err := s.deps.Users.IsSuspended(ctx, quoted.AuthorID)
	if err != nil {
		return s.wrap(err, "读取原帖作者状态失败")
	}
	if suspended {
		return apperr.Forbidden("原帖作者账号已停用，不能引用该内容")
	}
	return nil
}

// rebuildDerived 重建发布内容的派生投影：标签、搜索、提及与引用通知。
// 只对已发布内容通知生效；非公开内容会停用提及，避免继续触发 AI 与通知。
func (s *Service) rebuildDerived(ctx context.Context, post model.Post, plain string, at time.Time) error {
	records := make([]repo.TagRecord, 0, 4)
	for _, tag := range richtext.ExtractTags(plain) {
		tagID, err := s.nextID()
		if err != nil {
			return err
		}
		records = append(records, repo.TagRecord{ID: tagID, Name: tag.Name, DisplayName: tag.DisplayName})
	}
	if err := s.repo.ReplacePostTags(ctx, post.ID, records, at); err != nil {
		return s.wrap(err, "重建标签失败")
	}
	if err := s.repo.UpsertSearchText(ctx, post.ID, plain, at); err != nil {
		return s.wrap(err, "更新搜索投影失败")
	}
	if post.Status != model.PostStatusPublished || s.deps.Notice == nil {
		return nil
	}
	if post.Visibility != model.VisibilityPublic {
		return s.wrap(s.deps.Notice.DeactivateMentions(ctx, model.NoticeSourcePost, post.ID, at), "停用提及失败")
	}
	if err := s.deps.Notice.PublishPostNotifications(ctx, contracts.PostNotificationsInput{
		PostID:       post.ID,
		AuthorID:     post.AuthorID,
		PlainText:    plain,
		MentionNames: richtext.ExtractMentions(plain),
		At:           at,
	}); err != nil {
		return s.wrap(err, "写入提及通知失败")
	}
	return s.notifyQuote(ctx, post, at)
}

// notifyQuote 为引用转发写一条引用通知；幂等键保证重试或重复编辑不会重复通知。
func (s *Service) notifyQuote(ctx context.Context, post model.Post, at time.Time) error {
	if post.QuotedPostID == nil || s.deps.Notice == nil {
		return nil
	}
	quoted, err := s.repo.GetPost(ctx, *post.QuotedPostID)
	if err != nil {
		if apperr.IsKind(err, apperr.KindNotFound) {
			return nil
		}
		return s.wrap(err, "读取引用原帖失败")
	}
	postID := post.ID
	if err := s.deps.Notice.CreateNotification(ctx, contracts.NotificationInput{
		IdempotencyKey: "quote:" + format.FormatID(post.ID),
		RecipientID:    quoted.AuthorID,
		ActorID:        post.AuthorID,
		Type:           model.NoticeTypeQuote,
		TargetType:     model.NoticeTargetPost,
		TargetID:       post.ID,
		PostID:         &postID,
		At:             at,
	}); err != nil {
		return s.wrap(err, "写入引用通知失败")
	}
	return nil
}

// linkAttachments 把附件正式挂到帖子上（attachments 表归 media 模块）。
func (s *Service) linkAttachments(ctx context.Context, postID int64, attachmentIDs []int64, at time.Time) error {
	if len(attachmentIDs) == 0 {
		return nil
	}
	if s.deps.Media == nil {
		return apperr.Internal("附件服务未就绪")
	}
	if err := s.deps.Media.LinkAttachmentsToPost(ctx, postID, attachmentIDs, at); err != nil {
		return s.wrap(err, "关联帖子附件失败")
	}
	return nil
}

// enqueuePublishJob 入队定时发布作业。
// 幂等键已存在时不会被重复插入；若作业被取消/暂停过，通过可选 reactivate 能力重新激活。
func (s *Service) enqueuePublishJob(ctx context.Context, postID int64, at time.Time) error {
	if s.deps.Jobs == nil {
		return nil
	}
	key := domain.PublishJobKey(postID, at)
	if err := s.deps.Jobs.Enqueue(ctx, contracts.JobEnqueue{
		Type:           contracts.JobTypePostPublish,
		RefType:        domain.RefTypePost,
		RefID:          &postID,
		IdempotencyKey: key,
		RunAt:          at,
	}); err != nil {
		return s.wrap(err, "写入定时发布作业失败")
	}
	// 取消后重新设置同一时间时，旧作业已是 canceled/paused；若适配器支持则重新激活。
	if reactivator, ok := s.deps.Jobs.(jobReactivator); ok {
		if err := reactivator.Reactivate(ctx, key, at); err != nil {
			return s.wrap(err, "重新激活定时发布作业失败")
		}
	}
	return nil
}

// jobReactivator 是 platform/jobs 适配器可选提供的能力：按幂等键把 canceled/paused
// 作业重新置为 pending。contracts.JobsQueue 暂无该入口，缺失时只是保留旧作业状态。
type jobReactivator interface {
	Reactivate(ctx context.Context, idempotencyKey string, runAt time.Time) error
}

// cancelPublishJobs 取消该帖子所有未执行的定时发布作业。
func (s *Service) cancelPublishJobs(ctx context.Context, postID int64) error {
	if s.deps.Jobs == nil {
		return nil
	}
	if err := s.deps.Jobs.CancelPending(ctx, contracts.CancelPendingJobsInput{
		JobType: contracts.JobTypePostPublish,
		RefType: domain.RefTypePost,
		RefID:   postID,
		At:      s.Now(),
	}); err != nil {
		return s.wrap(err, "取消定时发布作业失败")
	}
	return nil
}

// loadUser 读取账号行并映射 NotFound 文案。
func (s *Service) loadUser(ctx context.Context, userID int64, notFoundMessage string) (model.User, error) {
	if s.deps.Users == nil {
		return model.User{ID: userID, Role: model.RoleUser, Status: model.StatusActive}, nil
	}
	user, err := s.deps.Users.GetUser(ctx, userID)
	if err != nil {
		if apperr.IsKind(err, apperr.KindNotFound) {
			return model.User{}, apperr.NotFound(notFoundMessage)
		}
		return model.User{}, s.wrap(err, "读取账号失败")
	}
	return user, nil
}
