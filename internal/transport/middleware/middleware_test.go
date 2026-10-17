package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// fakeReadiness 用于就绪中间件测试。
type fakeReadiness struct{ ready bool }

func (f fakeReadiness) Ready() bool { return f.ready }

func TestRequireReady(t *testing.T) {
	engine := gin.New()
	engine.Use(RequireReady(fakeReadiness{ready: false}))
	engine.GET("/api/v1/test", func(c *gin.Context) { c.Status(http.StatusOK) })

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/test", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("未就绪 status=%d", recorder.Code)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.Error.Code != "service_unavailable" {
		t.Fatalf("未就绪错误响应不符: %s err=%v", recorder.Body.String(), err)
	}

	readyEngine := gin.New()
	readyEngine.Use(RequireReady(fakeReadiness{ready: true}))
	readyEngine.GET("/api/v1/test", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	recorder = httptest.NewRecorder()
	readyEngine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/test", nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("就绪后 status=%d", recorder.Code)
	}
}

func TestRequireReadyNilReadiness(t *testing.T) {
	engine := gin.New()
	engine.Use(RequireReady(nil))
	engine.GET("/api/v1/test", func(c *gin.Context) { c.Status(http.StatusOK) })

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/test", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil readiness status=%d", recorder.Code)
	}
}

func TestSecurityHeaders(t *testing.T) {
	engine := gin.New()
	engine.Use(SecurityHeaders())
	engine.GET("/api/v1/test", func(c *gin.Context) { c.Status(http.StatusOK) })
	engine.GET("/index.html", func(c *gin.Context) { c.Status(http.StatusOK) })

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/test", nil))
	if got := recorder.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("nosniff=%q", got)
	}
	if got := recorder.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("frame=%q", got)
	}
	if got := recorder.Header().Get("Referrer-Policy"); got != "same-origin" {
		t.Fatalf("referrer=%q", got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("api cache-control=%q", got)
	}

	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/index.html", nil))
	if got := recorder.Header().Get("Cache-Control"); got != "" {
		t.Fatalf("非 API 路径不应设置 no-store，got=%q", got)
	}
}

func TestRequestID(t *testing.T) {
	engine := gin.New()
	engine.Use(RequestID())
	var fromContext string
	engine.GET("/api/v1/test", func(c *gin.Context) { fromContext = RequestIDFrom(c); c.Status(http.StatusOK) })

	first := httptest.NewRecorder()
	engine.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/api/v1/test", nil))
	headerID := first.Header().Get(HeaderRequestID)
	if headerID == "" || len(headerID) != 32 {
		t.Fatalf("请求 ID 格式不符合预期: %q", headerID)
	}
	if fromContext != headerID {
		t.Fatalf("上下文请求 ID=%q 响应头=%q", fromContext, headerID)
	}

	second := httptest.NewRecorder()
	engine.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/api/v1/test", nil))
	if second.Header().Get(HeaderRequestID) == headerID {
		t.Fatal("不同请求必须生成不同请求 ID")
	}
}

func TestRequestIDFromWithoutMiddleware(t *testing.T) {
	engine := gin.New()
	var id string
	engine.GET("/", func(c *gin.Context) { id = RequestIDFrom(c) })
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if id != "" {
		t.Fatalf("未挂中间件时请求 ID 应为空，got=%q", id)
	}
}

func TestAccessLogLevelsAndFields(t *testing.T) {
	cases := []struct {
		name      string
		path      string
		wantLevel string
	}{
		{"info", "/ok", "INFO"},
		{"warn", "/warn", "WARN"},
		{"error", "/error", "ERROR"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&buf, nil))
			engine := gin.New()
			engine.Use(RequestID(), AccessLog(logger))
			engine.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })
			engine.GET("/warn", func(c *gin.Context) { c.Status(http.StatusNotFound) })
			engine.GET("/error", func(c *gin.Context) { c.Status(http.StatusInternalServerError) })

			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tc.path, nil))

			entry := lastLogEntry(t, &buf)
			if entry["level"] != tc.wantLevel {
				t.Fatalf("level=%v want=%s", entry["level"], tc.wantLevel)
			}
			if entry["msg"] != "http" {
				t.Fatalf("msg=%v", entry["msg"])
			}
			for _, field := range []string{"method", "path", "status", "latency_ms", "ip", "request_id"} {
				if _, exists := entry[field]; !exists {
					t.Fatalf("日志缺少字段 %s: %#v", field, entry)
				}
			}
			if entry["request_id"] != recorder.Header().Get(HeaderRequestID) {
				t.Fatalf("日志 request_id=%v 响应头=%q", entry["request_id"], recorder.Header().Get(HeaderRequestID))
			}
		})
	}
}

func TestAccessLogIncludesUserIDForAuthenticatedViewer(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	engine := gin.New()
	engine.Use(RequestID(), AccessLog(logger))
	engine.GET("/api/v1/me", func(c *gin.Context) {
		httpx.SetViewer(c, model.Viewer{ID: 99, Authenticated: true})
		c.Status(http.StatusOK)
	})

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/me", nil))

	entry := lastLogEntry(t, &buf)
	if entry["user_id"] != "99" {
		t.Fatalf("已登录请求日志应带 user_id: %#v", entry)
	}
}

func TestAccessLogNilLoggerUsesDefault(t *testing.T) {
	engine := gin.New()
	engine.Use(AccessLog(nil))
	engine.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("nil logger status=%d", recorder.Code)
	}
}

func lastLogEntry(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	if len(lines) == 0 || len(lines[len(lines)-1]) == 0 {
		t.Fatalf("没有日志输出")
	}
	var entry map[string]any
	if err := json.Unmarshal(lines[len(lines)-1], &entry); err != nil {
		t.Fatalf("日志不是合法 JSON: %v line=%s", err, lines[len(lines)-1])
	}
	return entry
}
