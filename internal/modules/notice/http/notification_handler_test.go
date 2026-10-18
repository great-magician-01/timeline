package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/modules/notice/app"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

func init() { gin.SetMode(gin.TestMode) }

// testCreatedAt 是固定的通知创建时间，保证 created_at 文本可精确断言。
var testCreatedAt = time.Date(2026, 5, 1, 12, 0, 0, 0, clock.AsiaShanghai)

// ===== 假端口（http 层用例只需覆盖 handler → app 的装配与错误映射） =====

// noticeListCall 记录通知列表的仓储调用参数。
type noticeListCall struct {
	recipientID int64
	filter      string
	noticeType  string
	cursor      int64
	limit       int
}

// markReadCall 记录单条已读的仓储调用参数。
type markReadCall struct {
	recipientID int64
	id          int64
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

	listCalls     []noticeListCall
	unreadCalls   []int64
	markReadCalls []markReadCall
	markAllCalls  []int64
}

// Insert 实现通知写入（HTTP 用例不写通知，保留空实现）。
func (f *fakeNotices) Insert(context.Context, model.Notification) error { return nil }

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

// fakeUsers 只实现列表需要的作者摘要读取。
type fakeUsers struct {
	contracts.UserReader
	briefs     map[int64]model.UserBrief
	briefsErr  error
	briefCalls [][]int64
}

// GetUserBriefs 批量读取作者摘要。
func (f *fakeUsers) GetUserBriefs(_ context.Context, userIDs []int64) (map[int64]model.UserBrief, error) {
	f.briefCalls = append(f.briefCalls, append([]int64(nil), userIDs...))
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

// fakePosts 只实现列表预览需要的帖子批量读取。
type fakePosts struct {
	contracts.ContentService
	posts  map[int64]model.Post
	texts  map[int64]string
	getErr error
}

// GetPosts 批量读取帖子行。
func (f *fakePosts) GetPosts(_ context.Context, postIDs []int64) (map[int64]model.Post, error) {
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

// GetPostPlainTexts 批量读取帖子纯文本投影。
func (f *fakePosts) GetPostPlainTexts(_ context.Context, postIDs []int64) (map[int64]string, error) {
	result := make(map[int64]string, len(postIDs))
	for _, id := range postIDs {
		if text, ok := f.texts[id]; ok {
			result[id] = text
		}
	}
	return result, nil
}

// fakeRelations 只实现关注集合读取。
type fakeRelations struct {
	contracts.RelationReader
	following map[int64]bool
}

// FollowingSet 返回访问者的生效关注集合。
func (f *fakeRelations) FollowingSet(context.Context, int64) (map[int64]bool, error) {
	return f.following, nil
}

// fakeComments 只实现评论批量读取。
type fakeComments struct {
	comments map[int64]model.Comment
}

// GetComments 批量读取评论。
func (f *fakeComments) GetComments(_ context.Context, commentIDs []int64) (map[int64]model.Comment, error) {
	result := make(map[int64]model.Comment, len(commentIDs))
	for _, id := range commentIDs {
		if comment, ok := f.comments[id]; ok {
			result[id] = comment
		}
	}
	return result, nil
}

// ===== 夹具与通用断言 =====

// handlerFixture 汇总一次用例的假端口与已装配的 Handler。
type handlerFixture struct {
	notices  *fakeNotices
	users    *fakeUsers
	posts    *fakePosts
	rels     *fakeRelations
	comments *fakeComments
	handler  *Handler
}

// newHandlerFixture 用假端口构造 notice handler。
func newHandlerFixture() *handlerFixture {
	f := &handlerFixture{
		notices:  &fakeNotices{},
		users:    &fakeUsers{briefs: map[int64]model.UserBrief{}},
		posts:    &fakePosts{posts: map[int64]model.Post{}, texts: map[int64]string{}},
		rels:     &fakeRelations{following: map[int64]bool{}},
		comments: &fakeComments{comments: map[int64]model.Comment{}},
	}
	svc := app.New(app.Deps{
		Users:     f.users,
		Posts:     f.posts,
		Relations: f.rels,
		Comments:  f.comments,
		Notices:   f.notices,
	})
	f.handler = New(svc, nil)
	return f
}

// discardLogger 返回丢弃输出的 logger，避免错误路径用例污染测试输出。
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestContext 构造只有请求与响应记录器的 gin 上下文。
func newTestContext(method, target string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, target, nil)
	return c, recorder
}

// newAuthedContext 构造已写入登录访问者的上下文。
func newAuthedContext(method, target string) (*gin.Context, *httptest.ResponseRecorder) {
	c, recorder := newTestContext(method, target)
	httpx.SetViewer(c, model.Viewer{ID: 9, Authenticated: true})
	return c, recorder
}

// errorCode 解析统一错误响应中的错误码。
func errorCode(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析错误响应失败: %v，body=%s", err, recorder.Body.String())
	}
	return body.Error.Code
}

// errorMessage 解析统一错误响应中的错误文案。
func errorMessage(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析错误响应失败: %v，body=%s", err, recorder.Body.String())
	}
	return body.Error.Message
}

// ===== 列表 =====

// TestListResponseShape 覆盖成功列表的 JSON 形状、分页字段与 DTO 格式化。
func TestListResponseShape(t *testing.T) {
	postID := int64(101)
	f := newHandlerFixture()
	f.notices.rows = []model.Notification{
		{
			ID: 901, RecipientID: 9, ActorID: 7, Type: model.NoticeTypePostLike,
			TargetType: model.NoticeTargetPost, TargetID: postID, PostID: &postID,
			CreatedAt: testCreatedAt,
		},
		{
			ID: 900, RecipientID: 9, ActorID: 8, Type: model.NoticeTypeFollow,
			TargetType: model.NoticeTargetUser, TargetID: 42,
			CreatedAt: testCreatedAt,
		},
	}
	f.users.briefs = map[int64]model.UserBrief{
		7: {ID: 7, Username: "actor7", Nickname: "演员七", AvatarURL: "/uploads/a.png"},
		8: {ID: 8, Username: "actor8", Nickname: "演员八", IsSuspended: true},
	}
	f.posts.posts = map[int64]model.Post{
		postID: {ID: postID, AuthorID: 7, Status: model.PostStatusPublished, Visibility: model.VisibilityPublic},
	}
	f.posts.texts = map[int64]string{postID: "帖子正文"}

	c, recorder := newAuthedContext(http.MethodGet, "/api/v1/notifications?limit=2")
	f.handler.List(c)
	if recorder.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200，body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Items      []NotificationResponse `json:"items"`
		NextCursor *string                `json:"next_cursor"`
		HasMore    bool                   `json:"has_more"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败: %v，body=%s", err, recorder.Body.String())
	}
	if len(body.Items) != 2 {
		t.Fatalf("items 数量 = %d，期望 2", len(body.Items))
	}
	first := body.Items[0]
	if first.ID != "901" || first.Type != model.NoticeTypePostLike ||
		first.TargetType != model.NoticeTargetPost || first.TargetID != "101" {
		t.Fatalf("首条通知字段不符: %+v", first)
	}
	if first.Actor.ID != "7" || first.Actor.Username != "actor7" ||
		first.Actor.Nickname != "演员七" || first.Actor.AvatarURL != "/uploads/a.png" ||
		first.Actor.IsAI || first.Actor.IsSuspended {
		t.Fatalf("首条 Actor 摘要不符: %+v", first.Actor)
	}
	if first.Preview == nil || *first.Preview != "帖子正文" {
		t.Fatalf("首条 preview = %v，期望 帖子正文", first.Preview)
	}
	if first.IsRead {
		t.Fatal("首条通知应为未读")
	}
	if first.CreatedAt != "2026-05-01T12:00:00+08:00" {
		t.Fatalf("created_at = %q，期望北京时间 RFC3339 文本", first.CreatedAt)
	}
	second := body.Items[1]
	if second.ID != "900" || second.Type != model.NoticeTypeFollow ||
		second.TargetType != model.NoticeTargetUser || second.TargetID != "42" {
		t.Fatalf("次条通知字段不符: %+v", second)
	}
	if second.Actor.Username != "actor8" || !second.Actor.IsSuspended {
		t.Fatalf("次条 Actor 摘要不符: %+v", second.Actor)
	}
	if second.Preview != nil {
		t.Fatalf("关注类通知 preview = %q，期望 null", *second.Preview)
	}
	if body.HasMore || body.NextCursor != nil {
		t.Fatalf("末页分页字段不符: has_more=%v next_cursor=%v", body.HasMore, body.NextCursor)
	}
	if len(f.notices.listCalls) != 1 {
		t.Fatalf("仓储调用次数 = %d，期望 1", len(f.notices.listCalls))
	}
	call := f.notices.listCalls[0]
	if call.recipientID != 9 || call.filter != "all" || call.noticeType != "" ||
		call.cursor != 0 || call.limit != 3 {
		t.Fatalf("仓储参数不符: %+v（limit 应为请求值 +1）", call)
	}
}

// TestListHasMoreCursor 覆盖还有更多时分页游标的生成。
func TestListHasMoreCursor(t *testing.T) {
	f := newHandlerFixture()
	f.notices.rows = []model.Notification{
		{ID: 901, RecipientID: 9, ActorID: 7, Type: model.NoticeTypeFollow},
		{ID: 900, RecipientID: 9, ActorID: 7, Type: model.NoticeTypeFollow},
		{ID: 899, RecipientID: 9, ActorID: 7, Type: model.NoticeTypeFollow},
	}
	c, recorder := newAuthedContext(http.MethodGet, "/api/v1/notifications?limit=2")
	f.handler.List(c)
	if recorder.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200，body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Items      []NotificationResponse `json:"items"`
		NextCursor *string                `json:"next_cursor"`
		HasMore    bool                   `json:"has_more"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if len(body.Items) != 2 || !body.HasMore {
		t.Fatalf("分页结果 = %d 条 / has_more=%v，期望 2 条 / true", len(body.Items), body.HasMore)
	}
	wantCursor := httpx.EncodeCursor(900)
	if body.NextCursor == nil || *body.NextCursor != wantCursor {
		t.Fatalf("next_cursor = %v，期望 %q", body.NextCursor, wantCursor)
	}
}

// TestListQueryParamsPassedThrough 覆盖 filter/type/cursor/limit 查询参数解析与透传。
func TestListQueryParamsPassedThrough(t *testing.T) {
	cursor := httpx.EncodeCursor(500)
	f := newHandlerFixture()
	target := "/api/v1/notifications?filter=unread&type=mention&limit=5&cursor=" + cursor
	c, recorder := newAuthedContext(http.MethodGet, target)
	f.handler.List(c)
	if recorder.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200，body=%s", recorder.Code, recorder.Body.String())
	}
	if len(f.notices.listCalls) != 1 {
		t.Fatalf("仓储调用次数 = %d，期望 1", len(f.notices.listCalls))
	}
	call := f.notices.listCalls[0]
	if call.recipientID != 9 || call.filter != "unread" || call.noticeType != "mention" ||
		call.cursor != 500 || call.limit != 6 {
		t.Fatalf("仓储参数不符: %+v", call)
	}
}

// TestListRejectsInvalidParams 覆盖分页参数与筛选项非法时返回 400。
func TestListRejectsInvalidParams(t *testing.T) {
	cases := []struct {
		name        string
		target      string
		wantCode    string
		wantMessage string
	}{
		{
			name:     "limit 为 0",
			target:   "/api/v1/notifications?limit=0",
			wantCode: "invalid_request",
		},
		{
			name:     "limit 超过上限",
			target:   "/api/v1/notifications?limit=51",
			wantCode: "invalid_request",
		},
		{
			name:     "limit 非整数",
			target:   "/api/v1/notifications?limit=abc",
			wantCode: "invalid_request",
		},
		{
			name:     "cursor 非法",
			target:   "/api/v1/notifications?cursor=not-a-cursor",
			wantCode: "invalid_request",
		},
		{
			name:        "filter 非法",
			target:      "/api/v1/notifications?filter=read",
			wantCode:    "invalid_request",
			wantMessage: "filter 只能是 all 或 unread",
		},
		{
			name:        "type 非法",
			target:      "/api/v1/notifications?type=bogus",
			wantCode:    "invalid_request",
			wantMessage: "type 参数无效，只能是合法通知类型",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newHandlerFixture()
			c, recorder := newAuthedContext(http.MethodGet, tc.target)
			f.handler.List(c)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("状态码 = %d，期望 400，body=%s", recorder.Code, recorder.Body.String())
			}
			if code := errorCode(t, recorder); code != tc.wantCode {
				t.Fatalf("错误码 = %q，期望 %q", code, tc.wantCode)
			}
			if tc.wantMessage != "" {
				if message := errorMessage(t, recorder); message != tc.wantMessage {
					t.Fatalf("错误文案 = %q，期望 %q", message, tc.wantMessage)
				}
			}
			if len(f.notices.listCalls) != 0 {
				t.Fatalf("参数校验失败后不应查询仓储，实际 %d 次", len(f.notices.listCalls))
			}
		})
	}
}

// TestListRequiresLogin 覆盖未登录（无 viewer 与游客两种上下文）返回 401。
func TestListRequiresLogin(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*gin.Context)
	}{
		{name: "无 viewer 上下文", setup: func(*gin.Context) {}},
		{
			name: "游客 viewer",
			setup: func(c *gin.Context) {
				httpx.SetViewer(c, model.GuestViewer())
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newHandlerFixture()
			c, recorder := newTestContext(http.MethodGet, "/api/v1/notifications")
			tc.setup(c)
			f.handler.List(c)
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("状态码 = %d，期望 401，body=%s", recorder.Code, recorder.Body.String())
			}
			if code := errorCode(t, recorder); code != "unauthenticated" {
				t.Fatalf("错误码 = %q，期望 unauthenticated", code)
			}
			if len(f.notices.listCalls) != 0 {
				t.Fatalf("未登录时不应查询仓储，实际 %d 次", len(f.notices.listCalls))
			}
		})
	}
}

// TestListInternalError 覆盖无法识别的错误按 500 internal_error 返回。
func TestListInternalError(t *testing.T) {
	f := newHandlerFixture()
	f.notices.listErr = errors.New("数据库故障")
	f.handler = New(f.handler.svc, discardLogger())
	c, recorder := newAuthedContext(http.MethodGet, "/api/v1/notifications")
	f.handler.List(c)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("状态码 = %d，期望 500，body=%s", recorder.Code, recorder.Body.String())
	}
	if code := errorCode(t, recorder); code != "internal_error" {
		t.Fatalf("错误码 = %q，期望 internal_error", code)
	}
	if !strings.Contains(errorMessage(t, recorder), "服务暂时异常") {
		t.Fatalf("错误文案不符: %s", recorder.Body.String())
	}
}

// ===== 未读数 =====

// TestUnreadCount 覆盖未读数成功响应、鉴权与内部错误。
func TestUnreadCount(t *testing.T) {
	t.Run("成功返回未读数", func(t *testing.T) {
		f := newHandlerFixture()
		f.notices.unread = 4
		c, recorder := newAuthedContext(http.MethodGet, "/api/v1/notifications/unread-count")
		f.handler.UnreadCount(c)
		if recorder.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，期望 200，body=%s", recorder.Code, recorder.Body.String())
		}
		var body struct {
			UnreadCount int64 `json:"unread_count"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("解析响应失败: %v", err)
		}
		if body.UnreadCount != 4 {
			t.Fatalf("unread_count = %d，期望 4", body.UnreadCount)
		}
		if len(f.notices.unreadCalls) != 1 || f.notices.unreadCalls[0] != 9 {
			t.Fatalf("仓储入参 = %v，期望 [9]", f.notices.unreadCalls)
		}
	})

	t.Run("未登录返回 401", func(t *testing.T) {
		f := newHandlerFixture()
		c, recorder := newTestContext(http.MethodGet, "/api/v1/notifications/unread-count")
		f.handler.UnreadCount(c)
		if recorder.Code != http.StatusUnauthorized || errorCode(t, recorder) != "unauthenticated" {
			t.Fatalf("状态码/错误码不符: %d/%s", recorder.Code, errorCode(t, recorder))
		}
		if len(f.notices.unreadCalls) != 0 {
			t.Fatalf("未登录时不应查询仓储，实际 %d 次", len(f.notices.unreadCalls))
		}
	})

	t.Run("仓储错误返回 500", func(t *testing.T) {
		f := newHandlerFixture()
		f.notices.unreadErr = errors.New("统计失败")
		f.handler = New(f.handler.svc, discardLogger())
		c, recorder := newAuthedContext(http.MethodGet, "/api/v1/notifications/unread-count")
		f.handler.UnreadCount(c)
		if recorder.Code != http.StatusInternalServerError || errorCode(t, recorder) != "internal_error" {
			t.Fatalf("状态码/错误码不符: %d/%s", recorder.Code, errorCode(t, recorder))
		}
	})
}

// ===== 标记单条已读 =====

// TestMarkRead 覆盖标记单条已读的成功、参数非法、not_found 与鉴权路径。
func TestMarkRead(t *testing.T) {
	t.Run("成功返回已读 ID", func(t *testing.T) {
		f := newHandlerFixture()
		f.notices.markReadOK = true
		c, recorder := newAuthedContext(http.MethodPost, "/api/v1/notifications/42/read")
		c.Params = gin.Params{{Key: "id", Value: "42"}}
		f.handler.MarkRead(c)
		if recorder.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，期望 200，body=%s", recorder.Code, recorder.Body.String())
		}
		var body struct {
			ID     string `json:"id"`
			IsRead bool   `json:"is_read"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("解析响应失败: %v", err)
		}
		if body.ID != "42" || !body.IsRead {
			t.Fatalf("响应 = %+v，期望 id=42 is_read=true", body)
		}
		if len(f.notices.markReadCalls) != 1 {
			t.Fatalf("仓储调用次数 = %d，期望 1", len(f.notices.markReadCalls))
		}
		call := f.notices.markReadCalls[0]
		if call.recipientID != 9 || call.id != 42 {
			t.Fatalf("仓储入参 = %+v，期望 recipient=9 id=42", call)
		}
	})

	t.Run("通知不存在返回 404", func(t *testing.T) {
		f := newHandlerFixture()
		f.notices.markReadOK = false
		c, recorder := newAuthedContext(http.MethodPost, "/api/v1/notifications/42/read")
		c.Params = gin.Params{{Key: "id", Value: "42"}}
		f.handler.MarkRead(c)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("状态码 = %d，期望 404，body=%s", recorder.Code, recorder.Body.String())
		}
		if code := errorCode(t, recorder); code != "not_found" {
			t.Fatalf("错误码 = %q，期望 not_found", code)
		}
		if message := errorMessage(t, recorder); message != "通知不存在" {
			t.Fatalf("错误文案 = %q，期望 通知不存在", message)
		}
	})

	t.Run("路径 ID 非法返回 400", func(t *testing.T) {
		cases := []string{"abc", "0", "-1"}
		for _, raw := range cases {
			t.Run(raw, func(t *testing.T) {
				f := newHandlerFixture()
				c, recorder := newAuthedContext(http.MethodPost, "/api/v1/notifications/"+raw+"/read")
				c.Params = gin.Params{{Key: "id", Value: raw}}
				f.handler.MarkRead(c)
				if recorder.Code != http.StatusBadRequest {
					t.Fatalf("状态码 = %d，期望 400，body=%s", recorder.Code, recorder.Body.String())
				}
				if code := errorCode(t, recorder); code != "invalid_request" {
					t.Fatalf("错误码 = %q，期望 invalid_request", code)
				}
				if len(f.notices.markReadCalls) != 0 {
					t.Fatalf("ID 非法时不应查询仓储，实际 %d 次", len(f.notices.markReadCalls))
				}
			})
		}
	})

	t.Run("未登录返回 401", func(t *testing.T) {
		f := newHandlerFixture()
		c, recorder := newTestContext(http.MethodPost, "/api/v1/notifications/42/read")
		c.Params = gin.Params{{Key: "id", Value: "42"}}
		f.handler.MarkRead(c)
		if recorder.Code != http.StatusUnauthorized || errorCode(t, recorder) != "unauthenticated" {
			t.Fatalf("状态码/错误码不符: %d/%s", recorder.Code, errorCode(t, recorder))
		}
	})
}

// ===== 全部标记已读 =====

// TestMarkAllRead 覆盖全部已读的成功响应、鉴权与内部错误。
func TestMarkAllRead(t *testing.T) {
	t.Run("成功返回更新行数", func(t *testing.T) {
		f := newHandlerFixture()
		f.notices.markAllUpdated = 7
		c, recorder := newAuthedContext(http.MethodPost, "/api/v1/notifications/read-all")
		f.handler.MarkAllRead(c)
		if recorder.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，期望 200，body=%s", recorder.Code, recorder.Body.String())
		}
		var body struct {
			Updated int64 `json:"updated"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("解析响应失败: %v", err)
		}
		if body.Updated != 7 {
			t.Fatalf("updated = %d，期望 7", body.Updated)
		}
		if len(f.notices.markAllCalls) != 1 || f.notices.markAllCalls[0] != 9 {
			t.Fatalf("仓储入参 = %v，期望 [9]", f.notices.markAllCalls)
		}
	})

	t.Run("未登录返回 401", func(t *testing.T) {
		f := newHandlerFixture()
		c, recorder := newTestContext(http.MethodPost, "/api/v1/notifications/read-all")
		f.handler.MarkAllRead(c)
		if recorder.Code != http.StatusUnauthorized || errorCode(t, recorder) != "unauthenticated" {
			t.Fatalf("状态码/错误码不符: %d/%s", recorder.Code, errorCode(t, recorder))
		}
		if len(f.notices.markAllCalls) != 0 {
			t.Fatalf("未登录时不应更新仓储，实际 %d 次", len(f.notices.markAllCalls))
		}
	})

	t.Run("仓储错误返回 500", func(t *testing.T) {
		f := newHandlerFixture()
		f.notices.markAllErr = errors.New("更新失败")
		f.handler = New(f.handler.svc, discardLogger())
		c, recorder := newAuthedContext(http.MethodPost, "/api/v1/notifications/read-all")
		f.handler.MarkAllRead(c)
		if recorder.Code != http.StatusInternalServerError || errorCode(t, recorder) != "internal_error" {
			t.Fatalf("状态码/错误码不符: %d/%s", recorder.Code, errorCode(t, recorder))
		}
	})
}

// 编译期断言：测试替身必须满足 app 端口，避免接口签名漂移后测试静默失效。
var (
	_ app.NotificationRepo = (*fakeNotices)(nil)
	_ app.CommentReader    = (*fakeComments)(nil)
)
