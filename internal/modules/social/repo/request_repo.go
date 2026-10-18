package repo

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/modules/social/domain"
	"github.com/great-magician-01/timeline/internal/platform/db"
)

// RequestRepo 实现 app.FollowRequestRepo。
type RequestRepo struct {
	pool *pgxpool.Pool
}

// NewRequestRepo 构造关注申请仓储。
func NewRequestRepo(pool *pgxpool.Pool) *RequestRepo { return &RequestRepo{pool: pool} }

const (
	// 同一对账号只保留一条待处理申请：部分唯一索引兜底并发幂等。
	sqlRequestInsertPending = `INSERT INTO follow_requests (id, requester_id, target_id, status, created_at)
		VALUES ($1, $2, $3, 'pending', $4)
		ON CONFLICT (requester_id, target_id) WHERE status = 'pending' DO NOTHING
		RETURNING id`

	sqlRequestColumns = `id, requester_id, target_id, status, created_at, resolved_at`

	sqlRequestGetForUpdate = `SELECT ` + sqlRequestColumns + `
		FROM follow_requests WHERE id = $1 FOR UPDATE`

	sqlRequestUpdateStatus = `UPDATE follow_requests SET status = $2, resolved_at = $3 WHERE id = $1`

	sqlRequestListIncomingFirstPage = `SELECT ` + sqlRequestColumns + `
		FROM follow_requests WHERE target_id = $1 AND status = 'pending'
		ORDER BY id DESC LIMIT $2`
	sqlRequestListIncomingAfterCursor = `SELECT ` + sqlRequestColumns + `
		FROM follow_requests WHERE target_id = $1 AND status = 'pending' AND id < $2
		ORDER BY id DESC LIMIT $3`
	sqlRequestListOutgoingFirstPage = `SELECT ` + sqlRequestColumns + `
		FROM follow_requests WHERE requester_id = $1 AND status = 'pending'
		ORDER BY id DESC LIMIT $2`
	sqlRequestListOutgoingAfterCursor = `SELECT ` + sqlRequestColumns + `
		FROM follow_requests WHERE requester_id = $1 AND status = 'pending' AND id < $2
		ORDER BY id DESC LIMIT $3`

	sqlRequestCancelPending = `UPDATE follow_requests SET status = 'canceled', resolved_at = $3
		WHERE requester_id = $1 AND target_id = $2 AND status = 'pending'`
)

// InsertPending 创建待处理申请；已有 pending 申请时 changed 为 false。
func (r *RequestRepo) InsertPending(
	ctx context.Context, id, requesterID, targetID int64, now time.Time,
) (requestID int64, ok bool, err error) {
	err = db.ExecutorFrom(ctx, r.pool).QueryRow(ctx, sqlRequestInsertPending, id, requesterID, targetID, now).
		Scan(&requestID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return requestID, true, nil
}

// GetForUpdate 在事务内加锁读取申请。
func (r *RequestRepo) GetForUpdate(ctx context.Context, id int64) (domain.RequestRow, error) {
	var row domain.RequestRow
	err := db.ExecutorFrom(ctx, r.pool).QueryRow(ctx, sqlRequestGetForUpdate, id).
		Scan(&row.ID, &row.RequesterID, &row.TargetID, &row.Status, &row.CreatedAt, &row.ResolvedAt)
	return row, notFound(err, "申请不存在")
}

// UpdateStatus 迁移申请状态并写入处理时间。
func (r *RequestRepo) UpdateStatus(ctx context.Context, id int64, status string, resolvedAt time.Time) error {
	_, err := db.ExecutorFrom(ctx, r.pool).Exec(ctx, sqlRequestUpdateStatus, id, status, resolvedAt)
	return err
}

// ListPendingIncoming 列出待我审批的申请。
func (r *RequestRepo) ListPendingIncoming(ctx context.Context, targetID, cursor int64, limit int) ([]domain.RequestRow, error) {
	return r.list(ctx, sqlRequestListIncomingFirstPage, sqlRequestListIncomingAfterCursor, targetID, cursor, limit)
}

// ListPendingOutgoing 列出我发出的待审批申请。
func (r *RequestRepo) ListPendingOutgoing(ctx context.Context, requesterID, cursor int64, limit int) ([]domain.RequestRow, error) {
	return r.list(ctx, sqlRequestListOutgoingFirstPage, sqlRequestListOutgoingAfterCursor, requesterID, cursor, limit)
}

// CancelPending 取消自己发出的待处理申请。
func (r *RequestRepo) CancelPending(ctx context.Context, requesterID, targetID int64, now time.Time) (bool, error) {
	tag, err := db.ExecutorFrom(ctx, r.pool).Exec(ctx, sqlRequestCancelPending, requesterID, targetID, now)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (r *RequestRepo) list(
	ctx context.Context,
	firstPageSQL, afterCursorSQL string,
	ownerID, cursor int64,
	limit int,
) ([]domain.RequestRow, error) {
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

	items := make([]domain.RequestRow, 0, limit)
	for rows.Next() {
		var row domain.RequestRow
		if err := rows.Scan(&row.ID, &row.RequesterID, &row.TargetID, &row.Status, &row.CreatedAt, &row.ResolvedAt); err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, rows.Err()
}
