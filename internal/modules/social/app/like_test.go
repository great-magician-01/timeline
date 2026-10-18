package app

import (
	"context"
	"strconv"
	"testing"

	"github.com/great-magician-01/timeline/internal/modules/social/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// TestLikePostSuccess 验证点赞帖子成功路径：计数 +1、通知帖子作者。
func TestLikePostSuccess(t *testing.T) {
	stubs := newStubSet()
	stubs.posts.counterValue = 4
	stubs.likes.activateID = 700
	svc := stubs.service()

	got, err := svc.LikePost(context.Background(), stubViewer(), stubPostID)
	requireOK(t, err)
	if !got.Liked || got.Count != 4 {
		t.Fatalf("点赞结果 = (%v, %d)，期望 (true, 4)", got.Liked, got.Count)
	}
	if len(stubs.likes.activates) != 1 {
		t.Fatalf("点赞写入调用 = %d，期望 1", len(stubs.likes.activates))
	}
	call := stubs.likes.activates[0]
	if call.userID != stubViewerID || call.targetType != domain.TargetPost || call.targetID != stubPostID {
		t.Fatalf("点赞写入参数 = %+v，期望 (%d, post, %d)", call, stubViewerID, stubPostID)
	}
	if call.id != 101 {
		t.Fatalf("点赞行 ID = %d，期望 101", call.id)
	}
	if !call.at.Equal(stubNow) {
		t.Fatalf("点赞时间 = %v，期望 %v", call.at, stubNow)
	}
	if want := (contracts.PostCounterInput{PostID: stubPostID, Counter: contracts.PostCounterLike, Delta: 1, At: stubNow}); len(stubs.posts.counters) != 1 || stubs.posts.counters[0] != want {
		t.Fatalf("帖子计数参数 = %+v，期望 %+v", stubs.posts.counters, want)
	}
	if len(stubs.notices.notifications) != 1 {
		t.Fatalf("通知条数 = %d，期望 1", len(stubs.notices.notifications))
	}
	notice := stubs.notices.notifications[0]
	if notice.RecipientID != stubAuthorID || notice.ActorID != stubViewerID {
		t.Fatalf("通知接收者/操作者 = (%d, %d)", notice.RecipientID, notice.ActorID)
	}
	if notice.Type != model.NoticeTypePostLike || notice.TargetType != model.NoticeTargetPost || notice.TargetID != stubPostID {
		t.Fatalf("通知内容 = %+v，期望帖子点赞通知", notice)
	}
	if notice.IdempotencyKey != "post_like:700" {
		t.Fatalf("通知幂等键 = %q，期望 post_like:700", notice.IdempotencyKey)
	}
	if notice.PostID == nil || *notice.PostID != stubPostID || notice.CommentID != nil {
		t.Fatalf("通知关联 = (post=%v, comment=%v)", notice.PostID, notice.CommentID)
	}
}

// TestLikePostIdempotent 验证重复点赞不重复计数、不重复通知，并返回当前计数。
func TestLikePostIdempotent(t *testing.T) {
	stubs := newStubSet()
	stubs.likes.activateChanged = false
	svc := stubs.service()

	got, err := svc.LikePost(context.Background(), stubViewer(), stubPostID)
	requireOK(t, err)
	if !got.Liked || got.Count != 3 {
		t.Fatalf("重复点赞结果 = (%v, %d)，期望 (true, 3)", got.Liked, got.Count)
	}
	if len(stubs.posts.counters) != 0 || len(stubs.notices.notifications) != 0 {
		t.Fatal("重复点赞不应调整计数或写通知")
	}
}

// TestUnlikePost 覆盖取消点赞真正生效与幂等两种路径。
func TestUnlikePost(t *testing.T) {
	t.Run("取消成功", func(t *testing.T) {
		stubs := newStubSet()
		stubs.posts.counterValue = 2
		got, err := stubs.service().UnlikePost(context.Background(), stubViewer(), stubPostID)
		requireOK(t, err)
		if got.Liked || got.Count != 2 {
			t.Fatalf("取消点赞结果 = (%v, %d)，期望 (false, 2)", got.Liked, got.Count)
		}
		if len(stubs.likes.deactivates) != 1 || stubs.likes.deactivates[0].targetType != domain.TargetPost {
			t.Fatalf("取消点赞写入参数 = %+v", stubs.likes.deactivates)
		}
		if want := (contracts.PostCounterInput{PostID: stubPostID, Counter: contracts.PostCounterLike, Delta: -1, At: stubNow}); len(stubs.posts.counters) != 1 || stubs.posts.counters[0] != want {
			t.Fatalf("计数参数 = %+v，期望 %+v", stubs.posts.counters, want)
		}
	})

	t.Run("重复取消幂等", func(t *testing.T) {
		stubs := newStubSet()
		stubs.likes.deactivateChanged = false
		got, err := stubs.service().UnlikePost(context.Background(), stubViewer(), stubPostID)
		requireOK(t, err)
		if got.Liked || got.Count != 3 {
			t.Fatalf("重复取消结果 = (%v, %d)，期望 (false, 3)", got.Liked, got.Count)
		}
		if len(stubs.posts.counters) != 0 {
			t.Fatal("重复取消不应调整计数")
		}
	})
}

// TestLikePostGuards 覆盖点赞帖子的鉴权、可见性与互动限制。
func TestLikePostGuards(t *testing.T) {
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
			setup:    func(s *stubSet) { s.posts.visiblePost.Status = model.PostStatusDraft },
			wantKind: apperr.KindForbidden,
		},
		{
			name:     "帖子作者停用",
			viewer:   stubViewer(),
			setup:    func(s *stubSet) { s.users.suspended[stubAuthorID] = true },
			wantKind: apperr.KindForbidden,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubs := newStubSet()
			tc.setup(stubs)
			_, err := stubs.service().LikePost(ctx, tc.viewer, stubPostID)
			requireKind(t, err, tc.wantKind)
		})
	}

	t.Run("作者点赞自己帖子不受停用影响", func(t *testing.T) {
		stubs := newStubSet()
		stubs.users.suspended[stubAuthorID] = true
		_, err := stubs.service().LikePost(ctx, stubAuthorViewer(), stubPostID)
		requireOK(t, err)
	})

	t.Run("限频", func(t *testing.T) {
		stubs := newStubSet()
		stubs.likeLimiter = newFixedLimiter(1)
		svc := stubs.service()
		svc.deps.LikeLimiter.Consume("user:" + strconv.FormatInt(stubViewerID, 10))
		_, err := svc.LikePost(ctx, stubViewer(), stubPostID)
		requireKind(t, err, apperr.KindRateLimited)
	})
}

// TestLikePostErrors 覆盖点赞写入链路错误。
func TestLikePostErrors(t *testing.T) {
	boom := apperr.Internal("内部错误")
	cases := []struct {
		name  string
		ids   IDGenerator
		tx    TxRunner
		setup func(*stubSet)
	}{
		{"ID 生成失败", errIDs{err: boom}, nil, func(*stubSet) {}},
		{"事务失败", nil, errTx{err: boom}, func(*stubSet) {}},
		{"点赞写入失败", nil, nil, func(s *stubSet) { s.likes.activateErr = boom }},
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
			if _, err := svc.LikePost(context.Background(), stubViewer(), stubPostID); err == nil {
				t.Fatal("期望返回错误")
			}
		})
	}

	t.Run("取消点赞时读取帖子失败", func(t *testing.T) {
		stubs := newStubSet()
		stubs.likes.deactivateChanged = false
		stubs.posts.getPostErr = apperr.Internal("读取帖子失败")
		_, err := stubs.service().UnlikePost(context.Background(), stubViewer(), stubPostID)
		requireKind(t, err, apperr.KindInternal)
	})
}

// TestLikeCommentSuccess 验证点赞评论成功路径：评论计数 +1、通知评论作者。
func TestLikeCommentSuccess(t *testing.T) {
	stubs := newStubSet()
	stubs.comments.comments[60] = stubCommentRow(60, stubPostID, stubOtherID)
	stubs.comments.likeCountValue = 9
	stubs.likes.activateID = 701
	svc := stubs.service()

	got, err := svc.LikeComment(context.Background(), stubViewer(), 60)
	requireOK(t, err)
	if !got.Liked || got.Count != 9 {
		t.Fatalf("评论点赞结果 = (%v, %d)，期望 (true, 9)", got.Liked, got.Count)
	}
	if len(stubs.comments.adjustLikes) != 1 {
		t.Fatalf("评论计数调用 = %d，期望 1", len(stubs.comments.adjustLikes))
	}
	if adjust := stubs.comments.adjustLikes[0]; adjust.id != 60 || adjust.delta != 1 || !adjust.at.Equal(stubNow) {
		t.Fatalf("评论计数参数 = %+v，期望 (60, +1, %v)", adjust, stubNow)
	}
	if len(stubs.notices.notifications) != 1 {
		t.Fatalf("通知条数 = %d，期望 1", len(stubs.notices.notifications))
	}
	notice := stubs.notices.notifications[0]
	if notice.RecipientID != stubOtherID || notice.Type != model.NoticeTypeCommentLike {
		t.Fatalf("通知内容 = %+v，期望评论点赞通知", notice)
	}
	if notice.TargetType != model.NoticeTargetComment || notice.TargetID != 60 {
		t.Fatalf("通知目标 = (%q, %d)，期望 (comment, 60)", notice.TargetType, notice.TargetID)
	}
	if notice.IdempotencyKey != "comment_like:701" {
		t.Fatalf("通知幂等键 = %q，期望 comment_like:701", notice.IdempotencyKey)
	}
	if notice.PostID == nil || *notice.PostID != stubPostID || notice.CommentID == nil || *notice.CommentID != 60 {
		t.Fatalf("通知关联 = (post=%v, comment=%v)", notice.PostID, notice.CommentID)
	}
}

// TestLikeCommentIdempotent 验证重复点赞评论不重复计数并返回当前计数。
func TestLikeCommentIdempotent(t *testing.T) {
	stubs := newStubSet()
	comment := stubCommentRow(60, stubPostID, stubOtherID)
	comment.LikeCount = 2
	stubs.comments.comments[60] = comment
	stubs.likes.activateChanged = false
	got, err := stubs.service().LikeComment(context.Background(), stubViewer(), 60)
	requireOK(t, err)
	if !got.Liked || got.Count != 2 {
		t.Fatalf("重复点赞结果 = (%v, %d)，期望 (true, 2)", got.Liked, got.Count)
	}
	if len(stubs.comments.adjustLikes) != 0 || len(stubs.notices.notifications) != 0 {
		t.Fatal("重复点赞不应调整计数或写通知")
	}
}

// TestUnlikeComment 覆盖取消点赞评论的生效与幂等路径。
func TestUnlikeComment(t *testing.T) {
	t.Run("取消成功", func(t *testing.T) {
		stubs := newStubSet()
		stubs.comments.comments[60] = stubCommentRow(60, stubPostID, stubOtherID)
		stubs.comments.likeCountValue = 1
		got, err := stubs.service().UnlikeComment(context.Background(), stubViewer(), 60)
		requireOK(t, err)
		if got.Liked || got.Count != 1 {
			t.Fatalf("取消点赞结果 = (%v, %d)，期望 (false, 1)", got.Liked, got.Count)
		}
		if len(stubs.comments.adjustLikes) != 1 || stubs.comments.adjustLikes[0].delta != -1 {
			t.Fatalf("评论计数参数 = %+v，期望 -1", stubs.comments.adjustLikes)
		}
	})

	t.Run("重复取消幂等", func(t *testing.T) {
		stubs := newStubSet()
		comment := stubCommentRow(60, stubPostID, stubOtherID)
		comment.LikeCount = 4
		stubs.comments.comments[60] = comment
		stubs.likes.deactivateChanged = false
		got, err := stubs.service().UnlikeComment(context.Background(), stubViewer(), 60)
		requireOK(t, err)
		if got.Liked || got.Count != 4 {
			t.Fatalf("重复取消结果 = (%v, %d)，期望 (false, 4)", got.Liked, got.Count)
		}
		if len(stubs.comments.adjustLikes) != 0 {
			t.Fatal("重复取消不应调整计数")
		}
	})
}

// TestLikeCommentGuards 覆盖评论点赞的鉴权与状态限制。
func TestLikeCommentGuards(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name     string
		viewer   model.Viewer
		setup    func(*stubSet)
		wantKind apperr.Kind
	}{
		{
			name:     "评论不存在",
			viewer:   stubViewer(),
			setup:    func(*stubSet) {},
			wantKind: apperr.KindNotFound,
		},
		{
			name:   "评论已删除",
			viewer: stubViewer(),
			setup: func(s *stubSet) {
				row := stubCommentRow(60, stubPostID, stubOtherID)
				row.Status = model.CommentStatusDeleted
				s.comments.comments[60] = row
			},
			wantKind: apperr.KindForbidden,
		},
		{
			name:   "评论作者停用",
			viewer: stubViewer(),
			setup: func(s *stubSet) {
				s.comments.comments[60] = stubCommentRow(60, stubPostID, stubSuspendedID)
			},
			wantKind: apperr.KindForbidden,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubs := newStubSet()
			tc.setup(stubs)
			_, err := stubs.service().LikeComment(ctx, tc.viewer, 60)
			requireKind(t, err, tc.wantKind)
		})
	}

	t.Run("作者点赞自己评论不受停用影响", func(t *testing.T) {
		stubs := newStubSet()
		stubs.comments.comments[60] = stubCommentRow(60, stubPostID, stubOtherID)
		stubs.users.suspended[stubOtherID] = true
		viewer := model.ViewerFrom(model.User{ID: stubOtherID, Status: model.StatusActive})
		_, err := stubs.service().LikeComment(ctx, viewer, 60)
		requireOK(t, err)
	})

	t.Run("限频", func(t *testing.T) {
		stubs := newStubSet()
		stubs.comments.comments[60] = stubCommentRow(60, stubPostID, stubOtherID)
		stubs.likeLimiter = newFixedLimiter(1)
		svc := stubs.service()
		svc.deps.LikeLimiter.Consume("user:" + strconv.FormatInt(stubViewerID, 10))
		_, err := svc.LikeComment(ctx, stubViewer(), 60)
		requireKind(t, err, apperr.KindRateLimited)
	})
}

// TestLikeCommentErrors 覆盖评论点赞写入链路错误。
func TestLikeCommentErrors(t *testing.T) {
	boom := apperr.Internal("内部错误")
	cases := []struct {
		name  string
		setup func(*stubSet)
	}{
		{"点赞写入失败", func(s *stubSet) {
			s.comments.comments[60] = stubCommentRow(60, stubPostID, stubOtherID)
			s.likes.activateErr = boom
		}},
		{"计数失败", func(s *stubSet) {
			s.comments.comments[60] = stubCommentRow(60, stubPostID, stubOtherID)
			s.comments.adjustLikeErr = boom
		}},
		{"通知失败", func(s *stubSet) {
			s.comments.comments[60] = stubCommentRow(60, stubPostID, stubOtherID)
			s.notices.createErr = boom
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubs := newStubSet()
			tc.setup(stubs)
			_, err := stubs.service().LikeComment(context.Background(), stubViewer(), 60)
			if err == nil {
				t.Fatal("期望返回错误")
			}
		})
	}

	t.Run("取消点赞写入失败", func(t *testing.T) {
		stubs := newStubSet()
		stubs.comments.comments[60] = stubCommentRow(60, stubPostID, stubOtherID)
		stubs.likes.deactivateErr = boom
		_, err := stubs.service().UnlikeComment(context.Background(), stubViewer(), 60)
		requireKind(t, err, apperr.KindInternal)
	})
}
