// Package app 编排私信用例：会话创建/列表、消息发送/分页/撤回、已读与隐藏。
// 事务边界、限频、取时与跨模块端口调用都在本层；SQL 不在本层。
package app

import (
	"context"
	"strconv"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/dm/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/platform/ratelimit"
	"github.com/great-magician-01/timeline/internal/shared/access"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// 发送私信与创建会话共享限频：双人对话吞吐高于评论，但仍能拦截批量群发与空会话滥建。
const (
	messageLimit  = 60
	messageWindow = 10 * time.Minute
)

// Deps 是 Service 的全部依赖；Clock 与限频器不注入时使用生产默认值。
type Deps struct {
	Clock clock.Clock
	IDs   IDGenerator
	Tx    TxRunner

	// Users 读取接收者账号行与作者摘要（auth 实现）。
	Users contracts.UserReader
	// Relations 读取实时关注关系，用于 following/mutual 接收策略（social 实现）。
	Relations contracts.RelationReader

	Conversations ConversationRepo
	Messages      MessageRepo

	// Realtime 推送会话更新的实时提示信号（组合根注入进程内事件总线）；
	// nil 时静默跳过（测试与未启用实时推送的部署）。
	Realtime contracts.RealtimePublisher

	MessageLimiter *ratelimit.Limiter
}

// Service 实现私信域用例；不向其它模块发布端口（私信不进通知中心、后台不可见）。
type Service struct {
	deps Deps
}

// New 组装私信服务；nil Clock 回退系统时钟，nil 限频器使用生产默认参数。
func New(deps Deps) *Service {
	if deps.Clock == nil {
		deps.Clock = clock.System()
	}
	if deps.MessageLimiter == nil {
		deps.MessageLimiter = ratelimit.New(messageLimit, messageWindow)
	}
	return &Service{deps: deps}
}

// ===== 读模型 =====

// Conversation 是会话列表/详情项：行数据 + 对方摘要 + 最后消息预览 + 本会话未读数。
type Conversation struct {
	Row         model.DMConversation
	Peer        model.UserBrief
	LastMessage *model.DMMessage
	UnreadCount int
}

// ConversationPage 是会话列表分页结果（按最后消息倒序）。
type ConversationPage struct {
	Items      []Conversation
	HasMore    bool
	NextCursor int64
}

// Message 是消息列表项：行数据 + 发送者摘要 + 是否仍可撤回。
type Message struct {
	Row       model.DMMessage
	Sender    model.UserBrief
	CanRecall bool
}

// MessagePage 是消息分页结果。
type MessagePage struct {
	Items      []Message
	HasMore    bool
	NextCursor int64
	// LastID 是本页最后一条消息的 ID（after_id 增量模式下供客户端继续拉取）。
	LastID int64
}

// ===== 内部工具 =====

func (s *Service) now() time.Time { return s.deps.Clock.Now() }

// newID 生成雪花 ID；失败包装为 internal 错误，不把驱动错误暴露给调用方。
func (s *Service) newID() (int64, error) {
	id, err := s.deps.IDs.Next()
	if err != nil {
		return 0, apperr.Wrap(err, apperr.KindInternal, "生成 ID 失败")
	}
	if id <= 0 {
		return 0, apperr.Internal("生成 ID 失败")
	}
	return id, nil
}

// requireWritable 校验写操作最低条件（登录、未停用、非 AI）。
func (s *Service) requireWritable(viewer model.Viewer) error {
	return access.RequireWritable(viewer)
}

// allowAction 执行用户维度限频；超限返回带 RetryAfter 的 rate_limited 错误。
func (s *Service) allowAction(viewer model.Viewer) error {
	allowed, retryAfter := s.deps.MessageLimiter.Consume("user:" + strconv.FormatInt(viewer.ID, 10))
	if allowed {
		return nil
	}
	return apperr.RateLimited(retryAfter)
}

// trimPage 把「多查一条」的结果裁剪为 limit 条，并给出是否还有下一页。
func trimPage[T any](items []T, limit int) ([]T, bool) {
	if len(items) > limit {
		return items[:limit], true
	}
	return items, false
}

// ensureCanSend 按接收者当前策略加载必要的关注关系，再交给 domain 纯策略判定。
// following（“仅我关注的人”）与 mutual 都需要接收者关注发送者；mutual 另需发送者关注接收者。
// 发送与创建会话共用；调用方必须在事务内重新调用（策略可能在建会话后变化）。
func (s *Service) ensureCanSend(ctx context.Context, senderID int64, recipient model.User) error {
	var (
		senderFollows    bool
		recipientFollows bool
		err              error
	)
	if recipient.DMPolicy == model.DMPolicyFollowing || recipient.DMPolicy == model.DMPolicyMutual {
		recipientFollows, err = s.deps.Relations.IsFollowing(ctx, recipient.ID, senderID)
		if err != nil {
			return err
		}
	}
	if recipient.DMPolicy == model.DMPolicyMutual {
		senderFollows, err = s.deps.Relations.IsFollowing(ctx, senderID, recipient.ID)
		if err != nil {
			return err
		}
	}
	return domain.CanSendTo(recipient, senderID, senderFollows, recipientFollows)
}

// conversationView 按行组装会话视图：对方摘要、最后消息与未读数。
func (s *Service) conversationView(ctx context.Context, viewerID int64, conv model.DMConversation) (Conversation, error) {
	peerID, err := domain.PeerID(conv, viewerID)
	if err != nil {
		return Conversation{}, err
	}
	briefs, err := s.deps.Users.GetUserBriefs(ctx, []int64{peerID})
	if err != nil {
		return Conversation{}, err
	}
	view := Conversation{Row: conv, Peer: briefs[peerID]}
	if conv.LastMessageID != nil {
		messages, err := s.deps.Messages.ByIDs(ctx, []int64{*conv.LastMessageID})
		if err != nil {
			return Conversation{}, err
		}
		if msg, ok := messages[*conv.LastMessageID]; ok {
			view.LastMessage = &msg
		}
	}
	unread, err := s.deps.Messages.UnreadByConversations(ctx, viewerID, []int64{conv.ID})
	if err != nil {
		return Conversation{}, err
	}
	view.UnreadCount = unread[conv.ID]
	return view, nil
}

// canRecall 判断消息当前对访问者是否可撤回（已撤回不再展示撤回动作）。
func (s *Service) canRecall(msg model.DMMessage, viewer model.Viewer, now time.Time) bool {
	if msg.Recalled() {
		return false
	}
	return domain.CanRecall(msg, viewer, now) == nil
}

// assembleMessages 批量补齐发送者摘要并计算 can_recall，保持输入顺序。
func (s *Service) assembleMessages(ctx context.Context, viewer model.Viewer, rows []model.DMMessage, now time.Time) ([]Message, error) {
	senderIDs := make([]int64, 0, len(rows))
	seen := make(map[int64]struct{}, len(rows))
	for _, row := range rows {
		if _, ok := seen[row.SenderID]; ok {
			continue
		}
		seen[row.SenderID] = struct{}{}
		senderIDs = append(senderIDs, row.SenderID)
	}
	briefs, err := s.deps.Users.GetUserBriefs(ctx, senderIDs)
	if err != nil {
		return nil, err
	}
	items := make([]Message, 0, len(rows))
	for _, row := range rows {
		items = append(items, Message{
			Row:       row,
			Sender:    briefs[row.SenderID],
			CanRecall: s.canRecall(row, viewer, now),
		})
	}
	return items, nil
}
