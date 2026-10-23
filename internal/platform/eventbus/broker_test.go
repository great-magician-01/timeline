package eventbus

import (
	"sync"
	"testing"
	"time"
)

// TestSubscribePublishReceive 覆盖基本投递：订阅者按序收到事件。
func TestSubscribePublishReceive(t *testing.T) {
	b := New()
	ch, cancel, err := b.Subscribe(42)
	if err != nil {
		t.Fatalf("Subscribe 返回错误: %v", err)
	}
	defer cancel()

	b.Publish(42, Event{Kind: KindNotification})
	b.Publish(42, Event{Kind: KindDM, ConversationID: 7})

	for i, want := range []Event{
		{Kind: KindNotification},
		{Kind: KindDM, ConversationID: 7},
	} {
		select {
		case got := <-ch:
			if got != want {
				t.Fatalf("事件[%d] = %+v, want %+v", i, got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("事件[%d] 未送达", i)
		}
	}
}

// TestPublishIsolation 覆盖用户隔离：事件只投递给目标用户。
func TestPublishIsolation(t *testing.T) {
	b := New()
	chA, cancelA, err := b.Subscribe(1)
	if err != nil {
		t.Fatalf("Subscribe 返回错误: %v", err)
	}
	defer cancelA()
	chB, cancelB, err := b.Subscribe(2)
	if err != nil {
		t.Fatalf("Subscribe 返回错误: %v", err)
	}
	defer cancelB()

	b.Publish(1, Event{Kind: KindNotification})

	select {
	case got := <-chA:
		if got.Kind != KindNotification {
			t.Fatalf("事件 = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("事件未送达用户 1")
	}
	select {
	case got := <-chB:
		t.Fatalf("事件不应投递给其他用户: %+v", got)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestPublishWithoutSubscriber 覆盖无订阅者时静默丢弃且不计数。
func TestPublishWithoutSubscriber(t *testing.T) {
	b := New()
	b.Publish(1, Event{Kind: KindNotification})
	if b.Dropped() != 0 {
		t.Fatalf("无订阅者的丢弃不应计数, dropped=%d", b.Dropped())
	}
}

// TestSlowConsumerDrops 覆盖缓冲满时丢弃且不阻塞发布方，已缓冲事件完整可读。
func TestSlowConsumerDrops(t *testing.T) {
	b := New()
	ch, cancel, err := b.Subscribe(1)
	if err != nil {
		t.Fatalf("Subscribe 返回错误: %v", err)
	}
	defer cancel()

	for i := 0; i < channelBuffer+10; i++ {
		b.Publish(1, Event{Kind: KindNotification}) // 无消费者也不阻塞
	}
	if got := b.Dropped(); got != 10 {
		t.Fatalf("dropped = %d, want 10", got)
	}
	for i := 0; i < channelBuffer; i++ {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatalf("缓冲事件[%d] 未送达", i)
		}
	}
}

// TestPerUserConnLimit 覆盖单用户连接数上限与退订后释放配额。
func TestPerUserConnLimit(t *testing.T) {
	b := New()
	cancels := make([]func(), 0, PerUserConnLimit)
	for i := 0; i < PerUserConnLimit; i++ {
		_, cancel, err := b.Subscribe(1)
		if err != nil {
			t.Fatalf("Subscribe#%d 返回错误: %v", i+1, err)
		}
		cancels = append(cancels, cancel)
	}
	if _, _, err := b.Subscribe(1); err == nil {
		t.Fatal("超过单用户上限应返回错误")
	}
	cancels[len(cancels)-1]()
	if _, cancel, err := b.Subscribe(1); err != nil {
		t.Fatalf("退订后应可重新订阅: %v", err)
	} else {
		cancel()
	}
	for _, cancel := range cancels {
		cancel()
	}
}

// TestGlobalConnLimit 覆盖全局连接数上限。
func TestGlobalConnLimit(t *testing.T) {
	b := New()
	cancels := make([]func(), 0, GlobalConnLimit)
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
	}()
	for i := 1; i <= GlobalConnLimit; i++ {
		_, cancel, err := b.Subscribe(int64(i))
		if err != nil {
			t.Fatalf("Subscribe(%d) 返回错误: %v", i, err)
		}
		cancels = append(cancels, cancel)
	}
	if _, _, err := b.Subscribe(GlobalConnLimit + 1); err == nil {
		t.Fatal("超过全局上限应返回错误")
	}
	if got := b.TotalConns(); got != GlobalConnLimit {
		t.Fatalf("TotalConns = %d, want %d", got, GlobalConnLimit)
	}
}

// TestCancelIdempotent 覆盖退订幂等、计数回收与 channel 关闭可观测。
func TestCancelIdempotent(t *testing.T) {
	b := New()
	ch, cancel, err := b.Subscribe(1)
	if err != nil {
		t.Fatalf("Subscribe 返回错误: %v", err)
	}
	cancel()
	cancel() // 二次退订不应 panic
	if got := b.TotalConns(); got != 0 {
		t.Fatalf("TotalConns = %d, want 0", got)
	}
	// 关闭后的 channel 立即可读出关闭信号，读循环据此退出。
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("退订后 channel 不应有事件")
		}
	default:
		t.Fatal("退订后 channel 应已关闭")
	}
}

// TestInvalidSubscriber 覆盖非法订阅者。
func TestInvalidSubscriber(t *testing.T) {
	b := New()
	if _, _, err := b.Subscribe(0); err == nil {
		t.Fatal("userID=0 应返回错误")
	}
	if _, _, err := b.Subscribe(-1); err == nil {
		t.Fatal("userID<0 应返回错误")
	}
}

// TestCloseBroadcast 覆盖 Close：全部订阅 channel 关闭、之后订阅失败、Publish 空操作、幂等。
func TestCloseBroadcast(t *testing.T) {
	b := New()
	ch1, cancel1, err := b.Subscribe(1)
	if err != nil {
		t.Fatalf("Subscribe 返回错误: %v", err)
	}
	defer cancel1()
	ch2, cancel2, err := b.Subscribe(2)
	if err != nil {
		t.Fatalf("Subscribe 返回错误: %v", err)
	}
	defer cancel2()

	b.Close()
	b.Close() // 幂等

	for name, ch := range map[string]<-chan Event{"user1": ch1, "user2": ch2} {
		select {
		case _, ok := <-ch:
			if ok {
				t.Fatalf("%s 的订阅 channel 关闭后不应有事件", name)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s 的订阅 channel 未被关闭", name)
		}
	}
	if _, _, err := b.Subscribe(3); err == nil {
		t.Fatal("关闭后 Subscribe 应返回错误")
	}
	b.Publish(1, Event{Kind: KindNotification}) // 关闭后发布不应 panic
	if got := b.TotalConns(); got != 0 {
		t.Fatalf("关闭后 TotalConns = %d, want 0", got)
	}
}

// TestPublishAfterCancel 覆盖退订后发布：不 panic、无 send-on-closed。
func TestPublishAfterCancel(t *testing.T) {
	b := New()
	_, cancel, err := b.Subscribe(1)
	if err != nil {
		t.Fatalf("Subscribe 返回错误: %v", err)
	}
	cancel()
	b.Publish(1, Event{Kind: KindNotification})
}

// TestConcurrentSubscribePublish 覆盖并发场景：并发订阅/退订/发布/Close 无 panic。
func TestConcurrentSubscribePublish(t *testing.T) {
	b := New()
	var wg sync.WaitGroup
	for i := 1; i <= 16; i++ {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_, cancel, err := b.Subscribe(id)
				if err != nil {
					continue
				}
				b.Publish(id, Event{Kind: KindNotification})
				b.Publish(id+100, Event{Kind: KindDM})
				cancel()
			}
		}(int64(i))
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 100; j++ {
			b.Publish(1, Event{Kind: KindNotification})
		}
	}()
	wg.Wait()
	b.Close()
}
