// Package eventbus 提供进程内的按用户事件总线，把业务侧的提示信号投递给在线用户的
// SSE 连接。无业务语义：事件只携带种类与可选会话 ID，不携带任何内容载荷——客户端
// 收到信号后通过常规只读接口自行拉取，可见性与数据一致性都由既有接口保证。
//
// 单实例部署前提（docs/ARCHITECTURE.md）：订阅表在进程内存中，不跨实例广播；
// 未来水平扩展时替换 Broker 实现即可，消费方端口（shared/contracts.RealtimePublisher）
// 不变。
package eventbus

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// Kind 是事件种类，与 SSE 的 event 字段、前端分发逻辑一一对应。
const (
	// KindNotification 通知中心有更新（未读数/列表），接收者应重新拉取。
	KindNotification = "notification"
	// KindDM 私信会话有更新（新消息或撤回），双方应重新拉取。
	KindDM = "dm"
	// KindGroup 群聊有更新（新消息、撤回或成员变更），成员应重新拉取。
	KindGroup = "group"
)

// 连接与投递参数。v1 为代码常量不做配置化：SSE 连接是长寿命资源，
// 上限的作用是防滥用而非精细调优（每用户 3 条覆盖多标签页，全局 2000
// 远超当前部署规模）。
const (
	// PerUserConnLimit 是单个用户并发连接数上限（多标签页场景）。
	PerUserConnLimit = 3
	// GlobalConnLimit 是全局并发连接数上限。
	GlobalConnLimit = 2000
	// channelBuffer 是单个订阅的事件缓冲；满即丢弃（信号语义 + 轮询兜底，丢弃无害）。
	channelBuffer = 16
)

// Event 是推送给客户端的最小提示信号；不含任何内容载荷。
type Event struct {
	Kind string
	// ConversationID 仅 Kind=DM/Group 时有效（群聊为群 ID）；通知事件不携带载荷细节。
	ConversationID int64
}

// subscription 是一个已注册的连接订阅。
type subscription struct {
	ch   chan Event
	user int64
}

// Broker 是按用户订阅的进程内事件总线；零值不可用，须经 New 构造。
type Broker struct {
	mu      sync.RWMutex
	subs    map[int64]map[*subscription]struct{}
	total   int
	closed  bool
	dropped atomic.Int64
}

// New 构造事件总线。
func New() *Broker {
	return &Broker{subs: make(map[int64]map[*subscription]struct{})}
}

// Subscribe 为用户注册一个订阅，返回只读事件 channel 与退订函数。
// 退订函数幂等；调用方必须在连接结束时调用（通常 defer），也须监听返回的
// channel 关闭（Close 广播）及时结束读循环。超过单用户/全局连接上限时返回错误。
func (b *Broker) Subscribe(userID int64) (<-chan Event, func(), error) {
	if userID <= 0 {
		return nil, nil, fmt.Errorf("订阅者无效")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, nil, fmt.Errorf("事件总线已关闭")
	}
	if b.total >= GlobalConnLimit {
		return nil, nil, fmt.Errorf("连接数已达全局上限")
	}
	if len(b.subs[userID]) >= PerUserConnLimit {
		return nil, nil, fmt.Errorf("连接数已达单用户上限")
	}
	sub := &subscription{ch: make(chan Event, channelBuffer), user: userID}
	if b.subs[userID] == nil {
		b.subs[userID] = make(map[*subscription]struct{})
	}
	b.subs[userID][sub] = struct{}{}
	b.total++
	return sub.ch, b.unsubscribeFunc(sub), nil
}

// unsubscribeFunc 返回幂等退订函数；close channel 前必须持写锁，避免与 Publish 的
// 持读锁发送并发导致「向已关闭 channel 发送」panic。
func (b *Broker) unsubscribeFunc(sub *subscription) func() {
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		subs, ok := b.subs[sub.user]
		if !ok {
			return
		}
		if _, ok := subs[sub]; !ok {
			return
		}
		delete(subs, sub)
		if len(subs) == 0 {
			delete(b.subs, sub.user)
		}
		b.total--
		close(sub.ch)
	}
}

// Publish 向用户的全部订阅非阻塞投递事件。缓冲满（慢消费者）或无订阅时静默丢弃——
// 事件是提示信号，客户端轮询兜底，丢弃只损失实时性不损失正确性。
//
// 本方法自身 recover：它作为 db.AfterCommit 回调执行，契约要求不得向上 panic。
func (b *Broker) Publish(userID int64, ev Event) {
	defer func() { _ = recover() }()
	if userID <= 0 {
		return
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for sub := range b.subs[userID] {
		select {
		case sub.ch <- ev:
		default:
			b.dropped.Add(1)
		}
	}
}

// Dropped 返回累计丢弃的事件数（慢消费者/无缓冲可用），供测试与诊断。
func (b *Broker) Dropped() int64 { return b.dropped.Load() }

// TotalConns 返回当前活跃连接数，供测试与诊断。
func (b *Broker) TotalConns() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.total
}

// Close 关闭事件总线并断开全部订阅（订阅 channel 被关闭，读循环随即结束）。
// 幂等；关闭后 Subscribe 返回错误、Publish 成为空操作。
func (b *Broker) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for userID, subs := range b.subs {
		for sub := range subs {
			close(sub.ch)
		}
		delete(b.subs, userID)
	}
	b.total = 0
}
