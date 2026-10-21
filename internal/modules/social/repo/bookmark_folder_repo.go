package repo

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/modules/social/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/db"
)

// BookmarkFolderRepo 实现 app.BookmarkRepo 的收藏夹部分。
type BookmarkFolderRepo struct {
	pool *pgxpool.Pool
}

// NewBookmarkFolderRepo 构造收藏夹仓储。
func NewBookmarkFolderRepo(pool *pgxpool.Pool) *BookmarkFolderRepo {
	return &BookmarkFolderRepo{pool: pool}
}

const (
	sqlFolderInsert = `INSERT INTO bookmark_folders (id, user_id, name, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $4)
		RETURNING id, user_id, name, created_at, updated_at`

	// 事务内锁定收藏夹行并读取当前归属，供应用做属主校验（非本人按 404，不暴露存在性）。
	sqlFolderGetForUpdate = `SELECT id, user_id, name, created_at, updated_at FROM bookmark_folders
		WHERE id = $1 AND deleted_at IS NULL
		FOR UPDATE`

	// 只读版本：收藏列表的夹过滤参数校验（是否存在且属于该用户）。
	sqlFolderGetActive = `SELECT id, user_id FROM bookmark_folders
		WHERE id = $1 AND deleted_at IS NULL`

	sqlFolderRename = `UPDATE bookmark_folders SET name = $2, updated_at = $3
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING id, user_id, name, created_at, updated_at`

	// 逻辑删除收藏夹；归属的生效收藏由应用在同一事务内调用 ReassignToDefault 归还默认收藏。
	sqlFolderDelete = `UPDATE bookmark_folders SET deleted_at = $3, updated_at = $3
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
		RETURNING id`

	sqlFolderReassignToDefault = `UPDATE bookmarks SET folder_id = NULL, updated_at = $3
		WHERE user_id = $1 AND folder_id = $2 AND is_active`

	sqlFolderCountActive = `SELECT COUNT(*) FROM bookmark_folders
		WHERE user_id = $1 AND deleted_at IS NULL`

	sqlFolderListWithCounts = `SELECT f.id, f.name, f.created_at, f.updated_at,
			COUNT(b.id) FILTER (WHERE b.is_active) AS bookmark_count
		FROM bookmark_folders f
		LEFT JOIN bookmarks b ON b.folder_id = f.id
		WHERE f.user_id = $1 AND f.deleted_at IS NULL
		GROUP BY f.id
		ORDER BY f.id DESC`
)

// CreateFolder 创建收藏夹；返回完整行。
func (r *BookmarkFolderRepo) CreateFolder(
	ctx context.Context, id, userID int64, name string, now time.Time,
) (domain.BookmarkFolderRow, error) {
	var row domain.BookmarkFolderRow
	err := db.ExecutorFrom(ctx, r.pool).
		QueryRow(ctx, sqlFolderInsert, id, userID, name, now).
		Scan(&row.ID, &row.UserID, &row.Name, &row.CreatedAt, &row.UpdatedAt)
	return row, err
}

// GetActiveFolderForUpdate 事务内锁定并读取生效收藏夹；不存在（或已删除）返回 apperr.NotFound。
func (r *BookmarkFolderRepo) GetActiveFolderForUpdate(ctx context.Context, folderID int64) (domain.BookmarkFolderRow, error) {
	var row domain.BookmarkFolderRow
	err := db.ExecutorFrom(ctx, r.pool).
		QueryRow(ctx, sqlFolderGetForUpdate, folderID).
		Scan(&row.ID, &row.UserID, &row.Name, &row.CreatedAt, &row.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.BookmarkFolderRow{}, apperr.NotFound("收藏夹不存在")
	}
	return row, err
}

// GetActiveFolder 只读读取生效收藏夹（不锁行）；不存在（或已删除）返回 apperr.NotFound。
// 供收藏列表的夹过滤参数校验（是否存在且属于该用户）。
func (r *BookmarkFolderRepo) GetActiveFolder(ctx context.Context, folderID int64) (domain.BookmarkFolderRow, error) {
	var row domain.BookmarkFolderRow
	err := db.ExecutorFrom(ctx, r.pool).
		QueryRow(ctx, sqlFolderGetActive, folderID).
		Scan(&row.ID, &row.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.BookmarkFolderRow{}, apperr.NotFound("收藏夹不存在")
	}
	return row, err
}

// RenameFolder 重命名生效收藏夹；返回更新后的行。
func (r *BookmarkFolderRepo) RenameFolder(ctx context.Context, folderID int64, name string, now time.Time) (domain.BookmarkFolderRow, error) {
	var row domain.BookmarkFolderRow
	err := db.ExecutorFrom(ctx, r.pool).
		QueryRow(ctx, sqlFolderRename, folderID, name, now).
		Scan(&row.ID, &row.UserID, &row.Name, &row.CreatedAt, &row.UpdatedAt)
	return row, err
}

// DeleteFolder 逻辑删除收藏夹；changed 表示是否存在生效夹被删除（幂等）。
func (r *BookmarkFolderRepo) DeleteFolder(ctx context.Context, folderID, userID int64, now time.Time) (bool, error) {
	var rowID int64
	err := db.ExecutorFrom(ctx, r.pool).
		QueryRow(ctx, sqlFolderDelete, folderID, userID, now).
		Scan(&rowID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// ReassignToDefault 把某收藏夹下的生效收藏归还默认收藏（folder_id 置 NULL）。
func (r *BookmarkFolderRepo) ReassignToDefault(ctx context.Context, userID, folderID int64, now time.Time) error {
	_, err := db.ExecutorFrom(ctx, r.pool).
		Exec(ctx, sqlFolderReassignToDefault, userID, folderID, now)
	return err
}

// CountActiveFolders 统计用户当前生效收藏夹数量（上限校验用）。
func (r *BookmarkFolderRepo) CountActiveFolders(ctx context.Context, userID int64) (int, error) {
	var count int
	err := db.ExecutorFrom(ctx, r.pool).
		QueryRow(ctx, sqlFolderCountActive, userID).Scan(&count)
	return count, err
}

// ListFoldersWithCounts 读取用户的生效收藏夹与各自的生效收藏数（新建在前）。
func (r *BookmarkFolderRepo) ListFoldersWithCounts(ctx context.Context, userID int64) ([]domain.BookmarkFolderRow, error) {
	rows, err := db.ExecutorFrom(ctx, r.pool).Query(ctx, sqlFolderListWithCounts, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	folders := make([]domain.BookmarkFolderRow, 0, 8)
	for rows.Next() {
		var row domain.BookmarkFolderRow
		if err := rows.Scan(&row.ID, &row.Name, &row.CreatedAt, &row.UpdatedAt, &row.BookmarkCount); err != nil {
			return nil, err
		}
		folders = append(folders, row)
	}
	return folders, rows.Err()
}
