package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func newQueryContext(query string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	target := "/api/v1/test"
	if query != "" {
		target += "?" + query
	}
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	return c, recorder
}

func TestDecodeJSON(t *testing.T) {
	type input struct {
		Name string `json:"name"`
	}

	c, _ := newQueryContext("")
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/test", strings.NewReader(`{"name":"alice"}`))
	var got input
	if err := DecodeJSON(c, &got); err != nil {
		t.Fatalf("DecodeJSON 失败: %v", err)
	}
	if got.Name != "alice" {
		t.Fatalf("name=%q", got.Name)
	}

	c, _ = newQueryContext("")
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/test", strings.NewReader(`{"name":"alice","extra":1}`))
	if err := DecodeJSON(c, &input{}); err == nil {
		t.Fatal("未知字段必须被拒绝")
	}

	c, _ = newQueryContext("")
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/test", strings.NewReader(`{"name":"a"}{"name":"b"}`))
	if err := DecodeJSON(c, &input{}); err == nil {
		t.Fatal("请求体只允许一个 JSON 值")
	}

	tooLarge := `{"name":"` + strings.Repeat("a", MaxJSONBody) + `"}`
	c, _ = newQueryContext("")
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/test", strings.NewReader(tooLarge))
	if err := DecodeJSON(c, &input{}); err == nil {
		t.Fatal("超过 1MiB 的请求体必须被拒绝")
	}
}

func TestParseID(t *testing.T) {
	if id, err := ParseID("123456789"); err != nil || id != 123456789 {
		t.Fatalf("id=%d err=%v", id, err)
	}
	for _, raw := range []string{"", "abc", "0", "-1"} {
		if _, err := ParseID(raw); err == nil {
			t.Fatalf("ParseID(%q) 应返回错误", raw)
		}
	}
}

func TestFormatHelpers(t *testing.T) {
	if got := FormatID(42); got != "42" {
		t.Fatalf("FormatID=%q", got)
	}
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if got := FormatTime(ts); got != "2026-01-02T11:04:05+08:00" {
		t.Fatalf("FormatTime=%q", got)
	}
}

func TestPageLimit(t *testing.T) {
	c, _ := newQueryContext("")
	if limit, err := PageLimit(c); err != nil || limit != 20 {
		t.Fatalf("默认 limit=%d err=%v", limit, err)
	}
	c, _ = newQueryContext("limit=50")
	if limit, err := PageLimit(c); err != nil || limit != 50 {
		t.Fatalf("limit=%d err=%v", limit, err)
	}
	for _, query := range []string{"limit=0", "limit=51", "limit=abc"} {
		c, _ = newQueryContext(query)
		if _, err := PageLimit(c); err == nil {
			t.Fatalf("PageLimit(%q) 应返回错误", query)
		}
	}
}

func TestCursorRoundTrip(t *testing.T) {
	encoded := EncodeCursor(123456789)
	id, err := DecodeCursor(encoded)
	if err != nil || id != 123456789 {
		t.Fatalf("id=%d err=%v", id, err)
	}
	// 旧实现（internal/api）的编码格式必须保持兼容。
	if encoded != "eyJpZCI6MTIzNDU2Nzg5fQ" {
		t.Fatalf("游标编码格式变化: %q", encoded)
	}

	if id, err := DecodeCursor(""); err != nil || id != 0 {
		t.Fatalf("空游标应返回 0: id=%d err=%v", id, err)
	}
	for _, raw := range []string{"not-base64", "eyJpZCI6MH0", "eyJ4IjoxfQ"} {
		if _, err := DecodeCursor(raw); err == nil {
			t.Fatalf("DecodeCursor(%q) 应返回错误", raw)
		}
	}
}

func TestPageCursorReadsQuery(t *testing.T) {
	c, _ := newQueryContext("cursor=" + EncodeCursor(7))
	id, err := PageCursor(c)
	if err != nil || id != 7 {
		t.Fatalf("id=%d err=%v", id, err)
	}
	c, _ = newQueryContext("")
	if id, err := PageCursor(c); err != nil || id != 0 {
		t.Fatalf("无游标: id=%d err=%v", id, err)
	}
}

func TestListResponse(t *testing.T) {
	body := ListResponse([]int{1, 2}, 2, true)
	if body["has_more"] != true {
		t.Fatalf("has_more=%v", body["has_more"])
	}
	cursor, ok := body["next_cursor"].(string)
	if !ok {
		t.Fatalf("next_cursor 类型=%T", body["next_cursor"])
	}
	if id, err := DecodeCursor(cursor); err != nil || id != 2 {
		t.Fatalf("next_cursor 解码失败: id=%d err=%v", id, err)
	}

	body = ListResponse([]int{}, 0, false)
	if body["next_cursor"] != nil {
		t.Fatalf("无更多数据时 next_cursor 应为 nil: %v", body["next_cursor"])
	}
}
