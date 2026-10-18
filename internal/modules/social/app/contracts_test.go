package app

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/social/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/audit"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// TestLoadActor 验证以指定身份执行动作时的账号状态校验。
func TestLoadActor(t *testing.T) {
	ctx := context.Background()
	stubs := newStubSet()
	svc := stubs.service()

	if _, err := svc.loadActor(ctx, 0); requireKind(t, err, apperr.KindUnauthenticated) == nil {
		t.Fatal("非法作者 ID 应返回未登录错误")
	}
	if _, err := svc.loadActor(ctx, 777); requireKind(t, err, apperr.KindNotFound) == nil {
		t.Fatal("不存在的作者应返回 not_found")
	}
	if _, err := svc.loadActor(ctx, stubSuspendedID); requireKind(t, err, apperr.KindSuspended) == nil {
		t.Fatal("停用账号不能执行动作")
	}
	actor, err := svc.loadActor(ctx, stubAIID)
	requireOK(t, err)
	if !actor.IsAI {
		t.Fatalf("AI 身份应可作为动作主体: %+v", actor)
	}

	stubs.users.usersErr = apperr.Internal("账号读取失败")
	if _, err := svc.loadActor(ctx, stubViewerID); requireKind(t, err, apperr.KindInternal) == nil {
		t.Fatal("账号读取错误应透传")
	}
}

// TestEventTime 验证显式时间优先、零值回退应用时钟。
func TestEventTime(t *testing.T) {
	svc := newStubSet().service()
	explicit := stubNow.Add(-2 * time.Hour)
	if got := svc.eventTime(explicit); !got.Equal(explicit) {
		t.Fatalf("显式时间被覆盖: %v，期望 %v", got, explicit)
	}
	if got := svc.eventTime(time.Time{}); !got.Equal(stubNow) {
		t.Fatalf("零值时间应回退 Clock: %v，期望 %v", got, stubNow)
	}
}

// TestRequireActorVisiblePost 验证动作主体视角的可见性校验。
func TestRequireActorVisiblePost(t *testing.T) {
	ctx := context.Background()
	actor, err := newStubSet().users.GetUser(ctx, stubViewerID)
	requireOK(t, err)

	t.Run("可见公开帖子", func(t *testing.T) {
		stubs := newStubSet()
		post, err := stubs.service().requireActorVisiblePost(ctx, actor, stubPostID)
		requireOK(t, err)
		if post.ID != stubPostID {
			t.Fatalf("帖子 = %+v", post)
		}
	})

	t.Run("帖子不存在", func(t *testing.T) {
		stubs := newStubSet()
		stubs.posts.getPostErr = apperr.NotFound("内容不存在或无权访问")
		_, err := stubs.service().requireActorVisiblePost(ctx, actor, stubPostID)
		requireKind(t, err, apperr.KindNotFound)
	})

	t.Run("AI 只能看公开内容", func(t *testing.T) {
		stubs := newStubSet()
		aiActor, err := stubs.users.GetUser(ctx, stubAIID)
		requireOK(t, err)
		stubs.posts.getPost.Visibility = model.VisibilityFollowers
		_, err = stubs.service().requireActorVisiblePost(ctx, aiActor, stubPostID)
		requireKind(t, err, apperr.KindNotFound)
	})

	t.Run("关注集合读取失败", func(t *testing.T) {
		stubs := newStubSet()
		stubs.follows.followingSetErr = apperr.Internal("关注集合失败")
		_, err := stubs.service().requireActorVisiblePost(ctx, actor, stubPostID)
		requireKind(t, err, apperr.KindInternal)
	})

	t.Run("非公开帖子对非作者不可见", func(t *testing.T) {
		stubs := newStubSet()
		stubs.posts.getPost.Visibility = model.VisibilityPrivate
		_, err := stubs.service().requireActorVisiblePost(ctx, actor, stubPostID)
		requireKind(t, err, apperr.KindNotFound)
	})
}

// TestRequireActorInteractivePost 验证互动条件（已发布、作者未停用）。
func TestRequireActorInteractivePost(t *testing.T) {
	ctx := context.Background()
	stubs := newStubSet()
	// 作者本人能看到草稿，但草稿不可互动。
	actor, err := stubs.users.GetUser(ctx, stubAuthorID)
	requireOK(t, err)

	stubs.posts.getPost.Status = model.PostStatusDraft
	_, err = stubs.service().requireActorInteractivePost(ctx, actor, stubPostID)
	requireKind(t, err, apperr.KindForbidden)
}

// TestCreateCommentForActor 验证端口写路径的评论创建与时间参数。
func TestCreateCommentForActor(t *testing.T) {
	ctx := context.Background()

	t.Run("显式时间", func(t *testing.T) {
		stubs := newStubSet()
		at := stubNow.Add(-30 * time.Minute)
		created, err := stubs.service().CreateCommentForActor(ctx, contracts.ActorCommentInput{
			ActorID:       stubViewerID,
			PostID:        stubPostID,
			ContentFormat: "richtext",
			ContentSource: "<p>端口评论</p>",
			At:            at,
		})
		requireOK(t, err)
		if created.ID != 101 || created.AuthorID != stubViewerID {
			t.Fatalf("创建评论 = %+v", created)
		}
		if !created.CreatedAt.Equal(at) {
			t.Fatalf("评论时间 = %v，期望调用方传入的 %v", created.CreatedAt, at)
		}
		if len(stubs.notices.notifications) != 1 || !stubs.notices.notifications[0].At.Equal(at) {
			t.Fatalf("通知时间未复用业务时间: %+v", stubs.notices.notifications)
		}
	})

	t.Run("零值时间回退时钟", func(t *testing.T) {
		stubs := newStubSet()
		created, err := stubs.service().CreateCommentForActor(ctx, contracts.ActorCommentInput{
			ActorID:       stubAIID,
			PostID:        stubPostID,
			ContentFormat: "richtext",
			ContentSource: "<p>AI 评论</p>",
		})
		requireOK(t, err)
		if !created.CreatedAt.Equal(stubNow) {
			t.Fatalf("评论时间 = %v，期望回退时钟 %v", created.CreatedAt, stubNow)
		}
	})

	t.Run("回复父评论", func(t *testing.T) {
		stubs := newStubSet()
		stubs.comments.comments[55] = stubCommentRow(55, stubPostID, stubAuthorID)
		parentID := int64(55)
		created, err := stubs.service().CreateCommentForActor(ctx, contracts.ActorCommentInput{
			ActorID:       stubViewerID,
			PostID:        stubPostID,
			ParentID:      &parentID,
			ContentFormat: "richtext",
			ContentSource: "<p>端口回复</p>",
			At:            stubNow,
		})
		requireOK(t, err)
		if created.ParentCommentID == nil || *created.ParentCommentID != 55 || *created.RootCommentID != 55 {
			t.Fatalf("回复线程位置 = %+v", created)
		}
	})

	t.Run("作者非法", func(t *testing.T) {
		stubs := newStubSet()
		_, err := stubs.service().CreateCommentForActor(ctx, contracts.ActorCommentInput{
			ActorID: 777, PostID: stubPostID, ContentFormat: "richtext", ContentSource: "<p>x</p>",
		})
		requireKind(t, err, apperr.KindNotFound)
	})

	t.Run("格式非法", func(t *testing.T) {
		stubs := newStubSet()
		_, err := stubs.service().CreateCommentForActor(ctx, contracts.ActorCommentInput{
			ActorID: stubViewerID, PostID: stubPostID, ContentFormat: "html", ContentSource: "<p>x</p>",
		})
		requireKind(t, err, apperr.KindValidation)
	})

	t.Run("帖子不可见", func(t *testing.T) {
		stubs := newStubSet()
		stubs.posts.getPostErr = apperr.NotFound("内容不存在或无权访问")
		_, err := stubs.service().CreateCommentForActor(ctx, contracts.ActorCommentInput{
			ActorID: stubViewerID, PostID: stubPostID, ContentFormat: "richtext", ContentSource: "<p>x</p>",
		})
		requireKind(t, err, apperr.KindNotFound)
	})

	t.Run("AI 访问非公开帖子", func(t *testing.T) {
		stubs := newStubSet()
		stubs.posts.getPost.Visibility = model.VisibilityFollowers
		_, err := stubs.service().CreateCommentForActor(ctx, contracts.ActorCommentInput{
			ActorID: stubAIID, PostID: stubPostID, ContentFormat: "richtext", ContentSource: "<p>x</p>",
		})
		requireKind(t, err, apperr.KindNotFound)
	})
}

// TestDeleteCommentAsAdmin 验证管理员删除评论的计数、提及、审计与幂等。
func TestDeleteCommentAsAdmin(t *testing.T) {
	ctx := context.Background()

	t.Run("成功删除并写审计", func(t *testing.T) {
		stubs := newStubSet()
		stubs.comments.comments[50] = stubCommentRow(50, stubPostID, stubOtherID)
		err := stubs.service().DeleteCommentAsAdmin(ctx, contracts.DeleteCommentInput{
			ActorID: stubAdminID, CommentID: 50, At: stubNow,
		})
		requireOK(t, err)
		if len(stubs.comments.markDeletedCalls) != 1 || !stubs.comments.markDeletedCalls[0].byAdmin {
			t.Fatalf("删除参数 = %+v，期望管理员标记", stubs.comments.markDeletedCalls)
		}
		if want := (contracts.PostCounterInput{PostID: stubPostID, Counter: contracts.PostCounterComment, Delta: -1, At: stubNow}); len(stubs.posts.counters) != 1 || stubs.posts.counters[0] != want {
			t.Fatalf("计数参数 = %+v，期望 %+v", stubs.posts.counters, want)
		}
		if len(stubs.notices.deactivated) != 1 || stubs.notices.deactivated[0].sourceID != 50 {
			t.Fatalf("提及失效 = %+v", stubs.notices.deactivated)
		}
		if len(stubs.audit.entries) != 1 {
			t.Fatalf("审计条数 = %d，期望 1", len(stubs.audit.entries))
		}
		entry := stubs.audit.entries[0]
		if entry.AdminID != stubAdminID || entry.Action != audit.ActionCommentDelete || entry.TargetID == nil || *entry.TargetID != 50 {
			t.Fatalf("审计内容 = %+v", entry)
		}
	})

	t.Run("已删除评论幂等", func(t *testing.T) {
		stubs := newStubSet()
		row := stubCommentRow(50, stubPostID, stubOtherID)
		row.Status = model.CommentStatusDeleted
		stubs.comments.comments[50] = row
		requireOK(t, stubs.service().DeleteCommentAsAdmin(ctx, contracts.DeleteCommentInput{
			ActorID: stubAdminID, CommentID: 50,
		}))
		if len(stubs.comments.markDeletedCalls) != 0 || len(stubs.audit.entries) != 0 {
			t.Fatal("已删除评论不应重复处置")
		}
	})

	t.Run("并发已删除幂等", func(t *testing.T) {
		stubs := newStubSet()
		stubs.comments.comments[50] = stubCommentRow(50, stubPostID, stubOtherID)
		stubs.comments.markDeletedChanged = false
		requireOK(t, stubs.service().DeleteCommentAsAdmin(ctx, contracts.DeleteCommentInput{
			ActorID: stubAdminID, CommentID: 50,
		}))
		if len(stubs.posts.counters) != 0 || len(stubs.audit.entries) != 0 {
			t.Fatal("未真正删除时不应回退计数或写审计")
		}
	})

	t.Run("回复删除回退父评论回复数", func(t *testing.T) {
		stubs := newStubSet()
		parentID := int64(55)
		reply := stubCommentRow(50, stubPostID, stubOtherID)
		reply.ParentCommentID = &parentID
		stubs.comments.comments[50] = reply
		requireOK(t, stubs.service().DeleteCommentAsAdmin(ctx, contracts.DeleteCommentInput{
			ActorID: stubAdminID, CommentID: 50,
		}))
		if len(stubs.comments.adjustReplies) != 1 || stubs.comments.adjustReplies[0].delta != -1 {
			t.Fatalf("父评论回复计数 = %+v，期望 -1", stubs.comments.adjustReplies)
		}
	})
}

// TestDeleteCommentAsAdminErrors 覆盖管理员删除的权限与依赖错误。
func TestDeleteCommentAsAdminErrors(t *testing.T) {
	ctx := context.Background()
	boom := apperr.Internal("内部错误")
	cases := []struct {
		name     string
		actorID  int64
		setup    func(*stubSet)
		wantKind apperr.Kind
	}{
		{"普通用户无权删除", stubViewerID, func(s *stubSet) {
			s.comments.comments[50] = stubCommentRow(50, stubPostID, stubOtherID)
		}, apperr.KindForbidden},
		{"AI 管理员不能删除", stubAIID, func(s *stubSet) {
			aiAdmin := s.users.byID[stubAIID]
			aiAdmin.Role = model.RoleAdmin
			s.users.byID[stubAIID] = aiAdmin
		}, apperr.KindForbidden},
		{"评论不存在", stubAdminID, func(*stubSet) {}, apperr.KindNotFound},
		{"所属帖子不存在", stubAdminID, func(s *stubSet) {
			s.comments.comments[50] = stubCommentRow(50, stubPostID, stubOtherID)
			s.posts.getPostErr = apperr.NotFound("内容不存在或无权访问")
		}, apperr.KindNotFound},
		{"读取评论失败", stubAdminID, func(s *stubSet) { s.comments.getForUpdateErr = boom }, apperr.KindInternal},
		{"删除写入失败", stubAdminID, func(s *stubSet) {
			s.comments.comments[50] = stubCommentRow(50, stubPostID, stubOtherID)
			s.comments.markDeletedErr = boom
		}, apperr.KindInternal},
		{"计数回退失败", stubAdminID, func(s *stubSet) {
			s.comments.comments[50] = stubCommentRow(50, stubPostID, stubOtherID)
			s.posts.counterErr = boom
		}, apperr.KindInternal},
		{"回复计数回退失败", stubAdminID, func(s *stubSet) {
			parentID := int64(55)
			reply := stubCommentRow(50, stubPostID, stubOtherID)
			reply.ParentCommentID = &parentID
			s.comments.comments[50] = reply
			s.comments.adjustReplyErr = boom
		}, apperr.KindInternal},
		{"提及失效失败", stubAdminID, func(s *stubSet) {
			s.comments.comments[50] = stubCommentRow(50, stubPostID, stubOtherID)
			s.notices.deactivateErr = boom
		}, apperr.KindInternal},
		{"审计失败", stubAdminID, func(s *stubSet) {
			s.comments.comments[50] = stubCommentRow(50, stubPostID, stubOtherID)
			s.audit.err = boom
		}, apperr.KindInternal},
		{"作者账号停用", stubSuspendedID, func(*stubSet) {}, apperr.KindSuspended},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubs := newStubSet()
			tc.setup(stubs)
			err := stubs.service().DeleteCommentAsAdmin(ctx, contracts.DeleteCommentInput{
				ActorID: tc.actorID, CommentID: 50,
			})
			requireKind(t, err, tc.wantKind)
		})
	}
}

// TestLikeForActor 验证端口点赞路径。
func TestLikeForActor(t *testing.T) {
	ctx := context.Background()

	t.Run("点赞帖子", func(t *testing.T) {
		stubs := newStubSet()
		stubs.posts.counterValue = 7
		active, count, err := stubs.service().LikeForActor(ctx, contracts.LikeInput{
			ActorID: stubViewerID, TargetType: domain.TargetPost, TargetID: stubPostID, At: stubNow,
		})
		requireOK(t, err)
		if !active || count != 7 {
			t.Fatalf("结果 = (%v, %d)，期望 (true, 7)", active, count)
		}
	})

	t.Run("点赞评论", func(t *testing.T) {
		stubs := newStubSet()
		stubs.comments.comments[60] = stubCommentRow(60, stubPostID, stubOtherID)
		stubs.comments.likeCountValue = 2
		active, count, err := stubs.service().LikeForActor(ctx, contracts.LikeInput{
			ActorID: stubViewerID, TargetType: domain.TargetComment, TargetID: 60, At: stubNow,
		})
		requireOK(t, err)
		if !active || count != 2 {
			t.Fatalf("结果 = (%v, %d)，期望 (true, 2)", active, count)
		}
	})

	t.Run("AI 可执行动作", func(t *testing.T) {
		stubs := newStubSet()
		active, _, err := stubs.service().LikeForActor(ctx, contracts.LikeInput{
			ActorID: stubAIID, TargetType: domain.TargetPost, TargetID: stubPostID,
		})
		requireOK(t, err)
		if !active {
			t.Fatal("AI 点赞应生效")
		}
	})

	t.Run("目标类型非法", func(t *testing.T) {
		stubs := newStubSet()
		_, _, err := stubs.service().LikeForActor(ctx, contracts.LikeInput{
			ActorID: stubViewerID, TargetType: "user", TargetID: 1,
		})
		requireKind(t, err, apperr.KindValidation)
	})

	t.Run("评论已删除", func(t *testing.T) {
		stubs := newStubSet()
		row := stubCommentRow(60, stubPostID, stubOtherID)
		row.Status = model.CommentStatusDeleted
		stubs.comments.comments[60] = row
		_, _, err := stubs.service().LikeForActor(ctx, contracts.LikeInput{
			ActorID: stubViewerID, TargetType: domain.TargetComment, TargetID: 60,
		})
		requireKind(t, err, apperr.KindForbidden)
	})

	t.Run("评论不存在", func(t *testing.T) {
		stubs := newStubSet()
		_, _, err := stubs.service().LikeForActor(ctx, contracts.LikeInput{
			ActorID: stubViewerID, TargetType: domain.TargetComment, TargetID: 60,
		})
		requireKind(t, err, apperr.KindNotFound)
	})

	t.Run("作者停用", func(t *testing.T) {
		stubs := newStubSet()
		_, _, err := stubs.service().LikeForActor(ctx, contracts.LikeInput{
			ActorID: stubSuspendedID, TargetType: domain.TargetPost, TargetID: stubPostID,
		})
		requireKind(t, err, apperr.KindSuspended)
	})
}

// TestUnlikeForActor 验证端口取消点赞的计数与幂等语义。
func TestUnlikeForActor(t *testing.T) {
	ctx := context.Background()

	t.Run("帖子取消成功", func(t *testing.T) {
		stubs := newStubSet()
		stubs.posts.counterValue = 2
		active, count, err := stubs.service().UnlikeForActor(ctx, contracts.LikeInput{
			ActorID: stubViewerID, TargetType: domain.TargetPost, TargetID: stubPostID, At: stubNow,
		})
		requireOK(t, err)
		if active || count != 2 {
			t.Fatalf("结果 = (%v, %d)，期望 (false, 2)", active, count)
		}
	})

	t.Run("帖子未点赞返回当前计数", func(t *testing.T) {
		stubs := newStubSet()
		stubs.likes.deactivateChanged = false
		active, count, err := stubs.service().UnlikeForActor(ctx, contracts.LikeInput{
			ActorID: stubViewerID, TargetType: domain.TargetPost, TargetID: stubPostID,
		})
		requireOK(t, err)
		if active || count != 3 {
			t.Fatalf("结果 = (%v, %d)，期望 (false, 3)", active, count)
		}
	})

	t.Run("评论取消成功", func(t *testing.T) {
		stubs := newStubSet()
		stubs.comments.comments[60] = stubCommentRow(60, stubPostID, stubOtherID)
		stubs.comments.likeCountValue = 1
		active, count, err := stubs.service().UnlikeForActor(ctx, contracts.LikeInput{
			ActorID: stubViewerID, TargetType: domain.TargetComment, TargetID: 60,
		})
		requireOK(t, err)
		if active || count != 1 {
			t.Fatalf("结果 = (%v, %d)，期望 (false, 1)", active, count)
		}
	})

	t.Run("评论未点赞返回当前计数", func(t *testing.T) {
		stubs := newStubSet()
		comment := stubCommentRow(60, stubPostID, stubOtherID)
		comment.LikeCount = 4
		stubs.comments.comments[60] = comment
		stubs.likes.deactivateChanged = false
		active, count, err := stubs.service().UnlikeForActor(ctx, contracts.LikeInput{
			ActorID: stubViewerID, TargetType: domain.TargetComment, TargetID: 60,
		})
		requireOK(t, err)
		if active || count != 4 {
			t.Fatalf("结果 = (%v, %d)，期望 (false, 4)", active, count)
		}
	})

	t.Run("目标类型非法", func(t *testing.T) {
		stubs := newStubSet()
		_, _, err := stubs.service().UnlikeForActor(ctx, contracts.LikeInput{
			ActorID: stubViewerID, TargetType: "user", TargetID: 1,
		})
		requireKind(t, err, apperr.KindValidation)
	})

	t.Run("数据库错误", func(t *testing.T) {
		stubs := newStubSet()
		stubs.likes.deactivateErr = apperr.Internal("取消点赞失败")
		_, _, err := stubs.service().UnlikeForActor(ctx, contracts.LikeInput{
			ActorID: stubViewerID, TargetType: domain.TargetPost, TargetID: stubPostID,
		})
		requireKind(t, err, apperr.KindInternal)
	})

	t.Run("评论计数失败", func(t *testing.T) {
		stubs := newStubSet()
		stubs.comments.comments[60] = stubCommentRow(60, stubPostID, stubOtherID)
		stubs.comments.adjustLikeErr = apperr.Internal("计数失败")
		_, _, err := stubs.service().UnlikeForActor(ctx, contracts.LikeInput{
			ActorID: stubViewerID, TargetType: domain.TargetComment, TargetID: 60,
		})
		requireKind(t, err, apperr.KindInternal)
	})

	t.Run("作者账号停用", func(t *testing.T) {
		stubs := newStubSet()
		_, _, err := stubs.service().UnlikeForActor(ctx, contracts.LikeInput{
			ActorID: stubSuspendedID, TargetType: domain.TargetPost, TargetID: stubPostID,
		})
		requireKind(t, err, apperr.KindSuspended)
	})
}

// TestFollowForActor 验证端口关注路径的审批、幂等与校验。
func TestFollowForActor(t *testing.T) {
	ctx := context.Background()

	t.Run("直接关注成功", func(t *testing.T) {
		stubs := newStubSet()
		state, err := stubs.service().FollowForActor(ctx, contracts.FollowInput{
			ActorID: stubViewerID, TargetID: stubAuthorID, At: stubNow,
		})
		requireOK(t, err)
		if state != model.FollowStateFollowing {
			t.Fatalf("状态 = %q，期望 following", state)
		}
		if len(stubs.follows.inserts) != 1 || !stubs.follows.inserts[0].at.Equal(stubNow) {
			t.Fatalf("关注写入 = %+v", stubs.follows.inserts)
		}
	})

	t.Run("审批模式", func(t *testing.T) {
		stubs := newStubSet()
		carol := stubs.users.byID[stubOtherID]
		carol.RequireFollowApproval = true
		stubs.users.byID[stubOtherID] = carol
		state, err := stubs.service().FollowForActor(ctx, contracts.FollowInput{
			ActorID: stubViewerID, TargetID: stubOtherID,
		})
		requireOK(t, err)
		if state != model.FollowStatePending || len(stubs.requests.inserts) != 1 {
			t.Fatalf("审批关注状态 = %q，申请写入 %d 次", state, len(stubs.requests.inserts))
		}
	})

	t.Run("已关注幂等", func(t *testing.T) {
		stubs := newStubSet()
		stubs.follows.following[[2]int64{stubViewerID, stubAuthorID}] = true
		state, err := stubs.service().FollowForActor(ctx, contracts.FollowInput{
			ActorID: stubViewerID, TargetID: stubAuthorID,
		})
		requireOK(t, err)
		if state != model.FollowStateFollowing || len(stubs.follows.inserts) != 0 {
			t.Fatalf("状态 = %q，关注写入 %d 次", state, len(stubs.follows.inserts))
		}
	})

	t.Run("AI 可关注", func(t *testing.T) {
		stubs := newStubSet()
		state, err := stubs.service().FollowForActor(ctx, contracts.FollowInput{
			ActorID: stubAIID, TargetID: stubAuthorID,
		})
		requireOK(t, err)
		if state != model.FollowStateFollowing {
			t.Fatalf("AI 关注状态 = %q", state)
		}
	})

	t.Run("参数与状态校验", func(t *testing.T) {
		cases := []struct {
			name     string
			in       contracts.FollowInput
			wantKind apperr.Kind
		}{
			{"目标非法", contracts.FollowInput{ActorID: stubViewerID, TargetID: 0}, apperr.KindValidation},
			{"关注自己", contracts.FollowInput{ActorID: stubViewerID, TargetID: stubViewerID}, apperr.KindValidation},
			{"目标不存在", contracts.FollowInput{ActorID: stubViewerID, TargetID: 777}, apperr.KindNotFound},
			{"目标停用", contracts.FollowInput{ActorID: stubViewerID, TargetID: stubSuspendedID}, apperr.KindForbidden},
			{"作者停用", contracts.FollowInput{ActorID: stubSuspendedID, TargetID: stubAuthorID}, apperr.KindSuspended},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				stubs := newStubSet()
				_, err := stubs.service().FollowForActor(ctx, tc.in)
				requireKind(t, err, tc.wantKind)
			})
		}
	})
}

// TestUnfollowForActor 验证端口取关路径。
func TestUnfollowForActor(t *testing.T) {
	ctx := context.Background()

	t.Run("取关成功", func(t *testing.T) {
		stubs := newStubSet()
		state, err := stubs.service().UnfollowForActor(ctx, contracts.FollowInput{
			ActorID: stubViewerID, TargetID: stubAuthorID, At: stubNow,
		})
		requireOK(t, err)
		if state != model.FollowStateNone {
			t.Fatalf("状态 = %q，期望 none", state)
		}
		if len(stubs.follows.deactivates) != 1 || !stubs.follows.deactivates[0].at.Equal(stubNow) {
			t.Fatalf("取关写入 = %+v", stubs.follows.deactivates)
		}
		if _, ok := stubs.requests.calls["cancel"]; !ok {
			t.Fatal("取关应同时取消待处理申请")
		}
	})

	t.Run("目标未提供直接返回", func(t *testing.T) {
		stubs := newStubSet()
		state, err := stubs.service().UnfollowForActor(ctx, contracts.FollowInput{
			ActorID: stubViewerID, TargetID: 0,
		})
		requireOK(t, err)
		if state != model.FollowStateNone || len(stubs.follows.deactivates) != 0 {
			t.Fatalf("状态 = %q，取关写入 %d 次", state, len(stubs.follows.deactivates))
		}
	})

	t.Run("错误", func(t *testing.T) {
		cases := []struct {
			name     string
			in       contracts.FollowInput
			setup    func(*stubSet)
			wantKind apperr.Kind
		}{
			{"作者停用", contracts.FollowInput{ActorID: stubSuspendedID, TargetID: stubAuthorID}, func(*stubSet) {}, apperr.KindSuspended},
			{"取关失败", contracts.FollowInput{ActorID: stubViewerID, TargetID: stubAuthorID}, func(s *stubSet) {
				s.follows.deactivateErr = apperr.Internal("取关失败")
			}, apperr.KindInternal},
			{"取消申请失败", contracts.FollowInput{ActorID: stubViewerID, TargetID: stubAuthorID}, func(s *stubSet) {
				s.requests.cancelErr = apperr.Internal("取消失败")
			}, apperr.KindInternal},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				stubs := newStubSet()
				tc.setup(stubs)
				_, err := stubs.service().UnfollowForActor(ctx, tc.in)
				requireKind(t, err, tc.wantKind)
			})
		}
	})
}

// TestRepostForActor 验证端口纯转发路径。
func TestRepostForActor(t *testing.T) {
	ctx := context.Background()

	t.Run("转发成功", func(t *testing.T) {
		stubs := newStubSet()
		stubs.posts.counterValue = 8
		active, count, err := stubs.service().RepostForActor(ctx, contracts.RepostInput{
			ActorID: stubViewerID, PostID: stubPostID, At: stubNow,
		})
		requireOK(t, err)
		if !active || count != 8 {
			t.Fatalf("结果 = (%v, %d)，期望 (true, 8)", active, count)
		}
	})

	t.Run("AI 可转发", func(t *testing.T) {
		stubs := newStubSet()
		active, _, err := stubs.service().RepostForActor(ctx, contracts.RepostInput{
			ActorID: stubAIID, PostID: stubPostID,
		})
		requireOK(t, err)
		if !active {
			t.Fatal("AI 转发应生效")
		}
	})

	t.Run("作者转发自己非公开帖子被拒", func(t *testing.T) {
		stubs := newStubSet()
		stubs.posts.getPost.Visibility = model.VisibilityPrivate
		_, _, err := stubs.service().RepostForActor(ctx, contracts.RepostInput{
			ActorID: stubAuthorID, PostID: stubPostID,
		})
		requireKind(t, err, apperr.KindForbidden)
	})

	t.Run("作者停用", func(t *testing.T) {
		stubs := newStubSet()
		_, _, err := stubs.service().RepostForActor(ctx, contracts.RepostInput{
			ActorID: stubSuspendedID, PostID: stubPostID,
		})
		requireKind(t, err, apperr.KindSuspended)
	})

	t.Run("帖子不存在", func(t *testing.T) {
		stubs := newStubSet()
		stubs.posts.getPostErr = apperr.NotFound("内容不存在或无权访问")
		_, _, err := stubs.service().RepostForActor(ctx, contracts.RepostInput{
			ActorID: stubViewerID, PostID: stubPostID,
		})
		requireKind(t, err, apperr.KindNotFound)
	})
}

// TestUnrepostForActor 验证端口取消转发的参数校验与计数。
func TestUnrepostForActor(t *testing.T) {
	ctx := context.Background()

	stubs := newStubSet()
	stubs.posts.counterValue = 3
	active, count, err := stubs.service().UnrepostForActor(ctx, contracts.RepostInput{
		ActorID: stubViewerID, PostID: stubPostID, At: stubNow,
	})
	requireOK(t, err)
	if active || count != 3 {
		t.Fatalf("结果 = (%v, %d)，期望 (false, 3)", active, count)
	}

	stubs = newStubSet()
	_, _, err = stubs.service().UnrepostForActor(ctx, contracts.RepostInput{
		ActorID: stubViewerID, PostID: 0,
	})
	requireKind(t, err, apperr.KindValidation)

	stubs = newStubSet()
	_, _, err = stubs.service().UnrepostForActor(ctx, contracts.RepostInput{
		ActorID: stubSuspendedID, PostID: stubPostID,
	})
	requireKind(t, err, apperr.KindSuspended)
}

// TestReadPorts 覆盖供其他模块复用的读端口。
func TestReadPorts(t *testing.T) {
	ctx := context.Background()

	t.Run("GetComment", func(t *testing.T) {
		stubs := newStubSet()
		stubs.comments.comments[50] = stubCommentRow(50, stubPostID, stubOtherID)
		comment, err := stubs.service().GetComment(ctx, 50)
		requireOK(t, err)
		if comment.ID != 50 {
			t.Fatalf("评论 = %+v", comment)
		}
		if _, err := stubs.service().GetComment(ctx, 51); requireKind(t, err, apperr.KindNotFound) == nil {
			t.Fatal("评论不存在应返回 not_found")
		}
	})

	t.Run("IsPostLiked", func(t *testing.T) {
		stubs := newStubSet()
		stubs.likes.isActive = true
		got, err := stubs.service().IsPostLiked(ctx, stubViewerID, stubPostID)
		requireOK(t, err)
		if !got {
			t.Fatal("应返回已点赞")
		}
		if stubs.likes.lastIsActiveType != domain.TargetPost || stubs.likes.lastIsActiveTargetID != stubPostID {
			t.Fatalf("查询参数 = (%q, %d)，期望 (post, %d)",
				stubs.likes.lastIsActiveType, stubs.likes.lastIsActiveTargetID, stubPostID)
		}
	})

	t.Run("IsCommentLiked", func(t *testing.T) {
		stubs := newStubSet()
		got, err := stubs.service().IsCommentLiked(ctx, stubViewerID, 60)
		requireOK(t, err)
		if got {
			t.Fatal("默认应返回未点赞")
		}
		if stubs.likes.lastIsActiveType != domain.TargetComment || stubs.likes.lastIsActiveTargetID != 60 {
			t.Fatalf("查询参数 = (%q, %d)，期望 (comment, 60)",
				stubs.likes.lastIsActiveType, stubs.likes.lastIsActiveTargetID)
		}
	})

	t.Run("IsReposted", func(t *testing.T) {
		stubs := newStubSet()
		stubs.reposts.isActive = true
		got, err := stubs.service().IsReposted(ctx, stubViewerID, stubPostID)
		requireOK(t, err)
		if !got || stubs.reposts.lastIsActiveUserID != stubViewerID || stubs.reposts.lastIsActivePostID != stubPostID {
			t.Fatalf("转发状态查询 = (%v, user=%d, post=%d)",
				got, stubs.reposts.lastIsActiveUserID, stubs.reposts.lastIsActivePostID)
		}
	})

	t.Run("ViewerPostStates", func(t *testing.T) {
		stubs := newStubSet()
		stubs.likes.activeStatesMap[stubPostID] = true
		stubs.reposts.activeStatesMap[20] = false
		stubs.reposts.activeStatesMap[21] = true
		states, err := stubs.service().ViewerPostStates(ctx, stubViewerID, []int64{stubPostID, 20, 21})
		requireOK(t, err)
		if !states.Liked[stubPostID] || !states.Reposted[21] || states.Reposted[20] {
			t.Fatalf("互动状态 = %+v", states)
		}
	})

	t.Run("ViewerPostStates 空参数", func(t *testing.T) {
		stubs := newStubSet()
		states, err := stubs.service().ViewerPostStates(ctx, 0, []int64{stubPostID})
		requireOK(t, err)
		if len(states.Liked) != 0 || len(states.Reposted) != 0 {
			t.Fatalf("游客状态应为空: %+v", states)
		}
		states, err = stubs.service().ViewerPostStates(ctx, stubViewerID, nil)
		requireOK(t, err)
		if len(states.Liked) != 0 || len(states.Reposted) != 0 {
			t.Fatalf("空帖子列表状态应为空: %+v", states)
		}
	})

	t.Run("ViewerPostStates 错误", func(t *testing.T) {
		stubs := newStubSet()
		stubs.likes.statesErr = apperr.Internal("点赞状态失败")
		if _, err := stubs.service().ViewerPostStates(ctx, stubViewerID, []int64{stubPostID}); requireKind(t, err, apperr.KindInternal) == nil {
			t.Fatal("点赞状态错误应透传")
		}
		stubs = newStubSet()
		stubs.reposts.statesErr = apperr.Internal("转发状态失败")
		if _, err := stubs.service().ViewerPostStates(ctx, stubViewerID, []int64{stubPostID}); requireKind(t, err, apperr.KindInternal) == nil {
			t.Fatal("转发状态错误应透传")
		}
	})
}

// TestListRecentComments 验证最近讨论投影：倒序转正序、停用作者过滤与截断。
func TestListRecentComments(t *testing.T) {
	ctx := context.Background()
	longText := strings.Repeat("文", 205)
	stubs := newStubSet()
	stubs.comments.recentRows = []model.Comment{
		{ID: 3, AuthorID: stubOtherID, PlainText: "  " + longText + "  "},
		{ID: 2, AuthorID: stubViewerID, PlainText: "停用作者"},
		{ID: 1, AuthorID: stubAuthorID, PlainText: "第一条"},
	}
	suspended := stubs.users.briefs[stubViewerID]
	suspended.IsSuspended = true
	stubs.users.briefs[stubViewerID] = suspended
	nickname := stubs.users.briefs[stubAuthorID]
	nickname.Nickname = "鲍勃"
	stubs.users.briefs[stubAuthorID] = nickname

	got, err := stubs.service().ListRecentComments(ctx, contracts.CommentListQuery{PostID: stubPostID, Limit: 10})
	requireOK(t, err)
	if len(got) != 2 {
		t.Fatalf("讨论条数 = %d，期望 2（停用作者被过滤）", len(got))
	}
	if got[0].ID != 1 || got[1].ID != 3 {
		t.Fatalf("顺序 = [%d, %d]，期望按时间正序 [1, 3]", got[0].ID, got[1].ID)
	}
	if got[0].AuthorName != "鲍勃" {
		t.Fatalf("作者展示名 = %q，期望昵称优先", got[0].AuthorName)
	}
	if got[1].AuthorName != "carol" {
		t.Fatalf("作者展示名 = %q，期望回退用户名", got[1].AuthorName)
	}
	wantContent := strings.Repeat("文", maxRecentCommentRunes) + "…"
	if got[1].Content != wantContent {
		t.Fatalf("内容截断长度 = %d，期望 %d（含省略号）", len([]rune(got[1].Content)), len([]rune(wantContent)))
	}

	stubs = newStubSet()
	got, err = stubs.service().ListRecentComments(ctx, contracts.CommentListQuery{PostID: 0, Limit: 10})
	requireOK(t, err)
	if got != nil {
		t.Fatalf("非法帖子 ID 应返回 nil，实际 %v", got)
	}

	stubs = newStubSet()
	stubs.comments.recentErr = apperr.Internal("读取讨论失败")
	if _, err := stubs.service().ListRecentComments(ctx, contracts.CommentListQuery{PostID: stubPostID, Limit: 10}); requireKind(t, err, apperr.KindInternal) == nil {
		t.Fatal("讨论读取错误应透传")
	}

	stubs = newStubSet()
	stubs.comments.recentRows = []model.Comment{{ID: 1, AuthorID: stubAuthorID}}
	stubs.users.briefsErr = apperr.Internal("作者摘要失败")
	if _, err := stubs.service().ListRecentComments(ctx, contracts.CommentListQuery{PostID: stubPostID, Limit: 10}); requireKind(t, err, apperr.KindInternal) == nil {
		t.Fatal("作者摘要错误应透传")
	}
}

// TestListUserReplies 验证按作者读取回复的透传与参数校验。
func TestListUserReplies(t *testing.T) {
	ctx := context.Background()
	query := contracts.ReplyQuery{AuthorID: stubViewerID, AfterAt: stubNow, AfterID: 10, Limit: 5}

	stubs := newStubSet()
	stubs.comments.byAuthorRows = []model.Comment{stubCommentRow(50, stubPostID, stubViewerID)}
	got, err := stubs.service().ListUserReplies(ctx, query)
	requireOK(t, err)
	if len(got) != 1 || got[0].ID != 50 {
		t.Fatalf("回复列表 = %+v", got)
	}

	got, err = stubs.service().ListUserReplies(ctx, contracts.ReplyQuery{AuthorID: 0})
	requireOK(t, err)
	if got != nil {
		t.Fatalf("非法作者 ID 应返回 nil，实际 %v", got)
	}

	stubs.comments.byAuthorErr = apperr.Internal("读取回复失败")
	if _, err := stubs.service().ListUserReplies(ctx, query); requireKind(t, err, apperr.KindInternal) == nil {
		t.Fatal("回复读取错误应透传")
	}
}

// TestListNewCommentsOnAuthorPosts 覆盖评论触发扫描的边界与错误。
func TestListNewCommentsOnAuthorPosts(t *testing.T) {
	ctx := context.Background()
	query := contracts.CommentTriggerQuery{AuthorID: stubAuthorID, Since: stubNow.Add(-time.Hour), Limit: 10}

	t.Run("非法作者返回空", func(t *testing.T) {
		got, err := newStubSet().service().ListNewCommentsOnAuthorPosts(ctx, contracts.CommentTriggerQuery{})
		requireOK(t, err)
		if got != nil {
			t.Fatalf("期望 nil，实际 %v", got)
		}
	})

	t.Run("作者没有帖子", func(t *testing.T) {
		stubs := newStubSet()
		got, err := stubs.service().ListNewCommentsOnAuthorPosts(ctx, query)
		requireOK(t, err)
		if got != nil {
			t.Fatalf("期望 nil，实际 %v", got)
		}
	})

	t.Run("没有公开已发布帖子", func(t *testing.T) {
		stubs := newStubSet()
		stubs.posts.postIDs = []int64{stubPostID}
		stubs.posts.posts[stubPostID] = model.Post{ID: stubPostID, Status: model.PostStatusDraft, Visibility: model.VisibilityPublic}
		got, err := stubs.service().ListNewCommentsOnAuthorPosts(ctx, query)
		requireOK(t, err)
		if got != nil {
			t.Fatalf("期望 nil，实际 %v", got)
		}
	})

	t.Run("成功返回新评论", func(t *testing.T) {
		stubs := newStubSet()
		stubs.posts.postIDs = []int64{stubPostID, 21}
		stubs.posts.posts[stubPostID] = stubPublicPost()
		stubs.posts.posts[21] = model.Post{ID: 21, Status: model.PostStatusPublished, Visibility: model.VisibilityFollowers}
		stubs.comments.byPostIDsRows = []model.Comment{stubCommentRow(50, stubPostID, stubOtherID)}
		got, err := stubs.service().ListNewCommentsOnAuthorPosts(ctx, query)
		requireOK(t, err)
		if len(got) != 1 {
			t.Fatalf("评论条数 = %d，期望 1", len(got))
		}
		if len(stubs.comments.byPostIDsCalls) != 1 || len(stubs.comments.byPostIDsCalls[0].postIDs) != 1 ||
			stubs.comments.byPostIDsCalls[0].postIDs[0] != stubPostID {
			t.Fatalf("repo 查询参数 = %+v，期望只含公开已发布帖子", stubs.comments.byPostIDsCalls)
		}
	})

	t.Run("错误", func(t *testing.T) {
		cases := []struct {
			name  string
			setup func(*stubSet)
		}{
			{"帖子 ID 读取失败", func(s *stubSet) { s.posts.postIDsErr = apperr.Internal("读取帖子失败") }},
			{"帖子批量读取失败", func(s *stubSet) {
				s.posts.postIDs = []int64{stubPostID}
				s.posts.postsErr = apperr.Internal("批量读取失败")
			}},
			{"评论查询失败", func(s *stubSet) {
				s.posts.postIDs = []int64{stubPostID}
				s.comments.byPostIDsErr = apperr.Internal("评论查询失败")
			}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				stubs := newStubSet()
				tc.setup(stubs)
				if _, err := stubs.service().ListNewCommentsOnAuthorPosts(ctx, query); requireKind(t, err, apperr.KindInternal) == nil {
					t.Fatal("期望 internal 错误")
				}
			})
		}
	})
}

// TestListRecentLikedPosts 验证最近点赞帖子过滤与摘要截断。
func TestListRecentLikedPosts(t *testing.T) {
	ctx := context.Background()
	longText := strings.Repeat("帖", maxLikedSummaryRunes+5)

	stubs := newStubSet()
	stubs.likes.likedIDs = []int64{stubPostID, 21, 22, 23}
	stubs.posts.posts = map[int64]model.Post{
		stubPostID: {ID: stubPostID, Status: model.PostStatusPublished, Visibility: model.VisibilityPublic},
		21:         {ID: 21, Status: model.PostStatusPublished, Visibility: model.VisibilityFollowers},
		22:         {ID: 22, Status: model.PostStatusDraft, Visibility: model.VisibilityPublic},
	}
	stubs.posts.plainTexts = map[int64]string{stubPostID: "  " + longText + "  "}
	got, err := stubs.service().ListRecentLikedPosts(ctx, stubViewerID, 10)
	requireOK(t, err)
	if len(got) != 1 || got[0].PostID != stubPostID {
		t.Fatalf("结果 = %+v，期望仅公开已发布帖子", got)
	}
	wantSummary := strings.Repeat("帖", maxLikedSummaryRunes) + "…"
	if got[0].Summary != wantSummary {
		t.Fatalf("摘要截断 = %q", got[0].Summary)
	}

	for _, tc := range []struct {
		name   string
		userID int64
		limit  int
	}{{"游客", 0, 10}, {"limit 非法", stubViewerID, 0}} {
		got, err := newStubSet().service().ListRecentLikedPosts(ctx, tc.userID, tc.limit)
		requireOK(t, err)
		if got != nil {
			t.Fatalf("%s 应返回 nil，实际 %v", tc.name, got)
		}
	}

	stubs = newStubSet()
	stubs.likes.likedIDsErr = apperr.Internal("读取点赞失败")
	if _, err := stubs.service().ListRecentLikedPosts(ctx, stubViewerID, 10); requireKind(t, err, apperr.KindInternal) == nil {
		t.Fatal("点赞列表错误应透传")
	}

	stubs = newStubSet()
	stubs.likes.likedIDs = []int64{stubPostID}
	stubs.posts.postsErr = apperr.Internal("批量读取失败")
	if _, err := stubs.service().ListRecentLikedPosts(ctx, stubViewerID, 10); requireKind(t, err, apperr.KindInternal) == nil {
		t.Fatal("帖子批量读取错误应透传")
	}

	stubs = newStubSet()
	stubs.likes.likedIDs = []int64{stubPostID}
	stubs.posts.textsErr = apperr.Internal("摘要读取失败")
	if _, err := stubs.service().ListRecentLikedPosts(ctx, stubViewerID, 10); requireKind(t, err, apperr.KindInternal) == nil {
		t.Fatal("摘要读取错误应透传")
	}

	stubs = newStubSet()
	got, err = stubs.service().ListRecentLikedPosts(ctx, stubViewerID, 10)
	requireOK(t, err)
	if got != nil {
		t.Fatalf("没有点赞记录应返回 nil，实际 %v", got)
	}
}

// TestListRecentFollowees 验证最近关注账号投影。
func TestListRecentFollowees(t *testing.T) {
	ctx := context.Background()
	stubs := newStubSet()
	stubs.follows.recentRows = []domain.FollowRow{{RowID: 1, UserID: stubAuthorID}, {RowID: 2, UserID: 777}}
	got, err := stubs.service().ListRecentFollowees(ctx, stubViewerID, 10)
	requireOK(t, err)
	if len(got) != 1 {
		t.Fatalf("条数 = %d，期望 1（不存在的账号被跳过）", len(got))
	}
	if got[0].UserID != stubAuthorID || got[0].Username != "bob" || got[0].Nickname != "" {
		t.Fatalf("关注投影 = %+v", got[0])
	}

	for _, tc := range []struct {
		name   string
		userID int64
		limit  int
	}{{"游客", 0, 10}, {"limit 非法", stubViewerID, -1}} {
		got, err := newStubSet().service().ListRecentFollowees(ctx, tc.userID, tc.limit)
		requireOK(t, err)
		if got != nil {
			t.Fatalf("%s 应返回 nil，实际 %v", tc.name, got)
		}
	}

	stubs = newStubSet()
	stubs.follows.recentErr = apperr.Internal("读取关注失败")
	if _, err := stubs.service().ListRecentFollowees(ctx, stubViewerID, 10); requireKind(t, err, apperr.KindInternal) == nil {
		t.Fatal("最近关注错误应透传")
	}

	stubs = newStubSet()
	stubs.follows.recentRows = []domain.FollowRow{{RowID: 1, UserID: stubAuthorID}}
	stubs.users.listErr = apperr.Internal("批量读取失败")
	if _, err := stubs.service().ListRecentFollowees(ctx, stubViewerID, 10); requireKind(t, err, apperr.KindInternal) == nil {
		t.Fatal("账号批量读取错误应透传")
	}
}

// TestListTimelineReposts 验证纯转发事件查询映射与作者类型过滤。
func TestListTimelineReposts(t *testing.T) {
	ctx := context.Background()
	afterAt := stubNow.Add(-time.Hour)
	query := contracts.RepostTimelineQuery{
		AuthorID:     stubAuthorID,
		FollowingIDs: []int64{stubViewerID},
		AfterAt:      afterAt,
		AfterID:      9,
		Limit:        10,
	}

	t.Run("不过滤", func(t *testing.T) {
		stubs := newStubSet()
		stubs.reposts.events = []domain.RepostEvent{{ID: 1, UserID: stubViewerID, PostID: stubPostID, CreatedAt: stubNow}}
		got, err := stubs.service().ListTimelineReposts(ctx, query)
		requireOK(t, err)
		if len(got) != 1 || got[0].ID != 1 || got[0].UserID != stubViewerID || got[0].PostID != stubPostID {
			t.Fatalf("事件投影 = %+v", got)
		}
		repoQuery := stubs.reposts.lastQuery
		if repoQuery.AuthorID != stubAuthorID || repoQuery.AfterID != 9 || repoQuery.Limit != 10 ||
			!repoQuery.AfterAt.Equal(afterAt) || len(repoQuery.FollowingIDs) != 1 || repoQuery.AllowedIDs != nil {
			t.Fatalf("repo 查询参数 = %+v", repoQuery)
		}
	})

	t.Run("AI 过滤", func(t *testing.T) {
		stubs := newStubSet()
		stubs.users.aiIDs = []int64{stubAIID}
		got, err := stubs.service().ListTimelineReposts(ctx, contracts.RepostTimelineQuery{Filter: "ai", Limit: 5})
		requireOK(t, err)
		if len(got) != 0 {
			t.Fatalf("期望空事件，实际 %+v", got)
		}
		if len(stubs.reposts.lastQuery.AllowedIDs) != 1 || stubs.reposts.lastQuery.AllowedIDs[0] != stubAIID {
			t.Fatalf("作者过滤 = %v，期望 [%d]", stubs.reposts.lastQuery.AllowedIDs, stubAIID)
		}
	})

	t.Run("人类过滤", func(t *testing.T) {
		stubs := newStubSet()
		stubs.users.aiIDs = []int64{stubViewerID, stubAuthorID}
		_, err := stubs.service().ListTimelineReposts(ctx, contracts.RepostTimelineQuery{Filter: "human", Limit: 5})
		requireOK(t, err)
		allowed := append([]int64{}, stubs.reposts.lastQuery.AllowedIDs...)
		sort.Slice(allowed, func(i, j int) bool { return allowed[i] < allowed[j] })
		if len(allowed) != 2 || allowed[0] != stubViewerID || allowed[1] != stubAuthorID {
			t.Fatalf("作者过滤 = %v", allowed)
		}
	})

	t.Run("limit 非法返回空", func(t *testing.T) {
		stubs := newStubSet()
		got, err := stubs.service().ListTimelineReposts(ctx, contracts.RepostTimelineQuery{Limit: 0})
		requireOK(t, err)
		if got != nil {
			t.Fatalf("期望 nil，实际 %v", got)
		}
	})

	t.Run("错误", func(t *testing.T) {
		stubs := newStubSet()
		stubs.users.aiErr = apperr.Internal("作者类型查询失败")
		if _, err := stubs.service().ListTimelineReposts(ctx, contracts.RepostTimelineQuery{Filter: "ai", Limit: 5}); requireKind(t, err, apperr.KindInternal) == nil {
			t.Fatal("作者类型查询错误应透传")
		}

		stubs = newStubSet()
		stubs.reposts.listErr = apperr.Internal("事件读取失败")
		if _, err := stubs.service().ListTimelineReposts(ctx, contracts.RepostTimelineQuery{Limit: 5}); requireKind(t, err, apperr.KindInternal) == nil {
			t.Fatal("事件读取错误应透传")
		}
	})
}

// TestFirstNonEmpty 验证首个非空字符串选择。
func TestFirstNonEmpty(t *testing.T) {
	cases := []struct {
		name   string
		values []string
		want   string
	}{
		{"取首个非空", []string{"", "  ", "昵称", "用户名"}, "昵称"},
		{"全部为空", []string{"", " "}, ""},
		{"无参数", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := firstNonEmpty(tc.values...); got != tc.want {
				t.Fatalf("firstNonEmpty(%v) = %q，期望 %q", tc.values, got, tc.want)
			}
		})
	}
}

// TestTruncateRunes 验证按字符截断与省略号。
func TestTruncateRunes(t *testing.T) {
	cases := []struct {
		name  string
		value string
		limit int
		want  string
	}{
		{"limit 非法", "文本", 0, ""},
		{"未超长", "文本", 5, "文本"},
		{"正好等于上限", "文本", 2, "文本"},
		{"超长追加省略号", "一二三四五六", 4, "一二三四…"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := truncateRunes(tc.value, tc.limit); got != tc.want {
				t.Fatalf("truncateRunes(%q, %d) = %q，期望 %q", tc.value, tc.limit, got, tc.want)
			}
		})
	}
}
