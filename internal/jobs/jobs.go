// Package jobs 是单实例部署下的后台作业运行器（docs/DEPLOYMENT.md §2、§17.4）：
//   - 作业持久化在 timeline.background_jobs，应用重启后继续执行；
//   - 领取作业使用 UPDATE ... FOR UPDATE SKIP LOCKED，原子且不会重复执行；
//   - 失败按有限次数与指数退避重试，超过上限进入 failed 等待人工重试；
//   - 只更新状态，不物理删除作业记录。
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/access"
)

// 作业状态（与 background_jobs.status 约束一致）。
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusPaused    = "paused"
	StatusCanceled  = "canceled"
)

// 作业类型。
const (
	TypePostPublish       = "post_publish"
	TypeMediaDerivative   = "media_derivative"
	TypeAttachmentCleanup = "attachment_cleanup"
	TypeAISchedule        = "ai_schedule"
	TypeAITask            = "ai_task"
	TypeMemoryConsolidate = "memory_consolidation"
	TypeCallLogCleanup    = "ai_call_log_cleanup"
)

// Job 是领取到的作业快照。
type Job struct {
	ID          int64
	Type        string
	RefType     string
	RefID       *int64
	Payload     []byte
	Attempts    int
	MaxAttempts int
	// RunAt 是作业的计划执行时间。处理器可用它识别“被领取后计划时间又被改写”的
	// 过期作业（例如作者修改了定时发布时间），从而放弃执行。
	RunAt time.Time
}

// PayloadJSON 解析作业负载。
func (j Job) PayloadJSON(dst any) error {
	if len(j.Payload) == 0 {
		return nil
	}
	return json.Unmarshal(j.Payload, dst)
}

// Handler 执行作业。返回错误表示本次执行失败，由运行器决定重试。
type Handler func(ctx context.Context, job Job) error

// ErrHandled 表示处理器已经自行写入作业终态（例如账号停用把定时发布改为 paused），
// 运行器不得再改动该作业的状态。
var ErrHandled = errors.New("作业已由处理器写入终态")

type PoolProvider interface {
	Pool() *pgxpool.Pool
}

type IDGenerator interface {
	Next() (int64, error)
}

type periodicTask struct {
	name     string
	interval time.Duration
	fn       func(ctx context.Context)
}

// Runner 负责领取并执行作业，同时承载周期性调度任务。
type Runner struct {
	pools    PoolProvider
	ids      IDGenerator
	log      *slog.Logger
	handlers map[string]Handler
	periodic []periodicTask

	pollInterval time.Duration
	workers      int
}

func NewRunner(pools PoolProvider, ids IDGenerator, log *slog.Logger) *Runner {
	return &Runner{
		pools:        pools,
		ids:          ids,
		log:          log,
		handlers:     map[string]Handler{},
		pollInterval: 2 * time.Second,
		workers:      2,
	}
}

// Register 注册作业处理器；未注册类型的作业直接判定失败并记录原因。
func (r *Runner) Register(jobType string, handler Handler) {
	r.handlers[jobType] = handler
}

// RegisterPeriodic 注册周期任务（用于投递定时发布、AI 调度等作业）。
func (r *Runner) RegisterPeriodic(name string, interval time.Duration, fn func(ctx context.Context)) {
	r.periodic = append(r.periodic, periodicTask{name: name, interval: interval, fn: fn})
}

// EnqueueParams 描述一次入队。
type EnqueueParams struct {
	Type           string
	RefType        string
	RefID          *int64
	IdempotencyKey string
	Payload        any
	RunAt          time.Time
	MaxAttempts    int
}

// Enqueue 写入一条作业；幂等键重复时忽略（返回 nil），保证重试不会重复执行。
func (r *Runner) Enqueue(ctx context.Context, q access.Querier, params EnqueueParams) error {
	id, err := r.ids.Next()
	if err != nil {
		return fmt.Errorf("生成作业 ID 失败: %w", err)
	}
	payload := []byte("{}")
	if params.Payload != nil {
		if encoded, err := json.Marshal(params.Payload); err == nil {
			payload = encoded
		} else {
			return fmt.Errorf("序列化作业负载失败: %w", err)
		}
	}
	runAt := params.RunAt
	if runAt.IsZero() {
		runAt = time.Now()
	}
	maxAttempts := params.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	_, err = q.Exec(ctx, `
		INSERT INTO background_jobs (
			id, job_type, ref_type, ref_id, status, run_at, max_attempts, idempotency_key, payload
		) VALUES ($1, $2, $3, $4, 'pending', $5, $6, $7, $8)
		ON CONFLICT (idempotency_key) DO NOTHING`,
		id, params.Type, nullString(params.RefType), params.RefID, runAt, maxAttempts,
		params.IdempotencyKey, payload,
	)
	if err != nil {
		return fmt.Errorf("写入后台作业失败: %w", err)
	}
	return nil
}

// CancelPending 取消某个引用对象下所有待执行作业（如删除定时帖、取消定时发布）。
func (r *Runner) CancelPending(ctx context.Context, q access.Querier, jobType, refType string, refID int64) error {
	_, err := q.Exec(ctx, `
		UPDATE background_jobs
		SET status = 'canceled', updated_at = now()
		WHERE job_type = $1 AND ref_type = $2 AND ref_id = $3 AND status IN ('pending', 'running')`,
		jobType, refType, refID,
	)
	return err
}

// staleRunningAfter 是判定“running 作业已失去所属进程”的时长阈值。
// 需要大于单次作业的最长执行时间（处理器超时 10 分钟），避免误回收仍在运行的作业。
const staleRunningAfter = 20 * time.Minute

// Start 启动作业消费与周期任务；ctx 取消后停止。
func (r *Runner) Start(ctx context.Context) {
	for i := 0; i < r.workers; i++ {
		go r.consume(ctx, i)
	}
	for _, task := range r.periodic {
		go r.runPeriodic(ctx, task)
	}
	go r.recoverLoop(ctx)
}

// recoverLoop 负责把失去所属进程的 running 作业放回队列（单实例部署下安全）：
//   - 进程启动后的第一次成功回收会回收**全部** running 作业（此时不存在真正在跑的作业）；
//   - 之后每 2 分钟回收一次超过 staleRunningAfter 仍未更新的 running 作业，
//     覆盖进程被强杀、容器被重启等情况（否则这些作业会永远停在 running）。
//
// 循环在连接池就绪之前空转，避免像一次性任务那样在数据库未就绪时被跳过。
func (r *Runner) recoverLoop(ctx context.Context) {
	firstAttempt := true
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if pool := r.pools.Pool(); pool != nil {
			var (
				tag pgconn.CommandTag
				err error
			)
			if firstAttempt {
				tag, err = pool.Exec(ctx, `
					UPDATE background_jobs
					SET status = 'pending', updated_at = now()
					WHERE status = 'running'`)
			} else {
				tag, err = pool.Exec(ctx, `
					UPDATE background_jobs
					SET status = 'pending', updated_at = now()
					WHERE status = 'running' AND updated_at < now() - $1::interval`,
					staleRunningAfter.String())
			}
			switch {
			case err != nil:
				r.log.Warn("恢复中断的后台作业失败，稍后重试", "err", err)
			default:
				firstAttempt = false
				if tag.RowsAffected() > 0 {
					r.log.Info("已恢复中断的后台作业", "count", tag.RowsAffected())
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *Runner) runPeriodic(ctx context.Context, task periodicTask) {
	ticker := time.NewTicker(task.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if r.pools.Pool() == nil {
				continue
			}
			func() {
				// 周期任务必须自己兜住 panic 与超时，不能拖垮整个进程
				runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
				defer cancel()
				defer func() {
					if rec := recover(); rec != nil {
						r.log.Error("周期任务 panic", "task", task.name, "panic", rec)
					}
				}()
				task.fn(runCtx)
			}()
		}
	}
}

func (r *Runner) consume(ctx context.Context, worker int) {
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for ctx.Err() == nil {
				job, err := r.claim(ctx)
				if err != nil {
					r.log.Error("领取后台作业失败", "worker", worker, "err", err)
					break
				}
				if job == nil {
					break
				}
				r.execute(ctx, *job)
			}
		}
	}
}

func (r *Runner) claim(ctx context.Context) (*Job, error) {
	pool := r.pools.Pool()
	if pool == nil {
		return nil, nil
	}
	var job Job
	err := pool.QueryRow(ctx, `
		UPDATE background_jobs
		SET status = 'running', attempts = attempts + 1, updated_at = now()
		WHERE id = (
			SELECT id FROM background_jobs
			WHERE status = 'pending' AND run_at <= now()
			ORDER BY run_at, id
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, job_type, COALESCE(ref_type, ''), ref_id, payload, attempts, max_attempts, run_at`,
	).Scan(&job.ID, &job.Type, &job.RefType, &job.RefID, &job.Payload, &job.Attempts, &job.MaxAttempts, &job.RunAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &job, nil
}

func (r *Runner) execute(ctx context.Context, job Job) {
	handler, ok := r.handlers[job.Type]
	if !ok {
		r.finish(ctx, job, fmt.Errorf("未注册的作业类型 %s", job.Type))
		return
	}
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	var err error
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				err = fmt.Errorf("作业 panic: %v", rec)
			}
		}()
		err = handler(runCtx, job)
	}()
	r.finish(ctx, job, err)
}

func (r *Runner) finish(ctx context.Context, job Job, err error) {
	pool := r.pools.Pool()
	if pool == nil {
		return
	}
	if errors.Is(err, ErrHandled) {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	if err == nil {
		_, execErr := pool.Exec(ctx, `
			UPDATE background_jobs SET status = 'succeeded', last_error = '', updated_at = now()
			WHERE id = $1`, job.ID)
		if execErr != nil {
			r.log.Error("更新作业状态失败", "job_id", job.ID, "err", execErr)
		}
		return
	}

	message := err.Error()
	if len(message) > 2000 {
		message = message[:2000]
	}
	if job.Attempts >= job.MaxAttempts {
		if _, execErr := pool.Exec(ctx, `
			UPDATE background_jobs
			SET status = 'failed', last_error = $2, updated_at = now()
			WHERE id = $1`, job.ID, message); execErr != nil {
			r.log.Error("标记作业失败状态出错", "job_id", job.ID, "err", execErr)
		}
		r.log.Warn("后台作业失败", "job_id", job.ID, "type", job.Type, "err", message)
		return
	}
	backoff := time.Duration(1<<uint(job.Attempts-1)) * 30 * time.Second
	if backoff > 10*time.Minute {
		backoff = 10 * time.Minute
	}
	if _, execErr := pool.Exec(ctx, `
		UPDATE background_jobs
		SET status = 'pending', run_at = now() + $3::interval, last_error = $2, updated_at = now()
		WHERE id = $1`, job.ID, message, backoff.String()); execErr != nil {
		r.log.Error("重试作业失败", "job_id", job.ID, "err", execErr)
	}
	r.log.Info("后台作业将重试", "job_id", job.ID, "type", job.Type, "attempt", job.Attempts, "backoff", backoff.String())
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
