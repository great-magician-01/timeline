package app

import (
	"context"

	"github.com/great-magician-01/timeline/internal/modules/social/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// Repost 纯转发：只允许公开且已发布的帖子，幂等。
func (s *Service) Repost(ctx context.Context, viewer model.Viewer, postID int64) (RepostResult, error) {
	post, err := s.requireInteractivePost(ctx, viewer, postID)
	if err != nil {
		return RepostResult{}, err
	}
	if post.Visibility != model.VisibilityPublic {
		return RepostResult{}, apperr.Forbidden("仅公开内容可以转发")
	}
	if err := s.allowAction(s.deps.LikeLimiter, viewer); err != nil {
		return RepostResult{}, err
	}
	return s.setRepost(ctx, viewer.ID, viewer.IsAI, post, true)
}

// Unrepost 取消纯转发，幂等。
func (s *Service) Unrepost(ctx context.Context, viewer model.Viewer, postID int64) (RepostResult, error) {
	post, err := s.requireInteractivePost(ctx, viewer, postID)
	if err != nil {
		return RepostResult{}, err
	}
	if post.Visibility != model.VisibilityPublic {
		return RepostResult{}, apperr.Forbidden("仅公开内容可以转发")
	}
	if err := s.allowAction(s.deps.LikeLimiter, viewer); err != nil {
		return RepostResult{}, err
	}
	return s.setRepost(ctx, viewer.ID, viewer.IsAI, post, false)
}

// setRepost 在事务内执行纯转发/取消转发并返回最新计数；actorIsAI 作为转发者类型快照写入转发行。
func (s *Service) setRepost(
	ctx context.Context, actorID int64, actorIsAI bool, post model.Post, active bool,
) (RepostResult, error) {
	if active && post.AuthorID != actorID {
		suspended, err := s.authorSuspended(ctx, post.AuthorID)
		if err != nil {
			return RepostResult{}, err
		}
		if suspended {
			return RepostResult{}, apperr.Forbidden("对方账号已停用，不能产生新的互动")
		}
	}

	result := RepostResult{Reposted: active}
	err := s.deps.Tx.WithTx(ctx, func(ctx context.Context) error {
		now := s.now()
		if active {
			id, err := s.newID()
			if err != nil {
				return err
			}
			repostID, repostedAt, changed, err := s.deps.Reposts.Activate(ctx, id, actorID, post.ID, actorIsAI, now)
			if err != nil {
				return err
			}
			if !changed {
				// 已生效的重复转发：沿用既有转发行的时间，不重复计数/通知。
				current, err := s.deps.Posts.GetPost(ctx, post.ID)
				if err != nil {
					return err
				}
				result = RepostResult{
					Reposted:   true,
					Count:      current.RepostCount,
					RepostID:   repostID,
					RepostedAt: repostedAt,
				}
				return nil
			}
			count, err := s.bumpPostCounter(ctx, post.ID, contracts.PostCounterRepost, domain.CountDelta(false, true), now)
			if err != nil {
				return err
			}
			result = RepostResult{
				Reposted:   true,
				Count:      count,
				RepostID:   repostID,
				RepostedAt: repostedAt,
			}
			return s.deps.Notices.CreateNotification(ctx, contracts.NotificationInput{
				IdempotencyKey: notifyKey("repost", repostID),
				RecipientID:    post.AuthorID,
				ActorID:        actorID,
				Type:           model.NoticeTypeRepost,
				TargetType:     model.NoticeTargetPost,
				TargetID:       post.ID,
				PostID:         &post.ID,
				At:             now,
			})
		}

		_, changed, err := s.deps.Reposts.Deactivate(ctx, actorID, post.ID, now)
		if err != nil {
			return err
		}
		if !changed {
			current, err := s.deps.Posts.GetPost(ctx, post.ID)
			if err != nil {
				// 目标从未存在时保持取消转发的幂等语义（旧实现返回计数 0）。
				if apperr.IsKind(err, apperr.KindNotFound) {
					result = RepostResult{Reposted: false, Count: 0}
					return nil
				}
				return err
			}
			result = RepostResult{Reposted: false, Count: current.RepostCount}
			return nil
		}
		count, err := s.bumpPostCounter(ctx, post.ID, contracts.PostCounterRepost, domain.CountDelta(true, false), now)
		if err != nil {
			return err
		}
		result = RepostResult{Reposted: false, Count: count}
		return nil
	})
	if err != nil {
		return RepostResult{}, err
	}
	return result, nil
}
