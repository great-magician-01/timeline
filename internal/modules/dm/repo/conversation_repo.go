package repo

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/platform/db"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// ConversationRepo 实现 app.ConversationRepo。
type ConversationRepo struct {
	pool *pgxpool.Pool
}

// NewConversationRepo 构造私信会话仓储；pool 由组合根注入。
func NewConversationRepo(pool *pgxpool.Pool) *ConversationRepo { return &ConversationRepo{pool: pool} }

// conversationColumns 是 dm_conversations 的完整行投影，顺序与 scanConversation 一致。
const conversationColumns = `id, user_a_id, user_b_id, last_message_id,
	user_a_hidden_at, user_b_hidden_at, created_at, updated_at`

const (
	// 唯一组合索引兜底并发：已存在时不插入，由调用方再按双方读取既有会话。
	sqlConversationUpsert = `INSERT INTO dm_conversations (id, user_a_id, user_b_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $4)
		ON CONFLICT (user_a_id, user_b_id) DO NOTHING
		RETURNING ` + conversationColumns

	sqlConversationByPair = `SELECT ` + conversationColumns + `
		FROM dm_conversations WHERE user_a_id = $1 AND user_b_id = $2`

	sqlConversationByID = `SELECT ` + conversationColumns + `
		FROM dm_conversations WHERE id = $1`

	sqlConversationByIDForUpdate = `SELECT ` + conversationColumns + `
		FROM dm_conversations WHERE id = $1 FOR UPDATE`

	// 列表只返回「未隐藏且有消息」的会话；两侧各用一条部分索引，OR 条件显式加括号。
	sqlConversationsFirstPage = `SELECT ` + conversationColumns + `
		FROM dm_conversations
		WHERE ((user_a_id = $1 AND user_a_hidden_at IS NULL)
			OR (user_b_id = $1 AND user_b_hidden_at IS NULL))
		  AND last_message_id IS NOT NULL
		ORDER BY last_message_id DESC LIMIT $2`
	sqlConversationsAfterCursor = `SELECT ` + conversationColumns + `
		FROM dm_conversations
		WHERE ((user_a_id = $1 AND user_a_hidden_at IS NULL)
			OR (user_b_id = $1 AND user_b_hidden_at IS NULL))
		  AND last_message_id IS NOT NULL AND last_message_id < $2
		ORDER BY last_message_id DESC LIMIT $3`

	// 新消息推进最后消息指针，并清空双侧隐藏位（隐藏方重新看到会话）。
	sqlConversationTouchOnSend = `UPDATE dm_conversations
		SET last_message_id = $2, user_a_hidden_at = NULL, user_b_hidden_at = NULL, updated_at = $3
		WHERE id = $1`

	// 删除会话视图：只写自己的隐藏位，对方无感知。
	sqlConversationHideFor = `UPDATE dm_conversations
		SET user_a_hidden_at = CASE WHEN user_a_id = $2 THEN $3 ELSE user_a_hidden_at END,
		    user_b_hidden_at = CASE WHEN user_b_id = $2 THEN $3 ELSE user_b_hidden_at END,
		    updated_at = $3
		WHERE id = $1 AND (user_a_id = $2 OR user_b_id = $2)
		RETURNING id`
)

// Upsert 创建会话；已存在时返回既有行（重复发起幂等返回同一会话）。
func (r *ConversationRepo) Upsert(ctx context.Context, conv model.DMConversation) (model.DMConversation, error) {
	exec := db.ExecutorFrom(ctx, r.pool)
	created, err := scanConversation(exec.QueryRow(ctx, sqlConversationUpsert,
		conv.ID, conv.UserAID, conv.UserBID, conv.CreatedAt))
	if err == nil {
		return created, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return model.DMConversation{}, err
	}
	existing, err := scanConversation(exec.QueryRow(ctx, sqlConversationByPair, conv.UserAID, conv.UserBID))
	return existing, notFound(err, "会话不存在")
}

// GetByID 按 ID 读取会话；不存在返回领域 NotFound。
func (r *ConversationRepo) GetByID(ctx context.Context, id int64) (model.DMConversation, error) {
	conv, err := scanConversation(db.ExecutorFrom(ctx, r.pool).QueryRow(ctx, sqlConversationByID, id))
	return conv, notFound(err, "会话不存在")
}

// GetForUpdate 在调用方事务内读取会话并加行锁，用于发送/已读/隐藏的并发一致性。
func (r *ConversationRepo) GetForUpdate(ctx context.Context, id int64) (model.DMConversation, error) {
	conv, err := scanConversation(db.ExecutorFrom(ctx, r.pool).QueryRow(ctx, sqlConversationByIDForUpdate, id))
	return conv, notFound(err, "会话不存在")
}

// ListByUser 返回用户未隐藏且有消息的会话，按最后消息倒序；cursor=0 为首页。
func (r *ConversationRepo) ListByUser(ctx context.Context, userID, cursor int64, limit int) ([]model.DMConversation, error) {
	exec := db.ExecutorFrom(ctx, r.pool)
	var (
		rows pgx.Rows
		err  error
	)
	if cursor > 0 {
		rows, err = exec.Query(ctx, sqlConversationsAfterCursor, userID, cursor, limit)
	} else {
		rows, err = exec.Query(ctx, sqlConversationsFirstPage, userID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	conversations := make([]model.DMConversation, 0, limit)
	for rows.Next() {
		conv, err := scanConversation(rows)
		if err != nil {
			return nil, err
		}
		conversations = append(conversations, conv)
	}
	return conversations, rows.Err()
}

// TouchOnSend 在发送事务内推进最后消息指针并清空双侧隐藏位。
func (r *ConversationRepo) TouchOnSend(ctx context.Context, id, messageID int64, at time.Time) error {
	_, err := db.ExecutorFrom(ctx, r.pool).Exec(ctx, sqlConversationTouchOnSend, id, messageID, at)
	return err
}

// HideFor 只隐藏 userID 自己一侧的会话视图；非成员不产生行。
func (r *ConversationRepo) HideFor(ctx context.Context, id, userID int64, at time.Time) (bool, error) {
	var returned int64
	err := db.ExecutorFrom(ctx, r.pool).QueryRow(ctx, sqlConversationHideFor, id, userID, at).Scan(&returned)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func scanConversation(row rowScanner) (model.DMConversation, error) {
	var conv model.DMConversation
	err := row.Scan(
		&conv.ID, &conv.UserAID, &conv.UserBID, &conv.LastMessageID,
		&conv.UserAHiddenAt, &conv.UserBHiddenAt, &conv.CreatedAt, &conv.UpdatedAt,
	)
	return conv, err
}

// rowScanner 抽象 pgx.Row 与 pgx.Rows 的 Scan，便于复用同一列投影。
type rowScanner interface {
	Scan(dest ...any) error
}
