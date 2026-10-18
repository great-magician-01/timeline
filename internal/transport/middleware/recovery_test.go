package middleware

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"

	"github.com/gin-gonic/gin"
)

// newPanicEngine 构造带 RequestID + Recovery 的测试引擎，返回日志缓冲区。
func newPanicEngine(handler gin.HandlerFunc) (*gin.Engine, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, nil))
	engine := gin.New()
	engine.Use(RequestID(), Recovery(logger))
	engine.GET("/panic", handler)
	return engine, buf
}

// doPanicRequest 发起一次 GET /panic 请求。
func doPanicRequest(t *testing.T, engine *gin.Engine) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/panic", nil))
	return recorder
}

// TestRecoveryLogsPanicWithStackAndReturnsInternalError 覆盖 panic 的完整链路：
// 记录含 panic 值、完整调用栈与请求标识的 ERROR 日志，并返回统一 500 JSON。
func TestRecoveryLogsPanicWithStackAndReturnsInternalError(t *testing.T) {
	engine, buf := newPanicEngine(func(*gin.Context) { panic("处理器崩溃") })
	recorder := doPanicRequest(t, engine)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("panic 应返回 500，实际 %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "internal_error") {
		t.Fatalf("panic 应返回统一 JSON 错误: %s", recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "处理器崩溃") {
		t.Fatalf("panic 细节不能返回客户端: %s", recorder.Body.String())
	}

	entry := lastLogEntry(t, buf)
	if entry["level"] != "ERROR" || entry["msg"] != "HTTP 请求处理 panic" {
		t.Fatalf("panic 日志级别/消息错误: %#v", entry)
	}
	if entry["panic"] != "处理器崩溃" {
		t.Fatalf("panic 日志应记录 panic 值: %#v", entry)
	}
	stack, _ := entry["stack"].(string)
	if !strings.Contains(stack, "goroutine") || !strings.Contains(stack, "TestRecoveryLogsPanicWithStackAndReturnsInternalError") {
		t.Fatalf("panic 日志应包含完整调用栈: %q", stack)
	}
	requestID := recorder.Header().Get(HeaderRequestID)
	if requestID == "" {
		t.Fatal("请求应携带 X-Request-ID 响应头")
	}
	if entry["request_id"] != requestID {
		t.Fatalf("panic 日志 request_id 应与响应头一致: %#v", entry)
	}
	if entry["path"] != "/panic" || entry["method"] != http.MethodGet {
		t.Fatalf("panic 日志缺少请求信息: %#v", entry)
	}
}

// TestRecoveryKeepsAlreadyWrittenResponse 覆盖响应已写出后 panic：不重复写状态码，
// 仍记录带栈错误日志。
func TestRecoveryKeepsAlreadyWrittenResponse(t *testing.T) {
	engine, buf := newPanicEngine(func(c *gin.Context) {
		c.String(http.StatusOK, "已写出")
		panic("写出后崩溃")
	})
	recorder := doPanicRequest(t, engine)

	if recorder.Code != http.StatusOK || recorder.Body.String() != "已写出" {
		t.Fatalf("已写出的响应不应被覆盖，status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	entry := lastLogEntry(t, buf)
	if entry["level"] != "ERROR" {
		t.Fatalf("写出后 panic 仍应记录 ERROR 日志: %#v", entry)
	}
	if stack, _ := entry["stack"].(string); !strings.Contains(stack, "TestRecoveryKeepsAlreadyWrittenResponse") {
		t.Fatalf("写出后 panic 仍应记录完整栈: %q", stack)
	}
}

// TestRecoveryBrokenConnectionSkipsStack 覆盖客户端断开（EPIPE）：
// 按 WARN 记录摘要、不打印栈、不尝试写 500。
func TestRecoveryBrokenConnectionSkipsStack(t *testing.T) {
	engine, buf := newPanicEngine(func(*gin.Context) { panic(syscall.EPIPE) })
	recorder := doPanicRequest(t, engine)

	entry := lastLogEntry(t, buf)
	if entry["level"] != "WARN" || entry["msg"] != "HTTP 请求处理中断（客户端连接已断开）" {
		t.Fatalf("连接断开应按 WARN 记录摘要: %#v", entry)
	}
	if _, exists := entry["stack"]; exists {
		t.Fatalf("连接断开不应记录调用栈: %#v", entry)
	}
	if recorder.Code != http.StatusOK || recorder.Body.Len() != 0 {
		t.Fatalf("连接断开不应写响应，status=%d body=%q", recorder.Code, recorder.Body.String())
	}
}

// TestRecoveryErrAbortHandlerIsNotAnError 覆盖 http.ErrAbortHandler：
// 按连接中断处理，不写响应、不记录栈。
func TestRecoveryErrAbortHandlerIsNotAnError(t *testing.T) {
	engine, buf := newPanicEngine(func(*gin.Context) { panic(http.ErrAbortHandler) })
	recorder := doPanicRequest(t, engine)

	entry := lastLogEntry(t, buf)
	if entry["level"] != "WARN" {
		t.Fatalf("ErrAbortHandler 应按 WARN 处理: %#v", entry)
	}
	if _, exists := entry["stack"]; exists {
		t.Fatalf("ErrAbortHandler 不应记录调用栈: %#v", entry)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("ErrAbortHandler 不应写响应: %q", recorder.Body.String())
	}
}

// TestFormatPanicValueTruncates 覆盖异常大的 panic 值写入日志前按 rune 截断。
func TestFormatPanicValueTruncates(t *testing.T) {
	long := strings.Repeat("错", maxPanicValueRunes+10)
	got := formatPanicValue(long)
	if !strings.HasSuffix(got, "…（已截断）") {
		t.Fatalf("超长 panic 值应带截断标记，实际长度 %d", len(got))
	}
	if want := maxPanicValueRunes + len([]rune("…（已截断）")); len([]rune(got)) != want {
		t.Fatalf("超长 panic 值应截断到 %d 个 rune，实际 %d", want, len([]rune(got)))
	}
	if short := formatPanicValue("普通值"); short != "普通值" {
		t.Fatalf("未超长 panic 值不应改变，实际 %q", short)
	}
}

// TestRecoveryNilLoggerFallback 覆盖未注入 logger 时回退 slog.Default，不因 nil 崩溃。
func TestRecoveryNilLoggerFallback(t *testing.T) {
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer slog.SetDefault(original)

	engine := gin.New()
	engine.Use(Recovery(nil))
	engine.GET("/panic", func(*gin.Context) { panic("默认 logger") })
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/panic", nil))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("nil logger 时仍应恢复并返回 500，实际 %d", recorder.Code)
	}
}
