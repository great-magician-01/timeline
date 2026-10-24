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

// MemberRepo 实现 app.MemberRepo（群成员资格）。
type MemberRepo struct {
	pool *pgxpool.Pool
}

// NewMemberRepo 构造群成员仓储；pool 由组合根注入。
func NewMemberRepo(pool *pgxpool.Pool) *MemberRepo { return &MemberRepo{pool: pool} }

// memberColumns 是 group_members 的完整行投影，顺序与 scanMember 一致。
const memberColumns = `id, group_id, user_id, role, last_read_message_id, hidden_message_id, joined_at, left_at`

const (
	// 拉人/受让复用同一行：UNIQUE (group_id, user_id) 冲突时不插入，由调用方
	// 重读既有行判断「已是成员」还是「重新入群」（重置 left_at/角色）。
	sqlMemberInsert = `INSERT INTO group_members (id, group_id, user_id, role, joined_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (group_id, user_id) DO NOTHING
		RETURNING ` + memberColumns

	// 重新入群：重置退出标记与已读/隐藏游标，并按需刷新角色（群主转让回退场景
	// 由 app 层显式控制，这里只按传入角色重置普通成员）。
	sqlMemberRejoin = `UPDATE group_members
		SET role = $3, left_at = NULL, last_read_message_id = NULL, hidden_message_id = NULL, joined_at = $4
		WHERE group_id = $1 AND user_id = $2
		RETURNING ` + memberColumns

	sqlMemberByGroupAndUser = `SELECT ` + memberColumns + `
		FROM group_members WHERE group_id = $1 AND user_id = $2`

	sqlMemberByGroupAndUserForUpdate = `SELECT ` + memberColumns + `
		FROM group_members WHERE group_id = $1 AND user_id = $2 FOR UPDATE`

	// 成员分页（有效成员按加入时间正序）。
	sqlMembersFirstPage = `SELECT ` + memberColumns + `
		FROM group_members WHERE group_id = $1 AND left_at IS NULL
		ORDER BY joined_at ASC, id ASC LIMIT $2`
	sqlMembersAfterCursor = `SELECT ` + memberColumns + `
		FROM group_members WHERE group_id = $1 AND left_at IS NULL
		  AND (joined_at, id) > ($2, $3)
		ORDER BY joined_at ASC, id ASC LIMIT $4`

	// 我的群列表需要成员行的游标与未读信息，一次取回。
	sqlMembershipsByUser = `SELECT ` + memberColumns + `
		FROM group_members WHERE user_id = $1 AND left_at IS NULL`

	sqlMemberCount = `SELECT COUNT(*)
		FROM group_members WHERE group_id = $1 AND left_at IS NULL`

	sqlGroupIDsByUser = `SELECT group_id
		FROM group_members WHERE user_id = $1 AND left_at IS NULL`

	sqlOwnedGroupCount = `SELECT COUNT(*)
		FROM group_conversations WHERE owner_id = $1 AND status = 'active'`

	// 退出/被移除只置 left_at（逻辑删除；重新拉人复用同一行）。
	sqlMemberLeave = `UPDATE group_members
		SET left_at = $3
		WHERE group_id = $1 AND user_id = $2 AND left_at IS NULL`

	// 移交群主：新群主角色置 owner，原群主角色置 member（同一事务两条语句）。
	sqlMemberSetRole = `UPDATE group_members
		SET role = $3
		WHERE group_id = $1 AND user_id = $2 AND left_at IS NULL`

	// 已读推进：只向前推进游标（旧消息不会把游标倒退），幂等。
	sqlMemberMarkRead = `UPDATE group_members
		SET last_read_message_id = $3
		WHERE group_id = $1 AND user_id = $2 AND left_at IS NULL
		  AND (last_read_message_id IS NULL OR last_read_message_id < $3)`

	// 隐藏会话：把隐藏指针拨到群当前最后消息；新消息到达后自动重新出现。
	sqlMemberHide = `UPDATE group_members
		SET hidden_message_id = $3
		WHERE group_id = $1 AND user_id = $2 AND left_at IS NULL`
)

// Insert 插入成员行；已存在（含已退出的旧行）时返回 ok=false，由调用方决定重读。
func (r *MemberRepo) Insert(ctx context.Context, member model.GroupMember) (model.GroupMember, bool, error) {
	inserted, err := scanMember(db.ExecutorFrom(ctx, r.pool).QueryRow(ctx, sqlMemberInsert,
		member.ID, member.GroupID, member.UserID, member.Role, member.JoinedAt))
	if err == nil {
		return inserted, true, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return model.GroupMember{}, false, nil
	}
	return model.GroupMember{}, false, err
}

// Rejoin 复用既有行重新入群（重置退出标记与游标）；行不存在返回 ok=false。
func (r *MemberRepo) Rejoin(ctx context.Context, groupID, userID int64, role string, at time.Time) (model.GroupMember, bool, error) {
	row, err := scanMember(db.ExecutorFrom(ctx, r.pool).QueryRow(ctx, sqlMemberRejoin, groupID, userID, role, at))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return model.GroupMember{}, false, nil
		}
		return model.GroupMember{}, false, err
	}
	return row, true, nil
}

// GetByGroupAndUser 读取成员行（含已退出的历史行）；不存在返回领域 NotFound。
func (r *MemberRepo) GetByGroupAndUser(ctx context.Context, groupID, userID int64) (model.GroupMember, error) {
	member, err := scanMember(db.ExecutorFrom(ctx, r.pool).QueryRow(ctx, sqlMemberByGroupAndUser, groupID, userID))
	return member, notFound(err, "群聊不存在")
}

// GetForUpdate 在调用方事务内读取成员行并加行锁；不存在返回领域 NotFound。
func (r *MemberRepo) GetForUpdate(ctx context.Context, groupID, userID int64) (model.GroupMember, error) {
	member, err := scanMember(db.ExecutorFrom(ctx, r.pool).QueryRow(ctx, sqlMemberByGroupAndUserForUpdate, groupID, userID))
	return member, notFound(err, "群聊不存在")
}

// ListByGroup 返回群的有效成员分页（按加入时间正序）；cursor 为 (joinedAt, id) 复合。
func (r *MemberRepo) ListByGroup(ctx context.Context, groupID int64, cursorJoinedAt time.Time, cursorID int64, limit int) ([]model.GroupMember, error) {
	exec := db.ExecutorFrom(ctx, r.pool)
	var (
		rows pgx.Rows
		err  error
	)
	if cursorID > 0 {
		rows, err = exec.Query(ctx, sqlMembersAfterCursor, groupID, cursorJoinedAt, cursorID, limit)
	} else {
		rows, err = exec.Query(ctx, sqlMembersFirstPage, groupID, limit)
	}
	if err != nil {
		return nil, err
	}
	return collectMembers(rows, limit)
}

// MembershipsByUser 返回用户在全部群的成员行（含隐藏的），供列表批量组装。
func (r *MemberRepo) MembershipsByUser(ctx context.Context, userID int64) ([]model.GroupMember, error) {
	rows, err := db.ExecutorFrom(ctx, r.pool).Query(ctx, sqlMembershipsByUser, userID)
	if err != nil {
		return nil, err
	}
	return collectMembers(rows, 0)
}

// CountActive 返回群的有效成员数。
func (r *MemberRepo) CountActive(ctx context.Context, groupID int64) (int, error) {
	var count int
	err := db.ExecutorFrom(ctx, r.pool).QueryRow(ctx, sqlMemberCount, groupID).Scan(&count)
	return count, err
}

// GroupIDsByUser 返回用户全部有效成员的群 ID。
func (r *MemberRepo) GroupIDsByUser(ctx context.Context, userID int64) ([]int64, error) {
	rows, err := db.ExecutorFrom(ctx, r.pool).Query(ctx, sqlGroupIDsByUser, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]int64, 0, 8)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// OwnedGroupCount 返回用户当前持有的 active 群主名额数。
func (r *MemberRepo) OwnedGroupCount(ctx context.Context, userID int64) (int, error) {
	var count int
	err := db.ExecutorFrom(ctx, r.pool).QueryRow(ctx, sqlOwnedGroupCount, userID).Scan(&count)
	return count, err
}

// Leave 置 left_at（退出/被移除）；非有效成员返回 changed=false。
func (r *MemberRepo) Leave(ctx context.Context, groupID, userID int64, at time.Time) (bool, error) {
	tag, err := db.ExecutorFrom(ctx, r.pool).Exec(ctx, sqlMemberLeave, groupID, userID, at)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// SetRole 更新成员角色（群主转让）。
func (r *MemberRepo) SetRole(ctx context.Context, groupID, userID int64, role string) error {
	_, err := db.ExecutorFrom(ctx, r.pool).Exec(ctx, sqlMemberSetRole, groupID, userID, role)
	return err
}

// MarkRead 向前推进已读游标；游标已更新时幂等返回 false。
func (r *MemberRepo) MarkRead(ctx context.Context, groupID, userID, messageID int64) (bool, error) {
	tag, err := db.ExecutorFrom(ctx, r.pool).Exec(ctx, sqlMemberMarkRead, groupID, userID, messageID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// Hide 把会话隐藏指针拨到指定消息位置。
func (r *MemberRepo) Hide(ctx context.Context, groupID, userID, messageID int64) error {
	_, err := db.ExecutorFrom(ctx, r.pool).Exec(ctx, sqlMemberHide, groupID, userID, messageID)
	return err
}

func collectMembers(rows pgx.Rows, limit int) ([]model.GroupMember, error) {
	defer rows.Close()
	capacity := limit
	if capacity < 16 {
		capacity = 16
	}
	members := make([]model.GroupMember, 0, capacity)
	for rows.Next() {
		member, err := scanMember(rows)
		if err != nil {
			return nil, err
		}
		members = append(members, member)
	}
	return members, rows.Err()
}

func scanMember(row rowScanner) (model.GroupMember, error) {
	var member model.GroupMember
	err := row.Scan(
		&member.ID, &member.GroupID, &member.UserID, &member.Role,
		&member.LastReadMessageID, &member.HiddenMessageID,
		&member.JoinedAt, &member.LeftAt,
	)
	return member, err
}
