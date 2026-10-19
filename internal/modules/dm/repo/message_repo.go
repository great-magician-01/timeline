package repo

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/platform/db"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// MessageRepo 实现 app.MessageRepo。
type MessageRepo struct {
	pool *pgxpool.Pool
}

// NewMessageRepo 构造私信消息仓储；pool 由组合根注入。
func NewMessageRepo(pool *pgxpool.Pool) *MessageRepo { return &MessageRepo{pool: pool} }

// messageColumns 是 dm_messages 的完整行投影，顺序与 scanMessage 一致。
const messageColumns = `id, conversation_id, sender_id, recipient_id,
	content_format, content_source, content_html, plain_text,
	status, read_at, created_at, updated_at`

const (
	sqlMessageInsert = `INSERT INTO dm_messages (
			id, conversation_id, sender_id, recipient_id,
			content_format, content_source, content_html, plain_text,
			status, read_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11)
		RETURNING ` + messageColumns

	sqlMessageByIDForUpdate = `SELECT ` + messageColumns + `
		FROM dm_messages WHERE id = $1 FOR UPDATE`

	sqlMessagesByIDs = `SELECT ` + messageColumns + `
		FROM dm_messages WHERE id = ANY($1)`

	// 对话页翻旧消息：按雪花 ID 倒序，cursor=0 为最新一页。
	sqlMessagesFirstPage = `SELECT ` + messageColumns + `
		FROM dm_messages WHERE conversation_id = $1
		ORDER BY id DESC LIMIT $2`
	sqlMessagesAfterCursor = `SELECT ` + messageColumns + `
		FROM dm_messages WHERE conversation_id = $1 AND id < $2
		ORDER BY id DESC LIMIT $3`

	// 增量拉新：id > afterID 升序返回。
	sqlMessagesAfterID = `SELECT ` + messageColumns + `
		FROM dm_messages WHERE conversation_id = $1 AND id > $2
		ORDER BY id ASC LIMIT $3`

	// 已读只推进自己接收方向未读的已发布消息；幂等重复调用影响 0 行。
	sqlMessagesMarkRead = `UPDATE dm_messages
		SET read_at = $3, updated_at = $3
		WHERE conversation_id = $1 AND recipient_id = $2
		  AND read_at IS NULL AND status = 'published'`

	// 撤回仅由发送者本人对已发布消息生效，重复撤回影响 0 行。
	sqlMessageRecall = `UPDATE dm_messages
		SET status = 'recalled', updated_at = $3
		WHERE id = $1 AND sender_id = $2 AND status = 'published'`

	sqlMessagesUnreadCount = `SELECT COUNT(*)
		FROM dm_messages
		WHERE recipient_id = $1 AND read_at IS NULL AND status = 'published'`

	sqlMessagesUnreadByConversations = `SELECT conversation_id, COUNT(*)
		FROM dm_messages
		WHERE recipient_id = $1 AND read_at IS NULL AND status = 'published'
		  AND conversation_id = ANY($2)
		GROUP BY conversation_id`
)

// Insert 插入消息并返回落库行；时间列使用传入值。
func (r *MessageRepo) Insert(ctx context.Context, msg model.DMMessage) (model.DMMessage, error) {
	inserted, err := scanMessage(db.ExecutorFrom(ctx, r.pool).QueryRow(ctx, sqlMessageInsert,
		msg.ID, msg.ConversationID, msg.SenderID, msg.RecipientID,
		msg.ContentFormat, msg.ContentSource, msg.ContentHTML, msg.PlainText,
		msg.Status, msg.ReadAt, msg.CreatedAt))
	return inserted, notFound(err, "消息不存在")
}

// GetForUpdate 在调用方事务内读取消息并加行锁。
func (r *MessageRepo) GetForUpdate(ctx context.Context, id int64) (model.DMMessage, error) {
	msg, err := scanMessage(db.ExecutorFrom(ctx, r.pool).QueryRow(ctx, sqlMessageByIDForUpdate, id))
	return msg, notFound(err, "消息不存在")
}

// ByIDs 批量读取消息（会话列表最后消息预览）。
func (r *MessageRepo) ByIDs(ctx context.Context, ids []int64) (map[int64]model.DMMessage, error) {
	result := make(map[int64]model.DMMessage, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := db.ExecutorFrom(ctx, r.pool).Query(ctx, sqlMessagesByIDs, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		msg, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		result[msg.ID] = msg
	}
	return result, rows.Err()
}

// List 返回会话内消息（id DESC 翻旧）；cursor=0 为最新一页。
func (r *MessageRepo) List(ctx context.Context, conversationID, cursor int64, limit int) ([]model.DMMessage, error) {
	exec := db.ExecutorFrom(ctx, r.pool)
	var (
		rows pgx.Rows
		err  error
	)
	if cursor > 0 {
		rows, err = exec.Query(ctx, sqlMessagesAfterCursor, conversationID, cursor, limit)
	} else {
		rows, err = exec.Query(ctx, sqlMessagesFirstPage, conversationID, limit)
	}
	if err != nil {
		return nil, err
	}
	return collectMessages(rows, limit)
}

// ListAfter 返回 afterID 之后的新消息（id ASC），供对话页增量轮询。
func (r *MessageRepo) ListAfter(ctx context.Context, conversationID, afterID int64, limit int) ([]model.DMMessage, error) {
	rows, err := db.ExecutorFrom(ctx, r.pool).Query(ctx, sqlMessagesAfterID, conversationID, afterID, limit)
	if err != nil {
		return nil, err
	}
	return collectMessages(rows, limit)
}

// MarkRead 把会话内接收方未读的已发布消息置为已读，返回更新条数。
func (r *MessageRepo) MarkRead(ctx context.Context, conversationID, recipientID int64, at time.Time) (int64, error) {
	tag, err := db.ExecutorFrom(ctx, r.pool).Exec(ctx, sqlMessagesMarkRead, conversationID, recipientID, at)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// Recall 撤回发送者本人的已发布消息；重复撤回返回 changed=false。
func (r *MessageRepo) Recall(ctx context.Context, id, senderID int64, at time.Time) (bool, error) {
	tag, err := db.ExecutorFrom(ctx, r.pool).Exec(ctx, sqlMessageRecall, id, senderID, at)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// UnreadCount 返回用户全部私信未读数（仅未撤回），供徽章轮询。
func (r *MessageRepo) UnreadCount(ctx context.Context, recipientID int64) (int, error) {
	var count int
	err := db.ExecutorFrom(ctx, r.pool).QueryRow(ctx, sqlMessagesUnreadCount, recipientID).Scan(&count)
	return count, err
}

// UnreadByConversations 批量返回用户在指定会话内的未读数。
func (r *MessageRepo) UnreadByConversations(ctx context.Context, recipientID int64, conversationIDs []int64) (map[int64]int, error) {
	result := make(map[int64]int, len(conversationIDs))
	if len(conversationIDs) == 0 {
		return result, nil
	}
	rows, err := db.ExecutorFrom(ctx, r.pool).Query(ctx, sqlMessagesUnreadByConversations, recipientID, conversationIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			conversationID int64
			count          int
		)
		if err := rows.Scan(&conversationID, &count); err != nil {
			return nil, err
		}
		result[conversationID] = count
	}
	return result, rows.Err()
}

func collectMessages(rows pgx.Rows, limit int) ([]model.DMMessage, error) {
	defer rows.Close()
	messages := make([]model.DMMessage, 0, limit)
	for rows.Next() {
		msg, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}
	return messages, rows.Err()
}

func scanMessage(row rowScanner) (model.DMMessage, error) {
	var msg model.DMMessage
	err := row.Scan(
		&msg.ID, &msg.ConversationID, &msg.SenderID, &msg.RecipientID,
		&msg.ContentFormat, &msg.ContentSource, &msg.ContentHTML, &msg.PlainText,
		&msg.Status, &msg.ReadAt, &msg.CreatedAt, &msg.UpdatedAt,
	)
	return msg, err
}
