package repo

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/platform/db"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// ConversationRepo 实现 app.ConversationRepo（群会话）。
type ConversationRepo struct {
	pool *pgxpool.Pool
}

// NewConversationRepo 构造群会话仓储；pool 由组合根注入。
func NewConversationRepo(pool *pgxpool.Pool) *ConversationRepo { return &ConversationRepo{pool: pool} }

// conversationColumns 是 group_conversations 的完整行投影，顺序与 scanConversation 一致。
const conversationColumns = `id, name, owner_id, last_message_id, status, created_at, updated_at`

// conversationColumnsAliased 是 JOIN group_members 的列表查询用投影（g 别名限定）。
const conversationColumnsAliased = `g.id, g.name, g.owner_id, g.last_message_id, g.status, g.created_at, g.updated_at`

const (
	sqlConversationInsert = `INSERT INTO group_conversations (id, name, owner_id, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $5)
		RETURNING ` + conversationColumns

	sqlConversationByID = `SELECT ` + conversationColumns + `
		FROM group_conversations WHERE id = $1`

	sqlConversationByIDForUpdate = `SELECT ` + conversationColumns + `
		FROM group_conversations WHERE id = $1 FOR UPDATE`

	// 我的群列表：有效成员 + 未隐藏（hidden_message_id 为空，或群最后消息已越过
	// 隐藏指针）+ 群有消息；按群最后消息倒序分页。
	sqlConversationsByUser = `SELECT ` + conversationColumnsAliased + `
		FROM group_conversations g
		JOIN group_members m ON m.group_id = g.id
		WHERE m.user_id = $1 AND m.left_at IS NULL
		  AND g.last_message_id IS NOT NULL
		  AND (m.hidden_message_id IS NULL OR g.last_message_id > m.hidden_message_id)
		ORDER BY g.last_message_id DESC
		LIMIT $2 OFFSET $3`

	sqlConversationsByUserAfterCursor = `SELECT ` + conversationColumnsAliased + `
		FROM group_conversations g
		JOIN group_members m ON m.group_id = g.id
		WHERE m.user_id = $1 AND m.left_at IS NULL
		  AND g.last_message_id IS NOT NULL AND g.last_message_id < $2
		  AND (m.hidden_message_id IS NULL OR g.last_message_id > m.hidden_message_id)
		ORDER BY g.last_message_id DESC
		LIMIT $3`

	// 发送后推进群最后消息指针；解散群不推进（消息流已冻结）。
	sqlConversationTouchOnSend = `UPDATE group_conversations
		SET last_message_id = $2, updated_at = $3
		WHERE id = $1 AND status = 'active'`

	// 群主操作时间戳（改名/转让/解散共用 updated_at 维护）。
	sqlConversationRename = `UPDATE group_conversations
		SET name = $2, updated_at = $3
		WHERE id = $1 AND status = 'active'`

	sqlConversationTransfer = `UPDATE group_conversations
		SET owner_id = $2, updated_at = $3
		WHERE id = $1 AND status = 'active' AND owner_id = $4`

	sqlConversationDissolve = `UPDATE group_conversations
		SET status = 'dissolved', updated_at = $2
		WHERE id = $1 AND status = 'active'`
)

// Insert 创建群（创建入口，非幂等；并发由上游限频与用户维度唯一性约束承担）。
func (r *ConversationRepo) Insert(ctx context.Context, group model.GroupConversation) (model.GroupConversation, error) {
	inserted, err := scanConversation(db.ExecutorFrom(ctx, r.pool).QueryRow(ctx, sqlConversationInsert,
		group.ID, group.Name, group.OwnerID, group.Status, group.CreatedAt))
	return inserted, notFound(err, "群聊不存在")
}

// GetByID 按 ID 读取群；不存在返回领域 NotFound（含已解散群，详情页展示只读态）。
func (r *ConversationRepo) GetByID(ctx context.Context, id int64) (model.GroupConversation, error) {
	group, err := scanConversation(db.ExecutorFrom(ctx, r.pool).QueryRow(ctx, sqlConversationByID, id))
	return group, notFound(err, "群聊不存在")
}

// GetForUpdate 在调用方事务内读取群并加行锁。
func (r *ConversationRepo) GetForUpdate(ctx context.Context, id int64) (model.GroupConversation, error) {
	group, err := scanConversation(db.ExecutorFrom(ctx, r.pool).QueryRow(ctx, sqlConversationByIDForUpdate, id))
	return group, notFound(err, "群聊不存在")
}

// ListByUser 返回用户可见（未隐藏、有消息、未退出）的群，按最后消息倒序；
// cursor=0 为首页，否则按 last_message_id 游标续页。
func (r *ConversationRepo) ListByUser(ctx context.Context, userID, cursor int64, limit int) ([]model.GroupConversation, error) {
	exec := db.ExecutorFrom(ctx, r.pool)
	var (
		rows pgx.Rows
		err  error
	)
	if cursor > 0 {
		rows, err = exec.Query(ctx, sqlConversationsByUserAfterCursor, userID, cursor, limit)
	} else {
		rows, err = exec.Query(ctx, sqlConversationsByUser, userID, limit, 0)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := make([]model.GroupConversation, 0, limit)
	for rows.Next() {
		group, err := scanConversation(rows)
		if err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

// TouchOnSend 在发送事务内推进群最后消息指针（仅 active 群）。
func (r *ConversationRepo) TouchOnSend(ctx context.Context, id, messageID int64, at time.Time) error {
	_, err := db.ExecutorFrom(ctx, r.pool).Exec(ctx, sqlConversationTouchOnSend, id, messageID, at)
	return err
}

// Rename 群主改名（仅 active 群）。
func (r *ConversationRepo) Rename(ctx context.Context, id int64, name string, at time.Time) error {
	_, err := db.ExecutorFrom(ctx, r.pool).Exec(ctx, sqlConversationRename, id, name, at)
	return err
}

// Transfer 转让群主（仅现任群主与 active 群生效，返回是否发生变更）。
func (r *ConversationRepo) Transfer(ctx context.Context, id, newOwnerID, currentOwnerID int64, at time.Time) (bool, error) {
	tag, err := db.ExecutorFrom(ctx, r.pool).Exec(ctx, sqlConversationTransfer, id, newOwnerID, at, currentOwnerID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// Dissolve 解散群（仅 active 生效，重复解散幂等返回 false）。
func (r *ConversationRepo) Dissolve(ctx context.Context, id int64, at time.Time) (bool, error) {
	tag, err := db.ExecutorFrom(ctx, r.pool).Exec(ctx, sqlConversationDissolve, id, at)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func scanConversation(row rowScanner) (model.GroupConversation, error) {
	var group model.GroupConversation
	err := row.Scan(
		&group.ID, &group.Name, &group.OwnerID, &group.LastMessageID,
		&group.Status, &group.CreatedAt, &group.UpdatedAt,
	)
	return group, err
}
