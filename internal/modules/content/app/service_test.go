package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/great-magician-01/timeline/internal/modules/content/domain"
	"github.com/great-magician-01/timeline/internal/modules/content/repo"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/shared/page"
)

// 说明：本文件覆盖 content/app 中不触库的辅助函数、端口 fake 分支与时间线/查询类用例的
// 校验前置分支。触库分支（repo.*）需要数据库，不在单测范围。

// TestDisplayName 校验展示名：昵称优先，空白昵称回退用户名。
func TestDisplayName(t *testing.T) {
	cases := []struct {
		name string
		user model.User
		want string
	}{
		{name: "昵称优先", user: model.User{Username: "alice", Nickname: "爱丽丝"}, want: "爱丽丝"},
		{name: "昵称为空回退用户名", user: model.User{Username: "alice"}, want: "alice"},
		{name: "昵称仅空白回退用户名", user: model.User{Username: "alice", Nickname: "   "}, want: "alice"},
		{name: "两者皆空", user: model.User{}, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := displayName(tc.user); got != tc.want {
				t.Fatalf("displayName = %q，期望 %q", got, tc.want)
			}
		})
	}
}

// TestNormalizeScopeTerms 校验候选范围参数归一化：去空白、去重、保序、最多 20 个。
func TestNormalizeScopeTerms(t *testing.T) {
	got := normalizeScopeTerms([]string{" go ", "", "  ", "go", "rust", "Go", "rust "})
	want := []string{"go", "rust", "Go"}
	if len(got) != len(want) {
		t.Fatalf("归一化结果错误: %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("归一化结果错误: got=%#v want=%#v", got, want)
		}
	}
	if empty := normalizeScopeTerms(nil); empty == nil || len(empty) != 0 {
		t.Fatalf("空输入应返回空切片，实际 %#v", empty)
	}

	// 超过 20 个时截断。
	values := make([]string, 0, 30)
	for i := 0; i < 30; i++ {
		values = append(values, string(rune('a'+i%26))+string(rune('0'+i/26)))
	}
	if got := normalizeScopeTerms(values); len(got) != 20 {
		t.Fatalf("应最多保留 20 个词，实际 %d", len(got))
	}
}

// TestSelectFollowingIDs 校验关注时间线的关注集合传递。
func TestSelectFollowingIDs(t *testing.T) {
	ids := []int64{3, 1, 2}
	if got := selectFollowingIDs(false, ids); got != nil {
		t.Fatalf("非关注时间线不应传关注集合，实际 %#v", got)
	}
	got := selectFollowingIDs(true, ids)
	if len(got) != 3 || got[0] != 3 || got[2] != 2 {
		t.Fatalf("关注时间线应原样传关注集合，实际 %#v", got)
	}
	if got := selectFollowingIDs(true, nil); got != nil {
		t.Fatalf("空关注集合应为 nil，实际 %#v", got)
	}
}

// TestPublicContentNotFound 校验「帖子行不存在」统一对外文案。
func TestPublicContentNotFound(t *testing.T) {
	if err := publicContentNotFound(nil); err != nil {
		t.Fatalf("nil 应返回 nil，实际 %v", err)
	}
	e := requireKind(t, publicContentNotFound(apperr.NotFound("任意内部文案")), apperr.KindNotFound)
	if e.Message != "内容不存在或无权访问" {
		t.Fatalf("对外文案应统一，实际 %q", e.Message)
	}
	// 非 not_found 错误原样返回。
	validation := apperr.Validation("参数无效")
	if got := publicContentNotFound(validation); got != validation {
		t.Fatalf("非 not_found 错误应原样返回，实际 %v", got)
	}
	generic := errors.New("连接失败")
	if got := publicContentNotFound(generic); got != generic {
		t.Fatalf("普通错误应原样返回，实际 %v", got)
	}
}

// TestWrap 校验已知领域错误透传、未知错误包装为内部错误并保留底因。
func TestWrap(t *testing.T) {
	svc := newTestService(t, Deps{Log: discardLogger()})
	if err := svc.wrap(nil, "读取失败"); err != nil {
		t.Fatalf("nil 应返回 nil，实际 %v", err)
	}
	known := apperr.Validation("参数无效")
	if got := svc.wrap(known, "读取失败"); got != known {
		t.Fatalf("已知领域错误应原样返回，实际 %v", got)
	}
	cause := errors.New("数据库中断")
	err := svc.wrap(cause, "读取失败")
	e := requireKind(t, err, apperr.KindInternal)
	if e.Message != "服务暂时异常，请稍后重试" {
		t.Fatalf("内部错误文案应统一，实际 %q", e.Message)
	}
	if !errors.Is(err, cause) {
		t.Fatal("包装后的错误应保留底因")
	}

	// 无日志器时同样不应 panic。
	silent := newTestService(t, Deps{})
	requireKind(t, silent.wrap(cause, "读取失败"), apperr.KindInternal)
}

// TestMustInternal 校验内部错误包装辅助函数。
func TestMustInternal(t *testing.T) {
	svc := newTestService(t, Deps{})
	if err := svc.mustInternal(nil, "生成 ID 失败"); err != nil {
		t.Fatalf("nil 应返回 nil，实际 %v", err)
	}
	cause := errors.New("雪花失败")
	err := svc.mustInternal(cause, "生成 ID 失败")
	requireKind(t, err, apperr.KindInternal)
	if !errors.Is(err, cause) {
		t.Fatal("应保留底因")
	}
}

// TestNextID 校验雪花 ID 生成的成功与失败映射。
func TestNextID(t *testing.T) {
	svc := newTestService(t, Deps{IDs: fixedIDs{id: 88}})
	id, err := svc.nextID()
	if err != nil || id != 88 {
		t.Fatalf("生成 ID 失败: id=%d err=%v", id, err)
	}

	cause := errors.New("雪花时钟回拨")
	svc = newTestService(t, Deps{IDs: failingIDs{err: cause}})
	_, err = svc.nextID()
	e := requireKind(t, err, apperr.KindInternal)
	if e.Message != "生成资源 ID 失败" {
		t.Fatalf("内部错误文案错误: %q", e.Message)
	}
}

// TestServiceNowAndLog 校验取时来源与日志器暴露。
func TestServiceNowAndLog(t *testing.T) {
	logger := discardLogger()
	svc := newTestService(t, Deps{Clock: clock.NewFake(contentTestNow), Log: logger})
	if got := svc.Now(); !got.Equal(contentTestNow) {
		t.Fatalf("Now 应返回注入时钟时间，实际 %v", got)
	}
	if svc.Log() != logger {
		t.Fatal("Log 应返回注入的日志器")
	}
	if newTestService(t, Deps{}).Log() != nil {
		t.Fatal("未注入日志器时 Log 应返回 nil")
	}
}

// TestLoadUser 校验作者账号读取与 not_found 文案映射。
func TestLoadUser(t *testing.T) {
	ctx := context.Background()
	// 账号端口缺失时退化为占位用户（仅用于内部任务路径）。
	svc := newTestService(t, Deps{})
	user, err := svc.loadUser(ctx, 9, "作者账号不存在")
	if err != nil || user.ID != 9 || !user.IsActive() || user.IsAI {
		t.Fatalf("缺省账号占位错误: %+v, %v", user, err)
	}

	svc = newTestService(t, Deps{Users: fakeUsers{user: model.User{ID: 9, Status: model.StatusActive}}})
	user, err = svc.loadUser(ctx, 9, "作者账号不存在")
	if err != nil || user.ID != 9 {
		t.Fatalf("读取账号失败: %+v, %v", user, err)
	}

	svc = newTestService(t, Deps{Users: fakeUsers{getErr: apperr.NotFound("账号不存在")}})
	_, err = svc.loadUser(ctx, 9, "作者账号不存在")
	e := requireKind(t, err, apperr.KindNotFound)
	if e.Message != "作者账号不存在" {
		t.Fatalf("not_found 文案错误: %q", e.Message)
	}

	cause := errors.New("账号服务不可用")
	svc = newTestService(t, Deps{Users: fakeUsers{getErr: cause}})
	_, err = svc.loadUser(ctx, 9, "作者账号不存在")
	requireKind(t, err, apperr.KindInternal)
	if !errors.Is(err, cause) {
		t.Fatal("应保留账号端口错误")
	}
}

// TestFollowingSet 校验关注集合读取：游客/无端口返回空集合，端口错误映射内部错误。
func TestFollowingSet(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, Deps{})
	set, err := svc.followingSet(ctx, guestViewer())
	if err != nil || len(set) != 0 {
		t.Fatalf("游客应返回空关注集合: %#v, %v", set, err)
	}
	// 已登录但社会端口缺失也返回空集合。
	set, err = svc.followingSet(ctx, humanViewer(1))
	if err != nil || len(set) != 0 {
		t.Fatalf("端口缺失时应返回空关注集合: %#v, %v", set, err)
	}

	svc = newTestService(t, Deps{Social: fakeSocial{following: map[int64]bool{2: true, 3: false}}})
	set, err = svc.followingSet(ctx, humanViewer(1))
	if err != nil || len(set) != 2 {
		t.Fatalf("关注集合读取错误: %#v, %v", set, err)
	}

	cause := errors.New("社交服务不可用")
	svc = newTestService(t, Deps{Social: fakeSocial{followErr: cause}})
	_, err = svc.followingSet(ctx, humanViewer(1))
	requireKind(t, err, apperr.KindInternal)
	if !errors.Is(err, cause) {
		t.Fatal("应保留社交端口错误")
	}
}

// TestRequireVisible 校验帖子可见性校验（not_found 不暴露存在性）。
func TestRequireVisible(t *testing.T) {
	ctx := context.Background()
	published := func(visibility string) model.Post {
		return model.Post{ID: 1, AuthorID: 2, Status: model.PostStatusPublished, Visibility: visibility}
	}
	cases := []struct {
		name    string
		viewer  model.Viewer
		post    model.Post
		social  contracts.SocialService
		wantErr apperr.Kind
	}{
		{
			name:   "游客可看公开已发布",
			viewer: guestViewer(),
			post:   published(model.VisibilityPublic),
		},
		{
			name:    "游客不可看仅关注者",
			viewer:  guestViewer(),
			post:    published(model.VisibilityFollowers),
			wantErr: apperr.KindNotFound,
		},
		{
			name:    "游客不可看仅自己",
			viewer:  guestViewer(),
			post:    published(model.VisibilityPrivate),
			wantErr: apperr.KindNotFound,
		},
		{
			name:   "关注者可看仅关注者",
			viewer: humanViewer(1),
			post:   published(model.VisibilityFollowers),
			social: fakeSocial{following: map[int64]bool{2: true}},
		},
		{
			name:    "未关注不可看仅关注者",
			viewer:  humanViewer(1),
			post:    published(model.VisibilityFollowers),
			social:  fakeSocial{following: map[int64]bool{}},
			wantErr: apperr.KindNotFound,
		},
		{
			name:   "公开内容不读取关注集合",
			viewer: humanViewer(1),
			post:   published(model.VisibilityPublic),
			// 公开内容可见性不依赖关注关系：即使社交端口不可用也应放行，证明未发起查询。
			social: fakeSocial{followErr: errors.New("社交服务不可用")},
		},
		{
			name:   "作者本人不读取关注集合",
			viewer: humanViewer(2),
			post:   published(model.VisibilityFollowers),
			social: fakeSocial{followErr: errors.New("社交服务不可用")},
		},
		{
			name:   "管理员不读取关注集合",
			viewer: adminViewer(9),
			post:   published(model.VisibilityFollowers),
			social: fakeSocial{followErr: errors.New("社交服务不可用")},
		},
		{
			name:    "社交端口失败",
			viewer:  humanViewer(1),
			post:    published(model.VisibilityFollowers),
			social:  fakeSocial{followErr: errors.New("社交服务不可用")},
			wantErr: apperr.KindInternal,
		},
		{
			name:   "作者本人可看草稿",
			viewer: humanViewer(2),
			post:   model.Post{ID: 1, AuthorID: 2, Status: model.PostStatusDraft},
		},
		{
			name:    "他人不可看草稿",
			viewer:  humanViewer(1),
			post:    model.Post{ID: 1, AuthorID: 2, Status: model.PostStatusDraft},
			wantErr: apperr.KindNotFound,
		},
		{
			name:   "管理员可看已删除",
			viewer: adminViewer(9),
			post:   model.Post{ID: 1, AuthorID: 2, Status: model.PostStatusDeleted, Visibility: model.VisibilityPrivate},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestService(t, Deps{Social: tc.social})
			_, err := svc.requireVisible(ctx, tc.viewer, tc.post)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("应可见，实际 %v", err)
				}
				return
			}
			requireKind(t, err, tc.wantErr)
		})
	}
}

// TestRequireAuthor 校验作者校验：不可见返回 not_found，可见但非作者返回 forbidden。
// 作者本人不读取关注集合（社交端口不可用也应通过）。
func TestRequireAuthor(t *testing.T) {
	ctx := context.Background()
	brokenSocial := fakeSocial{followErr: errors.New("社交服务不可用")}
	svc := newTestService(t, Deps{Social: brokenSocial})
	if _, err := svc.requireAuthor(ctx, humanViewer(2),
		model.Post{ID: 1, AuthorID: 2, Status: model.PostStatusDraft}); err != nil {
		t.Fatalf("作者本人应通过（且不读取关注集合）: %v", err)
	}
	if _, err := svc.requireAuthor(ctx, humanViewer(2),
		model.Post{ID: 1, AuthorID: 2, Status: model.PostStatusPublished, Visibility: model.VisibilityFollowers}); err != nil {
		t.Fatalf("作者本人应通过（且不读取关注集合）: %v", err)
	}
	_, err := svc.requireAuthor(ctx, humanViewer(1),
		model.Post{ID: 1, AuthorID: 2, Status: model.PostStatusPublished, Visibility: model.VisibilityPublic})
	e := requireKind(t, err, apperr.KindForbidden)
	if e.Message != "只有作者可以执行此操作" {
		t.Fatalf("非作者文案错误: %q", e.Message)
	}
	_, err = svc.requireAuthor(ctx, humanViewer(1),
		model.Post{ID: 1, AuthorID: 2, Status: model.PostStatusPublished, Visibility: model.VisibilityPrivate})
	requireKind(t, err, apperr.KindNotFound)

	// 非作者的仅关注者内容需要读取关注集合，端口失败映射内部错误。
	_, err = svc.requireAuthor(ctx, humanViewer(1),
		model.Post{ID: 1, AuthorID: 2, Status: model.PostStatusPublished, Visibility: model.VisibilityFollowers})
	requireKind(t, err, apperr.KindInternal)
}

// TestNeedsFollowingSet 校验关注集合懒加载判定：只有「已发布 + 仅关注者可见 + 登录人类
// 且非作者/管理员」时可见性才取决于关注关系，其余情况都不应读取关注集合。
func TestNeedsFollowingSet(t *testing.T) {
	cases := []struct {
		name   string
		viewer model.Viewer
		post   model.Post
		want   bool
	}{
		{
			name:   "登录人类看他人仅关注者内容",
			viewer: humanViewer(1),
			post:   model.Post{AuthorID: 2, Status: model.PostStatusPublished, Visibility: model.VisibilityFollowers},
			want:   true,
		},
		{
			name:   "公开内容",
			viewer: humanViewer(1),
			post:   model.Post{AuthorID: 2, Status: model.PostStatusPublished, Visibility: model.VisibilityPublic},
			want:   false,
		},
		{
			name:   "仅自己可见",
			viewer: humanViewer(1),
			post:   model.Post{AuthorID: 2, Status: model.PostStatusPublished, Visibility: model.VisibilityPrivate},
			want:   false,
		},
		{
			name:   "草稿",
			viewer: humanViewer(1),
			post:   model.Post{AuthorID: 2, Status: model.PostStatusDraft, Visibility: model.VisibilityFollowers},
			want:   false,
		},
		{
			name:   "已删除",
			viewer: humanViewer(1),
			post:   model.Post{AuthorID: 2, Status: model.PostStatusDeleted, Visibility: model.VisibilityFollowers},
			want:   false,
		},
		{
			name:   "游客",
			viewer: guestViewer(),
			post:   model.Post{AuthorID: 2, Status: model.PostStatusPublished, Visibility: model.VisibilityFollowers},
			want:   false,
		},
		{
			name:   "AI 访问者",
			viewer: model.Viewer{ID: 1, IsAI: true, Authenticated: true},
			post:   model.Post{AuthorID: 2, Status: model.PostStatusPublished, Visibility: model.VisibilityFollowers},
			want:   false,
		},
		{
			name:   "管理员",
			viewer: adminViewer(9),
			post:   model.Post{AuthorID: 2, Status: model.PostStatusPublished, Visibility: model.VisibilityFollowers},
			want:   false,
		},
		{
			name:   "作者本人",
			viewer: humanViewer(2),
			post:   model.Post{AuthorID: 2, Status: model.PostStatusPublished, Visibility: model.VisibilityFollowers},
			want:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := needsFollowingSet(tc.viewer, tc.post); got != tc.want {
				t.Fatalf("needsFollowingSet = %v，期望 %v", got, tc.want)
			}
		})
	}
}

// TestAuthorTypeFilter 校验时间线 human/ai 过滤映射为 author_is_ai 标志。
func TestAuthorTypeFilter(t *testing.T) {
	if got := authorTypeFilter(domain.FilterAll); got != nil {
		t.Fatalf("all 过滤不应设置作者类型标志: %v", *got)
	}
	if got := authorTypeFilter("unknown"); got != nil {
		t.Fatalf("未知过滤不应设置作者类型标志: %v", *got)
	}
	human := authorTypeFilter(domain.FilterHuman)
	if human == nil || *human {
		t.Fatalf("human 过滤应映射为 false: %v", human)
	}
	ai := authorTypeFilter(domain.FilterAI)
	if ai == nil || !*ai {
		t.Fatalf("ai 过滤应映射为 true: %v", ai)
	}
}

// TestRankTagSuggestions 校验标签补全排序（帖子数倒序、同名次按名称升序）与上限截断。
func TestRankTagSuggestions(t *testing.T) {
	items := []TagSuggestion{
		{Name: "go", PostCount: 3},
		{Name: "golang", PostCount: 5},
		{Name: "gopher", PostCount: 3},
		{Name: "goroutine", PostCount: 0},
	}
	ranked := rankTagSuggestions(items)
	want := []string{"golang", "go", "gopher", "goroutine"}
	if len(ranked) != len(want) {
		t.Fatalf("排序结果数量错误: %#v", ranked)
	}
	for i := range want {
		if ranked[i].Name != want[i] {
			t.Fatalf("排序结果错误: got=%v want=%v", ranked, want)
		}
	}

	// 超过上限时截断到 MaxSuggestCount。
	many := make([]TagSuggestion, 0, domain.MaxSuggestCount+5)
	for i := 0; i < domain.MaxSuggestCount+5; i++ {
		many = append(many, TagSuggestion{Name: "tag" + string(rune('a'+i)), PostCount: i})
	}
	if got := rankTagSuggestions(many); len(got) != domain.MaxSuggestCount {
		t.Fatalf("应截断到 %d 条，实际 %d", domain.MaxSuggestCount, len(got))
	}
	if got := rankTagSuggestions(nil); len(got) != 0 {
		t.Fatalf("空输入应返回空结果，实际 %#v", got)
	}
}

// TestTimelineCandidateKey 校验时间线候选游标键。
func TestTimelineCandidateKey(t *testing.T) {
	sortAt := contentTestNow
	candidate := timelineCandidate{row: repo.CandidateRow{SortAt: sortAt, ItemID: 42}}
	gotTime, gotID := candidate.key()
	if !gotTime.Equal(sortAt) || gotID != 42 {
		t.Fatalf("候选游标键错误: %v, %d", gotTime, gotID)
	}
}

// TestAvailableAttachmentViews 校验附件展示视图只保留有效附件且保持顺序。
func TestAvailableAttachmentViews(t *testing.T) {
	rows := map[int64]model.Attachment{
		1: {ID: 1, Status: model.AttachmentAvailable},
		2: {ID: 2, Status: model.AttachmentDeleted},
		3: {ID: 3, Status: model.AttachmentAvailable},
	}
	derivatives := map[int64]map[string]string{
		1: {model.DerivativeThumbnail: model.DerivativeReady},
	}
	views := availableAttachmentViews([]int64{1, 2, 3, 4}, rows, derivatives)
	if len(views) != 2 {
		t.Fatalf("应保留 2 个有效附件，实际 %d", len(views))
	}
	if views[0].Attachment.ID != 1 || views[1].Attachment.ID != 3 {
		t.Fatalf("附件顺序错误: %+v", views)
	}
	if views[0].Derivatives[model.DerivativeThumbnail] != model.DerivativeReady {
		t.Fatalf("衍生文件状态未透传: %+v", views[0].Derivatives)
	}
	if got := availableAttachmentViews(nil, nil, nil); got == nil || len(got) != 0 {
		t.Fatalf("空输入应返回空切片，实际 %#v", got)
	}
}

// TestApplyCreateAction 校验创建动作分派：草稿直接成功、定时缺时间与未知动作被拒绝。
func TestApplyCreateAction(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, Deps{})
	at := contentTestNow

	if err := svc.applyCreateAction(ctx, 1, 2, domain.ActionDraft, nil, at); err != nil {
		t.Fatalf("草稿动作不应触库且应成功: %v", err)
	}

	err := svc.applyCreateAction(ctx, 1, 2, domain.ActionSchedule, nil, at)
	e := requireKind(t, err, apperr.KindValidation)
	if e.Message != "请填写定时发布时间" {
		t.Fatalf("缺少定时时间文案错误: %q", e.Message)
	}

	requireKind(t, svc.applyCreateAction(ctx, 1, 2, "unknown", nil, at), apperr.KindValidation)
}

// TestAdjustPostCounterValidation 校验计数器参数在触库前被拒绝。
func TestAdjustPostCounterValidation(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, Deps{})
	_, err := svc.BumpPostCounter(ctx, contracts.PostCounterInput{PostID: 1, Counter: "like", Delta: 0})
	requireKind(t, err, apperr.KindValidation)

	_, err = svc.AdjustPostCounter(ctx, contracts.PostCounterInput{PostID: 1, Counter: "bad", Delta: 1})
	e := requireKind(t, err, apperr.KindValidation)
	if e.Message != "计数器类型无效" {
		t.Fatalf("计数器类型错误文案错误: %q", e.Message)
	}
}

// TestListCandidatePostsEarlyReturns 校验候选范围在触库前返回的路径。
func TestListCandidatePostsEarlyReturns(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		deps  Deps
		query contracts.CandidateQuery
	}{
		{name: "空范围", deps: Deps{}, query: contracts.CandidateQuery{}},
		{name: "范围 none", deps: Deps{}, query: contracts.CandidateQuery{ScopeType: "none"}},
		{name: "关注范围但无社交端口", deps: Deps{}, query: contracts.CandidateQuery{ScopeType: "followed"}},
		{name: "标签范围但无标签", deps: Deps{}, query: contracts.CandidateQuery{ScopeType: "tags"}},
		{name: "作者范围但无账号端口", deps: Deps{}, query: contracts.CandidateQuery{ScopeType: "authors"}},
		{name: "未知范围", deps: Deps{}, query: contracts.CandidateQuery{ScopeType: "unknown"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestService(t, tc.deps)
			got, err := svc.ListCandidatePosts(ctx, tc.query)
			if err != nil || got != nil {
				t.Fatalf("应在触库前返回 (nil, nil)，实际 (%#v, %v)", got, err)
			}
		})
	}

	// 关注集合读取失败映射内部错误。
	svc := newTestService(t, Deps{Social: fakeSocial{followErr: errors.New("社交服务不可用")}})
	_, err := svc.ListCandidatePosts(ctx, contracts.CandidateQuery{ScopeType: "followed"})
	requireKind(t, err, apperr.KindInternal)
}

// TestListMyDraftsGuards 校验草稿/定时列表的登录与 AI 账号校验。
func TestListMyDraftsGuards(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, Deps{})
	if _, err := svc.ListMyDrafts(ctx, guestViewer(), 0, 20); !apperr.IsKind(err, apperr.KindUnauthenticated) {
		t.Fatalf("游客应返回 unauthenticated，实际 %v", err)
	}
	if _, err := svc.ListMyScheduled(ctx, guestViewer(), 0, 20); !apperr.IsKind(err, apperr.KindUnauthenticated) {
		t.Fatalf("游客应返回 unauthenticated，实际 %v", err)
	}
	aiViewer := model.Viewer{ID: 5, IsAI: true, Authenticated: true}
	if _, err := svc.ListMyDrafts(ctx, aiViewer, 0, 20); !apperr.IsKind(err, apperr.KindForbidden) {
		t.Fatalf("AI 账号应返回 forbidden，实际 %v", err)
	}
	if _, err := svc.ListMyScheduled(ctx, aiViewer, 0, 20); !apperr.IsKind(err, apperr.KindForbidden) {
		t.Fatalf("AI 账号应返回 forbidden，实际 %v", err)
	}
}

// TestTimelineFilterValidation 校验时间线 filter 参数校验。
func TestTimelineFilterValidation(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, Deps{})
	_, err := svc.Timeline(ctx, guestViewer(), "bad", page.TimeCursor{}, 20)
	e := requireKind(t, err, apperr.KindInvalid)
	if e.Message != "filter 参数无效" {
		t.Fatalf("filter 错误文案错误: %q", e.Message)
	}
}

// TestFollowingTimelineGuards 校验关注时间线的登录/AI/参数校验。
func TestFollowingTimelineGuards(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, Deps{})
	_, err := svc.FollowingTimeline(ctx, guestViewer(), "all", page.TimeCursor{}, 20)
	requireKind(t, err, apperr.KindUnauthenticated)

	aiViewer := model.Viewer{ID: 5, IsAI: true, Authenticated: true}
	_, err = svc.FollowingTimeline(ctx, aiViewer, "all", page.TimeCursor{}, 20)
	requireKind(t, err, apperr.KindForbidden)

	_, err = svc.FollowingTimeline(ctx, humanViewer(1), "bad", page.TimeCursor{}, 20)
	requireKind(t, err, apperr.KindInvalid)
}

// TestUserTimelineGuards 校验个人主页时间线的页签/账号端口校验。
func TestUserTimelineGuards(t *testing.T) {
	ctx := context.Background()
	_, err := newTestService(t, Deps{}).UserTimeline(ctx, guestViewer(), "alice", "bad", page.TimeCursor{}, 20)
	requireKind(t, err, apperr.KindInvalid)

	_, err = newTestService(t, Deps{}).UserTimeline(ctx, guestViewer(), "alice", "posts", page.TimeCursor{}, 20)
	e := requireKind(t, err, apperr.KindInternal)
	if e.Message != "账号服务未就绪" {
		t.Fatalf("账号端口缺失文案错误: %q", e.Message)
	}

	notFound := newTestService(t, Deps{Users: fakeUsers{byNameErr: apperr.NotFound("账号不存在")}})
	_, err = notFound.UserTimeline(ctx, guestViewer(), "alice", "posts", page.TimeCursor{}, 20)
	e = requireKind(t, err, apperr.KindNotFound)
	if e.Message != "用户不存在" {
		t.Fatalf("not_found 文案错误: %q", e.Message)
	}

	broken := newTestService(t, Deps{Users: fakeUsers{byNameErr: errors.New("账号服务不可用")}})
	_, err = broken.UserTimeline(ctx, guestViewer(), "alice", "posts", page.TimeCursor{}, 20)
	requireKind(t, err, apperr.KindInternal)

	// replies 页签需要互动端口。
	okUsers := newTestService(t, Deps{Users: fakeUsers{byUsername: model.User{ID: 3}}})
	_, err = okUsers.UserTimeline(ctx, guestViewer(), "alice", "replies", page.TimeCursor{}, 20)
	e = requireKind(t, err, apperr.KindInternal)
	if e.Message != "互动服务未就绪" {
		t.Fatalf("互动端口缺失文案错误: %q", e.Message)
	}
}

// TestSearchTagSuggestGuards 校验搜索/标签/补全类用例在触库前的参数与端口校验。
func TestSearchTagSuggestGuards(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, Deps{})

	_, err := svc.SearchPosts(ctx, guestViewer(), "   ", page.TimeCursor{}, 20)
	requireKind(t, err, apperr.KindValidation)

	timeline, err := svc.TagTimeline(ctx, guestViewer(), "   ", page.TimeCursor{}, 20)
	if err != nil || len(timeline.Posts) != 0 || timeline.HasMore {
		t.Fatalf("空标签应返回空列表: %+v, %v", timeline, err)
	}

	items, err := svc.SuggestTags(ctx, guestViewer(), "")
	if err != nil || len(items) != 0 {
		t.Fatalf("空关键词应返回空补全: %#v, %v", items, err)
	}
	items, err = svc.SuggestTags(ctx, guestViewer(), strings.Repeat("长", domain.MaxTagRunes+1))
	if err != nil || len(items) != 0 {
		t.Fatalf("超长关键词应返回空补全: %#v, %v", items, err)
	}
}

// TestLinkAttachments 校验发布附件关联：空集合直接成功、端口缺失/失败返回错误。
func TestLinkAttachments(t *testing.T) {
	ctx := context.Background()
	at := contentTestNow

	if err := newTestService(t, Deps{}).linkAttachments(ctx, 1, nil, at); err != nil {
		t.Fatalf("空附件应返回 nil，实际 %v", err)
	}
	err := newTestService(t, Deps{}).linkAttachments(ctx, 1, []int64{2}, at)
	e := requireKind(t, err, apperr.KindInternal)
	if e.Message != "附件服务未就绪" {
		t.Fatalf("附件端口缺失文案错误: %q", e.Message)
	}

	media := &fakeMedia{}
	if err := newTestService(t, Deps{Media: media}).linkAttachments(ctx, 1, []int64{2, 3}, at); err != nil {
		t.Fatalf("关联附件失败: %v", err)
	}
	if len(media.linked) != 2 || media.linked[0] != 2 || media.linked[1] != 3 || !media.linkedAt.Equal(at) {
		t.Fatalf("关联附件参数错误: %+v at=%v", media.linked, media.linkedAt)
	}

	cause := errors.New("附件服务不可用")
	media = &fakeMedia{linkErr: cause}
	err = newTestService(t, Deps{Media: media}).linkAttachments(ctx, 1, []int64{2}, at)
	requireKind(t, err, apperr.KindInternal)
	if !errors.Is(err, cause) {
		t.Fatal("应保留附件端口错误")
	}
}

// TestNotifyQuote 校验无引用/无通知端口时直接成功。
func TestNotifyQuote(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, Deps{})
	if err := svc.notifyQuote(ctx, model.Post{ID: 1}, contentTestNow); err != nil {
		t.Fatalf("无引用对象应返回 nil，实际 %v", err)
	}
	quotedID := int64(2)
	if err := svc.notifyQuote(ctx, model.Post{ID: 1, QuotedPostID: &quotedID}, contentTestNow); err != nil {
		t.Fatalf("无通知端口应返回 nil，实际 %v", err)
	}
}

// TestPublishJobHelpersWithoutQueue 校验作业端口缺失时定时发布辅助函数直接成功。
func TestPublishJobHelpersWithoutQueue(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, Deps{})
	if err := svc.enqueuePublishJob(ctx, 1, contentTestNow, contentTestNow); err != nil {
		t.Fatalf("无作业端口时入队应返回 nil，实际 %v", err)
	}
	if err := svc.cancelPublishJobs(ctx, 1, contentTestNow); err != nil {
		t.Fatalf("无作业端口时取消应返回 nil，实际 %v", err)
	}
	jobs, err := svc.publishJobsByRef(ctx, nil)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("空帖子列表应返回空作业映射: %#v, %v", jobs, err)
	}
}
