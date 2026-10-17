package repo

import (
	"context"
	"strconv"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/ai/domain"
)

// providerColumns 是 model_providers 的行列清单。
const providerColumns = `id, name, base_url, api_key_cipher, default_model, is_enabled, created_at, updated_at`

// ListProvidersFilter 是预设列表过滤条件。
type ListProvidersFilter struct {
	Query  string
	Status string // "" / "enabled" / "disabled"
	Cursor int64
	Limit  int
}

// ListProviders 返回未删除的预设（按 ID 倒序；调用方多取一条判断 has_more）。
func (s *Store) ListProviders(ctx context.Context, f ListProvidersFilter) ([]domain.Provider, error) {
	where := []string{"NOT is_deleted"}
	args := make([]any, 0, 4)
	addArg := func(value any) string {
		args = append(args, value)
		return "$" + strconv.Itoa(len(args))
	}
	if f.Query != "" {
		placeholder := addArg(f.Query)
		where = append(where, "STRPOS(LOWER(name), LOWER("+placeholder+")) > 0")
	}
	switch f.Status {
	case "enabled":
		where = append(where, "is_enabled")
	case "disabled":
		where = append(where, "NOT is_enabled")
	}
	if f.Cursor > 0 {
		where = append(where, "id < "+addArg(f.Cursor))
	}
	args = append(args, f.Limit)
	query := `SELECT ` + providerColumns + ` FROM model_providers WHERE `
	query += joinAnd(where) + ` ORDER BY id DESC LIMIT $` + strconv.Itoa(len(args))
	rows, err := s.exec(ctx).Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([]domain.Provider, 0, f.Limit)
	for rows.Next() {
		var record domain.Provider
		if err := rows.Scan(&record.ID, &record.Name, &record.BaseURL, &record.APIKeyCipher,
			&record.DefaultModel, &record.IsEnabled, &record.CreatedAt, &record.UpdatedAt); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

// GetProvider 读取未删除的预设；不存在时返回 pgx.ErrNoRows。
func (s *Store) GetProvider(ctx context.Context, providerID int64) (domain.Provider, error) {
	var record domain.Provider
	err := s.exec(ctx).QueryRow(ctx,
		`SELECT `+providerColumns+` FROM model_providers WHERE id = $1 AND NOT is_deleted`,
		providerID,
	).Scan(&record.ID, &record.Name, &record.BaseURL, &record.APIKeyCipher,
		&record.DefaultModel, &record.IsEnabled, &record.CreatedAt, &record.UpdatedAt)
	return record, normalizeNotFound(err)
}

// CreateProviderInput 是创建预设的写入参数。
type CreateProviderInput struct {
	ID           int64
	Name         string
	BaseURL      string
	APIKeyCipher string
	DefaultModel string
	At           time.Time
}

// CreateProvider 创建预设（显式写入应用时间）。
func (s *Store) CreateProvider(ctx context.Context, in CreateProviderInput) error {
	_, err := s.exec(ctx).Exec(ctx, `
		INSERT INTO model_providers (id, name, base_url, api_key_cipher, default_model, is_enabled, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, TRUE, $6, $6)`,
		in.ID, in.Name, in.BaseURL, in.APIKeyCipher, in.DefaultModel, in.At)
	return err
}

// UpdateProviderInput 是更新预设的写入参数。
type UpdateProviderInput struct {
	ID           int64
	Name         string
	BaseURL      string
	APIKeyCipher string
	DefaultModel string
	IsEnabled    bool
	At           time.Time
}

// UpdateProvider 更新预设字段与 updated_at。
func (s *Store) UpdateProvider(ctx context.Context, in UpdateProviderInput) error {
	_, err := s.exec(ctx).Exec(ctx, `
		UPDATE model_providers
		SET name = $2, base_url = $3, api_key_cipher = $4, default_model = $5,
		    is_enabled = $6, updated_at = $7
		WHERE id = $1 AND NOT is_deleted`,
		in.ID, in.Name, in.BaseURL, in.APIKeyCipher, in.DefaultModel, in.IsEnabled, in.At)
	return err
}

// SoftDeleteProvider 逻辑删除预设（同时停用）。
func (s *Store) SoftDeleteProvider(ctx context.Context, providerID int64, at time.Time) error {
	_, err := s.exec(ctx).Exec(ctx, `
		UPDATE model_providers SET is_deleted = TRUE, is_enabled = FALSE, updated_at = $2
		WHERE id = $1 AND NOT is_deleted`, providerID, at)
	return err
}

// ProviderNameTaken 检查预设名称（忽略大小写）是否已被其他未删除预设占用。
func (s *Store) ProviderNameTaken(ctx context.Context, name string, excludeID int64) (bool, error) {
	var exists bool
	err := s.exec(ctx).QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM model_providers
			WHERE LOWER(name) = LOWER($1) AND NOT is_deleted AND id <> $2
		)`, name, excludeID).Scan(&exists)
	return exists, err
}

// ProviderAICounts 批量统计每个预设关联的 AI 数量（ai_model_settings 归本模块所有）。
func (s *Store) ProviderAICounts(ctx context.Context, providerIDs []int64) (map[int64]int, error) {
	counts := make(map[int64]int, len(providerIDs))
	if len(providerIDs) == 0 {
		return counts, nil
	}
	rows, err := s.exec(ctx).Query(ctx, `
		SELECT provider_id, COUNT(*)
		FROM ai_model_settings
		WHERE provider_id = ANY($1)
		GROUP BY provider_id`, providerIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var providerID int64
		var count int
		if err := rows.Scan(&providerID, &count); err != nil {
			return nil, err
		}
		counts[providerID] = count
	}
	return counts, rows.Err()
}

// EnabledProviderExists 在事务内校验引用完整性（无外键，只能应用层校验）。
func (s *Store) EnabledProviderExists(ctx context.Context, providerID int64) (bool, error) {
	var exists bool
	err := s.exec(ctx).QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM model_providers
			WHERE id = $1 AND NOT is_deleted AND is_enabled
		)`, providerID).Scan(&exists)
	return exists, err
}

// joinAnd 用 AND 连接 SQL 片段。
func joinAnd(parts []string) string {
	result := ""
	for index, part := range parts {
		if index > 0 {
			result += " AND "
		}
		result += part
	}
	return result
}
