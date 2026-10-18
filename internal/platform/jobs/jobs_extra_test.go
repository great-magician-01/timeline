package jobs

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/db"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
)

// 本文件在既有 jobs_test.go 的 fake（testClock/testIDs/scriptedExecutor/fakeSnapshotRow）
// 基础上补齐人工处置（admin.go）与负载解析（PayloadJSON）路径，复用既有声明避免重复。

var extraTestNow = time.Date(2026, 10, 18, 9, 30, 0, 0, time.UTC)

// snapshotRows 是 pgx.Rows 的假实现：把快照列表逐行交给既有 fakeSnapshotRow 扫描。
// 复用 fakeSnapshotRow 保证扫描列顺序与生产代码一致。
type snapshotRows struct {
	snapshots []contracts.JobSnapshot
	idx       int
	scanErr   error
	rowsErr   error
}

func (r *snapshotRows) Close() {}

func (r *snapshotRows) Err() error { return r.rowsErr }

func (r *snapshotRows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }

func (r *snapshotRows) FieldDescriptions() []pgconn.FieldDescription { return nil }

func (r *snapshotRows) Next() bool {
	if r.rowsErr != nil || r.idx >= len(r.snapshots) {
		return false
	}
	r.idx++
	return true
}

func (r *snapshotRows) Scan(dest ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	if r.idx == 0 || r.idx > len(r.snapshots) {
		return errors.New("未调用 Next 就 Scan")
	}
	return (&fakeSnapshotRow{snapshot: r.snapshots[r.idx-1]}).Scan(dest...)
}

func (r *snapshotRows) Values() ([]any, error) { return nil, errors.New("测试不应调用 Values") }

func (r *snapshotRows) RawValues() [][]byte { return nil }

func (r *snapshotRows) Conn() *pgx.Conn { return nil }

func (r *snapshotRows) TypeMap() *pgtype.Map { return nil }

var _ pgx.Rows = (*snapshotRows)(nil)

// listingExecutor 返回预置行集合并记录 Query 的 SQL/参数。
type listingExecutor struct {
	rows      pgx.Rows
	queryErr  error
	querySQL  string
	queryArgs []any
	calls     int
}

func (e *listingExecutor) Exec(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("测试不应调用 Exec")
}

func (e *listingExecutor) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	e.calls++
	e.querySQL = sql
	e.queryArgs = args
	if e.queryErr != nil {
		return nil, e.queryErr
	}
	return e.rows, nil
}

func (e *listingExecutor) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	return nil
}

var _ db.Executor = (*listingExecutor)(nil)

func TestPayloadJSON(t *testing.T) {
	t.Run("空负载不报错且不解码", func(t *testing.T) {
		var dst map[string]any
		for _, payload := range [][]byte{nil, {}} {
			if err := (Job{Payload: payload}).PayloadJSON(&dst); err != nil {
				t.Fatalf("空负载应返回 nil，实际 %v", err)
			}
		}
		if dst != nil {
			t.Fatalf("空负载不应写入目标: %#v", dst)
		}
	})

	t.Run("合法 JSON 解码", func(t *testing.T) {
		var dst struct {
			PostID int64 `json:"post_id"`
		}
		if err := (Job{Payload: []byte(`{"post_id":42}`)}).PayloadJSON(&dst); err != nil {
			t.Fatalf("解码失败: %v", err)
		}
		if dst.PostID != 42 {
			t.Fatalf("解码结果错误: %+v", dst)
		}
	})

	t.Run("非法 JSON 返回错误", func(t *testing.T) {
		var dst map[string]any
		if err := (Job{Payload: []byte(`{`)}).PayloadJSON(&dst); err == nil {
			t.Fatal("非法 JSON 应返回错误")
		}
	})
}

func TestPausePending(t *testing.T) {
	clk := &testClock{now: extraTestNow}
	explicitAt := extraTestNow.Add(-time.Hour)
	refIDs := []int64{7, 8}

	t.Run("空引用列表直接返回 0 且不访问数据库", func(t *testing.T) {
		exec := &scriptedExecutor{}
		runner := newRunnerWithExecutor(t, clk, exec)
		got, err := runner.PausePending(context.Background(), contracts.PausePendingJobsInput{
			JobType: TypeAITask, RefType: "ai",
		})
		if err != nil || got != 0 {
			t.Fatalf("空引用列表应返回 (0, nil)，实际 (%d, %v)", got, err)
		}
		if len(exec.execSQL) != 0 {
			t.Fatalf("空引用列表不应执行 SQL，实际 %v", exec.execSQL)
		}
	})

	t.Run("使用显式应用时间并返回影响行数", func(t *testing.T) {
		exec := &scriptedExecutor{tag: pgconn.NewCommandTag("UPDATE 2")}
		runner := newRunnerWithExecutor(t, clk, exec)
		got, err := runner.PausePending(context.Background(), contracts.PausePendingJobsInput{
			JobType: TypeAITask, RefType: "ai", RefIDs: refIDs, At: explicitAt,
		})
		if err != nil || got != 2 {
			t.Fatalf("暂停待执行作业应返回 (2, nil)，实际 (%d, %v)", got, err)
		}
		if len(exec.execSQL) != 1 || exec.execSQL[0] != sqlPausePending {
			t.Fatalf("应执行暂停 SQL，实际 %v", exec.execSQL)
		}
		args := exec.execArgs[0]
		if len(args) != 4 || args[0] != explicitAt || args[1] != TypeAITask || args[2] != "ai" {
			t.Fatalf("暂停参数错误: %#v", args)
		}
		gotIDs, ok := args[3].([]int64)
		if !ok || len(gotIDs) != 2 || gotIDs[0] != 7 || gotIDs[1] != 8 {
			t.Fatalf("ref_ids 参数错误: %#v", args[3])
		}
	})

	t.Run("零值时间回退注入时钟", func(t *testing.T) {
		exec := &scriptedExecutor{tag: pgconn.NewCommandTag("UPDATE 1")}
		runner := newRunnerWithExecutor(t, clk, exec)
		if _, err := runner.PausePending(context.Background(), contracts.PausePendingJobsInput{
			RefType: "ai", RefIDs: refIDs,
		}); err != nil {
			t.Fatalf("暂停失败: %v", err)
		}
		if got := exec.execArgs[0][0]; got != extraTestNow {
			t.Fatalf("零值时间应回退注入时钟，实际 %v", got)
		}
	})

	t.Run("执行错误被包装", func(t *testing.T) {
		exec := &scriptedExecutor{execErr: errors.New("数据库不可用")}
		runner := newRunnerWithExecutor(t, clk, exec)
		_, err := runner.PausePending(context.Background(), contracts.PausePendingJobsInput{
			RefType: "ai", RefIDs: refIDs,
		})
		if err == nil || !strings.Contains(err.Error(), "暂停待执行作业失败") {
			t.Fatalf("错误应说明暂停失败，实际 %v", err)
		}
	})

	t.Run("执行器解析失败时返回错误", func(t *testing.T) {
		runner := NewRunner(nil, &testIDs{next: 100}, nil, clk)
		runner.execProvider = func(context.Context) (db.Executor, error) {
			return nil, errors.New("数据库尚未就绪")
		}
		if _, err := runner.PausePending(context.Background(), contracts.PausePendingJobsInput{
			RefType: "ai", RefIDs: refIDs,
		}); err == nil || !strings.Contains(err.Error(), "数据库尚未就绪") {
			t.Fatalf("应返回执行器解析错误，实际 %v", err)
		}
	})
}

func TestPauseAllPending(t *testing.T) {
	clk := &testClock{now: extraTestNow}

	t.Run("拒绝所有引用类型下全部待执行作业", func(t *testing.T) {
		exec := &scriptedExecutor{tag: pgconn.NewCommandTag("UPDATE 5")}
		runner := newRunnerWithExecutor(t, clk, exec)
		got, err := runner.PauseAllPending(context.Background(), contracts.PauseAllPendingInput{
			RefType: "ai",
		})
		if err != nil || got != 5 {
			t.Fatalf("应返回 (5, nil)，实际 (%d, %v)", got, err)
		}
		if len(exec.execSQL) != 1 || exec.execSQL[0] != sqlPauseAllPending {
			t.Fatalf("应执行 PauseAllPending SQL，实际 %v", exec.execSQL)
		}
		args := exec.execArgs[0]
		if len(args) != 3 || args[0] != extraTestNow || args[1] != "ai" || args[2] != "" {
			t.Fatalf("参数应为 (at, ref_type, 空 job_type)，实际 %#v", args)
		}
	})

	t.Run("显式时间与 job_type 过滤", func(t *testing.T) {
		exec := &scriptedExecutor{tag: pgconn.NewCommandTag("UPDATE 0")}
		runner := newRunnerWithExecutor(t, clk, exec)
		explicit := extraTestNow.Add(time.Minute)
		if _, err := runner.PauseAllPending(context.Background(), contracts.PauseAllPendingInput{
			JobType: TypeAISchedule, RefType: "ai", At: explicit,
		}); err != nil {
			t.Fatalf("暂停失败: %v", err)
		}
		args := exec.execArgs[0]
		if args[0] != explicit || args[1] != "ai" || args[2] != TypeAISchedule {
			t.Fatalf("参数错误: %#v", args)
		}
	})

	t.Run("执行错误被包装", func(t *testing.T) {
		exec := &scriptedExecutor{execErr: errors.New("数据库不可用")}
		runner := newRunnerWithExecutor(t, clk, exec)
		_, err := runner.PauseAllPending(context.Background(), contracts.PauseAllPendingInput{RefType: "ai"})
		if err == nil || !strings.Contains(err.Error(), "暂停全部待执行作业失败") {
			t.Fatalf("错误应说明暂停失败，实际 %v", err)
		}
	})
}

func TestCancelJob(t *testing.T) {
	clk := &testClock{now: extraTestNow}

	t.Run("作业不存在返回 NotFound 且不执行更新", func(t *testing.T) {
		exec := &scriptedExecutor{}
		runner := newRunnerWithExecutor(t, clk, exec)
		if _, err := runner.CancelJob(context.Background(), contracts.JobActionInput{JobID: 7}); !apperr.IsKind(err, apperr.KindNotFound) {
			t.Fatalf("不存在的作业应返回 NotFound，实际 %v", err)
		}
		if len(exec.execSQL) != 0 {
			t.Fatalf("不存在时不应执行更新: %v", exec.execSQL)
		}
	})

	t.Run("状态不允许返回 NotFound", func(t *testing.T) {
		snapshot := testSnapshot()
		exec := &scriptedExecutor{
			rows: []pgx.Row{&fakeSnapshotRow{snapshot: snapshot}},
			tag:  pgconn.NewCommandTag("UPDATE 0"),
		}
		runner := newRunnerWithExecutor(t, clk, exec)
		if _, err := runner.CancelJob(context.Background(), contracts.JobActionInput{JobID: 7}); !apperr.IsKind(err, apperr.KindNotFound) {
			t.Fatalf("条件更新未命中应返回 NotFound，实际 %v", err)
		}
		if len(exec.execSQL) != 1 || exec.execSQL[0] != sqlCancelJob {
			t.Fatalf("应执行取消 SQL，实际 %v", exec.execSQL)
		}
	})

	t.Run("成功返回最新快照", func(t *testing.T) {
		snapshot := testSnapshot()
		canceled := snapshot
		canceled.Status = StatusCanceled
		exec := &scriptedExecutor{
			rows: []pgx.Row{
				&fakeSnapshotRow{snapshot: snapshot},
				&fakeSnapshotRow{snapshot: canceled},
			},
			tag: pgconn.NewCommandTag("UPDATE 1"),
		}
		runner := newRunnerWithExecutor(t, clk, exec)
		got, err := runner.CancelJob(context.Background(), contracts.JobActionInput{
			JobID: 7, At: extraTestNow.Add(-time.Minute),
		})
		if err != nil {
			t.Fatalf("取消失败: %v", err)
		}
		if got.ID != 7 || got.Status != StatusCanceled {
			t.Fatalf("返回快照未反映取消结果: %+v", got)
		}
		args := exec.execArgs[0]
		if len(args) != 2 || args[0] != int64(7) || args[1] != extraTestNow.Add(-time.Minute) {
			t.Fatalf("取消参数错误: %#v", args)
		}
	})

	t.Run("执行错误被包装", func(t *testing.T) {
		exec := &scriptedExecutor{
			rows:    []pgx.Row{&fakeSnapshotRow{snapshot: testSnapshot()}},
			execErr: errors.New("数据库不可用"),
		}
		runner := newRunnerWithExecutor(t, clk, exec)
		_, err := runner.CancelJob(context.Background(), contracts.JobActionInput{JobID: 7})
		if err == nil || !strings.Contains(err.Error(), "取消作业失败") {
			t.Fatalf("错误应说明取消失败，实际 %v", err)
		}
	})
}

func TestSetJobStatus(t *testing.T) {
	clk := &testClock{now: extraTestNow}

	t.Run("显式时间写入指定状态", func(t *testing.T) {
		exec := &scriptedExecutor{}
		runner := newRunnerWithExecutor(t, clk, exec)
		explicit := extraTestNow.Add(2 * time.Minute)
		if err := runner.SetJobStatus(context.Background(), 7, StatusPaused, explicit); err != nil {
			t.Fatalf("写状态失败: %v", err)
		}
		if len(exec.execSQL) != 1 || exec.execSQL[0] != sqlSetJobStatus {
			t.Fatalf("应执行写状态 SQL，实际 %v", exec.execSQL)
		}
		args := exec.execArgs[0]
		if len(args) != 3 || args[0] != int64(7) || args[1] != StatusPaused || args[2] != explicit {
			t.Fatalf("参数应为 (id, status, at)，实际 %#v", args)
		}
	})

	t.Run("零值时间回退注入时钟", func(t *testing.T) {
		exec := &scriptedExecutor{}
		runner := newRunnerWithExecutor(t, clk, exec)
		if err := runner.SetJobStatus(context.Background(), 8, StatusCanceled, time.Time{}); err != nil {
			t.Fatalf("写状态失败: %v", err)
		}
		if got := exec.execArgs[0][2]; got != extraTestNow {
			t.Fatalf("零值时间应回退注入时钟，实际 %v", got)
		}
	})

	t.Run("执行错误被包装", func(t *testing.T) {
		exec := &scriptedExecutor{execErr: errors.New("数据库不可用")}
		runner := newRunnerWithExecutor(t, clk, exec)
		err := runner.SetJobStatus(context.Background(), 7, StatusPaused, extraTestNow)
		if err == nil || !strings.Contains(err.Error(), "写作业状态失败") {
			t.Fatalf("错误应说明写状态失败，实际 %v", err)
		}
	})
}

func TestListByRef(t *testing.T) {
	clk := &testClock{now: extraTestNow}

	t.Run("空引用列表返回 nil 且不查询", func(t *testing.T) {
		exec := &listingExecutor{}
		runner := newRunnerWithExecutor(t, clk, exec)
		got, err := runner.ListByRef(context.Background(), contracts.JobRefQuery{})
		if err != nil || got != nil {
			t.Fatalf("空引用列表应返回 (nil, nil)，实际 (%v, %v)", got, err)
		}
		if exec.calls != 0 {
			t.Fatalf("空引用列表不应查询，实际 %d 次", exec.calls)
		}
	})

	t.Run("默认上限与基本过滤", func(t *testing.T) {
		first := testSnapshot()
		second := testSnapshot()
		second.ID = 8
		second.Status = StatusPending
		exec := &listingExecutor{rows: &snapshotRows{snapshots: []contracts.JobSnapshot{first, second}}}
		runner := newRunnerWithExecutor(t, clk, exec)
		got, err := runner.ListByRef(context.Background(), contracts.JobRefQuery{RefIDs: []int64{7, 8}})
		if err != nil {
			t.Fatalf("查询失败: %v", err)
		}
		if len(got) != 2 || got[0].ID != 7 || got[1].ID != 8 {
			t.Fatalf("快照解析错误: %+v", got)
		}
		if !strings.Contains(exec.querySQL, "ref_id = ANY($1)") || !strings.Contains(exec.querySQL, "ORDER BY id DESC LIMIT $2") {
			t.Fatalf("查询 SQL 错误: %q", exec.querySQL)
		}
		if len(exec.queryArgs) != 2 {
			t.Fatalf("参数数量 = %d，期望 2", len(exec.queryArgs))
		}
		if exec.queryArgs[1] != 100 {
			t.Fatalf("非法 limit 应回退 100，实际 %v", exec.queryArgs[1])
		}
	})

	t.Run("类型过滤与显式上限", func(t *testing.T) {
		exec := &listingExecutor{rows: &snapshotRows{}}
		runner := newRunnerWithExecutor(t, clk, exec)
		got, err := runner.ListByRef(context.Background(), contracts.JobRefQuery{
			RefType: "ai", RefIDs: []int64{1, 2, 3},
			Types: []string{TypeAITask, TypeAISchedule}, Limit: 5,
		})
		if err != nil {
			t.Fatalf("查询失败: %v", err)
		}
		if len(got) != 0 || got == nil {
			t.Fatalf("空结果应返回空切片且非 nil: %#v", got)
		}
		for _, want := range []string{"ref_id = ANY($1)", " AND ref_type = $2", " AND job_type = ANY($3)", "LIMIT $4"} {
			if !strings.Contains(exec.querySQL, want) {
				t.Fatalf("查询 SQL 缺少 %q: %q", want, exec.querySQL)
			}
		}
		args := exec.queryArgs
		if len(args) != 4 || args[1] != "ai" || args[3] != 5 {
			t.Fatalf("查询参数错误: %#v", args)
		}
		types, ok := args[2].([]string)
		if !ok || len(types) != 2 || types[0] != TypeAITask {
			t.Fatalf("job_type 过滤参数错误: %#v", args[2])
		}
	})

	t.Run("超上限限制为 100", func(t *testing.T) {
		exec := &listingExecutor{rows: &snapshotRows{}}
		runner := newRunnerWithExecutor(t, clk, exec)
		for _, limit := range []int{-1, 0, 201, 1000} {
			if _, err := runner.ListByRef(context.Background(), contracts.JobRefQuery{
				RefIDs: []int64{1}, Limit: limit,
			}); err != nil {
				t.Fatalf("查询失败: %v", err)
			}
			args := exec.queryArgs
			if got := args[len(args)-1]; got != 100 {
				t.Fatalf("limit=%d 时应限制为 100，实际 %v", limit, got)
			}
		}
	})

	t.Run("查询错误被包装", func(t *testing.T) {
		exec := &listingExecutor{queryErr: errors.New("数据库不可用")}
		runner := newRunnerWithExecutor(t, clk, exec)
		_, err := runner.ListByRef(context.Background(), contracts.JobRefQuery{RefIDs: []int64{1}})
		if err == nil || !strings.Contains(err.Error(), "查询作业快照失败") {
			t.Fatalf("错误应说明查询失败，实际 %v", err)
		}
	})

	t.Run("扫描错误向上返回", func(t *testing.T) {
		scanErr := errors.New("列类型不匹配")
		exec := &listingExecutor{rows: &snapshotRows{snapshots: []contracts.JobSnapshot{testSnapshot()}, scanErr: scanErr}}
		runner := newRunnerWithExecutor(t, clk, exec)
		if _, err := runner.ListByRef(context.Background(), contracts.JobRefQuery{RefIDs: []int64{1}}); !errors.Is(err, scanErr) {
			t.Fatalf("应返回扫描错误，实际 %v", err)
		}
	})

	t.Run("遍历错误向上返回", func(t *testing.T) {
		rowsErr := errors.New("结果集中断")
		exec := &listingExecutor{rows: &snapshotRows{rowsErr: rowsErr}}
		runner := newRunnerWithExecutor(t, clk, exec)
		if _, err := runner.ListByRef(context.Background(), contracts.JobRefQuery{RefIDs: []int64{1}}); !errors.Is(err, rowsErr) {
			t.Fatalf("应返回结果集错误，实际 %v", err)
		}
	})
}

// TestGetJobScanError 覆盖读取快照时非 ErrNoRows 的扫描错误被包装。
func TestGetJobScanError(t *testing.T) {
	scanErr := errors.New("扫描失败")
	clk := &testClock{now: extraTestNow}

	exec := &scriptedExecutor{rows: []pgx.Row{&fakeSnapshotRow{err: scanErr}}}
	runner := newRunnerWithExecutor(t, clk, exec)
	if _, err := runner.GetJob(context.Background(), 7); !errors.Is(err, scanErr) {
		t.Fatalf("应返回扫描错误，实际 %v", err)
	}

	locked := &scriptedExecutor{rows: []pgx.Row{&fakeSnapshotRow{err: scanErr}}}
	runner = newRunnerWithExecutor(t, clk, locked)
	if _, err := runner.GetJobForUpdate(context.Background(), 7); !errors.Is(err, scanErr) {
		t.Fatalf("加锁读取也应返回扫描错误，实际 %v", err)
	}
}

// TestGetJobSuccess 覆盖读取快照的成功路径（单行与加锁读取）。
func TestGetJobSuccess(t *testing.T) {
	clk := &testClock{now: extraTestNow}
	want := testSnapshot()

	exec := &scriptedExecutor{rows: []pgx.Row{&fakeSnapshotRow{snapshot: want}}}
	runner := newRunnerWithExecutor(t, clk, exec)
	got, err := runner.GetJob(context.Background(), want.ID)
	if err != nil {
		t.Fatalf("读取作业失败: %v", err)
	}
	if got.ID != want.ID || got.Status != want.Status || got.LastError != want.LastError {
		t.Fatalf("快照字段错误: %+v", got)
	}

	locked := &scriptedExecutor{rows: []pgx.Row{&fakeSnapshotRow{snapshot: want}}}
	runner = newRunnerWithExecutor(t, clk, locked)
	if _, err := runner.GetJobForUpdate(context.Background(), want.ID); err != nil {
		t.Fatalf("加锁读取作业失败: %v", err)
	}
}

// TestEnqueueErrorPaths 覆盖入队的负载序列化失败与写入失败路径。
func TestEnqueueErrorPaths(t *testing.T) {
	clk := &testClock{now: extraTestNow}

	t.Run("负载无法序列化", func(t *testing.T) {
		runner, ids := newTestRunner(t, clk)
		err := runner.Enqueue(context.Background(), &recordedExecutor{}, EnqueueParams{
			Type: TypeAITask, Payload: make(chan int),
		})
		if err == nil || !strings.Contains(err.Error(), "序列化作业负载失败") {
			t.Fatalf("应返回序列化错误，实际 %v", err)
		}
		if ids.calls != 1 {
			t.Fatalf("ID 在序列化前生成，实际生成 %d 次", ids.calls)
		}
	})

	t.Run("写入作业失败被包装", func(t *testing.T) {
		runner, _ := newTestRunner(t, clk)
		exec := &recordedExecutor{err: errors.New("数据库不可用")}
		err := runner.Enqueue(context.Background(), exec, EnqueueParams{Type: TypeAITask, IdempotencyKey: "ai:1"})
		if err == nil || !strings.Contains(err.Error(), "写入后台作业失败") {
			t.Fatalf("应返回写入失败错误，实际 %v", err)
		}
	})
}

// TestCancelPendingError 覆盖取消待执行作业的执行失败路径。
func TestCancelPendingError(t *testing.T) {
	clk := &testClock{now: extraTestNow}
	runner, _ := newTestRunner(t, clk)
	exec := &recordedExecutor{err: errors.New("数据库不可用")}
	err := runner.CancelPending(context.Background(), exec, TypePostPublish, "post", 7)
	if err == nil || !strings.Contains(err.Error(), "取消待执行后台作业失败") {
		t.Fatalf("应返回取消失败错误，实际 %v", err)
	}
}

// TestExecutorResolution 覆盖人工处置的执行器解析失败路径（无数据库）。
func TestExecutorResolution(t *testing.T) {
	clk := &testClock{now: extraTestNow}

	t.Run("nil 运行器返回未初始化", func(t *testing.T) {
		var runner *Runner
		if _, err := runner.GetJob(context.Background(), 1); err == nil || !strings.Contains(err.Error(), "作业运行器未初始化") {
			t.Fatalf("nil 运行器应返回未初始化错误，实际 %v", err)
		}
	})

	t.Run("连接池提供者为空返回未初始化", func(t *testing.T) {
		runner := NewRunner(nil, &testIDs{next: 100}, nil, clk)
		if _, err := runner.GetJob(context.Background(), 1); err == nil || !strings.Contains(err.Error(), "作业运行器未初始化") {
			t.Fatalf("连接池提供者为空应返回未初始化错误，实际 %v", err)
		}
	})

	t.Run("连接池未就绪返回明确错误", func(t *testing.T) {
		runner := NewRunner(fakePoolProvider{}, &testIDs{next: 100}, nil, clk)
		if _, err := runner.GetJob(context.Background(), 1); err == nil || !strings.Contains(err.Error(), "数据库尚未就绪") {
			t.Fatalf("连接池为 nil 应返回未就绪错误，实际 %v", err)
		}
	})

	t.Run("注入执行器错误原样返回", func(t *testing.T) {
		providerErr := errors.New("事务不可用")
		runner := NewRunner(nil, &testIDs{next: 100}, nil, clk)
		runner.execProvider = func(context.Context) (db.Executor, error) { return nil, providerErr }
		if _, err := runner.GetJob(context.Background(), 1); !errors.Is(err, providerErr) {
			t.Fatalf("应返回注入错误，实际 %v", err)
		}
	})
}

// TestRetryJobAndReactivateErrorPaths 覆盖重试/重激活的执行与参数错误路径。
func TestRetryJobAndReactivateErrorPaths(t *testing.T) {
	clk := &testClock{now: extraTestNow}

	t.Run("重试执行错误被包装", func(t *testing.T) {
		exec := &scriptedExecutor{
			rows:    []pgx.Row{&fakeSnapshotRow{snapshot: testSnapshot()}},
			execErr: errors.New("数据库不可用"),
		}
		runner := newRunnerWithExecutor(t, clk, exec)
		_, err := runner.RetryJob(context.Background(), contracts.JobActionInput{JobID: 7})
		if err == nil || !strings.Contains(err.Error(), "重试作业失败") {
			t.Fatalf("应返回重试失败错误，实际 %v", err)
		}
	})

	t.Run("重试执行器解析失败", func(t *testing.T) {
		providerErr := errors.New("事务不可用")
		runner := NewRunner(nil, &testIDs{next: 100}, nil, clk)
		runner.execProvider = func(context.Context) (db.Executor, error) { return nil, providerErr }
		if _, err := runner.RetryJob(context.Background(), contracts.JobActionInput{JobID: 7}); !errors.Is(err, providerErr) {
			t.Fatalf("应返回执行器错误，实际 %v", err)
		}
	})

	t.Run("重激活幂等键为空", func(t *testing.T) {
		runner := newRunnerWithExecutor(t, clk, &scriptedExecutor{})
		changed, err := runner.Reactivate(context.Background(), "", extraTestNow, extraTestNow)
		if changed || err == nil || !strings.Contains(err.Error(), "作业幂等键不能为空") {
			t.Fatalf("空幂等键应返回错误，实际 (%v, %v)", changed, err)
		}
	})

	t.Run("重激活执行错误被包装", func(t *testing.T) {
		exec := &scriptedExecutor{execErr: errors.New("数据库不可用")}
		runner := newRunnerWithExecutor(t, clk, exec)
		if _, err := runner.Reactivate(context.Background(), "post_publish:1", extraTestNow, extraTestNow); err == nil ||
			!strings.Contains(err.Error(), "重新激活作业失败") {
			t.Fatalf("应返回重激活失败错误，实际 %v", err)
		}
	})
}

// TestAdminExecutorErrors 覆盖各人工处置方法在执行器解析失败时的返回。
func TestAdminExecutorErrors(t *testing.T) {
	clk := &testClock{now: extraTestNow}
	providerErr := errors.New("事务不可用")
	newRunner := func() *Runner {
		runner := NewRunner(nil, &testIDs{next: 100}, nil, clk)
		runner.execProvider = func(context.Context) (db.Executor, error) { return nil, providerErr }
		return runner
	}
	ctx := context.Background()

	if _, err := newRunner().PauseAllPending(ctx, contracts.PauseAllPendingInput{RefType: "ai"}); !errors.Is(err, providerErr) {
		t.Fatalf("PauseAllPending 应返回执行器错误，实际 %v", err)
	}
	if _, err := newRunner().CancelJob(ctx, contracts.JobActionInput{JobID: 7}); !errors.Is(err, providerErr) {
		t.Fatalf("CancelJob 应返回执行器错误，实际 %v", err)
	}
	if err := newRunner().SetJobStatus(ctx, 7, StatusPaused, extraTestNow); !errors.Is(err, providerErr) {
		t.Fatalf("SetJobStatus 应返回执行器错误，实际 %v", err)
	}
	if _, err := newRunner().GetJobForUpdate(ctx, 7); !errors.Is(err, providerErr) {
		t.Fatalf("GetJobForUpdate 应返回执行器错误，实际 %v", err)
	}
	if _, err := newRunner().ListByRef(ctx, contracts.JobRefQuery{RefIDs: []int64{1}}); !errors.Is(err, providerErr) {
		t.Fatalf("ListByRef 应返回执行器错误，实际 %v", err)
	}
}

// TestExecutorFallsBackToPool 覆盖无事务时执行器回退连接池（查询因连接失败返回错误）。
func TestExecutorFallsBackToPool(t *testing.T) {
	runner := NewRunner(fakePoolProvider{newUnreachablePool(t)}, &testIDs{next: 100}, newDiscardLogger(),
		&testClock{now: extraTestNow})
	_, err := runner.GetJob(context.Background(), 1)
	if err == nil || !strings.Contains(err.Error(), "读取作业快照失败") {
		t.Fatalf("应返回连接失败包装错误，实际 %v", err)
	}
}

// TestSnapshotFromRow 覆盖 scanJobSnapshot 对合法行的字段映射。
func TestSnapshotFromRow(t *testing.T) {
	refID := int64(42)
	snapshot := testSnapshot()
	snapshot.RefID = &refID
	got, err := scanJobSnapshot(&fakeSnapshotRow{snapshot: snapshot})
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	if got.ID != snapshot.ID || got.Type != snapshot.Type || got.RefID == nil || *got.RefID != refID ||
		got.Status != snapshot.Status || !got.RunAt.Equal(snapshot.RunAt) || got.LastError != snapshot.LastError {
		t.Fatalf("快照字段映射错误: %+v", got)
	}
}
