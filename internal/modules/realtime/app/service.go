// Package app 编排实时推送用例：把在线用户的 SSE 订阅转发到进程内事件总线。
// 本模块无 SQL、无领域状态机（因此没有 domain/repo 层）；事件只作提示信号，
// 内容一律由客户端收到信号后经既有只读接口拉取。
package app

import (
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/eventbus"
)

// Deps 是 Service 的全部依赖。
type Deps struct {
	// Broker 是进程内事件总线；由组合根构造并全应用共享同一实例。
	Broker *eventbus.Broker
}

// Service 实现实时推送域用例；不向其它模块发布端口（SSE 是面向浏览器的传输通道，
// 模块间事件发布走 contracts.RealtimePublisher，由组合根直接适配到同一 Broker）。
type Service struct {
	broker *eventbus.Broker
}

// New 组装实时推送服务。
func New(deps Deps) *Service {
	return &Service{broker: deps.Broker}
}

// Subscribe 为用户注册一条 SSE 订阅，返回只读事件 channel 与退订函数（幂等，
// 连接结束时必须调用）。超过单用户/全局连接上限时返回 Unavailable（handler 映射 503）。
func (s *Service) Subscribe(userID int64) (<-chan eventbus.Event, func(), error) {
	events, cancel, err := s.broker.Subscribe(userID)
	if err != nil {
		return nil, nil, apperr.Unavailable("实时连接数已达上限，请稍后重试")
	}
	return events, cancel, nil
}

// Close 断开全部订阅；组合根在收到退出信号与优雅关闭时调用（幂等）。
func (s *Service) Close() {
	s.broker.Close()
}
