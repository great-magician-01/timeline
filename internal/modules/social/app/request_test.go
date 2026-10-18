package app

import (
	"context"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/social/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// stubRequestRow 构造一条待处理关注申请。
func stubRequestRow(id, requesterID, targetID int64, status string) RequestRow {
	return RequestRow{
		ID:          id,
		RequesterID: requesterID,
		TargetID:    targetID,
		Status:      status,
		CreatedAt:   stubNow.Add(-10 * time.Minute),
	}
}

// TestListFollowRequests 验证申请列表方向、游标裁剪与双方摘要。
func TestListFollowRequests(t *testing.T) {
	ctx := context.Background()
	row := stubRequestRow(700, stubViewerID, stubAuthorID, model.FollowRequestPending)

	t.Run("默认发给我的申请", func(t *testing.T) {
		stubs := newStubSet()
		stubs.requests.incomingRows = []RequestRow{row}
		page, err := stubs.service().ListFollowRequests(ctx, stubViewer(), "", 0, 1)
		requireOK(t, err)
		if len(page.Items) != 1 {
			t.Fatalf("申请条数 = %d，期望 1", len(page.Items))
		}
		item := page.Items[0]
		if item.ID != 700 || item.Status != model.FollowRequestPending {
			t.Fatalf("申请项 = %+v", item)
		}
		if item.Requester.Username != "alice" || item.Target.Username != "bob" {
			t.Fatalf("申请双方摘要 = (%q, %q)", item.Requester.Username, item.Target.Username)
		}
		if !item.CreatedAt.Equal(row.CreatedAt) || item.ResolvedAt != nil {
			t.Fatalf("申请时间字段 = (%v, %v)", item.CreatedAt, item.ResolvedAt)
		}
		if call := stubs.requests.calls["incoming"]; call.userID != stubViewerID || call.limit != 2 {
			t.Fatalf("incoming 查询参数 = %+v，期望 (viewer=%d, limit=2)", call, stubViewerID)
		}
	})

	t.Run("我发出的申请", func(t *testing.T) {
		stubs := newStubSet()
		resolvedAt := stubNow.Add(-time.Minute)
		resolved := row
		resolved.ResolvedAt = &resolvedAt
		stubs.requests.outgoingRows = []RequestRow{resolved}
		page, err := stubs.service().ListFollowRequests(ctx, stubViewer(), "  outgoing  ", 0, 20)
		requireOK(t, err)
		if len(page.Items) != 1 || page.Items[0].ResolvedAt == nil || !page.Items[0].ResolvedAt.Equal(resolvedAt) {
			t.Fatalf("outgoing 申请项 = %+v，期望携带处理时间", page.Items)
		}
		if _, ok := stubs.requests.calls["outgoing"]; !ok {
			t.Fatal("direction=outgoing 应查询我发出的申请")
		}
	})

	t.Run("分页裁剪", func(t *testing.T) {
		stubs := newStubSet()
		second := stubRequestRow(600, stubOtherID, stubViewerID, model.FollowRequestPending)
		stubs.requests.incomingRows = []RequestRow{row, second}
		page, err := stubs.service().ListFollowRequests(ctx, stubViewer(), "", 10, 1)
		requireOK(t, err)
		if len(page.Items) != 1 || !page.HasMore || page.NextCursor != 700 {
			t.Fatalf("分页结果 = %+v，期望 1 条且游标 700", page)
		}
		if call := stubs.requests.calls["incoming"]; call.cursor != 10 || call.limit != 2 {
			t.Fatalf("游标参数 = %+v，期望 (cursor=10, limit=2)", call)
		}
	})
}

// TestListFollowRequestsErrors 覆盖申请列表错误路径。
func TestListFollowRequestsErrors(t *testing.T) {
	ctx := context.Background()
	boom := apperr.Internal("内部错误")
	cases := []struct {
		name      string
		viewer    model.Viewer
		direction string
		setup     func(*stubSet)
		wantKind  apperr.Kind
	}{
		{"游客", stubGuestViewer(), "", func(*stubSet) {}, apperr.KindUnauthenticated},
		{"方向非法", stubViewer(), "sideways", func(*stubSet) {}, apperr.KindInvalid},
		{"incoming 查询失败", stubViewer(), "", func(s *stubSet) { s.requests.incomingErr = boom }, apperr.KindInternal},
		{"outgoing 查询失败", stubViewer(), "outgoing", func(s *stubSet) { s.requests.outgoingErr = boom }, apperr.KindInternal},
		{"作者摘要失败", stubViewer(), "", func(s *stubSet) {
			s.requests.incomingRows = []RequestRow{stubRequestRow(700, stubViewerID, stubAuthorID, model.FollowRequestPending)}
			s.users.briefsErr = boom
		}, apperr.KindInternal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubs := newStubSet()
			tc.setup(stubs)
			_, err := stubs.service().ListFollowRequests(ctx, tc.viewer, tc.direction, 0, 20)
			requireKind(t, err, tc.wantKind)
		})
	}
}

// TestApproveFollowRequest 验证批准申请：状态迁移、建立关注、通知与事务时间。
func TestApproveFollowRequest(t *testing.T) {
	stubs := newStubSet()
	stubs.requests.rows[700] = stubRequestRow(700, stubViewerID, stubAuthorID, model.FollowRequestPending)
	svc := stubs.service()

	result, err := svc.ApproveFollowRequest(context.Background(), stubAuthorViewer(), 700)
	requireOK(t, err)
	if result.Status != model.FollowRequestApproved || result.FollowState != model.FollowStateFollowing {
		t.Fatalf("批准结果 = %+v，期望 approved/following", result)
	}
	if len(stubs.requests.updates) != 1 {
		t.Fatalf("状态迁移调用 = %d，期望 1", len(stubs.requests.updates))
	}
	update := stubs.requests.updates[0]
	if update.id != 700 || update.status != model.FollowRequestApproved || !update.resolvedAt.Equal(stubNow) {
		t.Fatalf("状态迁移参数 = %+v，期望 (700, approved, %v)", update, stubNow)
	}
	if len(stubs.follows.inserts) != 1 {
		t.Fatalf("关注关系写入 = %d，期望 1", len(stubs.follows.inserts))
	}
	insert := stubs.follows.inserts[0]
	if insert.id != 101 || insert.followerID != stubViewerID || insert.followeeID != stubAuthorID || !insert.at.Equal(stubNow) {
		t.Fatalf("批准建立关系参数 = %+v", insert)
	}
	if len(stubs.notices.notifications) != 1 {
		t.Fatalf("通知条数 = %d，期望 1", len(stubs.notices.notifications))
	}
	notice := stubs.notices.notifications[0]
	if notice.RecipientID != stubViewerID || notice.ActorID != stubAuthorID {
		t.Fatalf("通知接收者/操作者 = (%d, %d)", notice.RecipientID, notice.ActorID)
	}
	if notice.Type != model.NoticeTypeFollowApprove || notice.TargetType != model.NoticeTargetUser || notice.TargetID != stubAuthorID {
		t.Fatalf("通知内容 = %+v，期望批准通知", notice)
	}
	if notice.IdempotencyKey != "follow_approve:700" || !notice.At.Equal(stubNow) {
		t.Fatalf("通知幂等键/时间 = (%q, %v)", notice.IdempotencyKey, notice.At)
	}
}

// TestApproveFollowRequestIdempotent 验证已批准申请重复批准幂等且不再建立关系。
func TestApproveFollowRequestIdempotent(t *testing.T) {
	stubs := newStubSet()
	stubs.requests.rows[700] = stubRequestRow(700, stubViewerID, stubAuthorID, model.FollowRequestApproved)
	result, err := stubs.service().ApproveFollowRequest(context.Background(), stubAuthorViewer(), 700)
	requireOK(t, err)
	if result.Status != model.FollowRequestApproved || result.FollowState != model.FollowStateFollowing {
		t.Fatalf("重复批准结果 = %+v", result)
	}
	if len(stubs.follows.inserts) != 0 || len(stubs.requests.updates) != 0 || len(stubs.notices.notifications) != 0 {
		t.Fatal("重复批准不应再次建立关系/迁移状态/通知")
	}
}

// TestRejectAndCancelFollowRequest 验证拒绝与取消的状态机。
func TestRejectAndCancelFollowRequest(t *testing.T) {
	ctx := context.Background()

	t.Run("拒绝申请", func(t *testing.T) {
		stubs := newStubSet()
		stubs.requests.rows[700] = stubRequestRow(700, stubViewerID, stubAuthorID, model.FollowRequestPending)
		result, err := stubs.service().RejectFollowRequest(ctx, stubAuthorViewer(), 700)
		requireOK(t, err)
		if result.Status != model.FollowRequestRejected || result.FollowState != model.FollowStateNone {
			t.Fatalf("拒绝结果 = %+v，期望 rejected/none", result)
		}
		if update := stubs.requests.updates[0]; update.status != model.FollowRequestRejected || !update.resolvedAt.Equal(stubNow) {
			t.Fatalf("状态迁移参数 = %+v", update)
		}
		if len(stubs.follows.inserts) != 0 || len(stubs.notices.notifications) != 0 {
			t.Fatal("拒绝不应建立关系或通知")
		}
	})

	t.Run("取消申请", func(t *testing.T) {
		stubs := newStubSet()
		stubs.requests.rows[700] = stubRequestRow(700, stubViewerID, stubAuthorID, model.FollowRequestPending)
		result, err := stubs.service().CancelFollowRequest(ctx, stubViewer(), 700)
		requireOK(t, err)
		if result.Status != model.FollowRequestCanceled || result.FollowState != model.FollowStateNone {
			t.Fatalf("取消结果 = %+v，期望 canceled/none", result)
		}
		if update := stubs.requests.updates[0]; update.status != model.FollowRequestCanceled {
			t.Fatalf("状态迁移参数 = %+v", update)
		}
	})

	t.Run("已取消幂等", func(t *testing.T) {
		stubs := newStubSet()
		stubs.requests.rows[700] = stubRequestRow(700, stubViewerID, stubAuthorID, model.FollowRequestCanceled)
		result, err := stubs.service().CancelFollowRequest(ctx, stubViewer(), 700)
		requireOK(t, err)
		if result.Status != model.FollowRequestCanceled {
			t.Fatalf("结果 = %+v", result)
		}
		if len(stubs.requests.updates) != 0 {
			t.Fatal("已取消申请不应再次迁移状态")
		}
	})
}

// TestFollowRequestActionErrors 覆盖处理申请时的权限、冲突与依赖错误。
func TestFollowRequestActionErrors(t *testing.T) {
	ctx := context.Background()
	boom := apperr.Internal("内部错误")
	cases := []struct {
		name     string
		action   string
		viewer   model.Viewer
		ids      IDGenerator
		setup    func(*stubSet)
		wantKind apperr.Kind
		wantCode string
	}{
		{
			name:     "申请不存在",
			action:   domain.ActionApprove,
			viewer:   stubAuthorViewer(),
			setup:    func(*stubSet) {},
			wantKind: apperr.KindNotFound,
		},
		{
			name:   "非目标用户不能批准",
			action: domain.ActionApprove,
			viewer: stubViewer(),
			setup: func(s *stubSet) {
				s.requests.rows[700] = stubRequestRow(700, stubViewerID, stubAuthorID, model.FollowRequestPending)
			},
			wantKind: apperr.KindNotFound,
		},
		{
			name:   "非申请者不能取消",
			action: domain.ActionCancel,
			viewer: stubAuthorViewer(),
			setup: func(s *stubSet) {
				s.requests.rows[700] = stubRequestRow(700, stubViewerID, stubAuthorID, model.FollowRequestPending)
			},
			wantKind: apperr.KindNotFound,
		},
		{
			name:   "已拒绝申请不能批准",
			action: domain.ActionApprove,
			viewer: stubAuthorViewer(),
			setup: func(s *stubSet) {
				s.requests.rows[700] = stubRequestRow(700, stubViewerID, stubAuthorID, model.FollowRequestRejected)
			},
			wantKind: apperr.KindConflict,
			wantCode: "request_resolved",
		},
		{
			name:   "申请者已停用",
			action: domain.ActionApprove,
			viewer: stubAuthorViewer(),
			setup: func(s *stubSet) {
				s.requests.rows[700] = stubRequestRow(700, stubViewerID, stubAuthorID, model.FollowRequestPending)
				requester := s.users.byID[stubViewerID]
				requester.Status = model.StatusSuspended
				s.users.byID[stubViewerID] = requester
			},
			wantKind: apperr.KindForbidden,
		},
		{
			name:   "ID 生成失败",
			action: domain.ActionApprove,
			viewer: stubAuthorViewer(),
			ids:    errIDs{err: boom},
			setup: func(s *stubSet) {
				s.requests.rows[700] = stubRequestRow(700, stubViewerID, stubAuthorID, model.FollowRequestPending)
			},
			wantKind: apperr.KindInternal,
		},
		{
			name:     "读取申请失败",
			action:   domain.ActionApprove,
			viewer:   stubAuthorViewer(),
			setup:    func(s *stubSet) { s.requests.getErr = boom },
			wantKind: apperr.KindInternal,
		},
		{
			name:   "关注关系写入失败",
			action: domain.ActionApprove,
			viewer: stubAuthorViewer(),
			setup: func(s *stubSet) {
				s.requests.rows[700] = stubRequestRow(700, stubViewerID, stubAuthorID, model.FollowRequestPending)
				s.follows.insertErr = boom
			},
			wantKind: apperr.KindInternal,
		},
		{
			name:   "通知失败",
			action: domain.ActionApprove,
			viewer: stubAuthorViewer(),
			setup: func(s *stubSet) {
				s.requests.rows[700] = stubRequestRow(700, stubViewerID, stubAuthorID, model.FollowRequestPending)
				s.notices.createErr = boom
			},
			wantKind: apperr.KindInternal,
		},
		{
			name:   "状态迁移失败",
			action: domain.ActionApprove,
			viewer: stubAuthorViewer(),
			setup: func(s *stubSet) {
				s.requests.rows[700] = stubRequestRow(700, stubViewerID, stubAuthorID, model.FollowRequestPending)
				s.requests.updateErr = boom
			},
			wantKind: apperr.KindInternal,
		},
		{
			name:     "游客",
			action:   domain.ActionApprove,
			viewer:   stubGuestViewer(),
			setup:    func(*stubSet) {},
			wantKind: apperr.KindUnauthenticated,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubs := newStubSet()
			tc.setup(stubs)
			var svc *Service
			if tc.ids != nil {
				svc = stubs.serviceWith(func(deps *Deps) { deps.IDs = tc.ids })
			} else {
				svc = stubs.service()
			}
			var err error
			switch tc.action {
			case domain.ActionApprove:
				_, err = svc.ApproveFollowRequest(ctx, tc.viewer, 700)
			case domain.ActionReject:
				_, err = svc.RejectFollowRequest(ctx, tc.viewer, 700)
			default:
				_, err = svc.CancelFollowRequest(ctx, tc.viewer, 700)
			}
			appErr := requireKind(t, err, tc.wantKind)
			if tc.wantCode != "" && appErr.Code != tc.wantCode {
				t.Fatalf("错误码 = %q，期望 %q", appErr.Code, tc.wantCode)
			}
		})
	}
}
