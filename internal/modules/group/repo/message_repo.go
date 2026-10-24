package repo

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/platform/db"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// MessageRepo 实现 app.MessageRepo（群消息）。
type MessageRepo struct {
	pool *pgxpool.Pool
}

// NewMessageRepo 构造群消息仓储；pool 由组合根注入。
func NewMessageRepo(pool *pgxpool.Pool) *MessageRepo { return &MessageRepo{pool: pool} }

// messageColumns 是 group_messages 的完整行投影，顺序与 scanMessage 一致。
const messageColumns = `id, group_id, sender_id,
	content_format, content_source, content_html, plain_text,
	status, created_at, updated_at`

const (
	sqlMessageInsert = `INSERT INTO group_messages (
			id, group_id, sender_id,
			content_format, content_source, content_html, plain_text,
			status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)
		RETURNING ` + messageColumns

	sqlMessageByIDForUpdate = `SELECT ` + messageColumns + `
		FROM group_messages WHERE id = $1 FOR UPDATE`

	sqlMessagesByIDs = `SELECT ` + messageColumns + `
		FROM group_messages WHERE id = ANY($1)`

	// 对话页翻旧消息：按雪花 ID 倒序，cursor=0 为最新一页。
	sqlMessagesFirstPage = `SELECT ` + messageColumns + `
		FROM group_messages WHERE group_id = $1
		ORDER BY id DESC LIMIT $2`
	sqlMessagesAfterCursor = `SELECT ` + messageColumns + `
		FROM group_messages WHERE group_id = $1 AND id < $2
		ORDER BY id DESC LIMIT $3`

	// 增量拉新：id > afterID 升序返回。
	sqlMessagesAfterID = `SELECT ` + messageColumns + `
		FROM group_messages WHERE group_id = $1 AND id > $2
		ORDER BY id ASC LIMIT $3`

	// 撤回仅由发送者本人对已发布消息生效，重复撤回影响 0 行。
	sqlMessageRecall = `UPDATE group_messages
		SET status = 'recalled', updated_at = $3
		WHERE id = $1 AND sender_id = $2 AND status = 'published'`

	// 未读数按成员级游标：游标之后、非本人发送、未撤回；游标为空时全部计数。
	// COALESCE 参数显式 ::bigint：裸 0 字面量会让 PG 把 $3 推断为 int4，装不下雪花 ID。
	sqlMessagesUnreadCount = `SELECT COUNT(*)
		FROM group_messages
		WHERE group_id = $1 AND sender_id <> $2 AND status = 'published'
		  AND id > COALESCE($3::bigint, 0)`

	// 我的群列表未读批量统计：一条查询返回 (group_id, unread)。
	sqlMessagesUnreadByGroups = `SELECT m.group_id, COUNT(gm.id)
		FROM group_members m
		JOIN group_messages gm ON gm.group_id = m.group_id
			AND gm.sender_id <> m.user_id AND gm.status = 'published'
			AND gm.id > COALESCE(m.last_read_message_id, 0)
		WHERE m.user_id = $1 AND m.left_at IS NULL
		GROUP BY m.group_id`
)

// Insert 插入消息并返回落库行；时间列使用传入值。
func (r *MessageRepo) Insert(ctx context.Context, msg model.GroupMessage) (model.GroupMessage, error) {
	inserted, err := scanMessage(db.ExecutorFrom(ctx, r.pool).QueryRow(ctx, sqlMessageInsert,
		msg.ID, msg.GroupID, msg.SenderID,
		msg.ContentFormat, msg.ContentSource, msg.ContentHTML, msg.PlainText,
		msg.Status, msg.CreatedAt))
	return inserted, notFound(err, "消息不存在")
}

// GetForUpdate 在调用方事务内读取消息并加行锁；不存在返回领域 NotFound。
func (r *MessageRepo) GetForUpdate(ctx context.Context, id int64) (model.GroupMessage, error) {
	msg, err := scanMessage(db.ExecutorFrom(ctx, r.pool).QueryRow(ctx, sqlMessageByIDForUpdate, id))
	return msg, notFound(err, "消息不存在")
}

// ByIDs 批量读取消息（群列表最后消息预览）；缺失 ID 不在 map 中。
func (r *MessageRepo) ByIDs(ctx context.Context, ids []int64) (map[int64]model.GroupMessage, error) {
	result := make(map[int64]model.GroupMessage, len(ids))
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

// List 返回群内消息（id DESC 翻旧）；cursor=0 为最新一页。
func (r *MessageRepo) List(ctx context.Context, groupID, cursor int64, limit int) ([]model.GroupMessage, error) {
	exec := db.ExecutorFrom(ctx, r.pool)
	var (
		rows pgx.Rows
		err  error
	)
	if cursor > 0 {
		rows, err = exec.Query(ctx, sqlMessagesAfterCursor, groupID, cursor, limit)
	} else {
		rows, err = exec.Query(ctx, sqlMessagesFirstPage, groupID, limit)
	}
	if err != nil {
		return nil, err
	}
	return collectMessages(rows, limit)
}

// ListAfter 返回 afterID 之后的新消息（id ASC），供对话页增量轮询。
func (r *MessageRepo) ListAfter(ctx context.Context, groupID, afterID int64, limit int) ([]model.GroupMessage, error) {
	rows, err := db.ExecutorFrom(ctx, r.pool).Query(ctx, sqlMessagesAfterID, groupID, afterID, limit)
	if err != nil {
		return nil, err
	}
	return collectMessages(rows, limit)
}

// ListBefore 返回 beforeID 之前的消息（id DESC），供 AI 上下文回看。
func (r *MessageRepo) ListBefore(ctx context.Context, groupID, beforeID int64, limit int) ([]model.GroupMessage, error) {
	if beforeID <= 0 {
		beforeID = 1 << 62
	}
	rows, err := db.ExecutorFrom(ctx, r.pool).Query(ctx, sqlMessagesAfterCursor, groupID, beforeID, limit)
	if err != nil {
		return nil, err
	}
	return collectMessages(rows, limit)
}

// Recall 撤回发送者本人的已发布消息；重复撤回返回 changed=false。
func (r *MessageRepo) Recall(ctx context.Context, id, senderID int64, at time.Time) (bool, error) {
	tag, err := db.ExecutorFrom(ctx, r.pool).Exec(ctx, sqlMessageRecall, id, senderID, at)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// UnreadCount 返回成员在单个群内的未读数（游标之后、非本人、未撤回）。
func (r *MessageRepo) UnreadCount(ctx context.Context, groupID, userID int64, lastReadMessageID *int64) (int, error) {
	var count int
	cursor := int64(0)
	if lastReadMessageID != nil {
		cursor = *lastReadMessageID
	}
	err := db.ExecutorFrom(ctx, r.pool).QueryRow(ctx, sqlMessagesUnreadCount, groupID, userID, cursor).Scan(&count)
	return count, err
}

// UnreadByGroups 按成员游标批量统计用户在全部群的未读数。
func (r *MessageRepo) UnreadByGroups(ctx context.Context, userID int64) (map[int64]int, error) {
	rows, err := db.ExecutorFrom(ctx, r.pool).Query(ctx, sqlMessagesUnreadByGroups, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[int64]int, 8)
	for rows.Next() {
		var (
			groupID int64
			count   int
		)
		if err := rows.Scan(&groupID, &count); err != nil {
			return nil, err
		}
		result[groupID] = count
	}
	return result, rows.Err()
}

func collectMessages(rows pgx.Rows, limit int) ([]model.GroupMessage, error) {
	defer rows.Close()
	capacity := limit
	if capacity < 16 {
		capacity = 16
	}
	messages := make([]model.GroupMessage, 0, capacity)
	for rows.Next() {
		msg, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}
	return messages, rows.Err()
}

func scanMessage(row rowScanner) (model.GroupMessage, error) {
	var msg model.GroupMessage
	err := row.Scan(
		&msg.ID, &msg.GroupID, &msg.SenderID,
		&msg.ContentFormat, &msg.ContentSource, &msg.ContentHTML, &msg.PlainText,
		&msg.Status, &msg.CreatedAt, &msg.UpdatedAt,
	)
	return msg, err
}
