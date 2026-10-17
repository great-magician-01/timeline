package app

import (
	"context"

	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// DeletePost 管理员逻辑删除帖子；审计由 content 模块在同一事务内写入
// （contracts.ContentService.DeletePostAsAdmin 的契约）。
func (s *Service) DeletePost(ctx context.Context, actor model.Viewer, postID int64) error {
	now := s.now()
	return s.tx.WithTx(ctx, func(txCtx context.Context) error {
		return s.content.DeletePostAsAdmin(txCtx, contracts.DeletePostInput{
			AdminID: actor.ID,
			PostID:  postID,
			At:      now,
		})
	})
}

// DeleteComment 管理员逻辑删除评论；通知/提及失效与审计由 social 模块在同一事务内完成。
func (s *Service) DeleteComment(ctx context.Context, actor model.Viewer, commentID int64) error {
	now := s.now()
	return s.tx.WithTx(ctx, func(txCtx context.Context) error {
		return s.social.DeleteCommentAsAdmin(txCtx, contracts.DeleteCommentInput{
			ActorID:   actor.ID,
			CommentID: commentID,
			ByAdmin:   true,
			At:        now,
		})
	})
}

// DeleteAttachment 管理员逻辑删除附件；引用规则与审计由 media 模块在同一事务内完成。
func (s *Service) DeleteAttachment(ctx context.Context, actor model.Viewer, attachmentID int64) error {
	now := s.now()
	return s.tx.WithTx(ctx, func(txCtx context.Context) error {
		return s.media.DeleteAttachmentAsAdmin(txCtx, contracts.DeleteAttachmentInput{
			AdminID:      actor.ID,
			AttachmentID: attachmentID,
			At:           now,
		})
	})
}
