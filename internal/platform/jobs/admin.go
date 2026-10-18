package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/db"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
)

// 本文件提供作业生命周期人工处置的公开方法（暂停/重试/取消/重激活/显式终态/查询）。
// background_jobs 的所有权在本包，因此 SQL 只出现在这里；组合根与模块 app 只调用方法。
//
// 对外错误契约：作业不存在统一返回 apperr.NotFound（Handler 映射 404）；状态不允许
// 人工处置由调用方（admin app）先行校验并返回 validation。重试/取消"命中但状态已变化"
// 视为作业不存在（并发下不可再操作），避免暴露内部状态竞争细节。

// jobNotFoundMessage 是作业不存在时的统一对外文案。
const jobNotFoundMessage = "作业不存在"

const sqlPausePending = `
	UPDATE background_jobs SET status = 'paused', updated_at = $1
	WHERE ($2 = '' OR job_type = $2) AND ref_type = $3 AND ref_id = ANY($4) AND status = 'pending'`

const sqlPauseAllPending = `
	UPDATE background_jobs SET status = 'paused', updated_at = $1
	WHERE ref_type = $2 AND status = 'pending' AND ($3 = '' OR job_type = $3)`

// sqlRetryJob 人工重试：重置为 pending，并清空失败原因。
// last_error 列是 NOT NULL DEFAULT 空字符串，必须写空字符串而不是 NULL。
const sqlRetryJob = `
	UPDATE background_jobs
	SET status = 'pending', attempts = 0, last_error = '', run_at = $2, updated_at = $2
	WHERE id = $1 AND status = 'failed'`

const sqlCancelJob = `
	UPDATE background_jobs SET status = 'canceled', updated_at = $2
	WHERE id = $1 AND status IN ('pending', 'running', 'paused')`

const sqlSetJobStatus = `
	UPDATE background_jobs SET status = $2, updated_at = $3 WHERE id = $1`

// sqlReactivate 按幂等键把被取消/暂停的作业重新激活；不命中的键返回 false 而不是错误。
const sqlReactivate = `
	UPDATE background_jobs
	SET status = 'pending', attempts = 0, last_error = '', run_at = $2, updated_at = $3
	WHERE idempotency_key = $1 AND status IN ('canceled', 'paused')`

const jobSnapshotColumns = `id, job_type, COALESCE(ref_type, ''), ref_id, status, run_at,
	attempts, max_attempts, COALESCE(last_error, ''), created_at, updated_at`

const sqlGetJob = `SELECT ` + jobSnapshotColumns + ` FROM background_jobs WHERE id = $1`

// sqlGetJobForUpdate 在事务中锁定作业行，供"读状态 → 条件更新"的人工处置使用；
// 不在事务中使用时锁只覆盖当前语句，调用方应优先经 TxManager.WithTx 包住整段处置。
const sqlGetJobForUpdate = sqlGetJob + ` FOR UPDATE`

const sqlListByRef = `SELECT ` + jobSnapshotColumns + ` FROM background_jobs WHERE ref_id = ANY($1)`

// executor 解析当前上下文对应的执行器（事务优先）；连接池未就绪时返回错误。
func (r *Runner) executor(ctx context.Context) (db.Executor, error) {
	if r == nil {
		return nil, fmt.Errorf("作业运行器未初始化")
	}
	if r.execProvider != nil {
		return r.execProvider(ctx)
	}
	if r.pools == nil {
		return nil, fmt.Errorf("作业运行器未初始化")
	}
	pool := r.pools.Pool()
	if pool == nil {
		return nil, fmt.Errorf("数据库尚未就绪")
	}
	return db.ExecutorFrom(ctx, pool), nil
}

// actionTime 返回一次人工处置使用的应用时间：优先使用调用方传入值（保证同一事务
// 一个 now），零值时回退注入时钟。
func (r *Runner) actionTime(at time.Time) time.Time {
	if !at.IsZero() {
		return at
	}
	return r.clk.Now()
}

// PausePending 把一批引用对象的 pending 作业置为 paused（逻辑状态，不删除记录）。
func (r *Runner) PausePending(ctx context.Context, in contracts.PausePendingJobsInput) (int, error) {
	if len(in.RefIDs) == 0 {
		return 0, nil
	}
	exec, err := r.executor(ctx)
	if err != nil {
		return 0, err
	}
	at := r.actionTime(in.At)
	tag, err := exec.Exec(ctx, sqlPausePending, at, in.JobType, in.RefType, in.RefIDs)
	if err != nil {
		return 0, fmt.Errorf("暂停待执行作业失败: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// PauseAllPending 暂停某引用类型下全部 pending 作业（不管 ref_id），返回受影响数量。
// 消费者：agent（关闭全局 AI 开关时暂停 ref_type='ai' 的所有待执行作业）。
func (r *Runner) PauseAllPending(ctx context.Context, in contracts.PauseAllPendingInput) (int, error) {
	exec, err := r.executor(ctx)
	if err != nil {
		return 0, err
	}
	at := r.actionTime(in.At)
	tag, err := exec.Exec(ctx, sqlPauseAllPending, at, in.RefType, in.JobType)
	if err != nil {
		return 0, fmt.Errorf("暂停全部待执行作业失败: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// RetryJob 把 failed 作业重置为 pending（等价人工重试），返回最新快照。
// 先 FOR UPDATE 锁行再条件更新：在调用方事务内可防止并发处置互相覆盖；
// 未命中（行不存在或状态已变化）返回 apperr.NotFound。
func (r *Runner) RetryJob(ctx context.Context, in contracts.JobActionInput) (contracts.JobSnapshot, error) {
	exec, err := r.executor(ctx)
	if err != nil {
		return contracts.JobSnapshot{}, err
	}
	if _, err := getJobWith(ctx, exec, sqlGetJobForUpdate, in.JobID); err != nil {
		return contracts.JobSnapshot{}, err
	}
	at := r.actionTime(in.At)
	tag, err := exec.Exec(ctx, sqlRetryJob, in.JobID, at)
	if err != nil {
		return contracts.JobSnapshot{}, fmt.Errorf("重试作业失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return contracts.JobSnapshot{}, apperr.NotFound(jobNotFoundMessage)
	}
	return getJobWith(ctx, exec, sqlGetJob, in.JobID)
}

// CancelJob 把 pending/running/paused 作业置为 canceled，返回最新快照。
// 与 RetryJob 相同：先锁行再条件更新，未命中返回 apperr.NotFound。
func (r *Runner) CancelJob(ctx context.Context, in contracts.JobActionInput) (contracts.JobSnapshot, error) {
	exec, err := r.executor(ctx)
	if err != nil {
		return contracts.JobSnapshot{}, err
	}
	if _, err := getJobWith(ctx, exec, sqlGetJobForUpdate, in.JobID); err != nil {
		return contracts.JobSnapshot{}, err
	}
	at := r.actionTime(in.At)
	tag, err := exec.Exec(ctx, sqlCancelJob, in.JobID, at)
	if err != nil {
		return contracts.JobSnapshot{}, fmt.Errorf("取消作业失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return contracts.JobSnapshot{}, apperr.NotFound(jobNotFoundMessage)
	}
	return getJobWith(ctx, exec, sqlGetJob, in.JobID)
}

// Reactivate 按幂等键把 canceled/paused 作业重新置为 pending，
// 用于"取消后重新设置同一时间/同一幂等键"的定时发布等场景。
//
// runAt 为新的计划执行时间，零值时使用 at；at 为应用时间（零值回退注入时钟）。
// 幂等键不存在或当前状态不允许重激活时返回 (false, nil)，交由调用方决定是否继续。
func (r *Runner) Reactivate(
	ctx context.Context, idempotencyKey string, runAt, at time.Time,
) (bool, error) {
	if idempotencyKey == "" {
		return false, fmt.Errorf("作业幂等键不能为空")
	}
	exec, err := r.executor(ctx)
	if err != nil {
		return false, err
	}
	at = r.actionTime(at)
	if runAt.IsZero() {
		runAt = at
	}
	tag, err := exec.Exec(ctx, sqlReactivate, idempotencyKey, runAt, at)
	if err != nil {
		return false, fmt.Errorf("重新激活作业失败: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// SetJobStatus 由作业处理器显式写作业终态（不经过运行器重试逻辑）。
func (r *Runner) SetJobStatus(ctx context.Context, jobID int64, status string, at time.Time) error {
	exec, err := r.executor(ctx)
	if err != nil {
		return err
	}
	if _, err := exec.Exec(ctx, sqlSetJobStatus, jobID, status, r.actionTime(at)); err != nil {
		return fmt.Errorf("写作业状态失败: %w", err)
	}
	return nil
}

// GetJob 读取单个作业快照；不存在时返回 apperr.NotFound（Handler 映射 404）。
func (r *Runner) GetJob(ctx context.Context, jobID int64) (contracts.JobSnapshot, error) {
	exec, err := r.executor(ctx)
	if err != nil {
		return contracts.JobSnapshot{}, err
	}
	return getJobWith(ctx, exec, sqlGetJob, jobID)
}

// GetJobForUpdate 在事务内读取并锁定单个作业快照；不存在时返回 apperr.NotFound。
// 供需要"读-改-写"的人工处置流程使用（调用方应在 TxManager.WithTx 内调用）。
func (r *Runner) GetJobForUpdate(ctx context.Context, jobID int64) (contracts.JobSnapshot, error) {
	exec, err := r.executor(ctx)
	if err != nil {
		return contracts.JobSnapshot{}, err
	}
	return getJobWith(ctx, exec, sqlGetJobForUpdate, jobID)
}

// ListByRef 按引用对象批量查询作业快照。
func (r *Runner) ListByRef(ctx context.Context, query contracts.JobRefQuery) ([]contracts.JobSnapshot, error) {
	if len(query.RefIDs) == 0 {
		return nil, nil
	}
	exec, err := r.executor(ctx)
	if err != nil {
		return nil, err
	}
	limit := query.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	snapshotSQL := sqlListByRef
	args := []any{query.RefIDs}
	if query.RefType != "" {
		args = append(args, query.RefType)
		snapshotSQL += fmt.Sprintf(" AND ref_type = $%d", len(args))
	}
	if len(query.Types) > 0 {
		args = append(args, query.Types)
		snapshotSQL += fmt.Sprintf(" AND job_type = ANY($%d)", len(args))
	}
	args = append(args, limit)
	snapshotSQL += fmt.Sprintf(" ORDER BY id DESC LIMIT $%d", len(args))
	rows, err := exec.Query(ctx, snapshotSQL, args...)
	if err != nil {
		return nil, fmt.Errorf("查询作业快照失败: %w", err)
	}
	defer rows.Close()
	result := make([]contracts.JobSnapshot, 0, limit)
	for rows.Next() {
		snapshot, scanErr := scanJobSnapshot(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, snapshot)
	}
	return result, rows.Err()
}

// getJobWith 用给定查询读取单个作业快照；无行时统一映射为领域 NotFound。
func getJobWith(
	ctx context.Context, exec db.Executor, query string, jobID int64,
) (contracts.JobSnapshot, error) {
	snapshot, err := scanJobSnapshot(exec.QueryRow(ctx, query, jobID))
	if errors.Is(err, pgx.ErrNoRows) {
		return contracts.JobSnapshot{}, apperr.NotFound(jobNotFoundMessage)
	}
	if err != nil {
		return contracts.JobSnapshot{}, fmt.Errorf("读取作业快照失败: %w", err)
	}
	return snapshot, nil
}

// scanner 同时覆盖 pgx.Row 与 pgx.Rows 的 Scan 方法。
type scanner interface {
	Scan(dest ...any) error
}

func scanJobSnapshot(row scanner) (contracts.JobSnapshot, error) {
	var snapshot contracts.JobSnapshot
	if err := row.Scan(&snapshot.ID, &snapshot.Type, &snapshot.RefType, &snapshot.RefID,
		&snapshot.Status, &snapshot.RunAt, &snapshot.Attempts, &snapshot.MaxAttempts,
		&snapshot.LastError, &snapshot.CreatedAt, &snapshot.UpdatedAt); err != nil {
		return contracts.JobSnapshot{}, err
	}
	return snapshot, nil
}
