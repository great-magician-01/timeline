package contracts

import (
	"context"
	"time"

	"github.com/great-magician-01/timeline/internal/shared/model"
)

// AttachmentCheckInput 校验一批附件是否可由指定作者用于发帖（归属/可用性/用途）。
// 消费者：content（创建/更新/发布帖子前校验附件归属与用途）。
type AttachmentCheckInput struct {
	UploaderID    int64
	AttachmentIDs []int64
	Purpose       string // "post" / "avatar"
}

// DeleteAttachmentInput 管理员逻辑删除附件（引用规则由 media 实现，审计同事务）。
// 消费者：admin（附件审核删除）。
type DeleteAttachmentInput struct {
	AdminID      int64
	AttachmentID int64
	At           time.Time
}

// MediaService 是 media 模块对外发布的全部跨模块能力。
//
// 消费者：content（附件校验/读取/衍生状态）、admin（附件处置）。
// 实现方：modules/media/app 的 App 类型（组合根注入）。
type MediaService interface {
	// CheckAttachmentsUsable 校验附件归属、可用状态与用途；不合格返回领域错误。
	// 消费者：content。
	CheckAttachmentsUsable(ctx context.Context, in AttachmentCheckInput) error

	// AttachmentsByIDs 批量读取附件行（含已逻辑删除记录，由调用方按 Available 判断）。
	// 消费者：content（帖子详情/版本快照 DTO）。
	AttachmentsByIDs(ctx context.Context, attachmentIDs []int64) (map[int64]model.Attachment, error)

	// DerivativeStatuses 批量读取衍生文件状态：attachment_id → derivative_type → status。
	// 消费者：content（附件 DTO 缩略图/封面地址）。
	DerivativeStatuses(ctx context.Context, attachmentIDs []int64) (map[int64]map[string]string, error)

	// LinkAttachmentsToPost 把附件绑定到已发布/草稿帖子（UPDATE attachments.post_id）。
	// 消费者：content（发布帖子时把草稿附件挂到帖子）。
	LinkAttachmentsToPost(ctx context.Context, postID int64, attachmentIDs []int64, at time.Time) error

	// DeleteAttachmentAsAdmin 管理员逻辑删除附件；实现方在同一事务内写审计。
	// 消费者：admin。
	DeleteAttachmentAsAdmin(ctx context.Context, in DeleteAttachmentInput) error
}
