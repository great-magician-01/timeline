package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/content/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/ratelimit"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// 说明：帖子写用例的「正文校验 → 可见性 → 附件 → 作者 → 事务」链条中，前四步都是纯逻辑/
// 端口调用；合法输入会进入事务入口（nil TxManager 直接返回），因此可以在无数据库下覆盖
// 校验与错误映射，不覆盖 repo 读写。

// validPostInput 返回一份合法的创建草稿输入。
func validPostInput() PostInput {
	return PostInput{
		ContentFormat: "richtext",
		ContentSource: "<p>你好，时间线</p>",
		Visibility:    model.VisibilityPublic,
		Action:        domain.ActionDraft,
	}
}

// TestPrepareContent 校验正文渲染、格式/可见性校验、AI 可见性策略与附件校验。
func TestPrepareContent(t *testing.T) {
	author := model.User{ID: 9}
	aiAuthor := model.User{ID: 9, IsAI: true}
	ctx := context.Background()

	t.Run("合法内容", func(t *testing.T) {
		svc := newClockedService(t, Deps{})
		content, err := svc.prepareContent(ctx, author, contentInput{
			Format:     "richtext",
			Source:     "<p>你好</p>",
			Visibility: "",
		}, false)
		if err != nil {
			t.Fatalf("合法内容应通过: %v", err)
		}
		if content.Format != "richtext" || content.Visibility != model.VisibilityPublic {
			t.Fatalf("格式/可见性归一化错误: %+v", content)
		}
		if content.Plain != "你好" {
			t.Fatalf("纯文本渲染错误: %q", content.Plain)
		}
		if content.HTML == "" {
			t.Fatal("安全 HTML 不应为空")
		}
		if content.AttachmentIDs == nil || len(content.AttachmentIDs) != 0 {
			t.Fatalf("无附件时应返回空集合: %#v", content.AttachmentIDs)
		}
	})

	cases := []struct {
		name    string
		author  model.User
		input   contentInput
		wantErr apperr.Kind
	}{
		{
			name:    "内容格式无效",
			author:  author,
			input:   contentInput{Format: "text", Source: "正文"},
			wantErr: apperr.KindValidation,
		},
		{
			name:    "正文为空",
			author:  author,
			input:   contentInput{Format: "richtext", Source: "   "},
			wantErr: apperr.KindValidation,
		},
		{
			name:    "可见性无效",
			author:  author,
			input:   contentInput{Format: "richtext", Source: "<p>正文</p>", Visibility: "weird"},
			wantErr: apperr.KindValidation,
		},
		{
			name:    "AI 内容必须公开",
			author:  aiAuthor,
			input:   contentInput{Format: "markdown", Source: "正文", Visibility: model.VisibilityPrivate},
			wantErr: apperr.KindValidation,
		},
		{
			name:    "附件端口缺失",
			author:  author,
			input:   contentInput{Format: "richtext", Source: "<p>正文</p>", AttachmentIDs: []int64{5}},
			wantErr: apperr.KindInternal,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newClockedService(t, Deps{})
			_, err := svc.prepareContent(ctx, tc.author, tc.input, false)
			requireKind(t, err, tc.wantErr)
		})
	}

	t.Run("AI 公开内容通过", func(t *testing.T) {
		svc := newClockedService(t, Deps{})
		content, err := svc.prepareContent(ctx, aiAuthor, contentInput{
			Format: "markdown", Source: "**正文**", Visibility: model.VisibilityPublic,
		}, false)
		if err != nil {
			t.Fatalf("AI 公开内容应通过: %v", err)
		}
		if content.Format != "markdown" || content.Visibility != model.VisibilityPublic {
			t.Fatalf("归一化错误: %+v", content)
		}
	})

	t.Run("附件去重与端口参数", func(t *testing.T) {
		media := &fakeMedia{}
		svc := newClockedService(t, Deps{Media: media})
		content, err := svc.prepareContent(ctx, author, contentInput{
			Format:        "richtext",
			Source:        "<p>正文</p>",
			AttachmentIDs: []int64{5, 5, 0, -1, 6},
		}, false)
		if err != nil {
			t.Fatalf("附件校验失败: %v", err)
		}
		if len(content.AttachmentIDs) != 2 || content.AttachmentIDs[0] != 5 || content.AttachmentIDs[1] != 6 {
			t.Fatalf("附件 ID 应去重并忽略非正数: %#v", content.AttachmentIDs)
		}
		if len(media.checked) != 1 {
			t.Fatalf("应调用一次附件校验，实际 %d", len(media.checked))
		}
		check := media.checked[0]
		if check.UploaderID != author.ID || check.Purpose != domain.AttachmentPurposePost ||
			len(check.AttachmentIDs) != 2 {
			t.Fatalf("附件校验参数错误: %+v", check)
		}
	})

	t.Run("附件校验失败", func(t *testing.T) {
		media := &fakeMedia{checkErr: apperr.Validation("附件不可用")}
		svc := newClockedService(t, Deps{Media: media})
		_, err := svc.prepareContent(ctx, author, contentInput{
			Format: "richtext", Source: "<p>正文</p>", AttachmentIDs: []int64{5},
		}, false)
		// 已知领域错误原样透传。
		requireKind(t, err, apperr.KindValidation)
	})

	t.Run("编辑不允许变更引用", func(t *testing.T) {
		svc := newClockedService(t, Deps{})
		quotedID := int64(3)
		content, err := svc.prepareContent(ctx, author, contentInput{
			Format: "richtext", Source: "<p>正文</p>", QuotedPostID: &quotedID,
		}, false)
		if err != nil {
			t.Fatalf("编辑路径应通过: %v", err)
		}
		if content.QuotedPostID != nil {
			t.Fatalf("allowQuote=false 时不应设置引用 ID: %#v", content.QuotedPostID)
		}
	})
}

// TestCreatePostGuards 校验创建帖子的登录/账号/动作/定时/ID 生成等前置校验。
func TestCreatePostGuards(t *testing.T) {
	ctx := context.Background()

	// 未登录。
	_, err := newClockedService(t, Deps{}).CreatePost(ctx, guestViewer(), validPostInput())
	requireKind(t, err, apperr.KindUnauthenticated)

	// 停用账号。
	suspended := model.Viewer{ID: 1, Authenticated: true, IsSuspended: true}
	_, err = newClockedService(t, Deps{}).CreatePost(ctx, suspended, validPostInput())
	requireKind(t, err, apperr.KindSuspended)

	// AI 账号。
	ai := model.Viewer{ID: 1, Authenticated: true, IsAI: true}
	_, err = newClockedService(t, Deps{}).CreatePost(ctx, ai, validPostInput())
	requireKind(t, err, apperr.KindForbidden)

	// 动作非法。
	in := validPostInput()
	in.Action = "unknown"
	_, err = newClockedService(t, Deps{IDs: fixedIDs{id: 1}}).CreatePost(ctx, humanViewer(1), in)
	requireKind(t, err, apperr.KindValidation)

	// 定时动作缺少时间。
	in = validPostInput()
	in.Action = domain.ActionSchedule
	_, err = newClockedService(t, Deps{IDs: fixedIDs{id: 1}}).CreatePost(ctx, humanViewer(1), in)
	e := requireKind(t, err, apperr.KindValidation)
	if e.Message != "请填写定时发布时间" {
		t.Fatalf("缺少定时时间文案错误: %q", e.Message)
	}

	// 定时时间过早。
	in = validPostInput()
	in.Action = domain.ActionSchedule
	past := contentTestNow.Add(-time.Hour).Format(time.RFC3339)
	in.ScheduledAt = &past
	_, err = newClockedService(t, Deps{IDs: fixedIDs{id: 1}}).CreatePost(ctx, humanViewer(1), in)
	e = requireKind(t, err, apperr.KindValidation)
	if e.Message != "定时发布时间过早" {
		t.Fatalf("定时过早文案错误: %q", e.Message)
	}

	// ID 生成失败。
	_, err = newClockedService(t, Deps{IDs: failingIDs{err: errors.New("雪花失败")}}).
		CreatePost(ctx, humanViewer(1), validPostInput())
	e = requireKind(t, err, apperr.KindInternal)
	if e.Message != "生成资源 ID 失败" {
		t.Fatalf("ID 生成错误文案错误: %q", e.Message)
	}
}

// TestCreatePostRateLimited 校验发帖限频在触库前返回带 RetryAfter 的错误。
func TestCreatePostRateLimited(t *testing.T) {
	svc := newClockedService(t, Deps{
		IDs:         fixedIDs{id: 1},
		PostLimiter: ratelimit.New(1, time.Minute),
		Settings: fakeSettings{values: contracts.SystemSettings{
			PostRateLimit:  1,
			PostRateWindow: time.Minute,
		}},
	})
	ctx := context.Background()
	if err := svc.consumePostQuota(ctx, 1); err != nil {
		t.Fatalf("首次额度应放行: %v", err)
	}
	_, err := svc.CreatePost(ctx, humanViewer(1), validPostInput())
	e := requireKind(t, err, apperr.KindRateLimited)
	if e.RetryAfter <= 0 {
		t.Fatalf("限频错误应带正的 RetryAfter，实际 %v", e.RetryAfter)
	}
}

// TestCreatePostStopsBeforeRepo 校验合法草稿在进入事务时返回（不触库）。
func TestCreatePostStopsBeforeRepo(t *testing.T) {
	svc := newClockedService(t, Deps{IDs: fixedIDs{id: 7}})
	_, err := svc.CreatePost(context.Background(), humanViewer(1), validPostInput())
	requireTransactionMissing(t, err)
}

// TestUpdatePostGuards 校验编辑帖子的前置校验与事务入口。
func TestUpdatePostGuards(t *testing.T) {
	ctx := context.Background()
	in := UpdatePostInput{ContentFormat: "richtext", ContentSource: "<p>正文</p>"}
	_, err := newClockedService(t, Deps{}).UpdatePost(ctx, guestViewer(), 1, in)
	requireKind(t, err, apperr.KindUnauthenticated)

	_, err = newClockedService(t, Deps{}).UpdatePost(ctx, humanViewer(1), 1, in)
	requireTransactionMissing(t, err)
}

// TestPublishPostGuards 校验发布帖子的登录校验与事务入口。
func TestPublishPostGuards(t *testing.T) {
	ctx := context.Background()
	_, err := newClockedService(t, Deps{}).PublishPost(ctx, guestViewer(), 1)
	requireKind(t, err, apperr.KindUnauthenticated)
	_, err = newClockedService(t, Deps{}).PublishPost(ctx, humanViewer(1), 1)
	requireTransactionMissing(t, err)
}

// TestSchedulePostGuards 校验设置定时发布的登录/时间校验与事务入口。
func TestSchedulePostGuards(t *testing.T) {
	ctx := context.Background()
	_, err := newClockedService(t, Deps{}).SchedulePost(ctx, guestViewer(), 1, SchedulePostInput{})
	requireKind(t, err, apperr.KindUnauthenticated)

	// 缺少时间。
	_, err = newClockedService(t, Deps{}).SchedulePost(ctx, humanViewer(1), 1, SchedulePostInput{})
	requireKind(t, err, apperr.KindValidation)

	// 格式无效。
	raw := "not-a-time"
	_, err = newClockedService(t, Deps{}).SchedulePost(ctx, humanViewer(1), 1, SchedulePostInput{ScheduledAt: &raw})
	requireKind(t, err, apperr.KindValidation)

	// 合法未来时间进入事务入口。
	future := contentTestNow.Add(2 * time.Hour).Format(time.RFC3339)
	_, err = newClockedService(t, Deps{}).SchedulePost(ctx, humanViewer(1), 1, SchedulePostInput{ScheduledAt: &future})
	requireTransactionMissing(t, err)
}

// TestCancelScheduleGuards 校验取消定时的登录校验与事务入口。
func TestCancelScheduleGuards(t *testing.T) {
	ctx := context.Background()
	_, err := newClockedService(t, Deps{}).CancelSchedule(ctx, guestViewer(), 1)
	requireKind(t, err, apperr.KindUnauthenticated)
	_, err = newClockedService(t, Deps{}).CancelSchedule(ctx, humanViewer(1), 1)
	requireTransactionMissing(t, err)
}

// TestDeletePostGuards 校验删除帖子的账号状态校验与事务入口。
func TestDeletePostGuards(t *testing.T) {
	ctx := context.Background()
	requireKind(t, newClockedService(t, Deps{}).DeletePost(ctx, guestViewer(), 1), apperr.KindUnauthenticated)
	suspended := model.Viewer{ID: 1, Authenticated: true, IsSuspended: true}
	requireKind(t, newClockedService(t, Deps{}).DeletePost(ctx, suspended, 1), apperr.KindSuspended)
	ai := model.Viewer{ID: 1, Authenticated: true, IsAI: true}
	requireKind(t, newClockedService(t, Deps{}).DeletePost(ctx, ai, 1), apperr.KindForbidden)
	requireTransactionMissing(t, newClockedService(t, Deps{}).DeletePost(ctx, humanViewer(1), 1))
}

// TestRestoreVersionGuards 校验恢复历史版本的登录校验与事务入口。
func TestRestoreVersionGuards(t *testing.T) {
	ctx := context.Background()
	_, err := newClockedService(t, Deps{}).RestoreVersion(ctx, guestViewer(), 1, 2)
	requireKind(t, err, apperr.KindUnauthenticated)
	_, err = newClockedService(t, Deps{}).RestoreVersion(ctx, humanViewer(1), 1, 2)
	requireTransactionMissing(t, err)
}

// TestCreatePostForActorGuards 校验跨模块创建帖子的作者/定时/ID 校验与事务入口。
func TestCreatePostForActorGuards(t *testing.T) {
	ctx := context.Background()
	_, err := newClockedService(t, Deps{}).CreatePostForActor(ctx, contracts.ActorPostInput{})
	e := requireKind(t, err, apperr.KindValidation)
	if e.Message != "缺少作者账号" {
		t.Fatalf("缺少作者文案错误: %q", e.Message)
	}

	past := contentTestNow.Add(-time.Minute)
	_, err = newClockedService(t, Deps{}).CreatePostForActor(ctx, contracts.ActorPostInput{
		AuthorID: 3, ScheduledAt: &past, At: contentTestNow,
	})
	e = requireKind(t, err, apperr.KindValidation)
	if e.Message != "定时发布时间必须晚于当前时间" {
		t.Fatalf("定时校验文案错误: %q", e.Message)
	}

	_, err = newClockedService(t, Deps{IDs: failingIDs{err: errors.New("雪花失败")}}).
		CreatePostForActor(ctx, contracts.ActorPostInput{
			AuthorID: 3, At: contentTestNow, AsDraft: true,
			ContentFormat: "richtext", ContentSource: "<p>正文</p>",
		})
	requireKind(t, err, apperr.KindInternal)

	// 合法草稿进入事务入口。
	_, err = newClockedService(t, Deps{IDs: fixedIDs{id: 9}}).CreatePostForActor(ctx, contracts.ActorPostInput{
		AuthorID: 3, At: contentTestNow, AsDraft: true,
		ContentFormat: "richtext", ContentSource: "<p>正文</p>",
	})
	requireTransactionMissing(t, err)
}

// TestUpdateDraftForActorValidation 校验管理员编辑 AI 草稿的正文校验（触库前）。
func TestUpdateDraftForActorValidation(t *testing.T) {
	ctx := context.Background()
	err := newClockedService(t, Deps{}).UpdateDraftForActor(ctx, contracts.UpdateDraftForActorInput{
		PostID: 1, ContentFormat: "text", ContentSource: "正文", At: contentTestNow,
	})
	requireKind(t, err, apperr.KindValidation)

	err = newClockedService(t, Deps{}).UpdateDraftForActor(ctx, contracts.UpdateDraftForActorInput{
		PostID: 1, ContentFormat: "richtext", ContentSource: "  ", At: contentTestNow,
	})
	e := requireKind(t, err, apperr.KindValidation)
	if e.Message != "正文不能为空" {
		t.Fatalf("空正文文案错误: %q", e.Message)
	}
}

// TestPublishPostForActorStopsBeforeRepo 校验跨模块发布在事务入口返回（不触库）。
func TestPublishPostForActorStopsBeforeRepo(t *testing.T) {
	err := newClockedService(t, Deps{}).PublishPostForActor(context.Background(), contracts.PublishPostForActorInput{
		PostID: 1, ActorID: 3, At: contentTestNow,
	})
	requireTransactionMissing(t, err)
}

// TestDeletePostAsAdminStopsBeforeRepo 校验管理员删除在事务入口返回（不触库）。
func TestDeletePostAsAdminStopsBeforeRepo(t *testing.T) {
	err := newClockedService(t, Deps{}).DeletePostAsAdmin(context.Background(), contracts.DeletePostInput{
		AdminID: 1, PostID: 2, At: contentTestNow,
	})
	requireTransactionMissing(t, err)
}
