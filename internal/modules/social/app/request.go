package app

import (
	"context"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/social/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// ListFollowRequests 列出发给我（incoming，默认）或我发出（outgoing）的待处理申请，
// 按 ID 倒序分页。
func (s *Service) ListFollowRequests(ctx context.Context, viewer model.Viewer, direction string, cursor int64, limit int) (RequestPage, error) {
	if !viewer.Authenticated {
		return RequestPage{}, apperr.Unauthenticated("请先登录")
	}
	dir, err := domain.NormalizeDirection(direction)
	if err != nil {
		return RequestPage{}, err
	}

	var rows []RequestRow
	if dir == domain.DirectionIncoming {
		rows, err = s.deps.Requests.ListPendingIncoming(ctx, viewer.ID, cursor, limit+1)
	} else {
		rows, err = s.deps.Requests.ListPendingOutgoing(ctx, viewer.ID, cursor, limit+1)
	}
	if err != nil {
		return RequestPage{}, err
	}
	trimmed, hasMore := trimPage(rows, limit)

	userIDs := make([]int64, 0, 2*len(trimmed))
	for _, row := range trimmed {
		userIDs = append(userIDs, row.RequesterID, row.TargetID)
	}
	briefs, err := s.deps.Users.GetUserBriefs(ctx, userIDs)
	if err != nil {
		return RequestPage{}, err
	}
	page := RequestPage{Items: make([]RequestItem, 0, len(trimmed)), HasMore: hasMore}
	for _, row := range trimmed {
		page.Items = append(page.Items, RequestItem{
			ID:         row.ID,
			Status:     row.Status,
			Requester:  briefs[row.RequesterID],
			Target:     briefs[row.TargetID],
			CreatedAt:  row.CreatedAt,
			ResolvedAt: row.ResolvedAt,
		})
		page.NextCursor = row.ID
	}
	return page, nil
}

// ApproveFollowRequest 批准发给自己的申请，并在同一事务内建立生效关注关系。
func (s *Service) ApproveFollowRequest(ctx context.Context, viewer model.Viewer, requestID int64) (RequestResult, error) {
	return s.resolveFollowRequest(ctx, viewer, requestID, domain.ActionApprove)
}

// RejectFollowRequest 拒绝发给自己的申请。
func (s *Service) RejectFollowRequest(ctx context.Context, viewer model.Viewer, requestID int64) (RequestResult, error) {
	return s.resolveFollowRequest(ctx, viewer, requestID, domain.ActionReject)
}

// CancelFollowRequest 取消自己发出的待审批申请。
func (s *Service) CancelFollowRequest(ctx context.Context, viewer model.Viewer, requestID int64) (RequestResult, error) {
	return s.resolveFollowRequest(ctx, viewer, requestID, domain.ActionCancel)
}

func (s *Service) resolveFollowRequest(ctx context.Context, viewer model.Viewer, requestID int64, action string) (RequestResult, error) {
	if err := s.requireWritable(viewer); err != nil {
		return RequestResult{}, err
	}
	result := RequestResult{FollowState: model.FollowStateNone}
	err := s.deps.Tx.WithTx(ctx, func(ctx context.Context) error {
		request, err := s.deps.Requests.GetForUpdate(ctx, requestID)
		if err != nil {
			return err
		}
		// 非本人的申请按「不存在」处理，不暴露存在性。
		if action == domain.ActionCancel {
			if viewer.ID != request.RequesterID {
				return apperr.NotFound("申请不存在")
			}
		} else if viewer.ID != request.TargetID {
			return apperr.NotFound("申请不存在")
		}

		next, changed, err := domain.FollowRequestTransition(request.Status, action)
		if err != nil {
			return err
		}
		result.Status = next
		if action == domain.ActionApprove {
			result.FollowState = model.FollowStateFollowing
		}
		if !changed {
			return nil
		}

		now := s.now()
		if action == domain.ActionApprove {
			if err := s.approveFollow(ctx, viewer, request, now); err != nil {
				return err
			}
		}
		return s.deps.Requests.UpdateStatus(ctx, requestID, next, now)
	})
	if err != nil {
		return RequestResult{}, err
	}
	return result, nil
}

// approveFollow 在事务内建立关注关系并写入批准通知。
// 批准就是授权：直接建立生效关系，不受申请者当前审批设置影响。
func (s *Service) approveFollow(ctx context.Context, viewer model.Viewer, request RequestRow, now time.Time) error {
	requester, err := s.deps.Users.GetUser(ctx, request.RequesterID)
	if err != nil {
		return err
	}
	if requester.IsSuspended() {
		return apperr.Forbidden("对方账号已停用，不能批准关注申请")
	}
	id, err := s.newID()
	if err != nil {
		return err
	}
	if _, _, _, err := s.deps.Follows.InsertActive(ctx, id, request.RequesterID, request.TargetID, now); err != nil {
		return err
	}
	return s.deps.Notices.CreateNotification(ctx, contracts.NotificationInput{
		IdempotencyKey: notifyKey("follow_approve", request.ID),
		RecipientID:    request.RequesterID,
		ActorID:        viewer.ID,
		Type:           model.NoticeTypeFollowApprove,
		TargetType:     model.NoticeTargetUser,
		TargetID:       request.TargetID,
		At:             now,
	})
}
