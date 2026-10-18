package app

import (
	"context"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/notice/domain"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// IDGenerator 生成雪花 ID（platform/id.Generator 满足）。
type IDGenerator interface {
	Next() (int64, error)
}

// CommentReader 是评论批量读取窄端口；方法签名与 contracts.SocialService.GetComments
// 完全一致，由 social app 结构化实现（组合根注入），避免 notice 直接查 comments 表。
type CommentReader interface {
	// GetComments 批量读取评论（含已删除评论，由调用方按状态处理）；缺失 ID 不在 map 中。
	GetComments(ctx context.Context, commentIDs []int64) (map[int64]model.Comment, error)
}

// NotificationRepo 是 notifications 表 SQL 端口。
type NotificationRepo interface {
	// Insert 写入通知；幂等键冲突时静默忽略。
	Insert(ctx context.Context, row model.Notification) error
	// List 按接收者分页（ID 倒序）；filter 为 all/unread，noticeType 为空表示不过滤。
	List(ctx context.Context, recipientID int64, filter, noticeType string, cursor int64, limit int) ([]model.Notification, error)
	UnreadCount(ctx context.Context, recipientID int64) (int64, error)
	// MarkRead 标记单条已读；不存在或不属于该接收者时返回 false。
	MarkRead(ctx context.Context, recipientID, id int64) (bool, error)
	// MarkAllRead 标记全部未读并返回更新行数。
	MarkAllRead(ctx context.Context, recipientID int64) (int64, error)
}

// MentionRow 是 mentions 行读取结构（定义在 domain，repo 不依赖 app）。
// 保留 app 包别名，既有 app 内引用无需改动。
type MentionRow = domain.MentionRow

// MentionRepo 是 mentions 表 SQL 端口。
type MentionRepo interface {
	// DeactivateExcept 停用来源对象上不在 keepUserIDs 中的提及。
	DeactivateExcept(ctx context.Context, sourceType string, sourceID int64, keepUserIDs []int64) error
	// Upsert 建链或恢复提及记录。
	Upsert(ctx context.Context, id int64, sourceType string, sourceID, userID int64, now time.Time) error
	// DeactivateAll 停用来源对象上的全部提及。
	DeactivateAll(ctx context.Context, sourceType string, sourceID int64) error
	// ListActive since 之后某用户生效中的提及（(created_at, id) 倒序）。
	ListActive(ctx context.Context, userID int64, since time.Time, limit int) ([]MentionRow, error)
	// GetActive 读取单条生效提及；不存在返回领域 not_found。
	GetActive(ctx context.Context, mentionID int64) (MentionRow, error)
}
