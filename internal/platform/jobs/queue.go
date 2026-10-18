package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// EnqueueParams 描述一次入队。
type EnqueueParams struct {
	Type           string
	RefType        string
	RefID          *int64
	IdempotencyKey string
	Payload        any
	RunAt          time.Time
	MaxAttempts    int
	// At 是本次入队的应用时间（created_at/updated_at）；零值回退注入时钟。
	// 在业务事务内入队时由调用方传入该事务的 now，保证同一事务时间一致。
	At time.Time
}

// Enqueue 写入一条作业；幂等键重复时忽略（返回 nil），保证重试不会重复执行。
// 调用方在事务内入队时传入事务执行器（db.Executor），与业务变更同生共死。
// RunAt 为零值时使用注入时钟的当前时间。
func (r *Runner) Enqueue(ctx context.Context, q Querier, params EnqueueParams) error {
	id, err := r.ids.Next()
	if err != nil {
		return fmt.Errorf("生成作业 ID 失败: %w", err)
	}
	payload := []byte("{}")
	if params.Payload != nil {
		encoded, err := json.Marshal(params.Payload)
		if err != nil {
			return fmt.Errorf("序列化作业负载失败: %w", err)
		}
		payload = encoded
	}
	now := params.At
	if now.IsZero() {
		now = r.clk.Now()
	}
	runAt := params.RunAt
	if runAt.IsZero() {
		runAt = now
	}
	maxAttempts := params.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	if _, err := q.Exec(ctx, sqlEnqueue,
		id, params.Type, nullString(params.RefType), params.RefID, runAt, maxAttempts,
		params.IdempotencyKey, payload, now,
	); err != nil {
		return fmt.Errorf("写入后台作业失败: %w", err)
	}
	return nil
}

// CancelPending 取消某个引用对象下所有待执行作业（如删除定时帖、取消定时发布）。
// updated_at 使用注入时钟的当前时间，由应用显式传入。
func (r *Runner) CancelPending(ctx context.Context, q Querier, jobType, refType string, refID int64) error {
	if _, err := q.Exec(ctx, sqlCancelPending, jobType, refType, refID, r.clk.Now()); err != nil {
		return fmt.Errorf("取消待执行后台作业失败: %w", err)
	}
	return nil
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
