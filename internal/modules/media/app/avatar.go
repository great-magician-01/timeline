package app

import (
	"context"

	"github.com/great-magician-01/timeline/internal/modules/media/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/format"
)

// SetAvatar 把附件设为用户头像：校验附件归属与用途后经 auth 端口更新 users.avatar_url。
// 旧头像附件不做任何物理处理，仅由头像地址切换决定展示。
func (s *Service) SetAvatar(ctx context.Context, userID, attachmentID int64) error {
	row, err := s.getAttachment(ctx, attachmentID)
	if err != nil {
		return err
	}
	if !row.Available() {
		return apperr.NotFound("附件不存在或已失效")
	}
	if row.UploaderID != userID {
		return apperr.Forbidden("只能使用自己上传的头像附件")
	}
	if row.Purpose != domain.PurposeAvatar {
		return apperr.Validation("该附件不是头像用途，无法设为头像")
	}
	at := s.clk.Now()
	err = s.tx.WithTx(ctx, func(txCtx context.Context) error {
		return s.auth.SetAvatar(txCtx, contracts.AvatarUpdateInput{
			UserID:       userID,
			AttachmentID: attachmentID,
			AvatarURL:    format.AttachmentContentURL(attachmentID),
			At:           at,
		})
	})
	if err != nil {
		return s.wrap("更新头像失败", err)
	}
	return nil
}
