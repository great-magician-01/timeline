// Package agent 实现 AI 的任务调度、决策引擎、动作执行、分层记忆与调用记录，
// 并提供 AI 生成、草稿发布、任务处置、调用记录与全局开关的管理后台接口。
//
// 设计要点（docs/WORKFLOWS.md 第 7、8 章）：
//   - 入队前检查与执行时重新校验都要做，全部权限与可见性判断复用 internal/access
//     与 internal/social，本包不自行实现可见性规则；
//   - AI 只能使用公开内容与自身数据，候选内容一律 status='published' AND visibility='public'；
//   - AI 已发布的原创帖与引用帖恒为公开；评论与回复只发生在公开上下文；
//   - 默认全部关闭：ai_profiles.is_enabled 默认 false，全局开关缺失时视为关闭；
//   - 模型必须允许返回 noop；任务的每个终态都可在重启后幂等恢复；
//   - 记忆形成与调用记录写入失败不得回滚已经成功的动作。
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/access"
	"github.com/great-magician-01/timeline/internal/ai"
	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/audit"
	"github.com/great-magician-01/timeline/internal/content"
	"github.com/great-magician-01/timeline/internal/jobs"
	"github.com/great-magician-01/timeline/internal/model"
	"github.com/great-magician-01/timeline/internal/social"
)

// PoolProvider 与 auth、ai 等包保持同形，便于主代理统一装配。
type PoolProvider interface {
	Pool() *pgxpool.Pool
}

// IDGenerator 与应用侧雪花 ID 生成器同形。
type IDGenerator interface {
	Next() (int64, error)
}

// Service 承载 AI 任务的入队、执行、记忆与管理后台操作。
type Service struct {
	pools PoolProvider
	ids   IDGenerator
	log   *slog.Logger

	ai      *ai.Service
	content *content.Service
	social  *social.Service
	runner  *jobs.Runner
}

// NewService 构造 AI 任务服务；aiService、contentService、socialService 必须非空，
// runner 可为 nil（此时只入队 ai_tasks，不投递后台作业）。
func NewService(
	pools PoolProvider,
	ids IDGenerator,
	log *slog.Logger,
	aiService *ai.Service,
	contentService *content.Service,
	socialService *social.Service,
	runner *jobs.Runner,
) *Service {
	return &Service{
		pools:   pools,
		ids:     ids,
		log:     log,
		ai:      aiService,
		content: contentService,
		social:  socialService,
		runner:  runner,
	}
}

func (s *Service) pool(c *gin.Context) (*pgxpool.Pool, bool) {
	pool := s.pools.Pool()
	if pool == nil {
		api.Error(c, http.StatusServiceUnavailable, "service_unavailable", "服务尚未就绪，请稍后重试")
		return nil, false
	}
	return pool, true
}

func (s *Service) internalError(c *gin.Context, message string, err error) {
	s.log.Error(message, "err", err, "path", c.Request.URL.Path)
	api.Error(c, http.StatusInternalServerError, "internal_error", "服务暂时异常，请稍后重试")
}

// respond 统一把业务错误写回响应；不是业务错误时按内部错误处理。
func (s *Service) respond(c *gin.Context, message string, err error) {
	if !api.Respond(c, err) {
		s.internalError(c, message, err)
	}
}

// beginTx 开启事务；失败时已经写出 500 响应。
func (s *Service) beginTx(c *gin.Context, message string) (pgx.Tx, bool) {
	pool, ok := s.pool(c)
	if !ok {
		return nil, false
	}
	tx, err := pool.Begin(c.Request.Context())
	if err != nil {
		s.internalError(c, message, err)
		return nil, false
	}
	return tx, true
}

// commitTx 提交事务；失败时已经写出 500 响应。
func (s *Service) commitTx(c *gin.Context, tx pgx.Tx, message string) bool {
	if err := tx.Commit(c.Request.Context()); err != nil {
		s.internalError(c, message, err)
		return false
	}
	return true
}

// writeAudit 统一走 audit.Write，保证审计与业务变更处于同一事务。
func (s *Service) writeAudit(
	ctx context.Context,
	q access.Querier,
	adminID int64,
	action, targetType string,
	targetID *int64,
	detail any,
) error {
	return audit.Write(ctx, q, s.ids, adminID, action, targetType, targetID, detail)
}

// 任务状态（ai_tasks.status，与数据库 CHECK 约束一致）。
const (
	statusPending   = "pending"
	statusRunning   = "running"
	statusSucceeded = "succeeded"
	statusNoop      = "noop"
	statusRejected  = "rejected"
	statusFailed    = "failed"
	statusCanceled  = "canceled"
)

// 触发类型（ai_tasks.trigger_type）。
const (
	triggerManual    = "manual"
	triggerSchedule  = "schedule"
	triggerMention   = "mention"
	triggerComment   = "comment"
	triggerProactive = "proactive"
	triggerRetry     = "retry"
)

// AI 可执行动作（ai_tasks.action_type）。
const (
	actionOriginalPost = "original_post"
	actionComment      = "comment"
	actionReply        = "reply"
	actionLike         = "like"
	actionUnlike       = "unlike"
	actionFollow       = "follow"
	actionUnfollow     = "unfollow"
	actionRepost       = "repost"
	actionQuote        = "quote"
	actionNoop         = "noop"
)

// 动作目标类型。
const (
	targetPost    = "post"
	targetComment = "comment"
	targetUser    = "user"
	// targetAI 是审计与任务结果记录中“AI 自身内容”的目标类型。
	targetAI = "ai"
)

// 内容来源类型（mentions.source_type、ai_memories.source_ref_type）。
const (
	sourcePost    = "post"
	sourceComment = "comment"
)

// 候选范围（ai_behavior_settings.candidate_scope_type）。
const (
	scopeNone         = "none"
	scopeGlobalPublic = "global_public"
	scopeFollowed     = "followed"
	scopeTags         = "tags"
	scopeAuthors      = "authors"
)

// ai_tasks.result_ref_type：动作产生或影响的对象类型。
const (
	resultRefPost    = "post"
	resultRefComment = "comment"
	resultRefLike    = "like"
	resultRefFollow  = "follow"
	resultRefRepost  = "repost"
)

// 错误分类（写入 ai_tasks.error 前缀与 ai_call_logs.error_type）。
const (
	errorTypeInvalidJSON   = "invalid_json"
	errorTypeInvalidAction = "invalid_action"
	errorTypeInvalidTarget = "invalid_target"
	errorTypeForbidden     = "forbidden"
	errorTypeRateLimited   = "rate_limited"
	errorTypeTimeout       = "timeout"
	errorTypeTransport     = "transport"
	errorTypeAPI4xx        = "api_4xx"
	errorTypeAPI429        = "api_429"
	errorTypeAPI5xx        = "api_5xx"
	errorTypeDatabase      = "database"
	errorTypeConfig        = "config"
	errorTypeInternal      = "internal"
)

// 作业与重试约定。
const (
	// aiTaskJobRefType 是 ai_task 作业的引用类型。
	aiTaskJobRefType = "ai_task"
	// memoryJobRefType 是记忆整理作业的引用类型。
	memoryJobRefType = "ai"
	// maxTaskAttempts 是单个任务的最大执行次数；succeeded/noop/rejected 不重试。
	maxTaskAttempts = 3
	// aiTaskJobPrefix 是 ai_task 作业幂等键的前缀。
	aiTaskJobPrefix = "ai-task-job:"
)

// globalAIEnabledKey 是全局 AI 开关在 system_settings 中的键。
// 本包不 import internal/admin（admin 依赖 content/social，直接引用会形成循环依赖），
// 因此在此保留一份等价读取逻辑，与 internal/ai 的键名保持一致。
const globalAIEnabledKey = "ai.global_enabled"

// globalAIEnabled 读取全局 AI 开关；设置行缺失时默认关闭。
func (s *Service) globalAIEnabled(ctx context.Context, q access.Querier) (bool, error) {
	var enabled bool
	err := q.QueryRow(ctx, `SELECT value FROM system_settings WHERE key = $1`, globalAIEnabledKey).Scan(&enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return enabled, err
}

// aiViewer 返回以指定 AI 身份执行只读判断时的访问者结构。
// AI 访问者看不到非公开内容（access.CanViewPost 对 IsAI 且非公开一律拒绝）。
func aiViewer(aiID int64) model.Viewer {
	return model.Viewer{ID: aiID, Authenticated: true, IsAI: true}
}

// aiTask 是 ai_tasks 的运行时视图（执行与入队所需的列）。
type aiTask struct {
	ID             int64
	AID            int64
	TriggerType    string
	TriggerRefType string
	TriggerRefID   *int64
	ActionType     string
	Status         string
	Attempts       int
	IdempotencyKey string
}

const taskColumns = `id, ai_id, trigger_type, COALESCE(trigger_ref_type, ''), trigger_ref_id,
	action_type, status, attempts, idempotency_key`

func scanTask(row model.Row) (aiTask, error) {
	var task aiTask
	err := row.Scan(&task.ID, &task.AID, &task.TriggerType, &task.TriggerRefType,
		&task.TriggerRefID, &task.ActionType, &task.Status, &task.Attempts, &task.IdempotencyKey)
	return task, err
}

// loadTask 读取任务；不存在时返回 404（适用于管理接口）。
func loadTask(ctx context.Context, q access.Querier, taskID int64) (aiTask, error) {
	task, err := scanTask(q.QueryRow(ctx,
		`SELECT `+taskColumns+` FROM ai_tasks WHERE id = $1`, taskID))
	if errors.Is(err, pgx.ErrNoRows) {
		return aiTask{}, api.ErrNotFound("AI 任务不存在")
	}
	return task, err
}

// SourceRef 描述触发 AI 决策的来源内容（提及或评论事件）。
type SourceRef struct {
	Type string // post | comment
	ID   int64
}

// EnqueueInput 描述一次任务入队请求。
type EnqueueInput struct {
	AID            int64
	TriggerType    string
	TriggerRefType string
	TriggerRefID   *int64
	// ActionType 是入队时预期的动作；由模型决定动作的触发类型使用 noop 占位。
	ActionType     string
	IdempotencyKey string
	// Source 是触发事件的来源内容；提及与评论触发必须提供，用于执行前校验公开性。
	Source *SourceRef
}

// Enqueue 执行入队前检查并写入一条 pending 任务与对应后台作业。
// 返回 (taskID, "", nil) 表示已入队；(0, 原因, nil) 表示触发被丢弃（不进入模型调用）；
// 非空 error 表示数据库或配置读取失败。同一个幂等键重复入队时视为已存在，直接返回原因。
func (s *Service) Enqueue(ctx context.Context, q access.Querier, in EnqueueInput) (int64, string, error) {
	if in.AID <= 0 {
		return 0, "缺少 AI 账号", nil
	}
	if strings.TrimSpace(in.IdempotencyKey) == "" {
		return 0, "缺少幂等键", nil
	}
	reason, err := s.checkEnqueue(ctx, q, in)
	if err != nil {
		return 0, "", err
	}
	if reason != "" {
		return 0, reason, nil
	}

	taskID, err := s.ids.Next()
	if err != nil {
		return 0, "", fmt.Errorf("生成任务 ID 失败: %w", err)
	}
	actionType := in.ActionType
	if !validActionType(actionType) {
		actionType = actionNoop
	}
	var created int64
	err = q.QueryRow(ctx, `
		INSERT INTO ai_tasks (
			id, ai_id, trigger_type, trigger_ref_type, trigger_ref_id,
			action_type, status, idempotency_key
		) VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING id`,
		taskID, in.AID, in.TriggerType, nullString(in.TriggerRefType), in.TriggerRefID,
		actionType, in.IdempotencyKey,
	).Scan(&created)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "同一事件已有待执行或已完成的 AI 任务", nil
	}
	if err != nil {
		return 0, "", fmt.Errorf("写入 AI 任务失败: %w", err)
	}

	if err := s.enqueueAIJob(ctx, q, created); err != nil {
		return 0, "", err
	}
	return created, "", nil
}

// enqueueAIJob 投递执行任务的后台作业；runner 为 nil 时只保留 ai_tasks 记录。
func (s *Service) enqueueAIJob(ctx context.Context, q access.Querier, taskID int64) error {
	if s.runner == nil {
		return nil
	}
	return s.runner.Enqueue(ctx, q, jobs.EnqueueParams{
		Type:           jobs.TypeAITask,
		RefType:        aiTaskJobRefType,
		RefID:          &taskID,
		IdempotencyKey: aiTaskJobPrefix + api.FormatID(taskID),
		RunAt:          time.Now(),
		MaxAttempts:    maxTaskAttempts,
	})
}

// checkEnqueue 执行入队前检查，返回非空原因表示应当丢弃该触发。
func (s *Service) checkEnqueue(ctx context.Context, q access.Querier, in EnqueueInput) (string, error) {
	enabled, err := s.globalAIEnabled(ctx, q)
	if err != nil {
		return "", err
	}
	if !enabled {
		return "全局 AI 开关已关闭", nil
	}

	rt, err := s.ai.RuntimeByAI(ctx, q, in.AID)
	if err != nil {
		if isNotFound(err) {
			return "AI 账号不存在", nil
		}
		return "", err
	}
	if !rt.Profile.IsEnabled {
		return "该 AI 未启用", nil
	}
	actor, err := loadActor(ctx, q, in.AID)
	if err != nil {
		return "", err
	}
	if !actor.Active {
		return "该 AI 账号已停用", nil
	}

	switch in.TriggerType {
	case triggerProactive:
		if !proactiveAllowed(rt.Behavior) {
			return "该 AI 未开启任何主动互动动作", nil
		}
		if !validCandidateScope(rt.Behavior.CandidateScopeType) {
			return "该 AI 未配置有效的主动互动候选范围", nil
		}
	case triggerManual, triggerSchedule, triggerMention, triggerComment, triggerRetry:
		if !actionAllowedForTrigger(rt.Behavior, in.TriggerType, in.ActionType) {
			return "该 AI 未开启对应动作", nil
		}
	default:
		return "触发类型无效", nil
	}

	if in.Source != nil {
		return s.checkSource(ctx, q, in.AID, *in.Source, rt.Behavior)
	}
	return "", nil
}

// checkSource 校验触发来源是否公开可用，并检查 AI 间互动与账号状态。
func (s *Service) checkSource(
	ctx context.Context,
	q access.Querier,
	aiID int64,
	source SourceRef,
	behavior ai.BehaviorSettings,
) (string, error) {
	sourceCtx, err := s.loadSourceContext(ctx, q, aiID, source)
	if err != nil {
		if isNotFound(err) {
			return "来源内容不存在或已不可见", nil
		}
		return "", err
	}
	if sourceCtx.AuthorIsAI && !behavior.AllowAIToAI {
		return "AI 间互动未开启", nil
	}
	return "", nil
}

// 内部小工具。

// actor 是账号状态的最小视图。
type actor struct {
	ID        int64
	IsAI      bool
	Active    bool
	Nickname  string
	Username  string
	Suspended bool
}

// loadActor 读取账号昵称、用户名、是否 AI 与状态；不存在返回 404。
func loadActor(ctx context.Context, q access.Querier, userID int64) (actor, error) {
	var item actor
	var status string
	err := q.QueryRow(ctx, `
		SELECT id, COALESCE(nickname, ''), username, is_ai, status
		FROM users WHERE id = $1`, userID,
	).Scan(&item.ID, &item.Nickname, &item.Username, &item.IsAI, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return actor{}, api.ErrNotFound("用户不存在")
	}
	if err != nil {
		return actor{}, err
	}
	item.Active = status == "active"
	item.Suspended = !item.Active
	return item, nil
}

// isNotFound 判断错误是否为 api 的 404 not_found。
func isNotFound(err error) bool {
	return failureCode(err) == "not_found"
}

// failureCode 取出 *api.Failure 的错误码；不是业务错误时返回空串。
func failureCode(err error) string {
	var failure *api.Failure
	if errors.As(err, &failure) {
		return failure.Code
	}
	return ""
}

// nullString 把空串写成 SQL NULL，避免空字符串进入可空文本列。
func nullString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
