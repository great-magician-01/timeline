package repo

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/modules/notice/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/db"
)

// MentionRepo 实现 app.MentionRepo。
type MentionRepo struct {
	pool *pgxpool.Pool
}

// NewMentionRepo 构造提及仓储。
func NewMentionRepo(pool *pgxpool.Pool) *MentionRepo { return &MentionRepo{pool: pool} }

const (
	// 停用来源对象上不在保留集合中的提及；空数组表示全部停用。
	sqlMentionDeactivateExcept = `UPDATE mentions SET is_active = FALSE
		WHERE source_type = $1 AND source_id = $2 AND is_active
		  AND NOT (mentioned_user_id = ANY($3::bigint[]))`

	sqlMentionUpsert = `INSERT INTO mentions (id, source_type, source_id, mentioned_user_id, is_active, created_at)
		VALUES ($1, $2, $3, $4, TRUE, $5)
		ON CONFLICT (source_type, source_id, mentioned_user_id)
		DO UPDATE SET is_active = TRUE`

	sqlMentionDeactivateAll = `UPDATE mentions SET is_active = FALSE
		WHERE source_type = $1 AND source_id = $2 AND is_active`

	// 按 (created_at, id) 倒序：命中 mentions_user_created 部分索引
	// (mentioned_user_id, created_at DESC, id DESC) WHERE is_active，
	// 时间窗过滤可用索引范围扫描，不再按 id 倒序扫用户全部历史提及。
	sqlMentionListActive = `SELECT id, mentioned_user_id, source_type, source_id, created_at
		FROM mentions
		WHERE mentioned_user_id = $1 AND is_active AND created_at > $2
		ORDER BY created_at DESC, id DESC LIMIT $3`

	sqlMentionGetActive = `SELECT id, mentioned_user_id, source_type, source_id, created_at
		FROM mentions WHERE id = $1 AND is_active`
)

// DeactivateExcept 停用来源对象上不在 keepUserIDs 中的提及。
func (r *MentionRepo) DeactivateExcept(
	ctx context.Context, sourceType string, sourceID int64, keepUserIDs []int64,
) error {
	if keepUserIDs == nil {
		keepUserIDs = []int64{}
	}
	_, err := db.ExecutorFrom(ctx, r.pool).Exec(ctx, sqlMentionDeactivateExcept, sourceType, sourceID, keepUserIDs)
	return err
}

// Upsert 建链或恢复提及记录。
func (r *MentionRepo) Upsert(
	ctx context.Context, id int64, sourceType string, sourceID, userID int64, now time.Time,
) error {
	_, err := db.ExecutorFrom(ctx, r.pool).Exec(ctx, sqlMentionUpsert, id, sourceType, sourceID, userID, now)
	return err
}

// DeactivateAll 停用来源对象上的全部提及。
func (r *MentionRepo) DeactivateAll(ctx context.Context, sourceType string, sourceID int64) error {
	_, err := db.ExecutorFrom(ctx, r.pool).Exec(ctx, sqlMentionDeactivateAll, sourceType, sourceID)
	return err
}

// ListActive 读取某用户 since 之后生效中的提及（(created_at, id) 倒序，命中时间窗部分索引）。
func (r *MentionRepo) ListActive(
	ctx context.Context, userID int64, since time.Time, limit int,
) ([]domain.MentionRow, error) {
	rows, err := db.ExecutorFrom(ctx, r.pool).Query(ctx, sqlMentionListActive, userID, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]domain.MentionRow, 0, limit)
	for rows.Next() {
		var row domain.MentionRow
		if err := rows.Scan(&row.ID, &row.MentionedUserID, &row.SourceType, &row.SourceID, &row.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, rows.Err()
}

// GetActive 读取单条生效提及；不存在返回领域 not_found。
func (r *MentionRepo) GetActive(ctx context.Context, mentionID int64) (domain.MentionRow, error) {
	var row domain.MentionRow
	err := db.ExecutorFrom(ctx, r.pool).QueryRow(ctx, sqlMentionGetActive, mentionID).
		Scan(&row.ID, &row.MentionedUserID, &row.SourceType, &row.SourceID, &row.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MentionRow{}, apperr.NotFound("提及不存在")
	}
	return row, err
}
