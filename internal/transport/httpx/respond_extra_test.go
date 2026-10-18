package httpx

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
)

// extraErrorBody 是统一错误响应的解析结构。
type extraErrorBody struct {
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

// decodeErrorBody 解析响应体中的统一错误结构。
func decodeErrorBody(t *testing.T, raw string) extraErrorBody {
	t.Helper()
	var body extraErrorBody
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("错误响应不是合法 JSON: %v", err)
	}
	return body
}

// TestRespondDefaultMessageFallback 校验领域错误未携带 message 时按 HTTP 状态码兜底中文文案，
// 且默认错误码来自 Kind（未知 Kind 回退 internal_error）。
func TestRespondDefaultMessageFallback(t *testing.T) {
	cases := []struct {
		name        string
		err         error
		wantStatus  int
		wantCode    string
		wantMessage string
	}{
		{"not_found 兜底", &apperr.Error{Kind: apperr.KindNotFound}, http.StatusNotFound, "not_found", "内容不存在或无权访问"},
		{"forbidden 兜底", &apperr.Error{Kind: apperr.KindForbidden}, http.StatusForbidden, "forbidden", "操作不允许"},
		{"unauthenticated 兜底", &apperr.Error{Kind: apperr.KindUnauthenticated}, http.StatusUnauthorized, "unauthenticated", "请先登录"},
		{"validation 兜底", &apperr.Error{Kind: apperr.KindValidation}, http.StatusBadRequest, "validation_failed", "请求参数无效"},
		{"conflict 兜底", &apperr.Error{Kind: apperr.KindConflict}, http.StatusConflict, "conflict", "资源状态冲突"},
		{"rate_limited 兜底", &apperr.Error{Kind: apperr.KindRateLimited}, http.StatusTooManyRequests, "rate_limited", "操作过于频繁，请稍后重试"},
		{"unavailable 兜底", &apperr.Error{Kind: apperr.KindUnavailable}, http.StatusServiceUnavailable, "service_unavailable", "服务尚未就绪，请稍后重试"},
		{"internal 兜底", &apperr.Error{Kind: apperr.KindInternal}, http.StatusInternalServerError, "internal_error", "服务暂时异常，请稍后重试"},
		{"未知 Kind 回退 internal_error", &apperr.Error{Kind: "unknown"}, http.StatusInternalServerError, "internal_error", "服务暂时异常，请稍后重试"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, recorder := newQueryContext("")
			if !Respond(c, tc.err) {
				t.Fatal("领域错误应被 Respond 识别")
			}
			if recorder.Code != tc.wantStatus {
				t.Fatalf("status=%d，期望 %d", recorder.Code, tc.wantStatus)
			}
			body := decodeErrorBody(t, recorder.Body.String())
			if body.Error.Code != tc.wantCode {
				t.Errorf("code=%q，期望 %q", body.Error.Code, tc.wantCode)
			}
			if body.Error.Message != tc.wantMessage {
				t.Errorf("message=%q，期望 %q", body.Error.Message, tc.wantMessage)
			}
		})
	}
}

// TestRespondRetryAfterRoundsUp 校验 Retry-After 秒数向上取整且至少为 1。
func TestRespondRetryAfterRoundsUp(t *testing.T) {
	cases := []struct {
		retryAfter time.Duration
		want       string
	}{
		{1500 * time.Millisecond, "2"},
		{10 * time.Millisecond, "1"},
		{time.Minute, "60"},
	}
	for _, tc := range cases {
		c, recorder := newQueryContext("")
		if !Respond(c, apperr.RateLimited(tc.retryAfter)) {
			t.Fatal("限频错误应被识别")
		}
		if recorder.Code != http.StatusTooManyRequests {
			t.Fatalf("status=%d，期望 429", recorder.Code)
		}
		if got := recorder.Header().Get("Retry-After"); got != tc.want {
			t.Errorf("Retry-After=%q，期望 %q", got, tc.want)
		}
	}
}

// TestDecodeJSONEmptyBody 校验空请求体返回错误（handler 据此返回 400），
// 而单值合法 JSON 正常解码。
func TestDecodeJSONEmptyBody(t *testing.T) {
	c, _ := newQueryContext("")
	c.Request = newRequest(http.MethodPost, "/api/v1/test", "")
	var dst struct{}
	if err := DecodeJSON(c, &dst); err == nil {
		t.Fatal("空请求体应返回错误")
	}
}

// TestListResponseCursorRules 校验列表响应：无更多数据或 lastID 非正数时 next_cursor 为 null。
func TestListResponseCursorRules(t *testing.T) {
	cases := []struct {
		name    string
		lastID  int64
		hasMore bool
		wantNil bool
	}{
		{"没有更多", 5, false, true},
		{"lastID 非正", 0, true, true},
		{"有更多且有 lastID", 5, true, false},
	}
	for _, tc := range cases {
		response := ListResponse([]string{"a"}, tc.lastID, tc.hasMore)
		if tc.wantNil {
			if response["next_cursor"] != nil {
				t.Errorf("%s: next_cursor 应为 nil，实际 %v", tc.name, response["next_cursor"])
			}
			continue
		}
		cursor, ok := response["next_cursor"].(string)
		if !ok || cursor == "" {
			t.Fatalf("%s: next_cursor 应为字符串，实际 %v", tc.name, response["next_cursor"])
		}
		if id, err := DecodeCursor(cursor); err != nil || id != tc.lastID {
			t.Errorf("%s: 游标解码 = %d, %v，期望 %d", tc.name, id, err, tc.lastID)
		}
		if hasMore, _ := response["has_more"].(bool); hasMore != tc.hasMore {
			t.Errorf("%s: has_more=%v，期望 %v", tc.name, hasMore, tc.hasMore)
		}
	}
}

// newRequest 构造指定方法与原始请求体的请求。
func newRequest(method, target, body string) *http.Request {
	request, err := http.NewRequest(method, target, strings.NewReader(body))
	if err != nil {
		panic(err)
	}
	return request
}
