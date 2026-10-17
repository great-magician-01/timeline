package app

import (
	"context"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/media/repo"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
)

// DeleteResult 是附件失效结果：是否解除草稿关联、是否整体失效。
type DeleteResult struct {
	Detached bool
	Deleted  bool
}

// Delete 处理用户删除自己的附件（仅上传者本人）。
// 已被历史版本引用时只解除当前草稿关联，否则把附件整体置为失效；
// 数据库记录与物理文件始终保留。
func (s *Service) Delete(ctx context.Context, userID, attachmentID int64) (DeleteResult, error) {
	row, err := s.getAttachment(ctx, attachmentID)
	if err != nil {
		return DeleteResult{}, err
	}
	if !row.Available() {
		return DeleteResult{}, apperr.NotFound("附件不存在或已失效")
	}
	if row.UploaderID != userID {
		// 不向非上传者暴露附件是否存在。
		return DeleteResult{}, apperr.NotFound("附件不存在或无权操作")
	}
	at := s.clk.Now()
	var result DeleteResult
	err = s.tx.WithTx(ctx, func(txCtx context.Context) error {
		detached, deleted, err := s.invalidateAttachment(txCtx, attachmentID, at)
		if err != nil {
			return err
		}
		result = DeleteResult{Detached: detached, Deleted: deleted}
		return nil
	})
	if err != nil {
		return DeleteResult{}, err
	}
	return result, nil
}

// DeleteAttachmentAsAdmin 供管理后台处置附件（contracts.MediaService）：
// 跳过上传者校验，与用户删除一致的引用规则，并在同一事务内写审计。
func (s *Service) DeleteAttachmentAsAdmin(ctx context.Context, in contracts.DeleteAttachmentInput) error {
	err := s.tx.WithTx(ctx, func(txCtx context.Context) error {
		row, err := s.getAttachment(txCtx, in.AttachmentID)
		if err != nil {
			return err
		}
		if !row.Available() {
			return apperr.NotFound("附件不存在或已失效")
		}
		detached, deleted, err := s.invalidateAttachment(txCtx, in.AttachmentID, in.At)
		if err != nil {
			return err
		}
		targetID := in.AttachmentID
		if err := s.audit.Write(txCtx, contracts.AuditEntry{
			AdminID:    in.AdminID,
			Action:     contracts.AuditActionAttachmentDelete,
			TargetType: "attachment",
			TargetID:   &targetID,
			Detail:     map[string]any{"detached": detached, "deleted": deleted},
			At:         in.At,
		}); err != nil {
			return s.internal("写入附件删除审计失败", err)
		}
		s.log.Info("管理员处置附件",
			"admin_id", in.AdminID, "attachment_id", in.AttachmentID,
			"detached", detached, "deleted", deleted)
		return nil
	})
	return err
}

// invalidateAttachment 执行附件失效，返回 (是否解除了草稿关联, 是否整体失效)。
// 规则（docs/WORKFLOWS.md 6.3）：
//   - 先通过 content 端口解除当前草稿关联；
//   - 只要仍被任何历史版本引用，就保留附件可用状态，仅解除关联；
//   - 否则把附件置为 deleted，并取消尚未就绪的衍生文件；物理文件不删除。
func (s *Service) invalidateAttachment(ctx context.Context, attachmentID int64, at time.Time) (bool, bool, error) {
	detached, err := s.content.DetachAttachmentFromDrafts(ctx, attachmentID, at)
	if err != nil {
		return false, false, s.wrap("解除草稿附件关联失败", err)
	}
	referenced, err := s.content.AttachmentReferencedByVersion(ctx, attachmentID)
	if err != nil {
		return detached, false, s.wrap("检查附件历史版本引用失败", err)
	}
	if referenced {
		return detached, false, nil
	}
	deleted, err := repo.MarkDeleted(ctx, s.exec(ctx), attachmentID, at)
	if err != nil {
		return detached, false, s.internal("失效附件失败", err)
	}
	if !deleted {
		// 附件已失效（例如并发删除），无需重复处理。
		return detached, true, nil
	}
	if err := repo.CancelIncompleteForAttachment(ctx, s.exec(ctx), attachmentID, at); err != nil {
		return detached, true, s.internal("取消失效附件的衍生文件失败", err)
	}
	return detached, true, nil
}
