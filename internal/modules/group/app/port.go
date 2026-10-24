package app

import (
	"context"
	"time"

	"github.com/great-magician-01/timeline/internal/shared/model"
)

// IDGenerator 是雪花 ID 生成端口（platform/id 满足）。
type IDGenerator interface {
	Next() (int64, error)
}

// TxRunner 是事务入口（*db.TxManager 满足）。
type TxRunner interface {
	WithTx(ctx context.Context, fn func(context.Context) error) error
}

// ConversationRepo 是群会话仓储端口（实现：modules/group/repo.ConversationRepo）。
// 所有方法通过 platform/db.ExecutorFrom(ctx) 自动加入 app 编排的事务。
type ConversationRepo interface {
	Insert(ctx context.Context, group model.GroupConversation) (model.GroupConversation, error)
	// GetByID 按 ID 读取群；不存在返回领域 NotFound。
	GetByID(ctx context.Context, id int64) (model.GroupConversation, error)
	// GetForUpdate 在调用方事务内读取群并加行锁。
	GetForUpdate(ctx context.Context, id int64) (model.GroupConversation, error)
	// ListByUser 返回用户可见（未隐藏、有消息、未退出）的群，按最后消息倒序；cursor=0 为首页。
	ListByUser(ctx context.Context, userID, cursor int64, limit int) ([]model.GroupConversation, error)
	// TouchOnSend 在发送事务内推进群最后消息指针（仅 active 群生效）。
	TouchOnSend(ctx context.Context, id, messageID int64, at time.Time) error
	// Rename 群主改名（仅 active 群生效）。
	Rename(ctx context.Context, id int64, name string, at time.Time) error
	// Transfer 转让群主（仅现任群主与 active 群生效，返回是否发生变更）。
	Transfer(ctx context.Context, id, newOwnerID, currentOwnerID int64, at time.Time) (bool, error)
	// Dissolve 解散群（仅 active 生效，重复解散返回 changed=false）。
	Dissolve(ctx context.Context, id int64, at time.Time) (bool, error)
}

// MemberRepo 是群成员仓储端口（实现：modules/group/repo.MemberRepo）。
type MemberRepo interface {
	// Insert 插入成员行；唯一冲突（含已退出的旧行）返回 ok=false。
	Insert(ctx context.Context, member model.GroupMember) (model.GroupMember, bool, error)
	// Rejoin 复用既有行重新入群（重置退出标记与游标）；行不存在返回 ok=false。
	Rejoin(ctx context.Context, groupID, userID int64, role string, at time.Time) (model.GroupMember, bool, error)
	// GetByGroupAndUser 读取成员行（含已退出的历史行）；不存在返回领域 NotFound。
	GetByGroupAndUser(ctx context.Context, groupID, userID int64) (model.GroupMember, error)
	// GetForUpdate 在调用方事务内读取成员行并加行锁；不存在返回领域 NotFound。
	GetForUpdate(ctx context.Context, groupID, userID int64) (model.GroupMember, error)
	// ListByGroup 返回群的有效成员分页（按加入时间正序）；cursorID=0 为首页。
	ListByGroup(ctx context.Context, groupID int64, cursorJoinedAt time.Time, cursorID int64, limit int) ([]model.GroupMember, error)
	// MembershipsByUser 返回用户在全部群的成员行（含隐藏的），供列表批量组装。
	MembershipsByUser(ctx context.Context, userID int64) ([]model.GroupMember, error)
	// CountActive 返回群的有效成员数。
	CountActive(ctx context.Context, groupID int64) (int, error)
	// GroupIDsByUser 返回用户全部有效成员的群 ID（实时信号批量目标）。
	GroupIDsByUser(ctx context.Context, userID int64) ([]int64, error)
	// OwnedGroupCount 返回用户当前持有的 active 群主名额数。
	OwnedGroupCount(ctx context.Context, userID int64) (int, error)
	// Leave 置 left_at（退出/被移除）；非有效成员返回 changed=false。
	Leave(ctx context.Context, groupID, userID int64, at time.Time) (bool, error)
	// SetRole 更新成员角色（群主转让）。
	SetRole(ctx context.Context, groupID, userID int64, role string) error
	// MarkRead 向前推进已读游标；游标已更新时幂等返回 changed=false。
	MarkRead(ctx context.Context, groupID, userID, messageID int64) (bool, error)
	// Hide 把会话隐藏指针拨到指定消息位置。
	Hide(ctx context.Context, groupID, userID, messageID int64) error
}

// MessageRepo 是群消息仓储端口（实现：modules/group/repo.MessageRepo）。
type MessageRepo interface {
	Insert(ctx context.Context, msg model.GroupMessage) (model.GroupMessage, error)
	// GetForUpdate 在调用方事务内读取消息并加行锁；不存在返回领域 NotFound。
	GetForUpdate(ctx context.Context, id int64) (model.GroupMessage, error)
	// ByIDs 批量读取消息（群列表预览）；缺失 ID 不在 map 中。
	ByIDs(ctx context.Context, ids []int64) (map[int64]model.GroupMessage, error)
	// List 返回群内消息（id DESC 翻旧）；cursor=0 为最新一页。
	List(ctx context.Context, groupID, cursor int64, limit int) ([]model.GroupMessage, error)
	// ListAfter 返回 afterID 之后的新消息（id ASC），供增量轮询。
	ListAfter(ctx context.Context, groupID, afterID int64, limit int) ([]model.GroupMessage, error)
	// ListBefore 返回 beforeID 之前的消息（id DESC），供 AI 上下文回看。
	ListBefore(ctx context.Context, groupID, beforeID int64, limit int) ([]model.GroupMessage, error)
	// Recall 撤回发送者本人的已发布消息；重复撤回返回 changed=false。
	Recall(ctx context.Context, id, senderID int64, at time.Time) (bool, error)
	// UnreadCount 返回成员在单个群内的未读数（游标之后、非本人、未撤回）。
	UnreadCount(ctx context.Context, groupID, userID int64, lastReadMessageID *int64) (int, error)
	// UnreadByGroups 按成员游标批量统计用户在全部群的未读数。
	UnreadByGroups(ctx context.Context, userID int64) (map[int64]int, error)
}
