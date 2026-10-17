package app

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/ai/domain"
	"github.com/great-magician-01/timeline/internal/modules/ai/repo"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/platform/db"
	"github.com/great-magician-01/timeline/internal/platform/ratelimit"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// IDGenerator 生成雪花 ID；与 platform/id.Generator 同形，避免 app 依赖具体实现。
type IDGenerator interface {
	Next() (int64, error)
}

// Deps 是 ai 模块的全部外部依赖，由组合根（internal/app/wire.go）注入。
type Deps struct {
	// Pool 提供运行期连接池；数据库未就绪时由 ready 中间件拒绝业务请求。
	Pool db.PoolProvider
	Tx   *db.TxManager
	IDs  IDGenerator
	// Clock 是唯一取时入口；所有写库时间显式传入。
	Clock clock.Clock
	Log   *slog.Logger
	// SecretKey 是 32 字节对称密钥（config.Config.SecretKey），用于加密预设与搜索密钥。
	SecretKey []byte
	// Users 是 auth 发布的账号读端口（AI 列表/详情/运行时读取用户资料）。
	Users contracts.UserReader
	// Auth 是 auth 发布的账号写端口（创建 AI 账号、更新账号资料，不直接写 users 表）。
	Auth contracts.AuthUserWriter
	// Tasks 是 agent 发布的只读任务视图；未接线时列表 today_actions 为 0、详情 recent_tasks 为空。
	Tasks TaskReader
	// Agent 是 agent 发布的跨模块命令（停用 AI 时取消待执行任务）；可选。
	Agent contracts.AgentService
	// Jobs 是后台作业队列（停用 AI 时暂停 ref_type='ai' 的 pending 作业）；可选，
	// 未接线时只更新 AI 启用状态并取消 agent 任务。
	Jobs contracts.JobsQueue
	// Audit 写入管理审计日志（经 contracts 端口，与业务变更同事务）。
	Audit contracts.AuditWriter
	// Settings 读取系统设置（全局网页搜索配置，经 contracts 端口）。
	Settings contracts.SettingsReader
}

// Service 实现 ai 模块的管理用例与跨模块端口（contracts.AIService / contracts.SecretCipher）。
type Service struct {
	deps                Deps
	repo                *repo.Store
	providerTestLimiter *ratelimit.Limiter
}

// 预设连接测试的限频阈值：单个管理员每分钟最多 10 次。
const (
	providerTestRateLimit  = 10
	providerTestRateWindow = time.Minute
)

// New 构造 ai 模块 Service。
func New(deps Deps) *Service {
	return &Service{
		deps:                deps,
		repo:                repo.New(deps.Pool.Pool()),
		providerTestLimiter: ratelimit.New(providerTestRateLimit, providerTestRateWindow),
	}
}

// 编译期断言：ai 模块实现 contracts 中的生产者接口。
var (
	_ contracts.AIService    = (*Service)(nil)
	_ contracts.SecretCipher = (*Service)(nil)
)

// 审计目标类型。
const (
	targetModelProvider = "model_provider"
	targetAI            = "ai"
	targetAIMemory      = "ai_memory"
)

// now 返回应用时钟当前时间；一次事务只调用一次并显式传给 repo。
func (s *Service) now() time.Time { return s.deps.Clock.Now() }

// withTx 在事务中执行用例；repo 通过 db.ExecutorFrom 自动加入该事务。
func (s *Service) withTx(ctx context.Context, fn func(context.Context) error) error {
	return s.deps.Tx.WithTx(ctx, fn)
}

// newID 生成雪花 ID。
func (s *Service) newID() (int64, error) {
	id, err := s.deps.IDs.Next()
	if err != nil {
		return 0, internalError(err, "生成资源 ID 失败")
	}
	return id, nil
}

// writeAudit 写入审计；必须在业务事务内调用（与业务变更同生共死）。
func (s *Service) writeAudit(
	ctx context.Context,
	adminID int64,
	action, targetType string,
	targetID *int64,
	detail any,
	at time.Time,
) error {
	if err := s.deps.Audit.Write(ctx, contracts.AuditEntry{
		AdminID:    adminID,
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Detail:     detail,
		At:         at,
	}); err != nil {
		return internalError(err, "写入审计日志失败")
	}
	return nil
}

// ensureAIUser 确认目标是 AI 用户；非 AI 用户按 not_found 处理，不暴露存在性。
func (s *Service) ensureAIUser(ctx context.Context, aiID int64) error {
	user, err := s.deps.Users.GetUser(ctx, aiID)
	if err != nil {
		if apperr.IsKind(err, apperr.KindNotFound) {
			return apperr.NotFound("AI 用户不存在")
		}
		return internalError(err, "读取 AI 用户失败")
	}
	if !user.IsAI {
		return apperr.NotFound("AI 用户不存在")
	}
	return nil
}

// isUniqueViolation 判断 PostgreSQL 唯一索引冲突（并发下的兜底映射为 409）。
func isUniqueViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	return errors.As(err, &pgErr) && pgErr.SQLState() == "23505"
}

// internalError 把底层错误包装为内部错误，保留原因供日志与 errors.Is/As。
func internalError(err error, message string) error {
	return apperr.Wrap(err, apperr.KindInternal, "服务暂时异常，请稍后重试")
}

// validationError 把字段级校验详情转换为 validation_failed。
func validationError(details map[string]string) error {
	return apperr.Validation(firstDetail(details)).WithDetails(details)
}

// firstDetail 取字段校验详情中的第一条作为错误消息；map 顺序不保证，仅用于提示。
func firstDetail(details map[string]string) string {
	for _, message := range details {
		return message
	}
	return "请求参数不符合要求"
}

// notFoundIfNoRows 把 repo 的行不存在错误映射为 not_found。
func notFoundIfNoRows(err error, message string) error {
	if errors.Is(err, repo.ErrNotFound) {
		return apperr.NotFound(message)
	}
	if err != nil {
		return internalError(err, "读取数据失败")
	}
	return nil
}

// formatID 把雪花 ID 转字符串（日志与审计明细使用）。
func formatID(id int64) string { return strconv.FormatInt(id, 10) }

// ProviderItem 是预设列表/详情的数据视图（APIKeyMasked 绝不返回明文或密文）。
type ProviderItem struct {
	Provider     domain.Provider
	APIKeyMasked string
	AICount      int
}

// ProviderList 是预设列表结果。
type ProviderList struct {
	Items   []ProviderItem
	LastID  int64
	HasMore bool
}

// ModelResponse 是模型设置的数据视图。
type ModelResponse struct {
	Settings domain.ModelSettings
}

// AIListItem 是 AI 列表项的数据视图。
type AIListItem struct {
	User         model.UserBrief
	IsEnabled    bool
	ProviderID   *int64
	ProviderName string
	Model        string
	TodayActions int
	CreatedAt    time.Time
}

// AIList 是 AI 列表结果。
type AIList struct {
	Items   []AIListItem
	LastID  int64
	HasMore bool
}

// MemoryCounts 是 AI 记忆条数概览。
type MemoryCounts struct {
	Recent   int
	LongTerm int
}

// AIDetail 是 AI 详情的完整数据视图。
type AIDetail struct {
	User         model.UserBrief
	Bio          string
	Profile      domain.Profile
	Model        domain.ModelSettings
	Behavior     domain.BehaviorSettings
	BehaviorAt   time.Time
	RecentTasks  []TaskBrief
	MemoryCounts MemoryCounts
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
