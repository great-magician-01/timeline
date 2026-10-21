package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/modules/social/domain"
	"github.com/great-magician-01/timeline/internal/platform/db"
)

// BookmarkRepo 实现 app.BookmarkRepo 的收藏部分。
type BookmarkRepo struct {
	pool *pgxpool.Pool
}

// NewBookmarkRepo 构造收藏仓储。
func NewBookmarkRepo(pool *pgxpool.Pool) *BookmarkRepo { return &BookmarkRepo{pool: pool} }

const (
	// 收藏无计数与通知，upsert 不需要 changed 判定：无条件覆盖即同时支持
	// 「新收藏 / 失效复活 / 移动到指定收藏夹」。folder_id 为 NULL 表示默认收藏。
	// 重新收藏已生效条目只更新 folder_id 与 updated_at，不刷新 created_at 与行 ID
	// （收藏列表按行 ID 倒序，位置保持首次收藏时不变）。
	sqlBookmarkUpsert = `INSERT INTO bookmarks (id, user_id, post_id, folder_id, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, TRUE, $5, $5)
		ON CONFLICT (user_id, post_id)
		DO UPDATE SET is_active = TRUE, folder_id = $4, updated_at = $5
		RETURNING id`

	// 取消收藏只做逻辑失效，不清 folder_id（重新收藏时由 upsert 指定归属）。
	sqlBookmarkDeactivate = `UPDATE bookmarks SET is_active = FALSE, updated_at = $3
		WHERE user_id = $1 AND post_id = $2 AND is_active
		RETURNING id`

	sqlBookmarkActiveStates = `SELECT post_id FROM bookmarks
		WHERE user_id = $1 AND post_id = ANY($2) AND is_active`

	sqlBookmarkListFirstPage = `SELECT id, post_id, folder_id, created_at FROM bookmarks
		WHERE user_id = $1 AND is_active%s
		ORDER BY id DESC LIMIT $%d`
	sqlBookmarkListAfterCursor = `SELECT id, post_id, folder_id, created_at FROM bookmarks
		WHERE user_id = $1 AND is_active%s AND id < $%d
		ORDER BY id DESC LIMIT $%d`
)

// UpsertActive 收藏或更新归属；返回 bookmark 行 ID（幂等）。
func (r *BookmarkRepo) UpsertActive(
	ctx context.Context, id, userID, postID int64, folderID *int64, now time.Time,
) (int64, error) {
	var rowID int64
	err := db.ExecutorFrom(ctx, r.pool).
		QueryRow(ctx, sqlBookmarkUpsert, id, userID, postID, folderID, now).
		Scan(&rowID)
	return rowID, err
}

// Deactivate 取消收藏；ok 表示是否存在生效收藏被翻转（幂等）。
func (r *BookmarkRepo) Deactivate(ctx context.Context, userID, postID int64, now time.Time) (bool, error) {
	var rowID int64
	err := db.ExecutorFrom(ctx, r.pool).
		QueryRow(ctx, sqlBookmarkDeactivate, userID, postID, now).
		Scan(&rowID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// ActiveStates 批量查询用户已收藏的帖子集合。
func (r *BookmarkRepo) ActiveStates(ctx context.Context, userID int64, postIDs []int64) (map[int64]bool, error) {
	result := map[int64]bool{}
	if userID <= 0 || len(postIDs) == 0 {
		return result, nil
	}
	rows, err := db.ExecutorFrom(ctx, r.pool).Query(ctx, sqlBookmarkActiveStates, userID, postIDs)
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

// ListBatch 读取一批生效收藏（行 ID 倒序），夹过滤与游标语义见 domain.BookmarkListQuery。
func (r *BookmarkRepo) ListBatch(ctx context.Context, query domain.BookmarkListQuery) ([]domain.BookmarkRow, error) {
	sql, args := buildBookmarkListSQL(query)
	rows, err := db.ExecutorFrom(ctx, r.pool).Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]domain.BookmarkRow, 0, query.Limit)
	for rows.Next() {
		var row domain.BookmarkRow
		if err := rows.Scan(&row.ID, &row.PostID, &row.FolderID, &row.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, rows.Err()
}

// buildBookmarkListSQL 是纯 SQL 组装函数（不访问数据库，便于单测断言三态与参数顺序）。
// All=true 不过滤夹；否则 DefaultOnly=true 追加 folder_id IS NULL；否则按 FolderID 精确过滤。
func buildBookmarkListSQL(query domain.BookmarkListQuery) (string, []any) {
	args := make([]any, 0, 4)
	// SQL 常量的 WHERE 以 user_id = $1 开头，参数序从 userID 开始。
	args = append(args, query.UserID)
	conditions := ""
	switch {
	case query.All:
		// 不过滤夹。
	case query.DefaultOnly:
		conditions += " AND folder_id IS NULL"
	default:
		args = append(args, query.FolderID)
		conditions += fmt.Sprintf(" AND folder_id = $%d", len(args))
	}
	if query.AfterID > 0 {
		args = append(args, query.AfterID)
		atPos := len(args)
		args = append(args, query.Limit)
		sql := fmt.Sprintf(sqlBookmarkListAfterCursor, conditions, atPos, len(args))
		return sql, args
	}
	args = append(args, query.Limit)
	sql := fmt.Sprintf(sqlBookmarkListFirstPage, conditions, len(args))
	return sql, args
}
