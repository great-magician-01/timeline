package contracts

// RealtimePublisher 是向在线用户推送实时提示信号的窄端口。
//
// 实现方：组合根用 platform/eventbus 的进程内总线适配（单实例部署前提）；
// 消费者：notice（通知写入）、dm（私信发送/撤回）。
//
// 事件只作提示信号、不携带内容载荷：客户端收到后通过常规只读接口自行拉取，
// 可见性与数据一致性都由既有接口保证。在事务内调用时须经 db.AfterCommit 注册，
// 保证消费方收到信号时数据必然已提交；实现方自身 recover，不会向上 panic。
type RealtimePublisher interface {
	// PublishNotification 通知接收者：通知中心有更新（未读数/列表）。
	// 幂等跳过、接收者无效等未实际写入通知的场景不应调用。
	PublishNotification(recipientID int64)

	// PublishDM 通知会话一方：会话有新消息或撤回，应重新拉取该会话。
	PublishDM(userID, conversationID int64)
}
