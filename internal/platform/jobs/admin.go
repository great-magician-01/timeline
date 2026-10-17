package jobs

import (
	"context"
	"fmt"
	"time"

	"github.com/great-magician-01/timeline/internal/platform/db"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
)

// 本文件提供管理端人工处置作业的公开方法（暂停/重试/取消/显式终态/按引用查询）。
// background_jobs 的所有权在本包，因此 SQL 只出现在这里；组合根与模块 app 只调用方法。

const sqlPausePending = `
	UPDATE background_jobs SET status = 'paused', updated_at = $1
	WHERE job_type = $2 AND ref_type = $3 AND ref_id = ANY($4) AND status = 'pending'`

const sqlPauseAllPending = `
	UPDATE background_jobs SET status = 'paused', updated_at = $1
	WHERE ref_type = $2 AND status = 'pending' AND ($3 = '' OR job_type = $3)`

const sqlRetryJob = `
	UPDATE background_jobs
	SET status = 'pending', attempts = 0, last_error = NULL, run_at = $2, updated_at = $2
	WHERE id = $1 AND status = 'failed'`

const sqlCancelJob = `
	UPDATE background_jobs SET status = 'canceled', updated_at = $2
	WHERE id = $1 AND status IN ('pending', 'running', 'paused')`

const sqlSetJobStatus = `
	UPDATE background_jobs SET status = $2, updated_at = $3 WHERE id = $1`

const jobSnapshotColumns = `id, job_type, COALESCE(ref_type, ''), ref_id, status, run_at,
	attempts, max_attempts, COALESCE(last_error, ''), created_at, updated_at`

const sqlGetJob = `SELECT ` + jobSnapshotColumns + ` FROM background_jobs WHERE id = $1`

const sqlListByRef = `SELECT ` + jobSnapshotColumns + ` FROM background_jobs WHERE ref_id = ANY($1)`

// executor 解析当前上下文对应的执行器（事务优先）；连接池未就绪时返回错误。
func (r *Runner) executor(ctx context.Context) (db.Executor, error) {
	if r == nil || r.pools == nil {
		return nil, fmt.Errorf("作业运行器未初始化")
	}
	pool := r.pools.Pool()
	if pool == nil {
		return nil, fmt.Errorf("数据库尚未就绪")
	}
	return db.ExecutorFrom(ctx, pool), nil
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
	at := in.At
	if at.IsZero() {
		at = r.clk.Now()
	}
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
	at := in.At
	if at.IsZero() {
		at = r.clk.Now()
	}
	tag, err := exec.Exec(ctx, sqlPauseAllPending, at, in.RefType, in.JobType)
	if err != nil {
		return 0, fmt.Errorf("暂停全部待执行作业失败: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// RetryJob 把 failed 作业重置为 pending（等价人工重试），返回最新快照。
func (r *Runner) RetryJob(ctx context.Context, in contracts.JobActionInput) (contracts.JobSnapshot, error) {
	exec, err := r.executor(ctx)
	if err != nil {
		return contracts.JobSnapshot{}, err
	}
	at := in.At
	if at.IsZero() {
		at = r.clk.Now()
	}
	if _, err := exec.Exec(ctx, sqlRetryJob, in.JobID, at); err != nil {
		return contracts.JobSnapshot{}, fmt.Errorf("重试作业失败: %w", err)
	}
	return r.GetJob(ctx, in.JobID)
}

// CancelJob 把 pending/running/paused 作业置为 canceled，返回最新快照。
func (r *Runner) CancelJob(ctx context.Context, in contracts.JobActionInput) (contracts.JobSnapshot, error) {
	exec, err := r.executor(ctx)
	if err != nil {
		return contracts.JobSnapshot{}, err
	}
	at := in.At
	if at.IsZero() {
		at = r.clk.Now()
	}
	if _, err := exec.Exec(ctx, sqlCancelJob, in.JobID, at); err != nil {
		return contracts.JobSnapshot{}, fmt.Errorf("取消作业失败: %w", err)
	}
	return r.GetJob(ctx, in.JobID)
}

// SetJobStatus 由作业处理器显式写作业终态（不经过运行器重试逻辑）。
func (r *Runner) SetJobStatus(ctx context.Context, jobID int64, status string, at time.Time) error {
	exec, err := r.executor(ctx)
	if err != nil {
		return err
	}
	if at.IsZero() {
		at = r.clk.Now()
	}
	if _, err := exec.Exec(ctx, sqlSetJobStatus, jobID, status, at); err != nil {
		return fmt.Errorf("写作业状态失败: %w", err)
	}
	return nil
}

// GetJob 读取单个作业快照；不存在时返回空快照与 nil（调用方可判断 ID==0）。
func (r *Runner) GetJob(ctx context.Context, jobID int64) (contracts.JobSnapshot, error) {
	exec, err := r.executor(ctx)
	if err != nil {
		return contracts.JobSnapshot{}, err
	}
	row := exec.QueryRow(ctx, sqlGetJob, jobID)
	snapshot, err := scanJobSnapshot(row)
	if err != nil {
		return contracts.JobSnapshot{}, err
	}
	return snapshot, nil
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
