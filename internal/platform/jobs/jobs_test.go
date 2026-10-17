package jobs

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
		"sqlEnqueue":       sqlEnqueue,
		"sqlCancelPending": sqlCancelPending,
		"sqlRecoverAll":    sqlRecoverAll,
		"sqlRecoverStale":  sqlRecoverStale,
		"sqlClaim":         sqlClaim,
		"sqlMarkSucceeded": sqlMarkSucceeded,
		"sqlMarkFailed":    sqlMarkFailed,
		"sqlScheduleRetry": sqlScheduleRetry,
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

// 编译期断言：测试用假执行器满足包内 Querier（即 db.Executor）。
var _ Querier = (*recordedExecutor)(nil)
