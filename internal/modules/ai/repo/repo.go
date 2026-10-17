// Package repo 是 ai 模块唯一的 SQL 层：model_providers、ai_profiles、
// ai_model_settings、ai_behavior_settings、ai_memories、ai_memory_sources。
//
// 约定：所有写库时间（created_at/updated_at）由 app 用 clock.Now() 传入，
// SQL 不使用 now()/CURRENT_DATE/::date/interval；users 表由 auth 模块拥有，
// ai 只通过 shared/contracts 端口访问，不在本包查询。
package repo

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/modules/ai/domain"
	"github.com/great-magician-01/timeline/internal/platform/db"
)

// ErrNotFound 表示目标行不存在。repo 在边界把 pgx.ErrNoRows 归一化为它，
// 使 app/domain 不直接依赖 pgx。
var ErrNotFound = errors.New("记录不存在")

// normalizeNotFound 把 pgx.ErrNoRows 归一化为 ErrNotFound。
func normalizeNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// Store 持有连接池；方法通过 db.ExecutorFrom 自动复用 app 编排的事务。
type Store struct {
	pool *pgxpool.Pool
}

// New 构造 repo。
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// exec 返回 ctx 中当前事务的执行器；不在事务中时使用连接池。
func (s *Store) exec(ctx context.Context) db.Executor {
	return db.ExecutorFrom(ctx, s.pool)
}

// AIState 是 AI 列表所需的模块内配置状态；
// 账号资料（用户名/昵称/头像/停用）经 contracts.UserReader 读取后合并。
type AIState struct {
	AIID         int64
	IsEnabled    bool
	CreatedAt    *time.Time
	ProviderID   *int64
	ProviderName string
	Model        string
}

// TaskBrief 是 AI 详情页展示的近期任务摘要（任务表由 agent 模块拥有）。
type TaskBrief struct {
	ID          int64
	TriggerType string
	ActionType  string
	Status      string
	CreatedAt   time.Time
}

// MemoryRow 是 ai_memories 的完整行读模型（含 superseded_by_id）。
type MemoryRow = domain.MemoryRecord
