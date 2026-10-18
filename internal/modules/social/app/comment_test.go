package app

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/social/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/audit"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// stubCommentRow 构造一条已发布评论行。
func stubCommentRow(id, postID, authorID int64) model.Comment {
	return model.Comment{
		ID:            id,
		PostID:        postID,
		AuthorID:      authorID,
		ContentFormat: "richtext",
		ContentSource: "<p>评论</p>",
		ContentHTML:   "<p>评论</p>",
		PlainText:     "评论",
		Status:        model.CommentStatusPublished,
		CreatedAt:     stubNow.Add(-time.Minute),
		UpdatedAt:     stubNow.Add(-time.Minute),
	}
}

// TestListComments 验证顶层评论分页、回复预览与访问者状态组装。
func TestListComments(t *testing.T) {
	ctx := context.Background()
	stubs := newStubSet()
	first := stubCommentRow(101, stubPostID, stubOtherID)
	deleted := stubCommentRow(102, stubPostID, stubOtherID)
	deleted.Status = model.CommentStatusDeleted
	reply := stubCommentRow(103, stubPostID, stubViewerID)
	stubs.comments.topLevelRows = []model.Comment{first, deleted}
	stubs.comments.previewsMap = map[int64][]model.Comment{
		first.ID: {reply},
	}
	stubs.likes.activeStatesMap[first.ID] = true
	svc := stubs.service()

	page, err := svc.ListComments(ctx, stubViewer(), stubPostID, 0, 1)
	requireOK(t, err)
	if len(page.Items) != 1 || !page.HasMore {
		t.Fatalf("分页 = %d 条/hasMore=%v，期望 1 条且还有下一页", len(page.Items), page.HasMore)
	}
	if page.NextCursor != first.ID {
		t.Fatalf("NextCursor = %d，期望 %d", page.NextCursor, first.ID)
	}
	if list := stubs.comments.listCalls["top"]; list.postID != stubPostID || list.limit != 2 {
		t.Fatalf("评论仓储收到 (%d, limit=%d)，期望 (post=%d, limit=2)", list.postID, list.limit, stubPostID)
	}
	item := page.Items[0]
	if !item.LikedByMe {
		t.Fatal("访问者已点赞的评论应标记 LikedByMe")
	}
	if item.CanDelete {
		t.Fatal("访问者不是评论作者或帖子作者，不应可删除")
	}
	if item.Author.Username != "carol" {
		t.Fatalf("作者摘要 = %+v，期望 carol", item.Author)
	}
	previews := page.Previews[first.ID]
	if len(previews) != 1 || previews[0].Row.ID != reply.ID {
		t.Fatalf("回复预览 = %+v，期望包含回复 %d", previews, reply.ID)
	}
	if !previews[0].CanDelete {
		t.Fatal("回复作者是访问者本人，应可删除")
	}
	if stubs.likes.lastStatesType != domain.TargetComment {
		t.Fatalf("点赞状态查询目标类型 = %q，期望 %q", stubs.likes.lastStatesType, domain.TargetComment)
	}
}

// TestListCommentsGuestAndDeletedMasking 验证游客可读且已删除评论不暴露点赞/删除权限。
func TestListCommentsGuestAndDeletedMasking(t *testing.T) {
	stubs := newStubSet()
	deleted := stubCommentRow(101, stubPostID, stubOtherID)
	deleted.Status = model.CommentStatusDeleted
	stubs.comments.topLevelRows = []model.Comment{deleted}
	stubs.likes.activeStatesMap[deleted.ID] = true
	svc := stubs.service()

	page, err := svc.ListComments(context.Background(), stubGuestViewer(), stubPostID, 0, 20)
	requireOK(t, err)
	if len(page.Items) != 1 {
		t.Fatalf("评论条数 = %d，期望 1", len(page.Items))
	}
	if page.Items[0].LikedByMe || page.Items[0].CanDelete {
		t.Fatalf("已删除评论应清除点赞与删除标记: %+v", page.Items[0])
	}
	if len(stubs.likes.lastStatesIDs) != 0 {
		t.Fatal("游客不应查询点赞状态")
	}
}

// TestListCommentsErrors 覆盖读取链路上的错误透传。
func TestListCommentsErrors(t *testing.T) {
	boom := apperr.Internal("读取评论失败")
	cases := []struct {
		name  string
		setup func(*stubSet)
	}{
		{"帖子不可见", func(s *stubSet) { s.posts.visibleErr = apperr.NotFound("内容不存在或无权访问") }},
		{"顶层评论读取失败", func(s *stubSet) { s.comments.topLevelErr = boom }},
		{"回复预览读取失败", func(s *stubSet) { s.comments.previewsErr = boom }},
		{"作者摘要读取失败", func(s *stubSet) { s.users.briefsErr = boom }},
		{"点赞状态读取失败", func(s *stubSet) { s.likes.statesErr = boom }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubs := newStubSet()
			stubs.comments.topLevelRows = []model.Comment{stubCommentRow(101, stubPostID, stubOtherID)}
			tc.setup(stubs)
			_, err := stubs.service().ListComments(context.Background(), stubViewer(), stubPostID, 0, 20)
			if err == nil {
				t.Fatal("期望返回错误")
			}
		})
	}
}

// TestListReplies 验证回复分页游标、权限与错误路径。
func TestListReplies(t *testing.T) {
	ctx := context.Background()
	stubs := newStubSet()
	root := stubCommentRow(101, stubPostID, stubAuthorID)
	replyA := stubCommentRow(102, stubPostID, stubViewerID)
	replyB := stubCommentRow(103, stubPostID, stubOtherID)
	stubs.comments.comments[root.ID] = root
	stubs.comments.replyRows = []model.Comment{replyA, replyB}
	svc := stubs.service()

	page, err := svc.ListReplies(ctx, stubViewer(), root.ID, 0, 1)
	requireOK(t, err)
	if len(page.Items) != 1 || !page.HasMore || page.NextCursor != replyA.ID {
		t.Fatalf("回复分页 = %+v，期望 1 条且游标 %d", page, replyA.ID)
	}
	if list := stubs.comments.listCalls["replies"]; list.rootID != root.ID || list.limit != 2 {
		t.Fatalf("回复仓储收到 (root=%d, limit=%d)，期望 (%d, 2)", list.rootID, list.limit, root.ID)
	}
	if !page.Items[0].CanDelete {
		t.Fatal("回复作者本人应可删除自己的回复")
	}
	if page.Items[0].LikedByMe {
		t.Fatal("默认未点赞时 LikedByMe 应为 false")
	}

	// 第二条回复：访问者既非作者也非帖子作者，不可删除。
	page, err = svc.ListReplies(ctx, stubViewer(), root.ID, 0, 20)
	requireOK(t, err)
	if len(page.Items) != 2 {
		t.Fatalf("回复条数 = %d，期望 2", len(page.Items))
	}
	if page.Items[1].CanDelete {
		t.Fatal("访问者无权删除他人回复")
	}
}

// TestListRepliesErrors 覆盖回复列表的错误路径。
func TestListRepliesErrors(t *testing.T) {
	boom := apperr.Internal("读取失败")
	cases := []struct {
		name  string
		setup func(*stubSet)
	}{
		{"评论不存在", func(s *stubSet) {}},
		{"帖子不可见", func(s *stubSet) {
			s.comments.comments[101] = stubCommentRow(101, stubPostID, stubAuthorID)
			s.posts.visibleErr = apperr.NotFound("内容不存在或无权访问")
		}},
		{"回复读取失败", func(s *stubSet) {
			s.comments.comments[101] = stubCommentRow(101, stubPostID, stubAuthorID)
			s.comments.repliesErr = boom
		}},
		{"作者摘要失败", func(s *stubSet) {
			s.comments.comments[101] = stubCommentRow(101, stubPostID, stubAuthorID)
			s.comments.replyRows = []model.Comment{stubCommentRow(102, stubPostID, stubOtherID)}
			s.users.briefsErr = boom
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubs := newStubSet()
			tc.setup(stubs)
			_, err := stubs.service().ListReplies(context.Background(), stubViewer(), 101, 0, 20)
			if err == nil {
				t.Fatal("期望返回错误")
			}
		})
	}
}

// TestCreateCommentAuthAndVisibility 验证写入口鉴权与可见性前置校验。
func TestCreateCommentAuthAndVisibility(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name     string
		viewer   model.Viewer
		setup    func(*stubSet)
		wantKind apperr.Kind
	}{
		{
			name:     "游客",
			viewer:   stubGuestViewer(),
			setup:    func(*stubSet) {},
			wantKind: apperr.KindUnauthenticated,
		},
		{
			name:     "停用账号",
			viewer:   stubSuspendedViewer(),
			setup:    func(*stubSet) {},
			wantKind: apperr.KindSuspended,
		},
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
			name:     "帖子作者停用",
			viewer:   stubViewer(),
			setup:    func(s *stubSet) { s.users.suspended[stubAuthorID] = true },
			wantKind: apperr.KindForbidden,
		},
		{
			name:     "内容格式无效",
			viewer:   stubViewer(),
			setup:    func(*stubSet) {},
			wantKind: apperr.KindValidation,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubs := newStubSet()
			tc.setup(stubs)
			format := "richtext"
			if tc.wantKind == apperr.KindValidation {
				format = "html"
			}
			_, err := stubs.service().CreateComment(
				ctx, tc.viewer, stubPostID, format, "<p>你好</p>", nil,
			)
			requireKind(t, err, tc.wantKind)
		})
	}
}

// TestCreateCommentRateLimited 验证评论限频在事务前生效。
func TestCreateCommentRateLimited(t *testing.T) {
	stubs := newStubSet()
	stubs.commentLimiter = newFixedLimiter(1)
	svc := stubs.service()
	// 先手动消耗唯一额度，模拟同窗口内已有一次评论。
	if allowed, _ := svc.deps.CommentLimiter.Consume("user:" + strconv.FormatInt(stubViewerID, 10)); !allowed {
		t.Fatal("首次消费应被允许")
	}
	_, err := svc.CreateComment(context.Background(), stubViewer(), stubPostID, "richtext", "<p>你好</p>", nil)
	requireKind(t, err, apperr.KindRateLimited)
}

// TestCreateCommentParentValidation 覆盖父评论相关的校验分支。
func TestCreateCommentParentValidation(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name     string
		parentID int64
		setup    func(*stubSet)
		wantKind apperr.Kind
	}{
		{
			name:     "父评论不存在",
			parentID: 999,
			setup:    func(*stubSet) {},
			wantKind: apperr.KindValidation,
		},
		{
			name:     "父评论不属于该帖子",
			parentID: 999,
			setup: func(s *stubSet) {
				s.comments.comments[999] = stubCommentRow(999, 77, stubOtherID)
			},
			wantKind: apperr.KindValidation,
		},
		{
			name:     "父评论已删除",
			parentID: 999,
			setup: func(s *stubSet) {
				row := stubCommentRow(999, stubPostID, stubOtherID)
				row.Status = model.CommentStatusDeleted
				s.comments.comments[999] = row
			},
			wantKind: apperr.KindValidation,
		},
		{
			name:     "父评论作者停用",
			parentID: 999,
			setup: func(s *stubSet) {
				s.comments.comments[999] = stubCommentRow(999, stubPostID, stubSuspendedID)
			},
			wantKind: apperr.KindForbidden,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubs := newStubSet()
			tc.setup(stubs)
			parentID := tc.parentID
			_, err := stubs.service().CreateComment(
				ctx, stubViewer(), stubPostID, "richtext", "<p>回复</p>", &parentID,
			)
			requireKind(t, err, tc.wantKind)
		})
	}
}

// TestCreateCommentTopLevelSuccess 验证顶层评论写入：线程为空、计数 +1、通知与提及。
func TestCreateCommentTopLevelSuccess(t *testing.T) {
	stubs := newStubSet()
	svc := stubs.service()

	got, err := svc.CreateComment(
		context.Background(), stubViewer(), stubPostID, "richtext",
		"<p>你好 <strong>世界</strong> @carol</p>", nil,
	)
	requireOK(t, err)

	if got.Row.ID != 101 {
		t.Fatalf("评论 ID = %d，期望 101", got.Row.ID)
	}
	if got.Row.PostID != stubPostID || got.Row.AuthorID != stubViewerID {
		t.Fatalf("评论归属 = (post=%d, author=%d)，期望 (%d, %d)",
			got.Row.PostID, got.Row.AuthorID, stubPostID, stubViewerID)
	}
	if got.Row.Status != model.CommentStatusPublished {
		t.Fatalf("评论状态 = %q，期望 published", got.Row.Status)
	}
	if !got.Row.CreatedAt.Equal(stubNow) || !got.Row.UpdatedAt.Equal(stubNow) {
		t.Fatalf("评论时间 = (%v, %v)，期望固定时钟 %v", got.Row.CreatedAt, got.Row.UpdatedAt, stubNow)
	}
	if got.Row.ParentCommentID != nil || got.Row.RootCommentID != nil {
		t.Fatalf("顶层评论的父/根评论应为空: %+v", got.Row)
	}
	if !strings.Contains(got.Row.ContentHTML, "<strong>世界</strong>") {
		t.Fatalf("安全 HTML 未保留允许的标签: %q", got.Row.ContentHTML)
	}
	if got.Row.PlainText != "你好 世界 @carol" {
		t.Fatalf("纯文本 = %q，期望包含正文与提及", got.Row.PlainText)
	}
	if got.Author.Username != "alice" || !got.CanDelete || got.LikedByMe {
		t.Fatalf("返回视图错误: %+v", got)
	}
	if len(stubs.comments.inserts) != 1 || stubs.comments.inserts[0].ID != 101 {
		t.Fatalf("评论写入参数 = %+v", stubs.comments.inserts)
	}
	if len(stubs.posts.counters) != 1 {
		t.Fatalf("帖子计数调用次数 = %d，期望 1", len(stubs.posts.counters))
	}
	if want := (contracts.PostCounterInput{PostID: stubPostID, Counter: contracts.PostCounterComment, Delta: 1, At: stubNow}); stubs.posts.counters[0] != want {
		t.Fatalf("帖子计数参数 = %+v，期望 %+v", stubs.posts.counters[0], want)
	}
	if len(stubs.comments.adjustReplies) != 0 {
		t.Fatal("顶层评论不应调整父评论回复数")
	}

	if len(stubs.notices.notifications) != 1 {
		t.Fatalf("通知条数 = %d，期望 1（帖子作者）", len(stubs.notices.notifications))
	}
	notice := stubs.notices.notifications[0]
	if notice.RecipientID != stubAuthorID || notice.ActorID != stubViewerID {
		t.Fatalf("通知接收者/操作者 = (%d, %d)，期望 (%d, %d)",
			notice.RecipientID, notice.ActorID, stubAuthorID, stubViewerID)
	}
	if notice.Type != model.NoticeTypeComment || notice.IdempotencyKey != "comment:101" {
		t.Fatalf("通知类型/幂等键 = (%q, %q)，期望 (comment, comment:101)", notice.Type, notice.IdempotencyKey)
	}
	if notice.TargetType != model.NoticeTargetComment || notice.TargetID != 101 {
		t.Fatalf("通知目标 = (%q, %d)，期望 (comment, 101)", notice.TargetType, notice.TargetID)
	}
	if notice.PostID == nil || *notice.PostID != stubPostID || notice.CommentID == nil || *notice.CommentID != 101 {
		t.Fatalf("通知关联帖子/评论 = (%v, %v)", notice.PostID, notice.CommentID)
	}
	if !notice.At.Equal(stubNow) {
		t.Fatalf("通知时间 = %v，期望 %v", notice.At, stubNow)
	}

	if len(stubs.notices.rebuilds) != 1 || len(stubs.notices.mentions) != 1 {
		t.Fatalf("提及重建/通知调用 = (%d, %d)，期望各 1 次", len(stubs.notices.rebuilds), len(stubs.notices.mentions))
	}
	rebuild := stubs.notices.rebuilds[0]
	if rebuild.SourceType != model.NoticeSourceComment || rebuild.SourceID != 101 || rebuild.PostID != stubPostID {
		t.Fatalf("提及重建参数 = %+v", rebuild)
	}
	if len(rebuild.MentionNames) != 1 || rebuild.MentionNames[0] != "carol" {
		t.Fatalf("提及用户名 = %v，期望 [carol]", rebuild.MentionNames)
	}
	if mention := stubs.notices.mentions[0]; mention.AuthorID != stubViewerID || mention.SourceID != 101 {
		t.Fatalf("提及通知参数 = %+v", mention)
	}
}

// TestCreateCommentReplyNotifiesParent 验证回复通知去重：父评论作者是帖子作者时只发一条。
func TestCreateCommentReplyNotifiesParent(t *testing.T) {
	stubs := newStubSet()
	stubs.comments.comments[55] = stubCommentRow(55, stubPostID, stubAuthorID)
	parentID := int64(55)
	got, err := stubs.service().CreateComment(
		context.Background(), stubViewer(), stubPostID, "richtext", "<p>回复你</p>", &parentID,
	)
	requireOK(t, err)

	if got.Row.ParentCommentID == nil || *got.Row.ParentCommentID != 55 {
		t.Fatalf("ParentCommentID = %v，期望 55", got.Row.ParentCommentID)
	}
	if got.Row.RootCommentID == nil || *got.Row.RootCommentID != 55 {
		t.Fatalf("顶层父评论的 RootCommentID 应为 55，实际 %v", got.Row.RootCommentID)
	}
	if len(stubs.comments.adjustReplies) != 1 {
		t.Fatalf("父评论回复计数调用 = %d，期望 1", len(stubs.comments.adjustReplies))
	}
	if adjust := stubs.comments.adjustReplies[0]; adjust.id != 55 || adjust.delta != 1 || !adjust.at.Equal(stubNow) {
		t.Fatalf("回复计数参数 = %+v，期望 (55, +1, %v)", adjust, stubNow)
	}
	if len(stubs.notices.notifications) != 1 {
		t.Fatalf("通知条数 = %d，期望 1（父评论作者即帖子作者时只发一条）", len(stubs.notices.notifications))
	}
	notice := stubs.notices.notifications[0]
	if notice.RecipientID != stubAuthorID || notice.Type != model.NoticeTypeReply || notice.IdempotencyKey != "reply:101" {
		t.Fatalf("回复通知 = %+v，期望 reply 通知发给帖子作者且键 reply:101", notice)
	}
}

// TestCreateCommentReplyNotifiesBoth 验证父评论作者不是帖子作者时分别通知两人。
func TestCreateCommentReplyNotifiesBoth(t *testing.T) {
	stubs := newStubSet()
	stubs.comments.comments[55] = stubCommentRow(55, stubPostID, stubOtherID)
	parentID := int64(55)
	_, err := stubs.service().CreateComment(
		context.Background(), stubViewer(), stubPostID, "richtext", "<p>回复</p>", &parentID,
	)
	requireOK(t, err)

	if len(stubs.notices.notifications) != 2 {
		t.Fatalf("通知条数 = %d，期望 2", len(stubs.notices.notifications))
	}
	if got := stubs.notices.notifications[0]; got.RecipientID != stubOtherID || got.Type != model.NoticeTypeReply {
		t.Fatalf("第一条通知 = %+v，期望回复通知发给父评论作者 %d", got, stubOtherID)
	}
	if got := stubs.notices.notifications[1]; got.RecipientID != stubAuthorID || got.Type != model.NoticeTypeComment {
		t.Fatalf("第二条通知 = %+v，期望评论通知发给帖子作者 %d", got, stubAuthorID)
	}
}

// TestCreateCommentNestedReplyKeepsRoot 验证楼中楼回复沿用根评论 ID。
func TestCreateCommentNestedReplyKeepsRoot(t *testing.T) {
	stubs := newStubSet()
	rootID := int64(55)
	parent := stubCommentRow(56, stubPostID, stubOtherID)
	parent.ParentCommentID = &rootID
	parent.RootCommentID = &rootID
	stubs.comments.comments[56] = parent
	parentID := int64(56)

	got, err := stubs.service().CreateComment(
		context.Background(), stubViewer(), stubPostID, "richtext", "<p>继续回复</p>", &parentID,
	)
	requireOK(t, err)
	if got.Row.ParentCommentID == nil || *got.Row.ParentCommentID != 56 {
		t.Fatalf("ParentCommentID = %v，期望 56", got.Row.ParentCommentID)
	}
	if got.Row.RootCommentID == nil || *got.Row.RootCommentID != 55 {
		t.Fatalf("RootCommentID = %v，期望沿用根评论 55", got.Row.RootCommentID)
	}
}

// TestCreateCommentPrivatePostSkipsMentions 验证非公开帖子的评论不重建提及。
func TestCreateCommentPrivatePostSkipsMentions(t *testing.T) {
	stubs := newStubSet()
	stubs.posts.visiblePost.Visibility = model.VisibilityPrivate
	_, err := stubs.service().CreateComment(
		context.Background(), stubViewer(), stubPostID, "richtext", "<p>@carol 你好</p>", nil,
	)
	requireOK(t, err)
	if len(stubs.notices.rebuilds) != 0 || len(stubs.notices.mentions) != 0 {
		t.Fatalf("非公开内容不应产生提及: rebuilds=%d mentions=%d",
			len(stubs.notices.rebuilds), len(stubs.notices.mentions))
	}
}

// TestCreateCommentEmptyBody 验证空正文被拒绝。
func TestCreateCommentEmptyBody(t *testing.T) {
	stubs := newStubSet()
	_, err := stubs.service().CreateComment(
		context.Background(), stubViewer(), stubPostID, "richtext", "   ", nil,
	)
	requireKind(t, err, apperr.KindValidation)
}

// TestCreateCommentErrors 覆盖写入链路上的依赖错误。
func TestCreateCommentErrors(t *testing.T) {
	boom := apperr.Internal("内部错误")
	parentID := int64(55)
	cases := []struct {
		name      string
		ids       IDGenerator
		tx        TxRunner
		setup     func(*stubSet)
		useParent bool
	}{
		{"ID 生成失败", errIDs{err: boom}, nil, func(*stubSet) {}, false},
		{"事务失败", nil, errTx{err: boom}, func(*stubSet) {}, false},
		{"评论写入失败", nil, nil, func(s *stubSet) { s.comments.insertErr = boom }, false},
		{"回复计数失败", nil, nil, func(s *stubSet) {
			s.comments.comments[55] = stubCommentRow(55, stubPostID, stubOtherID)
			s.comments.adjustReplyErr = boom
		}, true},
		{"帖子计数失败", nil, nil, func(s *stubSet) { s.posts.counterErr = boom }, false},
		{"通知写入失败", nil, nil, func(s *stubSet) { s.notices.createErr = boom }, false},
		{"提及重建失败", nil, nil, func(s *stubSet) { s.notices.rebuildErr = boom }, false},
		{"提及通知失败", nil, nil, func(s *stubSet) { s.notices.mentionErr = boom }, false},
		{"作者摘要失败", nil, nil, func(s *stubSet) { s.users.briefsErr = boom }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubs := newStubSet()
			tc.setup(stubs)
			mutate := func(deps *Deps) {
				if tc.ids != nil {
					deps.IDs = tc.ids
				}
				if tc.tx != nil {
					deps.Tx = tc.tx
				}
			}
			svc := stubs.serviceWith(mutate)
			var parent *int64
			if tc.useParent {
				parent = &parentID
			}
			_, err := svc.CreateComment(context.Background(), stubViewer(), stubPostID, "richtext", "<p>你好</p>", parent)
			if err == nil {
				t.Fatal("期望返回错误")
			}
		})
	}
}

// TestDeleteComment 验证作者、帖子作者与管理员三类处置与计数回退。
func TestDeleteComment(t *testing.T) {
	ctx := context.Background()

	t.Run("评论作者删除", func(t *testing.T) {
		stubs := newStubSet()
		stubs.comments.comments[50] = stubCommentRow(50, stubPostID, stubViewerID)
		requireOK(t, stubs.service().DeleteComment(ctx, stubViewer(), 50))

		if len(stubs.comments.markDeletedCalls) != 1 {
			t.Fatalf("MarkDeleted 调用 = %d，期望 1", len(stubs.comments.markDeletedCalls))
		}
		call := stubs.comments.markDeletedCalls[0]
		if call.id != 50 || call.byAdmin || !call.byAuthor || call.byPostAuthor || !call.at.Equal(stubNow) {
			t.Fatalf("MarkDeleted 参数 = %+v，期望作者删除标记", call)
		}
		if want := (contracts.PostCounterInput{PostID: stubPostID, Counter: contracts.PostCounterComment, Delta: -1, At: stubNow}); len(stubs.posts.counters) != 1 || stubs.posts.counters[0] != want {
			t.Fatalf("计数回退参数 = %+v，期望 %+v", stubs.posts.counters, want)
		}
		if len(stubs.notices.deactivated) != 1 || stubs.notices.deactivated[0].sourceID != 50 {
			t.Fatalf("提及失效参数 = %+v，期望评论 50", stubs.notices.deactivated)
		}
		if len(stubs.audit.entries) != 0 {
			t.Fatal("非管理员删除不应写审计")
		}
	})

	t.Run("帖子作者删除他人评论", func(t *testing.T) {
		stubs := newStubSet()
		stubs.comments.comments[50] = stubCommentRow(50, stubPostID, stubOtherID)
		requireOK(t, stubs.service().DeleteComment(ctx, stubAuthorViewer(), 50))
		call := stubs.comments.markDeletedCalls[0]
		if call.byAdmin || call.byAuthor || !call.byPostAuthor {
			t.Fatalf("MarkDeleted 参数 = %+v，期望帖子作者删除标记", call)
		}
	})

	t.Run("管理员删除并写审计", func(t *testing.T) {
		stubs := newStubSet()
		stubs.comments.comments[50] = stubCommentRow(50, stubPostID, stubOtherID)
		requireOK(t, stubs.service().DeleteComment(ctx, stubAdminViewer(), 50))
		if len(stubs.audit.entries) != 1 {
			t.Fatalf("审计条数 = %d，期望 1", len(stubs.audit.entries))
		}
		entry := stubs.audit.entries[0]
		if entry.AdminID != stubAdminID || entry.Action != audit.ActionCommentDelete || entry.TargetType != "comment" {
			t.Fatalf("审计内容 = %+v", entry)
		}
		if entry.TargetID == nil || *entry.TargetID != 50 || !entry.At.Equal(stubNow) {
			t.Fatalf("审计目标/时间 = (%v, %v)", entry.TargetID, entry.At)
		}
		detail, ok := entry.Detail.(map[string]any)
		if !ok || detail["deleted_by_admin"] != true {
			t.Fatalf("审计详情 = %v，期望 deleted_by_admin=true", entry.Detail)
		}
		if !stubs.comments.markDeletedCalls[0].byAdmin {
			t.Fatal("管理员删除应带 byAdmin 标记")
		}
	})

	t.Run("回复删除回退父评论回复数", func(t *testing.T) {
		stubs := newStubSet()
		parentID := int64(55)
		reply := stubCommentRow(50, stubPostID, stubViewerID)
		reply.ParentCommentID = &parentID
		reply.RootCommentID = &parentID
		stubs.comments.comments[50] = reply
		requireOK(t, stubs.service().DeleteComment(ctx, stubViewer(), 50))
		if len(stubs.comments.adjustReplies) != 1 {
			t.Fatalf("父评论回复计数调用 = %d，期望 1", len(stubs.comments.adjustReplies))
		}
		if adjust := stubs.comments.adjustReplies[0]; adjust.id != 55 || adjust.delta != -1 {
			t.Fatalf("回复计数参数 = %+v，期望 (55, -1)", adjust)
		}
	})

	t.Run("已删除评论幂等", func(t *testing.T) {
		stubs := newStubSet()
		row := stubCommentRow(50, stubPostID, stubViewerID)
		row.Status = model.CommentStatusDeleted
		stubs.comments.comments[50] = row
		requireOK(t, stubs.service().DeleteComment(ctx, stubViewer(), 50))
		if len(stubs.comments.markDeletedCalls) != 0 || len(stubs.posts.counters) != 0 {
			t.Fatal("已删除评论不应重复处置或回退计数")
		}
	})

	t.Run("并发已删除幂等", func(t *testing.T) {
		stubs := newStubSet()
		stubs.comments.comments[50] = stubCommentRow(50, stubPostID, stubViewerID)
		stubs.comments.markDeletedChanged = false
		requireOK(t, stubs.service().DeleteComment(ctx, stubViewer(), 50))
		if len(stubs.posts.counters) != 0 {
			t.Fatal("MarkDeleted 未真正变更时不应回退帖子计数")
		}
	})
}

// TestDeleteCommentPermissionsAndErrors 覆盖无权处置与依赖错误。
func TestDeleteCommentPermissionsAndErrors(t *testing.T) {
	ctx := context.Background()
	boom := apperr.Internal("内部错误")
	cases := []struct {
		name     string
		viewer   model.Viewer
		setup    func(*stubSet)
		wantKind apperr.Kind
	}{
		{
			name:     "游客",
			viewer:   stubGuestViewer(),
			setup:    func(*stubSet) {},
			wantKind: apperr.KindUnauthenticated,
		},
		{
			name:     "停用账号",
			viewer:   stubSuspendedViewer(),
			setup:    func(*stubSet) {},
			wantKind: apperr.KindSuspended,
		},
		{
			name:     "评论读取失败",
			viewer:   stubViewer(),
			setup:    func(s *stubSet) { s.comments.getForUpdateErr = boom },
			wantKind: apperr.KindInternal,
		},
		{
			name:   "所属帖子不存在",
			viewer: stubViewer(),
			setup: func(s *stubSet) {
				s.comments.comments[50] = stubCommentRow(50, stubPostID, stubViewerID)
				s.posts.getPostErr = apperr.NotFound("内容不存在或无权访问")
			},
			wantKind: apperr.KindNotFound,
		},
		{
			name:   "无权处置按不存在处理",
			viewer: stubViewer(),
			setup: func(s *stubSet) {
				s.comments.comments[50] = stubCommentRow(50, stubPostID, stubOtherID)
			},
			wantKind: apperr.KindNotFound,
		},
		{
			name:   "处置写入失败",
			viewer: stubViewer(),
			setup: func(s *stubSet) {
				s.comments.comments[50] = stubCommentRow(50, stubPostID, stubViewerID)
				s.comments.markDeletedErr = boom
			},
			wantKind: apperr.KindInternal,
		},
		{
			name:   "计数回退失败",
			viewer: stubViewer(),
			setup: func(s *stubSet) {
				s.comments.comments[50] = stubCommentRow(50, stubPostID, stubViewerID)
				s.posts.counterErr = boom
			},
			wantKind: apperr.KindInternal,
		},
		{
			name:   "提及失效失败",
			viewer: stubViewer(),
			setup: func(s *stubSet) {
				s.comments.comments[50] = stubCommentRow(50, stubPostID, stubViewerID)
				s.notices.deactivateErr = boom
			},
			wantKind: apperr.KindInternal,
		},
		{
			name:   "审计写入失败",
			viewer: stubAdminViewer(),
			setup: func(s *stubSet) {
				s.comments.comments[50] = stubCommentRow(50, stubPostID, stubOtherID)
				s.audit.err = boom
			},
			wantKind: apperr.KindInternal,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubs := newStubSet()
			tc.setup(stubs)
			err := stubs.service().DeleteComment(ctx, tc.viewer, 50)
			requireKind(t, err, tc.wantKind)
		})
	}
}

// TestCommentDepth 验证回复深度计算与防环上限。
func TestCommentDepth(t *testing.T) {
	ctx := context.Background()
	stubs := newStubSet()
	svc := stubs.service()

	if _, err := svc.CommentDepth(ctx, 0); requireKind(t, err, apperr.KindNotFound) == nil {
		t.Fatal("非法评论 ID 应返回 not_found")
	}

	stubs.comments.parentIDs[50] = nil
	if depth, err := svc.CommentDepth(ctx, 50); err != nil || depth != 1 {
		t.Fatalf("顶层评论深度 = (%d, %v)，期望 (1, nil)", depth, err)
	}

	parent40 := int64(40)
	stubs.comments.parentIDs[40] = nil
	stubs.comments.parentIDs[50] = &parent40
	if depth, err := svc.CommentDepth(ctx, 50); err != nil || depth != 2 {
		t.Fatalf("二级回复深度 = (%d, %v)，期望 (2, nil)", depth, err)
	}

	// 构造超过上限的父链，验证循环在上限处停止。
	for i := int64(2); i <= 300; i++ {
		parent := i - 1
		stubs.comments.parentIDs[i] = &parent
	}
	if depth, err := svc.CommentDepth(ctx, 300); err != nil || depth != domain.MaxCommentDepth {
		t.Fatalf("超深回复深度 = (%d, %v)，期望 (%d, nil)", depth, err, domain.MaxCommentDepth)
	}

	stubs.comments.parentErr = apperr.Internal("读取父评论失败")
	if _, err := svc.CommentDepth(ctx, 50); requireKind(t, err, apperr.KindInternal) == nil {
		t.Fatal("父评论读取错误应透传")
	}
}

// TestCommentViews 验证批量视图组装在游客/AI/删除评论下的差异。
func TestCommentViews(t *testing.T) {
	ctx := context.Background()
	stubs := newStubSet()
	svc := stubs.service()

	empty, err := svc.commentViews(ctx, stubViewer(), stubAuthorID, nil)
	requireOK(t, err)
	if len(empty) != 0 {
		t.Fatalf("空输入应返回空 map，实际 %v", empty)
	}

	rows := []model.Comment{
		stubCommentRow(101, stubPostID, stubOtherID),
		stubCommentRow(102, stubPostID, stubAuthorID),
	}
	// 游客不查询点赞状态：即使点赞仓储配置为错误也不应触发。
	stubs.likes.statesErr = apperr.Internal("不应调用")
	views, err := svc.commentViews(ctx, stubGuestViewer(), stubAuthorID, rows)
	requireOK(t, err)
	if len(views) != 2 || views[101].CanDelete || views[102].CanDelete {
		t.Fatalf("游客视图 = %+v，期望两条均不可删除", views)
	}

	// AI 访问者同样不查询点赞状态。
	aiViewer := model.ViewerFrom(model.User{ID: stubAIID, IsAI: true, Status: model.StatusActive})
	views, err = svc.commentViews(ctx, aiViewer, stubAuthorID, rows)
	requireOK(t, err)
	if len(views) != 2 {
		t.Fatalf("AI 视图条数 = %d，期望 2", len(views))
	}

	// 登录访问者：作者本人可删除、点赞状态来自批量查询。
	stubs.likes.statesErr = nil
	stubs.likes.activeStatesMap[101] = true
	views, err = svc.commentViews(ctx, stubViewer(), stubAuthorID, rows)
	requireOK(t, err)
	if !views[101].LikedByMe {
		t.Fatal("已点赞评论应标记 LikedByMe")
	}

	deleted := stubCommentRow(103, stubPostID, stubViewerID)
	deleted.Status = model.CommentStatusDeleted
	views, err = svc.commentViews(ctx, stubViewer(), stubAuthorID, []model.Comment{deleted})
	requireOK(t, err)
	if views[103].LikedByMe || views[103].CanDelete {
		t.Fatalf("已删除评论视图未清零: %+v", views[103])
	}

	stubs.users.briefsErr = apperr.Internal("作者摘要失败")
	if _, err := svc.commentViews(ctx, stubViewer(), stubAuthorID, rows); requireKind(t, err, apperr.KindInternal) == nil {
		t.Fatal("作者摘要错误应透传")
	}

	stubs = newStubSet()
	stubs.likes.statesErr = apperr.Internal("点赞状态失败")
	if _, err := stubs.service().commentViews(ctx, stubViewer(), stubAuthorID, rows); requireKind(t, err, apperr.KindInternal) == nil {
		t.Fatal("点赞状态错误应透传")
	}
}
