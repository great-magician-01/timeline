package httpx

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
)

func newTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/test", nil)
	return c, recorder
}

func errorBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body struct {
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v body=%s", err, recorder.Body.String())
	}
	return body.Error
}

func TestErrorWritesUnifiedShape(t *testing.T) {
	c, recorder := newTestContext()
	Error(c, http.StatusBadRequest, "invalid_request", "参数无效")

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", recorder.Code)
	}
	body := errorBody(t, recorder)
	if body["code"] != "invalid_request" || body["message"] != "参数无效" {
		t.Fatalf("错误响应形状不符: %#v", body)
	}
	if _, exists := body["details"]; exists {
		t.Fatalf("details 为空时不应出现该字段: %#v", body)
	}
	if !c.IsAborted() {
		t.Fatal("Error 必须中止后续 handler")
	}
}

func TestErrorWithDetails(t *testing.T) {
	c, recorder := newTestContext()
	ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", "请检查", gin.H{"field": "必填"})

	body := errorBody(t, recorder)
	details, ok := body["details"].(map[string]any)
	if !ok || details["field"] != "必填" {
		t.Fatalf("details 丢失: %#v", body)
	}
}

func TestRespondMapsAppErrKinds(t *testing.T) {
	cases := []struct {
		name       string
		err        *apperr.Error
		wantStatus int
		wantCode   string
	}{
		{"not_found", apperr.NotFound("内容不存在"), http.StatusNotFound, "not_found"},
		{"forbidden", apperr.Forbidden("无权访问"), http.StatusForbidden, "forbidden"},
		{"unauthenticated", apperr.Unauthenticated("请先登录"), http.StatusUnauthorized, "unauthenticated"},
		{"invalid", apperr.Invalid("参数无效"), http.StatusBadRequest, "invalid_request"},
		{"validation", apperr.Validation("校验失败"), http.StatusBadRequest, "validation_failed"},
		{"conflict", apperr.Conflict("draft_version_conflict", "草稿版本冲突"), http.StatusConflict, "draft_version_conflict"},
		{"suspended", apperr.Suspended("账号已停用"), http.StatusForbidden, "account_suspended"},
		{"rate_limited", apperr.RateLimited(), http.StatusTooManyRequests, "rate_limited"},
		{"unavailable", apperr.Unavailable("服务尚未就绪"), http.StatusServiceUnavailable, "service_unavailable"},
		{"internal", apperr.Internal("服务暂时异常"), http.StatusInternalServerError, "internal_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, recorder := newTestContext()
			if !Respond(c, tc.err) {
				t.Fatal("Respond 应识别 *apperr.Error")
			}
			if recorder.Code != tc.wantStatus {
				t.Fatalf("status=%d want=%d", recorder.Code, tc.wantStatus)
			}
			if got := errorBody(t, recorder)["code"]; got != tc.wantCode {
				t.Fatalf("code=%v want=%s", got, tc.wantCode)
			}
		})
	}
}

func TestRespondKeepsMessageDetailsAndRetryAfter(t *testing.T) {
	c, recorder := newTestContext()
	err := apperr.RateLimited(1500 * time.Millisecond).WithDetails(gin.H{"scope": "login"})
	if !Respond(c, err) {
		t.Fatal("Respond 应识别限频错误")
	}
	if got := recorder.Header().Get("Retry-After"); got != "2" {
		t.Fatalf("Retry-After=%q want 2（向上取整）", got)
	}
	body := errorBody(t, recorder)
	if body["message"] != "操作过于频繁，请稍后重试" {
		t.Fatalf("message=%v", body["message"])
	}
	if details, ok := body["details"].(map[string]any); !ok || details["scope"] != "login" {
		t.Fatalf("details=%v", body["details"])
	}
}

func TestRespondUnwrapsWrappedAppErr(t *testing.T) {
	c, recorder := newTestContext()
	wrapped := errors.Join(errors.New("db down"), apperr.Unavailable("服务尚未就绪"))
	if !Respond(c, wrapped) {
		t.Fatal("Respond 应通过 errors.As 识别包装的领域错误")
	}
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", recorder.Code)
	}
}

func TestRespondUnknownOrNilReturnsFalse(t *testing.T) {
	c, _ := newTestContext()
	if Respond(c, nil) {
		t.Fatal("nil 不应被视为已处理")
	}
	if Respond(c, errors.New("boom")) {
		t.Fatal("未识别错误应返回 false，由调用方按 500 处理")
	}
}

// legacyFailureShape 模拟已删除的旧 internal/api.Failure 字段形状，
// 仅用于断言新 Respond 不再兼容该形状（防止已删除的兼容层回潮）。
type legacyFailureShape struct {
	Status  int
	Code    string
	Message string
}

func (f *legacyFailureShape) Error() string { return f.Code + ": " + f.Message }

// TestRespondRejectsNonDomainFailureShape 保证已删除的旧 api.Failure 兼容层不会回潮：
// 只识别 *apperr.Error，其它错误形状一律交给调用方按 500 处理。
func TestRespondRejectsNonDomainFailureShape(t *testing.T) {
	c, _ := newTestContext()
	legacy := &legacyFailureShape{
		Status: http.StatusForbidden, Code: "forbidden", Message: "旧形状",
	}
	if Respond(c, legacy) {
		t.Fatal("旧 api.Failure 形状不应再被识别（兼容层已删除）")
	}
}
