package audit

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/platform/db"
)

// IDGenerator 生成雪花 ID；与 platform/id 同形，避免包之间相互依赖。
type IDGenerator interface {
	Next() (int64, error)
}

// Entry 是一条待写入的审计记录。
//
// At 是应用侧时钟计算的写入时间，必须由调用方显式传入（通常与业务变更共用同一
// 个 now），保证审计记录时间与业务时间一致。
type Entry struct {
	AdminID    int64
	Action     string
	TargetType string
	TargetID   *int64
	Detail     any
	At         time.Time
}

// Writer 写入一条审计记录；必须与业务变更在同一事务内调用（ctx 携带事务时
// 自动复用该事务），保证二者同生共死。
type Writer interface {
	Write(ctx context.Context, entry Entry) error
}

type writer struct {
	// exec 解析当前上下文对应的执行器：ctx 内有事务时用事务，否则用连接池。
	exec func(ctx context.Context) db.Executor
	ids  IDGenerator
}

// NewWriter 构造审计写入器。pool 只用于承载执行器解析，Write 会优先复用 ctx 中
// 的平台事务（db.WithTx），因此调用方应在业务事务内调用 Write。
func NewWriter(pool *pgxpool.Pool, ids IDGenerator) Writer {
	return newWriter(func(ctx context.Context) db.Executor {
		return db.ExecutorFrom(ctx, pool)
	}, ids)
}

// newWriter 是便于测试的构造入口（注入执行器解析函数，无需真实数据库）。
func newWriter(exec func(ctx context.Context) db.Executor, ids IDGenerator) Writer {
	return &writer{exec: exec, ids: ids}
}

// Write 写入一条审计记录；Detail 为 nil 时写空对象，与历史行为一致。
func (w *writer) Write(ctx context.Context, entry Entry) error {
	id, err := w.ids.Next()
	if err != nil {
		return fmt.Errorf("生成审计日志 ID 失败: %w", err)
	}
	detail := entry.Detail
	if detail == nil {
		detail = map[string]any{}
	}
	if _, err := w.exec(ctx).Exec(ctx, sqlInsertAuditLog,
		id, entry.AdminID, entry.Action, entry.TargetType, entry.TargetID, detail, entry.At,
	); err != nil {
		return fmt.Errorf("写入审计日志失败: %w", err)
	}
	return nil
}
