package contracts

import (
	"context"
	"time"
)

// NotificationInput 描述一次通知写入；IdempotencyKey 唯一标识一次事件。
// 消费者：content（引用通知）、social（关注/点赞/评论/转发/提及通知）。
//
// Type 取 model.NoticeType*，TargetType 取 "post"/"comment"/"user"。
type NotificationInput struct {
	IdempotencyKey string
	RecipientID    int64
	ActorID        int64
	Type           string
	TargetType     string
	TargetID       int64
	PostID         *int64
	CommentID      *int64
	At             time.Time
}

// PostNotificationsInput 在帖子发布时解析提及并写入提及通知（仅公开可见性）。
// 消费者：content（发布/重建派生投影）。
type PostNotificationsInput struct {
	PostID       int64
	AuthorID     int64
	PlainText    string
	MentionNames []string
	At           time.Time
}

// RebuildMentionsInput 按当前正文重建提及记录。
// 消费者：content（帖子）、social（评论）。
type RebuildMentionsInput struct {
	SourceType   string // model.NoticeSourcePost / model.NoticeSourceComment
	SourceID     int64
	PostID       int64
	MentionNames []string
	At           time.Time
}

// PublishMentionsInput 是一次「重建提及 + 写提及通知」的派生投影更新输入。
// 消费者：social（评论创建/编辑后的提及派生）。
type PublishMentionsInput struct {
	SourceType   string // model.NoticeSourcePost / model.NoticeSourceComment
	SourceID     int64
	PostID       int64
	AuthorID     int64
	PlainText    string
	MentionNames []string
	At           time.Time
}

// MentionNotificationsInput 为已发布的公开内容生成提及通知。
// 消费者：content（帖子）、social（评论）。
type MentionNotificationsInput struct {
	SourceType   string
	SourceID     int64
	PostID       int64
	AuthorID     int64
	MentionNames []string
	At           time.Time
}

// MentionQuery 读取某 AI 在指定时间后的生效提及（供调度器补投任务）。
// 消费者：agent（scanMentionTriggers）。
type MentionQuery struct {
	MentionedUserID int64
	Since           time.Time
	Limit           int
}

// MentionRef 是一条生效中的提及引用。消费者：agent（提及触发任务）。
type MentionRef struct {
	ID              int64
	MentionedUserID int64
	SourceType      string
	SourceID        int64
	CreatedAt       time.Time
}

// NoticeService 是 notice 模块对外发布的全部跨模块能力。
//
// 消费者：content、social、agent。
// 实现方：modules/notice/app 的 App 类型（组合根注入）。
type NoticeService interface {
	// CreateNotification 写入一条通知；接收者不存在/停用/AI 或幂等键重复时静默跳过。
	// 消费者：content、social。
	CreateNotification(ctx context.Context, in NotificationInput) error

	// PublishPostNotifications 在帖子发布时重建提及并写入提及通知。
	// 消费者：content。
	PublishPostNotifications(ctx context.Context, in PostNotificationsInput) error

	// RebuildMentions 按当前正文重建提及记录。消费者：content、social。
	RebuildMentions(ctx context.Context, in RebuildMentionsInput) error

	// PublishMentions 解析提及用户名一次后，同时重建提及记录并写入提及通知，
	// 避免调用方连续调用 RebuildMentions + MentionNotifications 造成重复解析与重复往返。
	// 消费者：social（评论创建/编辑）。
	PublishMentions(ctx context.Context, in PublishMentionsInput) error

	// MentionNotifications 为已发布公开内容生成提及通知。消费者：content、social。
	MentionNotifications(ctx context.Context, in MentionNotificationsInput) error

	// DeactivateMentions 内容转为非公开或删除时停用全部提及。
	// 消费者：content（帖子发布/删除/可见性变更）、social（评论删除）。
	DeactivateMentions(ctx context.Context, sourceType string, sourceID int64, at time.Time) error

	// ListActiveMentions 读取某用户指定时间后的生效提及。消费者：agent。
	ListActiveMentions(ctx context.Context, query MentionQuery) ([]MentionRef, error)

	// GetMention 读取单条生效提及；不存在返回领域 NotFound。消费者：agent。
	GetMention(ctx context.Context, mentionID int64) (MentionRef, error)
}
