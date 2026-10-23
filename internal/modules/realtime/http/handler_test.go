package http

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/modules/realtime/app"
	"github.com/great-magician-01/timeline/internal/platform/eventbus"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// newTestServer 构造真实 HTTP 服务（httptest.NewServer 支持流式冲刷，httptest.ResponseRecorder
// 不支持）；viewer 中间件模拟 Login 策略解析出的访问者身份。
func newTestServer(t *testing.T, broker *eventbus.Broker, viewerID int64) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		httpx.SetViewer(c, model.Viewer{ID: viewerID, Authenticated: viewerID > 0})
		c.Next()
	})
	engine.GET("/api/v1/events", New(app.New(app.Deps{Broker: broker}), nil).Events)
	srv := httptest.NewServer(engine)
	t.Cleanup(srv.Close)
	return srv
}

// connect 建立 SSE 连接并断言响应头；调用方负责 cancel。
func connect(t *testing.T, srv *httptest.Server, viewerID int64) (*http.Response, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/v1/events", nil)
	if err != nil {
		cancel()
		t.Fatalf("构造请求失败: %v", err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		cancel()
		t.Fatalf("连接失败: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := resp.Header.Get("X-Accel-Buffering"); got != "no" {
		t.Fatalf("X-Accel-Buffering = %q，期望 no", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control = %q", got)
	}
	return resp, cancel
}

// readEvent 从流中读到下一个 data 行，返回事件种类与 data 载荷。
func readEvent(t *testing.T, resp *http.Response) (string, string) {
	t.Helper()
	scanner := bufio.NewScanner(resp.Body)
	kind := ""
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			kind = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			return kind, strings.TrimPrefix(line, "data: ")
		}
	}
	t.Fatalf("流在读到事件前结束: %v", scanner.Err())
	return "", ""
}

// TestEventsRequiresLogin 覆盖未登录 401（真实路由由 Login 策略保证，这里复验 RequireViewer）。
func TestEventsRequiresLogin(t *testing.T) {
	srv := newTestServer(t, eventbus.New(), 0)
	resp, err := srv.Client().Get(srv.URL + "/api/v1/events")
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("状态码 = %d，期望 401", resp.StatusCode)
	}
}

// TestEventsStreamsSignal 覆盖事件流：retry 提示、事件格式与 data 载荷（ID 字符串化）。
func TestEventsStreamsSignal(t *testing.T) {
	broker := eventbus.New()
	srv := newTestServer(t, broker, 7)
	resp, cancel := connect(t, srv, 7)
	defer cancel()
	defer resp.Body.Close()

	// 首块应含 retry 提示与首个心跳前的连接保活（读到首个事件即可）。
	broker.Publish(7, eventbus.Event{Kind: eventbus.KindNotification})
	kind, data := readEvent(t, resp)
	if kind != eventbus.KindNotification {
		t.Fatalf("事件种类 = %q，期望 notification", kind)
	}
	if data != "{}" {
		t.Fatalf("通知事件 data = %q，期望 {}", data)
	}

	broker.Publish(7, eventbus.Event{Kind: eventbus.KindDM, ConversationID: 123})
	kind, data = readEvent(t, resp)
	if kind != eventbus.KindDM {
		t.Fatalf("事件种类 = %q，期望 dm", kind)
	}
	var payload streamPayload
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		t.Fatalf("data 解析失败: %v", err)
	}
	if payload.ConversationID != "123" {
		t.Fatalf("conversation_id = %q，期望字符串 \"123\"", payload.ConversationID)
	}
}

// TestEventsEndsOnBrokerClose 覆盖服务关闭广播：订阅 channel 关闭后连接结束。
func TestEventsEndsOnBrokerClose(t *testing.T) {
	broker := eventbus.New()
	srv := newTestServer(t, broker, 7)
	resp, cancel := connect(t, srv, 7)
	defer cancel()
	defer resp.Body.Close()

	broker.Close()
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "data: ") {
			t.Fatal("关闭后不应再有事件")
		}
	}
}

// TestEventsConnLimit 覆盖单用户连接上限：第 4 条连接返回 503。
func TestEventsConnLimit(t *testing.T) {
	broker := eventbus.New()
	srv := newTestServer(t, broker, 7)
	conns := make([]*http.Response, 0, eventbus.PerUserConnLimit)
	cancels := make([]context.CancelFunc, 0, eventbus.PerUserConnLimit)
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
		for _, resp := range conns {
			_ = resp.Body.Close()
		}
	}()
	for i := 0; i < eventbus.PerUserConnLimit; i++ {
		resp, cancel := connect(t, srv, 7)
		conns = append(conns, resp)
		cancels = append(cancels, cancel)
	}

	resp, err := srv.Client().Get(srv.URL + "/api/v1/events")
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("超限状态码 = %d，期望 503", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "unavailable") {
		t.Fatalf("超限响应应含 unavailable 错误码: %s", body)
	}
}
