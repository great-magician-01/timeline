package app

import (
	"context"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/content/domain"
	"github.com/great-magician-01/timeline/internal/modules/content/repo"
	"github.com/great-magician-01/timeline/internal/shared/access"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// briefOptions 描述一次 brief 组装的角色差异。
type briefOptions struct {
	kind       string
	authorID   int64
	blankBody  bool
	repostedAt *time.Time
	nested     bool
}

// briefData 汇总组装 DTO 所需的批量查询结果，避免逐条查询。
type briefData struct {
	posts       map[int64]model.Post
	authors     map[int64]model.UserBrief
	versions    map[int64]model.PostVersion
	drafts      map[int64]repo.Draft
	attachments map[int64][]AttachmentView
	quoted      map[int64]*QuotedPost
	states      contracts.PostViewerStates
}

// loadBriefData 批量载入帖子和被引用原帖、作者摘要、当前版本、附件与访问者互动状态。
// preloaded 是调用方已经读到的帖子行（时间线候选/详情主帖），只对它缺失的 ID 补查一次，
// 避免同一批 ID 在时间线/详情请求里重复执行 GetPosts。
func (s *Service) loadBriefData(
	ctx context.Context,
	viewer model.Viewer,
	following map[int64]bool,
	postIDs []int64,
	preloaded map[int64]model.Post,
	extraUserIDs []int64,
) (*briefData, error) {
	data := &briefData{
		posts:       map[int64]model.Post{},
		authors:     map[int64]model.UserBrief{},
		versions:    map[int64]model.PostVersion{},
		drafts:      map[int64]repo.Draft{},
		attachments: map[int64][]AttachmentView{},
		quoted:      map[int64]*QuotedPost{},
		states:      contracts.PostViewerStates{Liked: map[int64]bool{}, Reposted: map[int64]bool{}},
	}

	wanted := domain.UniqueIDs(postIDs)
	missing := make([]int64, 0, len(wanted))
	for _, id := range wanted {
		if post, ok := preloaded[id]; ok && post.ID != 0 {
			data.posts[id] = post
			continue
		}
		missing = append(missing, id)
	}
	if len(missing) > 0 {
		loaded, err := s.repo.GetPosts(ctx, missing)
		if err != nil {
			return nil, s.wrap(err, "读取帖子失败")
		}
		for id, post := range loaded {
			data.posts[id] = post
		}
	}

	// 引用转发的原帖只展开一层，避免递归拉取。
	quotedIDs := make([]int64, 0, len(wanted))
	for _, id := range wanted {
		if post, ok := data.posts[id]; ok && post.QuotedPostID != nil {
			quotedIDs = append(quotedIDs, *post.QuotedPostID)
		}
	}
	missingQuoted := make([]int64, 0, len(quotedIDs))
	for _, id := range domain.UniqueIDs(quotedIDs) {
		if _, ok := data.posts[id]; !ok {
			missingQuoted = append(missingQuoted, id)
		}
	}
	if len(missingQuoted) > 0 {
		quotedPosts, err := s.repo.GetPosts(ctx, missingQuoted)
		if err != nil {
			return nil, s.wrap(err, "读取引用帖子失败")
		}
		for id, post := range quotedPosts {
			data.posts[id] = post
		}
	}

	allIDs := make([]int64, 0, len(data.posts))
	for id := range data.posts {
		allIDs = append(allIDs, id)
	}

	// 作者摘要：展示作者（含转发者）、被引用原帖作者与额外作者。
	userIDs := append([]int64{}, extraUserIDs...)
	for _, id := range allIDs {
		userIDs = append(userIDs, data.posts[id].AuthorID)
	}
	authors, err := s.userBriefs(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	data.authors = authors

	// 已发布内容的当前版本正文：版本 ID 直接取自已读到的 current_version_id，
	// 只按版本 ID 批量回表，不再 JOIN posts。
	currentVersionIDs := make([]int64, 0, len(allIDs))
	for _, id := range allIDs {
		if versionID := data.posts[id].CurrentVersionID; versionID != nil {
			currentVersionIDs = append(currentVersionIDs, *versionID)
		}
	}
	versions, err := s.repo.CurrentVersions(ctx, currentVersionIDs)
	if err != nil {
		return nil, s.wrap(err, "读取当前版本失败")
	}
	data.versions = versions
	unpublished := make([]int64, 0, len(allIDs))
	for _, id := range allIDs {
		if data.posts[id].Status != model.PostStatusPublished {
			unpublished = append(unpublished, id)
		}
	}
	drafts, err := s.repo.DraftsMany(ctx, unpublished)
	if err != nil {
		return nil, s.wrap(err, "读取草稿失败")
	}
	data.drafts = drafts

	// 附件：已发布内容取当前版本快照，未发布内容取草稿工作区。
	versionIDs := make([]int64, 0, len(versions))
	for _, version := range versions {
		versionIDs = append(versionIDs, version.ID)
	}
	byVersion, err := s.repo.VersionAttachmentIDsMany(ctx, versionIDs)
	if err != nil {
		return nil, s.wrap(err, "读取版本附件失败")
	}
	byDraft, err := s.repo.DraftAttachmentIDsMany(ctx, unpublished)
	if err != nil {
		return nil, s.wrap(err, "读取草稿附件失败")
	}
	rawIDs := make(map[int64][]int64, len(allIDs))
	attachmentIDs := make([]int64, 0, len(byVersion)+len(byDraft))
	for _, id := range allIDs {
		if version, ok := versions[id]; ok {
			rawIDs[id] = byVersion[version.ID]
			continue
		}
		rawIDs[id] = byDraft[id]
	}
	for _, list := range rawIDs {
		attachmentIDs = append(attachmentIDs, list...)
	}
	attachmentRows, derivatives, err := s.loadAttachments(ctx, attachmentIDs)
	if err != nil {
		return nil, err
	}
	for id, list := range rawIDs {
		data.attachments[id] = availableAttachmentViews(list, attachmentRows, derivatives)
	}

	// 访问者互动状态。
	if viewer.ID > 0 && len(allIDs) > 0 && s.deps.Social != nil {
		states, err := s.deps.Social.ViewerPostStates(ctx, viewer.ID, allIDs)
		if err != nil {
			return nil, s.wrap(err, "读取互动状态失败")
		}
		data.states = states
	}

	// 引用原帖的可见性可能依赖关注集合：调用方传 nil（表示未加载）时按需补一次，
	// 避免把 nil 当空集合而错误隐藏「仅关注者可见」的原帖。
	if following == nil {
		if s.quotedNeedsFollowing(viewer, data.posts, allIDs) {
			following, err = s.followingSet(ctx, viewer)
			if err != nil {
				return nil, err
			}
		}
	}

	// 引用转发的原帖占位。
	for _, id := range allIDs {
		post, ok := data.posts[id]
		if !ok || post.QuotedPostID == nil {
			continue
		}
		data.quoted[id] = data.buildQuoted(viewer, following, *post.QuotedPostID)
	}
	return data, nil
}

// quotedNeedsFollowing 判断引用原帖的可见性判定是否需要关注集合。
func (s *Service) quotedNeedsFollowing(
	viewer model.Viewer,
	posts map[int64]model.Post,
	ids []int64,
) bool {
	for _, id := range ids {
		post, ok := posts[id]
		if !ok || post.QuotedPostID == nil {
			continue
		}
		quoted, ok := posts[*post.QuotedPostID]
		if !ok {
			continue
		}
		if needsFollowingSet(viewer, quoted) {
			return true
		}
	}
	return false
}

// userBriefs 批量读取作者摘要；用户端口缺失时退化为只含 ID 的占位。
// 读取失败必须向上传递，不能用空摘要静默降级（避免 200 响应里作者信息缺失）。
func (s *Service) userBriefs(ctx context.Context, userIDs []int64) (map[int64]model.UserBrief, error) {
	result := map[int64]model.UserBrief{}
	if s.deps.Users == nil {
		return result, nil
	}
	briefs, err := s.deps.Users.GetUserBriefs(ctx, domain.UniqueIDs(userIDs))
	if err != nil {
		return nil, s.wrap(err, "读取作者摘要失败")
	}
	return briefs, nil
}

// loadAttachments 批量读取附件行与衍生文件状态。
// 读取失败必须向上传递：附件缺失会改变列表内容，不能静默降级为空集合。
func (s *Service) loadAttachments(
	ctx context.Context,
	attachmentIDs []int64,
) (map[int64]model.Attachment, map[int64]map[string]string, error) {
	ids := domain.UniqueIDs(attachmentIDs)
	rows := map[int64]model.Attachment{}
	derivatives := map[int64]map[string]string{}
	if len(ids) == 0 || s.deps.Media == nil {
		return rows, derivatives, nil
	}
	loaded, err := s.deps.Media.AttachmentsByIDs(ctx, ids)
	if err != nil {
		return nil, nil, s.wrap(err, "读取附件失败")
	}
	statuses, err := s.deps.Media.DerivativeStatuses(ctx, ids)
	if err != nil {
		return nil, nil, s.wrap(err, "读取附件衍生文件状态失败")
	}
	return loaded, statuses, nil
}

// availableAttachmentViews 组装展示用附件视图：只保留仍然有效的附件。
func availableAttachmentViews(
	ids []int64,
	rows map[int64]model.Attachment,
	derivatives map[int64]map[string]string,
) []AttachmentView {
	views := make([]AttachmentView, 0, len(ids))
	for _, id := range ids {
		attachment, ok := rows[id]
		if !ok || !attachment.Available() {
			continue
		}
		views = append(views, AttachmentView{Attachment: attachment, Derivatives: derivatives[id]})
	}
	return views
}

// buildQuoted 组装引用占位：原帖不可见、非公开或已删除时只返回 Available=false。
func (d *briefData) buildQuoted(viewer model.Viewer, following map[int64]bool, quotedID int64) *QuotedPost {
	quoted, ok := d.posts[quotedID]
	if !ok || quoted.Status != model.PostStatusPublished ||
		!access.CanViewPostWithFollowing(viewer, quoted, following) {
		return &QuotedPost{Available: false}
	}
	brief := d.brief(quoted, briefOptions{kind: domain.KindPost, authorID: quoted.AuthorID, nested: true})
	return &QuotedPost{Available: true, Post: &brief}
}

// brief 组装列表项；纯转发外层以转发者作为作者、正文留空，并通过 RepostOf 指向原帖。
func (d *briefData) brief(post model.Post, opts briefOptions) PostBrief {
	kind := opts.kind
	if kind == "" {
		kind = domain.KindPost
	}
	author := model.UserBrief{ID: opts.authorID}
	if found, ok := d.authors[opts.authorID]; ok {
		author = found
	}
	out := PostBrief{
		ID:           post.ID,
		Kind:         kind,
		Author:       author,
		Visibility:   post.Visibility,
		LikeCount:    post.LikeCount,
		CommentCount: post.CommentCount,
		RepostCount:  post.RepostCount,
		Attachments:  d.attachments[post.ID],
		ViewerState: ViewerState{
			Liked:    d.states.Liked[post.ID],
			Reposted: d.states.Reposted[post.ID],
		},
	}
	if out.Attachments == nil {
		out.Attachments = []AttachmentView{}
	}
	if post.PublishedAt != nil {
		published := *post.PublishedAt
		out.PublishedAt = &published
	}
	if version, ok := d.versions[post.ID]; ok {
		if !opts.blankBody {
			out.ContentHTML = version.ContentHTML
		}
		out.Edited = version.VersionNumber > 1
	} else if draft, ok := d.drafts[post.ID]; ok {
		if !opts.blankBody {
			out.ContentHTML = draft.HTML
		}
	}
	if opts.repostedAt != nil {
		reposted := *opts.repostedAt
		out.RepostedAt = &reposted
	}
	// 置顶状态属于帖子本身：仅顶层帖子项携带，转发外层与嵌套引用不携带。
	if kind == domain.KindPost && !opts.nested && post.PinnedAt != nil {
		pinned := *post.PinnedAt
		out.PinnedAt = &pinned
	}
	if !opts.nested {
		out.QuotedPost = d.quoted[post.ID]
	}
	return out
}

// loadDetail 组装帖子详情。
// post 是调用方已经读到的帖子行（写路径在事务内读取、提交后组装），因此不再重复读 posts。
func (s *Service) loadDetail(
	ctx context.Context,
	viewer model.Viewer,
	following map[int64]bool,
	post model.Post,
) (PostDetail, error) {
	data, err := s.loadBriefData(ctx, viewer, following, []int64{post.ID},
		map[int64]model.Post{post.ID: post}, nil)
	if err != nil {
		return PostDetail{}, err
	}
	brief := data.brief(post, briefOptions{kind: domain.KindPost, authorID: post.AuthorID})
	detail := PostDetail{
		PostBrief:      brief,
		Status:         post.Status,
		CanEdit:        post.SelfPosted(viewer),
		CanDelete:      post.SelfPosted(viewer),
		CanViewHistory: true,
	}
	detail.CanPin = post.SelfPosted(viewer) && domain.CanPin(post.Status)
	if post.Status == model.PostStatusPublished {
		if version, ok := data.versions[post.ID]; ok {
			detail.ContentFormat = version.ContentFormat
			detail.VersionNumber = version.VersionNumber
		}
	} else if draft, ok := data.drafts[post.ID]; ok {
		detail.ContentFormat = draft.Format
		detail.Revision = draft.Revision
	}
	if post.ScheduledAt != nil {
		scheduled := *post.ScheduledAt
		detail.ScheduledAt = &scheduled
	}
	if viewer.Authenticated && viewer.ID != post.AuthorID && s.deps.Social != nil {
		state, err := s.deps.Social.FollowState(ctx, viewer.ID, post.AuthorID)
		if err != nil {
			return PostDetail{}, s.wrap(err, "读取关注状态失败")
		}
		detail.AuthorFollowState = state
	}
	return detail, nil
}
