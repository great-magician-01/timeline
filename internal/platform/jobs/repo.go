package jobs

// 本文件集中存放 background_jobs 的 SQL。所有时间值（created_at/updated_at/run_at/
// 恢复截止时间）都是应用侧 clock 计算的显式参数，SQL 内禁止 now()、CURRENT_DATE、
// interval 等数据库时间函数；SQL 文本的静态守卫测试见 jobs_test.go。

// sqlEnqueue 幂等入队：idempotency_key 冲突时静默忽略（返回 nil）。
// $9 同时用于 created_at 与 updated_at，保证一次入队只取一次应用时间。
const sqlEnqueue = `
	INSERT INTO background_jobs (
		id, job_type, ref_type, ref_id, status, run_at, max_attempts,
		idempotency_key, payload, created_at, updated_at
	) VALUES ($1, $2, $3, $4, 'pending', $5, $6, $7, $8, $9, $9)
	ON CONFLICT (idempotency_key) DO NOTHING`

// sqlCancelPending 取消引用对象下所有未结束作业；$4 是应用时间。
const sqlCancelPending = `
	UPDATE background_jobs
	SET status = 'canceled', updated_at = $4
	WHERE job_type = $1 AND ref_type = $2 AND ref_id = $3 AND status IN ('pending', 'running')`

// sqlRecoverAll 在启动后的首次成功回收中恢复全部 running 作业（此时不存在真正在跑的作业）；
// $1 是应用时间。
const sqlRecoverAll = `
	UPDATE background_jobs
	SET status = 'pending', updated_at = $1
	WHERE status = 'running'`

// sqlRecoverStale 回收超过租约仍未更新的 running 作业；
// $1 是应用时间（新的 updated_at），$2 是应用计算的截止时间（now - staleRunningAfter）。
const sqlRecoverStale = `
	UPDATE background_jobs
	SET status = 'pending', updated_at = $1
	WHERE status = 'running' AND updated_at < $2`

// sqlClaim 原子领取一条到期作业：$1 是“当前时间”（run_at 比较），$2 写入 updated_at。
const sqlClaim = `
	UPDATE background_jobs
	SET status = 'running', attempts = attempts + 1, updated_at = $2
	WHERE id = (
		SELECT id FROM background_jobs
		WHERE status = 'pending' AND run_at <= $1
		ORDER BY run_at, id
		LIMIT 1
		FOR UPDATE SKIP LOCKED
	)
	RETURNING id, job_type, COALESCE(ref_type, ''), ref_id, payload, attempts, max_attempts, run_at`

// sqlMarkSucceeded 标记成功；$2 是应用时间。
const sqlMarkSucceeded = `
	UPDATE background_jobs
	SET status = 'succeeded', last_error = '', updated_at = $2
	WHERE id = $1`

// sqlMarkFailed 标记最终失败；$2 是错误信息（已截断），$3 是应用时间。
const sqlMarkFailed = `
	UPDATE background_jobs
	SET status = 'failed', last_error = $2, updated_at = $3
	WHERE id = $1`

// sqlScheduleRetry 安排重试；$2 是错误信息，$3 是应用计算的下一执行时间，$4 是当前应用时间。
const sqlScheduleRetry = `
	UPDATE background_jobs
	SET status = 'pending', run_at = $3, last_error = $2, updated_at = $4
	WHERE id = $1`
