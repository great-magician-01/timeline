package jobs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/db"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
)

// testClock 是仅实现 clock.Clock 的最小假时钟。
type testClock struct {
	now time.Time
}

func (c *testClock) Now() time.Time { return c.now }

// recordedExecutor 记录一次 Exec 调用的 SQL 与参数，用于无数据库断言参数化时间。
type recordedExecutor struct {
	sql  string
	args []any
	err  error
}

func (e *recordedExecutor) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	e.sql = sql
	e.args = args
	return pgconn.CommandTag{}, e.err
}

func (e *recordedExecutor) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	return nil, errors.New("测试不应调用 Query")
}

func (e *recordedExecutor) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	return nil
}

type testIDs struct {
	next  int64
	err   error
	calls int
}

func (g *testIDs) Next() (int64, error) {
	g.calls++
	if g.err != nil {
		return 0, g.err
	}
	g.next++
	return g.next, nil
}

func newTestRunner(t *testing.T, clk *testClock) (*Runner, *testIDs) {
	t.Helper()
	ids := &testIDs{next: 100}
	runner := NewRunner(nil, ids, nil, clk)
	return runner, ids
}

func TestBackoffDuration(t *testing.T) {
	cases := []struct {
		attempts int
		want     time.Duration
	}{
		{0, 30 * time.Second},
		{1, 30 * time.Second},
		{2, time.Minute},
		{3, 2 * time.Minute},
		{4, 4 * time.Minute},
		{5, 8 * time.Minute},
		{6, maxBackoff},
		{1000, maxBackoff},
	}
	for _, tc := range cases {
		if got := backoffDuration(tc.attempts); got != tc.want {
			t.Errorf("backoffDuration(%d) = %s，期望 %s", tc.attempts, got, tc.want)
		}
	}
}

func TestEnqueueUsesClockTimeAndDefaults(t *testing.T) {
	now := time.Date(2026, 10, 18, 9, 30, 0, 0, time.FixedZone("CST", 8*60*60))
	clk := &testClock{now: now}
	runner, ids := newTestRunner(t, clk)
	exec := &recordedExecutor{}

	refID := int64(42)
	err := runner.Enqueue(context.Background(), exec, EnqueueParams{
		Type:           TypePostPublish,
		RefType:        "post",
		RefID:          &refID,
		IdempotencyKey: "post_publish:42",
		Payload:        map[string]any{"post_id": 42},
	})
	if err != nil {
		t.Fatalf("入队失败: %v", err)
	}
	if exec.sql != sqlEnqueue {
		t.Fatalf("应使用入队 SQL")
	}
	if len(exec.args) != 9 {
		t.Fatalf("入队参数数量应为 9，实际 %d", len(exec.args))
	}
	if got := exec.args[0]; got != int64(101) {
		t.Fatalf("作业 ID 应来自 IDGenerator，实际 %v", got)
	}
	if got := exec.args[2]; got != "post" {
		t.Fatalf("ref_type 错误: %v", got)
	}
	if got := exec.args[4]; got != now {
		t.Fatalf("RunAt 零值应使用注入时钟，实际 %v", got)
	}
	if got := exec.args[5]; got != 3 {
		t.Fatalf("默认 MaxAttempts 应为 3，实际 %v", got)
	}
	if got := string(exec.args[7].([]byte)); got != `{"post_id":42}` {
		t.Fatalf("payload 序列化错误: %s", got)
	}
	if got := exec.args[8]; got != now {
		t.Fatalf("created_at/updated_at 应使用注入时钟，实际 %v", got)
	}
	if ids.calls != 1 {
		t.Fatalf("应生成一次 ID，实际 %d", ids.calls)
	}
}

func TestEnqueueHonorsExplicitRunAt(t *testing.T) {
	now := time.Date(2026, 10, 18, 9, 30, 0, 0, time.UTC)
	clk := &testClock{now: now}
	runner, _ := newTestRunner(t, clk)
	exec := &recordedExecutor{}
	runAt := now.Add(30 * time.Minute)

	if err := runner.Enqueue(context.Background(), exec, EnqueueParams{
		Type:           TypeAISchedule,
		IdempotencyKey: "ai_schedule:1",
		RunAt:          runAt,
		MaxAttempts:    5,
	}); err != nil {
		t.Fatalf("入队失败: %v", err)
	}
	if got := exec.args[1]; got != TypeAISchedule {
		t.Fatalf("job_type 错误: %v", got)
	}
	if got := exec.args[2]; got != nil {
		t.Fatalf("空 ref_type 应传 nil，实际 %v", got)
	}
	if got := exec.args[4]; got != runAt {
		t.Fatalf("显式 RunAt 被覆盖: %v", got)
	}
	if got := exec.args[5]; got != 5 {
		t.Fatalf("显式 MaxAttempts 被覆盖: %v", got)
	}
	if got := exec.args[8]; got != now {
		t.Fatalf("created_at/updated_at 应使用注入时钟，实际 %v", got)
	}
}

func TestEnqueueIDFailure(t *testing.T) {
	clk := &testClock{now: time.Date(2026, 10, 18, 9, 30, 0, 0, time.UTC)}
	runner, ids := newTestRunner(t, clk)
	ids.err = errors.New("雪花不可用")
	if err := runner.Enqueue(context.Background(), &recordedExecutor{}, EnqueueParams{Type: TypeAITask}); err == nil {
		t.Fatal("ID 生成失败应返回错误")
	}
}

func TestCancelPendingUsesClockTime(t *testing.T) {
	now := time.Date(2026, 10, 18, 9, 30, 0, 0, time.UTC)
	clk := &testClock{now: now}
	runner, _ := newTestRunner(t, clk)
	exec := &recordedExecutor{}

	if err := runner.CancelPending(context.Background(), exec, TypePostPublish, "post", 7); err != nil {
		t.Fatalf("取消失败: %v", err)
	}
	if exec.sql != sqlCancelPending {
		t.Fatalf("应使用取消 SQL")
	}
	if len(exec.args) != 4 {
		t.Fatalf("取消参数数量应为 4，实际 %d", len(exec.args))
	}
	if got := exec.args[3]; got != now {
		t.Fatalf("updated_at 应使用注入时钟，实际 %v", got)
	}
}

// TestSQLUsesApplicationTimeOnly 静态守卫：jobs 的 SQL 不得依赖数据库时间函数，
// 时间必须由应用计算后参数化传入。
func TestSQLUsesApplicationTimeOnly(t *testing.T) {
	statements := map[string]string{
		"sqlEnqueue":         sqlEnqueue,
		"sqlCancelPending":   sqlCancelPending,
		"sqlRecoverAll":      sqlRecoverAll,
		"sqlRecoverStale":    sqlRecoverStale,
		"sqlClaim":           sqlClaim,
		"sqlMarkSucceeded":   sqlMarkSucceeded,
		"sqlMarkFailed":      sqlMarkFailed,
		"sqlScheduleRetry":   sqlScheduleRetry,
		"sqlRetryJob":        sqlRetryJob,
		"sqlCancelJob":       sqlCancelJob,
		"sqlReactivate":      sqlReactivate,
		"sqlGetJob":          sqlGetJob,
		"sqlGetJobForUpdate": sqlGetJobForUpdate,
	}
	forbidden := []string{
		"now()", "current_date", "current_timestamp", "clock_timestamp",
		"localtime", "statement_timestamp", "transaction_timestamp", "interval",
	}
	for name, sql := range statements {
		if strings.TrimSpace(sql) == "" {
			t.Fatalf("%s 为空", name)
		}
		lower := strings.ToLower(sql)
		for _, word := range forbidden {
			if strings.Contains(lower, word) {
				t.Errorf("%s 含数据库时间函数/运算 %q，应改为应用时间参数", name, word)
			}
		}
	}
	if !strings.Contains(sqlClaim, "FOR UPDATE SKIP LOCKED") {
		t.Fatal("领取 SQL 必须保留 FOR UPDATE SKIP LOCKED")
	}
	if !strings.Contains(sqlEnqueue, "ON CONFLICT (idempotency_key) DO NOTHING") {
		t.Fatal("入队 SQL 必须保留幂等冲突处理")
	}
}

// ===== 人工处置契约（404 / last_error / Reactivate）=====

// fakeSnapshotRow 把 JobSnapshot 包装为 pgx.Row，用于无数据库测试。
type fakeSnapshotRow struct {
	snapshot contracts.JobSnapshot
	err      error
}

func (r *fakeSnapshotRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != 11 {
		return fmt.Errorf("快照列数应为 11，实际 %d", len(dest))
	}
	*dest[0].(*int64) = r.snapshot.ID
	*dest[1].(*string) = r.snapshot.Type
	*dest[2].(*string) = r.snapshot.RefType
	*dest[3].(**int64) = r.snapshot.RefID
	*dest[4].(*string) = r.snapshot.Status
	*dest[5].(*time.Time) = r.snapshot.RunAt
	*dest[6].(*int) = r.snapshot.Attempts
	*dest[7].(*int) = r.snapshot.MaxAttempts
	*dest[8].(*string) = r.snapshot.LastError
	*dest[9].(*time.Time) = r.snapshot.CreatedAt
	*dest[10].(*time.Time) = r.snapshot.UpdatedAt
	return nil
}

// scriptedExecutor 按调用顺序返回预置行与执行结果，并记录 SQL/参数。
type scriptedExecutor struct {
	rows    []pgx.Row
	tag     pgconn.CommandTag
	execErr error

	rowCalls int
	execSQL  []string
	execArgs [][]any
}

func (e *scriptedExecutor) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	e.execSQL = append(e.execSQL, sql)
	e.execArgs = append(e.execArgs, args)
	return e.tag, e.execErr
}

func (e *scriptedExecutor) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	return nil, errors.New("测试不应调用 Query")
}

func (e *scriptedExecutor) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	e.rowCalls++
	if len(e.rows) == 0 {
		return &fakeSnapshotRow{err: pgx.ErrNoRows}
	}
	row := e.rows[0]
	e.rows = e.rows[1:]
	return row
}

func newRunnerWithExecutor(t *testing.T, clk *testClock, exec db.Executor) *Runner {
	t.Helper()
	runner := NewRunner(nil, &testIDs{next: 100}, nil, clk)
	runner.execProvider = func(context.Context) (db.Executor, error) { return exec, nil }
	return runner
}

func testSnapshot() contracts.JobSnapshot {
	at := time.Date(2026, 10, 18, 9, 30, 0, 0, time.UTC)
	return contracts.JobSnapshot{
		ID: 7, Type: TypePostPublish, RefType: "post", Status: StatusFailed,
		RunAt: at, Attempts: 3, MaxAttempts: 3, LastError: "上游失败",
		CreatedAt: at, UpdatedAt: at,
	}
}

// TestRetryJobSQLClearsLastErrorWithEmptyString 静态断言：last_error 列 NOT NULL，
// 人工重试必须写 ” 而不是 NULL，否则整个重试事务会因约束失败而 500。
func TestRetryJobSQLClearsLastErrorWithEmptyString(t *testing.T) {
	if !strings.Contains(sqlRetryJob, "last_error = ''") {
		t.Fatalf("重试 SQL 必须把 last_error 清为空字符串: %s", sqlRetryJob)
	}
	if strings.Contains(sqlRetryJob, "last_error = NULL") {
		t.Fatalf("重试 SQL 不能写 last_error = NULL（NOT NULL 约束）: %s", sqlRetryJob)
	}
	if !strings.Contains(sqlRetryJob, "status = 'failed'") {
		t.Fatalf("人工重试只允许 failed 作业: %s", sqlRetryJob)
	}
}

// TestReactivateSQLContract 覆盖重激活只处理 canceled/paused 且重置 attempts/last_error。
func TestReactivateSQLContract(t *testing.T) {
	for _, want := range []string{
		"idempotency_key = $1", "status IN ('canceled', 'paused')",
		"status = 'pending'", "attempts = 0", "last_error = ''", "run_at = $2", "updated_at = $3",
	} {
		if !strings.Contains(sqlReactivate, want) {
			t.Fatalf("重激活 SQL 缺少 %q: %s", want, sqlReactivate)
		}
	}
}

// TestGetJobForUpdateSQLUsesRowLock 保证人工处置的读-改-写流程有行锁可用。
func TestGetJobForUpdateSQLUsesRowLock(t *testing.T) {
	if !strings.HasSuffix(strings.TrimSpace(sqlGetJobForUpdate), "FOR UPDATE") {
		t.Fatalf("GetJobForUpdate SQL 必须以 FOR UPDATE 结尾: %s", sqlGetJobForUpdate)
	}
}

// TestGetJobMissingReturnsDomainNotFound 覆盖 GetJob/GetJobForUpdate 的 404 契约。
func TestGetJobMissingReturnsDomainNotFound(t *testing.T) {
	clk := &testClock{now: time.Date(2026, 10, 18, 9, 30, 0, 0, time.UTC)}
	cases := []struct {
		name string
		call func(*Runner) (contracts.JobSnapshot, error)
	}{
		{"GetJob", func(r *Runner) (contracts.JobSnapshot, error) { return r.GetJob(context.Background(), 404) }},
		{"GetJobForUpdate", func(r *Runner) (contracts.JobSnapshot, error) {
			return r.GetJobForUpdate(context.Background(), 404)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := newRunnerWithExecutor(t, clk, &scriptedExecutor{})
			if _, err := tc.call(runner); !apperr.IsKind(err, apperr.KindNotFound) {
				t.Fatalf("%s 对不存在的作业应返回 NotFound，实际 %v", tc.name, err)
			}
		})
	}
}

// TestRetryJobReturnsNotFoundWhenStateChanged 覆盖「行存在但条件更新未命中」的 404 契约。
func TestRetryJobReturnsNotFoundWhenStateChanged(t *testing.T) {
	clk := &testClock{now: time.Date(2026, 10, 18, 9, 30, 0, 0, time.UTC)}
	snapshot := testSnapshot()
	exec := &scriptedExecutor{
		rows: []pgx.Row{&fakeSnapshotRow{snapshot: snapshot}},
		tag:  pgconn.NewCommandTag("UPDATE 0"),
	}
	runner := newRunnerWithExecutor(t, clk, exec)
	if _, err := runner.RetryJob(context.Background(), contracts.JobActionInput{JobID: 7}); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatalf("条件更新未命中应返回 NotFound，实际 %v", err)
	}
	if len(exec.execSQL) != 1 || exec.execSQL[0] != sqlRetryJob {
		t.Fatalf("应先锁行再执行重试 SQL，实际 %v", exec.execSQL)
	}
}

// TestRetryJobSuccessReturnsUpdatedSnapshot 覆盖重试成功路径：一次 now 同时写 run_at/updated_at。
func TestRetryJobSuccessReturnsUpdatedSnapshot(t *testing.T) {
	now := time.Date(2026, 10, 18, 9, 30, 0, 0, time.UTC)
	clk := &testClock{now: now}
	snapshot := testSnapshot()
	updated := snapshot
	updated.Status = StatusPending
	updated.Attempts = 0
	updated.LastError = ""
	exec := &scriptedExecutor{
		rows: []pgx.Row{
			&fakeSnapshotRow{snapshot: snapshot},
			&fakeSnapshotRow{snapshot: updated},
		},
		tag: pgconn.NewCommandTag("UPDATE 1"),
	}
	runner := newRunnerWithExecutor(t, clk, exec)
	got, err := runner.RetryJob(context.Background(), contracts.JobActionInput{JobID: 7})
	if err != nil {
		t.Fatalf("重试失败: %v", err)
	}
	if got.Status != StatusPending || got.Attempts != 0 || got.LastError != "" {
		t.Fatalf("返回快照未反映重试结果: %+v", got)
	}
	if len(exec.execArgs) != 1 {
		t.Fatalf("应执行一次更新，实际 %d", len(exec.execArgs))
	}
	if args := exec.execArgs[0]; len(args) != 2 || args[0] != int64(7) || args[1] != now {
		t.Fatalf("重试参数应为 (id, 同一 now)，实际 %#v", args)
	}
}

// TestReactivateReportsChanged 覆盖重激活的 true/false 返回。
func TestReactivateReportsChanged(t *testing.T) {
	now := time.Date(2026, 10, 18, 9, 30, 0, 0, time.UTC)
	runAt := now.Add(10 * time.Minute)
	cases := []struct {
		name string
		tag  string
		want bool
	}{
		{"命中并重激活", "UPDATE 1", true},
		{"幂等键不存在或状态不允许", "UPDATE 0", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clk := &testClock{now: now}
			exec := &scriptedExecutor{tag: pgconn.NewCommandTag(tc.tag)}
			runner := newRunnerWithExecutor(t, clk, exec)
			changed, err := runner.Reactivate(context.Background(), "post_publish:7", runAt, now)
			if err != nil {
				t.Fatalf("重激活失败: %v", err)
			}
			if changed != tc.want {
				t.Fatalf("changed = %v，期望 %v", changed, tc.want)
			}
			args := exec.execArgs[0]
			if len(args) != 3 || args[0] != "post_publish:7" || args[1] != runAt || args[2] != now {
				t.Fatalf("重激活参数应为 (幂等键, runAt, at)，实际 %#v", args)
			}
		})
	}
}

// TestEnqueueUsesExplicitAt 覆盖业务事务传入 At 时 created_at/updated_at 使用同一时间。
func TestEnqueueUsesExplicitAt(t *testing.T) {
	clockNow := time.Date(2026, 10, 18, 9, 30, 0, 0, time.UTC)
	clk := &testClock{now: clockNow}
	runner, _ := newTestRunner(t, clk)
	exec := &recordedExecutor{}
	txNow := clockNow.Add(90 * time.Second)

	if err := runner.Enqueue(context.Background(), exec, EnqueueParams{
		Type:           TypePostPublish,
		IdempotencyKey: "post_publish:1",
		At:             txNow,
	}); err != nil {
		t.Fatalf("入队失败: %v", err)
	}
	if got := exec.args[4]; got != txNow {
		t.Fatalf("RunAt 零值应回退事务时间，实际 %v", got)
	}
	if got := exec.args[8]; got != txNow {
		t.Fatalf("created_at/updated_at 应使用显式 At，实际 %v", got)
	}
}

// 编译期断言：测试用假执行器满足包内 Querier（即 db.Executor）。
var _ Querier = (*recordedExecutor)(nil)
var _ Querier = (*scriptedExecutor)(nil)
