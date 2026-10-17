package repo

import (
	"context"
	"time"

	"github.com/great-magician-01/timeline/internal/shared/model"
)

// VersionRow 是历史版本列表行（不含正文字段，摘要按纯文本截取）。
type VersionRow struct {
	ID                    int64
	VersionNumber         int
	Visibility            string
	CreatedAt             time.Time
	RestoredFromVersionID *int64
	PlainText             string
}

// InsertVersion 追加一个完整历史版本快照；created_at 由调用方显式传入。
func (r *Repo) InsertVersion(ctx context.Context, version model.PostVersion) error {
	_, err := r.ex(ctx).Exec(ctx, `
		INSERT INTO post_versions (
			id, post_id, version_number, content_format, content_source, content_html,
			plain_text, visibility, restored_from_version_id, created_by, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		version.ID, version.PostID, version.VersionNumber, version.ContentFormat,
		version.ContentSource, version.ContentHTML, version.PlainText, version.Visibility,
		version.RestoredFromVersionID, version.CreatedBy, version.CreatedAt)
	return err
}

// InsertVersionAttachments 写入版本附件快照与排序。
func (r *Repo) InsertVersionAttachments(ctx context.Context, versionID int64, attachmentIDs []int64) error {
	exec := r.ex(ctx)
	for order, attachmentID := range attachmentIDs {
		if _, err := exec.Exec(ctx, `
			INSERT INTO post_version_attachments (version_id, attachment_id, sort_order)
			VALUES ($1, $2, $3)
			ON CONFLICT (version_id, attachment_id) DO NOTHING`,
			versionID, attachmentID, order); err != nil {
			return err
		}
	}
	return nil
}

// MaxVersionNumber 读取帖子当前最大版本号（没有版本时返回 0）。
func (r *Repo) MaxVersionNumber(ctx context.Context, postID int64) (int, error) {
	var maxVersion int
	err := r.ex(ctx).QueryRow(ctx,
		`SELECT COALESCE(MAX(version_number), 0) FROM post_versions WHERE post_id = $1`,
		postID).Scan(&maxVersion)
	return maxVersion, err
}

// GetVersion 读取指定帖子的历史版本快照。
func (r *Repo) GetVersion(ctx context.Context, postID, versionID int64) (model.PostVersion, error) {
	version, err := scanVersion(r.ex(ctx).QueryRow(ctx,
		`SELECT `+versionColumns+` FROM post_versions WHERE id = $1 AND post_id = $2`,
		versionID, postID))
	return version, notFoundIfNoRows(err, "历史版本不存在")
}

// ListVersions 读取历史版本列表，按版本 ID 倒序游标分页（雪花 ID 单调递增）。
func (r *Repo) ListVersions(ctx context.Context, postID, cursorID int64, limit int) ([]VersionRow, bool, error) {
	sql := `SELECT id, version_number, visibility, created_at, restored_from_version_id, plain_text
		FROM post_versions WHERE post_id = $1`
	args := []any{postID}
	if cursorID > 0 {
		args = append(args, cursorID)
		sql += " AND id < $" + itoa(len(args))
	}
	args = append(args, limit+1)
	sql += " ORDER BY id DESC LIMIT $" + itoa(len(args))

	rows, err := r.ex(ctx).Query(ctx, sql, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	items := make([]VersionRow, 0, limit+1)
	for rows.Next() {
		var row VersionRow
		if err := rows.Scan(&row.ID, &row.VersionNumber, &row.Visibility, &row.CreatedAt,
			&row.RestoredFromVersionID, &row.PlainText); err != nil {
			return nil, false, err
		}
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

// CurrentVersions 批量读取每个帖子的当前发布版本。
func (r *Repo) CurrentVersions(ctx context.Context, postIDs []int64) (map[int64]model.PostVersion, error) {
	result := make(map[int64]model.PostVersion, len(postIDs))
	if len(postIDs) == 0 {
		return result, nil
	}
	rows, err := r.ex(ctx).Query(ctx, `
		SELECT v.id, v.post_id, v.version_number, v.content_format, v.content_source,
		       v.content_html, v.plain_text, v.visibility, v.restored_from_version_id,
		       v.created_by, v.created_at
		FROM post_versions v JOIN posts p ON p.current_version_id = v.id
		WHERE p.id = ANY($1)`, postIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		version, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		result[version.PostID] = version
	}
	return result, rows.Err()
}

// VersionAttachmentIDsMany 批量读取版本快照引用的附件 ID（按原排序）。
func (r *Repo) VersionAttachmentIDsMany(ctx context.Context, versionIDs []int64) (map[int64][]int64, error) {
	result := make(map[int64][]int64, len(versionIDs))
	if len(versionIDs) == 0 {
		return result, nil
	}
	rows, err := r.ex(ctx).Query(ctx, `
		SELECT version_id, attachment_id FROM post_version_attachments
		WHERE version_id = ANY($1)
		ORDER BY version_id, sort_order, attachment_id`, versionIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var versionID, attachmentID int64
		if err := rows.Scan(&versionID, &attachmentID); err != nil {
			return nil, err
		}
		result[versionID] = append(result[versionID], attachmentID)
	}
	return result, rows.Err()
}

// AttachmentReferencedByVersion 判断附件是否仍被任何历史版本引用。
func (r *Repo) AttachmentReferencedByVersion(ctx context.Context, attachmentID int64) (bool, error) {
	var exists bool
	err := r.ex(ctx).QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM post_version_attachments WHERE attachment_id = $1
		)`, attachmentID).Scan(&exists)
	return exists, err
}
