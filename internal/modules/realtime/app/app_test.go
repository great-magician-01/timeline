package app

import (
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/eventbus"
)

// TestSubscribeDelegates 覆盖订阅转发：发布经总线送达订阅者。
func TestSubscribeDelegates(t *testing.T) {
	broker := eventbus.New()
	svc := New(Deps{Broker: broker})
	events, cancel, err := svc.Subscribe(1)
	if err != nil {
		t.Fatalf("Subscribe 返回错误: %v", err)
	}
	defer cancel()
	broker.Publish(1, eventbus.Event{Kind: eventbus.KindNotification})
	select {
	case got := <-events:
		if got.Kind != eventbus.KindNotification {
			t.Fatalf("事件 = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("事件未送达")
	}
}

// TestSubscribeLimitMapsUnavailable 覆盖连接超限返回 Unavailable 类别（handler 映射 503）。
func TestSubscribeLimitMapsUnavailable(t *testing.T) {
	svc := New(Deps{Broker: eventbus.New()})
	cancels := make([]func(), 0, eventbus.PerUserConnLimit)
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
	}()
	for i := 0; i < eventbus.PerUserConnLimit; i++ {
		_, cancel, err := svc.Subscribe(1)
		if err != nil {
			t.Fatalf("Subscribe#%d 返回错误: %v", i+1, err)
		}
		cancels = append(cancels, cancel)
	}
	if _, _, err := svc.Subscribe(1); !apperr.IsKind(err, apperr.KindUnavailable) {
		t.Fatalf("超限错误 = %v，期望 unavailable 类别", err)
	}
}

// TestCloseDelegates 覆盖 Close 透传与幂等：关闭后订阅返回错误。
func TestCloseDelegates(t *testing.T) {
	svc := New(Deps{Broker: eventbus.New()})
	svc.Close()
	if _, _, err := svc.Subscribe(1); err == nil {
		t.Fatal("Close 后 Subscribe 应返回错误")
	}
	svc.Close() // 幂等
}
