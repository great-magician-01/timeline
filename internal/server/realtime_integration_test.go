package server_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/testkit"
)

// TestRealtimeIntegration 覆盖 SSE 实时推送（GET /api/v1/events）：响应头与事件格式、
// 通知信号（点赞/评论触发）、私信信号（conversation_id 字符串化）、游客 401、
// 断线重连后仍能收到事件。只在隔离测试 schema 中执行，默认跳过，需 TIMELINE_INTEGRATION=1。
func TestRealtimeIntegration(t *testing.T) {
	srv := testkit.NewIntegrationServer(t)
	suffix := testkit.IntegrationShortSuffix()

	alice := srv.Client(t)
	alice.RegisterAndLogin("rt_alice_"+suffix, "rt-alice-"+suffix+"@example.com")

	bob := srv.Client(t)
	bobName := "rt_bob_" + suffix
	bob.RegisterAndLogin(bobName, "rt-bob-"+suffix+"@example.com")

	publish := func(t *testing.T, c *testkit.Client, text string) string {
		t.Helper()
		status, _, payload, raw := c.Call(http.MethodPost, "/api/v1/posts", true, map[string]any{
			"content_format": "richtext",
			"content_source": "<p>" + text + "</p>",
			"visibility":     "public",
			"action":         "publish",
		})
		testkit.ExpectStatus(t, status, http.StatusCreated, raw)
		return testkit.MustString(t, testkit.MustNested(t, payload, "post"), "id")
	}

	// ── 游客 401 ──
	guest := srv.Client(t)
	guestCtx, guestCancel := context.WithTimeout(context.Background(), 5*time.Second)
	guestResp, err := guest.OpenStream(guestCtx, "/api/v1/events")
	guestCancel()
	if err != nil {
		t.Fatalf("游客连接请求失败: %v", err)
	}
	if guestResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("游客连接状态码 = %d，期望 401", guestResp.StatusCode)
	}
	_ = guestResp.Body.Close()

	// ── Alice 连接事件流 ──
	streamCtx, cancelStream := context.WithCancel(context.Background())
	defer cancelStream()
	resp, err := alice.OpenStream(streamCtx, "/api/v1/events")
	if err != nil {
		t.Fatalf("连接事件流失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("事件流状态码 = %d，期望 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q，期望 text/event-stream", got)
	}
	if got := resp.Header.Get("X-Accel-Buffering"); got != "no" {
		t.Fatalf("X-Accel-Buffering = %q，期望 no", got)
	}

	reader := bufio.NewReader(resp.Body)
	// readEvent 读取下一个指定种类的 data 行；事件行与 data 行成对出现。
	readEvent := func(wantKind string, timeout time.Duration) string {
		t.Helper()
		deadline := time.Now().Add(timeout)
		kind, data := "", ""
		for time.Now().Before(deadline) {
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				t.Fatalf("读取流失败（等待 %s 事件）: %v", wantKind, readErr)
			}
			line = strings.TrimRight(line, "\r\n")
			switch {
			case strings.HasPrefix(line, "event: "):
				kind = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = strings.TrimPrefix(line, "data: ")
				if kind == wantKind {
					return data
				}
				kind, data = "", ""
			}
		}
		t.Fatalf("等待 %s 事件超时", wantKind)
		return ""
	}

	// ── Bob 点赞 Alice 的帖子 → notification 信号（无载荷） ──
	postID := publish(t, alice, "实时信号帖 "+suffix)
	status, _, _, raw := bob.Call(http.MethodPost, "/api/v1/posts/"+postID+"/like", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if data := readEvent("notification", 10*time.Second); data != "{}" {
		t.Fatalf("notification data = %q，期望 {}", data)
	}

	// ── Bob 给 Alice 发私信 → dm 信号（conversation_id 为字符串） ──
	status, _, payload, raw := bob.Call(http.MethodPost, "/api/v1/dm/conversations", true,
		map[string]any{"username": "rt_alice_" + suffix})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	convID := testkit.MustString(t, testkit.MustNested(t, payload, "conversation"), "id")
	status, _, _, raw = bob.Call(http.MethodPost, "/api/v1/dm/conversations/"+convID+"/messages", true,
		map[string]any{
			"content_format": "richtext",
			"content_source": "<p>实时私信 " + suffix + "</p>",
		})
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)

	var dmPayload struct {
		ConversationID string `json:"conversation_id"`
	}
	if err := json.Unmarshal([]byte(readEvent("dm", 10*time.Second)), &dmPayload); err != nil {
		t.Fatalf("dm data 解析失败: %v", err)
	}
	if dmPayload.ConversationID != convID {
		t.Fatalf("conversation_id = %q，期望 %q（雪花 ID 字符串化）", dmPayload.ConversationID, convID)
	}

	// ── 断线重连：关闭后重新连接，仍能收到后续事件 ──
	cancelStream()
	_ = resp.Body.Close()

	reconnectCtx, cancelReconnect := context.WithCancel(context.Background())
	defer cancelReconnect()
	resp2, err := alice.OpenStream(reconnectCtx, "/api/v1/events")
	if err != nil {
		t.Fatalf("重连事件流失败: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("重连状态码 = %d，期望 200", resp2.StatusCode)
	}
	reader2 := bufio.NewReader(resp2.Body)

	// Bob 再评论 Alice 的帖子 → 新流上收到 notification。
	status, _, _, raw = bob.Call(http.MethodPost, "/api/v1/posts/"+postID+"/comments", true,
		map[string]any{
			"content_format": "richtext",
			"content_source": "<p>重连后的评论 " + suffix + "</p>",
		})
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)

	deadline := time.Now().Add(10 * time.Second)
	kind, data := "", ""
	for time.Now().Before(deadline) {
		line, readErr := reader2.ReadString('\n')
		if readErr != nil {
			t.Fatalf("重连流读取失败: %v", readErr)
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "event: "):
			kind = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimPrefix(line, "data: ")
			if kind == "notification" {
				if data != "{}" {
					t.Fatalf("notification data = %q，期望 {}", data)
				}
				return
			}
			kind, data = "", ""
		}
	}
	t.Fatal("重连后等待 notification 事件超时")
}
