package app

import (
	"context"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/access"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// ReferencedAttachmentIDs 是 media 清理/网盘删除的批量引用查询端口。
func (s *Service) ReferencedAttachmentIDs(ctx context.Context, attachmentIDs []int64) ([]int64, error) {
	ids, err := s.repo.ReferencedAttachmentIDs(ctx, attachmentIDs)
	if err != nil {
		return nil, s.wrap(err, "批量读取附件内容引用失败")
	}
	return ids, nil
}

// HasNonPublicAttachmentReference 是 media 公开文件前的引用约束查询端口。
func (s *Service) HasNonPublicAttachmentReference(ctx context.Context, attachmentID int64) (bool, error) {
	restricted, err := s.repo.HasNonPublicAttachmentReference(ctx, attachmentID)
	if err != nil {
		return false, s.wrap(err, "检查附件非公开内容引用失败")
	}
	return restricted, nil
}

// CanViewAttachment 判断访问者能否通过任一当前可见帖子读取附件。
// 关注集合最多读取一次；不可见帖子按普通 404 跳过，不泄露其存在性。
func (s *Service) CanViewAttachment(ctx context.Context, viewer model.Viewer, attachmentID int64) (bool, error) {
	posts, err := s.repo.AttachmentPosts(ctx, attachmentID)
	if err != nil {
		return false, s.wrap(err, "读取附件所属帖子失败")
	}
	if len(posts) == 0 {
		return false, nil
	}
	following, err := s.followingSet(ctx, viewer)
	if err != nil {
		return false, err
	}
	for _, post := range posts {
		if err := access.RequireVisiblePost(viewer, post, following); err == nil {
			return true, nil
		} else if !apperr.IsKind(err, apperr.KindNotFound) {
			return false, err
		}
	}
	return false, nil
}

// DetachAttachment 精确解除一个帖子草稿工作区中的附件，不影响网盘或其他帖子。
// 已发布帖子的当前版本不会因单独解除草稿关联而改写，仍由后续保存生成新版本。
func (s *Service) DetachAttachment(ctx context.Context, viewer model.Viewer, postID, attachmentID int64) (bool, error) {
	if err := s.requireWritableViewer(viewer); err != nil {
		return false, err
	}
	var detached bool
	err := s.withTx(ctx, func(txCtx context.Context) error {
		post, err := s.repo.GetPostForUpdate(txCtx, postID)
		if err != nil {
			return err
		}
		if post.Status == model.PostStatusDeleted {
			return apperr.NotFound("内容不存在或无权访问")
		}
		if _, err := s.requireAuthor(txCtx, viewer, post); err != nil {
			return err
		}
		detached, err = s.repo.DetachAttachmentFromDraft(txCtx, postID, attachmentID)
		if err != nil {
			return s.wrap(err, "解除帖子附件关联失败")
		}
		return nil
	})
	return detached, err
}
