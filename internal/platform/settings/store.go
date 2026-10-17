// Package settings 读取与写入运行期系统设置（system_settings）。
//
// 设置项由管理后台维护，读取方在每次请求/任务执行时加载，保证修改后即时生效。
// 数据库缺失的键使用代码内默认值，避免升级后必须先执行数据回填。
//
// 时间约定：写入时间 updated_at 由调用方应用时钟计算后显式传入，SQL 不依赖
// 数据库 now()；读取只返回快照，不做时间比较。
package settings

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/platform/db"
)

// allKeys 是 Load 读取的全部已知设置键。
var allKeys = []string{
	KeyRegistrationEnabled, KeyAIGlobalEnabled,
	KeyUploadMaxFileBytes, KeyUploadMaxRequestBytes, KeyStorageQuotaBytes,
	KeyPostRateLimit, KeyPostRateWindowSeconds,
	KeyUploadRateLimit, KeyUploadRateWindowSeconds,
	KeyTempAttachmentRetentionHour, KeyCallLogMaxBytes, KeyCallLogRetentionDays,
}

// Load 读取全部已知设置；缺失或格式非法的键回退默认值。
// 数据库错误原样返回，调用方可选择使用 Defaults 继续运行。
func Load(ctx context.Context, q db.Executor) (Values, error) {
	values := Defaults()
	rows, err := q.Query(ctx, sqlSelectSettings, allKeys)
	if err != nil {
		return values, err
	}
	defer rows.Close()

	for rows.Next() {
		var key string
		var raw []byte
		if err := rows.Scan(&key, &raw); err != nil {
			return values, err
		}
		apply(&values, key, raw)
	}
	if err := rows.Err(); err != nil {
		return values, err
	}
	return values, nil
}

// LoadOrDefaults 读取设置，数据库错误时返回默认值（由调用方决定是否记录日志）。
func LoadOrDefaults(ctx context.Context, q db.Executor) Values {
	values, err := Load(ctx, q)
	if err != nil {
		return Defaults()
	}
	return values
}

// Set 在调用方事务内写入一个设置值，并记录更新管理员与应用时间 at。
func Set(ctx context.Context, q db.Executor, key string, value any, adminID int64, at time.Time) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, sqlUpsertSetting, key, string(encoded), adminID, at)
	return err
}

// Store 是面向组合根的设置存储：持有连接池，读写时自动使用 ctx 中的事务
// （db.WithTx 内调用）或连接池。
//
// 保留包级 Load/Set 函数是为了兼容旧调用方与单元测试注入执行器。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 构造设置存储。
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Load 读取全部已知设置。
func (s *Store) Load(ctx context.Context) (Values, error) {
	return Load(ctx, db.ExecutorFrom(ctx, s.pool))
}

// LoadOrDefaults 读取设置，数据库错误时返回默认值。
func (s *Store) LoadOrDefaults(ctx context.Context) Values {
	return LoadOrDefaults(ctx, db.ExecutorFrom(ctx, s.pool))
}

// LoadWebSearch 读取全局网页搜索设置。
func (s *Store) LoadWebSearch(ctx context.Context) (WebSearchValues, error) {
	return LoadWebSearch(ctx, db.ExecutorFrom(ctx, s.pool))
}

// Set 写入一个设置值；调用方负责开启事务并传入同一应用时间。
func (s *Store) Set(ctx context.Context, key string, value any, adminID int64, at time.Time) error {
	return Set(ctx, db.ExecutorFrom(ctx, s.pool), key, value, adminID, at)
}
