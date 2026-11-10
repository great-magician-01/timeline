package app

import (
	"context"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/media/domain"
	"github.com/great-magician-01/timeline/internal/modules/media/repo"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// ===== contracts.MediaService 的读/校验方法（供 content/admin 调用） =====

// AttachmentsByIDs 批量读取附件行（含已逻辑删除记录，由调用方按 Available 判断）。
func (s *Service) AttachmentsByIDs(ctx context.Context, attachmentIDs []int64) (map[int64]model.Attachment, error) {
	rows, err := repo.ListByIDs(ctx, s.exec(ctx), attachmentIDs)
	if err != nil {
		return nil, s.internal("批量读取附件失败", err)
	}
	result := make(map[int64]model.Attachment, len(rows))
	for id, row := range rows {
		result[id] = row.Attachment
	}
	return result, nil
}

// DerivativeStatuses 批量读取衍生文件状态：attachment_id → derivative_type → status。
func (s *Service) DerivativeStatuses(ctx context.Context, attachmentIDs []int64) (map[int64]map[string]string, error) {
	result, err := repo.StatusesByAttachmentIDs(ctx, s.exec(ctx), attachmentIDs)
	if err != nil {
		return nil, s.internal("批量读取衍生文件状态失败", err)
	}
	return result, nil
}

// CheckAttachmentsUsable 校验附件存在、有效、用途匹配且属于指定作者。
// 消费者：content（发帖/改稿前校验）。不合格返回 validation_failed + 字段详情。
func (s *Service) CheckAttachmentsUsable(ctx context.Context, in contracts.AttachmentCheckInput) error {
	if len(in.AttachmentIDs) == 0 {
		return nil
	}
	purpose := in.Purpose
	if purpose == "" {
		purpose = domain.PurposePost
	}
	rows, err := repo.ListByIDs(ctx, s.exec(ctx), in.AttachmentIDs)
	if err != nil {
		return s.internal("校验附件可用性失败", err)
	}
	invalid := apperr.Validation("附件不可用").
		WithDetails(map[string]any{"attachment_ids": "附件不存在、不属于当前账号或已失效"})
	for _, id := range in.AttachmentIDs {
		row, ok := rows[id]
		if !ok || row.UploaderID != in.UploaderID || !row.Available() || row.Purpose != purpose {
			return invalid
		}
	}
	// 非公开文章不能新引入公开网盘文件。既有引用允许在同事务内由 content 撤销公开，
	// 从而支持文章从公开下调为仅关注者/仅自己，而不是把降级操作卡死。
	if in.PostVisibility != "" && in.PostVisibility != model.VisibilityPublic {
		driveRows, err := repo.DriveFilesForUpdate(ctx, s.exec(ctx), in.AttachmentIDs)
		if err != nil {
			return s.internal("锁定网盘附件公开状态失败", err)
		}
		existing := make(map[int64]struct{}, len(in.ExistingAttachmentIDs))
		for _, id := range in.ExistingAttachmentIDs {
			existing[id] = struct{}{}
		}
		for _, id := range in.AttachmentIDs {
			row, ok := driveRows[id]
			if !ok || row.Status != "active" || row.Visibility != domain.DriveVisibilityPublic {
				continue
			}
			if _, ok := existing[id]; !ok {
				return apperr.Conflict("drive_visibility_conflict", "公开网盘文件不能加入非公开文章").
					WithDetails(driveConflictDetails([]int64{id}))
			}
		}
	}
	return nil
}

// LinkAttachmentsToPost 把一批附件绑定到帖子。消费者：content（发帖/发布时）。
// 调用方通常已在外层事务内，TxManager 会复用同一事务。
func (s *Service) LinkAttachmentsToPost(ctx context.Context, postID int64, attachmentIDs []int64, at time.Time) error {
	if len(attachmentIDs) == 0 {
		return nil
	}
	err := s.tx.WithTx(ctx, func(txCtx context.Context) error {
		return repo.LinkToPost(txCtx, s.exec(txCtx), postID, attachmentIDs, at)
	})
	if err != nil {
		if _, ok := apperr.AsKind(err); ok {
			return err
		}
		return s.internal("绑定附件到帖子失败", err)
	}
	return nil
}
