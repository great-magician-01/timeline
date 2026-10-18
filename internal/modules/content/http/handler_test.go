package http

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/modules/content/app"
	"github.com/great-magician-01/timeline/internal/modules/content/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/shared/page"
	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// 说明：本文件补充 content/http 的参数解析、错误映射与 handler 参数校验分支；
// view_test.go 已锁定 DTO JSON 契约，这里不重复断言 DTO 形状。
// handler 在参数校验失败时会在调用 app.Service 前返回，因此用 app.New(app.Deps{}) 构造
// handler 是安全的（不会触发 repo/连接池）。

// newContentHandler 构造用于参数校验测试的 handler（服务不会被触达）。
func newContentHandler() *Handler {
	return New(app.New(app.Deps{}))
}

// newContentContext 构造带请求与响应记录器的 gin.Context。
func newContentContext(
	t *testing.T,
	method, target, body string,
) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, target, reader)
	request.Header.Set("Content-Type", "application/json")
	context.Request = request
	return context, recorder
}

// setContentViewer 写入已登录的人类访问者。
func setContentViewer(context *gin.Context, id int64) {
	httpx.SetViewer(context, model.Viewer{ID: id, Authenticated: true})
}

// contentErrorBody 是统一错误响应体的解析结果。
type contentErrorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
}

// decodeContentError 解析统一错误响应。
func decodeContentError(t *testing.T, recorder *httptest.ResponseRecorder) contentErrorBody {
	t.Helper()
	var payload struct {
		Error contentErrorBody `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析错误响应失败: %v（body=%s）", err, recorder.Body.String())
	}
	return payload.Error
}

// requireContentStatus 断言 HTTP 状态码。
func requireContentStatus(t *testing.T, recorder *httptest.ResponseRecorder, want int) {
	t.Helper()
	if recorder.Code != want {
		t.Fatalf("HTTP 状态码错误: got=%d want=%d body=%s", recorder.Code, want, recorder.Body.String())
	}
}

// TestPathIDHelper 校验路径 ID 解析与错误写回。
func TestPathIDHelper(t *testing.T) {
	context, recorder := newContentContext(t, http.MethodGet, "/", "")
	context.Params = gin.Params{{Key: "id", Value: "42"}}
	id, ok := pathID(context, "id")
	if !ok || id != 42 {
		t.Fatalf("路径 ID 解析错误: id=%d ok=%v", id, ok)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("成功时不应写响应: %s", recorder.Body.String())
	}

	for _, value := range []string{"", "0", "-3", "abc"} {
		context, recorder = newContentContext(t, http.MethodGet, "/", "")
		context.Params = gin.Params{{Key: "id", Value: value}}
		if _, ok := pathID(context, "id"); ok {
			t.Fatalf("路径 ID %q 应解析失败", value)
		}
		requireContentStatus(t, recorder, http.StatusBadRequest)
		if got := decodeContentError(t, recorder); got.Code != "invalid_request" || got.Message != "资源 ID 无效" {
			t.Fatalf("路径 ID 错误响应错误: %+v", got)
		}
	}
}

// TestPageLimitHelper 校验 limit 解析与失败写回。
func TestPageLimitHelper(t *testing.T) {
	context, recorder := newContentContext(t, http.MethodGet, "/", "")
	limit, ok := pageLimit(context)
	if !ok || limit != 20 {
		t.Fatalf("默认 limit 应为 20: %d, %v", limit, ok)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("成功时不应写响应: %s", recorder.Body.String())
	}

	context, recorder = newContentContext(t, http.MethodGet, "/?limit=7", "")
	if limit, ok := pageLimit(context); !ok || limit != 7 {
		t.Fatalf("limit=7 解析错误: %d, %v", limit, ok)
	}

	for _, raw := range []string{"0", "51", "abc", "1.5"} {
		context, recorder = newContentContext(t, http.MethodGet, "/?limit="+raw, "")
		if _, ok := pageLimit(context); ok {
			t.Fatalf("limit=%q 应解析失败", raw)
		}
		requireContentStatus(t, recorder, http.StatusBadRequest)
		if got := decodeContentError(t, recorder); got.Code != "invalid_request" {
			t.Fatalf("limit 错误响应错误: %+v", got)
		}
	}
}

// TestCursorHelpers 校验时间游标与 ID 游标的编解码和失败写回。
func TestCursorHelpers(t *testing.T) {
	at := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	encodedTime := page.EncodeTimeCursor(at, 42)
	context, recorder := newContentContext(t, http.MethodGet, "/?cursor="+encodedTime, "")
	cursor, ok := timeCursor(context)
	if !ok || cursor.ID != 42 || !cursor.Time.Equal(at) {
		t.Fatalf("时间游标解析错误: %+v, %v", cursor, ok)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("成功时不应写响应: %s", recorder.Body.String())
	}

	// 空游标表示首页。
	context, _ = newContentContext(t, http.MethodGet, "/", "")
	if cursor, ok := timeCursor(context); !ok || cursor.Valid() {
		t.Fatalf("空时间游标应为零值: %+v, %v", cursor, ok)
	}

	encodedID := page.EncodeIDCursor(99)
	context, recorder = newContentContext(t, http.MethodGet, "/?cursor="+encodedID, "")
	id, ok := idCursor(context)
	if !ok || id != 99 {
		t.Fatalf("ID 游标解析错误: %d, %v", id, ok)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("成功时不应写响应: %s", recorder.Body.String())
	}
	context, _ = newContentContext(t, http.MethodGet, "/", "")
	if id, ok := idCursor(context); !ok || id != 0 {
		t.Fatalf("空 ID 游标应为 0: %d, %v", id, ok)
	}

	for _, helper := range []func(*gin.Context) bool{
		func(c *gin.Context) bool { _, ok := timeCursor(c); return ok },
		func(c *gin.Context) bool { _, ok := idCursor(c); return ok },
	} {
		context, recorder = newContentContext(t, http.MethodGet, "/?cursor=!!!", "")
		if helper(context) {
			t.Fatal("非法游标应解析失败")
		}
		requireContentStatus(t, recorder, http.StatusBadRequest)
		if got := decodeContentError(t, recorder); got.Message != "无效的游标" {
			t.Fatalf("游标错误文案错误: %+v", got)
		}
	}
}

// TestDecodeBodyHelper 校验请求体解析：拒绝非法 JSON、未知字段与多余值。
func TestDecodeBodyHelper(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
	}
	context, recorder := newContentContext(t, http.MethodPost, "/", `{"name":"帖子"}`)
	var dst payload
	if !decodeBody(context, &dst) {
		t.Fatalf("合法请求体应解析成功: %s", recorder.Body.String())
	}
	if dst.Name != "帖子" {
		t.Fatalf("请求体解析结果错误: %+v", dst)
	}

	for name, body := range map[string]string{
		"空请求体":      "",
		"非法 JSON":   "{",
		"未知字段":      `{"name":"x","unknown":1}`,
		"多余 JSON 值": `{"name":"x"}{"name":"y"}`,
	} {
		t.Run(name, func(t *testing.T) {
			context, recorder := newContentContext(t, http.MethodPost, "/", body)
			var dst payload
			if decodeBody(context, &dst) {
				t.Fatal("非法请求体应解析失败")
			}
			requireContentStatus(t, recorder, http.StatusBadRequest)
			if got := decodeContentError(t, recorder); got.Code != "invalid_request" ||
				got.Message != "请求体必须是有效的 JSON 对象，且不能包含未知字段" {
				t.Fatalf("请求体错误响应错误: %+v", got)
			}
		})
	}
}

// TestCurrentViewerHelper 校验可选身份读取：缺失按游客处理。
func TestCurrentViewerHelper(t *testing.T) {
	context, _ := newContentContext(t, http.MethodGet, "/", "")
	if viewer := currentViewer(context); viewer.Authenticated {
		t.Fatalf("未写入身份时应按游客处理: %+v", viewer)
	}
	setContentViewer(context, 7)
	if viewer := currentViewer(context); !viewer.Authenticated || viewer.ID != 7 {
		t.Fatalf("身份读取错误: %+v", viewer)
	}
}

// TestInvalidFieldHelper 校验字段级校验错误形状。
func TestInvalidFieldHelper(t *testing.T) {
	err := invalidField("attachment_ids", "附件 ID 无效")
	e, ok := apperr.AsKind(err)
	if !ok || e.Kind != apperr.KindValidation || e.Message != "附件 ID 无效" {
		t.Fatalf("字段校验错误错误: %#v", err)
	}
	details, ok := e.Details.(map[string]string)
	if !ok || details["attachment_ids"] != "附件 ID 无效" {
		t.Fatalf("字段详情错误: %#v", e.Details)
	}
}

// TestParseAttachmentAndQuotedHelpers 校验附件/引用 ID 参数转换与错误映射。
func TestParseAttachmentAndQuotedHelpers(t *testing.T) {
	ids, err := parseAttachments([]string{"5", "6", "5"})
	if err != nil || len(ids) != 2 || ids[0] != 5 || ids[1] != 6 {
		t.Fatalf("附件 ID 解析错误: %#v, %v", ids, err)
	}
	if _, err := parseAttachments([]string{"abc"}); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("非法附件 ID 应返回 validation，实际 %v", err)
	}

	if id, err := parseQuotedPostID(nil); err != nil || id != nil {
		t.Fatalf("未填写引用应返回 nil: %#v, %v", id, err)
	}
	raw := "42"
	id, err := parseQuotedPostID(&raw)
	if err != nil || id == nil || *id != 42 {
		t.Fatalf("引用 ID 解析错误: %#v, %v", id, err)
	}
	bad := "abc"
	if _, err := parseQuotedPostID(&bad); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("非法引用 ID 应返回 validation，实际 %v", err)
	}
}

// TestHTTPXRespondHelper 校验参数转换错误直接写回响应。
func TestHTTPXRespondHelper(t *testing.T) {
	context, recorder := newContentContext(t, http.MethodPost, "/", "")
	httpxRespond(context, apperr.Validation("附件 ID 无效"))
	requireContentStatus(t, recorder, http.StatusBadRequest)
	if got := decodeContentError(t, recorder); got.Code != "validation_failed" {
		t.Fatalf("错误码错误: %+v", got)
	}
}

// TestTimeListResponseEdges 校验无有效游标时 has_more 强制为 false。
func TestTimeListResponseEdges(t *testing.T) {
	// has_more=true 但游标为零值时不能输出游标，且 has_more 必须为 false。
	got := timeListResponse([]any{}, page.TimeCursor{}, true)
	if got["has_more"] != false {
		t.Fatalf("零值游标时 has_more 必须为 false: %v", got["has_more"])
	}
	if got["next_cursor"] != nil {
		t.Fatalf("零值游标时 next_cursor 必须为 nil: %v", got["next_cursor"])
	}
}

// TestRespondTimelineBranches 校验时间线响应按页签输出 posts/replies。
func TestRespondTimelineBranches(t *testing.T) {
	handler := newContentHandler()

	context, recorder := newContentContext(t, http.MethodGet, "/", "")
	handler.respondTimeline(context, app.TimelinePage{
		Kind:    domain.TabReplies,
		Replies: []app.CommentTimelineItem{{Kind: "comment", ID: 3, Author: testUserBrief()}},
	})
	requireContentStatus(t, recorder, http.StatusOK)
	var repliesBody struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &repliesBody); err != nil {
		t.Fatalf("解析回复时间线响应失败: %v", err)
	}
	if len(repliesBody.Items) != 1 || repliesBody.Items[0]["id"] != "3" {
		t.Fatalf("回复时间线响应错误: %s", recorder.Body.String())
	}

	context, recorder = newContentContext(t, http.MethodGet, "/", "")
	handler.respondTimeline(context, app.TimelinePage{
		Kind:  domain.TabPosts,
		Posts: []app.PostBrief{{ID: 9, Kind: domain.KindPost, Author: testUserBrief()}},
	})
	requireContentStatus(t, recorder, http.StatusOK)
	var postsBody struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &postsBody); err != nil {
		t.Fatalf("解析帖子时间线响应失败: %v", err)
	}
	if len(postsBody.Items) != 1 || postsBody.Items[0]["id"] != "9" {
		t.Fatalf("帖子时间线响应错误: %s", recorder.Body.String())
	}
}

// TestSuggestTagsEmptyKeyword 校验空关键词直接返回空补全（成功路径不需要数据库）。
func TestSuggestTagsEmptyKeyword(t *testing.T) {
	handler := newContentHandler()
	context, recorder := newContentContext(t, http.MethodGet, "/?q=", "")
	handler.SuggestTags(context)
	requireContentStatus(t, recorder, http.StatusOK)
	var body struct {
		Items []any `json:"items"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析标签补全响应失败: %v", err)
	}
	if body.Items == nil || len(body.Items) != 0 {
		t.Fatalf("空关键词应返回空数组: %s", recorder.Body.String())
	}
}

// TestTagTimelineEmptyName 校验空标签名返回空列表而不是 404。
func TestTagTimelineEmptyName(t *testing.T) {
	handler := newContentHandler()
	context, recorder := newContentContext(t, http.MethodGet, "/", "")
	context.Params = gin.Params{{Key: "name", Value: "  "}}
	handler.TagTimeline(context)
	requireContentStatus(t, recorder, http.StatusOK)
	var body struct {
		Items   []any `json:"items"`
		HasMore bool  `json:"has_more"`
		NextCur any   `json:"next_cursor"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析标签时间线响应失败: %v", err)
	}
	if body.Items == nil || len(body.Items) != 0 || body.HasMore || body.NextCur != nil {
		t.Fatalf("空标签应返回空列表: %s", recorder.Body.String())
	}
}

// TestContentHandlerParameterValidation 覆盖各 handler 在调用 service 之前的参数校验分支。
func TestContentHandlerParameterValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := newContentHandler()

	cases := []struct {
		name    string
		method  string
		target  string
		body    string
		params  gin.Params
		viewer  bool
		handler func(*gin.Context)
		status  int
		code    string
		message string
	}{
		{
			name: "读取帖子 id 非法", method: http.MethodGet, target: "/",
			params:  gin.Params{{Key: "id", Value: "x"}},
			handler: handler.GetPost, status: 400, code: "invalid_request", message: "资源 ID 无效",
		},
		{
			name: "创建帖子未登录", method: http.MethodPost, target: "/", body: `{}`,
			handler: handler.CreatePost, status: 401, code: "unauthenticated", message: "请先登录",
		},
		{
			name: "创建帖子请求体非法", method: http.MethodPost, target: "/", body: `{"unknown":1}`,
			viewer: true, handler: handler.CreatePost, status: 400, code: "invalid_request",
			message: "请求体必须是有效的 JSON 对象，且不能包含未知字段",
		},
		{
			name: "创建帖子附件 ID 非法", method: http.MethodPost, target: "/",
			body:   `{"content_format":"richtext","content_source":"正文","attachment_ids":["abc"]}`,
			viewer: true, handler: handler.CreatePost, status: 400, code: "validation_failed",
			message: "附件 ID 无效",
		},
		{
			name: "创建帖子引用 ID 非法", method: http.MethodPost, target: "/",
			body:   `{"content_format":"richtext","content_source":"正文","quoted_post_id":"abc"}`,
			viewer: true, handler: handler.CreatePost, status: 400, code: "validation_failed",
			message: "引用的帖子 ID 无效",
		},
		{
			name: "编辑帖子未登录", method: http.MethodPost, target: "/", body: `{}`,
			params:  gin.Params{{Key: "id", Value: "1"}},
			handler: handler.UpdatePost, status: 401, code: "unauthenticated", message: "请先登录",
		},
		{
			name: "编辑帖子 id 非法", method: http.MethodPost, target: "/", body: `{}`,
			params:  gin.Params{{Key: "id", Value: "x"}},
			viewer:  true,
			handler: handler.UpdatePost, status: 400, code: "invalid_request", message: "资源 ID 无效",
		},
		{
			name: "发布帖子未登录", method: http.MethodPost, target: "/",
			params:  gin.Params{{Key: "id", Value: "1"}},
			handler: handler.PublishPost, status: 401, code: "unauthenticated", message: "请先登录",
		},
		{
			name: "发布帖子 id 非法", method: http.MethodPost, target: "/",
			params:  gin.Params{{Key: "id", Value: "0"}},
			viewer:  true,
			handler: handler.PublishPost, status: 400, code: "invalid_request", message: "资源 ID 无效",
		},
		{
			name: "设置定时未登录", method: http.MethodPost, target: "/", body: `{}`,
			params:  gin.Params{{Key: "id", Value: "1"}},
			handler: handler.SchedulePost, status: 401, code: "unauthenticated", message: "请先登录",
		},
		{
			name: "设置定时请求体非法", method: http.MethodPost, target: "/", body: `{"unknown":1}`,
			params:  gin.Params{{Key: "id", Value: "1"}},
			viewer:  true,
			handler: handler.SchedulePost, status: 400, code: "invalid_request",
			message: "请求体必须是有效的 JSON 对象，且不能包含未知字段",
		},
		{
			name: "取消定时未登录", method: http.MethodPost, target: "/",
			params:  gin.Params{{Key: "id", Value: "1"}},
			handler: handler.CancelSchedule, status: 401, code: "unauthenticated", message: "请先登录",
		},
		{
			name: "删除帖子未登录", method: http.MethodPost, target: "/",
			params:  gin.Params{{Key: "id", Value: "1"}},
			handler: handler.DeletePost, status: 401, code: "unauthenticated", message: "请先登录",
		},
		{
			name: "历史版本 id 非法", method: http.MethodGet, target: "/",
			params:  gin.Params{{Key: "id", Value: "x"}},
			handler: handler.ListVersions, status: 400, code: "invalid_request", message: "资源 ID 无效",
		},
		{
			name: "历史版本游标非法", method: http.MethodGet, target: "/?cursor=!!!",
			params:  gin.Params{{Key: "id", Value: "1"}},
			handler: handler.ListVersions, status: 400, code: "invalid_request", message: "无效的游标",
		},
		{
			name: "历史版本 limit 非法", method: http.MethodGet, target: "/?limit=0",
			params:  gin.Params{{Key: "id", Value: "1"}},
			handler: handler.ListVersions, status: 400, code: "invalid_request",
			message: "limit 必须是 1-50 的整数",
		},
		{
			name: "版本详情 version_id 非法", method: http.MethodGet, target: "/",
			params:  gin.Params{{Key: "id", Value: "1"}, {Key: "version_id", Value: "x"}},
			handler: handler.GetVersion, status: 400, code: "invalid_request", message: "资源 ID 无效",
		},
		{
			name: "恢复版本未登录", method: http.MethodPost, target: "/",
			params:  gin.Params{{Key: "id", Value: "1"}, {Key: "version_id", Value: "2"}},
			handler: handler.RestoreVersion, status: 401, code: "unauthenticated", message: "请先登录",
		},
		{
			name: "草稿列表未登录", method: http.MethodGet, target: "/",
			handler: handler.ListMyDrafts, status: 401, code: "unauthenticated", message: "请先登录",
		},
		{
			name: "草稿列表游标非法", method: http.MethodGet, target: "/?cursor=!!!",
			viewer: true, handler: handler.ListMyDrafts, status: 400, code: "invalid_request",
			message: "无效的游标",
		},
		{
			name: "定时列表未登录", method: http.MethodGet, target: "/",
			handler: handler.ListMyScheduled, status: 401, code: "unauthenticated", message: "请先登录",
		},
		{
			name: "时间线游标非法", method: http.MethodGet, target: "/?cursor=!!!",
			handler: handler.Timeline, status: 400, code: "invalid_request", message: "无效的游标",
		},
		{
			name: "时间线 filter 非法", method: http.MethodGet, target: "/?filter=bad",
			handler: handler.Timeline, status: 400, code: "invalid_request", message: "filter 参数无效",
		},
		{
			name: "关注时间线未登录", method: http.MethodGet, target: "/",
			handler: handler.FollowingTimeline, status: 401, code: "unauthenticated", message: "请先登录",
		},
		{
			name: "个人主页页签非法", method: http.MethodGet, target: "/?tab=bad",
			params:  gin.Params{{Key: "username", Value: "alice"}},
			handler: handler.UserTimeline, status: 400, code: "invalid_request", message: "tab 参数无效",
		},
		{
			name: "标签时间线游标非法", method: http.MethodGet, target: "/?cursor=!!!",
			params:  gin.Params{{Key: "name", Value: "go"}},
			handler: handler.TagTimeline, status: 400, code: "invalid_request", message: "无效的游标",
		},
		{
			name: "搜索关键词为空", method: http.MethodGet, target: "/?q=",
			handler: handler.SearchPosts, status: 400, code: "validation_failed", message: "请输入搜索关键词",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			context, recorder := newContentContext(t, tc.method, tc.target, tc.body)
			if tc.params != nil {
				context.Params = tc.params
			}
			if tc.viewer {
				setContentViewer(context, 7)
			}
			tc.handler(context)
			requireContentStatus(t, recorder, tc.status)
			body := decodeContentError(t, recorder)
			if body.Code != tc.code || body.Message != tc.message {
				t.Fatalf("错误响应不符:\n got=%+v\nwant code=%s message=%s", body, tc.code, tc.message)
			}
		})
	}
}
