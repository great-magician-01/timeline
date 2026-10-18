package http

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// 说明：handler.go 的 writeError/adminID/decode/pageParams/pathID/listResponse 是
// HTTP 层的通用参数解析与错误映射入口；handler 校验失败时会在调用 service 之前返回，
// 因此可以用 New(nil) 安全测试这些分支（service 为 nil 也不会被触达）。

// newGinContext 构造带请求与空响应记录器的 gin.Context。
func newGinContext(t *testing.T, method, target, body string) (*gin.Context, *httptest.ResponseRecorder) {
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

// adminViewer 在上下文写入管理员身份。
func adminViewer(context *gin.Context, id int64) {
	httpx.SetViewer(context, model.Viewer{ID: id, IsAdmin: true, Authenticated: true})
}

// errorResponse 是统一错误响应体的解析结果。
type errorResponse struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
}

// decodeError 解析统一错误响应。
func decodeError(t *testing.T, recorder *httptest.ResponseRecorder) errorResponse {
	t.Helper()
	var payload struct {
		Error errorResponse `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析错误响应失败: %v（body=%s）", err, recorder.Body.String())
	}
	return payload.Error
}

// requireStatus 断言 HTTP 状态码。
func requireStatus(t *testing.T, recorder *httptest.ResponseRecorder, want int) {
	t.Helper()
	if recorder.Code != want {
		t.Fatalf("HTTP 状态码错误: got=%d want=%d body=%s", recorder.Code, want, recorder.Body.String())
	}
}

// TestWriteError 校验领域错误映射与未知错误兜底 500。
func TestWriteError(t *testing.T) {
	handler := New(nil)
	context, recorder := newGinContext(t, http.MethodGet, "/api/v1/admin/ai", "")
	handler.writeError(context, apperr.Validation("昵称需为 1-50 个字符").
		WithDetails(map[string]string{"nickname": "昵称需为 1-50 个字符"}))
	requireStatus(t, recorder, http.StatusBadRequest)
	body := decodeError(t, recorder)
	if body.Code != "validation_failed" || body.Message != "昵称需为 1-50 个字符" {
		t.Fatalf("校验错误映射错误: %+v", body)
	}
	if body.Details["nickname"] != "昵称需为 1-50 个字符" {
		t.Fatalf("校验详情映射错误: %+v", body.Details)
	}

	context, recorder = newGinContext(t, http.MethodGet, "/api/v1/admin/ai", "")
	handler.writeError(context, apperr.RateLimited(0))
	requireStatus(t, recorder, http.StatusTooManyRequests)
	if body := decodeError(t, recorder); body.Code != "rate_limited" {
		t.Fatalf("限频错误映射错误: %+v", body)
	}

	context, recorder = newGinContext(t, http.MethodGet, "/api/v1/admin/ai", "")
	handler.writeError(context, io.ErrUnexpectedEOF)
	requireStatus(t, recorder, http.StatusInternalServerError)
	body = decodeError(t, recorder)
	if body.Code != "internal_error" || body.Message != "服务暂时异常，请稍后重试" {
		t.Fatalf("未知错误应兜底为 500 internal_error: %+v", body)
	}

	// nil 错误按未知错误处理（500），不应 panic。
	context, recorder = newGinContext(t, http.MethodGet, "/api/v1/admin/ai", "")
	handler.writeError(context, nil)
	requireStatus(t, recorder, http.StatusInternalServerError)
}

// TestAdminID 校验管理员 ID 读取：缺失写回 401，存在返回 ID。
func TestAdminID(t *testing.T) {
	handler := New(nil)
	context, recorder := newGinContext(t, http.MethodPost, "/api/v1/admin/ai", "")
	if _, ok := handler.adminID(context); ok {
		t.Fatal("未登录时应返回 false")
	}
	requireStatus(t, recorder, http.StatusUnauthorized)
	if body := decodeError(t, recorder); body.Code != "unauthenticated" {
		t.Fatalf("未登录错误码错误: %+v", body)
	}

	context, recorder = newGinContext(t, http.MethodPost, "/api/v1/admin/ai", "")
	adminViewer(context, 7)
	id, ok := handler.adminID(context)
	if !ok || id != 7 {
		t.Fatalf("管理员 ID 读取错误: id=%d ok=%v", id, ok)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("成功时不应写响应: %s", recorder.Body.String())
	}
}

// TestDecode 校验请求体解析：拒绝未知字段、非法 JSON 与多余 JSON 值。
func TestDecode(t *testing.T) {
	handler := New(nil)
	type payload struct {
		Name string `json:"name"`
	}

	context, recorder := newGinContext(t, http.MethodPost, "/", `{"name":"预设"}`)
	var dst payload
	if !handler.decode(context, &dst) {
		t.Fatalf("合法请求体应解析成功: %s", recorder.Body.String())
	}
	if dst.Name != "预设" {
		t.Fatalf("解析结果错误: %+v", dst)
	}

	bodies := map[string]string{
		"非法 JSON":   `{`,
		"空请求体":      ``,
		"未知字段":      `{"name":"x","unknown":1}`,
		"多余 JSON 值": `{"name":"x"}{"name":"y"}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			context, recorder := newGinContext(t, http.MethodPost, "/", body)
			var dst payload
			if handler.decode(context, &dst) {
				t.Fatal("非法请求体应解析失败")
			}
			requireStatus(t, recorder, http.StatusBadRequest)
			if got := decodeError(t, recorder); got.Code != "invalid_request" {
				t.Fatalf("请求体错误码错误: %+v", got)
			}
		})
	}
}

// TestPageParams 校验通用分页参数解析（默认值、上限与游标失败）。
func TestPageParams(t *testing.T) {
	handler := New(nil)
	context, recorder := newGinContext(t, http.MethodGet, "/?limit=5", "")
	limit, cursor, ok := handler.pageParams(context)
	if !ok || limit != 5 || cursor != 0 {
		t.Fatalf("分页参数解析错误: limit=%d cursor=%d ok=%v", limit, cursor, ok)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("成功时不应写响应: %s", recorder.Body.String())
	}

	context, recorder = newGinContext(t, http.MethodGet, "/", "")
	if limit, cursor, ok = handler.pageParams(context); !ok || limit != 20 || cursor != 0 {
		t.Fatalf("默认分页参数错误: limit=%d cursor=%d ok=%v", limit, cursor, ok)
	}

	context, recorder = newGinContext(t, http.MethodGet, "/?limit=51", "")
	if _, _, ok := handler.pageParams(context); ok {
		t.Fatal("limit=51 应解析失败")
	}
	requireStatus(t, recorder, http.StatusBadRequest)
	if got := decodeError(t, recorder); got.Code != "invalid_request" ||
		got.Message != "limit 必须是 1-50 的整数" {
		t.Fatalf("limit 错误文案错误: %+v", got)
	}

	context, recorder = newGinContext(t, http.MethodGet, "/?cursor=!!!", "")
	if _, _, ok := handler.pageParams(context); ok {
		t.Fatal("非法游标应解析失败")
	}
	requireStatus(t, recorder, http.StatusBadRequest)
}

// TestPathID 校验路径雪花 ID 解析。
func TestPathID(t *testing.T) {
	handler := New(nil)
	context, recorder := newGinContext(t, http.MethodGet, "/", "")
	context.Params = gin.Params{{Key: "id", Value: "42"}}
	id, ok := handler.pathID(context, "id", "无效的 AI ID")
	if !ok || id != 42 {
		t.Fatalf("路径 ID 解析错误: id=%d ok=%v", id, ok)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("成功时不应写响应: %s", recorder.Body.String())
	}

	for _, value := range []string{"", "0", "-1", "abc", "1.2"} {
		context, recorder := newGinContext(t, http.MethodGet, "/", "")
		context.Params = gin.Params{{Key: "id", Value: value}}
		if _, ok := handler.pathID(context, "id", "无效的 AI ID"); ok {
			t.Fatalf("路径 ID %q 应解析失败", value)
		}
		requireStatus(t, recorder, http.StatusBadRequest)
		if got := decodeError(t, recorder); got.Message != "无效的 AI ID" {
			t.Fatalf("路径 ID 错误文案错误: %+v", got)
		}
	}
}

// TestListResponse 校验统一游标列表响应形状。
func TestListResponse(t *testing.T) {
	empty := listResponse([]any{}, 0, false)
	if empty["has_more"] != false {
		t.Fatalf("空页 has_more 必须为 false: %v", empty["has_more"])
	}
	if value, ok := empty["next_cursor"]; !ok || value != nil {
		t.Fatalf("无下一页时 next_cursor 必须为 nil: %v", empty["next_cursor"])
	}
	if value, ok := empty["has_more"]; !ok || value != false {
		t.Fatalf("has_more 字段缺失: %v", empty)
	}

	full := listResponse([]any{"x"}, 123, true)
	if full["has_more"] != true {
		t.Fatalf("有下一页时 has_more 必须为 true: %v", full["has_more"])
	}
	cursor, ok := full["next_cursor"].(string)
	if !ok || cursor == "" {
		t.Fatalf("应有下一页游标: %v", full["next_cursor"])
	}
	decoded, err := httpx.DecodeCursor(cursor)
	if err != nil || decoded != 123 {
		t.Fatalf("下一页游标不可解析: %v, %v", decoded, err)
	}

	// lastID 为 0 时即使 hasMore 也不输出游标。
	if value := listResponse([]any{}, 0, true); value["next_cursor"] != nil {
		t.Fatalf("lastID=0 时不应输出游标: %v", value["next_cursor"])
	}
}

// TestBehaviorInputDTOPatch 校验行为补丁 DTO 到领域补丁的映射。
func TestBehaviorInputDTOPatch(t *testing.T) {
	in := behaviorInputDTO{
		AllowOriginalPost:    boolPtrValue(true),
		AllowActiveComment:   boolPtrValue(true),
		AllowPassiveReply:    boolPtrValue(true),
		AllowLike:            boolPtrValue(true),
		AllowFollow:          boolPtrValue(true),
		AllowUnfollow:        boolPtrValue(true),
		AllowRepost:          boolPtrValue(true),
		AllowQuote:           boolPtrValue(true),
		AllowAIToAI:          boolPtrValue(true),
		AllowWebSearch:       boolPtrValue(true),
		CandidateScopeType:   strPtrValue("tags"),
		CandidateScopeParams: json.RawMessage(`{"tags":["go"]}`),
		RateLimits:           json.RawMessage(`{"daily_like":5}`),
		CooldownSeconds:      intPtrValue(60),
		ActiveHoursStart:     intPtrValue(8),
		ActiveHoursEnd:       intPtrValue(23),
		Schedule:             json.RawMessage(`{}`),
		MaxReplyDepth:        intPtrValue(2),
	}
	patch := in.patch()
	if patch.AllowOriginalPost == nil || !*patch.AllowOriginalPost ||
		patch.AllowWebSearch == nil || !*patch.AllowWebSearch ||
		patch.AllowAIToAI == nil || !*patch.AllowAIToAI {
		t.Fatalf("布尔开关映射错误: %+v", patch)
	}
	if patch.CandidateScopeType == nil || *patch.CandidateScopeType != "tags" ||
		string(patch.CandidateScopeParams) != `{"tags":["go"]}` ||
		string(patch.RateLimits) != `{"daily_like":5}` ||
		string(patch.Schedule) != "{}" {
		t.Fatalf("JSONB 字段映射错误: %+v", patch)
	}
	if patch.CooldownSeconds == nil || *patch.CooldownSeconds != 60 ||
		patch.ActiveHoursStart == nil || *patch.ActiveHoursStart != 8 ||
		patch.ActiveHoursEnd == nil || *patch.ActiveHoursEnd != 23 ||
		patch.MaxReplyDepth == nil || *patch.MaxReplyDepth != 2 {
		t.Fatalf("数值字段映射错误: %+v", patch)
	}

	// 空请求体应映射为零值补丁（全部 nil，不改变既有配置）。
	empty := behaviorInputDTO{}.patch()
	if empty.AllowLike != nil || empty.CandidateScopeType != nil || empty.CooldownSeconds != nil ||
		empty.CandidateScopeParams != nil || empty.RateLimits != nil || empty.Schedule != nil {
		t.Fatalf("空补丁应为零值: %+v", empty)
	}
}

// 指针构造辅助（避免与 app 包测试辅助重名）。
func boolPtrValue(value bool) *bool { return &value }

func strPtrValue(value string) *string { return &value }

func intPtrValue(value int) *int { return &value }

// TestHandlerParameterValidation 覆盖各 handler 在调用 service 之前的参数校验分支。
func TestHandlerParameterValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := New(nil)
	tooLongQuery := strings.Repeat("长", 101)

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
			name: "预设列表 limit 非法", method: http.MethodGet, target: "/?limit=0",
			handler: handler.ListProviders, status: 400, code: "invalid_request",
			message: "limit 必须是 1-50 的整数",
		},
		{
			name: "预设列表 status 非法", method: http.MethodGet, target: "/?status=bad",
			handler: handler.ListProviders, status: 400, code: "invalid_request",
			message: "status 只能是 enabled 或 disabled",
		},
		{
			name: "预设列表搜索词过长", method: http.MethodGet, target: "/?q=" + tooLongQuery,
			handler: handler.ListProviders, status: 400, code: "invalid_request",
			message: "搜索关键词过长",
		},
		{
			name: "AI 列表 status 非法", method: http.MethodGet, target: "/?status=bad",
			handler: handler.ListAIs, status: 400, code: "invalid_request",
			message: "status 只能是 enabled 或 disabled",
		},
		{
			name: "AI 列表搜索词过长", method: http.MethodGet, target: "/?q=" + tooLongQuery,
			handler: handler.ListAIs, status: 400, code: "invalid_request",
			message: "搜索关键词过长",
		},
		{
			name: "记忆列表 id 非法", method: http.MethodGet, target: "/",
			params:  gin.Params{{Key: "id", Value: "x"}},
			handler: handler.ListMemories, status: 400, code: "invalid_request",
			message: "无效的 AI ID",
		},
		{
			name: "记忆列表 limit 非法", method: http.MethodGet, target: "/?limit=99",
			params:  gin.Params{{Key: "id", Value: "5"}},
			handler: handler.ListMemories, status: 400, code: "invalid_request",
			message: "limit 必须是 1-50 的整数",
		},
		{
			name: "获取 AI id 非法", method: http.MethodGet, target: "/",
			params:  gin.Params{{Key: "id", Value: "x"}},
			handler: handler.GetAI, status: 400, code: "invalid_request",
			message: "无效的 AI ID",
		},
		{
			name: "创建预设未登录", method: http.MethodPost, target: "/", body: `{}`,
			handler: handler.CreateProvider, status: 401, code: "unauthenticated",
			message: "请先登录",
		},
		{
			name: "创建预设请求体非法", method: http.MethodPost, target: "/", body: `{"unknown":1}`,
			viewer: true, handler: handler.CreateProvider, status: 400, code: "invalid_request",
			message: "请求体必须是有效的 JSON 对象，且不能包含未知字段",
		},
		{
			name: "更新预设 id 非法", method: http.MethodPost, target: "/", body: `{}`,
			params: gin.Params{{Key: "id", Value: "x"}},
			viewer: true, handler: handler.UpdateProvider, status: 400, code: "invalid_request",
			message: "无效的模型接口预设 ID",
		},
		{
			name: "删除预设 id 非法", method: http.MethodPost, target: "/",
			params: gin.Params{{Key: "id", Value: "-1"}},
			viewer: true, handler: handler.DeleteProvider, status: 400, code: "invalid_request",
			message: "无效的模型接口预设 ID",
		},
		{
			name: "测试预设 id 非法", method: http.MethodPost, target: "/",
			params: gin.Params{{Key: "id", Value: "0"}},
			viewer: true, handler: handler.TestProvider, status: 400, code: "invalid_request",
			message: "无效的模型接口预设 ID",
		},
		{
			name: "创建 AI 请求体非法", method: http.MethodPost, target: "/", body: `{"unknown":1}`,
			viewer: true, handler: handler.CreateAI, status: 400, code: "invalid_request",
			message: "请求体必须是有效的 JSON 对象，且不能包含未知字段",
		},
		{
			name: "更新 AI id 非法", method: http.MethodPost, target: "/", body: `{}`,
			params: gin.Params{{Key: "id", Value: "x"}},
			viewer: true, handler: handler.UpdateAI, status: 400, code: "invalid_request",
			message: "无效的 AI ID",
		},
		{
			name: "更新模型 id 非法", method: http.MethodPost, target: "/", body: `{}`,
			params: gin.Params{{Key: "id", Value: "x"}},
			viewer: true, handler: handler.UpdateAIModel, status: 400, code: "invalid_request",
			message: "无效的 AI ID",
		},
		{
			name: "更新行为 id 非法", method: http.MethodPost, target: "/", body: `{}`,
			params: gin.Params{{Key: "id", Value: "x"}},
			viewer: true, handler: handler.UpdateAIBehavior, status: 400, code: "invalid_request",
			message: "无效的 AI ID",
		},
		{
			name: "更新行为请求体非法", method: http.MethodPost, target: "/", body: `{"unknown":1}`,
			params: gin.Params{{Key: "id", Value: "5"}},
			viewer: true, handler: handler.UpdateAIBehavior, status: 400, code: "invalid_request",
			message: "请求体必须是有效的 JSON 对象，且不能包含未知字段",
		},
		{
			name: "启用 AI id 非法", method: http.MethodPost, target: "/",
			params: gin.Params{{Key: "id", Value: "x"}},
			viewer: true, handler: handler.EnableAI, status: 400, code: "invalid_request",
			message: "无效的 AI ID",
		},
		{
			name: "停用 AI id 非法", method: http.MethodPost, target: "/",
			params: gin.Params{{Key: "id", Value: "0"}},
			viewer: true, handler: handler.DisableAI, status: 400, code: "invalid_request",
			message: "无效的 AI ID",
		},
		{
			name: "清空记忆 id 非法", method: http.MethodPost, target: "/", body: `{"type":"recent"}`,
			params: gin.Params{{Key: "id", Value: "x"}},
			viewer: true, handler: handler.ClearMemories, status: 400, code: "invalid_request",
			message: "无效的 AI ID",
		},
		{
			name: "编辑记忆 memory_id 非法", method: http.MethodPost, target: "/", body: `{}`,
			params:  gin.Params{{Key: "id", Value: "5"}, {Key: "memory_id", Value: "x"}},
			viewer:  true,
			handler: handler.UpdateMemory, status: 400, code: "invalid_request",
			message: "无效的记忆 ID",
		},
		{
			name: "删除记忆 memory_id 非法", method: http.MethodPost, target: "/",
			params:  gin.Params{{Key: "id", Value: "5"}, {Key: "memory_id", Value: "-2"}},
			viewer:  true,
			handler: handler.DeleteMemory, status: 400, code: "invalid_request",
			message: "无效的记忆 ID",
		},
		{
			name: "创建记忆 id 非法", method: http.MethodPost, target: "/", body: `{}`,
			params: gin.Params{{Key: "id", Value: "x"}},
			viewer: true, handler: handler.CreateMemory, status: 400, code: "invalid_request",
			message: "无效的 AI ID",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			context, recorder := newGinContext(t, tc.method, tc.target, tc.body)
			if tc.params != nil {
				context.Params = tc.params
			}
			if tc.viewer {
				adminViewer(context, 7)
			}
			tc.handler(context)
			requireStatus(t, recorder, tc.status)
			body := decodeError(t, recorder)
			if body.Code != tc.code || body.Message != tc.message {
				t.Fatalf("错误响应不符:\n got=%+v\nwant code=%s message=%s", body, tc.code, tc.message)
			}
		})
	}
}
