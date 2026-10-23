package app

import (
	"strconv"
	"time"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// Deps 是 Service 的全部依赖。
//
// 跨模块依赖使用 shared/contracts 的发布接口：Users 由 auth 实现、Posts 由 content 实现、
// Relations 由 social 实现；Comments 是窄端口（签名与 contracts.SocialService.GetComments 一致）。
type Deps struct {
	Clock clock.Clock
	IDs   IDGenerator

	Users     contracts.UserReader
	Posts     contracts.ContentService
	Relations contracts.RelationReader
	Comments  CommentReader

	Notices  NotificationRepo
	Mentions MentionRepo

	// Realtime 推送通知更新的实时提示信号（组合根注入进程内事件总线）；
	// nil 时静默跳过（测试与未启用实时推送的部署）。
	Realtime contracts.RealtimePublisher
}

// Service 实现通知域用例，并作为 content/social/agent 的通知写入端口（contracts.NoticeService）。
type Service struct {
	deps Deps
}

// 编译期断言：notice app 实现 contracts.NoticeService。
var _ contracts.NoticeService = (*Service)(nil)

// New 组装通知服务；nil Clock 回退系统时钟。
func New(deps Deps) *Service {
	if deps.Clock == nil {
		deps.Clock = clock.System()
	}
	return &Service{deps: deps}
}

// Item 是通知列表项：行数据 + 动作者摘要 + 按当前权限解析的预览。
type Item struct {
	Notification model.Notification
	Actor        model.UserBrief
	Preview      *string
}

// Page 是通知分页结果。
type Page struct {
	Items      []Item
	HasMore    bool
	NextCursor int64
}

func (s *Service) now() time.Time { return s.deps.Clock.Now() }

// newID 生成雪花 ID；失败包装为 internal 错误。
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

// eventTime 优先使用调用方传入的 At（同一事务应复用业务 now），为零时回退时钟。
func (s *Service) eventTime(at time.Time) time.Time {
	if at.IsZero() {
		return s.now()
	}
	return at
}

func trimPage[T any](items []T, limit int) ([]T, bool) {
	if len(items) > limit {
		return items[:limit], true
	}
	return items, false
}

// mentionKey 生成提及通知的稳定幂等键（与历史实现一致）。
func mentionKey(sourceType string, sourceID, userID int64) string {
	return "mention:" + sourceType + ":" + strconv.FormatInt(sourceID, 10) + ":" + strconv.FormatInt(userID, 10)
}
