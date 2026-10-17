package app

import (
	"context"

	"github.com/great-magician-01/timeline/internal/modules/social/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// Follow 关注目标用户：对方未开启审批（或目标是 AI）时直接生效，
// 开启审批时创建待审批申请；重复关注幂等（docs/WORKFLOWS.md 4.2）。
func (s *Service) Follow(ctx context.Context, viewer model.Viewer, username string) (string, error) {
	if err := s.requireWritable(viewer); err != nil {
		return "", err
	}
	if err := s.allowAction(s.deps.FollowLimiter, viewer); err != nil {
		return "", err
	}
	target, err := s.deps.Users.GetUserByUsername(ctx, username)
	if err != nil {
		return "", err
	}
	if target.ID == viewer.ID {
		return "", apperr.Validation("不能关注自己")
	}
	if target.IsSuspended() {
		return "", apperr.Forbidden("对方账号已停用，不能关注")
	}

	state := model.FollowStateNone
	err = s.deps.Tx.WithTx(ctx, func(ctx context.Context) error {
		active, err := s.deps.Follows.Following(ctx, viewer.ID, target.ID)
		if err != nil {
			return err
		}
		if active {
			state = model.FollowStateFollowing
			return nil
		}
		now := s.now()
		id, err := s.newID()
		if err != nil {
			return err
		}
		if !target.RequireFollowApproval || target.IsAI {
			rowID, activeAt, changed, err := s.deps.Follows.InsertActive(ctx, id, viewer.ID, target.ID, now)
			if err != nil {
				return err
			}
			state = model.FollowStateFollowing
			if !changed {
				// 并发请求已建立同样的关系，按幂等处理，不重复通知。
				return nil
			}
			return s.deps.Notices.CreateNotification(ctx, contracts.NotificationInput{
				IdempotencyKey: followNotifyKey(rowID, activeAt),
				RecipientID:    target.ID,
				ActorID:        viewer.ID,
				Type:           model.NoticeTypeFollow,
				TargetType:     model.NoticeTargetUser,
				TargetID:       viewer.ID,
				At:             now,
			})
		}

		requestID, changed, err := s.deps.Requests.InsertPending(ctx, id, viewer.ID, target.ID, now)
		if err != nil {
			return err
		}
		state = model.FollowStatePending
		if !changed {
			// 同一对账号只保留一条待处理申请，重复申请幂等。
			return nil
		}
		return s.deps.Notices.CreateNotification(ctx, contracts.NotificationInput{
			IdempotencyKey: notifyKey("follow_request", requestID),
			RecipientID:    target.ID,
			ActorID:        viewer.ID,
			Type:           model.NoticeTypeFollowRequest,
			TargetType:     model.NoticeTargetUser,
			TargetID:       viewer.ID,
			At:             now,
		})
	})
	if err != nil {
		return "", err
	}
	return state, nil
}

// Unfollow 取关，或取消自己发出的待审批申请；两者都幂等。
func (s *Service) Unfollow(ctx context.Context, viewer model.Viewer, username string) (string, error) {
	if err := s.requireWritable(viewer); err != nil {
		return "", err
	}
	if err := s.allowAction(s.deps.FollowLimiter, viewer); err != nil {
		return "", err
	}
	target, err := s.deps.Users.GetUserByUsername(ctx, username)
	if err != nil {
		return "", err
	}
	err = s.deps.Tx.WithTx(ctx, func(ctx context.Context) error {
		now := s.now()
		if _, err := s.deps.Follows.Deactivate(ctx, viewer.ID, target.ID, now); err != nil {
			return err
		}
		_, err := s.deps.Requests.CancelPending(ctx, viewer.ID, target.ID, now)
		return err
	})
	if err != nil {
		return "", err
	}
	return model.FollowStateNone, nil
}

// RemoveFollower 移除自己的粉丝：只解除关注关系，不创建通知（不等于拉黑）。
func (s *Service) RemoveFollower(ctx context.Context, viewer model.Viewer, username string) (string, error) {
	if err := s.requireWritable(viewer); err != nil {
		return "", err
	}
	if err := s.allowAction(s.deps.FollowLimiter, viewer); err != nil {
		return "", err
	}
	follower, err := s.deps.Users.GetUserByUsername(ctx, username)
	if err != nil {
		return "", err
	}
	// 不存在生效关系时不做任何修改，幂等返回。
	if _, err := s.deps.Follows.Deactivate(ctx, follower.ID, viewer.ID, s.now()); err != nil {
		return "", err
	}
	return model.FollowStateNone, nil
}

// ListFollowers 列出目标的生效粉丝（游标按关注行 ID 倒序）；游客可读。
func (s *Service) ListFollowers(ctx context.Context, username string, cursor int64, limit int) (FollowPage, error) {
	target, err := s.deps.Users.GetUserByUsername(ctx, username)
	if err != nil {
		return FollowPage{}, err
	}
	rows, err := s.deps.Follows.ListFollowers(ctx, target.ID, cursor, limit+1)
	if err != nil {
		return FollowPage{}, err
	}
	return s.followPage(ctx, rows, limit)
}

// ListFollowing 列出目标的生效关注（游标按关注行 ID 倒序）；游客可读。
func (s *Service) ListFollowing(ctx context.Context, username string, cursor int64, limit int) (FollowPage, error) {
	target, err := s.deps.Users.GetUserByUsername(ctx, username)
	if err != nil {
		return FollowPage{}, err
	}
	rows, err := s.deps.Follows.ListFollowing(ctx, target.ID, cursor, limit+1)
	if err != nil {
		return FollowPage{}, err
	}
	return s.followPage(ctx, rows, limit)
}

// followPage 批量补齐作者摘要并裁剪分页。
func (s *Service) followPage(ctx context.Context, rows []FollowRow, limit int) (FollowPage, error) {
	trimmed, hasMore := trimPage(rows, limit)
	userIDs := make([]int64, 0, len(trimmed))
	for _, row := range trimmed {
		userIDs = append(userIDs, row.UserID)
	}
	briefs, err := s.deps.Users.GetUserBriefs(ctx, userIDs)
	if err != nil {
		return FollowPage{}, err
	}
	page := FollowPage{Items: make([]FollowItem, 0, len(trimmed)), HasMore: hasMore}
	for _, row := range trimmed {
		page.Items = append(page.Items, FollowItem{RowID: row.RowID, User: briefs[row.UserID]})
		page.NextCursor = row.RowID
	}
	return page, nil
}

// 保证 domain 常量被引用（关注申请方向在 domain 中统一维护）。
var _ = domain.DirectionIncoming
