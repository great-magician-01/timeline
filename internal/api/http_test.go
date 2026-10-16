package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestCursorRoundTrip(t *testing.T) {
	const id int64 = 9223372036854775806
	encoded := EncodeCursor(id)
	decoded, err := DecodeCursor(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != id {
		t.Fatalf("游标 ID 精度丢失: got=%d want=%d", decoded, id)
	}
	if _, err := DecodeCursor("not-a-cursor"); err == nil {
		t.Fatal("非法游标应被拒绝")
	}
}

func TestDecodeJSONRejectsUnknownAndMultipleValues(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{
		`{"known":"ok","unknown":true}`,
		`{"known":"one"} {"known":"two"}`,
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		var dst struct {
			Known string `json:"known"`
		}
		if err := DecodeJSON(c, &dst); err == nil {
			t.Errorf("应拒绝请求体 %s", body)
		}
	}
}

func TestSnowflakeIDJSONRemainsString(t *testing.T) {
	// 这个用例锁定接口边界：即使超过 JavaScript 安全整数，也只按字符串处理。
	const id int64 = 9007199254740993
	payload, err := json.Marshal(map[string]string{"id": FormatID(id)})
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != `{"id":"9007199254740993"}` {
		t.Fatalf("ID JSON 格式错误: %s", payload)
	}
}
