package http

// 本文件提供 social/http 单元测试的固定装配：用 app.New(fake Deps) 构造 *app.Service，
// 再用 gin 测试上下文直接调用 handler，避免依赖数据库与网络。
// fake 端口内嵌对应接口，只覆写用例实际调用到的方法。

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/modules/social/app"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/audit"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/platform/ratelimit"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/shared/view"
	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// userBriefResponseAlias 让列表响应解码复用生产 DTO 类型，避免测试重复声明字段。
type userBriefResponseAlias = view.UserBriefResponse

// testNow 是全部用例共享的固定业务时间。
var testNow = time.Date(2026, 10, 18, 9, 30, 0, 0, time.UTC)

// 测试账号与资源 ID 约定。
const (
	uidViewer    int64 = 1
	uidAuthor    int64 = 2
	uidOther     int64 = 3
	uidSuspended int64 = 4
	uidAdmin     int64 = 99

	pid       int64 = 20
	commentID int64 = 60
)

func init() { gin.SetMode(gin.TestMode) }

// errNotFound 是 fake 端口返回的标准领域 NotFound 错误。
var errNotFound = apperr.NotFound("内容不存在或无权访问")

// ===== 通用 fake =====

type seqIDs struct{ next int64 }

func (s *seqIDs) Next() (int64, error) { s.next++; return s.next, nil }

type passTx struct{}

func (passTx) WithTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type quietAudit struct{ audit.Writer }

func (quietAudit) Write(context.Context, audit.Entry) error { return nil }

// viewerFor 构造已登录访问者。
func viewerFor(id int64) model.Viewer {
	return model.ViewerFrom(model.User{ID: id, Status: model.StatusActive})
}

// publicPost 返回默认公开已发布帖子。
func publicPost() model.Post {
	return model.Post{
		ID:           pid,
		AuthorID:     uidAuthor,
		Status:       model.PostStatusPublished,
		Visibility:   model.VisibilityPublic,
		LikeCount:    3,
		CommentCount: 4,
		RepostCount:  5,
		CreatedAt:    testNow.Add(-time.Hour),
		UpdatedAt:    testNow.Add(-time.Hour),
	}
}

// ===== auth 端口 =====

type fakeUsers struct {
	contracts.UserReader

	users     map[int64]model.User
	briefs    map[int64]model.UserBrief
	byName    map[string]model.User
	suspended map[int64]bool

	userErr, nameErr, briefErr error
}

func newFakeUsers() *fakeUsers {
	active := func(id int64, username string) model.User {
		return model.User{ID: id, Username: username, Status: model.StatusActive}
	}
	f := &fakeUsers{
		users: map[int64]model.User{
			uidViewer:    active(uidViewer, "alice"),
			uidAuthor:    active(uidAuthor, "bob"),
			uidOther:     active(uidOther, "carol"),
			uidSuspended: {ID: uidSuspended, Username: "dave", Status: model.StatusSuspended},
			uidAdmin:     {ID: uidAdmin, Username: "root", Role: model.RoleAdmin, Status: model.StatusActive},
		},
		briefs:    map[int64]model.UserBrief{},
		byName:    map[string]model.User{},
		suspended: map[int64]bool{uidSuspended: true},
	}
	for id, user := range f.users {
		f.byName[user.Username] = user
		f.briefs[id] = model.UserBriefFromUser(user)
	}
	return f
}

func (f *fakeUsers) GetUser(_ context.Context, userID int64) (model.User, error) {
	if f.userErr != nil {
		return model.User{}, f.userErr
	}
	if user, ok := f.users[userID]; ok {
		return user, nil
	}
	return model.User{}, nil
}

func (f *fakeUsers) GetUserByUsername(_ context.Context, username string) (model.User, error) {
	if f.nameErr != nil {
		return model.User{}, f.nameErr
	}
	user, ok := f.byName[username]
	if !ok {
		return model.User{}, errNotFound
	}
	return user, nil
}

func (f *fakeUsers) GetUserBriefs(_ context.Context, userIDs []int64) (map[int64]model.UserBrief, error) {
	if f.briefErr != nil {
		return nil, f.briefErr
	}
	result := make(map[int64]model.UserBrief, len(userIDs))
	for _, id := range userIDs {
		if brief, ok := f.briefs[id]; ok {
			result[id] = brief
		}
	}
	return result, nil
}

func (f *fakeUsers) IsSuspended(_ context.Context, userID int64) (bool, error) {
	return f.suspended[userID], nil
}

// ===== content 端口 =====

type fakePosts struct {
	contracts.ContentService

	visible model.Post
	getPost model.Post
	counter int

	visibleErr, getPostErr, counterErr error
}

func newFakePosts() *fakePosts {
	post := publicPost()
	return &fakePosts{visible: post, getPost: post}
}

func (f *fakePosts) GetVisiblePost(_ context.Context, _ model.Viewer, _ int64) (model.Post, error) {
	if f.visibleErr != nil {
		return model.Post{}, f.visibleErr
	}
	return f.visible, nil
}

func (f *fakePosts) GetPost(_ context.Context, _ int64) (model.Post, error) {
	if f.getPostErr != nil {
		return model.Post{}, f.getPostErr
	}
	return f.getPost, nil
}

func (f *fakePosts) BumpPostCounter(_ context.Context, _ contracts.PostCounterInput) (int, error) {
	if f.counterErr != nil {
		return 0, f.counterErr
	}
	return f.counter, nil
}

func (f *fakePosts) GetPosts(_ context.Context, _ []int64) (map[int64]model.Post, error) {
	return map[int64]model.Post{f.visible.ID: f.visible}, nil
}

func (f *fakePosts) GetPostPlainTexts(_ context.Context, _ []int64) (map[int64]string, error) {
	return map[int64]string{f.visible.ID: "公开帖子正文"}, nil
}

// ===== notice 端口 =====

type fakeNotices struct {
	contracts.NoticeService
	created int
}

func (f *fakeNotices) CreateNotification(_ context.Context, _ contracts.NotificationInput) error {
	f.created++
	return nil
}

func (f *fakeNotices) PublishMentions(context.Context, contracts.PublishMentionsInput) error {
	return nil
}

func (f *fakeNotices) DeactivateMentions(context.Context, string, int64, time.Time) error { return nil }

// ===== 关注端口 =====

type fakeFollows struct {
	app.FollowRepo

	followerRows  []app.FollowRow
	followingRows []app.FollowRow
	active        map[[2]int64]bool
}

func newFakeFollows() *fakeFollows { return &fakeFollows{active: map[[2]int64]bool{}} }

func (f *fakeFollows) Following(_ context.Context, followerID, followeeID int64) (bool, error) {
	return f.active[[2]int64{followerID, followeeID}], nil
}

func (f *fakeFollows) FollowingSet(context.Context, int64) (map[int64]bool, error) {
	return map[int64]bool{}, nil
}

func (f *fakeFollows) InsertActive(
	_ context.Context, id, _, _ int64, _ time.Time,
) (int64, time.Time, bool, error) {
	return id, testNow, true, nil
}

func (f *fakeFollows) Deactivate(context.Context, int64, int64, time.Time) (bool, error) {
	return true, nil
}

func (f *fakeFollows) ListFollowers(context.Context, int64, int64, int) ([]app.FollowRow, error) {
	return f.followerRows, nil
}

func (f *fakeFollows) ListFollowing(context.Context, int64, int64, int) ([]app.FollowRow, error) {
	return f.followingRows, nil
}

// ===== 关注申请端口 =====

type fakeRequests struct {
	app.FollowRequestRepo

	row          app.RequestRow
	hasRow       bool
	incoming     []app.RequestRow
	outgoing     []app.RequestRow
	updatedState string

	incomingErr, outgoingErr error
}

func (f *fakeRequests) InsertPending(_ context.Context, id, _, _ int64, _ time.Time) (int64, bool, error) {
	return id, true, nil
}

func (f *fakeRequests) GetForUpdate(context.Context, int64) (app.RequestRow, error) {
	if !f.hasRow {
		return app.RequestRow{}, errNotFound
	}
	return f.row, nil
}

func (f *fakeRequests) UpdateStatus(_ context.Context, _ int64, status string, _ time.Time) error {
	f.updatedState = status
	return nil
}

func (f *fakeRequests) ListPendingIncoming(context.Context, int64, int64, int) ([]app.RequestRow, error) {
	if f.incomingErr != nil {
		return nil, f.incomingErr
	}
	return f.incoming, nil
}

func (f *fakeRequests) ListPendingOutgoing(context.Context, int64, int64, int) ([]app.RequestRow, error) {
	if f.outgoingErr != nil {
		return nil, f.outgoingErr
	}
	return f.outgoing, nil
}

func (f *fakeRequests) CancelPending(context.Context, int64, int64, time.Time) (bool, error) {
	return true, nil
}

// ===== 点赞端口 =====

type fakeLikes struct {
	app.LikeRepo

	activateChanged   bool
	deactivateChanged bool
	activeStates      map[int64]bool
	activateErr       error
}

func newFakeLikes() *fakeLikes {
	return &fakeLikes{activateChanged: true, deactivateChanged: true, activeStates: map[int64]bool{}}
}

func (f *fakeLikes) Activate(
	_ context.Context, id, _ int64, _ string, _ int64, _ time.Time,
) (int64, bool, error) {
	if f.activateErr != nil {
		return 0, false, f.activateErr
	}
	return id, f.activateChanged, nil
}

func (f *fakeLikes) Deactivate(context.Context, int64, string, int64, time.Time) (int64, bool, error) {
	return 0, f.deactivateChanged, nil
}

func (f *fakeLikes) ActiveStates(_ context.Context, _ int64, _ string, targetIDs []int64) (map[int64]bool, error) {
	result := make(map[int64]bool, len(targetIDs))
	for _, id := range targetIDs {
		result[id] = f.activeStates[id]
	}
	return result, nil
}

func (f *fakeLikes) IsActive(context.Context, int64, string, int64) (bool, error) { return false, nil }

func (f *fakeLikes) LikedPostIDs(context.Context, int64, int) ([]int64, error) { return nil, nil }

// ===== 评论端口 =====

type fakeComments struct {
	app.CommentRepo

	comments  map[int64]model.Comment
	topLevel  []model.Comment
	replies   []model.Comment
	previews  map[int64][]model.Comment
	replyDiff int
	likeCount int
}

func newFakeComments() *fakeComments {
	return &fakeComments{
		comments: map[int64]model.Comment{},
		previews: map[int64][]model.Comment{},
	}
}

func (f *fakeComments) Get(_ context.Context, id int64) (model.Comment, error) {
	if comment, ok := f.comments[id]; ok {
		return comment, nil
	}
	return model.Comment{}, errNotFound
}

func (f *fakeComments) GetForUpdate(ctx context.Context, id int64) (model.Comment, error) {
	return f.Get(ctx, id)
}

func (f *fakeComments) ByIDs(_ context.Context, _ []int64) (map[int64]model.Comment, error) {
	return f.comments, nil
}

func (f *fakeComments) Insert(_ context.Context, comment model.Comment) (model.Comment, error) {
	return comment, nil
}

func (f *fakeComments) ListTopLevel(context.Context, int64, int64, int) ([]model.Comment, error) {
	return f.topLevel, nil
}

func (f *fakeComments) ListReplies(context.Context, int64, int64, int) ([]model.Comment, error) {
	return f.replies, nil
}

func (f *fakeComments) ReplyPreviews(context.Context, []int64, int) (map[int64][]model.Comment, error) {
	return f.previews, nil
}

func (f *fakeComments) AdjustReplyCount(context.Context, int64, int, time.Time) (int, error) {
	f.replyDiff++
	return 1, nil
}

func (f *fakeComments) AdjustLikeCount(context.Context, int64, int, time.Time) (int, error) {
	return f.likeCount, nil
}

func (f *fakeComments) MarkDeleted(context.Context, int64, bool, bool, bool, time.Time) (bool, error) {
	return true, nil
}

func (f *fakeComments) ParentID(context.Context, int64) (*int64, error) { return nil, nil }

// ===== 转发端口 =====

type fakeReposts struct {
	app.RepostRepo

	activateChanged   bool
	deactivateChanged bool
}

func newFakeReposts() *fakeReposts {
	return &fakeReposts{activateChanged: true, deactivateChanged: true}
}

func (f *fakeReposts) Activate(
	_ context.Context, id, _, _ int64, _ bool, _ time.Time,
) (int64, bool, error) {
	return id, f.activateChanged, nil
}

func (f *fakeReposts) Deactivate(context.Context, int64, int64, time.Time) (int64, bool, error) {
	return 0, f.deactivateChanged, nil
}

func (f *fakeReposts) IsActive(context.Context, int64, int64) (bool, error) { return false, nil }

func (f *fakeReposts) ActiveStates(context.Context, int64, []int64) (map[int64]bool, error) {
	return map[int64]bool{}, nil
}

func (f *fakeReposts) ListEvents(context.Context, app.RepostEventQuery) ([]app.RepostEvent, error) {
	return nil, nil
}

// ===== 固定装配 =====

// fixture 汇总全部 fake 与构造好的 handler。
type fixture struct {
	users    *fakeUsers
	posts    *fakePosts
	notices  *fakeNotices
	follows  *fakeFollows
	requests *fakeRequests
	likes    *fakeLikes
	comments *fakeComments
	reposts  *fakeReposts
	tx       app.TxRunner
	ids      app.IDGenerator

	commentLimiter *ratelimit.Limiter
	likeLimiter    *ratelimit.Limiter
	followLimiter  *ratelimit.Limiter
}

func newFixture() *fixture {
	return &fixture{
		users:    newFakeUsers(),
		posts:    newFakePosts(),
		notices:  &fakeNotices{},
		follows:  newFakeFollows(),
		requests: &fakeRequests{},
		likes:    newFakeLikes(),
		comments: newFakeComments(),
		reposts:  newFakeReposts(),
		tx:       passTx{},
		ids:      &seqIDs{next: 100},
	}
}

// deps 组装 app.Deps；unset 字段使用 fake 默认值。
func (f *fixture) deps() app.Deps {
	return app.Deps{
		Clock:    clock.NewFake(testNow),
		IDs:      f.ids,
		Tx:       f.tx,
		Users:    f.users,
		Posts:    f.posts,
		Notices:  f.notices,
		Audit:    quietAudit{},
		Follows:  f.follows,
		Requests: f.requests,
		Likes:    f.likes,
		Comments: f.comments,
		Reposts:  f.reposts,

		CommentLimiter: f.commentLimiter,
		LikeLimiter:    f.likeLimiter,
		FollowLimiter:  f.followLimiter,
	}
}

// handler 按当前 fake 状态构造被测 handler。
func (f *fixture) handler() *Handler { return New(app.New(f.deps()), nil) }

// ===== 请求/响应工具 =====

// newJSONContext 构造带 JSON 请求体的 gin 测试上下文。
func newJSONContext(method, target, body string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, recorder
}

// newTestContext 构造不带请求体的 gin 测试上下文。
func newTestContext(method, target string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, target, nil)
	return c, recorder
}

// withViewer 把已登录身份写入上下文。
func withViewer(c *gin.Context, viewer model.Viewer) { httpx.SetViewer(c, viewer) }

// withParam 写入路径参数。
func withParam(c *gin.Context, name, value string) {
	c.Params = append(c.Params, gin.Param{Key: name, Value: value})
}

// requireStatus 断言响应状态码。
func requireStatus(t *testing.T, recorder *httptest.ResponseRecorder, want int) {
	t.Helper()
	if recorder.Code != want {
		t.Fatalf("状态码 = %d，期望 %d，body=%s", recorder.Code, want, recorder.Body.String())
	}
}

// requireErrorCode 断言统一错误响应中的错误码。
func requireErrorCode(t *testing.T, recorder *httptest.ResponseRecorder, want string) {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析错误响应失败: %v，body=%s", err, recorder.Body.String())
	}
	if body.Error.Code != want {
		t.Fatalf("错误码 = %q，期望 %q，body=%s", body.Error.Code, want, recorder.Body.String())
	}
}

// errorDetails 返回错误响应中的 details 字段。
func errorDetails(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body struct {
		Error struct {
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析错误响应失败: %v，body=%s", err, recorder.Body.String())
	}
	return body.Error.Details
}

// decodeBody 把响应体解析为指定类型。
func decodeBody[T any](t *testing.T, recorder *httptest.ResponseRecorder) T {
	t.Helper()
	var body T
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败: %v，body=%s", err, recorder.Body.String())
	}
	return body
}

// listBody 是统一列表响应结构。
type listBody[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
	HasMore    bool    `json:"has_more"`
}
