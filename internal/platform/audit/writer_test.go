package audit

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/great-magician-01/timeline/internal/platform/db"
)

// recordedExecutor 记录一次 Exec 调用，用于无数据库断言参数顺序与显式时间。
type recordedExecutor struct {
	calls int
	sql   string
	args  []any
	err   error
}

func (e *recordedExecutor) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	e.calls++
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
	next int64
	err  error
}

func (g *testIDs) Next() (int64, error) {
	if g.err != nil {
		return 0, g.err
	}
	g.next++
	return g.next, nil
}

// staticExecutor 让 Write 始终拿到同一个记录器，并记录 ctx 是否被传递。
func staticExecutor(exec *recordedExecutor) func(context.Context) db.Executor {
	return func(context.Context) db.Executor { return exec }
}

func TestWriteUsesExplicitEntryTimeAndID(t *testing.T) {
	exec := &recordedExecutor{}
	ids := &testIDs{}
	w := newWriter(staticExecutor(exec), ids)

	at := time.Date(2026, 10, 18, 10, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	targetID := int64(9)
	ctx := context.Background()
	if err := w.Write(ctx, Entry{
		AdminID:    7,
		Action:     ActionPostDelete,
		TargetType: "post",
		TargetID:   &targetID,
		Detail:     map[string]any{"reason": "垃圾内容"},
		At:         at,
	}); err != nil {
		t.Fatalf("写入审计失败: %v", err)
	}

	if exec.calls != 1 {
		t.Fatalf("应执行一次 INSERT，实际 %d", exec.calls)
	}
	if exec.sql != sqlInsertAuditLog {
		t.Fatal("应使用审计 INSERT 语句")
	}
	if len(exec.args) != 7 {
		t.Fatalf("INSERT 参数数量应为 7，实际 %d", len(exec.args))
	}
	if got := exec.args[0]; got != int64(1) {
		t.Fatalf("审计 ID 应来自 IDGenerator，实际 %v", got)
	}
	if got := exec.args[1]; got != int64(7) {
		t.Fatalf("admin_id 错误: %v", got)
	}
	if got := exec.args[2]; got != ActionPostDelete {
		t.Fatalf("action_type 错误: %v", got)
	}
	if got := exec.args[6]; got != at {
		t.Fatalf("created_at 应使用 Entry.At，实际 %v", got)
	}
	if _, ok := exec.args[5].(map[string]any); !ok {
		t.Fatalf("detail 应原样传入 map，实际 %T", exec.args[5])
	}
}

func TestWriteNilDetailUsesEmptyObject(t *testing.T) {
	exec := &recordedExecutor{}
	w := newWriter(staticExecutor(exec), &testIDs{})
	if err := w.Write(context.Background(), Entry{
		AdminID:    1,
		Action:     ActionSettingsUpdate,
		TargetType: "settings",
		At:         time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("写入审计失败: %v", err)
	}
	detail, ok := exec.args[5].(map[string]any)
	if !ok || len(detail) != 0 {
		t.Fatalf("Detail 为 nil 时应写空对象，实际 %#v", exec.args[5])
	}
	if _, hasTarget := exec.args[4].(*int64); !hasTarget {
		t.Fatalf("target_id 应传 *int64，实际 %T", exec.args[4])
	}
}

func TestWritePropagatesErrors(t *testing.T) {
	at := time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC)
	idsErr := errors.New("ID 生成失败")
	w := newWriter(staticExecutor(&recordedExecutor{}), &testIDs{err: idsErr})
	if err := w.Write(context.Background(), Entry{At: at}); !errors.Is(err, idsErr) {
		t.Fatalf("ID 生成错误应被包装返回，实际 %v", err)
	}

	execErr := errors.New("数据库不可用")
	w = newWriter(staticExecutor(&recordedExecutor{err: execErr}), &testIDs{})
	if err := w.Write(context.Background(), Entry{At: at}); !errors.Is(err, execErr) {
		t.Fatalf("执行错误应被包装返回，实际 %v", err)
	}
}

// TestSQLUsesApplicationTimeOnly 静态守卫：审计 SQL 不依赖数据库时间函数。
func TestSQLUsesApplicationTimeOnly(t *testing.T) {
	lower := strings.ToLower(sqlInsertAuditLog)
	for _, word := range []string{"now()", "current_timestamp", "current_date", "interval"} {
		if strings.Contains(lower, word) {
			t.Fatalf("审计 SQL 含 %q，应改为应用时间参数", word)
		}
	}
	if !strings.Contains(lower, "created_at") {
		t.Fatal("审计 SQL 必须显式写入 created_at")
	}
}

var _ db.Executor = (*recordedExecutor)(nil)
