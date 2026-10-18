// Package repo 是 agent 模块唯一的 SQL 层：ai_tasks、ai_call_logs、ai_action_counters、
// ai_task_memory_refs。所有写入时间由 app 从 clock 取时后显式传入；
// SQL 中不出现 now()/CURRENT_DATE/::date/interval 等数据库时间函数。
package repo

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/db"
)

// Repo 持有连接池；每次查询通过 db.ExecutorFrom 自动加入 app 编排的事务。
type Repo struct {
	pool *pgxpool.Pool
}

// New 构造 agent 仓储。
func New(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

func (r *Repo) ex(ctx context.Context) db.Executor { return db.ExecutorFrom(ctx, r.pool) }

// scanner 同时覆盖 pgx.Row 与 pgx.Rows 的 Scan 方法。
type scanner interface{ Scan(dest ...any) error }

// Task 是 ai_tasks 行结构（仅 app 层可见的读取投影）。
type Task struct {
	ID              int64
	AIID            int64
	TriggerType     string
	TriggerRefType  string
	TriggerRefID    *int64
	ActionType      string
	Status          string
	DecisionSummary string
	TargetType      string
	TargetID        *int64
	ResultRefType   string
	ResultRefID     *int64
	ProviderID      *int64
	Model           string
	Error           string
	Attempts        int
	IdempotencyKey  string
	ScheduledAt     *time.Time
	StartedAt       *time.Time
	FinishedAt      *time.Time
	CreatedAt       time.Time
}

// TaskInsert 描述一条待执行任务。
type TaskInsert struct {
	ID             int64
	AIID           int64
	TriggerType    string
	TriggerRefType string
	TriggerRefID   *int64
	ActionType     string
	IdempotencyKey string
	ScheduledAt    *time.Time
	CreatedAt      time.Time
}

// TaskFilter 是任务列表过滤条件。
type TaskFilter struct {
	AIID        int64
	TriggerType string
	ActionType  string
	Status      string
	Cursor      int64
	Limit       int
}

// TaskResultUpdate 描述一次任务终态写回。
type TaskResultUpdate struct {
	TaskID          int64
	Status          string
	ActionType      string
	DecisionSummary string
	TargetType      string
	TargetID        *int64
	ResultRefType   string
	ResultRefID     *int64
	ProviderID      *int64
	Model           string
	Error           string
	FinishedAt      time.Time
}

// CallLog 是 ai_call_logs 行结构。
type CallLog struct {
	ID                   int64
	TaskID               *int64
	AIID                 *int64
	ProviderID           *int64
	Model                string
	Params               []byte
	RequestContent       string
	ResponseContent      string
	Status               string
	HTTPStatus           *int
	ErrorType            string
	ErrorMessage         string
	UsageInputTokens     *int
	UsageOutputTokens    *int
	UsageReasoningTokens *int
	Attempt              int
	LatencyMS            int64
	Truncated            bool
	StartedAt            time.Time
	FinishedAt           time.Time
	CreatedAt            time.Time
}

// CallLogInsert 描述一条调用记录。
type CallLogInsert struct {
	ID                   int64
	TaskID               *int64
	AIID                 *int64
	ProviderID           *int64
	Model                string
	Params               []byte
	RequestContent       string
	ResponseContent      string
	Status               string
	HTTPStatus           *int
	ErrorType            string
	ErrorMessage         string
	UsageInputTokens     *int
	UsageOutputTokens    *int
	UsageReasoningTokens *int
	Attempt              int
	LatencyMS            int64
	Truncated            bool
	StartedAt            time.Time
	FinishedAt           time.Time
	CreatedAt            time.Time
}

// CallLogFilter 是调用记录列表过滤条件。
type CallLogFilter struct {
	AIID       int64
	ProviderID int64
	TaskID     int64
	Status     string
	ErrorType  string
	From       *time.Time
	To         *time.Time
	Cursor     int64
	Limit      int
}

// MemoryRef 是 ai_task_memory_refs 行。
type MemoryRef struct {
	MemoryID   int64
	MemoryType string
}

const taskColumns = `id, ai_id, trigger_type, COALESCE(trigger_ref_type, ''), trigger_ref_id,
	action_type, status, decision_summary, COALESCE(target_type, ''), target_id,
	COALESCE(result_ref_type, ''), result_ref_id, provider_id, model, error,
	attempts, idempotency_key, scheduled_at, started_at, finished_at, created_at`

func scanTask(row scanner) (Task, error) {
	var task Task
	err := row.Scan(&task.ID, &task.AIID, &task.TriggerType, &task.TriggerRefType, &task.TriggerRefID,
		&task.ActionType, &task.Status, &task.DecisionSummary, &task.TargetType, &task.TargetID,
		&task.ResultRefType, &task.ResultRefID, &task.ProviderID, &task.Model, &task.Error,
		&task.Attempts, &task.IdempotencyKey, &task.ScheduledAt, &task.StartedAt, &task.FinishedAt,
		&task.CreatedAt)
	return task, err
}

// InsertTask 写入 pending 任务；幂等键冲突时返回 inserted=false。
func (r *Repo) InsertTask(ctx context.Context, in TaskInsert) (bool, error) {
	tag, err := r.ex(ctx).Exec(ctx, `
		INSERT INTO ai_tasks (
			id, ai_id, trigger_type, trigger_ref_type, trigger_ref_id,
			action_type, status, idempotency_key, scheduled_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7, $8, $9)
		ON CONFLICT (idempotency_key) DO NOTHING`,
		in.ID, in.AIID, in.TriggerType, nullString(in.TriggerRefType), in.TriggerRefID,
		in.ActionType, in.IdempotencyKey, in.ScheduledAt, in.CreatedAt)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// GetTask 读取任务；不存在返回领域 NotFound。
func (r *Repo) GetTask(ctx context.Context, taskID int64) (Task, error) {
	task, err := scanTask(r.ex(ctx).QueryRow(ctx, `SELECT `+taskColumns+` FROM ai_tasks WHERE id = $1`, taskID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, apperr.NotFound("AI 任务不存在")
	}
	return task, err
}

// ClaimTask 把任务置为 running（attempts+1）；租约超时的 running 任务可被重新领取。
func (r *Repo) ClaimTask(ctx context.Context, taskID int64, now time.Time, lease time.Duration) (int, bool, error) {
	var attempts int
	err := r.ex(ctx).QueryRow(ctx, `
		UPDATE ai_tasks
		SET status = 'running', started_at = $2, attempts = attempts + 1
		WHERE id = $1 AND (
			status IN ('pending', 'failed')
			OR (status = 'running' AND started_at < $3)
		)
		RETURNING attempts`, taskID, now, now.Add(-lease)).Scan(&attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return attempts, true, nil
}

// UpdateTaskResult 写回任务终态。
func (r *Repo) UpdateTaskResult(ctx context.Context, in TaskResultUpdate) error {
	_, err := r.ex(ctx).Exec(ctx, `
		UPDATE ai_tasks
		SET status = $2, action_type = COALESCE(NULLIF($3, ''), action_type),
		    decision_summary = $4, target_type = $5, target_id = $6,
		    result_ref_type = $7, result_ref_id = $8, provider_id = $9,
		    model = $10, error = $11, finished_at = $12
		WHERE id = $1`,
		in.TaskID, in.Status, in.ActionType, in.DecisionSummary, nullString(in.TargetType),
		in.TargetID, nullString(in.ResultRefType), in.ResultRefID, in.ProviderID,
		in.Model, in.Error, in.FinishedAt)
	return err
}

// SetTaskStatus 直接写任务状态（重试/取消/失败兜底）。
func (r *Repo) SetTaskStatus(ctx context.Context, taskID int64, status, errorMessage string, finishedAt *time.Time) error {
	_, err := r.ex(ctx).Exec(ctx, `
		UPDATE ai_tasks SET status = $2, error = $3, finished_at = $4 WHERE id = $1`,
		taskID, status, errorMessage, finishedAt)
	return err
}

// ListTasks 按过滤条件分页读取任务（id 倒序，多查一条判断 hasMore）。
func (r *Repo) ListTasks(ctx context.Context, filter TaskFilter) ([]Task, bool, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	ctxSQL := `SELECT ` + taskColumns + ` FROM ai_tasks WHERE 1 = 1`
	args := make([]any, 0, 6)
	addArg := func(value any) string {
		args = append(args, value)
		return "$" + itoa(len(args))
	}
	if filter.AIID > 0 {
		ctxSQL += ` AND ai_id = ` + addArg(filter.AIID)
	}
	if filter.TriggerType != "" {
		ctxSQL += ` AND trigger_type = ` + addArg(filter.TriggerType)
	}
	if filter.ActionType != "" {
		ctxSQL += ` AND action_type = ` + addArg(filter.ActionType)
	}
	if filter.Status != "" {
		ctxSQL += ` AND status = ` + addArg(filter.Status)
	}
	if filter.Cursor > 0 {
		ctxSQL += ` AND id < ` + addArg(filter.Cursor)
	}
	ctxSQL += ` ORDER BY id DESC LIMIT ` + addArg(limit+1)
	rows, err := r.ex(ctx).Query(ctx, ctxSQL, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	tasks := make([]Task, 0, limit+1)
	for rows.Next() {
		task, scanErr := scanTask(rows)
		if scanErr != nil {
			return nil, false, scanErr
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(tasks) > limit
	if hasMore {
		tasks = tasks[:limit]
	}
	return tasks, hasMore, nil
}

// ListPendingTasks 读取 pending 任务（reconcile 补投作业使用）。
func (r *Repo) ListPendingTasks(ctx context.Context, limit int) ([]Task, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.ex(ctx).Query(ctx, `
		SELECT `+taskColumns+` FROM ai_tasks WHERE status = 'pending' ORDER BY id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := make([]Task, 0, limit)
	for rows.Next() {
		task, scanErr := scanTask(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

// AddMemoryRefs 写入任务注入的记忆引用。
func (r *Repo) AddMemoryRefs(ctx context.Context, taskID int64, refs []MemoryRef, idSeed int64) error {
	for index, ref := range refs {
		if _, err := r.ex(ctx).Exec(ctx, `
			INSERT INTO ai_task_memory_refs (id, task_id, memory_id, memory_type)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (task_id, memory_id, memory_type) DO NOTHING`,
			idSeed+int64(index), taskID, ref.MemoryID, ref.MemoryType); err != nil {
			return err
		}
	}
	return nil
}

// ListMemoryRefs 读取任务注入的记忆引用。
func (r *Repo) ListMemoryRefs(ctx context.Context, taskID int64) ([]MemoryRef, error) {
	rows, err := r.ex(ctx).Query(ctx, `
		SELECT memory_id, memory_type FROM ai_task_memory_refs WHERE task_id = $1 ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := make([]MemoryRef, 0)
	for rows.Next() {
		var ref MemoryRef
		if err := rows.Scan(&ref.MemoryID, &ref.MemoryType); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

// BumpActionCounter 原子累加某 AI 某动作的当日计数。
func (r *Repo) BumpActionCounter(ctx context.Context, id, aiID int64, actionType string, day, at time.Time) error {
	_, err := r.ex(ctx).Exec(ctx, `
		INSERT INTO ai_action_counters (id, ai_id, action_type, counter_date, count, updated_at)
		VALUES ($1, $2, $3, $4, 1, $5)
		ON CONFLICT (ai_id, action_type, counter_date)
		DO UPDATE SET count = ai_action_counters.count + 1, updated_at = EXCLUDED.updated_at`,
		id, aiID, actionType, day, at)
	return err
}

// CountActions 读取某 AI 某动作当日计数。
func (r *Repo) CountActions(ctx context.Context, aiID int64, actionType string, day time.Time) (int, error) {
	var count int
	err := r.ex(ctx).QueryRow(ctx, `
		SELECT COALESCE(SUM(count), 0) FROM ai_action_counters
		WHERE ai_id = $1 AND action_type = $2 AND counter_date = $3`, aiID, actionType, day).Scan(&count)
	return count, err
}

// TotalActions 读取某 AI 当日全部动作计数。
func (r *Repo) TotalActions(ctx context.Context, aiID int64, day time.Time) (int, error) {
	var count int
	err := r.ex(ctx).QueryRow(ctx, `
		SELECT COALESCE(SUM(count), 0) FROM ai_action_counters
		WHERE ai_id = $1 AND counter_date = $2`, aiID, day).Scan(&count)
	return count, err
}

// LastSucceededAt 读取某 AI 某触发类型最近成功任务完成时间。
func (r *Repo) LastSucceededAt(ctx context.Context, aiID int64, triggerType string) (*time.Time, error) {
	var last *time.Time
	err := r.ex(ctx).QueryRow(ctx, `
		SELECT MAX(finished_at) FROM ai_tasks
		WHERE ai_id = $1 AND trigger_type = $2 AND status IN ('succeeded', 'noop')`,
		aiID, triggerType).Scan(&last)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return last, err
}

const callLogColumns = `id, task_id, ai_id, provider_id, model, params, request_content,
	response_content, status, http_status, error_type, error_message,
	usage_input_tokens, usage_output_tokens, usage_reasoning_tokens,
	attempt, latency_ms, truncated, started_at, finished_at, created_at`

func scanCallLog(row scanner) (CallLog, error) {
	var log CallLog
	err := row.Scan(&log.ID, &log.TaskID, &log.AIID, &log.ProviderID, &log.Model, &log.Params,
		&log.RequestContent, &log.ResponseContent, &log.Status, &log.HTTPStatus, &log.ErrorType,
		&log.ErrorMessage, &log.UsageInputTokens, &log.UsageOutputTokens, &log.UsageReasoningTokens,
		&log.Attempt, &log.LatencyMS, &log.Truncated, &log.StartedAt, &log.FinishedAt, &log.CreatedAt)
	return log, err
}

// InsertCallLog 写入一条调用记录。
func (r *Repo) InsertCallLog(ctx context.Context, in CallLogInsert) error {
	params := in.Params
	if len(params) == 0 {
		params = []byte("{}")
	}
	_, err := r.ex(ctx).Exec(ctx, `
		INSERT INTO ai_call_logs (
			id, task_id, ai_id, provider_id, model, params, request_content, response_content,
			status, http_status, error_type, error_message,
			usage_input_tokens, usage_output_tokens, usage_reasoning_tokens,
			attempt, latency_ms, truncated, started_at, finished_at, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)`,
		in.ID, in.TaskID, in.AIID, in.ProviderID, in.Model, params, in.RequestContent, in.ResponseContent,
		in.Status, in.HTTPStatus, in.ErrorType, in.ErrorMessage,
		in.UsageInputTokens, in.UsageOutputTokens, in.UsageReasoningTokens,
		in.Attempt, in.LatencyMS, in.Truncated, in.StartedAt, in.FinishedAt, in.CreatedAt)
	return err
}

// ListCallLogs 分页读取调用记录。
func (r *Repo) ListCallLogs(ctx context.Context, filter CallLogFilter) ([]CallLog, bool, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	sql := `SELECT ` + callLogColumns + ` FROM ai_call_logs WHERE NOT is_deleted`
	args := make([]any, 0, 8)
	addArg := func(value any) string {
		args = append(args, value)
		return "$" + itoa(len(args))
	}
	if filter.AIID > 0 {
		sql += ` AND ai_id = ` + addArg(filter.AIID)
	}
	if filter.ProviderID > 0 {
		sql += ` AND provider_id = ` + addArg(filter.ProviderID)
	}
	if filter.TaskID > 0 {
		sql += ` AND task_id = ` + addArg(filter.TaskID)
	}
	if filter.Status != "" {
		sql += ` AND status = ` + addArg(filter.Status)
	}
	if filter.ErrorType != "" {
		sql += ` AND error_type = ` + addArg(filter.ErrorType)
	}
	if filter.From != nil {
		sql += ` AND created_at >= ` + addArg(*filter.From)
	}
	if filter.To != nil {
		sql += ` AND created_at <= ` + addArg(*filter.To)
	}
	if filter.Cursor > 0 {
		sql += ` AND id < ` + addArg(filter.Cursor)
	}
	sql += ` ORDER BY id DESC LIMIT ` + addArg(limit+1)
	rows, err := r.ex(ctx).Query(ctx, sql, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	logs := make([]CallLog, 0, limit+1)
	for rows.Next() {
		log, scanErr := scanCallLog(rows)
		if scanErr != nil {
			return nil, false, scanErr
		}
		logs = append(logs, log)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(logs) > limit
	if hasMore {
		logs = logs[:limit]
	}
	return logs, hasMore, nil
}

// GetCallLog 读取单条调用记录；不存在返回领域 NotFound。
func (r *Repo) GetCallLog(ctx context.Context, logID int64) (CallLog, error) {
	log, err := scanCallLog(r.ex(ctx).QueryRow(ctx, `
		SELECT `+callLogColumns+` FROM ai_call_logs WHERE id = $1 AND NOT is_deleted`, logID))
	if errors.Is(err, pgx.ErrNoRows) {
		return CallLog{}, apperr.NotFound("调用记录不存在")
	}
	return log, err
}

// MarkDeletedBefore 把早于 cutoff 的调用记录标记为逻辑删除，返回处理数量。
func (r *Repo) MarkDeletedBefore(ctx context.Context, cutoff time.Time, limit int) (int, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := r.ex(ctx).Query(ctx, `
		SELECT id FROM ai_call_logs
		WHERE NOT is_deleted AND created_at < $1
		ORDER BY created_at, id LIMIT $2`, cutoff, limit)
	if err != nil {
		return 0, err
	}
	ids := make([]int64, 0, limit)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	tag, err := r.ex(ctx).Exec(ctx, `UPDATE ai_call_logs SET is_deleted = TRUE WHERE id = ANY($1)`, ids)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func itoa(value int) string {
	// 避免引入 strconv 之外的分页逻辑错误，占位符序号只需要十进制字符串。
	if value == 0 {
		return "0"
	}
	digits := make([]byte, 0, 4)
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
