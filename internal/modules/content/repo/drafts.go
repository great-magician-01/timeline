package repo

import (
	"context"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/content/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// Draft 是帖子草稿工作区的最新内容。
type Draft struct {
	PostID    int64
	Format    string
	Source    string
	HTML      string
	Plain     string
	Revision  int
	UpdatedAt time.Time
}

// GetDraft 读取草稿工作区；不存在返回领域 NotFound。
func (r *Repo) GetDraft(ctx context.Context, postID int64) (Draft, error) {
	return r.scanDraft(r.ex(ctx).QueryRow(ctx, `
		SELECT post_id, content_format, content_source, content_html, plain_text,
		       draft_revision, updated_at
		FROM post_drafts WHERE post_id = $1`, postID))
}

// GetDraftForUpdate 锁定草稿工作区行，用于自动保存冲突检测。
func (r *Repo) GetDraftForUpdate(ctx context.Context, postID int64) (Draft, error) {
	return r.scanDraft(r.ex(ctx).QueryRow(ctx, `
		SELECT post_id, content_format, content_source, content_html, plain_text,
		       draft_revision, updated_at
		FROM post_drafts WHERE post_id = $1 FOR UPDATE`, postID))
}

func (r *Repo) scanDraft(row scanner) (Draft, error) {
	var draft Draft
	err := row.Scan(&draft.PostID, &draft.Format, &draft.Source, &draft.HTML,
		&draft.Plain, &draft.Revision, &draft.UpdatedAt)
	return draft, notFoundIfNoRows(err, "草稿不存在")
}

// InsertDraft 写入草稿工作区（revision 从 1 开始）；updated_at 由调用方显式传入。
func (r *Repo) InsertDraft(ctx context.Context, draft Draft) error {
	revision := draft.Revision
	if revision <= 0 {
		revision = 1
	}
	_, err := r.ex(ctx).Exec(ctx, `
		INSERT INTO post_drafts (
			post_id, content_format, content_source, content_html, plain_text,
			draft_revision, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		draft.PostID, draft.Format, draft.Source, draft.HTML, draft.Plain,
		revision, draft.UpdatedAt)
	return err
}

// SaveDraftContent 覆盖草稿正文；bumpRevision 为真时草稿版本号 +1（自动保存冲突检测）。
func (r *Repo) SaveDraftContent(
	ctx context.Context,
	postID int64,
	format, source, html, plain string,
	at time.Time,
	bumpRevision bool,
) error {
	revisionExpr := "draft_revision"
	if bumpRevision {
		revisionExpr = "draft_revision + 1"
	}
	tag, err := r.ex(ctx).Exec(ctx, `
		UPDATE post_drafts
		SET content_format = $2, content_source = $3, content_html = $4, plain_text = $5,
		    draft_revision = `+revisionExpr+`, updated_at = $6
		WHERE post_id = $1`,
		postID, format, source, html, plain, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("草稿不存在")
	}
	return nil
}

// SyncDraftAfterPublish 把草稿工作区同步为当前正文（已发布内容追加版本后调用）。
// 草稿行不存在时视为无需同步，不返回错误。
func (r *Repo) SyncDraftAfterPublish(
	ctx context.Context,
	postID int64,
	format, source, html, plain string,
	at time.Time,
) error {
	_, err := r.ex(ctx).Exec(ctx, `
		UPDATE post_drafts
		SET content_format = $2, content_source = $3, content_html = $4, plain_text = $5,
		    draft_revision = draft_revision + 1, updated_at = $6
		WHERE post_id = $1`,
		postID, format, source, html, plain, at)
	return err
}

// ReplaceDraftAttachments 重建草稿工作区附件集合：旧关联置为失效后批量 upsert（不物理删除）。
// 单条 unnest 语句完成全部附件写入，sort_order 保持原先逐条追加一致的 0 起始顺序。
func (r *Repo) ReplaceDraftAttachments(ctx context.Context, postID int64, attachmentIDs []int64) error {
	exec := r.ex(ctx)
	if _, err := exec.Exec(ctx, `
		UPDATE post_draft_attachments SET is_active = FALSE
		WHERE post_id = $1 AND is_active`, postID); err != nil {
		return err
	}
	ids := domain.UniqueIDs(attachmentIDs)
	if len(ids) == 0 {
		return nil
	}
	_, err := exec.Exec(ctx, `
		INSERT INTO post_draft_attachments (post_id, attachment_id, sort_order, is_active)
		SELECT $1, t.attachment_id, (t.ord - 1)::int, TRUE
		FROM unnest($2::bigint[]) WITH ORDINALITY AS t(attachment_id, ord)
		ON CONFLICT (post_id, attachment_id)
		DO UPDATE SET sort_order = EXCLUDED.sort_order, is_active = TRUE`, postID, ids)
	return err
}

// DraftAttachmentIDs 读取草稿工作区中生效的附件 ID（按排序）。
func (r *Repo) DraftAttachmentIDs(ctx context.Context, postID int64) ([]int64, error) {
	return r.draftAttachmentIDsFor(ctx, []int64{postID}, postID)
}

// DraftAttachmentIDsMany 批量读取草稿工作区的生效附件：post_id → attachment_id 列表（按排序）。
func (r *Repo) DraftAttachmentIDsMany(ctx context.Context, postIDs []int64) (map[int64][]int64, error) {
	result := make(map[int64][]int64, len(postIDs))
	if len(postIDs) == 0 {
		return result, nil
	}
	rows, err := r.ex(ctx).Query(ctx, `
		SELECT post_id, attachment_id FROM post_draft_attachments
		WHERE post_id = ANY($1) AND is_active
		ORDER BY post_id, sort_order, attachment_id`, postIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var postID, attachmentID int64
		if err := rows.Scan(&postID, &attachmentID); err != nil {
			return nil, err
		}
		result[postID] = append(result[postID], attachmentID)
	}
	return result, rows.Err()
}

func (r *Repo) draftAttachmentIDsFor(ctx context.Context, postIDs []int64, scope int64) ([]int64, error) {
	all, err := r.DraftAttachmentIDsMany(ctx, postIDs)
	if err != nil {
		return nil, err
	}
	return all[scope], nil
}

// DraftsMany 批量读取草稿工作区：post_id → Draft。
func (r *Repo) DraftsMany(ctx context.Context, postIDs []int64) (map[int64]Draft, error) {
	result := make(map[int64]Draft, len(postIDs))
	if len(postIDs) == 0 {
		return result, nil
	}
	rows, err := r.ex(ctx).Query(ctx, `
		SELECT post_id, content_format, content_source, content_html, plain_text,
		       draft_revision, updated_at
		FROM post_drafts WHERE post_id = ANY($1)`, postIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		draft, err := r.scanDraft(rows)
		if err != nil {
			return nil, err
		}
		result[draft.PostID] = draft
	}
	return result, rows.Err()
}

// MyPostRow 是「我的草稿 / 我的定时发布」列表行：帖子行 + 草稿正文 + 附件数量。
// 最新发布作业状态由 app 通过 contracts.JobsQueue 补充（background_jobs 归 platform/jobs）。
type MyPostRow struct {
	Post            model.Post
	ContentHTML     string
	PlainText       string
	Revision        int
	AttachmentCount int
}

// ListMyPosts 读取某作者指定状态的草稿/定时帖列表，按 ID 倒序游标分页。
func (r *Repo) ListMyPosts(
	ctx context.Context,
	authorID int64,
	status string,
	cursorID int64,
	limit int,
) ([]MyPostRow, bool, error) {
	sql := `SELECT ` + postColumnsP + `,
			COALESCE(d.content_html, ''), COALESCE(d.plain_text, ''),
			COALESCE(d.draft_revision, 0), COALESCE(att.total, 0)
		FROM posts p
		LEFT JOIN post_drafts d ON d.post_id = p.id
		LEFT JOIN LATERAL (
			SELECT COUNT(*) AS total FROM post_draft_attachments da
			WHERE da.post_id = p.id AND da.is_active
		) att ON TRUE
		WHERE p.author_id = $1 AND p.status = $2`
	args := []any{authorID, status}
	if cursorID > 0 {
		args = append(args, cursorID)
		sql += " AND p.id < $" + itoa(len(args))
	}
	args = append(args, limit+1)
	sql += " ORDER BY p.id DESC LIMIT $" + itoa(len(args))

	rows, err := r.ex(ctx).Query(ctx, sql, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	items := make([]MyPostRow, 0, limit+1)
	for rows.Next() {
		var row MyPostRow
		post, err := scanPostWith(rows,
			&row.ContentHTML, &row.PlainText, &row.Revision, &row.AttachmentCount)
		if err != nil {
			return nil, false, err
		}
		row.Post = post
		items = append(items, row)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	return items, hasMore, nil
}

// scanPostWith 扫描「posts 全列 + 额外列」，避免每个查询重复列清单。
func scanPostWith(row scanner, extra ...any) (model.Post, error) {
	var post model.Post
	dest := []any{
		&post.ID, &post.AuthorID, &post.Status, &post.Visibility, &post.CurrentVersionID,
		&post.QuotedPostID, &post.ScheduledAt, &post.PublishedAt, &post.DeletedAt,
		&post.LikeCount, &post.CommentCount, &post.RepostCount, &post.CreatedAt, &post.UpdatedAt,
	}
	dest = append(dest, extra...)
	return post, row.Scan(dest...)
}

// DraftSummaryRow 是按作者读取草稿（agent AI 草稿管理）的列表行。
type DraftSummaryRow struct {
	Post          model.Post
	ContentHTML   string
	DraftRevision int
}

// ListDraftsByAuthor 按作者读取草稿（status=draft），按 ID 倒序游标分页。
func (r *Repo) ListDraftsByAuthor(
	ctx context.Context,
	authorID int64,
	cursorID int64,
	limit int,
) ([]DraftSummaryRow, error) {
	sql := `SELECT ` + postColumnsP + `, COALESCE(d.content_html, ''), COALESCE(d.draft_revision, 0)
		FROM posts p
		LEFT JOIN post_drafts d ON d.post_id = p.id
		WHERE p.author_id = $1 AND p.status = $2`
	args := []any{authorID, model.PostStatusDraft}
	if cursorID > 0 {
		args = append(args, cursorID)
		sql += " AND p.id < $" + itoa(len(args))
	}
	args = append(args, limit)
	sql += " ORDER BY p.id DESC LIMIT $" + itoa(len(args))

	rows, err := r.ex(ctx).Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]DraftSummaryRow, 0, limit)
	for rows.Next() {
		var item DraftSummaryRow
		post, err := scanPostWith(rows, &item.ContentHTML, &item.DraftRevision)
		if err != nil {
			return nil, err
		}
		item.Post = post
		items = append(items, item)
	}
	return items, rows.Err()
}

// DetachAttachmentFromDrafts 解除附件与草稿工作区的生效关联，返回是否发生变更。
func (r *Repo) DetachAttachmentFromDrafts(ctx context.Context, attachmentID int64) (bool, error) {
	tag, err := r.ex(ctx).Exec(ctx, `
		UPDATE post_draft_attachments SET is_active = FALSE
		WHERE attachment_id = $1 AND is_active`, attachmentID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// sqlActiveDraftAttachmentIDs 读取仍被生效草稿工作区引用的附件 ID；
// 单独定义便于纯函数测试断言（media 清理流程依赖该查询结果）。
const sqlActiveDraftAttachmentIDs = `
	SELECT DISTINCT attachment_id FROM post_draft_attachments
	WHERE is_active AND attachment_id = ANY($1)
	ORDER BY attachment_id`

// ActiveDraftAttachmentIDs 返回仍被生效草稿工作区引用的附件 ID（升序、去重）。
// 入参为空时直接返回空结果，不访问数据库。
func (r *Repo) ActiveDraftAttachmentIDs(ctx context.Context, attachmentIDs []int64) ([]int64, error) {
	ids := domain.UniqueIDs(attachmentIDs)
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := r.ex(ctx).Query(ctx, sqlActiveDraftAttachmentIDs, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]int64, 0, len(ids))
	for rows.Next() {
		var attachmentID int64
		if err := rows.Scan(&attachmentID); err != nil {
			return nil, err
		}
		result = append(result, attachmentID)
	}
	return result, rows.Err()
}
