package app

import (
	"context"
	"strconv"
	"testing"

	"github.com/great-magician-01/timeline/internal/modules/social/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// TestFollowDirect 验证无需审批的关注：关系写入、通知与幂等键。
func TestFollowDirect(t *testing.T) {
	stubs := newStubSet()
	svc := stubs.service()

	state, err := svc.Follow(context.Background(), stubViewer(), "bob")
	requireOK(t, err)
	if state != model.FollowStateFollowing {
		t.Fatalf("关注状态 = %q，期望 following", state)
	}
	if len(stubs.follows.inserts) != 1 {
		t.Fatalf("关注写入调用 = %d，期望 1", len(stubs.follows.inserts))
	}
	insert := stubs.follows.inserts[0]
	if insert.id != 101 || insert.followerID != stubViewerID || insert.followeeID != stubAuthorID {
		t.Fatalf("关注写入参数 = %+v，期望 (101, %d, %d)", insert, stubViewerID, stubAuthorID)
	}
	if !insert.at.Equal(stubNow) {
		t.Fatalf("关注写入时间 = %v，期望 %v", insert.at, stubNow)
	}
	if len(stubs.notices.notifications) != 1 {
		t.Fatalf("通知条数 = %d，期望 1", len(stubs.notices.notifications))
	}
	notice := stubs.notices.notifications[0]
	if notice.RecipientID != stubAuthorID || notice.ActorID != stubViewerID {
		t.Fatalf("通知接收者/操作者 = (%d, %d)", notice.RecipientID, notice.ActorID)
	}
	if notice.Type != model.NoticeTypeFollow || notice.TargetType != model.NoticeTargetUser || notice.TargetID != stubViewerID {
		t.Fatalf("通知内容 = %+v，期望关注通知", notice)
	}
	wantKey := "follow:101:" + strconv.FormatInt(stubNow.Unix(), 10)
	if notice.IdempotencyKey != wantKey {
		t.Fatalf("通知幂等键 = %q，期望 %q", notice.IdempotencyKey, wantKey)
	}
	if !notice.At.Equal(stubNow) {
		t.Fatalf("通知时间 = %v，期望 %v", notice.At, stubNow)
	}
}

// TestFollowApprovalMode 验证需要审批的关注只创建待处理申请并通知目标。
func TestFollowApprovalMode(t *testing.T) {
	stubs := newStubSet()
	carol := stubs.users.byName["carol"]
	carol.RequireFollowApproval = true
	stubs.users.byName["carol"] = carol
	stubs.users.byID[stubOtherID] = carol
	svc := stubs.service()

	state, err := svc.Follow(context.Background(), stubViewer(), "carol")
	requireOK(t, err)
	if state != model.FollowStatePending {
		t.Fatalf("关注状态 = %q，期望 pending", state)
	}
	if len(stubs.requests.inserts) != 1 {
		t.Fatalf("申请写入调用 = %d，期望 1", len(stubs.requests.inserts))
	}
	insert := stubs.requests.inserts[0]
	if insert.id != 101 || insert.requesterID != stubViewerID || insert.targetID != stubOtherID || !insert.at.Equal(stubNow) {
		t.Fatalf("申请写入参数 = %+v", insert)
	}
	if len(stubs.follows.inserts) != 0 {
		t.Fatal("审批模式不应直接建立关注关系")
	}
	if len(stubs.notices.notifications) != 1 {
		t.Fatalf("通知条数 = %d，期望 1", len(stubs.notices.notifications))
	}
	notice := stubs.notices.notifications[0]
	if notice.RecipientID != stubOtherID || notice.Type != model.NoticeTypeFollowRequest {
		t.Fatalf("通知内容 = %+v，期望关注申请通知", notice)
	}
	if notice.IdempotencyKey != "follow_request:101" {
		t.Fatalf("通知幂等键 = %q，期望 follow_request:101", notice.IdempotencyKey)
	}
}

// TestFollowApprovalModeAITargetIsDirect 验证 AI 目标即使开启审批也直接关注。
func TestFollowApprovalModeAITargetIsDirect(t *testing.T) {
	stubs := newStubSet()
	brief := stubs.users.byID[stubAIID]
	brief.RequireFollowApproval = true
	stubs.users.byID[stubAIID] = brief
	stubs.users.byName["aibot"] = brief

	state, err := stubs.service().Follow(context.Background(), stubViewer(), "aibot")
	requireOK(t, err)
	if state != model.FollowStateFollowing {
		t.Fatalf("AI 目标关注状态 = %q，期望 following", state)
	}
	if len(stubs.follows.inserts) != 1 || len(stubs.requests.inserts) != 0 {
		t.Fatal("AI 目标应直接建立关注关系")
	}
}

// TestFollowIdempotent 验证已关注与重复申请的幂等语义。
func TestFollowIdempotent(t *testing.T) {
	ctx := context.Background()

	t.Run("已关注", func(t *testing.T) {
		stubs := newStubSet()
		stubs.follows.following[[2]int64{stubViewerID, stubAuthorID}] = true
		state, err := stubs.service().Follow(ctx, stubViewer(), "bob")
		requireOK(t, err)
		if state != model.FollowStateFollowing {
			t.Fatalf("状态 = %q，期望 following", state)
		}
		if len(stubs.follows.inserts) != 0 || len(stubs.notices.notifications) != 0 {
			t.Fatal("已关注不应重复写入或通知")
		}
	})

	t.Run("重复申请", func(t *testing.T) {
		stubs := newStubSet()
		carol := stubs.users.byName["carol"]
		carol.RequireFollowApproval = true
		stubs.users.byName["carol"] = carol
		stubs.requests.insertChanged = false
		state, err := stubs.service().Follow(ctx, stubViewer(), "carol")
		requireOK(t, err)
		if state != model.FollowStatePending {
			t.Fatalf("状态 = %q，期望 pending", state)
		}
		if len(stubs.notices.notifications) != 0 {
			t.Fatal("重复申请不应重复通知")
		}
	})

	t.Run("并发建立关系", func(t *testing.T) {
		stubs := newStubSet()
		stubs.follows.insertChanged = false
		state, err := stubs.service().Follow(ctx, stubViewer(), "bob")
		requireOK(t, err)
		if state != model.FollowStateFollowing {
			t.Fatalf("状态 = %q，期望 following", state)
		}
		if len(stubs.notices.notifications) != 0 {
			t.Fatal("并发已建立关系不应重复通知")
		}
	})
}

// TestFollowGuards 覆盖关注鉴权、目标校验与限频。
func TestFollowGuards(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name     string
		viewer   model.Viewer
		username string
		setup    func(*stubSet)
		wantKind apperr.Kind
	}{
		{"游客", stubGuestViewer(), "bob", func(*stubSet) {}, apperr.KindUnauthenticated},
		{"停用账号", stubSuspendedViewer(), "bob", func(*stubSet) {}, apperr.KindSuspended},
		{
			name:     "AI 身份",
			viewer:   model.ViewerFrom(model.User{ID: stubAIID, IsAI: true, Status: model.StatusActive}),
			username: "bob",
			setup:    func(*stubSet) {},
			wantKind: apperr.KindForbidden,
		},
		{"关注自己", stubViewer(), "alice", func(*stubSet) {}, apperr.KindValidation},
		{"目标账号停用", stubViewer(), "dave", func(*stubSet) {}, apperr.KindForbidden},
		{
			name:     "目标不存在",
			viewer:   stubViewer(),
			username: "nobody",
			setup:    func(*stubSet) {},
			wantKind: apperr.KindNotFound,
		},
		{
			name:     "用户查询失败",
			viewer:   stubViewer(),
			username: "bob",
			setup:    func(s *stubSet) { s.users.nameErr = apperr.Internal("用户查询失败") },
			wantKind: apperr.KindInternal,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubs := newStubSet()
			tc.setup(stubs)
			_, err := stubs.service().Follow(ctx, tc.viewer, tc.username)
			requireKind(t, err, tc.wantKind)
		})
	}

	t.Run("限频", func(t *testing.T) {
		stubs := newStubSet()
		stubs.followLimiter = newFixedLimiter(1)
		svc := stubs.service()
		svc.deps.FollowLimiter.Consume("user:" + strconv.FormatInt(stubViewerID, 10))
		_, err := svc.Follow(ctx, stubViewer(), "bob")
		requireKind(t, err, apperr.KindRateLimited)
	})
}

// TestFollowErrors 覆盖关注写入链路错误。
func TestFollowErrors(t *testing.T) {
	boom := apperr.Internal("内部错误")
	cases := []struct {
		name  string
		ids   IDGenerator
		tx    TxRunner
		setup func(*stubSet)
	}{
		{"ID 生成失败", errIDs{err: boom}, nil, func(*stubSet) {}},
		{"事务失败", nil, errTx{err: boom}, func(*stubSet) {}},
		{"关注关系查询失败", nil, nil, func(s *stubSet) { s.follows.followingErr = boom }},
		{"关注写入失败", nil, nil, func(s *stubSet) { s.follows.insertErr = boom }},
		{"通知失败", nil, nil, func(s *stubSet) { s.notices.createErr = boom }},
		{"申请写入失败", nil, nil, func(s *stubSet) {
			carol := s.users.byName["carol"]
			carol.RequireFollowApproval = true
			s.users.byName["carol"] = carol
			s.requests.insertErr = boom
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubs := newStubSet()
			tc.setup(stubs)
			svc := stubs.serviceWith(func(deps *Deps) {
				if tc.ids != nil {
					deps.IDs = tc.ids
				}
				if tc.tx != nil {
					deps.Tx = tc.tx
				}
			})
			if _, err := svc.Follow(context.Background(), stubViewer(), "carol"); err == nil {
				t.Fatal("期望返回错误")
			}
		})
	}
}

// TestUnfollow 验证取关同时停用关注关系并取消待处理申请。
func TestUnfollow(t *testing.T) {
	ctx := context.Background()
	stubs := newStubSet()
	state, err := stubs.service().Unfollow(ctx, stubViewer(), "bob")
	requireOK(t, err)
	if state != model.FollowStateNone {
		t.Fatalf("取关状态 = %q，期望 none", state)
	}
	if len(stubs.follows.deactivates) != 1 {
		t.Fatalf("取关写入调用 = %d，期望 1", len(stubs.follows.deactivates))
	}
	deactivate := stubs.follows.deactivates[0]
	if deactivate.followerID != stubViewerID || deactivate.followeeID != stubAuthorID || !deactivate.at.Equal(stubNow) {
		t.Fatalf("取关参数 = %+v", deactivate)
	}
	cancel, ok := stubs.requests.calls["cancel"]
	if !ok || cancel.userID != stubViewerID || cancel.cursor != stubAuthorID {
		t.Fatalf("取消申请参数 = %+v，期望 (%d -> %d)", cancel, stubViewerID, stubAuthorID)
	}
}

// TestUnfollowErrors 覆盖取关的错误路径。
func TestUnfollowErrors(t *testing.T) {
	ctx := context.Background()
	boom := apperr.Internal("内部错误")
	cases := []struct {
		name     string
		viewer   model.Viewer
		username string
		tx       TxRunner
		setup    func(*stubSet)
		wantKind apperr.Kind
	}{
		{"游客", stubGuestViewer(), "bob", nil, func(*stubSet) {}, apperr.KindUnauthenticated},
		{"目标不存在", stubViewer(), "nobody", nil, func(*stubSet) {}, apperr.KindNotFound},
		{
			name:     "取关写入失败",
			viewer:   stubViewer(),
			username: "bob",
			setup:    func(s *stubSet) { s.follows.deactivateErr = boom },
			wantKind: apperr.KindInternal,
		},
		{
			name:     "取消申请失败",
			viewer:   stubViewer(),
			username: "bob",
			setup:    func(s *stubSet) { s.requests.cancelErr = boom },
			wantKind: apperr.KindInternal,
		},
		{
			name:     "事务失败",
			viewer:   stubViewer(),
			username: "bob",
			tx:       errTx{err: boom},
			setup:    func(*stubSet) {},
			wantKind: apperr.KindInternal,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubs := newStubSet()
			tc.setup(stubs)
			svc := stubs.service()
			if tc.tx != nil {
				svc = stubs.serviceWith(func(deps *Deps) { deps.Tx = tc.tx })
			}
			_, err := svc.Unfollow(ctx, tc.viewer, tc.username)
			requireKind(t, err, tc.wantKind)
		})
	}
}

// TestRemoveFollower 验证移除粉丝只解除关注关系且错误可透传。
func TestRemoveFollower(t *testing.T) {
	ctx := context.Background()
	stubs := newStubSet()
	state, err := stubs.service().RemoveFollower(ctx, stubViewer(), "bob")
	requireOK(t, err)
	if state != model.FollowStateNone {
		t.Fatalf("移除粉丝状态 = %q，期望 none", state)
	}
	if len(stubs.follows.deactivates) != 1 {
		t.Fatalf("解除关注调用 = %d，期望 1", len(stubs.follows.deactivates))
	}
	deactivate := stubs.follows.deactivates[0]
	if deactivate.followerID != stubAuthorID || deactivate.followeeID != stubViewerID {
		t.Fatalf("解除关系方向 = (%d -> %d)，期望 (%d -> %d)",
			deactivate.followerID, deactivate.followeeID, stubAuthorID, stubViewerID)
	}

	stubs.follows.deactivateErr = apperr.Internal("解除失败")
	if _, err := stubs.service().RemoveFollower(ctx, stubViewer(), "bob"); requireKind(t, err, apperr.KindInternal) == nil {
		t.Fatal("解除关注错误应透传")
	}

	stubs = newStubSet()
	if _, err := stubs.service().RemoveFollower(ctx, stubGuestViewer(), "bob"); requireKind(t, err, apperr.KindUnauthenticated) == nil {
		t.Fatal("游客不能移除粉丝")
	}
}

// TestListFollowersAndFollowing 验证关注/粉丝列表分页、游标与作者摘要。
func TestListFollowersAndFollowing(t *testing.T) {
	ctx := context.Background()

	t.Run("粉丝列表", func(t *testing.T) {
		stubs := newStubSet()
		stubs.follows.followerRows = []FollowRow{
			{RowID: 900, UserID: stubAuthorID},
			{RowID: 800, UserID: stubOtherID},
		}
		page, err := stubs.service().ListFollowers(ctx, "bob", 0, 1)
		requireOK(t, err)
		if len(page.Items) != 1 || !page.HasMore || page.NextCursor != 900 {
			t.Fatalf("粉丝分页 = %+v，期望 1 条且游标 900", page)
		}
		if page.Items[0].RowID != 900 || page.Items[0].User.Username != "bob" {
			t.Fatalf("粉丝项 = %+v", page.Items[0])
		}
		if call := stubs.follows.listCalls["followers"]; call.userID != stubAuthorID || call.limit != 2 {
			t.Fatalf("粉丝列表参数 = %+v，期望 (bob, limit=2)", call)
		}
	})

	t.Run("关注列表", func(t *testing.T) {
		stubs := newStubSet()
		stubs.follows.followingRows = []FollowRow{{RowID: 700, UserID: stubOtherID}}
		page, err := stubs.service().ListFollowing(ctx, "bob", 123, 20)
		requireOK(t, err)
		if len(page.Items) != 1 || page.HasMore || page.NextCursor != 700 {
			t.Fatalf("关注分页 = %+v", page)
		}
		if call := stubs.follows.listCalls["following"]; call.cursor != 123 || call.limit != 21 {
			t.Fatalf("关注列表参数 = %+v，期望游标 123、limit 21", call)
		}
	})

	t.Run("游客可读空列表", func(t *testing.T) {
		stubs := newStubSet()
		page, err := stubs.service().ListFollowers(ctx, "bob", 0, 20)
		requireOK(t, err)
		if len(page.Items) != 0 || page.HasMore {
			t.Fatalf("空粉丝列表 = %+v", page)
		}
	})

	t.Run("用户不存在", func(t *testing.T) {
		stubs := newStubSet()
		_, err := stubs.service().ListFollowers(ctx, "nobody", 0, 20)
		requireKind(t, err, apperr.KindNotFound)
	})

	t.Run("仓储错误", func(t *testing.T) {
		cases := []struct {
			name  string
			setup func(*stubSet)
		}{
			{"粉丝列表失败", func(s *stubSet) { s.follows.followersErr = apperr.Internal("查询失败") }},
			{"关注列表失败", func(s *stubSet) { s.follows.followingListErr = apperr.Internal("查询失败") }},
			{"作者摘要失败", func(s *stubSet) {
				s.follows.followerRows = []FollowRow{{RowID: 1, UserID: stubAuthorID}}
				s.users.briefsErr = apperr.Internal("摘要失败")
			}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				stubs := newStubSet()
				tc.setup(stubs)
				var err error
				if tc.name == "关注列表失败" {
					_, err = stubs.service().ListFollowing(ctx, "bob", 0, 20)
				} else {
					_, err = stubs.service().ListFollowers(ctx, "bob", 0, 20)
				}
				requireKind(t, err, apperr.KindInternal)
			})
		}
	})
}

// 保证 fake 行类型与 domain 别名一致（编译期检查，防止端口签名漂移）。
var _ = domain.FollowRow{}
