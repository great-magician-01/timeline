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

// Usage 读取上传者累计占用的存储字节数（attachment_usage 冗余计数）。
// 无计数行表示该用户尚未上传过附件，返回 0 而不是 ErrNoRows。
// 口径：含已逻辑删除的附件（物理文件仍占磁盘、从不释放），与旧实时
// SUM(attachments.size_bytes) 一致，因此计数只增不减。
func Usage(ctx context.Context, exec db.Executor, uploaderID int64) (int64, error) {
	var used int64
	err := exec.QueryRow(ctx,
		`SELECT used_bytes FROM attachment_usage WHERE uploader_id = $1`, uploaderID).Scan(&used)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return used, nil
}

// AddUsage 在上传事务内把本次附件大小累加到上传者用量（首次 upsert、之后 O(1) 递增）。
// delta 与 used_bytes 均非负；updated_at 使用调用方传入的应用时间。
func AddUsage(ctx context.Context, exec db.Executor, uploaderID, delta int64, at time.Time) error {
	_, err := exec.Exec(ctx, `
		INSERT INTO attachment_usage (uploader_id, used_bytes, updated_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (uploader_id) DO UPDATE
		SET used_bytes = attachment_usage.used_bytes + EXCLUDED.used_bytes,
		    updated_at = EXCLUDED.updated_at`, uploaderID, delta, at)
	return err
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

// listExpiredTempAttachmentCandidatesSQL 选出超过保留期、尚未挂帖子的临时帖子附件候选 ID。
// $1 是应用计算的截止时间，$2 是当前头像附件 ID 列表（auth 端口提供）。
//
// 保护条件（全流程只更新状态、不删除记录与物理文件）：
//   - 只处理仍可用、用途为帖子、尚未挂到任何帖子的记录（头像附件不在此列）；
//   - 不被任何用户当前头像引用（$2 由 auth 端口 ListAvatarAttachmentIDs 提供；
//     存量头像引用由迁移 0016 回填 users.avatar_attachment_id 保证，清理不依赖 avatar_url，
//     也不查询 users 表）；
//   - 草稿引用不在 SQL 内检查：由 app 通过 content 端口 ActiveDraftAttachmentIDs 二次过滤，
//     避免 media 跨模块查询 content 拥有的 post_draft_attachments 表。
//
// 排序使用 created_at, id：与 0017 的部分索引
// `(created_at) WHERE post_id IS NULL AND status='available' AND purpose='post'` 顺序一致，
// 避免先取全部候选再按 id 排序；清理语义只要求“过期”，返回顺序不影响正确性。
const listExpiredTempAttachmentCandidatesSQL = `
	SELECT a.id FROM attachments AS a
	WHERE a.post_id IS NULL
	  AND a.status = 'available'
	  AND a.purpose = 'post'
	  AND a.created_at < $1
	  AND a.id <> ALL($2)
	ORDER BY a.created_at, a.id
	LIMIT $3`

// ListExpiredTempAttachmentCandidates 返回过期临时附件候选 ID（按创建时间、ID 升序，最多 limit 条）。
func ListExpiredTempAttachmentCandidates(
	ctx context.Context, exec db.Executor, cutoff time.Time, limit int, avatarAttachmentIDs []int64,
) ([]int64, error) {
	if limit <= 0 {
		limit = defaultCleanupCandidateLimit
	}
	if avatarAttachmentIDs == nil {
		avatarAttachmentIDs = []int64{}
	}
	rows, err := exec.Query(ctx, listExpiredTempAttachmentCandidatesSQL, cutoff, avatarAttachmentIDs, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]int64, 0, limit)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// defaultCleanupCandidateLimit 是单次清理最多处理的候选附件数（避免一次扫描过大）。
const defaultCleanupCandidateLimit = 1000

// markDeletedByIDsSQL 把一批附件置为逻辑失效；已失效的记录不重复计入。
const markDeletedByIDsSQL = `
	UPDATE attachments
	SET status = 'deleted', updated_at = $2
	WHERE id = ANY($1) AND status <> 'deleted'`

// MarkDeletedByIDs 批量逻辑失效附件，返回实际变更行数（空集合直接返回 0）。
func MarkDeletedByIDs(ctx context.Context, exec db.Executor, ids []int64, at time.Time) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	tag, err := exec.Exec(ctx, markDeletedByIDsSQL, ids, at)
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
