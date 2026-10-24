package contracts

import (
	"context"
	"time"
)

// GroupMentionSignal 是群消息提及成员 AI 的触发信号。
// 消费者：agent（投递被 @ 回复任务）；生产者：group app 在消息提交后发出。
type GroupMentionSignal struct {
	AIID      int64
	GroupID   int64
	MessageID int64
	// MentionedBy 是提及该 AI 的消息发送者（提示词中的 @ 者）。
	MentionedBy int64
}

// GroupAITrigger 把群内 @提及 转交 agent 模块投递 AI 回复任务。
//
// 消费者：group app（群消息提交后经 db.AfterCommit 调用）；
// 实现方：agent app（组合根注入；构造环用惰性端口解开的同现有模式）。
// 实现方自行保证幂等（重复信号不重复投递）；AI 未启用被动回复、非成员等
// 场景直接返回 nil（无任务产生）。nil 实现安全（group app 判空跳过）。
type GroupAITrigger interface {
	OnGroupMention(ctx context.Context, signal GroupMentionSignal) error
}

// GroupContextMessage 是注入 AI 群聊上下文的单条消息（仅未撤回正文）。
type GroupContextMessage struct {
	MessageID int64
	SenderID  int64
	PlainText string
	CreatedAt time.Time
}

// GroupSnapshot 是 agent 组装群聊上下文所需的最小只读视图。
type GroupSnapshot struct {
	GroupID int64
	Name    string
	Active  bool
	// RecentMessages 按时间升序返回最近消息（不含已撤回正文）。
	RecentMessages []GroupContextMessage
}

// GroupReader 提供 AI 群聊任务所需的群只读能力。
//
// 消费者：agent（被 @ 回复与自主发言任务的上下文组装、执行时成员资格复核）；
// 实现方：group app（组合根注入）。
type GroupReader interface {
	// IsMemberActive 判断 userID 是否是群的有效成员且群未解散。
	IsMemberActive(ctx context.Context, groupID, userID int64) (bool, error)
	// Snapshot 返回群名、状态与最近 limit 条消息（beforeID>0 时只取其之前的
	// 更早消息，用于回看更长上下文）；群不存在返回领域 NotFound。
	Snapshot(ctx context.Context, groupID, beforeID int64, limit int) (GroupSnapshot, error)
	// ListActiveGroupIDs 返回 userID 当前有效成员且未解散的群 ID（自主发言候选）。
	ListActiveGroupIDs(ctx context.Context, userID int64) ([]int64, error)
}

// GroupMessageInput 是 AI 代发群消息的输入；ContentFormat/ContentSource 约定
// 与人类发送一致（richtext 段落 HTML），代发走与人类相同的校验、清洗与限频。
type GroupMessageInput struct {
	GroupID       int64
	SenderID      int64 // 发送的 AI 账号
	ContentFormat string
	ContentSource string
}

// GroupMessageWriter 是 AI 代发群消息端口。
//
// 消费者：agent（任务产出落库）；实现方：group app（组合根注入）。
// AI 已被移出群、群已解散或账号停用时返回领域错误，任务按失败处理。
type GroupMessageWriter interface {
	WriteFromAI(ctx context.Context, input GroupMessageInput) (messageID int64, err error)
}
