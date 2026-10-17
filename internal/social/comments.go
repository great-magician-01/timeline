package social

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/great-magician-01/timeline/internal/access"
	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/audit"
	"github.com/great-magician-01/timeline/internal/auth"
	"github.com/great-magician-01/timeline/internal/model"
	"github.com/great-magician-01/timeline/internal/notice"
	"github.com/great-magician-01/timeline/internal/richtext"
)

const (
	// replyPreviewSize 顶层评论在列表中携带的回复预览条数。
	replyPreviewSize = 2
	// maxCommentDepth 计算回复深度时的防环上限。
	maxCommentDepth = 256
)

// CommentResponse 是评论与回复的对外 DTO（docs/API.md §8）。
type CommentResponse struct {
	ID              string                  `json:"id"`
	PostID          string                  `json:"post_id"`
	Author          model.UserBriefResponse `json:"author"`
	ContentHTML     string                  `json:"content_html"`
	Status          string                  `json:"status"`
	ParentCommentID *string                 `json:"parent_comment_id,omitempty"`
	RootCommentID   *string                 `json:"root_comment_id,omitempty"`
	ReplyCount      int                     `json:"reply_count"`
	LikeCount       int                     `json:"like_count"`
	LikedByMe       bool                    `json:"liked_by_me"`
	CanDelete       bool                    `json:"can_delete"`
	CreatedAt       string                  `json:"created_at"`
	ReplyPreview    []CommentResponse       `json:"reply_preview,omitempty"` // 仅列表接口填充，嵌套不递归
}

// CommentInput 描述一次评论或回复；AI 动作执行走同一入口。
type CommentInput struct {
	ActorID int64
	IsAI    bool
	// IsAdmin 保留字段：管理后台复用该入口时用于区分处置来源，当前不影响评论写入规则。
	IsAdmin  bool
	PostID   int64
	ParentID *int64
	Format   string // richtext | markdown
	Source   string
}

// optionalIDFromBody 解析请求体里的可选 ID：缺省、空串与空白视为未提供。
func optionalIDFromBody(raw *string) (*int64, error) {
	if raw == nil {
		return nil, nil
	}
	value := strings.TrimSpace(*raw)
	if value == "" {
		return nil, nil
	}
	id, err := api.ParseID(value)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// threadPosition 推导回复的 parent / root：顶层评论两者都为空；
// 父评论本身是顶层时 root 就是父评论 ID。
func threadPosition(parentID int64, parentRootID *int64) (parent, root *int64) {
	parent = &parentID
	if parentRootID != nil {
		return parent, parentRootID
	}
	return parent, parent
}

// validateCommentBody 校验渲染后的正文非空；只包含本站附件图片的正文视为非空。
func validateCommentBody(safeHTML, plain string) error {
	if strings.TrimSpace(plain) != "" {
		return nil
	}
	if strings.Contains(safeHTML, "<img") {
		return nil
	}
	return api.ErrValidation("评论内容不能为空")
}

// commentCanDelete 判断访问者能否删除评论：评论作者、帖子作者或管理员。
func commentCanDelete(comment model.Comment, postAuthorID int64, viewer model.Viewer) bool {
	if comment.Deleted() {
		return false
	}
	if viewer.IsAdmin {
		return true
	}
	if !viewer.Authenticated {
		return false
	}
	return viewer.ID == comment.AuthorID || viewer.ID == postAuthorID
}

// commentResponseFor 组装评论 DTO。已删除评论保留占位：正文为空、不能删除，
// 且不再显示“我点过赞”（docs/API.md §8）。
func commentResponseFor(
	comment model.Comment,
	postAuthorID int64,
	author model.UserBrief,
	viewer model.Viewer,
	likedByMe bool,
) CommentResponse {
	resp := CommentResponse{
		ID:          api.FormatID(comment.ID),
		PostID:      api.FormatID(comment.PostID),
		Author:      author.Response(),
		ContentHTML: comment.ContentHTML,
		Status:      comment.Status,
		ReplyCount:  comment.ReplyCount,
		LikeCount:   comment.LikeCount,
		LikedByMe:   likedByMe,
		CanDelete:   commentCanDelete(comment, postAuthorID, viewer),
		CreatedAt:   api.FormatTime(comment.CreatedAt),
	}
	if comment.ParentCommentID != nil {
		id := api.FormatID(*comment.ParentCommentID)
		resp.ParentCommentID = &id
	}
	if comment.RootCommentID != nil {
		id := api.FormatID(*comment.RootCommentID)
		resp.RootCommentID = &id
	}
	if comment.Deleted() {
		resp.ContentHTML = ""
		resp.LikedByMe = false
		resp.CanDelete = false
	}
	return resp
}

// ListComments 列出顶层评论（按 ID 升序，游标分页），每项带前两条回复预览。
// 已删除评论保留在原位显示占位。
func (s *Service) ListComments(c *gin.Context) {
	ctx := c.Request.Context()
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	postID, err := api.ParseID(c.Param("id"))
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	viewer := viewerForRead(c)
	post, err := LoadVisiblePost(ctx, pool, viewer, postID)
	if err != nil {
		s.fail(c, "读取评论所属帖子失败", err)
		return
	}
	limit, cursor, err := listParams(c)
	if err != nil {
		s.fail(c, "分页参数无效", err)
		return
	}

	rows, err := pool.Query(ctx, `
		SELECT `+model.CommentColumns+`
		FROM comments
		WHERE post_id = $1 AND parent_comment_id IS NULL AND id > $2
		ORDER BY id ASC
		LIMIT $3`, postID, cursor, limit+1)
	if err != nil {
		s.internalError(c, "查询评论列表失败", err)
		return
	}
	comments, err := scanComments(rows)
	if err != nil {
		s.internalError(c, "读取评论列表失败", err)
		return
	}
	items, hasMore := trimPage(comments, limit)

	views, err := commentViews(ctx, pool, viewer, post.AuthorID, items)
	if err != nil {
		s.internalError(c, "组装评论列表失败", err)
		return
	}
	previews, err := replyPreviews(ctx, pool, viewer, post.AuthorID, items)
	if err != nil {
		s.internalError(c, "读取回复预览失败", err)
		return
	}

	responses := make([]CommentResponse, 0, len(items))
	var lastID int64
	for _, comment := range items {
		resp := views[comment.ID]
		if preview := previews[comment.ID]; len(preview) > 0 {
			resp.ReplyPreview = preview
		}
		responses = append(responses, resp)
		lastID = comment.ID
	}
	c.JSON(http.StatusOK, api.ListResponse(responses, lastID, hasMore))
}

// ListReplies 列出某条顶层评论下的全部回复（按 ID 升序，游标分页）。
func (s *Service) ListReplies(c *gin.Context) {
	ctx := c.Request.Context()
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	commentID, err := api.ParseID(c.Param("id"))
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	viewer := viewerForRead(c)

	var postID int64
	err = pool.QueryRow(ctx, `SELECT post_id FROM comments WHERE id = $1`, commentID).Scan(&postID)
	if errors.Is(err, pgx.ErrNoRows) {
		s.fail(c, "读取回复列表失败", api.ErrNotFound("评论不存在或无权访问"))
		return
	}
	if err != nil {
		s.internalError(c, "读取评论失败", err)
		return
	}
	post, err := LoadVisiblePost(ctx, pool, viewer, postID)
	if err != nil {
		s.fail(c, "读取回复所属帖子失败", err)
		return
	}
	limit, cursor, err := listParams(c)
	if err != nil {
		s.fail(c, "分页参数无效", err)
		return
	}

	rows, err := pool.Query(ctx, `
		SELECT `+model.CommentColumns+`
		FROM comments
		WHERE root_comment_id = $1 AND parent_comment_id IS NOT NULL AND id > $2
		ORDER BY id ASC
		LIMIT $3`, commentID, cursor, limit+1)
	if err != nil {
		s.internalError(c, "查询回复列表失败", err)
		return
	}
	comments, err := scanComments(rows)
	if err != nil {
		s.internalError(c, "读取回复列表失败", err)
		return
	}
	items, hasMore := trimPage(comments, limit)

	views, err := commentViews(ctx, pool, viewer, post.AuthorID, items)
	if err != nil {
		s.internalError(c, "组装回复列表失败", err)
		return
	}
	responses := make([]CommentResponse, 0, len(items))
	var lastID int64
	for _, comment := range items {
		responses = append(responses, views[comment.ID])
		lastID = comment.ID
	}
	c.JSON(http.StatusOK, api.ListResponse(responses, lastID, hasMore))
}

// CreateComment 发表评论或回复：正文先经 richtext 渲染为安全 HTML 再入库。
func (s *Service) CreateComment(c *gin.Context) {
	ctx := c.Request.Context()
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	viewer, ok := s.requireViewer(c)
	if !ok {
		return
	}
	postID, err := api.ParseID(c.Param("id"))
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	post, err := LoadVisiblePost(ctx, pool, viewer, postID)
	if err != nil {
		s.fail(c, "读取评论所属帖子失败", err)
		return
	}
	if err := access.RequireInteractivePost(ctx, pool, viewer, post); err != nil {
		s.fail(c, "检查评论权限失败", err)
		return
	}
	if !s.allowAction(c, s.commentLimiter, viewer.ID) {
		return
	}

	var in struct {
		ContentFormat   string  `json:"content_format"`
		ContentSource   string  `json:"content_source"`
		ParentCommentID *string `json:"parent_comment_id"`
	}
	if err := api.DecodeJSON(c, &in); err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", "请求体必须是有效的 JSON 对象")
		return
	}
	if !richtext.ValidFormat(in.ContentFormat) {
		api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", "评论内容格式无效",
			gin.H{"content_format": "只支持 richtext 或 markdown"})
		return
	}
	parentID, err := optionalIDFromBody(in.ParentCommentID)
	if err != nil {
		api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", "父评论参数无效",
			gin.H{"parent_comment_id": "必须是有效的评论 ID"})
		return
	}

	tx, ok := s.beginTx(c, pool)
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	comment, err := s.CreateCommentFor(ctx, tx, CommentInput{
		ActorID:  viewer.ID,
		IsAdmin:  viewer.IsAdmin,
		PostID:   postID,
		ParentID: parentID,
		Format:   in.ContentFormat,
		Source:   in.ContentSource,
	})
	if err != nil {
		s.fail(c, "发表评论失败", err)
		return
	}
	if !s.commit(c, tx) {
		return
	}
	author, _ := auth.CurrentUser(c)
	c.JSON(http.StatusCreated, gin.H{
		"comment": commentResponseFor(comment, post.AuthorID, model.UserBriefFromAuth(author), viewer, false),
	})
}

// CreateCommentFor 发表评论或回复的核心实现（供 AI 动作执行复用）。
// AI 只允许在公开且已发布的帖子下评论；父评论必须属于同一帖子且未删除。
func (s *Service) CreateCommentFor(ctx context.Context, q access.Querier, in CommentInput) (model.Comment, error) {
	if in.ActorID <= 0 || in.PostID <= 0 {
		return model.Comment{}, api.ErrValidation("评论参数无效")
	}
	if !richtext.ValidFormat(in.Format) {
		return model.Comment{}, api.ErrValidation("评论内容格式无效")
	}
	if err := access.RequireActorWritable(ctx, q, in.ActorID); err != nil {
		return model.Comment{}, err
	}
	post, err := loadPostForInteraction(ctx, q, in.PostID, in.IsAI)
	if err != nil {
		return model.Comment{}, err
	}
	if post.AuthorID != in.ActorID {
		if err := ensureAuthorActive(ctx, q, post.AuthorID); err != nil {
			return model.Comment{}, err
		}
	}

	var (
		parentID       *int64
		rootID         *int64
		parentAuthorID int64
	)
	if in.ParentID != nil {
		parent, err := model.ScanComment(q.QueryRow(ctx,
			`SELECT `+model.CommentColumns+` FROM comments WHERE id = $1 FOR UPDATE`, *in.ParentID))
		if errors.Is(err, pgx.ErrNoRows) {
			return model.Comment{}, api.ErrValidation("父评论不存在")
		}
		if err != nil {
			return model.Comment{}, err
		}
		if parent.PostID != in.PostID {
			return model.Comment{}, api.ErrValidation("父评论不属于该帖子")
		}
		if parent.Deleted() {
			return model.Comment{}, api.ErrValidation("父评论已删除，不能回复")
		}
		// 父评论作者停用后公开内容仍可读，但不再接受新的回复互动。
		if parent.AuthorID != in.ActorID {
			if err := ensureAuthorActive(ctx, q, parent.AuthorID); err != nil {
				return model.Comment{}, err
			}
		}
		parentID, rootID = threadPosition(parent.ID, parent.RootCommentID)
		parentAuthorID = parent.AuthorID
	}

	safeHTML, plain := richtext.Render(in.Format, in.Source)
	if err := validateCommentBody(safeHTML, plain); err != nil {
		return model.Comment{}, err
	}
	id, err := s.ids.Next()
	if err != nil {
		return model.Comment{}, err
	}
	comment, err := model.ScanComment(q.QueryRow(ctx, `
		INSERT INTO comments (
			id, post_id, author_id, parent_comment_id, root_comment_id,
			content_format, content_source, content_html, plain_text, status,
			like_count, reply_count, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'published', 0, 0, now(), now())
		RETURNING `+model.CommentColumns,
		id, in.PostID, in.ActorID, parentID, rootID, in.Format, in.Source, safeHTML, plain))
	if err != nil {
		return model.Comment{}, err
	}

	if parentID != nil {
		if _, err := q.Exec(ctx,
			`UPDATE comments SET reply_count = reply_count + 1 WHERE id = $1`, *parentID); err != nil {
			return model.Comment{}, err
		}
	}
	if _, err := q.Exec(ctx,
		`UPDATE posts SET comment_count = comment_count + 1 WHERE id = $1`, in.PostID); err != nil {
		return model.Comment{}, err
	}
	if err := s.notifyComment(ctx, q, in, comment, post, parentID, parentAuthorID, plain); err != nil {
		return model.Comment{}, err
	}
	return comment, nil
}

// notifyComment 生成评论与回复通知，并在公开帖子下重建提及。
// 被回复者就是帖子作者时只发一条通知（docs/WORKFLOWS.md 9.1）。
func (s *Service) notifyComment(
	ctx context.Context,
	q access.Querier,
	in CommentInput,
	comment model.Comment,
	post model.Post,
	parentID *int64,
	parentAuthorID int64,
	plain string,
) error {
	commentID := comment.ID
	postID := post.ID
	notify := func(kind string, noticeType string, recipientID int64) error {
		return notice.Create(ctx, q, s.ids, notice.Params{
			IdempotencyKey: notifyKey(kind, commentID),
			RecipientID:    recipientID,
			ActorID:        in.ActorID,
			Type:           noticeType,
			TargetType:     targetComment,
			TargetID:       commentID,
			PostID:         &postID,
			CommentID:      &commentID,
		})
	}
	switch {
	case parentID == nil:
		if err := notify("comment", notice.TypeComment, post.AuthorID); err != nil {
			return err
		}
	case parentAuthorID == post.AuthorID:
		// 被回复者就是帖子作者：只发一条回复通知。
		if err := notify("reply", notice.TypeReply, parentAuthorID); err != nil {
			return err
		}
	default:
		if err := notify("reply", notice.TypeReply, parentAuthorID); err != nil {
			return err
		}
		if err := notify("comment", notice.TypeComment, post.AuthorID); err != nil {
			return err
		}
	}
	// 提及只从公开内容生成，非公开评论不触发 AI 读取与提及通知。
	if post.Visibility != model.VisibilityPublic {
		return nil
	}
	names := richtext.ExtractMentions(plain)
	if err := notice.RebuildMentions(ctx, q, s.ids, notice.SourceComment, commentID, postID, names); err != nil {
		return err
	}
	return notice.MentionNotifications(ctx, q, s.ids, notice.SourceComment, commentID, postID, in.ActorID, names)
}

// DeleteComment 逻辑删除评论：评论作者、帖子作者或管理员；管理员操作写审计。
func (s *Service) DeleteComment(c *gin.Context) {
	ctx := c.Request.Context()
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	viewer, ok := s.requireViewer(c)
	if !ok {
		return
	}
	commentID, err := api.ParseID(c.Param("id"))
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	tx, ok := s.beginTx(c, pool)
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	byAdmin := viewer.IsAdmin
	if err := s.DeleteCommentFor(ctx, tx, viewer.ID, byAdmin, commentID); err != nil {
		s.fail(c, "删除评论失败", err)
		return
	}
	if byAdmin {
		detail := gin.H{"deleted_by_admin": true}
		if err := audit.Write(ctx, tx, s.ids, viewer.ID, audit.ActionCommentDelete, "comment", &commentID, detail); err != nil {
			s.internalError(c, "写入评论删除审计失败", err)
			return
		}
	}
	if !s.commit(c, tx) {
		return
	}
	c.Status(http.StatusNoContent)
}

// DeleteCommentFor 逻辑删除评论的核心实现：不级联删除子回复，
// 同步回退帖子评论数与父评论回复数（计数不允许为负），幂等。
func (s *Service) DeleteCommentFor(ctx context.Context, q access.Querier, actorID int64, byAdmin bool, commentID int64) error {
	comment, err := model.ScanComment(q.QueryRow(ctx,
		`SELECT `+model.CommentColumns+` FROM comments WHERE id = $1 FOR UPDATE`, commentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return api.ErrNotFound("评论不存在")
	}
	if err != nil {
		return err
	}
	var postAuthorID int64
	err = q.QueryRow(ctx, `SELECT author_id FROM posts WHERE id = $1`, comment.PostID).Scan(&postAuthorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.ErrNotFound("评论不存在")
	}
	if err != nil {
		return err
	}
	if err := access.RequireActorWritable(ctx, q, actorID); err != nil {
		return err
	}
	if !byAdmin && actorID != comment.AuthorID && actorID != postAuthorID {
		// 无权处置时按“不存在”处理，不暴露他人评论。
		return api.ErrNotFound("评论不存在")
	}
	if comment.Deleted() {
		return nil
	}

	// 记录处置角色，便于事后区分“评论作者删除 / 帖子作者删除 / 管理员处置”
	// （docs/REQUIREMENTS.md 5.1）。三者互斥，只有逻辑删除的行才会带标记。
	if _, err := q.Exec(ctx, `
		UPDATE comments
		SET status = 'deleted',
		    deleted_by_admin = $2,
		    deleted_by_author = $3,
		    deleted_by_post_author = $4,
		    updated_at = now()
		WHERE id = $1 AND status = 'published'`,
		commentID,
		byAdmin,
		!byAdmin && actorID == comment.AuthorID,
		!byAdmin && actorID == postAuthorID && actorID != comment.AuthorID,
	); err != nil {
		return err
	}
	if _, err := q.Exec(ctx,
		`UPDATE posts SET comment_count = GREATEST(comment_count - 1, 0) WHERE id = $1`, comment.PostID); err != nil {
		return err
	}
	if comment.ParentCommentID != nil {
		if _, err := q.Exec(ctx,
			`UPDATE comments SET reply_count = GREATEST(reply_count - 1, 0) WHERE id = $1`,
			*comment.ParentCommentID); err != nil {
			return err
		}
	}
	// 已删除评论不再触发 AI 读取或提及通知。
	return notice.DeactivateMentions(ctx, q, notice.SourceComment, commentID)
}

// CommentDepth 返回评论的回复深度（顶层 = 1），用于 AI 讨论深度限制。
func (s *Service) CommentDepth(ctx context.Context, q access.Querier, commentID int64) (int, error) {
	if commentID <= 0 {
		return 0, api.ErrNotFound("评论不存在")
	}
	depth := 1
	current := commentID
	for depth < maxCommentDepth {
		var parent *int64
		err := q.QueryRow(ctx, `SELECT parent_comment_id FROM comments WHERE id = $1`, current).Scan(&parent)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, api.ErrNotFound("评论不存在")
		}
		if err != nil {
			return 0, err
		}
		if parent == nil {
			return depth, nil
		}
		current = *parent
		depth++
	}
	return depth, nil
}

// scanComments 读取并关闭行集。
func scanComments(rows pgx.Rows) ([]model.Comment, error) {
	defer rows.Close()
	var comments []model.Comment
	for rows.Next() {
		comment, err := model.ScanComment(rows)
		if err != nil {
			return nil, err
		}
		comments = append(comments, comment)
	}
	return comments, rows.Err()
}

// commentViews 批量组装评论 DTO，避免逐条查询作者与点赞状态。
func commentViews(
	ctx context.Context,
	q access.Querier,
	viewer model.Viewer,
	postAuthorID int64,
	comments []model.Comment,
) (map[int64]CommentResponse, error) {
	result := make(map[int64]CommentResponse, len(comments))
	if len(comments) == 0 {
		return result, nil
	}
	authorIDs := make([]int64, 0, len(comments))
	commentIDs := make([]int64, 0, len(comments))
	for _, comment := range comments {
		authorIDs = append(authorIDs, comment.AuthorID)
		commentIDs = append(commentIDs, comment.ID)
	}
	authors, err := access.UserBriefsByID(ctx, q, authorIDs)
	if err != nil {
		return nil, err
	}
	liked, err := likedCommentIDs(ctx, q, viewer.ID, commentIDs)
	if err != nil {
		return nil, err
	}
	for _, comment := range comments {
		result[comment.ID] = commentResponseFor(
			comment, postAuthorID, authors[comment.AuthorID], viewer, liked[comment.ID])
	}
	return result, nil
}

// likedCommentIDs 批量查询访问者点过赞的评论。
func likedCommentIDs(ctx context.Context, q access.Querier, userID int64, ids []int64) (map[int64]bool, error) {
	result := make(map[int64]bool, len(ids))
	if userID <= 0 || len(ids) == 0 {
		return result, nil
	}
	rows, err := q.Query(ctx, `
		SELECT target_id FROM likes
		WHERE user_id = $1 AND target_type = 'comment' AND target_id = ANY($2) AND is_active`,
		userID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result[id] = true
	}
	return result, rows.Err()
}

// replyPreviews 为每个顶层评论取前 replyPreviewSize 条回复（按 ID 升序）。
func replyPreviews(
	ctx context.Context,
	q access.Querier,
	viewer model.Viewer,
	postAuthorID int64,
	comments []model.Comment,
) (map[int64][]CommentResponse, error) {
	result := map[int64][]CommentResponse{}
	if len(comments) == 0 {
		return result, nil
	}
	ids := make([]int64, 0, len(comments))
	for _, comment := range comments {
		ids = append(ids, comment.ID)
	}
	rows, err := q.Query(ctx, `
		SELECT `+model.CommentColumns+`
		FROM (
			SELECT *, ROW_NUMBER() OVER (PARTITION BY root_comment_id ORDER BY id) AS rn
			FROM comments
			WHERE root_comment_id = ANY($1) AND parent_comment_id IS NOT NULL
		) AS preview
		WHERE rn <= $2
		ORDER BY root_comment_id, id`, ids, replyPreviewSize)
	if err != nil {
		return nil, err
	}
	replies, err := scanComments(rows)
	if err != nil {
		return nil, err
	}
	views, err := commentViews(ctx, q, viewer, postAuthorID, replies)
	if err != nil {
		return nil, err
	}
	for _, reply := range replies {
		if reply.RootCommentID == nil {
			continue
		}
		result[*reply.RootCommentID] = append(result[*reply.RootCommentID], views[reply.ID])
	}
	return result, nil
}
