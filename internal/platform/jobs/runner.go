package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/platform/db"
)

const (
	// defaultPollInterval 是 worker 轮询领取作业的间隔。
	defaultPollInterval = 2 * time.Second
	// defaultWorkers 是并发 worker 数。
	defaultWorkers = 2
	// jobTimeout 是单次作业执行上限。
	jobTimeout = 10 * time.Minute
	// periodicTimeout 是单次周期任务执行上限。
	periodicTimeout = 2 * time.Minute
	// staleRunningAfter 是判定“running 作业已失去所属进程”的时长阈值。
	// 需要大于单次作业的最长执行时间（jobTimeout），避免误回收仍在运行的作业。
	staleRunningAfter = 20 * time.Minute
	// recoverInterval 是恢复中断作业的轮询间隔。
	recoverInterval = 2 * time.Minute
	// maxBackoff 是重试退避上限。
	maxBackoff = 10 * time.Minute
)

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
	clk      clock.Clock
	handlers map[string]Handler
	periodic []periodicTask

	pollInterval time.Duration
	workers      int

	// execProvider 仅供单元测试注入假执行器；nil 时按连接池解析当前执行器。
	// 生产装配不设置该字段，保持「事务优先、连接池兜底」的既有语义。
	execProvider func(ctx context.Context) (db.Executor, error)
}

// NewRunner 构造作业运行器。clk 是运行期唯一时间来源，不得为 nil。
func NewRunner(pools PoolProvider, ids IDGenerator, log *slog.Logger, clk clock.Clock) *Runner {
	return &Runner{
		pools:        pools,
		ids:          ids,
		log:          log,
		clk:          clk,
		handlers:     map[string]Handler{},
		pollInterval: defaultPollInterval,
		workers:      defaultWorkers,
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
	ticker := time.NewTicker(recoverInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if pool := r.pools.Pool(); pool != nil {
			now := r.clk.Now()
			var (
				tag pgconn.CommandTag
				err error
			)
			if firstAttempt {
				tag, err = pool.Exec(ctx, sqlRecoverAll, now)
			} else {
				tag, err = pool.Exec(ctx, sqlRecoverStale, now, now.Add(-staleRunningAfter))
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
				runCtx, cancel := context.WithTimeout(ctx, periodicTimeout)
				defer cancel()
				defer func() {
					if rec := recover(); rec != nil {
						// panic 必须在恢复现场采集完整栈：栈需要包含 panic 触发帧与 goroutine 头。
						r.log.Error("周期任务 panic",
							"task", task.name, "panic", rec, "stack", string(debug.Stack()))
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
	now := r.clk.Now()
	var job Job
	err := pool.QueryRow(ctx, sqlClaim, now, now).Scan(
		&job.ID, &job.Type, &job.RefType, &job.RefID, &job.Payload, &job.Attempts, &job.MaxAttempts, &job.RunAt,
	)
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
	runCtx, cancel := context.WithTimeout(ctx, jobTimeout)
	defer cancel()

	var err error
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				// 作业 panic 是程序缺陷，必须带完整栈记录；错误本身继续走失败/重试状态机。
				r.log.Error("后台作业 panic",
					"job_id", job.ID, "type", job.Type, "panic", rec, "stack", string(debug.Stack()))
				err = fmt.Errorf("作业 panic: %v", rec)
			}
		}()
		err = handler(runCtx, job)
	}()
	r.finish(ctx, job, err)
}

// finish 写入作业终态。一次调用只取一次应用时间 now：重试的 run_at 与 updated_at
// 都基于同一个 now 计算，避免 SQL 里做时间运算。
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

	now := r.clk.Now()
	if err == nil {
		if _, execErr := pool.Exec(ctx, sqlMarkSucceeded, job.ID, now); execErr != nil {
			r.log.Error("更新作业状态失败", "job_id", job.ID, "err", execErr)
		}
		return
	}

	message := err.Error()
	if len(message) > 2000 {
		message = message[:2000]
	}
	if job.Attempts >= job.MaxAttempts {
		if _, execErr := pool.Exec(ctx, sqlMarkFailed, job.ID, message, now); execErr != nil {
			r.log.Error("标记作业失败状态出错", "job_id", job.ID, "err", execErr)
		}
		r.log.Warn("后台作业失败", "job_id", job.ID, "type", job.Type, "err", message)
		return
	}
	backoff := backoffDuration(job.Attempts)
	if _, execErr := pool.Exec(ctx, sqlScheduleRetry, job.ID, message, now.Add(backoff), now); execErr != nil {
		r.log.Error("重试作业失败", "job_id", job.ID, "err", execErr)
	}
	r.log.Info("后台作业将重试", "job_id", job.ID, "type", job.Type, "attempt", job.Attempts, "backoff", backoff.String())
}

// backoffDuration 返回第 attempts 次尝试失败后的重试退避：30 秒起指数增长，
// 上限 maxBackoff（10 分钟）。attempts 从 1 开始（领取时自增）。
func backoffDuration(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	if attempts >= 6 {
		return maxBackoff
	}
	backoff := time.Duration(1<<uint(attempts-1)) * 30 * time.Second
	if backoff > maxBackoff {
		return maxBackoff
	}
	return backoff
}
