package app

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// TestNewFillsDefaults 验证 New 在未注入 Clock/限频器时补生产默认值。
func TestNewFillsDefaults(t *testing.T) {
	svc := New(Deps{})
	if svc.deps.Clock == nil {
		t.Fatal("nil Clock 应回退系统时钟")
	}
	if svc.deps.CommentLimiter == nil || svc.deps.LikeLimiter == nil || svc.deps.FollowLimiter == nil {
		t.Fatal("nil 限频器应回退生产默认参数")
	}
	if got := svc.now(); got.IsZero() {
		t.Fatalf("默认时钟返回零值时间: %v", got)
	}
}

// TestNewKeepsInjectedDeps 验证显式注入的 Clock 不被覆盖。
func TestNewKeepsInjectedDeps(t *testing.T) {
	svc := newStubSet().service()
	if got := svc.now(); !got.Equal(stubNow) {
		t.Fatalf("now = %v，期望固定时间 %v", got, stubNow)
	}
}

// TestIsFollowing 覆盖非法 ID、跟随与未跟随三种情况。
func TestIsFollowing(t *testing.T) {
	stubs := newStubSet()
	svc := stubs.service()
	ctx := context.Background()

	for _, tc := range []struct {
		name     string
		follower int64
		followee int64
	}{
		{"follower 非法", 0, stubAuthorID},
		{"followee 非法", stubViewerID, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := svc.IsFollowing(ctx, tc.follower, tc.followee)
			requireOK(t, err)
			if got {
				t.Fatalf("非法 ID 组合应返回 false")
			}
		})
	}

	stubs.follows.following[[2]int64{stubViewerID, stubAuthorID}] = true
	got, err := svc.IsFollowing(ctx, stubViewerID, stubAuthorID)
	requireOK(t, err)
	if !got {
		t.Fatal("已建立关注关系应返回 true")
	}
	got, err = svc.IsFollowing(ctx, stubAuthorID, stubViewerID)
	requireOK(t, err)
	if got {
		t.Fatal("反向关系不应为 true")
	}
}

// TestIsFollowingError 验证仓储错误原样透传。
func TestIsFollowingError(t *testing.T) {
	stubs := newStubSet()
	boom := apperr.Internal("查询关注关系失败")
	stubs.follows.followingErr = boom
	_, err := stubs.service().IsFollowing(context.Background(), stubViewerID, stubAuthorID)
	if requireKind(t, err, apperr.KindInternal) != boom {
		t.Fatalf("应透传仓储错误")
	}
}

// TestFollowingSet 验证关注集合读取与错误透传。
func TestFollowingSet(t *testing.T) {
	stubs := newStubSet()
	stubs.follows.followingSet[stubViewerID] = map[int64]bool{stubAuthorID: true, stubOtherID: true}
	svc := stubs.service()

	got, err := svc.FollowingSet(context.Background(), stubViewerID)
	requireOK(t, err)
	if len(got) != 2 || !got[stubAuthorID] || !got[stubOtherID] {
		t.Fatalf("关注集合 = %v，期望包含 %d/%d", got, stubAuthorID, stubOtherID)
	}
	// 返回副本，修改结果不应影响 fake 内部状态。
	got[stubAuthorID] = false
	if !stubs.follows.followingSet[stubViewerID][stubAuthorID] {
		t.Fatal("FollowingSet 返回值不应与 fake 内部共享底层 map")
	}

	boom := apperr.Internal("读取关注集合失败")
	stubs.follows.followingSetErr = boom
	if _, err := svc.FollowingSet(context.Background(), stubViewerID); requireKind(t, err, apperr.KindInternal) != boom {
		t.Fatal("应透传仓储错误")
	}
}

// TestFollowState 覆盖四态分类、非法 ID 与依赖错误。
func TestFollowState(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name     string
		follow   bool
		pending  bool
		incoming bool
		want     string
	}{
		{"已关注", true, false, false, model.FollowStateFollowing},
		{"已发出申请", false, true, false, model.FollowStatePending},
		{"收到对方申请", false, false, true, model.FollowStateIncomingPending},
		{"无关系", false, false, false, model.FollowStateNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubs := newStubSet()
			svc := stubs.service()
			stubs.follows.following[[2]int64{stubViewerID, stubAuthorID}] = tc.follow
			stubs.requests.hasPendingMap[[2]int64{stubViewerID, stubAuthorID}] = tc.pending
			stubs.requests.hasPendingMap[[2]int64{stubAuthorID, stubViewerID}] = tc.incoming

			got, err := svc.FollowState(ctx, stubViewerID, stubAuthorID)
			requireOK(t, err)
			if got != tc.want {
				t.Fatalf("FollowState = %q，期望 %q", got, tc.want)
			}
		})
	}

	stubs := newStubSet()
	svc := stubs.service()
	if got, err := svc.FollowState(ctx, 0, stubAuthorID); err != nil || got != model.FollowStateNone {
		t.Fatalf("游客 FollowState = (%q, %v)，期望 (none, nil)", got, err)
	}

	stubs.follows.followingErr = apperr.Internal("查询失败")
	if _, err := svc.FollowState(ctx, stubViewerID, stubAuthorID); requireKind(t, err, apperr.KindInternal) == nil {
		t.Fatal("关注查询错误应透传")
	}

	stubs = newStubSet()
	stubs.requests.hasErr = apperr.Internal("申请查询失败")
	if _, err := stubs.service().FollowState(ctx, stubViewerID, stubAuthorID); requireKind(t, err, apperr.KindInternal) == nil {
		t.Fatal("申请查询错误应透传")
	}
}

// TestGetComments 验证批量评论读取直接委托仓储。
func TestGetComments(t *testing.T) {
	stubs := newStubSet()
	stubs.comments.byIDsMap = map[int64]model.Comment{7: {ID: 7, PostID: stubPostID}}
	got, err := stubs.service().GetComments(context.Background(), []int64{7, 8})
	requireOK(t, err)
	if len(got) != 1 || got[7].ID != 7 {
		t.Fatalf("GetComments = %v，期望仅评论 7", got)
	}

	boom := apperr.Internal("读取评论失败")
	stubs.comments.byIDsErr = boom
	if _, err := stubs.service().GetComments(context.Background(), []int64{7}); requireKind(t, err, apperr.KindInternal) != boom {
		t.Fatal("应透传仓储错误")
	}
}

// TestTrimPage 覆盖多查一条的裁剪逻辑。
func TestTrimPage(t *testing.T) {
	cases := []struct {
		name     string
		items    []int
		limit    int
		wantLen  int
		wantMore bool
	}{
		{"不足一页", []int{1, 2}, 3, 2, false},
		{"正好一页", []int{1, 2, 3}, 3, 3, false},
		{"多查一条", []int{1, 2, 3, 4}, 3, 3, true},
		{"空列表", nil, 3, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items, hasMore := trimPage(tc.items, tc.limit)
			if len(items) != tc.wantLen || hasMore != tc.wantMore {
				t.Fatalf("trimPage = (%v, %v)，期望 (%d 条, %v)", items, hasMore, tc.wantLen, tc.wantMore)
			}
		})
	}
}

// TestNotifyKeys 验证稳定幂等键格式；关注键还带生效时间秒级精度。
func TestNotifyKeys(t *testing.T) {
	if got := notifyKey("post_like", 42); got != "post_like:42" {
		t.Fatalf("notifyKey = %q，期望 post_like:42", got)
	}
	at := time.Date(2026, 10, 18, 9, 30, 0, 0, time.UTC)
	want := "follow:7:" + strconv.FormatInt(at.Unix(), 10)
	if got := followNotifyKey(7, at); got != want {
		t.Fatalf("followNotifyKey = %q，期望 %q", got, want)
	}
	// 同一关注行、不同生效时间应生成不同幂等键：取关后重新关注可再次通知。
	if same := followNotifyKey(7, at); same == followNotifyKey(7, at.Add(time.Minute)) {
		t.Fatal("不同生效时间的关注通知键不应相同")
	}
}

// TestNewIDFailure 覆盖 ID 生成错误与非正 ID 兜底，并确认错误包装为 internal。
func TestNewIDFailure(t *testing.T) {
	cause := apperr.Internal("雪花节点未就绪")
	svc := New(Deps{IDs: errIDs{err: cause}})
	if _, err := svc.newID(); requireKind(t, err, apperr.KindInternal) == nil {
		t.Fatal("ID 生成失败应包装为 internal 错误")
	}

	svc = New(Deps{IDs: zeroIDs{}})
	if _, err := svc.newID(); requireKind(t, err, apperr.KindInternal) == nil {
		t.Fatal("非正 ID 应返回 internal 错误")
	}
}

// TestAllowActionRateLimited 验证用户维度限频超限返回 rate_limited 并携带重试间隔。
func TestAllowActionRateLimited(t *testing.T) {
	stubs := newStubSet()
	stubs.likeLimiter = newFixedLimiter(1)
	svc := stubs.service()
	viewer := stubViewer()

	requireOK(t, svc.allowAction(svc.deps.LikeLimiter, viewer))
	err := svc.allowAction(svc.deps.LikeLimiter, viewer)
	appErr := requireKind(t, err, apperr.KindRateLimited)
	if appErr.RetryAfter <= 0 {
		t.Fatalf("限频错误应携带重试间隔，实际 %v", appErr.RetryAfter)
	}
}

// TestRequireWritable 覆盖未登录、停用与 AI 三种拒绝条件。
func TestRequireWritable(t *testing.T) {
	stubs := newStubSet()
	svc := stubs.service()

	if err := svc.requireWritable(stubGuestViewer()); requireKind(t, err, apperr.KindUnauthenticated) == nil {
		t.Fatal("游客写操作应返回 unauthenticated")
	}
	if err := svc.requireWritable(stubSuspendedViewer()); requireKind(t, err, apperr.KindSuspended) == nil {
		t.Fatal("停用账号写操作应返回 suspended")
	}
	aiViewer := model.ViewerFrom(model.User{ID: stubAIID, IsAI: true, Status: model.StatusActive})
	if err := svc.requireWritable(aiViewer); requireKind(t, err, apperr.KindForbidden) == nil {
		t.Fatal("AI 身份不应通过 HTTP 写接口")
	}
	requireOK(t, svc.requireWritable(stubViewer()))
}

// TestBumpPostCounterPassesInput 验证帖子计数端口收到完整参数。
func TestBumpPostCounterPassesInput(t *testing.T) {
	stubs := newStubSet()
	stubs.posts.counterValue = 11
	svc := stubs.service()

	count, err := svc.bumpPostCounter(context.Background(), stubPostID, contracts.PostCounterLike, 1, stubNow)
	requireOK(t, err)
	if count != 11 {
		t.Fatalf("计数返回值 = %d，期望 11", count)
	}
	want := contracts.PostCounterInput{PostID: stubPostID, Counter: contracts.PostCounterLike, Delta: 1, At: stubNow}
	if len(stubs.posts.counters) != 1 || stubs.posts.counters[0] != want {
		t.Fatalf("计数端口收到 %+v，期望 %+v", stubs.posts.counters, want)
	}
}
