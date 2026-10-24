// Package http 提供实时推送域的 Gin handler：SSE 提示信号流（text/event-stream）。
// 业务规则在 app 与 platform/eventbus；本层只做 SSE 协议写入、心跳与冲刷。
package http

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/modules/realtime/app"
	"github.com/great-magician-01/timeline/internal/platform/eventbus"
	"github.com/great-magician-01/timeline/internal/shared/format"
	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// heartbeatInterval 是 SSE 心跳注释行间隔；必须小于常见反代的空闲读超时
// （nginx proxy_read_timeout 默认 60s），否则长连接会被代理掐断。
const heartbeatInterval = 25 * time.Second

// Handler 承载 SSE 实时推送接口。
type Handler struct {
	svc *app.Service
	log *slog.Logger
}

// New 构造实时推送 handler；nil logger 回退默认 logger。
func New(svc *app.Service, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{svc: svc, log: log}
}

// streamPayload 是 SSE data 行的 JSON 载荷；雪花 ID 以字符串序列化（全站 JSON 约定），
// 通知事件不携带细节，私信事件只带会话 ID。
type streamPayload struct {
	ConversationID string `json:"conversation_id,omitempty"`
}

// Events 处理 GET /api/v1/events：登录用户的 SSE 提示信号流。
// 响应只携带事件种类（与私信会话 ID）；客户端收到后经既有只读接口拉取详情。
// 连接建立后本 handler 阻塞至客户端断开或服务关闭；访问日志在连接结束时记录一条。
func (h *Handler) Events(c *gin.Context) {
	viewer, ok := httpx.RequireViewer(c)
	if !ok {
		return
	}
	flusher, canFlush := c.Writer.(http.Flusher)
	if !canFlush {
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "当前连接不支持流式响应")
		return
	}
	events, cancel, err := h.svc.Subscribe(viewer.ID)
	if err != nil {
		if httpx.Respond(c, err) {
			return
		}
		h.log.Error("建立实时连接失败", "err", err, "path", c.Request.URL.Path)
		httpx.Error(c, http.StatusInternalServerError, "internal_error", "服务暂时异常，请稍后重试")
		return
	}
	defer cancel()

	header := c.Writer.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	// nginx 默认按 proxy_buffering 缓冲响应，会吞掉事件流直到连接结束；
	// 该头指示反代对本响应关闭缓冲（应用侧解决，无需改代理配置）。
	header.Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	// retry 提示 EventSource 断线后的重连等待（毫秒）；首条写入同时把响应头冲刷出去。
	if _, err := fmt.Fprint(c.Writer, "retry: 3000\n\n"); err != nil {
		return
	}
	flusher.Flush()

	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(c.Writer, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case event, open := <-events:
			if !open {
				// 服务关闭广播：结束连接；EventSource 将按 retry 间隔重连。
				return
			}
			payload, err := json.Marshal(payloadFor(event))
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event.Kind, payload); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// payloadFor 把总线事件转为 SSE data 载荷。
func payloadFor(event eventbus.Event) streamPayload {
	if event.Kind != eventbus.KindDM && event.Kind != eventbus.KindGroup {
		return streamPayload{}
	}
	return streamPayload{ConversationID: format.FormatID(event.ConversationID)}
}
