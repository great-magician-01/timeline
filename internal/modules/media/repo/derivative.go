package repo

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/great-magician-01/timeline/internal/platform/db"
)

// Derivative 是 attachment_derivatives 行。
type Derivative struct {
	ID             int64
	AttachmentID   int64
	DerivativeType string
	Status         string
	StorageKey     string
	LastError      string
	Attempts       int
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

const derivativeColumns = `id, attachment_id, derivative_type, status, storage_key,
	last_error, attempts, created_at, updated_at`

// GetDerivative 读取单个衍生文件记录；不存在返回 pgx.ErrNoRows。
func GetDerivative(ctx context.Context, exec db.Executor, attachmentID int64, derivativeType string) (Derivative, error) {
	return scanDerivative(exec.QueryRow(ctx,
		`SELECT `+derivativeColumns+` FROM attachment_derivatives
		 WHERE attachment_id = $1 AND derivative_type = $2`, attachmentID, derivativeType))
}

// StatusesByAttachmentIDs 批量读取衍生文件状态：attachment_id → derivative_type → status。
func StatusesByAttachmentIDs(ctx context.Context, exec db.Executor, attachmentIDs []int64) (map[int64]map[string]string, error) {
	result := make(map[int64]map[string]string, len(attachmentIDs))
	if len(attachmentIDs) == 0 {
		return result, nil
	}
	rows, err := exec.Query(ctx, `
		SELECT attachment_id, derivative_type, status
		FROM attachment_derivatives
		WHERE attachment_id = ANY($1)`, attachmentIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var attachmentID int64
		var derivativeType, status string
		if err := rows.Scan(&attachmentID, &derivativeType, &status); err != nil {
			return nil, err
		}
		if result[attachmentID] == nil {
			result[attachmentID] = map[string]string{}
		}
		result[attachmentID][derivativeType] = status
	}
	return result, rows.Err()
}

// InsertDerivativeInput 描述一条新衍生文件记录。
type InsertDerivativeInput struct {
	ID             int64
	AttachmentID   int64
	DerivativeType string
	Status         string
	LastError      string
	At             time.Time
}

// InsertDerivative 写入一条衍生文件记录；created_at/updated_at 使用应用时间 At。
func InsertDerivative(ctx context.Context, exec db.Executor, in InsertDerivativeInput) error {
	_, err := exec.Exec(ctx, `
		INSERT INTO attachment_derivatives (
			id, attachment_id, derivative_type, status, last_error, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $6)`,
		in.ID, in.AttachmentID, in.DerivativeType, in.Status, in.LastError, in.At)
	return err
}

// ClaimProcessing 原子认领衍生文件：只有 pending/failed/processing 的行可以进入处理。
func ClaimProcessing(ctx context.Context, exec db.Executor, attachmentID int64, derivativeType string, at time.Time) (bool, error) {
	tag, err := exec.Exec(ctx, `
		UPDATE attachment_derivatives
		SET status = 'processing', updated_at = $3
		WHERE attachment_id = $1 AND derivative_type = $2
		  AND status IN ('pending', 'failed', 'processing')`,
		attachmentID, derivativeType, at)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// MarkReady 记录衍生文件生成成功；只在仍处于 processing 时生效（不复活已取消的行）。
func MarkReady(ctx context.Context, exec db.Executor, attachmentID int64, derivativeType, storageKey string, at time.Time) (bool, error) {
	tag, err := exec.Exec(ctx, `
		UPDATE attachment_derivatives
		SET storage_key = $3, status = 'ready', last_error = '', updated_at = $4
		WHERE attachment_id = $1 AND derivative_type = $2 AND status = 'processing'`,
		attachmentID, derivativeType, storageKey, at)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// MarkFailed 记录失败原因并累加尝试次数；只影响仍处于 processing 的行。
func MarkFailed(ctx context.Context, exec db.Executor, attachmentID int64, derivativeType, message string, at time.Time) (bool, error) {
	tag, err := exec.Exec(ctx, `
		UPDATE attachment_derivatives
		SET status = 'failed', last_error = $3, attempts = attempts + 1, updated_at = $4
		WHERE attachment_id = $1 AND derivative_type = $2 AND status = 'processing'`,
		attachmentID, derivativeType, message, at)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// CancelIncompleteForAttachment 取消某个附件上尚未就绪的衍生文件（附件失效时）。
func CancelIncompleteForAttachment(ctx context.Context, exec db.Executor, attachmentID int64, at time.Time) error {
	_, err := exec.Exec(ctx, `
		UPDATE attachment_derivatives
		SET status = 'canceled', updated_at = $2
		WHERE attachment_id = $1 AND status NOT IN ('ready', 'canceled')`, attachmentID, at)
	return err
}

// CancelIncompleteForDeletedAttachments 取消全部已失效附件上尚未就绪的衍生文件。
// 状态条件写成正向列表（pending/processing/failed，即 NOT IN (ready,canceled) 的补集），
// 这样能命中 attachment_derivatives_status (status, id) 索引；再由 EXISTS 按主键探查
// 附件状态，避免每次清理都全量重扫历史上所有已逻辑删除的附件。
func CancelIncompleteForDeletedAttachments(ctx context.Context, exec db.Executor, at time.Time) (int64, error) {
	tag, err := exec.Exec(ctx, `
		UPDATE attachment_derivatives AS d
		SET status = 'canceled', updated_at = $1
		WHERE d.status IN ('pending', 'processing', 'failed')
		  AND EXISTS (
			SELECT 1 FROM attachments AS a
			WHERE a.id = d.attachment_id AND a.status = 'deleted'
		  )`, at)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func scanDerivative(row pgx.Row) (Derivative, error) {
	var out Derivative
	if err := row.Scan(
		&out.ID, &out.AttachmentID, &out.DerivativeType, &out.Status, &out.StorageKey,
		&out.LastError, &out.Attempts, &out.CreatedAt, &out.UpdatedAt,
	); err != nil {
		return Derivative{}, err
	}
	return out, nil
}
