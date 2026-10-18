// Package app 是 media 模块的用例层：上传、鉴权读取、逻辑删除、头像更新、
// 衍生文件作业与临时附件清理。用例通过 platform/db 的 TxManager 编排事务，
// 通过 contracts 端口调用 auth/content/jobs/audit/settings，不直接跨模块查表。
package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/media/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/platform/db"
	"github.com/great-magician-01/timeline/internal/platform/ratelimit"
	"github.com/great-magician-01/timeline/internal/platform/settings"
	"github.com/great-magician-01/timeline/internal/platform/storage"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// IDGenerator 生成雪花 ID。
type IDGenerator interface {
	Next() (int64, error)
}

// ContentPort 是 media 对 content 模块的最小依赖（由 content app 结构化实现）：
// 附件可见性继承所属帖子、失效流程的草稿/版本引用检查、清理任务的草稿引用过滤
// 都走这里，media 不查 posts / post_draft_attachments 表。
type ContentPort interface {
	// GetVisiblePost 按访问者校验帖子可见性；不可见返回领域 NotFound。
	GetVisiblePost(ctx context.Context, viewer model.Viewer, postID int64) (model.Post, error)
	// DetachAttachmentFromDrafts 解除附件与草稿工作区的生效关联，返回是否发生变更。
	DetachAttachmentFromDrafts(ctx context.Context, attachmentID int64, at time.Time) (bool, error)
	// AttachmentReferencedByVersion 判断附件是否仍被任何历史版本引用。
	AttachmentReferencedByVersion(ctx context.Context, attachmentID int64) (bool, error)
	// ActiveDraftAttachmentIDs 返回仍被生效草稿关联的附件 ID 子集。
	// 消费者：media 临时附件清理（替代直接查询 content 拥有的 post_draft_attachments）。
	ActiveDraftAttachmentIDs(ctx context.Context, attachmentIDs []int64) ([]int64, error)
}

// Deps 是 media 模块的构造依赖，由组合根（internal/app）注入。
type Deps struct {
	// Pools 提供运行期连接池；未就绪时 Pool() 返回 nil，业务请求由 ready 中间件拦截。
	Pools    db.PoolProvider
	Tx       *db.TxManager
	Clock    clock.Clock
	IDs      IDGenerator
	Log      *slog.Logger
	Storage  *storage.Store
	Jobs     contracts.JobsQueue
	Audit    contracts.AuditWriter
	Settings contracts.SettingsReader
	Auth     contracts.AuthService
	Content  ContentPort
}

// Service 是 media 用例服务；同时实现 contracts.MediaService 供 content/admin 调用。
type Service struct {
	pools    db.PoolProvider
	tx       *db.TxManager
	clk      clock.Clock
	ids      IDGenerator
	log      *slog.Logger
	store    *storage.Store
	jobs     contracts.JobsQueue
	audit    contracts.AuditWriter
	settings contracts.SettingsReader
	auth     contracts.AuthService
	content  ContentPort

	// uploadLimiter 限制单用户上传频率；阈值与窗口每次请求从系统设置读取，
	// 构造参数只作为未读取设置时的兜底（安全保护而非业务配额）。
	uploadLimiter *ratelimit.Limiter
}

// New 组装媒体用例服务；nil Clock 回退系统时钟，保证测试/手工构造不会空指针。
func New(deps Deps) *Service {
	if deps.Clock == nil {
		deps.Clock = clock.System()
	}
	defaults := defaultSystemSettings()
	return &Service{
		pools:         deps.Pools,
		tx:            deps.Tx,
		clk:           deps.Clock,
		ids:           deps.IDs,
		log:           deps.Log,
		store:         deps.Storage,
		jobs:          deps.Jobs,
		audit:         deps.Audit,
		settings:      deps.Settings,
		auth:          deps.Auth,
		content:       deps.Content,
		uploadLimiter: ratelimit.New(defaults.UploadRateLimit, defaults.UploadRateWindow),
	}
}

// exec 返回当前上下文对应的执行器：ctx 内有事务时用事务，否则用连接池。
func (s *Service) exec(ctx context.Context) db.Executor {
	if s.pools == nil {
		return db.ExecutorFrom(ctx, nil)
	}
	return db.ExecutorFrom(ctx, s.pools.Pool())
}

// settingsOrDefaults 读取系统设置；读取失败或未装配时回退代码内默认值（与旧实现一致）。
func (s *Service) settingsOrDefaults(ctx context.Context) contracts.SystemSettings {
	if s.settings == nil {
		return defaultSystemSettings()
	}
	values, err := s.settings.Settings(ctx)
	if err != nil {
		s.log.Warn("读取系统设置失败，使用默认值", "err", err)
		return defaultSystemSettings()
	}
	return values
}

// defaultSystemSettings 把 platform/settings 的默认值映射为跨模块快照。
func defaultSystemSettings() contracts.SystemSettings {
	values := settings.Defaults()
	return contracts.SystemSettings{
		RegistrationEnabled:     values.RegistrationEnabled,
		AIGlobalEnabled:         values.AIGlobalEnabled,
		MaxUploadBytes:          values.MaxUploadBytes,
		MaxRequestBytes:         values.MaxRequestBytes,
		StorageQuotaBytes:       values.StorageQuotaBytes,
		PostRateLimit:           values.PostRateLimit,
		PostRateWindow:          values.PostRateWindow,
		UploadRateLimit:         values.UploadRateLimit,
		UploadRateWindow:        values.UploadRateWindow,
		TempAttachmentRetention: values.TempAttachmentRetention,
		CallLogMaxBytes:         values.CallLogMaxBytes,
		CallLogRetentionDays:    values.CallLogRetentionDays,
	}
}

// internal 记录内部错误并返回统一的 500 领域错误；细节只进日志，不进响应。
func (s *Service) internal(message string, err error) error {
	s.log.Error(message, "err", err)
	return apperr.Wrap(err, apperr.KindInternal, "服务暂时异常，请稍后重试")
}

// wrap 保留调用方/下游模块返回的领域错误，只把未知错误包装为内部错误。
func (s *Service) wrap(message string, err error) error {
	if err == nil {
		return nil
	}
	if _, ok := apperr.AsKind(err); ok {
		return err
	}
	return s.internal(message, err)
}

// UploadLimits 返回 http 层读取 multipart 请求体前需要的上限：
// 请求体上限与单文件上限。设置读取失败时回退默认值。
func (s *Service) UploadLimits(ctx context.Context) (maxRequestBytes, maxUploadBytes int64) {
	values := s.settingsOrDefaults(ctx)
	return values.MaxRequestBytes, values.MaxUploadBytes
}

// 编译期断言：Service 实现 contracts.MediaService（content/admin 的消费端口）。
//
// 说明：ContentPort 含 ActiveDraftAttachmentIDs（由 content 模块新增并通过组合根注入），
// 因此不再断言 contracts.ContentService 直接满足该窄接口——contracts.ContentService
// 的接口清单不随 media 的局部需求扩张，保持 media 只依赖自身声明的窄端口。
var _ contracts.MediaService = (*Service)(nil)

// 保证 domain 常量在编译期被引用，避免误删（同时给读者定位用途定义）。
var _ = domain.PurposePost
