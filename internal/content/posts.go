package content

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/great-magician-01/timeline/internal/access"
	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/audit"
	"github.com/great-magician-01/timeline/internal/auth"
	"github.com/great-magician-01/timeline/internal/jobs"
	"github.com/great-magician-01/timeline/internal/model"
	"github.com/great-magician-01/timeline/internal/notice"
	"github.com/great-magician-01/timeline/internal/richtext"
)

// 作业与附件的固定取值。
const (
	refTypePost           = "post"
	attachmentPurposePost = "post"
)

// postInput 是创建与更新帖子共用的请求体。
type postInput struct {
	ContentFormat string   `json:"content_format"`
	ContentSource string   `json:"content_source"`
	Visibility    string   `json:"visibility"`
	AttachmentIDs []string `json:"attachment_ids"`
	QuotedPostID  *string  `json:"quoted_post_id"`
	Action        string   `json:"action"`
	ScheduledAt   *string  `json:"scheduled_at"`
	Revision      *int     `json:"revision"`
}

// normalizedPost 是校验、渲染并校验引用之后可直接写入的内容。
type normalizedPost struct {
	format        string
	source        string
	html          string
	plain         string
	visibility    string
	attachmentIDs []int64
	quotedPostID  *int64
}

// normalizePostInput 校验并渲染正文、可见性、附件与引用目标。
// allowQuote 为真时校验 quoted_post_id 必须指向公开的已发布帖子。
func normalizePostInput(ctx context.Context, q access.Querier, authorID int64, in postInput, allowQuote bool) (normalizedPost, error) {
	format, err := normalizeFormat(in.ContentFormat)
	if err != nil {
		return normalizedPost{}, err
	}
	html, plain := richtext.Render(format, in.ContentSource)
	if strings.TrimSpace(plain) == "" {
		return normalizedPost{}, api.ErrValidation("正文不能为空").
			WithDetails(gin.H{"content_source": "正文不能为空"})
	}
	visibility, err := normalizeVisibility(in.Visibility)
	if err != nil {
		return normalizedPost{}, err
	}
	attachmentIDs, err := parseAttachmentIDs(in.AttachmentIDs)
	if err != nil {
		return normalizedPost{}, err
	}
	if err := ensureAttachmentsUsable(ctx, q, authorID, attachmentIDs); err != nil {
		return normalizedPost{}, err
	}
	if err := ensureVisibilityAllowed(ctx, q, authorID, visibility); err != nil {
		return normalizedPost{}, err
	}

	content := normalizedPost{
		format:        format,
		source:        in.ContentSource,
		html:          html,
		plain:         plain,
		visibility:    visibility,
		attachmentIDs: attachmentIDs,
	}
	quotedID, err := parseQuotedPostID(in.QuotedPostID)
	if err != nil {
		return normalizedPost{}, err
	}
	if allowQuote && quotedID != nil {
		if err := ensureQuotablePost(ctx, q, *quotedID); err != nil {
			return normalizedPost{}, err
		}
		content.quotedPostID = quotedID
	}
	return content, nil
}

// ensureQuotablePost 校验引用目标只能是公开的已发布帖子，且原帖作者未停用。
// 公开帖子本身仍然可读，但停用账号不再接受新的引用互动。
func ensureQuotablePost(ctx context.Context, q access.Querier, quotedID int64) error {
	quoted, err := loadPost(ctx, q, quotedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.ErrNotFound("引用的内容不存在")
	}
	if err != nil {
		return err
	}
	if quoted.Status != model.PostStatusPublished || quoted.Visibility != model.VisibilityPublic {
		return api.ErrValidation("只能引用公开的已发布内容").
			WithDetails(gin.H{"quoted_post_id": "只能引用公开的已发布内容"})
	}
	suspended, err := access.IsSuspended(ctx, q, quoted.AuthorID)
	if err != nil {
		return err
	}
	if suspended {
		return api.ErrForbidden("原帖作者账号已停用，不能引用该内容")
	}
	return nil
}

// ensureVisibilityAllowed 校验作者账号允许的可见性：AI 内容必须公开（需求 13.4）。
func ensureVisibilityAllowed(ctx context.Context, q access.Querier, authorID int64, visibility string) error {
	var isAI bool
	err := q.QueryRow(ctx, `SELECT is_ai FROM users WHERE id = $1`, authorID).Scan(&isAI)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.ErrNotFound("作者账号不存在")
	}
	if err != nil {
		return err
	}
	if isAI && visibility != model.VisibilityPublic {
		return api.ErrValidation("AI 内容必须公开").
			WithDetails(gin.H{"visibility": "AI 账号发布的内容只能是 public"})
	}
	return nil
}

// ensureAttachmentsUsable 校验附件存在、有效、用途为帖子且属于该作者。
func ensureAttachmentsUsable(ctx context.Context, q access.Querier, authorID int64, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	invalid := api.ErrValidation("附件不可用").
		WithDetails(gin.H{"attachment_ids": "附件不存在、不属于当前账号或已失效"})
	rows, err := q.Query(ctx, `
		SELECT id, uploader_id, status, purpose FROM attachments WHERE id = ANY($1)`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	found := 0
	for rows.Next() {
		var id, uploaderID int64
		var status, purpose string
		if err := rows.Scan(&id, &uploaderID, &status, &purpose); err != nil {
			return err
		}
		if uploaderID != authorID || status != model.AttachmentAvailable || purpose != attachmentPurposePost {
			return invalid
		}
		found++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if found != len(ids) {
		return invalid
	}
	return nil
}

// replaceDraftAttachments 重建草稿工作区附件集合：旧关联置为失效后逐个 upsert（不物理删除）。
func replaceDraftAttachments(ctx context.Context, q access.Querier, postID int64, ids []int64) error {
	if _, err := q.Exec(ctx, `
		UPDATE post_draft_attachments SET is_active = FALSE WHERE post_id = $1 AND is_active`, postID); err != nil {
		return err
	}
	for order, attachmentID := range ids {
		if _, err := q.Exec(ctx, `
			INSERT INTO post_draft_attachments (post_id, attachment_id, sort_order, is_active)
			VALUES ($1, $2, $3, TRUE)
			ON CONFLICT (post_id, attachment_id)
			DO UPDATE SET sort_order = EXCLUDED.sort_order, is_active = TRUE`,
			postID, attachmentID, order); err != nil {
			return err
		}
	}
	return nil
}

// linkAttachments 把附件正式挂到帖子上；物理文件永不删除，只更新关联。
func linkAttachments(ctx context.Context, q access.Querier, postID int64, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := q.Exec(ctx, `
		UPDATE attachments SET post_id = $1, updated_at = now() WHERE id = ANY($2)`, postID, ids)
	return err
}

// insertVersion 追加一个完整历史版本快照，并写入该版本的附件与排序。
func (s *Service) insertVersion(
	ctx context.Context,
	q access.Querier,
	postID, authorID int64,
	versionNumber int,
	content normalizedPost,
	restoredFrom *int64,
) (int64, error) {
	versionID, err := s.ids.Next()
	if err != nil {
		return 0, err
	}
	if _, err := q.Exec(ctx, `
		INSERT INTO post_versions (
			id, post_id, version_number, content_format, content_source, content_html,
			plain_text, visibility, restored_from_version_id, created_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		versionID, postID, versionNumber, content.format, content.source, content.html,
		content.plain, content.visibility, restoredFrom, authorID); err != nil {
		return 0, err
	}
	for order, attachmentID := range content.attachmentIDs {
		if _, err := q.Exec(ctx, `
			INSERT INTO post_version_attachments (version_id, attachment_id, sort_order)
			VALUES ($1, $2, $3)
			ON CONFLICT (version_id, attachment_id) DO NOTHING`,
			versionID, attachmentID, order); err != nil {
			return 0, err
		}
	}
	return versionID, nil
}

// replacePostTags 重建当前标签投影：旧关联先失效，再 upsert 当前标签（不物理删除）。
func (s *Service) replacePostTags(ctx context.Context, q access.Querier, postID int64, tags []richtext.Tag) error {
	if _, err := q.Exec(ctx, `
		UPDATE post_tags SET is_active = FALSE WHERE post_id = $1 AND is_active`, postID); err != nil {
		return err
	}
	for _, tag := range tags {
		tagID, err := s.ids.Next()
		if err != nil {
			return err
		}
		var storedID int64
		if err := q.QueryRow(ctx, `
			INSERT INTO tags (id, name, display_name) VALUES ($1, $2, $3)
			ON CONFLICT (name) DO UPDATE SET display_name = EXCLUDED.display_name
			RETURNING id`, tagID, tag.Name, tag.DisplayName).Scan(&storedID); err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `
			INSERT INTO post_tags (post_id, tag_id, is_active) VALUES ($1, $2, TRUE)
			ON CONFLICT (post_id, tag_id) DO UPDATE SET is_active = TRUE`, postID, storedID); err != nil {
			return err
		}
	}
	return nil
}

// rebuildDerived 重建发布内容的派生投影：标签、搜索、提及与引用通知。
// 只对已发布内容生效；非公开内容会停用提及，避免继续触发 AI 与通知。
func (s *Service) rebuildDerived(ctx context.Context, q access.Querier, post model.Post, plain string) error {
	if err := s.replacePostTags(ctx, q, post.ID, richtext.ExtractTags(plain)); err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `
		INSERT INTO post_search (post_id, plain_text, updated_at) VALUES ($1, $2, now())
		ON CONFLICT (post_id) DO UPDATE SET plain_text = EXCLUDED.plain_text, updated_at = now()`,
		post.ID, plain); err != nil {
		return err
	}
	if post.Status != model.PostStatusPublished {
		return nil
	}
	if post.Visibility != model.VisibilityPublic {
		return notice.DeactivateMentions(ctx, q, notice.SourcePost, post.ID)
	}
	if err := notice.PublishPostNotifications(ctx, q, s.ids, post.ID, post.AuthorID, plain,
		richtext.ExtractMentions(plain)); err != nil {
		return err
	}
	return s.notifyQuote(ctx, q, post)
}

// notifyQuote 为引用转发写一条引用通知；幂等键保证重试或重复编辑不会重复通知。
func (s *Service) notifyQuote(ctx context.Context, q access.Querier, post model.Post) error {
	if post.QuotedPostID == nil {
		return nil
	}
	var quotedAuthorID int64
	err := q.QueryRow(ctx, `SELECT author_id FROM posts WHERE id = $1`, *post.QuotedPostID).Scan(&quotedAuthorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	postID := post.ID
	return notice.Create(ctx, q, s.ids, notice.Params{
		IdempotencyKey: "quote:" + api.FormatID(postID),
		RecipientID:    quotedAuthorID,
		ActorID:        post.AuthorID,
		Type:           notice.TypeQuote,
		TargetType:     "post",
		TargetID:       postID,
		PostID:         &postID,
	})
}

// publishJobKey 是定时发布作业的幂等键。
func publishJobKey(postID int64, at time.Time) string {
	return "post_publish:" + api.FormatID(postID) + ":" + strconv.FormatInt(at.Unix(), 10)
}

// enqueuePublishJob 入队定时发布作业。幂等键已存在时不会被重复插入，
// 因此这里额外把同键的已取消/已暂停作业重新激活，避免“取消后重新设置同一时间”丢失作业。
func (s *Service) enqueuePublishJob(ctx context.Context, q access.Querier, postID int64, at time.Time) error {
	if s.jobs == nil {
		return nil
	}
	key := publishJobKey(postID, at)
	if err := s.jobs.Enqueue(ctx, q, jobs.EnqueueParams{
		Type:           jobs.TypePostPublish,
		RefType:        refTypePost,
		RefID:          &postID,
		IdempotencyKey: key,
		RunAt:          at,
	}); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `
		UPDATE background_jobs
		SET status = 'pending', run_at = $2, last_error = '', updated_at = now()
		WHERE idempotency_key = $1 AND status IN ('canceled', 'paused')`, key, at)
	return err
}

// cancelPublishJobs 取消该帖子所有未执行的定时发布作业。
func (s *Service) cancelPublishJobs(ctx context.Context, q access.Querier, postID int64) error {
	if s.jobs == nil {
		return nil
	}
	return s.jobs.CancelPending(ctx, q, jobs.TypePostPublish, refTypePost, postID)
}

// requirePostAuthor 校验访问者为作者；不可见时返回 404，可见但非作者返回 403。
func requirePostAuthor(ctx context.Context, q access.Querier, viewer model.Viewer, post model.Post) error {
	if post.SelfPosted(viewer) {
		return nil
	}
	if err := access.RequireVisiblePost(ctx, q, viewer, post); err != nil {
		return err
	}
	return api.ErrForbidden("只有作者可以执行此操作")
}

// loadDetail 组装帖子详情 DTO。
func (s *Service) loadDetail(ctx context.Context, q access.Querier, viewer model.Viewer, post model.Post) (PostDetail, error) {
	following := map[int64]bool{}
	if viewer.Authenticated && viewer.ID > 0 {
		set, err := access.FollowingSet(ctx, q, viewer.ID)
		if err != nil {
			return PostDetail{}, err
		}
		following = set
	}
	data, err := s.loadBriefData(ctx, q, viewer, following, []int64{post.ID}, nil, nil)
	if err != nil {
		return PostDetail{}, err
	}
	brief := data.brief(post, briefOptions{kind: kindPost, authorID: post.AuthorID})

	detail := PostDetail{
		PostBrief:      brief,
		Status:         post.Status,
		CanEdit:        post.SelfPosted(viewer),
		CanDelete:      post.SelfPosted(viewer),
		CanViewHistory: true,
	}
	if post.Status == model.PostStatusPublished {
		if version, ok := data.versions[post.ID]; ok {
			detail.ContentFormat = version.ContentFormat
			detail.VersionNumber = version.VersionNumber
		}
	} else if draft, ok := data.drafts[post.ID]; ok {
		detail.ContentFormat = draft.format
		detail.Revision = draft.revision
	}
	if post.ScheduledAt != nil {
		scheduled := api.FormatTime(*post.ScheduledAt)
		detail.ScheduledAt = &scheduled
	}
	if viewer.Authenticated && viewer.ID != post.AuthorID {
		state, err := access.FollowState(ctx, q, viewer.ID, post.AuthorID)
		if err != nil {
			return PostDetail{}, err
		}
		detail.AuthorFollowState = state
	}
	return detail, nil
}

// CreatePost 创建草稿、立即发布或设置定时发布。
func (s *Service) CreatePost(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	viewer := viewerOf(c)
	if err := access.RequireWritable(viewer); err != nil {
		api.Respond(c, err)
		return
	}
	var in postInput
	if err := api.DecodeJSON(c, &in); err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", invalidInputError(err))
		return
	}
	action, err := normalizeAction(in.Action)
	if err != nil {
		s.respond(c, "解析创建动作失败", err)
		return
	}
	if !s.consumePostQuota(c, viewer.ID) {
		return
	}

	ctx := c.Request.Context()
	var scheduledAt *time.Time
	if action == actionSchedule {
		raw := ""
		if in.ScheduledAt != nil {
			raw = *in.ScheduledAt
		}
		at, err := parseScheduledAt(raw, time.Now())
		if err != nil {
			s.respond(c, "校验定时发布时间失败", err)
			return
		}
		scheduledAt = &at
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		s.internalError(c, "开启创建帖子事务失败", err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 引用与附件校验必须在同一事务内完成（数据库无外键，完整性由应用层保证）。
	content, err := normalizePostInput(ctx, tx, viewer.ID, in, true)
	if err != nil {
		s.respond(c, "校验帖子内容失败", err)
		return
	}

	postID, err := s.ids.Next()
	if err != nil {
		s.internalError(c, "生成帖子 ID 失败", err)
		return
	}
	// 统一先建草稿工作区，再按动作推进状态，保证三条创建路径共用同一套发布逻辑。
	if _, err := tx.Exec(ctx, `
		INSERT INTO posts (id, author_id, status, visibility, quoted_post_id, scheduled_at)
		VALUES ($1, $2, 'draft', $3, $4, $5)`,
		postID, viewer.ID, content.visibility, content.quotedPostID, scheduledAt); err != nil {
		s.internalError(c, "写入帖子失败", err)
		return
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO post_drafts (
			post_id, content_format, content_source, content_html, plain_text, draft_revision
		) VALUES ($1, $2, $3, $4, $5, 1)`,
		postID, content.format, content.source, content.html, content.plain); err != nil {
		s.internalError(c, "写入草稿失败", err)
		return
	}
	if err := replaceDraftAttachments(ctx, tx, postID, content.attachmentIDs); err != nil {
		s.internalError(c, "保存草稿附件失败", err)
		return
	}
	if err := s.applyCreateAction(ctx, tx, postID, viewer.ID, action, scheduledAt); err != nil {
		s.respond(c, "应用帖子动作失败", err)
		return
	}
	post, err := loadPost(ctx, tx, postID)
	if err != nil {
		s.internalError(c, "读取新建帖子失败", err)
		return
	}
	detail, err := s.loadDetail(ctx, tx, viewer, post)
	if err != nil {
		s.respond(c, "组装帖子详情失败", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.internalError(c, "提交创建帖子事务失败", err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"post": detail})
}

// applyCreateAction 按创建动作推进帖子状态。
// q 可以是事务或连接池：传入事务时与调用方的其他写入保持原子。
func (s *Service) applyCreateAction(ctx context.Context, q access.Querier, postID, authorID int64, action string, scheduledAt *time.Time) error {
	switch action {
	case actionDraft:
		return nil
	case actionPublish:
		return s.publishLocked(ctx, q, postID, authorID)
	case actionSchedule:
		if scheduledAt == nil {
			return api.ErrValidation("请填写定时发布时间")
		}
		if _, err := q.Exec(ctx, `
			UPDATE posts SET status = 'scheduled', scheduled_at = $2, updated_at = now()
			WHERE id = $1`, postID, *scheduledAt); err != nil {
			return err
		}
		return s.enqueuePublishJob(ctx, q, postID, *scheduledAt)
	default:
		return api.ErrValidation("缺少或无效的操作类型")
	}
}

// GetPost 返回帖子详情；不可见一律返回 404。
func (s *Service) GetPost(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	postID, ok := pathID(c)
	if !ok {
		return
	}
	viewer := viewerOf(c)
	ctx := c.Request.Context()

	post, err := loadPost(ctx, pool, postID)
	if errors.Is(err, pgx.ErrNoRows) {
		api.Respond(c, api.ErrNotFound("内容不存在或无权访问"))
		return
	}
	if err != nil {
		s.internalError(c, "读取帖子失败", err)
		return
	}
	if err := access.RequireVisiblePost(ctx, pool, viewer, post); err != nil {
		s.respond(c, "校验帖子可见性失败", err)
		return
	}
	detail, err := s.loadDetail(ctx, pool, viewer, post)
	if err != nil {
		s.internalError(c, "组装帖子详情失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"post": detail})
}

// UpdatePost 保存草稿（自动保存）或为已发布内容追加新版本。
func (s *Service) UpdatePost(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	viewer := viewerOf(c)
	if err := access.RequireWritable(viewer); err != nil {
		api.Respond(c, err)
		return
	}
	postID, ok := pathID(c)
	if !ok {
		return
	}
	var in postInput
	if err := api.DecodeJSON(c, &in); err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", invalidInputError(err))
		return
	}
	if !s.consumePostQuota(c, viewer.ID) {
		return
	}

	ctx := c.Request.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		s.internalError(c, "开启更新帖子事务失败", err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 引用目标在编辑阶段不可变更：历史版本不记录该字段，改动会导致时间线语义不一致。
	content, err := normalizePostInput(ctx, tx, viewer.ID, in, false)
	if err != nil {
		s.respond(c, "校验帖子内容失败", err)
		return
	}

	post, err := loadPostForUpdate(ctx, tx, postID)
	if errors.Is(err, pgx.ErrNoRows) {
		api.Respond(c, api.ErrNotFound("内容不存在或无权访问"))
		return
	}
	if err != nil {
		s.internalError(c, "读取待更新帖子失败", err)
		return
	}
	if post.Status == model.PostStatusDeleted {
		api.Respond(c, api.ErrNotFound("内容不存在或无权访问"))
		return
	}
	// 管理员不代替作者编辑，但同样不暴露他人内容的细节。
	if err := requirePostAuthor(ctx, tx, viewer, post); err != nil {
		s.respond(c, "校验编辑权限失败", err)
		return
	}

	if post.Status != model.PostStatusPublished {
		var err error
		if post, err = s.updateDraftWorkspace(ctx, tx, post, content, in); err != nil {
			s.respond(c, "保存草稿失败", err)
			return
		}
	} else {
		var err error
		if post, err = s.appendVersion(ctx, tx, post, content); err != nil {
			s.respond(c, "保存新版本失败", err)
			return
		}
	}

	detail, err := s.loadDetail(ctx, tx, viewer, post)
	if err != nil {
		s.respond(c, "组装帖子详情失败", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.internalError(c, "提交更新帖子事务失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"post": detail})
}

// updateDraftWorkspace 保存草稿/定时帖的编辑内容，并处理草稿版本冲突与定时时间调整。
func (s *Service) updateDraftWorkspace(ctx context.Context, tx pgx.Tx, post model.Post, content normalizedPost, in postInput) (model.Post, error) {
	if in.Revision == nil {
		return post, api.ErrValidation("缺少草稿版本号").
			WithDetails(gin.H{"revision": "草稿保存需要携带 revision"})
	}
	var revision int
	var serverSource, serverHTML string
	var serverUpdatedAt time.Time
	err := tx.QueryRow(ctx, `
		SELECT draft_revision, content_source, content_html, updated_at
		FROM post_drafts WHERE post_id = $1 FOR UPDATE`, post.ID,
	).Scan(&revision, &serverSource, &serverHTML, &serverUpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return post, api.ErrNotFound("草稿不存在")
	}
	if err != nil {
		return post, err
	}
	if *in.Revision != revision {
		return post, api.ErrConflict("draft_conflict", "草稿已被更新").WithDetails(gin.H{
			"revision":       revision,
			"content_source": serverSource,
			"content_html":   serverHTML,
			"updated_at":     api.FormatTime(serverUpdatedAt),
		})
	}
	if _, err := tx.Exec(ctx, `
		UPDATE post_drafts
		SET content_format = $2, content_source = $3, content_html = $4, plain_text = $5,
		    draft_revision = draft_revision + 1, updated_at = now()
		WHERE post_id = $1`,
		post.ID, content.format, content.source, content.html, content.plain); err != nil {
		return post, err
	}
	if err := replaceDraftAttachments(ctx, tx, post.ID, content.attachmentIDs); err != nil {
		return post, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE posts SET visibility = $2, updated_at = now() WHERE id = $1`,
		post.ID, content.visibility); err != nil {
		return post, err
	}
	// 定时帖允许在同一接口内调整发布时间；时间未变时不重建作业，避免取消后无法重新入队。
	if post.Status == model.PostStatusScheduled && in.ScheduledAt != nil && strings.TrimSpace(*in.ScheduledAt) != "" {
		at, err := parseScheduledAt(*in.ScheduledAt, time.Now())
		if err != nil {
			return post, err
		}
		if post.ScheduledAt == nil || post.ScheduledAt.Unix() != at.Unix() {
			if _, err := tx.Exec(ctx, `
				UPDATE posts SET scheduled_at = $2, updated_at = now() WHERE id = $1`,
				post.ID, at); err != nil {
				return post, err
			}
			if err := s.cancelPublishJobs(ctx, tx, post.ID); err != nil {
				return post, err
			}
			if err := s.enqueuePublishJob(ctx, tx, post.ID, at); err != nil {
				return post, err
			}
		}
	}
	return loadPost(ctx, tx, post.ID)
}

// appendVersion 为已发布内容追加一个新版本，并重建标签、搜索与提及。
func (s *Service) appendVersion(ctx context.Context, q access.Querier, post model.Post, content normalizedPost) (model.Post, error) {
	var maxVersion int
	if err := q.QueryRow(ctx, `
		SELECT COALESCE(MAX(version_number), 0) FROM post_versions WHERE post_id = $1`,
		post.ID).Scan(&maxVersion); err != nil {
		return post, err
	}
	versionID, err := s.insertVersion(ctx, q, post.ID, post.AuthorID, maxVersion+1, content, nil)
	if err != nil {
		return post, err
	}
	if err := linkAttachments(ctx, q, post.ID, content.attachmentIDs); err != nil {
		return post, err
	}
	if _, err := q.Exec(ctx, `
		UPDATE posts SET current_version_id = $2, visibility = $3, updated_at = now()
		WHERE id = $1`, post.ID, versionID, content.visibility); err != nil {
		return post, err
	}
	// 草稿工作区同步为当前正文，避免编辑页读到过期内容。
	if _, err := q.Exec(ctx, `
		UPDATE post_drafts
		SET content_format = $2, content_source = $3, content_html = $4, plain_text = $5,
		    draft_revision = draft_revision + 1, updated_at = now()
		WHERE post_id = $1`,
		post.ID, content.format, content.source, content.html, content.plain); err != nil {
		return post, err
	}
	if err := replaceDraftAttachments(ctx, q, post.ID, content.attachmentIDs); err != nil {
		return post, err
	}
	updated, err := loadPost(ctx, q, post.ID)
	if err != nil {
		return post, err
	}
	if err := s.rebuildDerived(ctx, q, updated, content.plain); err != nil {
		return post, err
	}
	return updated, nil
}

// PublishPost 立即发布草稿或定时帖；已发布时幂等返回当前详情。
func (s *Service) PublishPost(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	viewer := viewerOf(c)
	if err := access.RequireWritable(viewer); err != nil {
		api.Respond(c, err)
		return
	}
	postID, ok := pathID(c)
	if !ok {
		return
	}
	if !s.consumePostQuota(c, viewer.ID) {
		return
	}
	ctx := c.Request.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		s.internalError(c, "开启发布事务失败", err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	post, err := loadPostForUpdate(ctx, tx, postID)
	if errors.Is(err, pgx.ErrNoRows) {
		api.Respond(c, api.ErrNotFound("内容不存在或无权访问"))
		return
	}
	if err != nil {
		s.internalError(c, "读取帖子失败", err)
		return
	}
	if post.Status == model.PostStatusDeleted {
		api.Respond(c, api.ErrNotFound("内容不存在或无权访问"))
		return
	}
	if err := requirePostAuthor(ctx, tx, viewer, post); err != nil {
		s.respond(c, "校验发布权限失败", err)
		return
	}
	if post.Status != model.PostStatusPublished {
		if err := s.publishLocked(ctx, tx, postID, viewer.ID); err != nil {
			s.respond(c, "发布帖子失败", err)
			return
		}
	}
	if err := s.cancelPublishJobs(ctx, tx, postID); err != nil {
		s.internalError(c, "取消定时发布作业失败", err)
		return
	}
	post, err = loadPost(ctx, tx, postID)
	if err != nil {
		s.internalError(c, "读取发布后帖子失败", err)
		return
	}
	detail, err := s.loadDetail(ctx, tx, viewer, post)
	if err != nil {
		s.respond(c, "组装帖子详情失败", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.internalError(c, "提交发布事务失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"post": detail})
}

// SchedulePost 设置或修改定时发布时间。
func (s *Service) SchedulePost(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	viewer := viewerOf(c)
	if err := access.RequireWritable(viewer); err != nil {
		api.Respond(c, err)
		return
	}
	postID, ok := pathID(c)
	if !ok {
		return
	}
	var in struct {
		ScheduledAt *string `json:"scheduled_at"`
	}
	if err := api.DecodeJSON(c, &in); err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", invalidInputError(err))
		return
	}
	raw := ""
	if in.ScheduledAt != nil {
		raw = *in.ScheduledAt
	}
	at, err := parseScheduledAt(raw, time.Now())
	if err != nil {
		s.respond(c, "校验定时发布时间失败", err)
		return
	}
	if !s.consumePostQuota(c, viewer.ID) {
		return
	}
	ctx := c.Request.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		s.internalError(c, "开启定时发布事务失败", err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	post, err := loadPostForUpdate(ctx, tx, postID)
	if errors.Is(err, pgx.ErrNoRows) {
		api.Respond(c, api.ErrNotFound("内容不存在或无权访问"))
		return
	}
	if err != nil {
		s.internalError(c, "读取帖子失败", err)
		return
	}
	if post.Status == model.PostStatusDeleted {
		api.Respond(c, api.ErrNotFound("内容不存在或无权访问"))
		return
	}
	if err := requirePostAuthor(ctx, tx, viewer, post); err != nil {
		s.respond(c, "校验定时发布权限失败", err)
		return
	}
	if post.Status != model.PostStatusDraft && post.Status != model.PostStatusScheduled {
		api.Respond(c, api.ErrForbidden("已发布的内容不能设置定时发布"))
		return
	}
	if _, err := tx.Exec(ctx, `
		UPDATE posts SET status = 'scheduled', scheduled_at = $2, updated_at = now()
		WHERE id = $1`, postID, at); err != nil {
		s.internalError(c, "更新定时发布时间失败", err)
		return
	}
	// 旧作业必须失效，避免按旧时间提前发布。
	if err := s.cancelPublishJobs(ctx, tx, postID); err != nil {
		s.internalError(c, "取消旧定时发布作业失败", err)
		return
	}
	if err := s.enqueuePublishJob(ctx, tx, postID, at); err != nil {
		s.internalError(c, "写入定时发布作业失败", err)
		return
	}
	post, err = loadPost(ctx, tx, postID)
	if err != nil {
		s.internalError(c, "读取定时帖失败", err)
		return
	}
	detail, err := s.loadDetail(ctx, tx, viewer, post)
	if err != nil {
		s.respond(c, "组装帖子详情失败", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.internalError(c, "提交定时发布事务失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"post": detail})
}

// CancelSchedule 取消定时，帖子回到草稿状态。
func (s *Service) CancelSchedule(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	viewer := viewerOf(c)
	if err := access.RequireWritable(viewer); err != nil {
		api.Respond(c, err)
		return
	}
	postID, ok := pathID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		s.internalError(c, "开启取消定时事务失败", err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	post, err := loadPostForUpdate(ctx, tx, postID)
	if errors.Is(err, pgx.ErrNoRows) {
		api.Respond(c, api.ErrNotFound("内容不存在或无权访问"))
		return
	}
	if err != nil {
		s.internalError(c, "读取帖子失败", err)
		return
	}
	if post.Status == model.PostStatusDeleted {
		api.Respond(c, api.ErrNotFound("内容不存在或无权访问"))
		return
	}
	if err := requirePostAuthor(ctx, tx, viewer, post); err != nil {
		s.respond(c, "校验取消定时权限失败", err)
		return
	}
	if post.Status == model.PostStatusPublished {
		api.Respond(c, api.ErrForbidden("已发布的内容不能取消定时"))
		return
	}
	if post.Status == model.PostStatusScheduled {
		if _, err := tx.Exec(ctx, `
			UPDATE posts SET status = 'draft', scheduled_at = NULL, updated_at = now()
			WHERE id = $1`, postID); err != nil {
			s.internalError(c, "取消定时失败", err)
			return
		}
		if err := s.cancelPublishJobs(ctx, tx, postID); err != nil {
			s.internalError(c, "取消定时发布作业失败", err)
			return
		}
	}
	post, err = loadPost(ctx, tx, postID)
	if err != nil {
		s.internalError(c, "读取帖子失败", err)
		return
	}
	detail, err := s.loadDetail(ctx, tx, viewer, post)
	if err != nil {
		s.respond(c, "组装帖子详情失败", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.internalError(c, "提交取消定时事务失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"post": detail})
}

// DeletePost 逻辑删除帖子：作者或管理员。
// 管理员通过本接口删除时写入管理审计，普通作者自行删除不写管理审计。
func (s *Service) DeletePost(c *gin.Context) {
	viewer := viewerOf(c)
	if !viewer.Authenticated {
		api.Respond(c, api.ErrUnauthenticated("请先登录"))
		return
	}
	if viewer.IsSuspended {
		api.Respond(c, api.ErrSuspended("账号已停用，不能执行此操作"))
		return
	}
	if viewer.IsAI {
		api.Respond(c, api.ErrForbidden("AI 账号不能通过此接口操作"))
		return
	}
	postID, ok := pathID(c)
	if !ok {
		return
	}
	var hook AuditHook
	if viewer.IsAdmin {
		adminID := viewer.ID
		hook = func(ctx context.Context, tx pgx.Tx, deletedPostID, authorID int64) error {
			return audit.Write(ctx, tx, s.ids, adminID, audit.ActionPostDelete, "post", &deletedPostID, gin.H{
				"author_id": api.FormatID(authorID),
				"source":    "post_delete",
			})
		}
	}
	if _, err := s.DeletePostInternal(c.Request.Context(), viewer.ID, postID, viewer.IsAdmin, hook); err != nil {
		s.respond(c, "删除帖子失败", err)
		return
	}
	c.Status(http.StatusNoContent)
}

// AuditHook 允许调用方在删除事务内追加记录（例如管理后台写审计日志），
// 保证“处置已发生但没有审计记录”的窗口不存在。tx 与删除操作处于同一事务。
type AuditHook func(ctx context.Context, tx pgx.Tx, postID, authorID int64) error

// DeletePostInternal 供作者删除接口与管理后台复用同一套逻辑删除流程。
// asAdmin 为真时允许删除他人内容；返回被删除帖子的作者 ID 供审计记录使用。
// audit 可为 nil（作者自行删除不需要审计）。
func (s *Service) DeletePostInternal(
	ctx context.Context,
	actorID, postID int64,
	asAdmin bool,
	audit AuditHook,
) (int64, error) {
	pool := s.pools.Pool()
	if pool == nil {
		return 0, api.NewFailure(http.StatusServiceUnavailable, "service_unavailable", "服务尚未就绪，请稍后重试")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	post, err := loadPostForUpdate(ctx, tx, postID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, api.ErrNotFound("内容不存在或无权访问")
	}
	if err != nil {
		return 0, err
	}
	if !asAdmin && post.AuthorID != actorID {
		if err := access.RequireVisiblePost(ctx, tx, model.Viewer{ID: actorID, Authenticated: true}, post); err != nil {
			return 0, err
		}
		return 0, api.ErrForbidden("只有作者或管理员可以删除内容")
	}
	if post.Status == model.PostStatusDeleted {
		return post.AuthorID, nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE posts
		SET status = 'deleted', deleted_at = now(), scheduled_at = NULL, updated_at = now()
		WHERE id = $1`, postID); err != nil {
		return 0, err
	}
	if err := s.cancelPublishJobs(ctx, tx, postID); err != nil {
		return 0, err
	}
	// 搜索投影同步清空（逻辑删除，不物理删除行）。
	if _, err := tx.Exec(ctx, `
		UPDATE post_search SET plain_text = '', updated_at = now() WHERE post_id = $1`, postID); err != nil {
		return 0, err
	}
	if err := notice.DeactivateMentions(ctx, tx, notice.SourcePost, postID); err != nil {
		return 0, err
	}
	if audit != nil {
		if err := audit(ctx, tx, postID, post.AuthorID); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return post.AuthorID, nil
}

// PostByID 读取帖子行，供 AI 任务引擎与管理后台复用；不存在时返回 404。
func PostByID(ctx context.Context, q access.Querier, id int64) (model.Post, error) {
	post, err := loadPost(ctx, q, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Post{}, api.ErrNotFound("内容不存在")
	}
	return post, err
}

// ActorPostInput 描述一次以指定作者身份创建内容（AI 生成、管理员代发）。
// 这类调用不经过会话身份校验，调用方必须先完成鉴权。
type ActorPostInput struct {
	AuthorID      int64
	ContentFormat string
	ContentSource string
	Visibility    string // AI 作者只能是 public
	AttachmentIDs []int64
	QuotedPostID  *int64
	AsDraft       bool       // true → status=draft（AI 草稿仅管理员可见）；false → 直接发布
	ScheduledAt   *time.Time // 非空时创建定时帖（优先于 AsDraft），必须晚于当前时间至少 1 分钟
}

// CreatePostForActor 在调用方的事务内创建帖子：草稿、直接发布或定时发布
// （含版本、标签、搜索、提及）。定时发布先创建草稿工作区，再复用 applyCreateAction
// 置为 scheduled 并入队发布作业。作者账号停用时返回 api.ErrSuspended；
// 引用的原帖必须公开、已发布且作者未停用。
// 函数自身不开启事务：q 为事务时天然原子，为连接池时由调用方保证。
func (s *Service) CreatePostForActor(ctx context.Context, q access.Querier, in ActorPostInput) (model.Post, error) {
	if in.AuthorID <= 0 {
		return model.Post{}, api.ErrValidation("缺少作者账号")
	}
	var authorStatus string
	err := q.QueryRow(ctx, `SELECT status FROM users WHERE id = $1`, in.AuthorID).Scan(&authorStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Post{}, api.ErrNotFound("作者账号不存在")
	}
	if err != nil {
		return model.Post{}, err
	}
	if authorStatus != auth.StatusActive {
		return model.Post{}, api.ErrSuspended("账号已停用，不能创建内容")
	}

	// 定时发布优先于草稿/直接发布；时间校验必须在写入前完成，避免留下半成品。
	action := actionPublish
	if in.AsDraft {
		action = actionDraft
	}
	if in.ScheduledAt != nil {
		if err := validateFutureScheduledAt(*in.ScheduledAt, time.Now()); err != nil {
			return model.Post{}, err
		}
		action = actionSchedule
	}

	content, err := normalizeActorContent(ctx, q, in)
	if err != nil {
		return model.Post{}, err
	}

	postID, err := s.ids.Next()
	if err != nil {
		return model.Post{}, err
	}
	if _, err := q.Exec(ctx, `
		INSERT INTO posts (id, author_id, status, visibility, quoted_post_id)
		VALUES ($1, $2, 'draft', $3, $4)`,
		postID, in.AuthorID, content.visibility, content.quotedPostID); err != nil {
		return model.Post{}, err
	}
	if _, err := q.Exec(ctx, `
		INSERT INTO post_drafts (
			post_id, content_format, content_source, content_html, plain_text, draft_revision
		) VALUES ($1, $2, $3, $4, $5, 1)`,
		postID, content.format, content.source, content.html, content.plain); err != nil {
		return model.Post{}, err
	}
	if err := replaceDraftAttachments(ctx, q, postID, content.attachmentIDs); err != nil {
		return model.Post{}, err
	}
	if err := s.applyCreateAction(ctx, q, postID, in.AuthorID, action, in.ScheduledAt); err != nil {
		return model.Post{}, err
	}
	return PostByID(ctx, q, postID)
}

// normalizeActorContent 校验并渲染指定作者的内容，复用自助发布路径的同一套规则。
func normalizeActorContent(ctx context.Context, q access.Querier, in ActorPostInput) (normalizedPost, error) {
	format, err := normalizeFormat(in.ContentFormat)
	if err != nil {
		return normalizedPost{}, err
	}
	html, plain := richtext.Render(format, in.ContentSource)
	if strings.TrimSpace(plain) == "" {
		return normalizedPost{}, api.ErrValidation("正文不能为空").
			WithDetails(gin.H{"content_source": "正文不能为空"})
	}
	visibility, err := normalizeVisibility(in.Visibility)
	if err != nil {
		return normalizedPost{}, err
	}
	if err := ensureVisibilityAllowed(ctx, q, in.AuthorID, visibility); err != nil {
		return normalizedPost{}, err
	}
	attachmentIDs := uniqueIDs(in.AttachmentIDs)
	if err := ensureAttachmentsUsable(ctx, q, in.AuthorID, attachmentIDs); err != nil {
		return normalizedPost{}, err
	}
	content := normalizedPost{
		format:        format,
		source:        in.ContentSource,
		html:          html,
		plain:         plain,
		visibility:    visibility,
		attachmentIDs: attachmentIDs,
	}
	if in.QuotedPostID != nil {
		if err := ensureQuotablePost(ctx, q, *in.QuotedPostID); err != nil {
			return normalizedPost{}, err
		}
		content.quotedPostID = in.QuotedPostID
	}
	return content, nil
}

// PublishPostForActor 立即发布指定草稿（AI 草稿发布、管理员代发共用）；已发布时幂等返回 nil。
func (s *Service) PublishPostForActor(ctx context.Context, q access.Querier, postID, actorID int64) error {
	post, err := PostByID(ctx, q, postID)
	if err != nil {
		return err
	}
	switch post.Status {
	case model.PostStatusPublished:
		return nil
	case model.PostStatusDraft, model.PostStatusScheduled:
		return s.publishLocked(ctx, q, postID, actorID)
	default:
		return api.ErrForbidden("当前内容状态不能发布")
	}
}

// UpdateDraftForActor 以指定作者身份覆盖草稿工作区内容（管理员编辑 AI 草稿用），
// 不做 revision 校验；已发布内容会追加一个新版本，可见性与附件沿用当前状态。
func (s *Service) UpdateDraftForActor(ctx context.Context, q access.Querier, postID int64, format, source string) error {
	normalizedFormat, err := normalizeFormat(format)
	if err != nil {
		return err
	}
	html, plain := richtext.Render(normalizedFormat, source)
	if strings.TrimSpace(plain) == "" {
		return api.ErrValidation("正文不能为空").
			WithDetails(gin.H{"content_source": "正文不能为空"})
	}
	post, err := PostByID(ctx, q, postID)
	if err != nil {
		return err
	}
	if post.Status == model.PostStatusDeleted {
		return api.ErrNotFound("内容不存在")
	}
	if post.Status == model.PostStatusPublished {
		attachments, err := activeDraftAttachmentIDs(ctx, q, postID)
		if err != nil {
			return err
		}
		_, err = s.appendVersion(ctx, q, post, normalizedPost{
			format:        normalizedFormat,
			source:        source,
			html:          html,
			plain:         plain,
			visibility:    post.Visibility,
			attachmentIDs: attachments,
		})
		return err
	}
	tag, err := q.Exec(ctx, `
		UPDATE post_drafts
		SET content_format = $2, content_source = $3, content_html = $4, plain_text = $5,
		    draft_revision = draft_revision + 1, updated_at = now()
		WHERE post_id = $1`, postID, normalizedFormat, source, html, plain)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return api.ErrNotFound("草稿不存在")
	}
	return nil
}
