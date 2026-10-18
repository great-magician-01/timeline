package app

import (
	"context"
	"strconv"
	"testing"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// TestRepostSuccess 验证纯转发成功路径：计数 +1、通知帖子作者。
func TestRepostSuccess(t *testing.T) {
	stubs := newStubSet()
	stubs.posts.counterValue = 6
	stubs.reposts.activateID = 800
	svc := stubs.service()

	got, err := svc.Repost(context.Background(), stubViewer(), stubPostID)
	requireOK(t, err)
	if !got.Reposted || got.Count != 6 {
		t.Fatalf("转发结果 = (%v, %d)，期望 (true, 6)", got.Reposted, got.Count)
	}
	if len(stubs.reposts.activates) != 1 {
		t.Fatalf("转发写入调用 = %d，期望 1", len(stubs.reposts.activates))
	}
	call := stubs.reposts.activates[0]
	if call.userID != stubViewerID || call.postID != stubPostID || call.id != 101 || !call.at.Equal(stubNow) {
		t.Fatalf("转发写入参数 = %+v", call)
	}
	if call.authorIsAI {
		t.Fatal("人类访问者转发应写入 author_is_ai=false")
	}
	if want := (contracts.PostCounterInput{PostID: stubPostID, Counter: contracts.PostCounterRepost, Delta: 1, At: stubNow}); len(stubs.posts.counters) != 1 || stubs.posts.counters[0] != want {
		t.Fatalf("帖子计数参数 = %+v，期望 %+v", stubs.posts.counters, want)
	}
	if len(stubs.notices.notifications) != 1 {
		t.Fatalf("通知条数 = %d，期望 1", len(stubs.notices.notifications))
	}
	notice := stubs.notices.notifications[0]
	if notice.RecipientID != stubAuthorID || notice.ActorID != stubViewerID {
		t.Fatalf("通知接收者/操作者 = (%d, %d)", notice.RecipientID, notice.ActorID)
	}
	if notice.Type != model.NoticeTypeRepost || notice.TargetType != model.NoticeTargetPost || notice.TargetID != stubPostID {
		t.Fatalf("通知内容 = %+v，期望转发通知", notice)
	}
	if notice.IdempotencyKey != "repost:800" {
		t.Fatalf("通知幂等键 = %q，期望 repost:800", notice.IdempotencyKey)
	}
	if notice.PostID == nil || *notice.PostID != stubPostID || notice.CommentID != nil {
		t.Fatalf("通知关联 = (post=%v, comment=%v)", notice.PostID, notice.CommentID)
	}
}

// TestRepostIdempotent 验证重复转发不重复计数并返回当前计数。
func TestRepostIdempotent(t *testing.T) {
	stubs := newStubSet()
	stubs.reposts.activateChanged = false
	got, err := stubs.service().Repost(context.Background(), stubViewer(), stubPostID)
	requireOK(t, err)
	if !got.Reposted || got.Count != 5 {
		t.Fatalf("重复转发结果 = (%v, %d)，期望 (true, 5)", got.Reposted, got.Count)
	}
	if len(stubs.posts.counters) != 0 || len(stubs.notices.notifications) != 0 {
		t.Fatal("重复转发不应调整计数或写通知")
	}
}

// TestRepostGuards 覆盖转发鉴权、可见性与公开性限制。
func TestRepostGuards(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name     string
		viewer   model.Viewer
		setup    func(*stubSet)
		wantKind apperr.Kind
	}{
		{"游客", stubGuestViewer(), func(*stubSet) {}, apperr.KindUnauthenticated},
		{"停用账号", stubSuspendedViewer(), func(*stubSet) {}, apperr.KindSuspended},
		{
			name:     "AI 身份",
			viewer:   model.ViewerFrom(model.User{ID: stubAIID, IsAI: true, Status: model.StatusActive}),
			setup:    func(*stubSet) {},
			wantKind: apperr.KindForbidden,
		},
		{
			name:     "帖子不可见",
			viewer:   stubViewer(),
			setup:    func(s *stubSet) { s.posts.visibleErr = apperr.NotFound("内容不存在或无权访问") },
			wantKind: apperr.KindNotFound,
		},
		{
			name:     "帖子未发布",
			viewer:   stubViewer(),
			setup:    func(s *stubSet) { s.posts.visiblePost.Status = model.PostStatusScheduled },
			wantKind: apperr.KindForbidden,
		},
		{
			name:     "帖子作者停用",
			viewer:   stubViewer(),
			setup:    func(s *stubSet) { s.users.suspended[stubAuthorID] = true },
			wantKind: apperr.KindForbidden,
		},
		{
			name:     "非公开帖子不能转发",
			viewer:   stubViewer(),
			setup:    func(s *stubSet) { s.posts.visiblePost.Visibility = model.VisibilityFollowers },
			wantKind: apperr.KindForbidden,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubs := newStubSet()
			tc.setup(stubs)
			_, err := stubs.service().Repost(ctx, tc.viewer, stubPostID)
			requireKind(t, err, tc.wantKind)
		})
	}

	t.Run("作者转发自己帖子不受停用影响", func(t *testing.T) {
		stubs := newStubSet()
		stubs.users.suspended[stubAuthorID] = true
		_, err := stubs.service().Repost(ctx, stubAuthorViewer(), stubPostID)
		requireOK(t, err)
	})

	t.Run("限频", func(t *testing.T) {
		stubs := newStubSet()
		stubs.likeLimiter = newFixedLimiter(1)
		svc := stubs.service()
		svc.deps.LikeLimiter.Consume("user:" + strconv.FormatInt(stubViewerID, 10))
		_, err := svc.Repost(ctx, stubViewer(), stubPostID)
		requireKind(t, err, apperr.KindRateLimited)
	})
}

// TestRepostErrors 覆盖转发写入链路错误。
func TestRepostErrors(t *testing.T) {
	boom := apperr.Internal("内部错误")
	cases := []struct {
		name  string
		ids   IDGenerator
		tx    TxRunner
		setup func(*stubSet)
	}{
		{"ID 生成失败", errIDs{err: boom}, nil, func(*stubSet) {}},
		{"事务失败", nil, errTx{err: boom}, func(*stubSet) {}},
		{"转发写入失败", nil, nil, func(s *stubSet) { s.reposts.activateErr = boom }},
		{"计数失败", nil, nil, func(s *stubSet) { s.posts.counterErr = boom }},
		{"通知失败", nil, nil, func(s *stubSet) { s.notices.createErr = boom }},
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
			if _, err := svc.Repost(context.Background(), stubViewer(), stubPostID); err == nil {
				t.Fatal("期望返回错误")
			}
		})
	}
}

// TestUnrepost 覆盖取消转发的生效、幂等与目标不存在幂等。
func TestUnrepost(t *testing.T) {
	ctx := context.Background()

	t.Run("取消成功", func(t *testing.T) {
		stubs := newStubSet()
		stubs.posts.counterValue = 4
		got, err := stubs.service().Unrepost(ctx, stubViewer(), stubPostID)
		requireOK(t, err)
		if got.Reposted || got.Count != 4 {
			t.Fatalf("取消转发结果 = (%v, %d)，期望 (false, 4)", got.Reposted, got.Count)
		}
		if len(stubs.reposts.deactivates) != 1 || stubs.reposts.deactivates[0].postID != stubPostID {
			t.Fatalf("取消转发参数 = %+v", stubs.reposts.deactivates)
		}
		if want := (contracts.PostCounterInput{PostID: stubPostID, Counter: contracts.PostCounterRepost, Delta: -1, At: stubNow}); len(stubs.posts.counters) != 1 || stubs.posts.counters[0] != want {
			t.Fatalf("计数参数 = %+v，期望 %+v", stubs.posts.counters, want)
		}
	})

	t.Run("重复取消幂等", func(t *testing.T) {
		stubs := newStubSet()
		stubs.reposts.deactivateChanged = false
		got, err := stubs.service().Unrepost(ctx, stubViewer(), stubPostID)
		requireOK(t, err)
		if got.Reposted || got.Count != 5 {
			t.Fatalf("重复取消结果 = (%v, %d)，期望 (false, 5)", got.Reposted, got.Count)
		}
		if len(stubs.posts.counters) != 0 {
			t.Fatal("重复取消不应调整计数")
		}
	})

	t.Run("目标帖子不存在时幂等", func(t *testing.T) {
		stubs := newStubSet()
		stubs.reposts.deactivateChanged = false
		stubs.posts.getPostErr = apperr.NotFound("内容不存在或无权访问")
		got, err := stubs.service().Unrepost(ctx, stubViewer(), stubPostID)
		requireOK(t, err)
		if got.Reposted || got.Count != 0 {
			t.Fatalf("结果 = (%v, %d)，期望 (false, 0)", got.Reposted, got.Count)
		}
	})

	t.Run("非公开帖子不能取消转发", func(t *testing.T) {
		stubs := newStubSet()
		stubs.posts.visiblePost.Visibility = model.VisibilityPrivate
		_, err := stubs.service().Unrepost(ctx, stubViewer(), stubPostID)
		requireKind(t, err, apperr.KindForbidden)
	})

	t.Run("写入失败", func(t *testing.T) {
		stubs := newStubSet()
		stubs.reposts.deactivateErr = apperr.Internal("取消转发失败")
		_, err := stubs.service().Unrepost(ctx, stubViewer(), stubPostID)
		requireKind(t, err, apperr.KindInternal)
	})
}
