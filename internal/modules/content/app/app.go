// Package app 实现 content 模块的用例编排：事务边界、应用时钟取时、跨模块端口调用、
// 审计/通知/入队协作。所有 SQL 在 repo 层；本包不 import gin/pgx。
package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/content/domain"
	"github.com/great-magician-01/timeline/internal/modules/content/repo"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/platform/db"
	"github.com/great-magician-01/timeline/internal/platform/ratelimit"
	"github.com/great-magician-01/timeline/internal/shared/access"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/format"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// IDGenerator 与 platform/id 的雪花生成器同形。
type IDGenerator interface {
	Next() (int64, error)
}

// Deps 是 content 模块的全部外部依赖，由组合根装配。
type Deps struct {
	// Pools 提供运行期连接池；未就绪时 Pool() 返回 nil，业务请求会被 ready 中间件拦截。
	Pools db.PoolProvider
	// Tx 是事务入口：所有多表写入经 WithTx 编排，repo 通过 ctx 自动加入事务。
	Tx *db.TxManager
	// Clock 是唯一取时来源。
	Clock clock.Clock
	// IDs 生成帖子/版本/标签等雪花 ID。
	IDs IDGenerator
	// Log 用于内部错误与定时发布作业日志。
	Log *slog.Logger

	// Users 读取作者账号与摘要（auth 模块）。
	Users contracts.UserReader
	// Media 校验/读取附件并把附件挂到帖子（media 模块）。
	Media contracts.MediaService
	// Social 提供关注关系、访问者互动状态、回复与纯转发时间线（social 模块）。
	Social contracts.SocialService
	// Notice 写入提及/引用通知（notice 模块）。
	Notice contracts.NoticeService
	// Jobs 入队/取消定时发布作业（platform/jobs 适配器）。
	Jobs contracts.JobsQueue
	// Audit 写管理审计（platform/audit 适配器）。
	Audit contracts.AuditWriter
	// Settings 读取发帖限频等系统设置。
	Settings contracts.SettingsReader

	// PostLimiter 是发帖/编辑限频器；组合根未注入时 New 会按默认阈值兜底创建，
	// 不允许为 nil（nil 等于关闭发帖限频，违反安全红线）。
	PostLimiter *ratelimit.Limiter
}

// Service 是 content 模块的应用服务；同时实现 contracts.ContentService。
type Service struct {
	deps Deps
	repo *repo.Repo
}

// New 构造 content 应用服务；nil Clock 回退系统时钟（仅用于单元测试/特殊装配）。
// 发帖限频器始终存在：组合根未注入时按默认阈值兜底创建，避免装配遗漏导致限频静默失效。
func New(deps Deps) *Service {
	if deps.Clock == nil {
		deps.Clock = clock.System()
	}
	if deps.PostLimiter == nil {
		deps.PostLimiter = ratelimit.New(domain.DefaultPostRateLimit, domain.DefaultPostRateWindow)
	}
	return &Service{deps: deps, repo: repo.New(deps.Pools)}
}

// Contracts 编译期断言：content 生产者实现跨模块发布接口。
var _ contracts.ContentService = (*Service)(nil)

// Log 暴露日志器给同模块 http 层复用（记录内部错误）。
func (s *Service) Log() *slog.Logger { return s.deps.Log }

// Now 返回应用时钟当前时间（唯一取时来源，不使用 time.Now）。
func (s *Service) Now() time.Time {
	return s.deps.Clock.Now()
}

// nextID 生成雪花 ID。
func (s *Service) nextID() (int64, error) {
	id, err := s.deps.IDs.Next()
	if err != nil {
		return 0, apperr.Internal("生成资源 ID 失败")
	}
	return id, nil
}

// withTx 在事务中执行 fn；repo 通过 ctx 自动加入该事务。
func (s *Service) withTx(ctx context.Context, fn func(context.Context) error) error {
	return s.deps.Tx.WithTx(ctx, fn)
}

// wrap 把未预期错误转为领域内部错误并记录日志，已知领域错误原样返回。
func (s *Service) wrap(err error, message string) error {
	if err == nil {
		return nil
	}
	if _, ok := apperr.AsKind(err); ok {
		return err
	}
	if s.deps.Log != nil {
		s.deps.Log.Error(message, "err", err)
	}
	return apperr.Wrap(err, apperr.KindInternal, "服务暂时异常，请稍后重试")
}

// followingSet 读取访问者的生效关注集合（游客返回空集合）。
func (s *Service) followingSet(ctx context.Context, viewer model.Viewer) (map[int64]bool, error) {
	if viewer.ID <= 0 || s.deps.Social == nil {
		return map[int64]bool{}, nil
	}
	set, err := s.deps.Social.FollowingSet(ctx, viewer.ID)
	if err != nil {
		return nil, s.wrap(err, "读取关注集合失败")
	}
	return set, nil
}

// requireVisible 按访问者可见性校验；不可见返回 not_found。
func (s *Service) requireVisible(
	ctx context.Context,
	viewer model.Viewer,
	post model.Post,
) (map[int64]bool, error) {
	following, err := s.followingSet(ctx, viewer)
	if err != nil {
		return nil, err
	}
	if err := access.RequireVisiblePost(viewer, post, following); err != nil {
		return nil, err
	}
	return following, nil
}

// requireAuthor 校验访问者为作者：不可见返回 not_found，可见但非作者返回 forbidden。
func (s *Service) requireAuthor(
	ctx context.Context,
	viewer model.Viewer,
	post model.Post,
) (map[int64]bool, error) {
	following, err := s.followingSet(ctx, viewer)
	if err != nil {
		return nil, err
	}
	if post.SelfPosted(viewer) {
		return following, nil
	}
	if err := access.RequireVisiblePost(viewer, post, following); err != nil {
		return nil, err
	}
	return following, apperr.Forbidden("只有作者可以执行此操作")
}

// consumePostQuota 消耗一次发帖/编辑额度；超限返回带 RetryAfter 的领域限频错误。
// 阈值与窗口每次从系统设置读取，管理后台修改后立即生效；设置端口缺失或读取失败时使用默认值。
func (s *Service) consumePostQuota(ctx context.Context, userID int64) error {
	limit, window := domain.DefaultPostRateLimit, domain.DefaultPostRateWindow
	if s.deps.Settings != nil {
		if values, err := s.deps.Settings.Settings(ctx); err == nil {
			limit = values.PostRateLimit
			window = values.PostRateWindow
		}
	}
	allowed, retryAfter := s.deps.PostLimiter.ConsumeLimit("user:"+format.FormatID(userID), limit, window)
	if allowed {
		return nil
	}
	if retryAfter < time.Second {
		retryAfter = time.Second
	}
	return apperr.RateLimited(retryAfter)
}

// requireWritableViewer 校验写操作的最低条件。
func (s *Service) requireWritableViewer(viewer model.Viewer) error {
	return access.RequireWritable(viewer)
}

// mustInternal 包装内部错误（生成 ID 等）。
func (s *Service) mustInternal(err error, message string) error {
	if err == nil {
		return nil
	}
	if s.deps.Log != nil {
		s.deps.Log.Error(message, "err", err)
	}
	return apperr.Wrap(err, apperr.KindInternal, "服务暂时异常，请稍后重试")
}

// publicContentNotFound 把「帖子行不存在」统一映射为对外 not_found 文案，
// 与旧实现一致且不暴露资源存在性；其它错误原样返回。
func publicContentNotFound(err error) error {
	if err != nil && apperr.IsKind(err, apperr.KindNotFound) {
		return apperr.NotFound("内容不存在或无权访问")
	}
	return err
}
