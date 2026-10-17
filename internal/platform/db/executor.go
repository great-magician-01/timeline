package db

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Executor 是 repo 层唯一依赖的数据库执行原语；连接池与事务都满足它。
//
// repo 方法不接收 *pgxpool.Pool 或 pgx.Tx，而是通过 ExecutorFrom(ctx, pool)
// 自动使用 app 编排的当前事务或连接池，从而让跨模块协作复用同一事务。
type Executor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// PoolProvider 提供运行期连接池；db.Readiness 与 platform/jobs.Runner 等共享同一对象。
// 连接池未就绪（数据库尚未连接）时返回 nil，由 ready 中间件拒绝业务请求。
type PoolProvider interface {
	Pool() *pgxpool.Pool
}

// 编译期断言：连接池、事务、单连接都显式满足 Executor，repo 可无差别使用。
var (
	_ Executor = (*pgxpool.Pool)(nil)
	_ Executor = (pgx.Tx)(nil)
	_ Executor = (*pgx.Conn)(nil)
)
