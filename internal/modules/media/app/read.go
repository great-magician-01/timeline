package app

import (
	"context"
	"os"

	"github.com/great-magician-01/timeline/internal/modules/media/domain"
	"github.com/great-magician-01/timeline/internal/modules/media/repo"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// ContentFile 是原文件读取结果；File 由调用方（http 层）负责关闭。
type ContentFile struct {
	Attachment model.Attachment
	File       *os.File
}

// DerivativeFile 是衍生文件读取结果；File 由调用方（http 层）负责关闭。
type DerivativeFile struct {
	Attachment     model.Attachment
	DerivativeType string
	File           *os.File
}

// getAttachment 读取附件行；不存在返回领域 NotFound，其他错误包装为 500。
func (s *Service) getAttachment(ctx context.Context, attachmentID int64) (repo.AttachmentRow, error) {
	if attachmentID <= 0 {
		return repo.AttachmentRow{}, apperr.NotFound("附件不存在")
	}
	row, err := repo.Get(ctx, s.exec(ctx), attachmentID)
	if repo.IsNotFound(err) {
		return repo.AttachmentRow{}, apperr.NotFound("附件不存在")
	}
	if err != nil {
		return repo.AttachmentRow{}, s.internal("读取附件失败", err)
	}
	return row, nil
}

// requireAttachmentAccess 校验访问者能否读取附件内容（原文件与衍生文件共用）。
// 规则：purpose='avatar' 的头像附件与 purpose='banner' 的主页横幅附件公开可读
// （均为个人主页公开展示内容）；其余附件继承所属帖子的可见性；
// 尚未关联任何帖子的附件只有上传者本人与管理员可查看。不满足时返回 404，
// 不暴露资源存在性（docs/PAGES_AND_PERMISSIONS.md §8）。
func (s *Service) requireAttachmentAccess(ctx context.Context, viewer model.Viewer, row repo.AttachmentRow) error {
	if !row.Available() {
		return apperr.NotFound("附件不存在或已失效")
	}
	if row.Purpose == domain.PurposeAvatar || row.Purpose == domain.PurposeBanner {
		return nil
	}
	if row.PostID == nil {
		if viewer.IsAdmin || (viewer.Authenticated && viewer.ID == row.UploaderID) {
			return nil
		}
		return apperr.NotFound("附件不存在或无权访问")
	}
	if _, err := s.content.GetVisiblePost(ctx, viewer, *row.PostID); err != nil {
		if apperr.IsKind(err, apperr.KindNotFound) {
			return apperr.NotFound("附件不存在或无权访问")
		}
		return s.internal("校验附件所属帖子可见性失败", err)
	}
	return nil
}

// OpenContent 打开附件原文件：先做鉴权读取校验，再按存储键打开物理文件。
// 物理文件缺失时不泄露存储路径与内部错误，按不存在处理。
func (s *Service) OpenContent(ctx context.Context, viewer model.Viewer, attachmentID int64) (ContentFile, error) {
	row, err := s.getAttachment(ctx, attachmentID)
	if err != nil {
		return ContentFile{}, err
	}
	if err := s.requireAttachmentAccess(ctx, viewer, row); err != nil {
		return ContentFile{}, err
	}
	file, err := s.store.Open(row.StorageKey)
	if err != nil {
		s.log.Error("附件物理文件缺失", "attachment_id", row.ID, "err", err)
		return ContentFile{}, apperr.NotFound("附件不存在")
	}
	return ContentFile{Attachment: row.Attachment, File: file}, nil
}

// OpenDerivative 打开衍生文件：只有就绪且物理文件存在时才返回内容，否则统一 404，
// 由前端回退到原图或占位（docs/REQUIREMENTS.md 7.1.6）。
func (s *Service) OpenDerivative(ctx context.Context, viewer model.Viewer, attachmentID int64, derivativeType string) (DerivativeFile, error) {
	if !domain.ValidDerivativeType(derivativeType) {
		return DerivativeFile{}, apperr.NotFound("衍生文件不存在")
	}
	row, err := s.getAttachment(ctx, attachmentID)
	if err != nil {
		return DerivativeFile{}, err
	}
	// 衍生文件继承原文件与其所属内容的访问权限，不存在绕开鉴权的公开地址。
	if err := s.requireAttachmentAccess(ctx, viewer, row); err != nil {
		return DerivativeFile{}, err
	}
	derivative, err := repo.GetDerivative(ctx, s.exec(ctx), row.ID, derivativeType)
	if repo.IsNotFound(err) {
		return DerivativeFile{}, apperr.NotFound("衍生文件尚未生成")
	}
	if err != nil {
		return DerivativeFile{}, s.internal("读取衍生文件记录失败", err)
	}
	if derivative.Status != model.DerivativeReady || derivative.StorageKey == "" {
		return DerivativeFile{}, apperr.NotFound("衍生文件尚未生成")
	}
	file, err := s.store.Open(derivative.StorageKey)
	if err != nil {
		s.log.Error("衍生文件物理文件缺失",
			"attachment_id", row.ID, "type", derivativeType, "err", err)
		return DerivativeFile{}, apperr.NotFound("衍生文件不存在")
	}
	return DerivativeFile{
		Attachment:     row.Attachment,
		DerivativeType: derivativeType,
		File:           file,
	}, nil
}
