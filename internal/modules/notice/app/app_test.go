package app

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/notice/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// testNow 是假时钟的固定起点，所有用例必须避免依赖真实时间。
var testNow = time.Date(2026, 5, 1, 12, 0, 0, 0, clock.AsiaShanghai)

// stringPtr 返回字符串指针，便于断言预览等可空字段。
func stringPtr(value string) *string { return &value }

// int64Ptr 返回 int64 指针，便于构造 PostID/CommentID 等可空列。
func int64Ptr(value int64) *int64 { return &value }

// ===== 假端口（notice app 用例层的无数据库测试替身） =====

// fakeIDs 按预设序列返回雪花 ID，可注入生成失败。
type fakeIDs struct {
	ids  []int64
	err  error
	next int
}

// Next 实现 IDGenerator。
func (f *fakeIDs) Next() (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	if f.next >= len(f.ids) {
		return 0, errors.New("测试 ID 序列已耗尽")
	}
	id := f.ids[f.next]
	f.next++
	return id, nil
}

// fakeUsers 实现 contracts.UserReader 中被通知域用到的方法；
// 未实现的方法由嵌入接口兜底，一旦越界调用会 panic 暴露问题。
type fakeUsers struct {
	contracts.UserReader
	users      map[int64]model.User
	briefs     map[int64]model.UserBrief
	findBriefs []model.UserBrief

	getUserErr error
	briefsErr  error
	findErr    error

	getUserCalls []int64
	briefCalls   [][]int64
	findCalls    [][]string
}

// GetUser 读取账号行；未配置的 ID 返回领域 not_found。
func (f *fakeUsers) GetUser(_ context.Context, userID int64) (model.User, error) {
	f.getUserCalls = append(f.getUserCalls, userID)
	if f.getUserErr != nil {
		return model.User{}, f.getUserErr
	}
	user, ok := f.users[userID]
	if !ok {
		return model.User{}, apperr.NotFound("用户不存在")
	}
	return user, nil
}

// GetUserBriefs 批量读取作者摘要。
func (f *fakeUsers) GetUserBriefs(_ context.Context, userIDs []int64) (map[int64]model.UserBrief, error) {
	f.briefCalls = append(f.briefCalls, slices.Clone(userIDs))
	if f.briefsErr != nil {
		return nil, f.briefsErr
	}
	result := make(map[int64]model.UserBrief, len(userIDs))
	for _, id := range userIDs {
		if brief, ok := f.briefs[id]; ok {
			result[id] = brief
		}
	}
	return result, nil
}

// FindActiveUsersByUsernames 按用户名解析生效账号。
func (f *fakeUsers) FindActiveUsersByUsernames(_ context.Context, usernames []string) ([]model.UserBrief, error) {
	f.findCalls = append(f.findCalls, slices.Clone(usernames))
	if f.findErr != nil {
		return nil, f.findErr
	}
	return f.findBriefs, nil
}

// fakePosts 实现 contracts.ContentService 中的帖子批量读取方法。
type fakePosts struct {
	contracts.ContentService
	posts   map[int64]model.Post
	texts   map[int64]string
	getErr  error
	textErr error

	getCalls  [][]int64
	textCalls [][]int64
}

// GetPosts 批量读取帖子行。
func (f *fakePosts) GetPosts(_ context.Context, postIDs []int64) (map[int64]model.Post, error) {
	f.getCalls = append(f.getCalls, slices.Clone(postIDs))
	if f.getErr != nil {
		return nil, f.getErr
	}
	result := make(map[int64]model.Post, len(postIDs))
	for _, id := range postIDs {
		if post, ok := f.posts[id]; ok {
			result[id] = post
		}
	}
	return result, nil
}

// GetPostPlainTexts 批量读取帖子搜索投影的纯文本。
func (f *fakePosts) GetPostPlainTexts(_ context.Context, postIDs []int64) (map[int64]string, error) {
	f.textCalls = append(f.textCalls, slices.Clone(postIDs))
	if f.textErr != nil {
		return nil, f.textErr
	}
	result := make(map[int64]string, len(postIDs))
	for _, id := range postIDs {
		if text, ok := f.texts[id]; ok {
			result[id] = text
		}
	}
	return result, nil
}

// fakeRelations 实现 contracts.RelationReader 中的关注集合读取。
type fakeRelations struct {
	contracts.RelationReader
	following map[int64]bool
	err       error
	calls     []int64
}

// FollowingSet 返回访问者的生效关注集合。
func (f *fakeRelations) FollowingSet(_ context.Context, followerID int64) (map[int64]bool, error) {
	f.calls = append(f.calls, followerID)
	if f.err != nil {
		return nil, f.err
	}
	return f.following, nil
}

// fakeComments 实现 app.CommentReader。
type fakeComments struct {
	comments map[int64]model.Comment
	err      error
	calls    [][]int64
}

// GetComments 批量读取评论。
func (f *fakeComments) GetComments(_ context.Context, commentIDs []int64) (map[int64]model.Comment, error) {
	f.calls = append(f.calls, slices.Clone(commentIDs))
	if f.err != nil {
		return nil, f.err
	}
	result := make(map[int64]model.Comment, len(commentIDs))
	for _, id := range commentIDs {
		if comment, ok := f.comments[id]; ok {
			result[id] = comment
		}
	}
	return result, nil
}

// noticeListCall 记录通知列表的一次仓储调用参数。
type noticeListCall struct {
	recipientID int64
	filter      string
	noticeType  string
	cursor      int64
	limit       int
}

// markReadCall 记录单条已读的一次仓储调用参数。
type markReadCall struct {
	recipientID int64
	id          int64
}

// fakeRealtime 记录实时提示信号的发布（contracts.RealtimePublisher）。
type fakeRealtime struct {
	notifications []int64
	dmUsers       []int64
	dmConvIDs     []int64
}

func (f *fakeRealtime) PublishNotification(recipientID int64) {
	f.notifications = append(f.notifications, recipientID)
}

func (f *fakeRealtime) PublishDM(userID, conversationID int64) {
	f.dmUsers = append(f.dmUsers, userID)
	f.dmConvIDs = append(f.dmConvIDs, conversationID)
}

// fakeNotices 实现 app.NotificationRepo。
type fakeNotices struct {
	rows []model.Notification

	listErr        error
	unread         int64
	unreadErr      error
	markReadOK     bool
	markReadErr    error
	markAllUpdated int64
	markAllErr     error
	inserted       []model.Notification
	insertErr      error
	insertDeduped  bool

	listCalls     []noticeListCall
	unreadCalls   []int64
	markReadCalls []markReadCall
	markAllCalls  []int64
}

// Insert 记录通知写入；insertDeduped 预设幂等键冲突（返回未写入且不报错）。
func (f *fakeNotices) Insert(_ context.Context, row model.Notification) (bool, error) {
	if f.insertErr != nil {
		return false, f.insertErr
	}
	f.inserted = append(f.inserted, row)
	return !f.insertDeduped, nil
}

// List 记录列表参数并返回预设行。
func (f *fakeNotices) List(
	_ context.Context, recipientID int64, filter, noticeType string, cursor int64, limit int,
) ([]model.Notification, error) {
	f.listCalls = append(f.listCalls, noticeListCall{
		recipientID: recipientID, filter: filter, noticeType: noticeType, cursor: cursor, limit: limit,
	})
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.rows, nil
}

// UnreadCount 返回预设未读数。
func (f *fakeNotices) UnreadCount(_ context.Context, recipientID int64) (int64, error) {
	f.unreadCalls = append(f.unreadCalls, recipientID)
	return f.unread, f.unreadErr
}

// MarkRead 返回预设的「是否更新」结果。
func (f *fakeNotices) MarkRead(_ context.Context, recipientID, id int64) (bool, error) {
	f.markReadCalls = append(f.markReadCalls, markReadCall{recipientID: recipientID, id: id})
	return f.markReadOK, f.markReadErr
}

// MarkAllRead 返回预设的更新行数。
func (f *fakeNotices) MarkAllRead(_ context.Context, recipientID int64) (int64, error) {
	f.markAllCalls = append(f.markAllCalls, recipientID)
	return f.markAllUpdated, f.markAllErr
}

// deactivateExceptCall 记录 DeactivateExcept 的一次调用参数。
type deactivateExceptCall struct {
	sourceType  string
	sourceID    int64
	keepUserIDs []int64
}

// mentionUpsertCall 记录提及 Upsert 的一次调用参数。
type mentionUpsertCall struct {
	id         int64
	sourceType string
	sourceID   int64
	userID     int64
	now        time.Time
}

// mentionSourceCall 记录按来源对象操作的调用参数。
type mentionSourceCall struct {
	sourceType string
	sourceID   int64
}

// mentionListCall 记录生效提及列表的调用参数。
type mentionListCall struct {
	userID int64
	since  time.Time
	limit  int
}

// fakeMentions 实现 app.MentionRepo。
type fakeMentions struct {
	deactivateExceptCalls []deactivateExceptCall
	deactivateExceptErr   error
	upserts               []mentionUpsertCall
	upsertErr             error
	deactivateAllCalls    []mentionSourceCall
	deactivateAllErr      error
	activeRows            []domain.MentionRow
	listActiveErr         error
	listActiveCalls       []mentionListCall
	getActiveRow          domain.MentionRow
	getActiveErr          error
	getActiveCalls        []int64
}

// DeactivateExcept 记录需要保留的提及用户集合。
func (f *fakeMentions) DeactivateExcept(
	_ context.Context, sourceType string, sourceID int64, keepUserIDs []int64,
) error {
	f.deactivateExceptCalls = append(f.deactivateExceptCalls, deactivateExceptCall{
		sourceType: sourceType, sourceID: sourceID, keepUserIDs: slices.Clone(keepUserIDs),
	})
	return f.deactivateExceptErr
}

// Upsert 记录提及建链/恢复。
func (f *fakeMentions) Upsert(
	_ context.Context, id int64, sourceType string, sourceID, userID int64, now time.Time,
) error {
	f.upserts = append(f.upserts, mentionUpsertCall{
		id: id, sourceType: sourceType, sourceID: sourceID, userID: userID, now: now,
	})
	return f.upsertErr
}

// DeactivateAll 记录来源对象上全部提及的停用。
func (f *fakeMentions) DeactivateAll(_ context.Context, sourceType string, sourceID int64) error {
	f.deactivateAllCalls = append(f.deactivateAllCalls, mentionSourceCall{sourceType: sourceType, sourceID: sourceID})
	return f.deactivateAllErr
}

// ListActive 记录生效提及列表查询参数。
func (f *fakeMentions) ListActive(
	_ context.Context, userID int64, since time.Time, limit int,
) ([]domain.MentionRow, error) {
	f.listActiveCalls = append(f.listActiveCalls, mentionListCall{userID: userID, since: since, limit: limit})
	return f.activeRows, f.listActiveErr
}

// GetActive 记录单条生效提及查询。
func (f *fakeMentions) GetActive(_ context.Context, mentionID int64) (domain.MentionRow, error) {
	f.getActiveCalls = append(f.getActiveCalls, mentionID)
	return f.getActiveRow, f.getActiveErr
}

// ===== 夹具与通用断言 =====

// fixture 汇总一次用例的全部假端口，默认提供可执行成功路径的最小数据。
type fixture struct {
	clock    *clock.Fake
	ids      *fakeIDs
	users    *fakeUsers
	posts    *fakePosts
	rels     *fakeRelations
	comments *fakeComments
	notices  *fakeNotices
	mentions *fakeMentions
	realtime *fakeRealtime
	svc      *Service
}

// newFixture 组装注入了假端口与假时钟的 Service。
func newFixture() *fixture {
	f := &fixture{
		clock:    clock.NewFake(testNow),
		ids:      &fakeIDs{},
		users:    &fakeUsers{users: map[int64]model.User{}, briefs: map[int64]model.UserBrief{}},
		posts:    &fakePosts{posts: map[int64]model.Post{}, texts: map[int64]string{}},
		rels:     &fakeRelations{following: map[int64]bool{}},
		comments: &fakeComments{comments: map[int64]model.Comment{}},
		notices:  &fakeNotices{},
		mentions: &fakeMentions{},
		realtime: &fakeRealtime{},
	}
	f.svc = New(Deps{
		Clock:     f.clock,
		IDs:       f.ids,
		Users:     f.users,
		Posts:     f.posts,
		Relations: f.rels,
		Comments:  f.comments,
		Notices:   f.notices,
		Mentions:  f.mentions,
		Realtime:  f.realtime,
	})
	return f
}

// authedViewer 返回指定 ID 的已登录访问者。
func authedViewer(id int64) model.Viewer { return model.Viewer{ID: id, Authenticated: true} }

// activeHuman 返回正常状态的人类账号行。
func activeHuman(id int64) model.User {
	return model.User{ID: id, Username: "user" + strconv.FormatInt(id, 10), Status: model.StatusActive}
}

// assertInt64Set 断言两个 ID 切片作为集合相等（GetPosts 的入参来自 map，顺序不稳定）。
func assertInt64Set(t *testing.T, got, want []int64) {
	t.Helper()
	gotSorted := slices.Clone(got)
	wantSorted := slices.Clone(want)
	slices.Sort(gotSorted)
	slices.Sort(wantSorted)
	if !slices.Equal(gotSorted, wantSorted) {
		t.Fatalf("ID 集合 = %v，期望 %v", got, want)
	}
}

// requireKind 断言错误属于指定领域错误类别。
func requireKind(t *testing.T, err error, kind apperr.Kind) *apperr.Error {
	t.Helper()
	appErr, ok := apperr.AsKind(err)
	if !ok || appErr.Kind != kind {
		t.Fatalf("错误 = %v，期望类别 %s", err, kind)
	}
	return appErr
}

// ===== 构造与纯函数 =====

// TestNewFallsBackToSystemClock 覆盖 nil Clock 回退系统时钟。
func TestNewFallsBackToSystemClock(t *testing.T) {
	svc := New(Deps{})
	if svc.deps.Clock == nil {
		t.Fatal("nil Clock 必须回退系统时钟")
	}
	if svc.now().IsZero() {
		t.Fatal("回退系统时钟后不应返回零值时间")
	}
}

// TestNewKeepsInjectedClock 覆盖显式注入的时钟不被覆盖。
func TestNewKeepsInjectedClock(t *testing.T) {
	fake := clock.NewFake(testNow)
	svc := New(Deps{Clock: fake})
	if !svc.now().Equal(testNow) {
		t.Fatalf("now() = %v，期望假时钟时间 %v", svc.now(), testNow)
	}
}

// TestNewID 覆盖 ID 生成成功与被包装为 internal 的失败路径。
func TestNewID(t *testing.T) {
	sentinel := errors.New("ID 生成器故障")
	cases := []struct {
		name     string
		ids      *fakeIDs
		wantID   int64
		wantKind apperr.Kind
		wantErr  error
	}{
		{name: "成功返回生成的 ID", ids: &fakeIDs{ids: []int64{42}}, wantID: 42},
		{name: "生成器报错包装为 internal", ids: &fakeIDs{err: sentinel}, wantKind: apperr.KindInternal, wantErr: sentinel},
		{name: "零 ID 视为生成失败", ids: &fakeIDs{ids: []int64{0}}, wantKind: apperr.KindInternal},
		{name: "负数 ID 视为生成失败", ids: &fakeIDs{ids: []int64{-3}}, wantKind: apperr.KindInternal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := New(Deps{IDs: tc.ids})
			got, err := svc.newID()
			if tc.wantKind != "" {
				requireKind(t, err, tc.wantKind)
				if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
					t.Fatalf("错误应可用 errors.Is 穿透底层原因，实际 %v", err)
				}
				if got != 0 {
					t.Fatalf("失败时 ID = %d，期望 0", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("newID 返回错误: %v", err)
			}
			if got != tc.wantID {
				t.Fatalf("newID = %d，期望 %d", got, tc.wantID)
			}
		})
	}
}

// TestEventTime 覆盖 At 为零时回退时钟、非零时原样使用。
func TestEventTime(t *testing.T) {
	svc := New(Deps{Clock: clock.NewFake(testNow)})
	if got := svc.eventTime(time.Time{}); !got.Equal(testNow) {
		t.Fatalf("At 为零时应使用时钟时间 %v，实际 %v", testNow, got)
	}
	explicit := testNow.Add(90 * time.Minute)
	if got := svc.eventTime(explicit); !got.Equal(explicit) {
		t.Fatalf("At 非零时应原样返回 %v，实际 %v", explicit, got)
	}
}

// TestTrimPage 覆盖分页裁剪与 HasMore 判定。
func TestTrimPage(t *testing.T) {
	items := func(n int) []int {
		return make([]int, n)
	}
	cases := []struct {
		name     string
		items    []int
		limit    int
		wantLen  int
		wantMore bool
	}{
		{name: "未超过限制", items: items(2), limit: 2, wantLen: 2, wantMore: false},
		{name: "恰好等于限制", items: items(2), limit: 2, wantLen: 2, wantMore: false},
		{name: "超过限制时裁剪并标记还有更多", items: items(3), limit: 2, wantLen: 2, wantMore: true},
		{name: "空列表", items: nil, limit: 2, wantLen: 0, wantMore: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, hasMore := trimPage(tc.items, tc.limit)
			if len(got) != tc.wantLen || hasMore != tc.wantMore {
				t.Fatalf("trimPage(len=%d, limit=%d) = (%d, %v)，期望 (%d, %v)",
					len(tc.items), tc.limit, len(got), hasMore, tc.wantLen, tc.wantMore)
			}
		})
	}
}

// TestMentionKey 覆盖提及通知幂等键的稳定格式。
func TestMentionKey(t *testing.T) {
	cases := []struct {
		name       string
		sourceType string
		sourceID   int64
		userID     int64
		want       string
	}{
		{name: "帖子来源", sourceType: model.NoticeSourcePost, sourceID: 101, userID: 7, want: "mention:post:101:7"},
		{name: "评论来源", sourceType: model.NoticeSourceComment, sourceID: 300, userID: 8, want: "mention:comment:300:8"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mentionKey(tc.sourceType, tc.sourceID, tc.userID); got != tc.want {
				t.Fatalf("mentionKey = %q，期望 %q", got, tc.want)
			}
		})
	}
}

// TestTargetTypeForSource 覆盖提及来源到通知目标类型的映射。
func TestTargetTypeForSource(t *testing.T) {
	cases := []struct {
		name       string
		sourceType string
		want       string
	}{
		{name: "评论来源映射为评论目标", sourceType: model.NoticeSourceComment, want: model.NoticeTargetComment},
		{name: "帖子来源映射为帖子目标", sourceType: model.NoticeSourcePost, want: model.NoticeTargetPost},
		{name: "未知来源回退为帖子目标", sourceType: "unknown", want: model.NoticeTargetPost},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := targetTypeForSource(tc.sourceType); got != tc.want {
				t.Fatalf("targetTypeForSource(%q) = %q，期望 %q", tc.sourceType, got, tc.want)
			}
		})
	}
}

// ===== List =====

// TestListRequiresLogin 覆盖未登录访问通知列表。
func TestListRequiresLogin(t *testing.T) {
	f := newFixture()
	_, err := f.svc.List(context.Background(), model.GuestViewer(), "", "", 0, 20)
	requireKind(t, err, apperr.KindUnauthenticated)
	if len(f.notices.listCalls) != 0 {
		t.Fatalf("未登录时不应查询仓储，实际调用 %d 次", len(f.notices.listCalls))
	}
}

// TestListValidation 覆盖 filter/type 归一化失败与校验顺序。
func TestListValidation(t *testing.T) {
	cases := []struct {
		name       string
		viewer     model.Viewer
		filter     string
		noticeType string
		wantKind   apperr.Kind
	}{
		{name: "非法 filter", viewer: authedViewer(1), filter: "read", wantKind: apperr.KindInvalid},
		{name: "非法 type", viewer: authedViewer(1), noticeType: "bogus", wantKind: apperr.KindInvalid},
		{name: "未登录优先于参数校验", viewer: model.GuestViewer(), filter: "read", wantKind: apperr.KindUnauthenticated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			_, err := f.svc.List(context.Background(), tc.viewer, tc.filter, tc.noticeType, 0, 20)
			requireKind(t, err, tc.wantKind)
			if len(f.notices.listCalls) != 0 {
				t.Fatalf("参数校验失败后不应查询仓储，实际调用 %d 次", len(f.notices.listCalls))
			}
		})
	}
}

// TestListNormalizesAndPassesPageParams 覆盖 filter/type 归一化与分页参数透传。
func TestListNormalizesAndPassesPageParams(t *testing.T) {
	cases := []struct {
		name       string
		filter     string
		noticeType string
		wantFilter string
		wantType   string
	}{
		{name: "空 filter 归一化为 all", filter: "", noticeType: "", wantFilter: "all", wantType: ""},
		{name: "去空白并透传 unread/mention", filter: " unread ", noticeType: " mention ", wantFilter: "unread", wantType: "mention"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			viewer := authedViewer(9)
			page, err := f.svc.List(context.Background(), viewer, tc.filter, tc.noticeType, 55, 10)
			if err != nil {
				t.Fatalf("List 返回错误: %v", err)
			}
			if len(page.Items) != 0 || page.HasMore || page.NextCursor != 0 {
				t.Fatalf("空结果分页字段不符: %+v", page)
			}
			if len(f.notices.listCalls) != 1 {
				t.Fatalf("仓储调用次数 = %d，期望 1", len(f.notices.listCalls))
			}
			call := f.notices.listCalls[0]
			if call.recipientID != viewer.ID || call.filter != tc.wantFilter ||
				call.noticeType != tc.wantType || call.cursor != 55 || call.limit != 11 {
				t.Fatalf("仓储参数不符: %+v", call)
			}
		})
	}
}

// TestListPaginationAndActors 覆盖 limit+1 裁剪、HasMore/NextCursor 与 Actor 摘要。
func TestListPaginationAndActors(t *testing.T) {
	postA, postB := int64(101), int64(102)
	rows := []model.Notification{
		{
			ID: 300, RecipientID: 1, ActorID: 7, Type: model.NoticeTypePostLike,
			TargetType: model.NoticeTargetPost, TargetID: postA, PostID: &postA,
			CreatedAt: testNow,
		},
		{
			ID: 299, RecipientID: 1, ActorID: 8, Type: model.NoticeTypePostLike,
			TargetType: model.NoticeTargetPost, TargetID: postB, PostID: &postB,
			CreatedAt: testNow,
		},
		{
			ID: 298, RecipientID: 1, ActorID: 9, Type: model.NoticeTypeFollow,
			TargetType: model.NoticeTargetUser, TargetID: 42,
		},
	}

	t.Run("还有更多时裁剪并携带游标", func(t *testing.T) {
		f := newFixture()
		f.notices.rows = rows
		f.posts.posts = map[int64]model.Post{
			postA: {ID: postA, AuthorID: 7, Status: model.PostStatusPublished, Visibility: model.VisibilityPublic},
			postB: {ID: postB, AuthorID: 8, Status: model.PostStatusPublished, Visibility: model.VisibilityPublic},
		}
		f.posts.texts = map[int64]string{postA: "第一帖正文", postB: "第二帖正文"}
		f.users.briefs = map[int64]model.UserBrief{
			7: {ID: 7, Username: "actor7", Nickname: "演员七"},
			8: {ID: 8, Username: "actor8", Nickname: "演员八"},
		}

		page, err := f.svc.List(context.Background(), authedViewer(1), "", "", 0, 2)
		if err != nil {
			t.Fatalf("List 返回错误: %v", err)
		}
		if len(page.Items) != 2 || !page.HasMore {
			t.Fatalf("分页结果 = %d 条 / hasMore=%v，期望 2 条 / true", len(page.Items), page.HasMore)
		}
		if page.NextCursor != 299 {
			t.Fatalf("NextCursor = %d，期望 299（最后一条可见通知 ID）", page.NextCursor)
		}
		if page.Items[0].Actor.Username != "actor7" || page.Items[0].Actor.Nickname != "演员七" {
			t.Fatalf("首条 Actor 摘要不符: %+v", page.Items[0].Actor)
		}
		if page.Items[1].Actor.Username != "actor8" {
			t.Fatalf("次条 Actor 摘要不符: %+v", page.Items[1].Actor)
		}
		if page.Items[0].Preview == nil || *page.Items[0].Preview != "第一帖正文" {
			t.Fatalf("首条预览 = %v，期望第一帖正文", page.Items[0].Preview)
		}
		if page.Items[1].Preview == nil || *page.Items[1].Preview != "第二帖正文" {
			t.Fatalf("次条预览 = %v，期望第二帖正文", page.Items[1].Preview)
		}
		if len(f.users.briefCalls) != 1 || !slices.Equal(f.users.briefCalls[0], []int64{7, 8}) {
			t.Fatalf("Actor 摘要入参 = %v，期望仅包含本页两个作者 [7 8]", f.users.briefCalls)
		}
		// 本页通知目标全是公开帖子：关注集合不影响可见性，不应加载（懒加载）。
		if len(f.rels.calls) != 0 {
			t.Fatalf("公开帖子预览不应加载关注集合，实际调用 %d 次", len(f.rels.calls))
		}
	})

	t.Run("末页无更多", func(t *testing.T) {
		f := newFixture()
		f.notices.rows = rows[:2]
		f.posts.posts = map[int64]model.Post{
			postA: {ID: postA, AuthorID: 7, Status: model.PostStatusPublished, Visibility: model.VisibilityPublic},
			postB: {ID: postB, AuthorID: 8, Status: model.PostStatusPublished, Visibility: model.VisibilityPublic},
		}
		f.posts.texts = map[int64]string{postA: "第一帖正文", postB: "第二帖正文"}

		page, err := f.svc.List(context.Background(), authedViewer(1), "", "", 0, 2)
		if err != nil {
			t.Fatalf("List 返回错误: %v", err)
		}
		if len(page.Items) != 2 || page.HasMore {
			t.Fatalf("分页结果 = %d 条 / hasMore=%v，期望 2 条 / false", len(page.Items), page.HasMore)
		}
		if page.NextCursor != 299 {
			t.Fatalf("NextCursor = %d，期望 299", page.NextCursor)
		}
	})

	t.Run("空结果", func(t *testing.T) {
		f := newFixture()
		page, err := f.svc.List(context.Background(), authedViewer(1), "", "", 0, 20)
		if err != nil {
			t.Fatalf("List 返回错误: %v", err)
		}
		if page.Items == nil || len(page.Items) != 0 {
			t.Fatalf("空结果 Items = %v，期望非 nil 空切片", page.Items)
		}
		if page.HasMore || page.NextCursor != 0 {
			t.Fatalf("空结果分页字段不符: %+v", page)
		}
		if len(f.users.briefCalls) != 1 || len(f.users.briefCalls[0]) != 0 {
			t.Fatalf("空结果仍应查询作者摘要（入参为空），实际 %v", f.users.briefCalls)
		}
	})
}

// TestListPortErrors 覆盖列表链路中每个端口的错误透传。
func TestListPortErrors(t *testing.T) {
	sentinel := errors.New("端口故障")
	postID := int64(101)
	commentID := int64(201)
	followersPostID := int64(102)
	postRow := model.Notification{
		ID: 1, ActorID: 7, Type: model.NoticeTypePostLike,
		TargetType: model.NoticeTargetPost, TargetID: postID, PostID: &postID,
	}
	commentRow := model.Notification{
		ID: 2, ActorID: 7, Type: model.NoticeTypeComment,
		TargetType: model.NoticeTargetComment, TargetID: commentID, PostID: &postID, CommentID: &commentID,
	}
	followersPostRow := model.Notification{
		ID: 3, ActorID: 7, Type: model.NoticeTypePostLike,
		TargetType: model.NoticeTargetPost, TargetID: followersPostID, PostID: &followersPostID,
	}
	followRow := model.Notification{
		ID: 4, ActorID: 7, Type: model.NoticeTypeFollow,
		TargetType: model.NoticeTargetUser, TargetID: 42,
	}
	cases := []struct {
		name  string
		rows  []model.Notification
		setup func(*fixture)
	}{
		{
			name: "通知仓储读取失败",
			rows: nil,
			setup: func(f *fixture) {
				f.notices.listErr = sentinel
			},
		},
		{
			name: "作者摘要读取失败",
			rows: []model.Notification{followRow},
			setup: func(f *fixture) {
				f.users.briefsErr = sentinel
			},
		},
		{
			name: "评论读取失败",
			rows: []model.Notification{commentRow},
			setup: func(f *fixture) {
				f.comments.err = sentinel
			},
		},
		{
			name: "帖子读取失败",
			rows: []model.Notification{postRow},
			setup: func(f *fixture) {
				f.posts.getErr = sentinel
			},
		},
		{
			name: "帖子纯文本读取失败",
			rows: []model.Notification{postRow},
			setup: func(f *fixture) {
				f.posts.textErr = sentinel
			},
		},
		{
			name: "关注集合读取失败（仅关注者可见帖子才需要关注集合）",
			rows: []model.Notification{followersPostRow},
			setup: func(f *fixture) {
				f.posts.posts = map[int64]model.Post{
					followersPostID: {
						ID: followersPostID, AuthorID: 42,
						Status: model.PostStatusPublished, Visibility: model.VisibilityFollowers,
					},
				}
				f.rels.err = sentinel
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			f.notices.rows = tc.rows
			tc.setup(f)
			_, err := f.svc.List(context.Background(), authedViewer(1), "", "", 0, 20)
			if !errors.Is(err, sentinel) {
				t.Fatalf("错误 = %v，期望透传 %v", err, sentinel)
			}
		})
	}
}

// TestListPreviewsShortCircuit 验证纯关注类通知（无帖子/评论目标）直接短路：
// 不查询评论、帖子、纯文本，也不加载关注集合。
func TestListPreviewsShortCircuit(t *testing.T) {
	f := newFixture()
	f.notices.rows = []model.Notification{
		{ID: 3, ActorID: 7, Type: model.NoticeTypeFollow, TargetType: model.NoticeTargetUser, TargetID: 42},
		{ID: 2, ActorID: 8, Type: model.NoticeTypeFollowRequest, TargetType: model.NoticeTargetUser, TargetID: 43},
	}
	page, err := f.svc.List(context.Background(), authedViewer(1), "", "", 0, 20)
	if err != nil {
		t.Fatalf("List 返回错误: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("通知条数 = %d，期望 2", len(page.Items))
	}
	for i, item := range page.Items {
		if item.Preview != nil {
			t.Fatalf("第 %d 条关注类通知预览 = %q，期望 nil", i+1, *item.Preview)
		}
	}
	if len(f.comments.calls) != 0 || len(f.posts.getCalls) != 0 || len(f.posts.textCalls) != 0 || len(f.rels.calls) != 0 {
		t.Fatalf("纯关注通知应短路全部预览查询，实际 comments=%d posts=%d texts=%d following=%d",
			len(f.comments.calls), len(f.posts.getCalls), len(f.posts.textCalls), len(f.rels.calls))
	}
}

// TestListPreviewsLazyFollowing 验证仅当存在「已发布 + 仅关注者可见 + 非本人」帖子时
// 才加载关注集合；关注者可见帖子按关注集合返回正文。
func TestListPreviewsLazyFollowing(t *testing.T) {
	followersPostID := int64(102)
	f := newFixture()
	f.notices.rows = []model.Notification{
		{ID: 2, ActorID: 7, PostID: int64Ptr(followersPostID)},
	}
	f.posts.posts = map[int64]model.Post{
		followersPostID: {
			ID: followersPostID, AuthorID: 42,
			Status: model.PostStatusPublished, Visibility: model.VisibilityFollowers,
		},
	}
	f.posts.texts = map[int64]string{followersPostID: "仅关注者可见正文"}
	f.rels.following = map[int64]bool{42: true}

	page, err := f.svc.List(context.Background(), authedViewer(1), "", "", 0, 20)
	if err != nil {
		t.Fatalf("List 返回错误: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Preview == nil || *page.Items[0].Preview != "仅关注者可见正文" {
		t.Fatalf("预览 = %+v，期望关注者可见正文", page.Items)
	}
	if len(f.rels.calls) != 1 || f.rels.calls[0] != 1 {
		t.Fatalf("关注集合入参 = %v，期望访问者 1 且只加载一次", f.rels.calls)
	}
}

// TestListPreviewsByPermission 覆盖各通知类型预览的权限判定：
// 公开/已关注可见帖子与未删除评论给出正文，非公开/已删除/缺失/关注类通知为 nil。
func TestListPreviewsByPermission(t *testing.T) {
	publicPost := func(id int64) model.Post {
		return model.Post{ID: id, AuthorID: 50, Status: model.PostStatusPublished, Visibility: model.VisibilityPublic}
	}
	longComment := strings.Repeat("长", domain.PreviewRunes+5)
	longWant := string([]rune(longComment)[:domain.PreviewRunes]) + "…"

	cases := []struct {
		name string
		row  model.Notification
		want *string
	}{
		{
			name: "公开帖可见",
			row:  model.Notification{ID: 914, ActorID: 7, PostID: int64Ptr(101)},
			want: stringPtr("公开帖子正文"),
		},
		{
			name: "仅关注者可见且访问者已关注",
			row:  model.Notification{ID: 913, ActorID: 7, PostID: int64Ptr(102)},
			want: stringPtr("仅关注者可见正文"),
		},
		{
			name: "仅关注者可见但访问者未关注",
			row:  model.Notification{ID: 912, ActorID: 7, PostID: int64Ptr(103)},
			want: nil,
		},
		{
			name: "已删除帖不可见",
			row:  model.Notification{ID: 911, ActorID: 7, PostID: int64Ptr(104)},
			want: nil,
		},
		{
			name: "他人草稿不可见",
			row:  model.Notification{ID: 910, ActorID: 7, PostID: int64Ptr(105)},
			want: nil,
		},
		{
			name: "评论未删除且所在帖子可见",
			row: model.Notification{
				ID: 909, ActorID: 7, PostID: int64Ptr(106), CommentID: int64Ptr(201),
			},
			want: stringPtr("评论正文"),
		},
		{
			name: "评论已删除",
			row: model.Notification{
				ID: 908, ActorID: 7, PostID: int64Ptr(106), CommentID: int64Ptr(202),
			},
			want: nil,
		},
		{
			name: "评论所在帖子不可见",
			row: model.Notification{
				ID: 907, ActorID: 7, PostID: int64Ptr(107), CommentID: int64Ptr(203),
			},
			want: nil,
		},
		{
			name: "评论不存在",
			row: model.Notification{
				ID: 906, ActorID: 7, PostID: int64Ptr(106), CommentID: int64Ptr(999),
			},
			want: nil,
		},
		{
			name: "关注类通知无正文预览",
			row:  model.Notification{ID: 905, ActorID: 7, Type: model.NoticeTypeFollow, TargetID: 42},
			want: nil,
		},
		{
			name: "PostID 为 0 不视为帖子预览",
			row:  model.Notification{ID: 904, ActorID: 7, PostID: int64Ptr(0)},
			want: nil,
		},
		{
			name: "超长评论按统一规则截断",
			row: model.Notification{
				ID: 903, ActorID: 7, PostID: int64Ptr(106), CommentID: int64Ptr(204),
			},
			want: stringPtr(longWant),
		},
		{
			name: "作者本人的私密帖可见",
			row:  model.Notification{ID: 902, ActorID: 7, PostID: int64Ptr(109)},
			want: stringPtr("自己的私密正文"),
		},
		{
			name: "帖子缺少纯文本投影",
			row:  model.Notification{ID: 901, ActorID: 7, PostID: int64Ptr(108)},
			want: nil,
		},
	}

	f := newFixture()
	f.rels.following = map[int64]bool{42: true}
	f.users.briefs = map[int64]model.UserBrief{7: {ID: 7, Username: "actor7"}}
	f.posts.posts = map[int64]model.Post{
		101: publicPost(101),
		102: {ID: 102, AuthorID: 42, Status: model.PostStatusPublished, Visibility: model.VisibilityFollowers},
		103: {ID: 103, AuthorID: 43, Status: model.PostStatusPublished, Visibility: model.VisibilityFollowers},
		104: {ID: 104, AuthorID: 50, Status: model.PostStatusDeleted, Visibility: model.VisibilityPublic},
		105: {ID: 105, AuthorID: 50, Status: model.PostStatusDraft, Visibility: model.VisibilityPublic},
		106: publicPost(106),
		107: {ID: 107, AuthorID: 50, Status: model.PostStatusPublished, Visibility: model.VisibilityPrivate},
		108: publicPost(108),
		109: {ID: 109, AuthorID: 1, Status: model.PostStatusPublished, Visibility: model.VisibilityPrivate},
	}
	f.posts.texts = map[int64]string{
		101: "公开帖子正文",
		102: "仅关注者可见正文",
		106: "评论所在帖子正文",
		109: "自己的私密正文",
	}
	f.comments.comments = map[int64]model.Comment{
		201: {ID: 201, PostID: 106, PlainText: "评论正文", Status: model.CommentStatusPublished},
		202: {ID: 202, PostID: 106, PlainText: "已删除评论", Status: model.CommentStatusDeleted},
		203: {ID: 203, PostID: 107, PlainText: "私密帖评论", Status: model.CommentStatusPublished},
		204: {ID: 204, PostID: 106, PlainText: longComment, Status: model.CommentStatusPublished},
	}
	rows := make([]model.Notification, 0, len(cases))
	wants := make([]*string, 0, len(cases))
	for _, tc := range cases {
		rows = append(rows, tc.row)
		wants = append(wants, tc.want)
	}
	f.notices.rows = rows

	page, err := f.svc.List(context.Background(), authedViewer(1), "", "", 0, 20)
	if err != nil {
		t.Fatalf("List 返回错误: %v", err)
	}
	if len(page.Items) != len(cases) {
		t.Fatalf("通知条数 = %d，期望 %d", len(page.Items), len(cases))
	}
	if page.HasMore {
		t.Fatal("未超过 limit 时不应标记还有更多")
	}
	for i, tc := range cases {
		got := page.Items[i].Preview
		switch {
		case tc.want == nil && got != nil:
			t.Errorf("%s：预览 = %q，期望 nil", tc.name, *got)
		case tc.want != nil && got == nil:
			t.Errorf("%s：预览 = nil，期望 %q", tc.name, *tc.want)
		case tc.want != nil && got != nil && *got != *tc.want:
			t.Errorf("%s：预览 = %q，期望 %q", tc.name, *got, *tc.want)
		}
	}

	if len(f.comments.calls) != 1 {
		t.Fatalf("评论读取调用次数 = %d，期望 1", len(f.comments.calls))
	}
	wantCommentIDs := []int64{201, 202, 203, 999, 204}
	if !slices.Equal(f.comments.calls[0], wantCommentIDs) {
		t.Fatalf("评论读取入参 = %v，期望 %v", f.comments.calls[0], wantCommentIDs)
	}
	if len(f.posts.getCalls) != 1 {
		t.Fatalf("帖子读取调用次数 = %d，期望 1", len(f.posts.getCalls))
	}
	wantPostIDs := []int64{101, 102, 103, 104, 105, 106, 107, 108, 109}
	assertInt64Set(t, f.posts.getCalls[0], wantPostIDs)
	if !slices.Equal(f.posts.textCalls[0], f.posts.getCalls[0]) {
		t.Fatalf("纯文本读取入参与帖子读取不一致: %v vs %v", f.posts.textCalls[0], f.posts.getCalls[0])
	}
}

// ===== UnreadCount / MarkRead / MarkAllRead =====

// TestUnreadCount 覆盖未读数的鉴权、错误透传与成功路径。
func TestUnreadCount(t *testing.T) {
	sentinel := errors.New("统计失败")
	cases := []struct {
		name      string
		viewer    model.Viewer
		count     int64
		repoErr   error
		wantCount int64
		wantKind  apperr.Kind
		wantErr   error
	}{
		{name: "未登录返回 unauthenticated", viewer: model.GuestViewer(), wantKind: apperr.KindUnauthenticated},
		{name: "仓储错误透传", viewer: authedViewer(9), repoErr: sentinel, wantErr: sentinel},
		{name: "成功返回未读数", viewer: authedViewer(9), count: 5, wantCount: 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			f.notices.unread = tc.count
			f.notices.unreadErr = tc.repoErr
			got, err := f.svc.UnreadCount(context.Background(), tc.viewer)
			if tc.wantKind != "" {
				requireKind(t, err, tc.wantKind)
				return
			}
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("错误 = %v，期望透传 %v", err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.wantCount {
				t.Fatalf("UnreadCount = %d/%v，期望 %d/nil", got, err, tc.wantCount)
			}
			if len(f.notices.unreadCalls) != 1 || f.notices.unreadCalls[0] != tc.viewer.ID {
				t.Fatalf("仓储入参 = %v，期望访问者 ID %d", f.notices.unreadCalls, tc.viewer.ID)
			}
		})
	}
}

// TestMarkRead 覆盖单条已读的鉴权、not_found 与参数透传。
func TestMarkRead(t *testing.T) {
	sentinel := errors.New("已读失败")
	cases := []struct {
		name     string
		viewer   model.Viewer
		ok       bool
		repoErr  error
		wantKind apperr.Kind
		wantErr  error
	}{
		{name: "未登录返回 unauthenticated", viewer: model.GuestViewer(), wantKind: apperr.KindUnauthenticated},
		{name: "仓储错误透传", viewer: authedViewer(9), repoErr: sentinel, wantErr: sentinel},
		{name: "通知不存在返回 not_found", viewer: authedViewer(9), ok: false, wantKind: apperr.KindNotFound},
		{name: "标记成功", viewer: authedViewer(9), ok: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			f.notices.markReadOK = tc.ok
			f.notices.markReadErr = tc.repoErr
			err := f.svc.MarkRead(context.Background(), tc.viewer, 42)
			if tc.wantKind != "" {
				appErr := requireKind(t, err, tc.wantKind)
				if tc.wantKind == apperr.KindNotFound && appErr.Message != "通知不存在" {
					t.Fatalf("not_found 文案 = %q，期望 通知不存在", appErr.Message)
				}
				return
			}
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("错误 = %v，期望透传 %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("MarkRead 返回错误: %v", err)
			}
			if len(f.notices.markReadCalls) != 1 {
				t.Fatalf("仓储调用次数 = %d，期望 1", len(f.notices.markReadCalls))
			}
			call := f.notices.markReadCalls[0]
			if call.recipientID != tc.viewer.ID || call.id != 42 {
				t.Fatalf("仓储入参 = %+v，期望 recipient=%d id=42", call, tc.viewer.ID)
			}
		})
	}
}

// TestMarkAllRead 覆盖全部已读的鉴权、更新行数透传与错误路径。
func TestMarkAllRead(t *testing.T) {
	sentinel := errors.New("全部已读失败")
	cases := []struct {
		name      string
		viewer    model.Viewer
		updated   int64
		repoErr   error
		wantCount int64
		wantKind  apperr.Kind
		wantErr   error
	}{
		{name: "未登录返回 unauthenticated", viewer: model.GuestViewer(), wantKind: apperr.KindUnauthenticated},
		{name: "仓储错误透传", viewer: authedViewer(9), repoErr: sentinel, wantErr: sentinel},
		{name: "成功返回更新行数", viewer: authedViewer(9), updated: 7, wantCount: 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			f.notices.markAllUpdated = tc.updated
			f.notices.markAllErr = tc.repoErr
			got, err := f.svc.MarkAllRead(context.Background(), tc.viewer)
			if tc.wantKind != "" {
				requireKind(t, err, tc.wantKind)
				return
			}
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("错误 = %v，期望透传 %v", err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.wantCount {
				t.Fatalf("MarkAllRead = %d/%v，期望 %d/nil", got, err, tc.wantCount)
			}
			if len(f.notices.markAllCalls) != 1 || f.notices.markAllCalls[0] != tc.viewer.ID {
				t.Fatalf("仓储入参 = %v，期望访问者 ID %d", f.notices.markAllCalls, tc.viewer.ID)
			}
		})
	}
}

// ===== CreateNotification =====

// validNotificationInput 返回可成功写入的通知输入。
func validNotificationInput() contracts.NotificationInput {
	return contracts.NotificationInput{
		IdempotencyKey: "follow:1:2",
		RecipientID:    9,
		ActorID:        7,
		Type:           model.NoticeTypeFollow,
		TargetType:     model.NoticeTargetUser,
		TargetID:       7,
	}
}

// TestCreateNotificationSkips 覆盖静默跳过的全部条件（不生成 ID、不写仓储）。
func TestCreateNotificationSkips(t *testing.T) {
	base := func() contracts.NotificationInput { return validNotificationInput() }
	cases := []struct {
		name   string
		mutate func(*contracts.NotificationInput)
		setup  func(*fixture)
	}{
		{
			name:   "接收者为 0",
			mutate: func(in *contracts.NotificationInput) { in.RecipientID = 0 },
		},
		{
			name:   "动作者为 0",
			mutate: func(in *contracts.NotificationInput) { in.ActorID = 0 },
		},
		{
			name:   "接收者就是动作者",
			mutate: func(in *contracts.NotificationInput) { in.RecipientID = in.ActorID },
		},
		{
			name:   "幂等键为空",
			mutate: func(in *contracts.NotificationInput) { in.IdempotencyKey = "" },
		},
		{
			name: "接收者不存在",
			setup: func(f *fixture) {
				f.users.getUserErr = apperr.NotFound("用户不存在")
			},
		},
		{
			name: "接收者已停用",
			setup: func(f *fixture) {
				user := activeHuman(9)
				user.Status = model.StatusSuspended
				f.users.users[9] = user
			},
		},
		{
			name: "接收者是 AI",
			setup: func(f *fixture) {
				user := activeHuman(9)
				user.IsAI = true
				f.users.users[9] = user
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			f.ids.ids = []int64{123}
			if tc.setup != nil {
				tc.setup(f)
			}
			in := base()
			if tc.mutate != nil {
				tc.mutate(&in)
			}
			if err := f.svc.CreateNotification(context.Background(), in); err != nil {
				t.Fatalf("静默跳过场景不应返回错误: %v", err)
			}
			if len(f.notices.inserted) != 0 {
				t.Fatalf("静默跳过场景不应写仓储，实际写入 %d 条", len(f.notices.inserted))
			}
			if f.ids.next != 0 {
				t.Fatalf("静默跳过场景不应消耗 ID，实际消耗 %d 个", f.ids.next)
			}
		})
	}
}

// TestCreateNotificationErrors 覆盖账号读取、ID 生成与仓储写入的错误传播。
func TestCreateNotificationErrors(t *testing.T) {
	sentinel := errors.New("端口故障")
	cases := []struct {
		name     string
		setup    func(*fixture)
		wantKind apperr.Kind
		wantErr  error
	}{
		{
			name: "账号读取失败原样返回",
			setup: func(f *fixture) {
				f.users.getUserErr = sentinel
			},
			wantErr: sentinel,
		},
		{
			name: "ID 生成失败包装为 internal",
			setup: func(f *fixture) {
				f.users.users[9] = activeHuman(9)
				f.ids.err = sentinel
			},
			wantKind: apperr.KindInternal,
			wantErr:  sentinel,
		},
		{
			name: "通知写入失败原样返回",
			setup: func(f *fixture) {
				f.users.users[9] = activeHuman(9)
				f.ids.ids = []int64{555}
				f.notices.insertErr = sentinel
			},
			wantErr: sentinel,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			tc.setup(f)
			err := f.svc.CreateNotification(context.Background(), validNotificationInput())
			if tc.wantKind != "" {
				requireKind(t, err, tc.wantKind)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("错误 = %v，期望可用 errors.Is 匹配 %v", err, tc.wantErr)
			}
			if len(f.notices.inserted) != 0 {
				t.Fatalf("失败路径不应留下成功写入，实际 %d 条", len(f.notices.inserted))
			}
		})
	}
}

// TestCreateNotificationSuccess 覆盖字段映射与 At 为零时回退时钟。
func TestCreateNotificationSuccess(t *testing.T) {
	explicitAt := testNow.Add(30 * time.Minute)
	postID, commentID := int64(101), int64(201)
	cases := []struct {
		name   string
		at     time.Time
		wantAt time.Time
	}{
		{name: "使用调用方传入时间", at: explicitAt, wantAt: explicitAt},
		{name: "At 为零时回退时钟", at: time.Time{}, wantAt: testNow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			f.users.users[9] = activeHuman(9)
			f.ids.ids = []int64{555}
			in := validNotificationInput()
			in.Type = model.NoticeTypeComment
			in.TargetType = model.NoticeTargetComment
			in.TargetID = commentID
			in.PostID = &postID
			in.CommentID = &commentID
			in.At = tc.at

			if err := f.svc.CreateNotification(context.Background(), in); err != nil {
				t.Fatalf("CreateNotification 返回错误: %v", err)
			}
			if len(f.notices.inserted) != 1 {
				t.Fatalf("写入条数 = %d，期望 1", len(f.notices.inserted))
			}
			row := f.notices.inserted[0]
			if row.ID != 555 {
				t.Fatalf("通知 ID = %d，期望生成 ID 555", row.ID)
			}
			if row.RecipientID != 9 || row.ActorID != 7 {
				t.Fatalf("接收者/动作者 = %d/%d，期望 9/7", row.RecipientID, row.ActorID)
			}
			if row.Type != model.NoticeTypeComment || row.TargetType != model.NoticeTargetComment ||
				row.TargetID != commentID {
				t.Fatalf("类型/目标字段不符: %+v", row)
			}
			if row.PostID == nil || *row.PostID != postID {
				t.Fatalf("PostID = %v，期望 %d", row.PostID, postID)
			}
			if row.CommentID == nil || *row.CommentID != commentID {
				t.Fatalf("CommentID = %v，期望 %d", row.CommentID, commentID)
			}
			if row.IsRead {
				t.Fatal("新通知必须是未读状态")
			}
			if row.IdempotencyKey != in.IdempotencyKey {
				t.Fatalf("幂等键 = %q，期望 %q", row.IdempotencyKey, in.IdempotencyKey)
			}
			if !row.CreatedAt.Equal(tc.wantAt) {
				t.Fatalf("CreatedAt = %v，期望 %v", row.CreatedAt, tc.wantAt)
			}
		})
	}
}

// TestCreateNotificationRealtimeSignal 覆盖实时提示信号：
// 实际写入才发布（事务外立即执行）、幂等跳过与写入失败不发布、nil 端口安全。
// 事务内注册、提交后发布的时序由 platform/db 的 AfterCommit 单测与集成测试覆盖。
func TestCreateNotificationRealtimeSignal(t *testing.T) {
	t.Run("写入成功发布给接收者", func(t *testing.T) {
		f := newFixture()
		f.users.users[9] = activeHuman(9)
		f.ids.ids = []int64{555}
		if err := f.svc.CreateNotification(context.Background(), validNotificationInput()); err != nil {
			t.Fatalf("CreateNotification 返回错误: %v", err)
		}
		if len(f.realtime.notifications) != 1 || f.realtime.notifications[0] != 9 {
			t.Fatalf("通知信号 = %v，期望 [9]", f.realtime.notifications)
		}
		if len(f.realtime.dmUsers) != 0 {
			t.Fatalf("通知路径不应发布私信信号: %v", f.realtime.dmUsers)
		}
	})
	t.Run("幂等跳过不发布", func(t *testing.T) {
		f := newFixture()
		f.users.users[9] = activeHuman(9)
		f.ids.ids = []int64{555}
		f.notices.insertDeduped = true
		if err := f.svc.CreateNotification(context.Background(), validNotificationInput()); err != nil {
			t.Fatalf("幂等跳过不应返回错误: %v", err)
		}
		if len(f.realtime.notifications) != 0 {
			t.Fatalf("幂等跳过不应发布信号: %v", f.realtime.notifications)
		}
	})
	t.Run("写入失败不发布", func(t *testing.T) {
		f := newFixture()
		f.users.users[9] = activeHuman(9)
		f.ids.ids = []int64{555}
		f.notices.insertErr = errors.New("端口故障")
		if err := f.svc.CreateNotification(context.Background(), validNotificationInput()); err == nil {
			t.Fatal("写入失败应返回错误")
		}
		if len(f.realtime.notifications) != 0 {
			t.Fatalf("写入失败不应发布信号: %v", f.realtime.notifications)
		}
	})
	t.Run("nil 端口安全", func(t *testing.T) {
		f := newFixture()
		f.users.users[9] = activeHuman(9)
		f.ids.ids = []int64{555}
		f.svc.deps.Realtime = nil
		if err := f.svc.CreateNotification(context.Background(), validNotificationInput()); err != nil {
			t.Fatalf("nil Realtime 端口不应报错: %v", err)
		}
	})
}

// ===== 提及写入 =====

// TestRebuildMentions 覆盖提及重建的解析、保留集合与 Upsert 参数。
func TestRebuildMentions(t *testing.T) {
	explicitAt := testNow.Add(10 * time.Minute)
	cases := []struct {
		name         string
		mentionNames []string
		findBriefs   []model.UserBrief
		at           time.Time
		wantKeep     []int64
		wantUpserts  []mentionUpsertCall
	}{
		{
			name:         "按用户名解析并逐个建链",
			mentionNames: []string{"alice", "bob"},
			findBriefs:   []model.UserBrief{{ID: 21}, {ID: 22}},
			at:           explicitAt,
			wantKeep:     []int64{21, 22},
			wantUpserts: []mentionUpsertCall{
				{id: 1001, sourceType: model.NoticeSourceComment, sourceID: 300, userID: 21, now: explicitAt},
				{id: 1002, sourceType: model.NoticeSourceComment, sourceID: 300, userID: 22, now: explicitAt},
			},
		},
		{
			name:         "At 为零时使用时钟时间",
			mentionNames: []string{"alice"},
			findBriefs:   []model.UserBrief{{ID: 21}},
			at:           time.Time{},
			wantKeep:     []int64{21},
			wantUpserts: []mentionUpsertCall{
				{id: 1001, sourceType: model.NoticeSourceComment, sourceID: 300, userID: 21, now: testNow},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			f.users.findBriefs = tc.findBriefs
			f.ids.ids = []int64{1001, 1002}
			in := contracts.RebuildMentionsInput{
				SourceType:   model.NoticeSourceComment,
				SourceID:     300,
				PostID:       101,
				MentionNames: tc.mentionNames,
				At:           tc.at,
			}
			if err := f.svc.RebuildMentions(context.Background(), in); err != nil {
				t.Fatalf("RebuildMentions 返回错误: %v", err)
			}
			if len(f.mentions.deactivateExceptCalls) != 1 {
				t.Fatalf("DeactivateExcept 调用次数 = %d，期望 1", len(f.mentions.deactivateExceptCalls))
			}
			deactivate := f.mentions.deactivateExceptCalls[0]
			if deactivate.sourceType != in.SourceType || deactivate.sourceID != in.SourceID ||
				!slices.Equal(deactivate.keepUserIDs, tc.wantKeep) {
				t.Fatalf("DeactivateExcept 入参不符: %+v，期望 keep=%v", deactivate, tc.wantKeep)
			}
			if len(f.mentions.upserts) != len(tc.wantUpserts) {
				t.Fatalf("Upsert 次数 = %d，期望 %d", len(f.mentions.upserts), len(tc.wantUpserts))
			}
			for i, want := range tc.wantUpserts {
				got := f.mentions.upserts[i]
				if got.id != want.id || got.sourceType != want.sourceType || got.sourceID != want.sourceID ||
					got.userID != want.userID || !got.now.Equal(want.now) {
					t.Fatalf("第 %d 次 Upsert = %+v，期望 %+v", i+1, got, want)
				}
			}
		})
	}
}

// TestRebuildMentionsEmptyNames 覆盖空提及名时只停用旧提及。
func TestRebuildMentionsEmptyNames(t *testing.T) {
	f := newFixture()
	in := contracts.RebuildMentionsInput{
		SourceType:   model.NoticeSourcePost,
		SourceID:     101,
		MentionNames: nil,
	}
	if err := f.svc.RebuildMentions(context.Background(), in); err != nil {
		t.Fatalf("RebuildMentions 返回错误: %v", err)
	}
	if len(f.users.findCalls) != 0 {
		t.Fatalf("空提及名不应查询账号，实际调用 %d 次", len(f.users.findCalls))
	}
	if len(f.mentions.upserts) != 0 {
		t.Fatalf("空提及名不应 Upsert，实际 %d 次", len(f.mentions.upserts))
	}
	if len(f.mentions.deactivateExceptCalls) != 1 {
		t.Fatalf("DeactivateExcept 调用次数 = %d，期望 1", len(f.mentions.deactivateExceptCalls))
	}
	if keep := f.mentions.deactivateExceptCalls[0].keepUserIDs; keep != nil {
		t.Fatalf("空提及名的保留集合 = %v，期望 nil", keep)
	}
}

// TestRebuildMentionsErrors 覆盖解析、停用、ID 生成与 Upsert 的错误路径。
func TestRebuildMentionsErrors(t *testing.T) {
	findErr := errors.New("账号解析失败")
	deactivateErr := errors.New("停用旧提及失败")
	upsertErr := errors.New("提及建链失败")
	idErr := errors.New("ID 生成失败")
	cases := []struct {
		name       string
		setup      func(*fixture)
		wantKind   apperr.Kind
		wantErr    error
		wantDeact  bool
		wantUpsert int
	}{
		{
			name: "用户名解析失败",
			setup: func(f *fixture) {
				f.users.findErr = findErr
			},
			wantErr: findErr,
		},
		{
			name: "停用旧提及失败",
			setup: func(f *fixture) {
				f.users.findBriefs = []model.UserBrief{{ID: 21}}
				f.mentions.deactivateExceptErr = deactivateErr
			},
			wantErr:   deactivateErr,
			wantDeact: true,
		},
		{
			name: "解析结果为空时只停用不建链",
			setup: func(f *fixture) {
				f.users.findBriefs = nil
			},
			wantDeact: true,
		},
		{
			name: "ID 生成失败",
			setup: func(f *fixture) {
				f.users.findBriefs = []model.UserBrief{{ID: 21}}
				f.ids.err = idErr
			},
			wantKind:  apperr.KindInternal,
			wantErr:   idErr,
			wantDeact: true,
		},
		{
			name: "Upsert 失败",
			setup: func(f *fixture) {
				f.users.findBriefs = []model.UserBrief{{ID: 21}}
				f.ids.ids = []int64{1001}
				f.mentions.upsertErr = upsertErr
			},
			wantErr:    upsertErr,
			wantDeact:  true,
			wantUpsert: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			tc.setup(f)
			in := contracts.RebuildMentionsInput{
				SourceType:   model.NoticeSourcePost,
				SourceID:     101,
				MentionNames: []string{"alice", "bob"},
				At:           testNow,
			}
			err := f.svc.RebuildMentions(context.Background(), in)
			if tc.wantKind != "" {
				requireKind(t, err, tc.wantKind)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("错误 = %v，期望可用 errors.Is 匹配 %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && err != nil {
				t.Fatalf("不应返回错误: %v", err)
			}
			if got := len(f.mentions.deactivateExceptCalls) > 0; got != tc.wantDeact {
				t.Fatalf("DeactivateExcept 是否调用 = %v，期望 %v", got, tc.wantDeact)
			}
			if len(f.mentions.upserts) != tc.wantUpsert {
				t.Fatalf("Upsert 调用次数 = %d，期望 %d", len(f.mentions.upserts), tc.wantUpsert)
			}
		})
	}
}

// TestMentionNotifications 覆盖帖子/评论来源的通知字段与排除自己。
func TestMentionNotifications(t *testing.T) {
	explicitAt := testNow.Add(5 * time.Minute)
	cases := []struct {
		name        string
		sourceType  string
		sourceID    int64
		postID      int64
		authorID    int64
		wantTarget  string
		wantComment *int64
	}{
		{
			name:        "帖子来源通知目标是帖子",
			sourceType:  model.NoticeSourcePost,
			sourceID:    101,
			postID:      101,
			authorID:    7,
			wantTarget:  model.NoticeTargetPost,
			wantComment: nil,
		},
		{
			name:        "评论来源通知目标是评论",
			sourceType:  model.NoticeSourceComment,
			sourceID:    300,
			postID:      101,
			authorID:    7,
			wantTarget:  model.NoticeTargetComment,
			wantComment: int64Ptr(300),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			f.users.findBriefs = []model.UserBrief{{ID: 21}}
			f.users.users[21] = activeHuman(21)
			f.ids.ids = []int64{5001}
			in := contracts.MentionNotificationsInput{
				SourceType:   tc.sourceType,
				SourceID:     tc.sourceID,
				PostID:       tc.postID,
				AuthorID:     tc.authorID,
				MentionNames: []string{"alice"},
				At:           explicitAt,
			}
			if err := f.svc.MentionNotifications(context.Background(), in); err != nil {
				t.Fatalf("MentionNotifications 返回错误: %v", err)
			}
			if len(f.notices.inserted) != 1 {
				t.Fatalf("通知写入条数 = %d，期望 1", len(f.notices.inserted))
			}
			row := f.notices.inserted[0]
			wantKey := mentionKey(tc.sourceType, tc.sourceID, 21)
			if row.IdempotencyKey != wantKey {
				t.Fatalf("幂等键 = %q，期望 %q", row.IdempotencyKey, wantKey)
			}
			if row.RecipientID != 21 || row.ActorID != tc.authorID || row.Type != model.NoticeTypeMention {
				t.Fatalf("接收者/动作者/类型不符: %+v", row)
			}
			if row.TargetType != tc.wantTarget || row.TargetID != tc.postID {
				t.Fatalf("目标类型/ID = %q/%d，期望 %q/%d", row.TargetType, row.TargetID, tc.wantTarget, tc.postID)
			}
			if row.PostID == nil || *row.PostID != tc.postID {
				t.Fatalf("PostID = %v，期望 %d", row.PostID, tc.postID)
			}
			if tc.wantComment == nil {
				if row.CommentID != nil {
					t.Fatalf("帖子来源 CommentID = %v，期望 nil", row.CommentID)
				}
			} else if row.CommentID == nil || *row.CommentID != *tc.wantComment {
				t.Fatalf("CommentID = %v，期望 %d", row.CommentID, *tc.wantComment)
			}
			if !row.CreatedAt.Equal(explicitAt) {
				t.Fatalf("CreatedAt = %v，期望 %v", row.CreatedAt, explicitAt)
			}
		})
	}
}

// TestMentionNotificationsSkipsSelf 覆盖作者提及自己时不写通知。
func TestMentionNotificationsSkipsSelf(t *testing.T) {
	f := newFixture()
	f.users.findBriefs = []model.UserBrief{{ID: 7}, {ID: 21}}
	f.users.users[7] = activeHuman(7)
	f.users.users[21] = activeHuman(21)
	f.ids.ids = []int64{5001}
	in := contracts.MentionNotificationsInput{
		SourceType:   model.NoticeSourcePost,
		SourceID:     101,
		PostID:       101,
		AuthorID:     7,
		MentionNames: []string{"self", "alice"},
		At:           testNow,
	}
	if err := f.svc.MentionNotifications(context.Background(), in); err != nil {
		t.Fatalf("MentionNotifications 返回错误: %v", err)
	}
	if len(f.notices.inserted) != 1 {
		t.Fatalf("通知写入条数 = %d，期望 1（自己不通知）", len(f.notices.inserted))
	}
	if got := f.notices.inserted[0].RecipientID; got != 21 {
		t.Fatalf("接收者 = %d，期望 21", got)
	}
	if f.ids.next != 1 {
		t.Fatalf("排除自己时只应消耗 1 个 ID，实际 %d 个", f.ids.next)
	}
}

// TestMentionNotificationsErrors 覆盖提及解析与通知写入的错误路径。
func TestMentionNotificationsErrors(t *testing.T) {
	findErr := errors.New("账号解析失败")
	writeErr := errors.New("账号读取失败")
	cases := []struct {
		name    string
		setup   func(*fixture)
		wantErr error
	}{
		{
			name: "用户名解析失败",
			setup: func(f *fixture) {
				f.users.findErr = findErr
			},
			wantErr: findErr,
		},
		{
			name: "通知写入失败",
			setup: func(f *fixture) {
				f.users.findBriefs = []model.UserBrief{{ID: 21}}
				f.users.getUserErr = writeErr
			},
			wantErr: writeErr,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			tc.setup(f)
			in := contracts.MentionNotificationsInput{
				SourceType:   model.NoticeSourcePost,
				SourceID:     101,
				PostID:       101,
				AuthorID:     7,
				MentionNames: []string{"alice"},
				At:           testNow,
			}
			err := f.svc.MentionNotifications(context.Background(), in)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("错误 = %v，期望可用 errors.Is 匹配 %v", err, tc.wantErr)
			}
			if len(f.notices.inserted) != 0 {
				t.Fatalf("失败路径不应写入通知，实际 %d 条", len(f.notices.inserted))
			}
		})
	}
}

// TestMentionNotificationsIdempotencyKeyStable 覆盖重复投递时幂等键稳定（仓储靠唯一键去重）。
func TestMentionNotificationsIdempotencyKeyStable(t *testing.T) {
	f := newFixture()
	f.users.findBriefs = []model.UserBrief{{ID: 21}}
	f.users.users[21] = activeHuman(21)
	f.ids.ids = []int64{5001, 5002}
	in := contracts.MentionNotificationsInput{
		SourceType:   model.NoticeSourcePost,
		SourceID:     101,
		PostID:       101,
		AuthorID:     7,
		MentionNames: []string{"alice"},
		At:           testNow,
	}
	for i := 0; i < 2; i++ {
		if err := f.svc.MentionNotifications(context.Background(), in); err != nil {
			t.Fatalf("第 %d 次 MentionNotifications 返回错误: %v", i+1, err)
		}
	}
	if len(f.notices.inserted) != 2 {
		t.Fatalf("两次投递的通知条数 = %d，期望 2（去重由仓储唯一键负责）", len(f.notices.inserted))
	}
	first, second := f.notices.inserted[0], f.notices.inserted[1]
	if first.IdempotencyKey != second.IdempotencyKey {
		t.Fatalf("重复投递幂等键不一致: %q vs %q", first.IdempotencyKey, second.IdempotencyKey)
	}
	if first.ID == second.ID {
		t.Fatalf("两次投递应生成不同行 ID（唯一键冲突由仓储静默忽略），实际都是 %d", first.ID)
	}
}

// ===== PublishMentions =====

// TestPublishMentions 覆盖「只解析一次用户名、重建提及 + 写提及通知」的合并契约：
// 两次账号查询被合并为一次 FindActiveUsersByUsernames，解析结果同时用于重建与通知。
func TestPublishMentions(t *testing.T) {
	explicitAt := testNow.Add(10 * time.Minute)
	f := newFixture()
	f.users.findBriefs = []model.UserBrief{{ID: 21}, {ID: 22}}
	f.users.users[21] = activeHuman(21)
	f.users.users[22] = activeHuman(22)
	f.ids.ids = []int64{9001, 9002, 9003, 9004}
	in := contracts.PublishMentionsInput{
		SourceType:   model.NoticeSourceComment,
		SourceID:     300,
		PostID:       101,
		AuthorID:     7,
		PlainText:    "@alice @bob 你好",
		MentionNames: []string{"alice", "bob"},
		At:           explicitAt,
	}
	if err := f.svc.PublishMentions(context.Background(), in); err != nil {
		t.Fatalf("PublishMentions 返回错误: %v", err)
	}
	if len(f.users.findCalls) != 1 || !slices.Equal(f.users.findCalls[0], []string{"alice", "bob"}) {
		t.Fatalf("用户名解析调用 = %v，期望只解析一次 [alice bob]", f.users.findCalls)
	}
	if len(f.mentions.deactivateExceptCalls) != 1 {
		t.Fatalf("DeactivateExcept 调用次数 = %d，期望 1", len(f.mentions.deactivateExceptCalls))
	}
	deactivate := f.mentions.deactivateExceptCalls[0]
	if deactivate.sourceType != model.NoticeSourceComment || deactivate.sourceID != 300 ||
		!slices.Equal(deactivate.keepUserIDs, []int64{21, 22}) {
		t.Fatalf("DeactivateExcept 入参不符: %+v", deactivate)
	}
	if len(f.mentions.upserts) != 2 {
		t.Fatalf("Upsert 次数 = %d，期望 2", len(f.mentions.upserts))
	}
	for i, wantUserID := range []int64{21, 22} {
		got := f.mentions.upserts[i]
		if got.id != int64(9001+i) || got.userID != wantUserID || got.sourceID != 300 ||
			got.sourceType != model.NoticeSourceComment || !got.now.Equal(explicitAt) {
			t.Fatalf("第 %d 次 Upsert = %+v", i+1, got)
		}
	}
	if len(f.notices.inserted) != 2 {
		t.Fatalf("通知写入条数 = %d，期望 2", len(f.notices.inserted))
	}
	for i, wantUserID := range []int64{21, 22} {
		row := f.notices.inserted[i]
		if row.ID != int64(9003+i) || row.RecipientID != wantUserID || row.ActorID != 7 {
			t.Fatalf("第 %d 条通知 = %+v，期望接收者 %d", i+1, row, wantUserID)
		}
		if row.Type != model.NoticeTypeMention || row.TargetType != model.NoticeTargetComment {
			t.Fatalf("第 %d 条通知类型/目标类型不符: %+v", i+1, row)
		}
		if row.TargetID != 101 || row.PostID == nil || *row.PostID != 101 ||
			row.CommentID == nil || *row.CommentID != 300 {
			t.Fatalf("第 %d 条通知关联不符: %+v", i+1, row)
		}
		if want := mentionKey(model.NoticeSourceComment, 300, wantUserID); row.IdempotencyKey != want {
			t.Fatalf("第 %d 条通知幂等键 = %q，期望 %q", i+1, row.IdempotencyKey, want)
		}
		if !row.CreatedAt.Equal(explicitAt) {
			t.Fatalf("第 %d 条通知时间 = %v，期望 %v", i+1, row.CreatedAt, explicitAt)
		}
	}
	if f.ids.next != 4 {
		t.Fatalf("消耗 ID = %d 个，期望 4（2 个提及 + 2 条通知）", f.ids.next)
	}
}

// TestPublishMentionsEmptyNames 覆盖无提及时只停用旧提及、不解析也不写通知。
func TestPublishMentionsEmptyNames(t *testing.T) {
	f := newFixture()
	in := contracts.PublishMentionsInput{
		SourceType:   model.NoticeSourceComment,
		SourceID:     300,
		PostID:       101,
		AuthorID:     7,
		PlainText:    "没有提及",
		MentionNames: nil,
		At:           testNow,
	}
	if err := f.svc.PublishMentions(context.Background(), in); err != nil {
		t.Fatalf("PublishMentions 返回错误: %v", err)
	}
	if len(f.users.findCalls) != 0 {
		t.Fatalf("空提及名不应解析账号，实际 %d 次", len(f.users.findCalls))
	}
	if len(f.mentions.deactivateExceptCalls) != 1 || f.mentions.deactivateExceptCalls[0].keepUserIDs != nil {
		t.Fatalf("DeactivateExcept 入参不符: %+v", f.mentions.deactivateExceptCalls)
	}
	if len(f.notices.inserted) != 0 || f.ids.next != 0 {
		t.Fatalf("空提及名不应写通知或消耗 ID，实际 inserted=%d ids=%d", len(f.notices.inserted), f.ids.next)
	}
}

// TestPublishMentionsErrors 覆盖解析、停用、建链与通知写入的错误路径。
func TestPublishMentionsErrors(t *testing.T) {
	findErr := errors.New("账号解析失败")
	deactivateErr := errors.New("停用旧提及失败")
	upsertErr := errors.New("提及建链失败")
	writeErr := errors.New("接收者读取失败")
	cases := []struct {
		name        string
		setup       func(*fixture)
		wantErr     error
		wantDeact   bool
		wantUpserts int
	}{
		{
			name:    "用户名解析失败",
			setup:   func(f *fixture) { f.users.findErr = findErr },
			wantErr: findErr,
		},
		{
			name: "停用旧提及失败",
			setup: func(f *fixture) {
				f.users.findBriefs = []model.UserBrief{{ID: 21}}
				f.mentions.deactivateExceptErr = deactivateErr
			},
			wantErr:   deactivateErr,
			wantDeact: true,
		},
		{
			name: "建链失败",
			setup: func(f *fixture) {
				f.users.findBriefs = []model.UserBrief{{ID: 21}}
				f.ids.ids = []int64{9001}
				f.mentions.upsertErr = upsertErr
			},
			wantErr:     upsertErr,
			wantDeact:   true,
			wantUpserts: 1,
		},
		{
			name: "通知写入失败",
			setup: func(f *fixture) {
				f.users.findBriefs = []model.UserBrief{{ID: 21}}
				f.ids.ids = []int64{9001}
				f.users.getUserErr = writeErr
			},
			wantErr:     writeErr,
			wantDeact:   true,
			wantUpserts: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			tc.setup(f)
			in := contracts.PublishMentionsInput{
				SourceType:   model.NoticeSourceComment,
				SourceID:     300,
				PostID:       101,
				AuthorID:     7,
				MentionNames: []string{"alice"},
				At:           testNow,
			}
			err := f.svc.PublishMentions(context.Background(), in)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("错误 = %v，期望可用 errors.Is 匹配 %v", err, tc.wantErr)
			}
			if got := len(f.mentions.deactivateExceptCalls) > 0; got != tc.wantDeact {
				t.Fatalf("DeactivateExcept 是否调用 = %v，期望 %v", got, tc.wantDeact)
			}
			if len(f.mentions.upserts) != tc.wantUpserts {
				t.Fatalf("Upsert 调用次数 = %d，期望 %d", len(f.mentions.upserts), tc.wantUpserts)
			}
			if len(f.notices.inserted) != 0 {
				t.Fatalf("失败路径不应写入通知，实际 %d 条", len(f.notices.inserted))
			}
		})
	}
}

// ===== PublishPostNotifications =====

// TestPublishPostNotifications 覆盖发布帖子时重建提及并写提及通知。
func TestPublishPostNotifications(t *testing.T) {
	t.Run("有提及时先重建再通知", func(t *testing.T) {
		f := newFixture()
		f.users.findBriefs = []model.UserBrief{{ID: 21}}
		f.users.users[21] = activeHuman(21)
		f.ids.ids = []int64{7001, 7002}
		in := contracts.PostNotificationsInput{
			PostID:       101,
			AuthorID:     7,
			MentionNames: []string{"alice"},
			At:           testNow,
		}
		if err := f.svc.PublishPostNotifications(context.Background(), in); err != nil {
			t.Fatalf("PublishPostNotifications 返回错误: %v", err)
		}
		if len(f.mentions.deactivateExceptCalls) != 1 {
			t.Fatalf("DeactivateExcept 调用次数 = %d，期望 1", len(f.mentions.deactivateExceptCalls))
		}
		deactivate := f.mentions.deactivateExceptCalls[0]
		if deactivate.sourceType != model.NoticeSourcePost || deactivate.sourceID != in.PostID ||
			!slices.Equal(deactivate.keepUserIDs, []int64{21}) {
			t.Fatalf("DeactivateExcept 入参不符: %+v", deactivate)
		}
		if len(f.mentions.upserts) != 1 || f.mentions.upserts[0].id != 7001 {
			t.Fatalf("Upsert 记录不符: %+v", f.mentions.upserts)
		}
		if len(f.users.findCalls) != 1 {
			t.Fatalf("用户名解析次数 = %d，期望 1（重建与通知复用同一解析结果）", len(f.users.findCalls))
		}
		if len(f.notices.inserted) != 1 {
			t.Fatalf("通知写入条数 = %d，期望 1", len(f.notices.inserted))
		}
		row := f.notices.inserted[0]
		if row.ID != 7002 || row.IdempotencyKey != mentionKey(model.NoticeSourcePost, 101, 21) {
			t.Fatalf("通知字段不符: %+v", row)
		}
		if row.TargetType != model.NoticeTargetPost || row.TargetID != in.PostID {
			t.Fatalf("目标字段不符: %+v", row)
		}
	})

	t.Run("无提及时只重建不通知", func(t *testing.T) {
		f := newFixture()
		in := contracts.PostNotificationsInput{PostID: 101, AuthorID: 7, At: testNow}
		if err := f.svc.PublishPostNotifications(context.Background(), in); err != nil {
			t.Fatalf("PublishPostNotifications 返回错误: %v", err)
		}
		if len(f.mentions.deactivateExceptCalls) != 1 {
			t.Fatalf("DeactivateExcept 调用次数 = %d，期望 1", len(f.mentions.deactivateExceptCalls))
		}
		if len(f.users.findCalls) != 0 {
			t.Fatalf("无提及名时不应解析用户名，实际 %d 次", len(f.users.findCalls))
		}
		if len(f.notices.inserted) != 0 {
			t.Fatalf("无提及名时不应写通知，实际 %d 条", len(f.notices.inserted))
		}
	})

	t.Run("重建提及失败", func(t *testing.T) {
		sentinel := errors.New("重建失败")
		f := newFixture()
		f.users.findErr = sentinel
		in := contracts.PostNotificationsInput{
			PostID: 101, AuthorID: 7, MentionNames: []string{"alice"}, At: testNow,
		}
		if err := f.svc.PublishPostNotifications(context.Background(), in); !errors.Is(err, sentinel) {
			t.Fatalf("错误 = %v，期望 %v", err, sentinel)
		}
		if len(f.notices.inserted) != 0 {
			t.Fatal("重建失败后不应写通知")
		}
	})

	t.Run("写提及通知失败", func(t *testing.T) {
		sentinel := errors.New("账号读取失败")
		f := newFixture()
		f.users.findBriefs = []model.UserBrief{{ID: 21}}
		f.users.getUserErr = sentinel
		f.ids.ids = []int64{7001}
		in := contracts.PostNotificationsInput{
			PostID: 101, AuthorID: 7, MentionNames: []string{"alice"}, At: testNow,
		}
		if err := f.svc.PublishPostNotifications(context.Background(), in); !errors.Is(err, sentinel) {
			t.Fatalf("错误 = %v，期望 %v", err, sentinel)
		}
	})
}

// ===== DeactivateMentions =====

// TestDeactivateMentions 覆盖提及停用与错误透传。
func TestDeactivateMentions(t *testing.T) {
	t.Run("成功停用来源对象全部提及", func(t *testing.T) {
		f := newFixture()
		if err := f.svc.DeactivateMentions(
			context.Background(), model.NoticeSourcePost, 101, testNow,
		); err != nil {
			t.Fatalf("DeactivateMentions 返回错误: %v", err)
		}
		if len(f.mentions.deactivateAllCalls) != 1 {
			t.Fatalf("DeactivateAll 调用次数 = %d，期望 1", len(f.mentions.deactivateAllCalls))
		}
		call := f.mentions.deactivateAllCalls[0]
		if call.sourceType != model.NoticeSourcePost || call.sourceID != 101 {
			t.Fatalf("DeactivateAll 入参不符: %+v", call)
		}
	})

	t.Run("仓储错误透传", func(t *testing.T) {
		sentinel := errors.New("停用失败")
		f := newFixture()
		f.mentions.deactivateAllErr = sentinel
		if err := f.svc.DeactivateMentions(
			context.Background(), model.NoticeSourceComment, 300, time.Time{},
		); !errors.Is(err, sentinel) {
			t.Fatalf("错误 = %v，期望 %v", err, sentinel)
		}
	})
}

// ===== ListActiveMentions / GetMention / resolveMentionIDs =====

// TestListActiveMentions 覆盖默认 limit、行映射与错误透传。
func TestListActiveMentions(t *testing.T) {
	t.Run("映射生效提及并透传查询参数", func(t *testing.T) {
		f := newFixture()
		createdAt := testNow.Add(-time.Hour)
		f.mentions.activeRows = []domain.MentionRow{{
			ID: 88, MentionedUserID: 21, SourceType: model.NoticeSourceComment,
			SourceID: 300, CreatedAt: createdAt,
		}}
		query := contracts.MentionQuery{MentionedUserID: 21, Since: testNow.Add(-2 * time.Hour), Limit: 5}
		refs, err := f.svc.ListActiveMentions(context.Background(), query)
		if err != nil {
			t.Fatalf("ListActiveMentions 返回错误: %v", err)
		}
		if len(refs) != 1 {
			t.Fatalf("提及数量 = %d，期望 1", len(refs))
		}
		want := contracts.MentionRef{
			ID: 88, MentionedUserID: 21, SourceType: model.NoticeSourceComment,
			SourceID: 300, CreatedAt: createdAt,
		}
		if refs[0] != want {
			t.Fatalf("提及映射 = %+v，期望 %+v", refs[0], want)
		}
		if len(f.mentions.listActiveCalls) != 1 {
			t.Fatalf("ListActive 调用次数 = %d，期望 1", len(f.mentions.listActiveCalls))
		}
		call := f.mentions.listActiveCalls[0]
		if call.userID != 21 || call.limit != 5 || !call.since.Equal(query.Since) {
			t.Fatalf("ListActive 入参不符: %+v", call)
		}
	})

	t.Run("默认 limit 与空结果", func(t *testing.T) {
		cases := []struct {
			name      string
			limit     int
			wantLimit int
		}{
			{name: "limit 为 0 使用默认 20", limit: 0, wantLimit: 20},
			{name: "负数 limit 使用默认 20", limit: -3, wantLimit: 20},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				f := newFixture()
				refs, err := f.svc.ListActiveMentions(context.Background(), contracts.MentionQuery{Limit: tc.limit})
				if err != nil {
					t.Fatalf("ListActiveMentions 返回错误: %v", err)
				}
				if refs == nil || len(refs) != 0 {
					t.Fatalf("空结果 = %v，期望非 nil 空切片", refs)
				}
				if got := f.mentions.listActiveCalls[0].limit; got != tc.wantLimit {
					t.Fatalf("limit = %d，期望 %d", got, tc.wantLimit)
				}
			})
		}
	})

	t.Run("仓储错误透传", func(t *testing.T) {
		sentinel := errors.New("读取失败")
		f := newFixture()
		f.mentions.listActiveErr = sentinel
		if _, err := f.svc.ListActiveMentions(context.Background(), contracts.MentionQuery{Limit: 5}); !errors.Is(err, sentinel) {
			t.Fatalf("错误 = %v，期望 %v", err, sentinel)
		}
	})
}

// TestGetMention 覆盖单条提及读取与 not_found 映射。
func TestGetMention(t *testing.T) {
	t.Run("成功映射单条提及", func(t *testing.T) {
		f := newFixture()
		createdAt := testNow.Add(-time.Minute)
		f.mentions.getActiveRow = domain.MentionRow{
			ID: 77, MentionedUserID: 21, SourceType: model.NoticeSourcePost,
			SourceID: 101, CreatedAt: createdAt,
		}
		ref, err := f.svc.GetMention(context.Background(), 77)
		if err != nil {
			t.Fatalf("GetMention 返回错误: %v", err)
		}
		want := contracts.MentionRef{
			ID: 77, MentionedUserID: 21, SourceType: model.NoticeSourcePost,
			SourceID: 101, CreatedAt: createdAt,
		}
		if ref != want {
			t.Fatalf("提及映射 = %+v，期望 %+v", ref, want)
		}
		if len(f.mentions.getActiveCalls) != 1 || f.mentions.getActiveCalls[0] != 77 {
			t.Fatalf("GetActive 入参 = %v，期望 [77]", f.mentions.getActiveCalls)
		}
	})

	t.Run("不存在返回 not_found", func(t *testing.T) {
		f := newFixture()
		f.mentions.getActiveErr = apperr.NotFound("提及不存在")
		_, err := f.svc.GetMention(context.Background(), 404)
		requireKind(t, err, apperr.KindNotFound)
	})
}

// TestResolveMentionIDs 覆盖提及名解析的短路、顺序与错误透传。
func TestResolveMentionIDs(t *testing.T) {
	t.Run("空名称短路", func(t *testing.T) {
		f := newFixture()
		ids, err := f.svc.resolveMentionIDs(context.Background(), nil)
		if err != nil || ids != nil {
			t.Fatalf("resolveMentionIDs(nil) = %v/%v，期望 nil/nil", ids, err)
		}
		if len(f.users.findCalls) != 0 {
			t.Fatalf("空名称不应查询账号，实际 %d 次", len(f.users.findCalls))
		}
	})

	t.Run("保持解析结果顺序", func(t *testing.T) {
		f := newFixture()
		f.users.findBriefs = []model.UserBrief{{ID: 22}, {ID: 21}}
		ids, err := f.svc.resolveMentionIDs(context.Background(), []string{"bob", "alice"})
		if err != nil {
			t.Fatalf("resolveMentionIDs 返回错误: %v", err)
		}
		if !slices.Equal(ids, []int64{22, 21}) {
			t.Fatalf("解析结果 = %v，期望 [22 21]", ids)
		}
		if len(f.users.findCalls) != 1 || !slices.Equal(f.users.findCalls[0], []string{"bob", "alice"}) {
			t.Fatalf("账号查询入参 = %v", f.users.findCalls)
		}
	})

	t.Run("解析错误透传", func(t *testing.T) {
		sentinel := errors.New("查询失败")
		f := newFixture()
		f.users.findErr = sentinel
		if _, err := f.svc.resolveMentionIDs(context.Background(), []string{"alice"}); !errors.Is(err, sentinel) {
			t.Fatalf("错误 = %v，期望 %v", err, sentinel)
		}
	})
}
