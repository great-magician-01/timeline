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

// ConversationRepo 是私信会话仓储端口（实现：modules/dm/repo.ConversationRepo）。
// 所有方法通过 platform/db.ExecutorFrom(ctx) 自动加入 app 编排的事务。
type ConversationRepo interface {
	// Upsert 创建会话；已存在时返回既有行（幂等）。
	Upsert(ctx context.Context, conv model.DMConversation) (model.DMConversation, error)
	// GetByID 按 ID 读取会话；不存在返回领域 NotFound。
	GetByID(ctx context.Context, id int64) (model.DMConversation, error)
	// GetForUpdate 在调用方事务内读取会话并加行锁。
	GetForUpdate(ctx context.Context, id int64) (model.DMConversation, error)
	// ListByUser 返回未隐藏且有消息的会话，按最后消息倒序；cursor=0 为首页。
	ListByUser(ctx context.Context, userID, cursor int64, limit int) ([]model.DMConversation, error)
	// TouchOnSend 推进最后消息指针并清空双侧隐藏位。
	TouchOnSend(ctx context.Context, id, messageID int64, at time.Time) error
	// HideFor 只隐藏 userID 自己一侧的会话视图；非成员返回 ok=false。
	HideFor(ctx context.Context, id, userID int64, at time.Time) (ok bool, err error)
}

// MessageRepo 是私信消息仓储端口（实现：modules/dm/repo.MessageRepo）。
type MessageRepo interface {
	Insert(ctx context.Context, msg model.DMMessage) (model.DMMessage, error)
	// GetForUpdate 在调用方事务内读取消息并加行锁；不存在返回领域 NotFound。
	GetForUpdate(ctx context.Context, id int64) (model.DMMessage, error)
	// ByIDs 批量读取消息（会话列表预览）。
	ByIDs(ctx context.Context, ids []int64) (map[int64]model.DMMessage, error)
	// List 返回会话内消息（id DESC 翻旧）；cursor=0 为最新一页。
	List(ctx context.Context, conversationID, cursor int64, limit int) ([]model.DMMessage, error)
	// ListAfter 返回 afterID 之后的新消息（id ASC），供增量轮询。
	ListAfter(ctx context.Context, conversationID, afterID int64, limit int) ([]model.DMMessage, error)
	// MarkRead 把会话内接收方未读的已发布消息置为已读，返回更新条数。
	MarkRead(ctx context.Context, conversationID, recipientID int64, at time.Time) (int64, error)
	// Recall 撤回发送者本人的已发布消息；重复撤回返回 changed=false。
	Recall(ctx context.Context, id, senderID int64, at time.Time) (changed bool, err error)
	// UnreadCount 返回用户全部私信未读数（仅未撤回）。
	UnreadCount(ctx context.Context, recipientID int64) (int, error)
	// UnreadByConversations 批量返回用户在指定会话内的未读数。
	UnreadByConversations(ctx context.Context, recipientID int64, conversationIDs []int64) (map[int64]int, error)
}
