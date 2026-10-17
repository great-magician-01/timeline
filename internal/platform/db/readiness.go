package db

import (
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Readiness 保存运行期就绪状态（连接池是否就绪、迁移是否完成），
// 供 /readyz 与 ready 中间件读取；零值可用。
//
// 典型流程：后台 goroutine 连接成功后 SetPool；迁移成功后 MarkMigrationsDone；
// 迁移失败时 SetMigrationError 记录原因并保持未就绪。
type Readiness struct {
	mu             sync.RWMutex
	pool           *pgxpool.Pool
	migrationsDone bool
	migrationErr   error
}

// 编译期断言：Readiness 可作为连接池提供者注入服务与作业运行器。
var _ PoolProvider = (*Readiness)(nil)

// NewReadiness 创建空的就绪状态（未连接、迁移未完成）。
func NewReadiness() *Readiness { return &Readiness{} }

// SetPool 设置已连接成功的连接池；数据库未就绪时传 nil。
func (r *Readiness) SetPool(p *pgxpool.Pool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pool = p
}

// Pool 返回当前连接池；数据库尚未连接时返回 nil。
func (r *Readiness) Pool() *pgxpool.Pool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.pool
}

// MarkMigrationsDone 标记迁移完成并清除此前的迁移错误；此时服务方可对外就绪。
func (r *Readiness) MarkMigrationsDone() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.migrationsDone = true
	r.migrationErr = nil
}

// MigrationsDone 报告迁移是否已完成。
func (r *Readiness) MigrationsDone() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.migrationsDone
}

// SetMigrationError 记录迁移失败原因，并把就绪状态置为迁移未完成；
// 传 nil 仅清除错误记录，不改变已完成的迁移标记。
func (r *Readiness) SetMigrationError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.migrationErr = err
	if err != nil {
		r.migrationsDone = false
	}
}

// MigrationError 返回最近一次迁移失败原因；无错误时返回 nil。
func (r *Readiness) MigrationError() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.migrationErr
}

// Ready 报告服务是否就绪：连接池已设置且迁移已完成。
// 该判断只读状态，不探测数据库；/readyz 可额外调用 Ping 验证连接。
func (r *Readiness) Ready() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.pool != nil && r.migrationsDone
}
