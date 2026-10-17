// Package repo 是 media 模块唯一出现 SQL 的层：attachments 与 attachment_derivatives
// 的读写。所有时间值都是应用时钟计算的显式参数，SQL 中禁止 now()/CURRENT_DATE/interval。
package repo

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/great-magician-01/timeline/internal/platform/db"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// AttachmentRow 是 attachments 行 + purpose。
// purpose 是 media 内部字段，不进入 shared/model.Attachment（跨模块 DTO 不需要它），
// 但离开发帖与头像流程都要用它做用途校验。
type AttachmentRow struct {
	model.Attachment
	Purpose string
}

const attachmentColumns = `id, uploader_id, post_id, kind, purpose, original_filename, media_type,
	size_bytes, storage_key, status, width, height, duration_ms, created_at, updated_at`

// Get 按 ID 读取附件行；不存在返回 pgx.ErrNoRows（由 app 转成领域 NotFound）。
// 已逻辑删除的记录也会返回，调用方按 Available 判断可用性。
func Get(ctx context.Context, exec db.Executor, id int64) (AttachmentRow, error) {
	return scanAttachment(exec.QueryRow(ctx,
		`SELECT `+attachmentColumns+` FROM attachments WHERE id = $1`, id))
}

// ListByIDs 批量读取附件行，返回 id → 行；缺失 ID 不在 map 中。
func ListByIDs(ctx context.Context, exec db.Executor, ids []int64) (map[int64]AttachmentRow, error) {
	result := make(map[int64]AttachmentRow, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := exec.Query(ctx,
		`SELECT `+attachmentColumns+` FROM attachments WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		row, err := scanAttachment(rows)
		if err != nil {
			return nil, err
		}
		result[row.ID] = row
	}
	return result, rows.Err()
}

// InsertInput 描述一条新附件记录。
type InsertInput struct {
	ID               int64
	UploaderID       int64
	Kind             string
	Purpose          string
	OriginalFilename string
	MediaType        string
	SizeBytes        int64
	StorageKey       string
	Status           string
	Width            *int
	Height           *int
	DurationMS       *int64
	At               time.Time
}

// Insert 写入一条附件记录；created_at/updated_at 使用应用时间 At。
func Insert(ctx context.Context, exec db.Executor, in InsertInput) error {
	_, err := exec.Exec(ctx, `
		INSERT INTO attachments (
			id, uploader_id, post_id, kind, purpose, original_filename, media_type,
			size_bytes, storage_key, status, width, height, duration_ms, created_at, updated_at
		) VALUES ($1, $2, NULL, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $13)`,
		in.ID, in.UploaderID, in.Kind, in.Purpose, in.OriginalFilename, in.MediaType,
		in.SizeBytes, in.StorageKey, in.Status, in.Width, in.Height, in.DurationMS, in.At,
	)
	return err
}

// SumSizeByUploader 统计上传者现有附件总大小（含已逻辑删除记录，物理文件仍占磁盘）。
func SumSizeByUploader(ctx context.Context, exec db.Executor, uploaderID int64) (int64, error) {
	var used int64
	err := exec.QueryRow(ctx,
		`SELECT COALESCE(SUM(size_bytes), 0) FROM attachments WHERE uploader_id = $1`,
		uploaderID).Scan(&used)
	return used, err
}

// MarkDeleted 把附件置为逻辑失效，返回是否发生变更（已失效时返回 false）。
func MarkDeleted(ctx context.Context, exec db.Executor, id int64, at time.Time) (bool, error) {
	tag, err := exec.Exec(ctx, `
		UPDATE attachments SET status = 'deleted', updated_at = $2
		WHERE id = $1 AND status <> 'deleted'`, id, at)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// LinkToPost 把一批附件绑定到帖子（post_id 由 content 模块通过 contracts 端口请求）。
func LinkToPost(ctx context.Context, exec db.Executor, postID int64, attachmentIDs []int64, at time.Time) error {
	if len(attachmentIDs) == 0 {
		return nil
	}
	_, err := exec.Exec(ctx, `
		UPDATE attachments SET post_id = $1, updated_at = $3
		WHERE id = ANY($2)`, postID, attachmentIDs, at)
	return err
}

// MarkExpiredTempDeleted 把超过保留期且未被引用的临时帖子附件标记失效，返回受影响行数。
// $1 是应用计算的截止时间，$2 是应用时间。
//
// 引用保护（与旧实现一致，全流程只更新状态、不删除记录与物理文件）：
//   - 只处理仍可用、用途为帖子、尚未挂到任何帖子的记录（头像附件不在此列）；
//   - 不存在生效中的草稿关联（post_draft_attachments.is_active）；
//   - 不被任何用户当前头像引用（$3 由 auth 端口 ListAvatarAttachmentIDs 提供）。
//
// 说明：post_draft_attachments 属于 content 模块；这里保留只读引用检查是清理任务
// 的既有安全语义。后续可在 contracts 增加“批量查询草稿引用附件”的只读端口后移除
// 这段跨表子查询（见 task-14 未决点）。
const markExpiredTempDeletedSQL = `
	UPDATE attachments AS a
	SET status = 'deleted', updated_at = $2
	WHERE a.post_id IS NULL
	  AND a.status = 'available'
	  AND a.purpose = 'post'
	  AND a.created_at < $1
	  AND a.id <> ALL($3)
	  AND NOT EXISTS (
		SELECT 1 FROM post_draft_attachments AS da
		WHERE da.attachment_id = a.id AND da.is_active
	  )`

// MarkExpiredTempDeleted 执行临时附件失效（见上）。
func MarkExpiredTempDeleted(ctx context.Context, exec db.Executor, cutoff, at time.Time, avatarAttachmentIDs []int64) (int64, error) {
	if avatarAttachmentIDs == nil {
		avatarAttachmentIDs = []int64{}
	}
	tag, err := exec.Exec(ctx, markExpiredTempDeletedSQL, cutoff, at, avatarAttachmentIDs)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func scanAttachment(row pgx.Row) (AttachmentRow, error) {
	var out AttachmentRow
	if err := row.Scan(
		&out.ID, &out.UploaderID, &out.PostID, &out.Kind, &out.Purpose,
		&out.OriginalFilename, &out.MediaType, &out.SizeBytes, &out.StorageKey,
		&out.Status, &out.Width, &out.Height, &out.DurationMS, &out.CreatedAt, &out.UpdatedAt,
	); err != nil {
		return AttachmentRow{}, err
	}
	return out, nil
}

// IsNotFound 判断错误是否为“记录不存在”。
func IsNotFound(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
