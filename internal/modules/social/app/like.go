package app

import (
	"context"

	"github.com/great-magician-01/timeline/internal/modules/social/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// LikePost 点赞帖子。
func (s *Service) LikePost(ctx context.Context, viewer model.Viewer, postID int64) (LikeResult, error) {
	post, err := s.requireInteractivePost(ctx, viewer, postID)
	if err != nil {
		return LikeResult{}, err
	}
	if err := s.allowAction(s.deps.LikeLimiter, viewer); err != nil {
		return LikeResult{}, err
	}
	return s.setPostLike(ctx, viewer.ID, post, true)
}

// UnlikePost 取消点赞帖子。
func (s *Service) UnlikePost(ctx context.Context, viewer model.Viewer, postID int64) (LikeResult, error) {
	post, err := s.requireInteractivePost(ctx, viewer, postID)
	if err != nil {
		return LikeResult{}, err
	}
	if err := s.allowAction(s.deps.LikeLimiter, viewer); err != nil {
		return LikeResult{}, err
	}
	return s.setPostLike(ctx, viewer.ID, post, false)
}

// LikeComment 点赞评论。
func (s *Service) LikeComment(ctx context.Context, viewer model.Viewer, commentID int64) (LikeResult, error) {
	comment, err := s.deps.Comments.Get(ctx, commentID)
	if err != nil {
		return LikeResult{}, err
	}
	if _, err := s.requireInteractivePost(ctx, viewer, comment.PostID); err != nil {
		return LikeResult{}, err
	}
	if comment.Deleted() {
		return LikeResult{}, apperr.Forbidden("该评论已删除，不能互动")
	}
	if err := s.allowAction(s.deps.LikeLimiter, viewer); err != nil {
		return LikeResult{}, err
	}
	return s.setCommentLike(ctx, viewer.ID, comment, true)
}

// UnlikeComment 取消点赞评论。
func (s *Service) UnlikeComment(ctx context.Context, viewer model.Viewer, commentID int64) (LikeResult, error) {
	comment, err := s.deps.Comments.Get(ctx, commentID)
	if err != nil {
		return LikeResult{}, err
	}
	if _, err := s.requireInteractivePost(ctx, viewer, comment.PostID); err != nil {
		return LikeResult{}, err
	}
	if comment.Deleted() {
		return LikeResult{}, apperr.Forbidden("该评论已删除，不能互动")
	}
	if err := s.allowAction(s.deps.LikeLimiter, viewer); err != nil {
		return LikeResult{}, err
	}
	return s.setCommentLike(ctx, viewer.ID, comment, false)
}

// setPostLike 在事务内执行帖子点赞/取消点赞并返回最新计数（actorID 已通过鉴权）。
func (s *Service) setPostLike(ctx context.Context, actorID int64, post model.Post, active bool) (LikeResult, error) {
	result := LikeResult{Liked: active}
	err := s.deps.Tx.WithTx(ctx, func(ctx context.Context) error {
		now := s.now()
		if active {
			id, err := s.newID()
			if err != nil {
				return err
			}
			likeID, changed, err := s.deps.Likes.Activate(ctx, id, actorID, domain.TargetPost, post.ID, now)
			if err != nil {
				return err
			}
			if !changed {
				current, err := s.deps.Posts.GetPost(ctx, post.ID)
				if err != nil {
					return err
				}
				result = LikeResult{Liked: true, Count: current.LikeCount}
				return nil
			}
			count, err := s.bumpPostCounter(ctx, post.ID, contracts.PostCounterLike, domain.CountDelta(false, true), now)
			if err != nil {
				return err
			}
			result = LikeResult{Liked: true, Count: count}
			return s.deps.Notices.CreateNotification(ctx, contracts.NotificationInput{
				IdempotencyKey: notifyKey("post_like", likeID),
				RecipientID:    post.AuthorID,
				ActorID:        actorID,
				Type:           model.NoticeTypePostLike,
				TargetType:     model.NoticeTargetPost,
				TargetID:       post.ID,
				PostID:         &post.ID,
				At:             now,
			})
		}

		_, changed, err := s.deps.Likes.Deactivate(ctx, actorID, domain.TargetPost, post.ID, now)
		if err != nil {
			return err
		}
		if !changed {
			current, err := s.deps.Posts.GetPost(ctx, post.ID)
			if err != nil {
				return err
			}
			result = LikeResult{Liked: false, Count: current.LikeCount}
			return nil
		}
		count, err := s.bumpPostCounter(ctx, post.ID, contracts.PostCounterLike, domain.CountDelta(true, false), now)
		if err != nil {
			return err
		}
		result = LikeResult{Liked: false, Count: count}
		return nil
	})
	if err != nil {
		return LikeResult{}, err
	}
	return result, nil
}

// setCommentLike 在事务内执行评论点赞/取消点赞并返回最新计数。
func (s *Service) setCommentLike(ctx context.Context, actorID int64, comment model.Comment, active bool) (LikeResult, error) {
	result := LikeResult{Liked: active}
	err := s.deps.Tx.WithTx(ctx, func(ctx context.Context) error {
		if active && comment.AuthorID != actorID {
			// 评论作者停用后不再接受新的点赞互动。
			suspended, err := s.authorSuspended(ctx, comment.AuthorID)
			if err != nil {
				return err
			}
			if suspended {
				return apperr.Forbidden("对方账号已停用，不能产生新的互动")
			}
		}
		now := s.now()
		if active {
			id, err := s.newID()
			if err != nil {
				return err
			}
			likeID, changed, err := s.deps.Likes.Activate(ctx, id, actorID, domain.TargetComment, comment.ID, now)
			if err != nil {
				return err
			}
			if !changed {
				current, err := s.deps.Comments.Get(ctx, comment.ID)
				if err != nil {
					return err
				}
				result = LikeResult{Liked: true, Count: current.LikeCount}
				return nil
			}
			count, err := s.deps.Comments.AdjustLikeCount(ctx, comment.ID, domain.CountDelta(false, true), now)
			if err != nil {
				return err
			}
			result = LikeResult{Liked: true, Count: count}
			postID := comment.PostID
			return s.deps.Notices.CreateNotification(ctx, contracts.NotificationInput{
				IdempotencyKey: notifyKey("comment_like", likeID),
				RecipientID:    comment.AuthorID,
				ActorID:        actorID,
				Type:           model.NoticeTypeCommentLike,
				TargetType:     model.NoticeTargetComment,
				TargetID:       comment.ID,
				PostID:         &postID,
				CommentID:      &comment.ID,
				At:             now,
			})
		}

		_, changed, err := s.deps.Likes.Deactivate(ctx, actorID, domain.TargetComment, comment.ID, now)
		if err != nil {
			return err
		}
		if !changed {
			current, err := s.deps.Comments.Get(ctx, comment.ID)
			if err != nil {
				return err
			}
			result = LikeResult{Liked: false, Count: current.LikeCount}
			return nil
		}
		count, err := s.deps.Comments.AdjustLikeCount(ctx, comment.ID, domain.CountDelta(true, false), now)
		if err != nil {
			return err
		}
		result = LikeResult{Liked: false, Count: count}
		return nil
	})
	if err != nil {
		return LikeResult{}, err
	}
	return result, nil
}
