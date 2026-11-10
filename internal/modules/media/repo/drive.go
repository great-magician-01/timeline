package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/great-magician-01/timeline/internal/platform/db"
)

// DriveFolderRow 是网盘文件夹及文件统计。
type DriveFolderRow struct {
	ID          int64
	OwnerID     int64
	Name        string
	SystemKey   *string
	Status      string
	FileCount   int
	PublicCount int
	CreatedAt   time.Time
	UpdatedAt   time.Time
	DeletedAt   *time.Time
}

// DriveFileRow 是网盘条目与附件元数据的组合。
type DriveFileRow struct {
	Attachment AttachmentRow
	OwnerID    int64
	FolderID   *int64
	FolderName string
	Visibility string
	Status     string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	DeletedAt  *time.Time
}

const driveFolderColumns = `id, owner_id, name, system_key, status, created_at, updated_at, deleted_at`

func scanDriveFolder(row pgx.Row) (DriveFolderRow, error) {
	var out DriveFolderRow
	err := row.Scan(&out.ID, &out.OwnerID, &out.Name, &out.SystemKey, &out.Status,
		&out.CreatedAt, &out.UpdatedAt, &out.DeletedAt)
	return out, err
}

// GetDriveFolder 读取文件夹（含逻辑删除行），调用方负责归属和状态判断。
func GetDriveFolder(ctx context.Context, exec db.Executor, id int64) (DriveFolderRow, error) {
	return scanDriveFolder(exec.QueryRow(ctx,
		`SELECT `+driveFolderColumns+` FROM drive_folders WHERE id = $1`, id))
}

// GetDriveFolderForUpdate 锁定文件夹，供改名、删除和批量公开使用。
func GetDriveFolderForUpdate(ctx context.Context, exec db.Executor, id int64) (DriveFolderRow, error) {
	return scanDriveFolder(exec.QueryRow(ctx,
		`SELECT `+driveFolderColumns+` FROM drive_folders WHERE id = $1 FOR UPDATE`, id))
}

// GetPostFolder 按稳定 system_key 查找文章默认目录，不依赖用户改名后的名称。
func GetPostFolder(ctx context.Context, exec db.Executor, ownerID int64) (DriveFolderRow, error) {
	return scanDriveFolder(exec.QueryRow(ctx,
		`SELECT `+driveFolderColumns+` FROM drive_folders
		 WHERE owner_id = $1 AND system_key = 'post' AND deleted_at IS NULL`, ownerID))
}

// InsertDriveFolder 新建用户文件夹；重名由唯一索引兜底。
func InsertDriveFolder(ctx context.Context, exec db.Executor, row DriveFolderRow) error {
	_, err := exec.Exec(ctx, `
		INSERT INTO drive_folders (
			id, owner_id, name, system_key, status, created_at, updated_at, deleted_at
		) VALUES ($1, $2, $3, $4, 'active', $5, $5, NULL)`,
		row.ID, row.OwnerID, row.Name, row.SystemKey, row.CreatedAt)
	return err
}

// EnsurePostFolder 用单条幂等语句补建并读取文章默认目录，兼容并发上传。
func EnsurePostFolder(ctx context.Context, exec db.Executor, id, ownerID int64, name string, at time.Time) (DriveFolderRow, error) {
	return scanDriveFolder(exec.QueryRow(ctx, `
		WITH inserted AS (
			INSERT INTO drive_folders (
				id, owner_id, name, system_key, status, created_at, updated_at, deleted_at
			) VALUES ($1, $2, $3, 'post', 'active', $4, $4, NULL)
			ON CONFLICT DO NOTHING
			RETURNING id, owner_id, name, system_key, status, created_at, updated_at, deleted_at
		)
		SELECT id, owner_id, name, system_key, status, created_at, updated_at, deleted_at FROM inserted
		UNION
		SELECT id, owner_id, name, system_key, status, created_at, updated_at, deleted_at
		FROM drive_folders WHERE owner_id = $2 AND system_key = 'post' AND deleted_at IS NULL
		LIMIT 1`, id, ownerID, name, at))
}

// RenameDriveFolder 修改有效文件夹名称。
func RenameDriveFolder(ctx context.Context, exec db.Executor, id int64, name string, at time.Time) error {
	_, err := exec.Exec(ctx, `
		UPDATE drive_folders SET name = $2, updated_at = $3
		WHERE id = $1 AND deleted_at IS NULL`, id, name, at)
	return err
}

// MarkDriveFolderDeleted 逻辑删除自定义文件夹；系统目录与空目录约束由 app 校验。
func MarkDriveFolderDeleted(ctx context.Context, exec db.Executor, id int64, at time.Time) (bool, error) {
	tag, err := exec.Exec(ctx, `
		UPDATE drive_folders SET status = 'deleted', deleted_at = $2, updated_at = $2
		WHERE id = $1 AND status = 'active' AND system_key IS NULL`, id, at)
	return tag.RowsAffected() > 0, err
}

// ListDriveFolders 返回用户的有效目录及有效文件统计，文章默认目录固定在最前。
func ListDriveFolders(ctx context.Context, exec db.Executor, ownerID int64) ([]DriveFolderRow, error) {
	rows, err := exec.Query(ctx, `
		SELECT f.id, f.owner_id, f.name, f.system_key, f.status,
		       f.created_at, f.updated_at, f.deleted_at,
		       COUNT(a.id) AS file_count,
		       COUNT(a.id) FILTER (WHERE df.visibility = 'public') AS public_count
		FROM drive_folders AS f
		LEFT JOIN drive_files AS df
		  ON df.folder_id = f.id AND df.status = 'active'
		LEFT JOIN attachments AS a
		  ON a.id = df.attachment_id AND a.status = 'available'
		WHERE f.owner_id = $1 AND f.deleted_at IS NULL
		GROUP BY f.id
		ORDER BY (f.system_key = 'post') DESC, f.id DESC`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]DriveFolderRow, 0)
	for rows.Next() {
		var item DriveFolderRow
		if err := rows.Scan(&item.ID, &item.OwnerID, &item.Name, &item.SystemKey, &item.Status,
			&item.CreatedAt, &item.UpdatedAt, &item.DeletedAt, &item.FileCount, &item.PublicCount); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// PublicDriveFolderRow 是公开网盘页的目录摘要；ID 为 nil 表示根目录。
type PublicDriveFolderRow struct {
	ID        *int64
	Name      string
	SystemKey *string
	FileCount int
}

// ListPublicDriveFolders 返回有公开有效文件的目录及根目录公开文件数。
// 纯私密目录、空目录和已删除目录不会出现在结果中。
func ListPublicDriveFolders(ctx context.Context, exec db.Executor, ownerID int64) ([]PublicDriveFolderRow, error) {
	rows, err := exec.Query(ctx, `
		SELECT id, name, system_key, file_count FROM (
			SELECT f.id, f.name, f.system_key, COUNT(a.id) AS file_count
			FROM drive_folders AS f
			JOIN drive_files AS df
			  ON df.folder_id = f.id AND df.owner_id = f.owner_id
			 AND df.status = 'active' AND df.visibility = 'public'
			JOIN attachments AS a
			  ON a.id = df.attachment_id AND a.status = 'available'
			WHERE f.owner_id = $1 AND f.deleted_at IS NULL
			GROUP BY f.id
			UNION ALL
			SELECT NULL, '根目录', NULL, COUNT(a.id) AS file_count
			FROM drive_files AS df
			JOIN attachments AS a
			  ON a.id = df.attachment_id AND a.status = 'available'
			WHERE df.owner_id = $1 AND df.folder_id IS NULL
			  AND df.status = 'active' AND df.visibility = 'public'
			HAVING COUNT(a.id) > 0
		) AS folders
		ORDER BY (id IS NULL), (system_key = 'post') DESC, id DESC`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]PublicDriveFolderRow, 0)
	for rows.Next() {
		var item PublicDriveFolderRow
		if err := rows.Scan(&item.ID, &item.Name, &item.SystemKey, &item.FileCount); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// CountActiveDriveFiles 统计目录内仍存在的网盘条目（不要求附件当前可用），
// 用于“空文件夹才能删除”的判定，避免删除后留下无目录可回的条目。
func CountActiveDriveFiles(ctx context.Context, exec db.Executor, folderID int64) (int, error) {
	var count int
	err := exec.QueryRow(ctx, `
		SELECT COUNT(*) FROM drive_files
		WHERE folder_id = $1 AND status = 'active'`, folderID).Scan(&count)
	return count, err
}

// DriveFileFilter 描述本人或公开网盘列表过滤条件。
type DriveFileFilter struct {
	OwnerID    int64
	FolderID   *int64
	RootOnly   bool
	Kind       string
	Keyword    string // app 已转小写并转义 LIKE 特殊字符
	Cursor     int64
	Limit      int
	PublicOnly bool
}

const driveFileSelect = `
	SELECT df.attachment_id, df.owner_id, df.folder_id, df.visibility, df.status,
	       df.created_at, df.updated_at, df.deleted_at,
	       COALESCE((SELECT f.name FROM drive_folders AS f WHERE f.id = df.folder_id), ''),
	       a.id, a.uploader_id, a.post_id, a.kind, a.purpose, a.original_filename, a.media_type,
	       a.size_bytes, a.storage_key, a.status, a.width, a.height, a.duration_ms, a.created_at, a.updated_at
	FROM drive_files AS df
	JOIN attachments AS a ON a.id = df.attachment_id`

func scanDriveFile(row pgx.Row) (DriveFileRow, error) {
	var out DriveFileRow
	err := row.Scan(
		&out.Attachment.ID, &out.OwnerID, &out.FolderID, &out.Visibility, &out.Status,
		&out.CreatedAt, &out.UpdatedAt, &out.DeletedAt, &out.FolderName,
		&out.Attachment.ID, &out.Attachment.UploaderID, &out.Attachment.PostID,
		&out.Attachment.Kind, &out.Attachment.Purpose, &out.Attachment.OriginalFilename,
		&out.Attachment.MediaType, &out.Attachment.SizeBytes, &out.Attachment.StorageKey,
		&out.Attachment.Status, &out.Attachment.Width, &out.Attachment.Height,
		&out.Attachment.DurationMS, &out.Attachment.CreatedAt, &out.Attachment.UpdatedAt,
	)
	return out, err
}

// ListDriveFiles 按附件 ID 倒序游标分页；公开列表只含公开且可用的附件。
func ListDriveFiles(ctx context.Context, exec db.Executor, filter DriveFileFilter) ([]DriveFileRow, error) {
	args := []any{filter.OwnerID}
	conditions := []string{"df.owner_id = $1", "df.status = 'active'", "a.status = 'available'"}
	add := func(value any) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}
	if filter.PublicOnly {
		conditions = append(conditions, "df.visibility = 'public'")
	}
	if filter.RootOnly {
		conditions = append(conditions, "df.folder_id IS NULL")
	} else if filter.FolderID != nil {
		conditions = append(conditions, "df.folder_id = "+add(*filter.FolderID))
	}
	if filter.Kind != "" {
		conditions = append(conditions, "a.kind = "+add(filter.Kind))
	}
	if filter.Keyword != "" {
		conditions = append(conditions, "LOWER(a.original_filename) LIKE '%' || "+add(filter.Keyword)+" || '%' ESCAPE '\\'")
	}
	if filter.Cursor > 0 {
		conditions = append(conditions, "df.attachment_id < "+add(filter.Cursor))
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}
	query := driveFileSelect + `
		WHERE ` + strings.Join(conditions, " AND ") + `
		ORDER BY df.attachment_id DESC LIMIT ` + add(limit+1)
	rows, err := exec.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]DriveFileRow, 0, limit+1)
	for rows.Next() {
		item, err := scanDriveFile(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// GetDriveFile 读取网盘条目（含逻辑删除行），供属主与公开状态判断。
func GetDriveFile(ctx context.Context, exec db.Executor, attachmentID int64) (DriveFileRow, error) {
	return scanDriveFile(exec.QueryRow(ctx, driveFileSelect+`
		WHERE df.attachment_id = $1`, attachmentID))
}

// GetDriveFileForUpdate 锁定网盘条目，供公开切换和删除使用。
func GetDriveFileForUpdate(ctx context.Context, exec db.Executor, attachmentID int64) (DriveFileRow, error) {
	return scanDriveFile(exec.QueryRow(ctx, driveFileSelect+`
		WHERE df.attachment_id = $1 FOR UPDATE OF df`, attachmentID))
}

// DriveFilesForUpdate 按固定顺序锁定一批条目，避免文件夹批量操作之间死锁。
func DriveFilesForUpdate(ctx context.Context, exec db.Executor, ids []int64) (map[int64]DriveFileRow, error) {
	result := make(map[int64]DriveFileRow, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := exec.Query(ctx, driveFileSelect+`
		WHERE df.attachment_id = ANY($1)
		ORDER BY df.attachment_id FOR UPDATE OF df`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scanDriveFile(rows)
		if err != nil {
			return nil, err
		}
		result[item.Attachment.ID] = item
	}
	return result, rows.Err()
}

// InsertDriveFile 把已有附件加入网盘；附件与网盘行由 app 在同一事务写入。
func InsertDriveFile(ctx context.Context, exec db.Executor, row DriveFileRow) error {
	_, err := exec.Exec(ctx, `
		INSERT INTO drive_files (
			attachment_id, owner_id, folder_id, visibility, status,
			created_at, updated_at, deleted_at
		) VALUES ($1, $2, $3, 'private', 'active', $4, $4, NULL)`,
		row.Attachment.ID, row.OwnerID, row.FolderID, row.CreatedAt)
	return err
}

// MoveDriveFile 移动本人文件；公开级别不随目录变化。
func MoveDriveFile(ctx context.Context, exec db.Executor, attachmentID, ownerID int64, folderID *int64, at time.Time) error {
	_, err := exec.Exec(ctx, `
		UPDATE drive_files SET folder_id = $3, updated_at = $4
		WHERE attachment_id = $1 AND owner_id = $2 AND status = 'active'`,
		attachmentID, ownerID, folderID, at)
	return err
}

// SetDriveFileVisibility 修改单文件公开级别。
func SetDriveFileVisibility(ctx context.Context, exec db.Executor, attachmentID int64, visibility string, at time.Time) error {
	_, err := exec.Exec(ctx, `
		UPDATE drive_files SET visibility = $2, updated_at = $3
		WHERE attachment_id = $1 AND status = 'active'`, attachmentID, visibility, at)
	return err
}

// SetDriveFolderVisibility 批量修改目录内全部有效条目的公开级别。
func SetDriveFolderVisibility(ctx context.Context, exec db.Executor, folderID, ownerID int64, visibility string, at time.Time) error {
	_, err := exec.Exec(ctx, `
		UPDATE drive_files SET visibility = $3, updated_at = $4
		WHERE folder_id = $1 AND owner_id = $2 AND status = 'active'`,
		folderID, ownerID, visibility, at)
	return err
}

// RevokePublicDriveFiles 在文章权限下调或删除时撤销公开，不在后续公开时自动恢复。
func RevokePublicDriveFiles(ctx context.Context, exec db.Executor, ids []int64, at time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := exec.Exec(ctx, `
		UPDATE drive_files SET visibility = 'private', updated_at = $2
		WHERE attachment_id = ANY($1) AND status = 'active' AND visibility = 'public'`, ids, at)
	return err
}

// ActiveDriveFileIDsByFolder 返回目录内仍有效的文件 ID，固定排序供批量锁定。
func ActiveDriveFileIDsByFolder(ctx context.Context, exec db.Executor, folderID int64) ([]int64, error) {
	rows, err := exec.Query(ctx, `
		SELECT df.attachment_id FROM drive_files AS df
		JOIN attachments AS a ON a.id = df.attachment_id AND a.status = 'available'
		WHERE df.folder_id = $1 AND df.status = 'active'
		ORDER BY df.attachment_id`, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// MarkDriveFileDeleted 只删除网盘成员关系，不解除任何文章或草稿引用。
func MarkDriveFileDeleted(ctx context.Context, exec db.Executor, attachmentID, ownerID int64, at time.Time) (bool, error) {
	tag, err := exec.Exec(ctx, `
		UPDATE drive_files SET status = 'deleted', deleted_at = $3, updated_at = $3
		WHERE attachment_id = $1 AND owner_id = $2 AND status = 'active'`,
		attachmentID, ownerID, at)
	return tag.RowsAffected() > 0, err
}

// IsUniqueViolation 供 app 把目录重名映射为冲突错误，避免 app 依赖 pgx。
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
