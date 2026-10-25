// Package app 编排群聊用例：建群/列表/详情、成员管理（拉人/踢人/转让/退出/解散/
// 隐藏）、消息发送/分页/撤回/已读与未读徽章。事务边界、限频、取时与跨模块端口
// 调用都在本层；SQL 不在本层。
package app

import (
	"log/slog"
	"strconv"
	"time"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/platform/ratelimit"
	"github.com/great-magician-01/timeline/internal/shared/access"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// 发送消息与成员变更（拉人）共享限频：与私信同参数，拦截批量建群与群发滥用；
// 已读、隐藏、退出等幂等管理动作不限频。
const (
	writeLimit  = 60
	writeWindow = 10 * time.Minute
)

// Deps 是 Service 的全部依赖；Clock 与限频器不注入时使用生产默认值。
type Deps struct {
	Clock clock.Clock
	IDs   IDGenerator
	Tx    TxRunner
	Log   *slog.Logger

	// Users 读取账号行与作者摘要（auth 实现）。
	Users contracts.UserReader

	Groups   ConversationRepo
	Members  MemberRepo
	Messages MessageRepo

	// Realtime 推送群聊更新的实时提示信号（组合根注入进程内事件总线）；
	// nil 时静默跳过（测试与未启用实时推送的部署）。
	Realtime contracts.RealtimePublisher

	// AITrigger 把群内 @提及 转交 agent 投递 AI 回复任务；nil 时跳过（未启用 AI）。
	// 实现方保证幂等；经 db.AfterCommit 在事务提交后调用。
	AITrigger contracts.GroupAITrigger

	WriteLimiter *ratelimit.Limiter
}

// Service 实现群聊域用例；不向其它模块发布端口（群聊不进通知中心、后台不可见）。
type Service struct {
	deps Deps
}

// New 组装群聊服务；nil Clock 回退系统时钟，nil Log 回退默认 logger，nil 限频器使用生产默认参数。
func New(deps Deps) *Service {
	if deps.Clock == nil {
		deps.Clock = clock.System()
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	if deps.WriteLimiter == nil {
		deps.WriteLimiter = ratelimit.New(writeLimit, writeWindow)
	}
	return &Service{deps: deps}
}

// ===== 读模型 =====

// Group 是群列表/详情项：行数据 + 本访问者视角的角色/未读数 + 最后消息预览。
type Group struct {
	Row         model.GroupConversation
	Role        string
	MemberCount int
	LastMessage *model.GroupMessage
	UnreadCount int
}

// GroupPage 是群列表分页结果（按最后消息倒序）。
type GroupPage struct {
	Items      []Group
	HasMore    bool
	NextCursor int64
}

// MemberItem 是成员列表项：成员行 + 账号摘要。
type MemberItem struct {
	Row   model.GroupMember
	Brief model.UserBrief
}

// MemberPage 是成员分页结果（按加入时间正序）。
type MemberPage struct {
	Items      []MemberItem
	HasMore    bool
	NextCursor string
}

// Message 是消息列表项：行数据 + 发送者摘要 + 是否仍可撤回。
type Message struct {
	Row       model.GroupMessage
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
// AI 代发群消息走 writeFromAI 内部路径，不经本入口。
func (s *Service) requireWritable(viewer model.Viewer) error {
	return access.RequireWritable(viewer)
}

// allowAction 执行用户维度限频；超限返回带 RetryAfter 的 rate_limited 错误。
func (s *Service) allowAction(viewer model.Viewer) error {
	allowed, retryAfter := s.deps.WriteLimiter.Consume("user:" + strconv.FormatInt(viewer.ID, 10))
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
