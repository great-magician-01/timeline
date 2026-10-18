package repo

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/platform/db"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// CommentRepo 实现 app.CommentRepo（comments 表归 social 所有）。
type CommentRepo struct {
	pool *pgxpool.Pool
}

// NewCommentRepo 构造评论仓储。
func NewCommentRepo(pool *pgxpool.Pool) *CommentRepo { return &CommentRepo{pool: pool} }

// commentColumns 是 comments 行的查询列清单（列序与 scanComment 一致）。
const commentColumns = `id, post_id, author_id, parent_comment_id, root_comment_id,
	content_format, content_source, content_html, plain_text, status,
	deleted_by_admin, deleted_by_author, like_count, reply_count, created_at, updated_at`

const (
	sqlCommentGet = `SELECT ` + commentColumns + ` FROM comments WHERE id = $1`

	sqlCommentGetForUpdate = `SELECT ` + commentColumns + ` FROM comments WHERE id = $1 FOR UPDATE`

	sqlCommentInsert = `INSERT INTO comments (
			id, post_id, author_id, parent_comment_id, root_comment_id,
			content_format, content_source, content_html, plain_text, status,
			like_count, reply_count, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 0, 0, $11, $11)
		RETURNING ` + commentColumns

	sqlCommentListTopFirstPage = `SELECT ` + commentColumns + `
		FROM comments WHERE post_id = $1 AND parent_comment_id IS NULL
		ORDER BY id ASC LIMIT $2`
	sqlCommentListTopAfterCursor = `SELECT ` + commentColumns + `
		FROM comments WHERE post_id = $1 AND parent_comment_id IS NULL AND id > $2
		ORDER BY id ASC LIMIT $3`

	sqlCommentListRepliesFirstPage = `SELECT ` + commentColumns + `
		FROM comments WHERE root_comment_id = $1 AND parent_comment_id IS NOT NULL
		ORDER BY id ASC LIMIT $2`
	sqlCommentListRepliesAfterCursor = `SELECT ` + commentColumns + `
		FROM comments WHERE root_comment_id = $1 AND parent_comment_id IS NOT NULL AND id > $2
		ORDER BY id ASC LIMIT $3`

	// 每个顶层评论取前 N 条回复：LATERAL + LIMIT 只做分页裁剪，不做业务运算。
	sqlCommentReplyPreviews = `SELECT c.id, c.post_id, c.author_id, c.parent_comment_id, c.root_comment_id,
			c.content_format, c.content_source, c.content_html, c.plain_text, c.status,
			c.deleted_by_admin, c.deleted_by_author, c.like_count, c.reply_count, c.created_at, c.updated_at
		FROM unnest($1::bigint[]) AS roots(root_id)
		JOIN LATERAL (
			SELECT * FROM comments
			WHERE root_comment_id = roots.root_id AND parent_comment_id IS NOT NULL
			ORDER BY id ASC
			LIMIT $2
		) AS c ON TRUE
		ORDER BY roots.root_id, c.id`

	sqlCommentAdjustReplyCount = `UPDATE comments SET reply_count = reply_count + $2, updated_at = $3
		WHERE id = $1 RETURNING reply_count`
	sqlCommentAdjustLikeCount = `UPDATE comments SET like_count = like_count + $2, updated_at = $3
		WHERE id = $1 RETURNING like_count`

	sqlCommentMarkDeleted = `UPDATE comments
		SET status = 'deleted', deleted_by_admin = $2, deleted_by_author = $3,
		    deleted_by_post_author = $4, updated_at = $5
		WHERE id = $1 AND status = 'published'`

	sqlCommentParentID = `SELECT parent_comment_id FROM comments WHERE id = $1`

	sqlCommentByIDs = `SELECT ` + commentColumns + ` FROM comments WHERE id = ANY($1)`

	// 公开帖子最近讨论：只取已发布评论，按 ID 倒序（由 app 反转成时间正序）。
	sqlCommentRecentByPost = `SELECT ` + commentColumns + `
		FROM comments WHERE post_id = $1 AND status = 'published'
		ORDER BY id DESC LIMIT $2`

	// 某作者的回复（个人主页 replies 页签）：按 (created_at, id) 倒序游标分页。
	sqlCommentByAuthorFirstPage = `SELECT ` + commentColumns + `
		FROM comments WHERE author_id = $1 AND status = 'published'
		ORDER BY created_at DESC, id DESC LIMIT $2`
	sqlCommentByAuthorAfterCursor = `SELECT ` + commentColumns + `
		FROM comments WHERE author_id = $1 AND status = 'published'
		  AND (created_at, id) < ($2, $3)
		ORDER BY created_at DESC, id DESC LIMIT $4`

	// 一组帖子在 since 之后的新评论（AI 被动回复触发兜底扫描）。
	// author_id <> $4 排除作者自己的评论：AI 回复用户后，自己的评论不再触发自回复任务。
	sqlCommentByPostIDsSince = `SELECT ` + commentColumns + `
		FROM comments
		WHERE post_id = ANY($1) AND status = 'published' AND created_at > $2
		  AND author_id <> $4
		ORDER BY id DESC LIMIT $3`

	sqlCommentByPostIDsSinceAllAuthors = `SELECT ` + commentColumns + `
		FROM comments
		WHERE post_id = ANY($1) AND status = 'published' AND created_at > $2
		ORDER BY id DESC LIMIT $3`
)

// RecentByPost 读取帖子最近讨论（已发布评论，按 ID 倒序）。
func (r *CommentRepo) RecentByPost(ctx context.Context, postID int64, limit int) ([]model.Comment, error) {
	rows, err := db.ExecutorFrom(ctx, r.pool).Query(ctx, sqlCommentRecentByPost, postID, limit)
	if err != nil {
		return nil, err
	}
	return scanCommentRows(rows, limit)
}

// ListByAuthor 按作者分页读取回复评论（(created_at, id) 倒序）。
func (r *CommentRepo) ListByAuthor(
	ctx context.Context, authorID int64, afterAt time.Time, afterID int64, limit int,
) ([]model.Comment, error) {
	exec := db.ExecutorFrom(ctx, r.pool)
	var (
		rows pgx.Rows
		err  error
	)
	if afterID > 0 && !afterAt.IsZero() {
		rows, err = exec.Query(ctx, sqlCommentByAuthorAfterCursor, authorID, afterAt, afterID, limit)
	} else {
		rows, err = exec.Query(ctx, sqlCommentByAuthorFirstPage, authorID, limit)
	}
	if err != nil {
		return nil, err
	}
	return scanCommentRows(rows, limit)
}

// ListByPostIDsSince 读取一组帖子在 since 之后的新评论（按 ID 倒序）。
// excludeAuthorID > 0 时排除该作者自己的评论（旧实现 `author_id <> $1`）。
func (r *CommentRepo) ListByPostIDsSince(
	ctx context.Context, postIDs []int64, excludeAuthorID int64, since time.Time, limit int,
) ([]model.Comment, error) {
	if len(postIDs) == 0 {
		return nil, nil
	}
	exec := db.ExecutorFrom(ctx, r.pool)
	var (
		rows pgx.Rows
		err  error
	)
	if excludeAuthorID > 0 {
		rows, err = exec.Query(ctx, sqlCommentByPostIDsSince, postIDs, since, limit, excludeAuthorID)
	} else {
		rows, err = exec.Query(ctx, sqlCommentByPostIDsSinceAllAuthors, postIDs, since, limit)
	}
	if err != nil {
		return nil, err
	}
	return scanCommentRows(rows, limit)
}

// scanCommentRows 读取并关闭行集。
func scanCommentRows(rows pgx.Rows, limit int) ([]model.Comment, error) {
	defer rows.Close()
	items := make([]model.Comment, 0, limit)
	for rows.Next() {
		comment, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, comment)
	}
	return items, rows.Err()
}

// ByIDs 批量读取评论（含已删除），缺失 ID 不在结果中。
func (r *CommentRepo) ByIDs(ctx context.Context, ids []int64) (map[int64]model.Comment, error) {
	result := map[int64]model.Comment{}
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := db.ExecutorFrom(ctx, r.pool).Query(ctx, sqlCommentByIDs, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		comment, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		result[comment.ID] = comment
	}
	return result, rows.Err()
}

// Get 读取评论；不存在返回 apperr.NotFound。
func (r *CommentRepo) Get(ctx context.Context, id int64) (model.Comment, error) {
	row := db.ExecutorFrom(ctx, r.pool).QueryRow(ctx, sqlCommentGet, id)
	comment, err := scanComment(row)
	return comment, notFound(err, "评论不存在或无权访问")
}

// GetForUpdate 在事务内加锁读取评论；不存在返回 apperr.NotFound。
func (r *CommentRepo) GetForUpdate(ctx context.Context, id int64) (model.Comment, error) {
	row := db.ExecutorFrom(ctx, r.pool).QueryRow(ctx, sqlCommentGetForUpdate, id)
	comment, err := scanComment(row)
	return comment, notFound(err, "评论不存在或无权访问")
}

// Insert 写入评论并返回数据库中的完整行。
func (r *CommentRepo) Insert(ctx context.Context, comment model.Comment) (model.Comment, error) {
	row := db.ExecutorFrom(ctx, r.pool).QueryRow(ctx, sqlCommentInsert,
		comment.ID, comment.PostID, comment.AuthorID, comment.ParentCommentID, comment.RootCommentID,
		comment.ContentFormat, comment.ContentSource, comment.ContentHTML, comment.PlainText,
		comment.Status, comment.CreatedAt,
	)
	return scanComment(row)
}

// ListTopLevel 按 ID 升序分页顶层评论。
func (r *CommentRepo) ListTopLevel(ctx context.Context, postID, cursor int64, limit int) ([]model.Comment, error) {
	return r.list(ctx, sqlCommentListTopFirstPage, sqlCommentListTopAfterCursor, postID, cursor, limit)
}

// ListReplies 按 ID 升序分页某顶层评论下的回复。
func (r *CommentRepo) ListReplies(ctx context.Context, rootCommentID, cursor int64, limit int) ([]model.Comment, error) {
	return r.list(ctx, sqlCommentListRepliesFirstPage, sqlCommentListRepliesAfterCursor, rootCommentID, cursor, limit)
}

// ReplyPreviews 返回每个顶层评论的前 perRoot 条回复（按 ID 升序）。
func (r *CommentRepo) ReplyPreviews(ctx context.Context, rootIDs []int64, perRoot int) (map[int64][]model.Comment, error) {
	result := map[int64][]model.Comment{}
	if len(rootIDs) == 0 || perRoot <= 0 {
		return result, nil
	}
	rows, err := db.ExecutorFrom(ctx, r.pool).Query(ctx, sqlCommentReplyPreviews, rootIDs, perRoot)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		comment, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		if comment.RootCommentID == nil {
			continue
		}
		result[*comment.RootCommentID] = append(result[*comment.RootCommentID], comment)
	}
	return result, rows.Err()
}

// AdjustReplyCount 原子调整回复数并返回新值。
func (r *CommentRepo) AdjustReplyCount(ctx context.Context, commentID int64, delta int, now time.Time) (int, error) {
	var count int
	err := db.ExecutorFrom(ctx, r.pool).
		QueryRow(ctx, sqlCommentAdjustReplyCount, commentID, delta, now).
		Scan(&count)
	return count, notFound(err, "评论不存在")
}

// AdjustLikeCount 原子调整点赞数并返回新值。
func (r *CommentRepo) AdjustLikeCount(ctx context.Context, commentID int64, delta int, now time.Time) (int, error) {
	var count int
	err := db.ExecutorFrom(ctx, r.pool).
		QueryRow(ctx, sqlCommentAdjustLikeCount, commentID, delta, now).
		Scan(&count)
	return count, notFound(err, "评论不存在")
}

// MarkDeleted 逻辑删除评论并记录处置角色；已删除时 changed 为 false。
func (r *CommentRepo) MarkDeleted(
	ctx context.Context, id int64, byAdmin, byAuthor, byPostAuthor bool, now time.Time,
) (bool, error) {
	tag, err := db.ExecutorFrom(ctx, r.pool).
		Exec(ctx, sqlCommentMarkDeleted, id, byAdmin, byAuthor, byPostAuthor, now)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ParentID 读取评论的父评论 ID（顶层返回 nil）。
func (r *CommentRepo) ParentID(ctx context.Context, commentID int64) (*int64, error) {
	var parent *int64
	err := db.ExecutorFrom(ctx, r.pool).QueryRow(ctx, sqlCommentParentID, commentID).Scan(&parent)
	return parent, notFound(err, "评论不存在")
}

func (r *CommentRepo) list(
	ctx context.Context,
	firstPageSQL, afterCursorSQL string,
	ownerID, cursor int64,
	limit int,
) ([]model.Comment, error) {
	exec := db.ExecutorFrom(ctx, r.pool)
	var (
		rows pgx.Rows
		err  error
	)
	if cursor > 0 {
		rows, err = exec.Query(ctx, afterCursorSQL, ownerID, cursor, limit)
	} else {
		rows, err = exec.Query(ctx, firstPageSQL, ownerID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]model.Comment, 0, limit)
	for rows.Next() {
		comment, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, comment)
	}
	return items, rows.Err()
}

// scanner 是 pgx.Row 与 pgx.Rows 的共同能力。
type scanner interface {
	Scan(dest ...any) error
}

// scanComment 按 commentColumns 的列序扫描评论行。
func scanComment(row scanner) (model.Comment, error) {
	var comment model.Comment
	err := row.Scan(
		&comment.ID, &comment.PostID, &comment.AuthorID, &comment.ParentCommentID, &comment.RootCommentID,
		&comment.ContentFormat, &comment.ContentSource, &comment.ContentHTML, &comment.PlainText,
		&comment.Status, &comment.DeletedByAdmin, &comment.DeletedByAuthor,
		&comment.LikeCount, &comment.ReplyCount, &comment.CreatedAt, &comment.UpdatedAt,
	)
	return comment, err
}
