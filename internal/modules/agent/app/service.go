// Package app 是 agent（AI 自主行为）用例层：任务调度、决策与动作执行、记忆形成/沉淀/
// 失效、调用记录、保留期清理、reconcile、联网搜索与管理用例。
//
// 依赖约定（目标架构）：
//   - 跨模块读写一律经 shared/contracts：ContentService / SocialService / AIService /
//     NoticeService / JobsQueue / AuditWriter / SettingsReader+Writer / AuthService；
//   - 事务由本层用 platform/db.WithTx 编排，跨模块端口通过 ctx 复用同一事务；
//   - 时间一律来自注入的 clock.Clock，repo 写入显式传时间，SQL 不出现数据库时间函数。
package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/great-magician-01/callable"

	"github.com/great-magician-01/timeline/internal/modules/agent/domain"
	"github.com/great-magician-01/timeline/internal/modules/agent/repo"
	"github.com/great-magician-01/timeline/internal/platform/aiclient"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/platform/jobs"
	"github.com/great-magician-01/timeline/internal/platform/settings"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

const (
	// taskLease 是 running 任务的租约：超过该时长仍未完成的任务可被重新领取。
	taskLease = 10 * time.Minute
	// maxCandidateCount 是单次候选内容上限。
	maxCandidateCount = 20
	// maxRecentMemoryCount 是注入上下文的近期记忆上限。
	maxRecentMemoryCount = 5
	// maxLongTermMemoryCount 是检索注入的长期记忆上限。
	maxLongTermMemoryCount = 5
	// consolidationThreshold 是触发长期记忆沉淀的近期记忆条数。
	consolidationThreshold = 10
	// callLogWriteTimeout 是调用记录写入超时（失败不得回滚业务动作）。
	callLogWriteTimeout = 5 * time.Second
	// schedulerTriggerWindow 是触发扫描时间窗。
	schedulerTriggerWindow = 30 * time.Minute
	// schedulerTriggerLimit 是单轮触发扫描上限。
	schedulerTriggerLimit = 20
)

// IDGenerator 生成雪花 ID（与 platform/id 同形）。
type IDGenerator interface {
	Next() (int64, error)
}

// TxRunner 是事务入口（*platform/db.TxManager 直接满足）。
type TxRunner interface {
	WithTx(ctx context.Context, fn func(context.Context) error) error
}

// TaskStore 是 agent 任务仓储（*repo.Repo 直接满足）。
type TaskStore interface {
	InsertTask(ctx context.Context, in repo.TaskInsert) (bool, error)
	GetTask(ctx context.Context, taskID int64) (repo.Task, error)
	ClaimTask(ctx context.Context, taskID int64, now time.Time, lease time.Duration) (int, bool, error)
	UpdateTaskResult(ctx context.Context, in repo.TaskResultUpdate) error
	SetTaskStatus(ctx context.Context, taskID int64, status, errorMessage string, finishedAt *time.Time) error
	ListTasks(ctx context.Context, filter repo.TaskFilter) ([]repo.Task, bool, error)
	ListPendingTasks(ctx context.Context, limit int) ([]repo.Task, error)
	AddMemoryRefs(ctx context.Context, taskID int64, refs []repo.MemoryRef, idSeed int64) error
	ListMemoryRefs(ctx context.Context, taskID int64) ([]repo.MemoryRef, error)
}

// CallLogStore 是调用记录仓储。
type CallLogStore interface {
	InsertCallLog(ctx context.Context, in repo.CallLogInsert) error
	ListCallLogs(ctx context.Context, filter repo.CallLogFilter) ([]repo.CallLog, bool, error)
	GetCallLog(ctx context.Context, logID int64) (repo.CallLog, error)
	MarkDeletedBefore(ctx context.Context, cutoff time.Time, limit int) (int, error)
}

// CounterStore 是动作计数仓储。
type CounterStore interface {
	BumpActionCounter(ctx context.Context, id, aiID int64, actionType string, day, at time.Time) error
	CountActions(ctx context.Context, aiID int64, actionType string, day time.Time) (int, error)
	TotalActions(ctx context.Context, aiID int64, day time.Time) (int, error)
	LastSucceededAt(ctx context.Context, aiID int64, triggerType string) (*time.Time, error)
}

// Deps 是 agent 模块的全部依赖。
type Deps struct {
	Clock clock.Clock
	IDs   IDGenerator
	Tx    TxRunner
	Log   *slog.Logger

	Tasks    TaskStore
	CallLogs CallLogStore
	Counters CounterStore

	Content contracts.ContentService
	Social  contracts.SocialService
	AI      contracts.AIService
	Notices contracts.NoticeService
	Users   contracts.UserReader

	Jobs  contracts.JobsQueue
	Audit contracts.AuditWriter

	Settings       contracts.SettingsReader
	SettingsWriter contracts.SettingsWriter
}

// Service 实现 agent 域用例。
type Service struct {
	deps Deps
}

// New 组装 agent 服务；nil Clock 回退系统时钟，nil Log 回退默认 logger。
func New(deps Deps) *Service {
	if deps.Clock == nil {
		deps.Clock = clock.System()
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	return &Service{deps: deps}
}

func (s *Service) now() time.Time { return s.deps.Clock.Now() }

func (s *Service) newID() (int64, error) {
	id, err := s.deps.IDs.Next()
	if err != nil || id <= 0 {
		return 0, apperr.Internal("生成 ID 失败")
	}
	return id, nil
}

// jobPayload 是 AI 任务/记忆沉淀作业的负载。
type jobPayload struct {
	TaskID int64 `json:"task_id,omitempty"`
	AIID   int64 `json:"ai_id,omitempty"`
}

// TaskView 是任务列表/详情返回结构（app 层视图，不含 JSON tag）。
type TaskView struct {
	Task       repo.Task
	MemoryRefs []repo.MemoryRef
}

// CallLogView 是调用记录视图。
type CallLogView = repo.CallLog

// DraftView 是 AI 草稿视图。
type DraftView struct {
	Post          model.Post
	ContentHTML   string
	PublishStatus string
	PublishError  string
	PendingRunAt  *time.Time
}

// GlobalState 是全局开关状态。
type GlobalState struct {
	Enabled bool
}

// GenerationInput 是管理员手动生成输入。
type GenerationInput struct {
	Mode        string
	ScheduledAt *time.Time
	Instruction string
}

// ===== 任务入队 =====

// EnqueueInput 描述一次任务入队。
type EnqueueInput struct {
	AID            int64
	TriggerType    string
	TriggerRefType string
	TriggerRefID   *int64
	ActionType     string
	IdempotencyKey string
	ScheduledAt    *time.Time
}

// Enqueue 写入任务并入队对应作业；同一幂等键已有任务时返回 (taskID, enqueued=false, nil)。
func (s *Service) Enqueue(ctx context.Context, in EnqueueInput) (int64, bool, error) {
	if in.AID <= 0 {
		return 0, false, apperr.Validation("缺少 AI 账号")
	}
	if in.IdempotencyKey == "" {
		return 0, false, apperr.Validation("缺少幂等键")
	}
	if !domain.ValidTrigger(in.TriggerType) {
		return 0, false, apperr.Validation("触发类型无效")
	}
	if !domain.ValidAction(in.ActionType) {
		return 0, false, apperr.Validation("动作类型无效")
	}
	if in.ScheduledAt != nil && !in.ScheduledAt.After(s.now()) {
		return 0, false, apperr.Validation("定时发布时间必须晚于当前时间")
	}
	taskID, err := s.newID()
	if err != nil {
		return 0, false, err
	}
	inserted, err := s.deps.Tasks.InsertTask(ctx, repo.TaskInsert{
		ID:             taskID,
		AIID:           in.AID,
		TriggerType:    in.TriggerType,
		TriggerRefType: in.TriggerRefType,
		TriggerRefID:   in.TriggerRefID,
		ActionType:     in.ActionType,
		IdempotencyKey: in.IdempotencyKey,
		ScheduledAt:    in.ScheduledAt,
		CreatedAt:      s.now(),
	})
	if err != nil {
		return 0, false, err
	}
	if !inserted {
		return 0, false, nil
	}
	if err := s.enqueueTaskJob(ctx, taskID); err != nil {
		return taskID, false, err
	}
	return taskID, true, nil
}

// enqueueTaskJob 投递 ai_task 作业（触发即执行；定时任务由作业 run_at 控制）。
func (s *Service) enqueueTaskJob(ctx context.Context, taskID int64) error {
	now := s.now()
	runAt := now
	task, err := s.deps.Tasks.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	if task.ScheduledAt != nil {
		runAt = *task.ScheduledAt
	}
	return s.deps.Jobs.Enqueue(ctx, contracts.JobEnqueue{
		Type:           contracts.JobTypeAITask,
		RefType:        domain.RefTask,
		RefID:          &taskID,
		IdempotencyKey: taskJobIdempotencyKey(taskID),
		Payload:        jobPayload{TaskID: taskID},
		RunAt:          runAt,
		MaxAttempts:    3,
		At:             now,
	})
}

// ===== 作业入口（由组合根注册） =====

// HandleAITaskJob 执行 ai_task 作业：领取任务 → 决策 → 动作 → 记忆/结果写回。
// 负载无效属于不可重试的处理完成，直接返回 nil，避免作业层按错误反复重试。
func (s *Service) HandleAITaskJob(ctx context.Context, job jobs.Job) error {
	var payload jobPayload
	if err := job.PayloadJSON(&payload); err != nil || payload.TaskID <= 0 {
		s.deps.Log.Warn("AI 任务作业负载无效，已忽略", "job_id", job.ID)
		return nil
	}
	return s.RunTask(ctx, payload.TaskID)
}

// HandleMemoryJob 执行记忆沉淀作业；负载无效视为处理完成，避免作业层重试。
func (s *Service) HandleMemoryJob(ctx context.Context, job jobs.Job) error {
	var payload jobPayload
	if err := job.PayloadJSON(&payload); err != nil || payload.AIID <= 0 {
		s.deps.Log.Warn("记忆沉淀作业负载无效，已忽略", "job_id", job.ID)
		return nil
	}
	return s.consolidateMemories(ctx, payload.AIID)
}

// RunCallLogCleanup 是调用记录保留期清理周期任务。
func (s *Service) RunCallLogCleanup(ctx context.Context) {
	values, err := s.deps.Settings.Settings(ctx)
	if err != nil {
		s.deps.Log.Warn("读取调用记录保留期失败，使用默认值", "err", err)
		values = contracts.SystemSettings{CallLogRetentionDays: settings.DefaultCallLogRetentionDays}
	}
	days := values.CallLogRetentionDays
	if days <= 0 {
		return
	}
	cutoff := s.now().Add(-time.Duration(days) * 24 * time.Hour)
	total := 0
	for {
		count, err := s.deps.CallLogs.MarkDeletedBefore(ctx, cutoff, 200)
		if err != nil {
			s.deps.Log.Error("清理调用记录失败", "err", err)
			return
		}
		total += count
		if count < 200 {
			break
		}
	}
	if total > 0 {
		s.deps.Log.Info("调用记录清理完成", "count", total, "retention_days", days)
	}
}

// ===== 动作计数与限额 =====

// bumpCounter 累加当日动作计数。
func (s *Service) bumpCounter(ctx context.Context, aiID int64, action string, at time.Time) error {
	id, err := s.newID()
	if err != nil {
		return err
	}
	return s.deps.Counters.BumpActionCounter(ctx, id, aiID, action, clock.DayStart(at), at)
}

// limitReached 检查每日上限与冷却时间；返回非空字符串表示应跳过本次动作。
func (s *Service) limitReached(ctx context.Context, aiID int64, behavior contracts.BehaviorSettings,
	trigger, action string, at time.Time) (string, error) {
	if limit := domain.DailyLimit(behavior, action); limit > 0 {
		count, err := s.deps.Counters.CountActions(ctx, aiID, action, clock.DayStart(at))
		if err != nil {
			return "", err
		}
		if !domain.UnderDailyLimit(count, limit) {
			return "已达到该动作的每日上限", nil
		}
	}
	if limit := domain.DailyLimit(behavior, "total"); limit > 0 {
		total, err := s.deps.Counters.TotalActions(ctx, aiID, clock.DayStart(at))
		if err != nil {
			return "", err
		}
		if !domain.UnderDailyLimit(total, limit) {
			return "已达到每日动作总上限", nil
		}
	}
	if behavior.CooldownSeconds > 0 && trigger != domain.TriggerRetry {
		last, err := s.deps.Counters.LastSucceededAt(ctx, aiID, trigger)
		if err != nil {
			return "", err
		}
		if last != nil && !domain.CooldownElapsed(*last, at, behavior.CooldownSeconds) {
			return "冷却时间未到", nil
		}
	}
	return "", nil
}

// CancelPendingTasksForActor 实现 contracts.AgentService：取消某个 AI 账号的待执行任务。
func (s *Service) CancelPendingTasksForActor(ctx context.Context, in contracts.CancelTasksInput) (int, error) {
	if in.AIID <= 0 {
		return 0, apperr.Validation("缺少 AI 账号")
	}
	at := in.At
	if at.IsZero() {
		at = s.now()
	}
	canceled := 0
	for {
		tasks, _, err := s.deps.Tasks.ListTasks(ctx, repo.TaskFilter{
			AIID: in.AIID, Status: domain.TaskStatusPending, Limit: 200,
		})
		if err != nil {
			return canceled, err
		}
		if len(tasks) == 0 {
			return canceled, nil
		}
		for _, task := range tasks {
			if err := s.deps.Tasks.SetTaskStatus(ctx, task.ID, domain.TaskStatusCanceled, "账号处置取消", &at); err != nil {
				return canceled, err
			}
			canceled++
		}
	}
}

// ===== 全局开关 =====

// GlobalState 读取全局 AI 开关。
func (s *Service) GlobalState(ctx context.Context) (GlobalState, error) {
	values, err := s.deps.Settings.Settings(ctx)
	if err != nil {
		return GlobalState{}, err
	}
	return GlobalState{Enabled: values.AIGlobalEnabled}, nil
}

// SetGlobal 打开/关闭全局 AI 开关；关闭时取消全部待执行任务并暂停待执行作业（逻辑状态，不删除记录）。
func (s *Service) SetGlobal(ctx context.Context, adminID int64, enabled bool) error {
	at := s.now()
	if !enabled {
		if err := s.cancelAllPendingTasks(ctx, "全局 AI 开关关闭", at); err != nil {
			return err
		}
		// ai_task 作业的 ref_type 是任务类型、ref_id 是任务 ID：按作业类型整体暂停。
		if _, err := s.deps.Jobs.PauseAllPending(ctx, contracts.PauseAllPendingInput{
			JobType: contracts.JobTypeAITask,
			RefType: domain.RefTask,
			At:      at,
		}); err != nil {
			return err
		}
		// 调度（ai_schedule）与记忆沉淀作业的 ref_type='ai'、ref_id 是 AI 账号 ID：
		// 暂停该引用类型下全部 pending 作业（等价旧实现按 AI 作业类型整体暂停）。
		if _, err := s.deps.Jobs.PauseAllPending(ctx, contracts.PauseAllPendingInput{
			RefType: domain.RefAI,
			At:      at,
		}); err != nil {
			return err
		}
	}
	if err := s.deps.SettingsWriter.Set(ctx, contracts.SettingsWrite{
		Key:     contracts.SettingKeyAIGlobalEnabled,
		Value:   enabled,
		AdminID: adminID,
		At:      at,
	}); err != nil {
		return err
	}
	action := contracts.AuditActionGlobalAIStop
	if enabled {
		action = contracts.AuditActionGlobalAIStart
	}
	return s.deps.Audit.Write(ctx, contracts.AuditEntry{
		AdminID:    adminID,
		Action:     action,
		TargetType: "system_settings",
		Detail:     map[string]any{"enabled": enabled},
		At:         at,
	})
}

// cancelAllPendingTasks 按批循环取消全部 pending 任务，避免只处理首批 200 条而遗漏。
func (s *Service) cancelAllPendingTasks(ctx context.Context, reason string, at time.Time) error {
	for {
		pending, err := s.deps.Tasks.ListPendingTasks(ctx, 200)
		if err != nil {
			return err
		}
		if len(pending) == 0 {
			return nil
		}
		for _, task := range pending {
			if err := s.deps.Tasks.SetTaskStatus(ctx, task.ID, domain.TaskStatusCanceled, reason, &at); err != nil {
				return err
			}
		}
	}
}

// ===== 工具 =====

// truncateRunes 按字符截断字符串。
func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

// marshalJSON 序列化调用记录参数（失败时返回空对象）。
func marshalJSON(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		return []byte("{}")
	}
	return encoded
}

// decisionFormat 构造决策结构化输出格式。
func decisionFormat() callable.ResponseFormat {
	return callable.JSONSchema("ai_decision", domain.DecisionSchema(), true)
}

// classifyModelError 把模型调用错误映射为任务终态与错误类型：
// 429/5xx/传输层失败可有限重试；其它 4xx（鉴权/参数/不支持工具调用）按 rejected 不重试；
// 超时按可重试失败；上下文取消按 canceled（docs/WORKFLOWS.md 7.5、7.6）。
func classifyModelError(err error) (status, errorType string, retryable bool) {
	if err == nil {
		return domain.TaskStatusSucceeded, "", false
	}
	var apiErr *aiclient.APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.StatusCode == 0:
			return domain.TaskStatusFailed, domain.ErrorTypeNetwork, true
		case apiErr.StatusCode == 429 || apiErr.StatusCode >= 500:
			return domain.TaskStatusFailed, domain.ErrorTypeNetwork, true
		case apiErr.StatusCode == 401 || apiErr.StatusCode == 403 || apiErr.StatusCode == 404:
			return domain.TaskStatusRejected, domain.ErrorTypeForbidden, false
		default:
			return domain.TaskStatusRejected, domain.ErrorTypeModel, false
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return domain.TaskStatusFailed, domain.ErrorTypeNetwork, true
	}
	if errors.Is(err, context.Canceled) {
		return domain.TaskStatusCanceled, "", false
	}
	return domain.TaskStatusFailed, domain.ErrorTypeNetwork, true
}

// modelForRuntime 返回一次调用实际使用的模型名：AI 的 model_override 优先，
// 为空时回退预设默认模型（docs/WORKFLOWS.md 7.4 第 6 条）。
func modelForRuntime(rt contracts.Runtime) string {
	if model := strings.TrimSpace(rt.Model); model != "" {
		return model
	}
	return strings.TrimSpace(rt.Provider.DefaultModel)
}

// newModelClient 按 AI 运行时快照构造模型客户端；实际模型名使用 modelForRuntime。
func (s *Service) newModelClient(rt contracts.Runtime) *aiclient.ChatClient {
	provider := aiclient.ProviderConfig{
		ID:           rt.Provider.ID,
		Name:         rt.Provider.Name,
		BaseURL:      rt.Provider.BaseURL,
		APIKey:       rt.Provider.APIKey,
		DefaultModel: modelForRuntime(rt),
		IsEnabled:    rt.Provider.IsEnabled,
	}
	return aiclient.NewChatClient(provider, aiclient.GenerationParams{
		MaxTokens:   rt.Params.MaxTokens,
		Temperature: rt.Params.Temperature,
		TopP:        rt.Params.TopP,
		Stop:        rt.Params.Stop,
	}, rt.Timeout)
}

// callModelRound 执行一轮模型请求并写入一条调用记录（工具轮的每一轮各自成条）。
// 调用记录不包含 API Key 或请求头；请求/响应按上限截断。
func (s *Service) callModelRound(ctx context.Context, rt contracts.Runtime, taskID int64,
	request *callable.Request, searchRound int, webSearch bool) (*callable.Response, error) {
	if request == nil {
		return nil, apperr.Internal("模型请求为空")
	}
	client := s.newModelClient(rt)
	model := modelForRuntime(rt)
	startedAt := s.now()
	resp, err := client.Create(ctx, request)
	finishedAt := s.now()
	params := map[string]any{"max_tokens": rt.Params.MaxTokens, "search_round": searchRound}
	if rt.Params.Temperature != nil {
		params["temperature"] = *rt.Params.Temperature
	}
	if rt.Params.TopP != nil {
		params["top_p"] = *rt.Params.TopP
	}
	if webSearch {
		params["web_search"] = true
	}
	logEntry := repo.CallLogInsert{
		TaskID:         &taskID,
		AIID:           &rt.AIID,
		ProviderID:     ptrInt64(rt.Provider.ID),
		Model:          model,
		Params:         marshalJSON(params),
		RequestContent: truncateRunes(renderRequestContent(request), 8000),
		Status:         "success",
		Attempt:        1,
		LatencyMS:      finishedAt.Sub(startedAt).Milliseconds(),
		StartedAt:      startedAt,
		FinishedAt:     finishedAt,
		CreatedAt:      finishedAt,
	}
	if err != nil {
		logEntry.Status = "error"
		logEntry.ErrorType = domain.ErrorTypeNetwork
		message := err.Error()
		if apiErr, ok := err.(*aiclient.APIError); ok && apiErr.StatusCode > 0 {
			code := apiErr.StatusCode
			logEntry.HTTPStatus = &code
			switch {
			case code == 429:
				logEntry.ErrorType = domain.ErrorTypeRateLimited
			case code >= 400 && code < 500:
				logEntry.ErrorType = domain.ErrorTypeForbidden
			default:
				logEntry.ErrorType = domain.ErrorTypeNetwork
			}
		}
		logEntry.ErrorMessage = truncateRunes(message, 1000)
	} else if resp != nil {
		logEntry.ResponseContent = truncateRunes(renderResponseContent(resp), 8000)
		logEntry.UsageInputTokens = ptrInt(resp.Usage.InputTokens)
		logEntry.UsageOutputTokens = ptrInt(resp.Usage.OutputTokens)
		logEntry.UsageReasoningTokens = ptrInt(resp.Usage.ReasoningTokens)
	}
	logID, idErr := s.deps.IDs.Next()
	if idErr == nil {
		logEntry.ID = logID
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), callLogWriteTimeout)
		if writeErr := s.deps.CallLogs.InsertCallLog(writeCtx, logEntry); writeErr != nil {
			s.deps.Log.Warn("写入调用记录失败", "err", writeErr)
		}
		cancel()
	}
	if err != nil {
		return nil, apperr.Wrap(err, apperr.KindInternal, "模型调用失败")
	}
	return resp, nil
}

// renderRequestContent 把请求消息渲染为可存档文本；只包含文本与工具调用，
// 不含 ThinkingPart、请求头或 API Key。
func renderRequestContent(req *callable.Request) string {
	if req == nil {
		return ""
	}
	var builder strings.Builder
	for _, message := range req.Messages {
		builder.WriteString("[")
		builder.WriteString(string(message.Role))
		builder.WriteString("]\n")
		for _, part := range message.Parts {
			switch current := part.(type) {
			case callable.TextPart:
				builder.WriteString(current.Text)
			case callable.ToolCallPart:
				builder.WriteString("[tool_call name=" + current.Name + "] ")
				builder.WriteString(current.Arguments)
				builder.WriteString("\n")
			case callable.ToolResultPart:
				builder.WriteString("[tool_result name=" + current.Name + "] ")
				builder.WriteString(current.Content)
				builder.WriteString("\n")
			}
		}
		builder.WriteString("\n")
	}
	return strings.TrimSpace(builder.String())
}

// renderResponseContent 记录模型文本与工具调用；工具轮通常没有文本。
func renderResponseContent(resp *callable.Response) string {
	if resp == nil {
		return ""
	}
	var builder strings.Builder
	builder.WriteString(resp.Text)
	for _, call := range resp.ToolCalls {
		if builder.Len() > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString("[tool_call name=" + call.Name + "] ")
		builder.WriteString(call.Arguments)
	}
	return strings.TrimSpace(builder.String())
}

func ptrInt(value int) *int {
	if value <= 0 {
		return nil
	}
	return &value
}

func ptrInt64(value int64) *int64 {
	if value <= 0 {
		return nil
	}
	return &value
}

// derefString 取指针字符串（nil 返回空串）。
func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
