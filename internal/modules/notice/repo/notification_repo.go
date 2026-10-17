package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/platform/db"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// NotificationRepo 实现 app.NotificationRepo。
type NotificationRepo struct {
	pool *pgxpool.Pool
}

// NewNotificationRepo 构造通知仓储。
func NewNotificationRepo(pool *pgxpool.Pool) *NotificationRepo {
	return &NotificationRepo{pool: pool}
}

const (
	notificationColumns = `id, recipient_id, actor_id, type, target_type, target_id,
		post_id, comment_id, is_read, idempotency_key, created_at`

	// 幂等键唯一索引兜底重复投递。
	sqlNotificationInsert = `INSERT INTO notifications (
			id, recipient_id, actor_id, type, target_type, target_id,
			post_id, comment_id, is_read, idempotency_key, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (idempotency_key) DO NOTHING`

	sqlNotificationUnreadCount = `SELECT COUNT(*) FROM notifications WHERE recipient_id = $1 AND NOT is_read`

	sqlNotificationMarkRead = `UPDATE notifications SET is_read = TRUE
		WHERE id = $1 AND recipient_id = $2 RETURNING is_read`

	sqlNotificationMarkAllRead = `UPDATE notifications SET is_read = TRUE
		WHERE recipient_id = $1 AND NOT is_read`
)

// Insert 写入通知；幂等键冲突时静默忽略。
func (r *NotificationRepo) Insert(ctx context.Context, row model.Notification) error {
	_, err := db.ExecutorFrom(ctx, r.pool).Exec(ctx, sqlNotificationInsert,
		row.ID, row.RecipientID, row.ActorID, row.Type, row.TargetType, row.TargetID,
		row.PostID, row.CommentID, row.IsRead, row.IdempotencyKey, row.CreatedAt,
	)
	return err
}

// List 按接收者分页（ID 倒序）；filter 为 all/unread，noticeType 为空表示不过滤。
func (r *NotificationRepo) List(
	ctx context.Context, recipientID int64, filter, noticeType string, cursor int64, limit int,
) ([]model.Notification, error) {
	args := make([]any, 0, 5)
	args = append(args, recipientID)
	query := `SELECT ` + notificationColumns + ` FROM notifications WHERE recipient_id = $1`
	if filter == "unread" {
		query += ` AND NOT is_read`
	}
	if noticeType != "" {
		args = append(args, noticeType)
		query += fmt.Sprintf(` AND type = $%d`, len(args))
	}
	if cursor > 0 {
		args = append(args, cursor)
		query += fmt.Sprintf(` AND id < $%d`, len(args))
	}
	args = append(args, limit)
	query += fmt.Sprintf(` ORDER BY id DESC LIMIT $%d`, len(args))

	rows, err := db.ExecutorFrom(ctx, r.pool).Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]model.Notification, 0, limit)
	for rows.Next() {
		var row model.Notification
		if err := rows.Scan(
			&row.ID, &row.RecipientID, &row.ActorID, &row.Type, &row.TargetType, &row.TargetID,
			&row.PostID, &row.CommentID, &row.IsRead, &row.IdempotencyKey, &row.CreatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, rows.Err()
}

// UnreadCount 返回接收者的未读通知数。
func (r *NotificationRepo) UnreadCount(ctx context.Context, recipientID int64) (int64, error) {
	var count int64
	err := db.ExecutorFrom(ctx, r.pool).QueryRow(ctx, sqlNotificationUnreadCount, recipientID).Scan(&count)
	return count, err
}

// MarkRead 标记单条已读；不存在或不属于该接收者时返回 false。
func (r *NotificationRepo) MarkRead(ctx context.Context, recipientID, id int64) (bool, error) {
	var isRead bool
	err := db.ExecutorFrom(ctx, r.pool).QueryRow(ctx, sqlNotificationMarkRead, id, recipientID).Scan(&isRead)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return isRead, nil
}

// MarkAllRead 标记全部未读并返回更新行数。
func (r *NotificationRepo) MarkAllRead(ctx context.Context, recipientID int64) (int64, error) {
	tag, err := db.ExecutorFrom(ctx, r.pool).Exec(ctx, sqlNotificationMarkAllRead, recipientID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
