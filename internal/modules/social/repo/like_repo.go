package repo

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/platform/db"
)

// LikeRepo 实现 app.LikeRepo。
type LikeRepo struct {
	pool *pgxpool.Pool
}

// NewLikeRepo 构造点赞仓储。
func NewLikeRepo(pool *pgxpool.Pool) *LikeRepo { return &LikeRepo{pool: pool} }

const (
	// 唯一索引兜底幂等：只有从失效翻转为生效时才返回行。
	sqlLikeActivate = `INSERT INTO likes (id, user_id, target_type, target_id, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, TRUE, $5, $5)
		ON CONFLICT (user_id, target_type, target_id)
		DO UPDATE SET is_active = TRUE, updated_at = $5
		WHERE likes.is_active = FALSE
		RETURNING id`

	sqlLikeDeactivate = `UPDATE likes SET is_active = FALSE, updated_at = $4
		WHERE user_id = $1 AND target_type = $2 AND target_id = $3 AND is_active
		RETURNING id`

	sqlLikeActiveStates = `SELECT target_id FROM likes
		WHERE user_id = $1 AND target_type = $2 AND target_id = ANY($3) AND is_active`

	sqlLikeIsActive = `SELECT EXISTS (
		SELECT 1 FROM likes WHERE user_id = $1 AND target_type = $2 AND target_id = $3 AND is_active
	)`

	sqlLikeLikedPostIDs = `SELECT target_id FROM likes
		WHERE user_id = $1 AND target_type = 'post' AND is_active
		ORDER BY id DESC LIMIT $2`
)

// IsActive 报告用户是否已点赞目标。
func (r *LikeRepo) IsActive(ctx context.Context, userID int64, targetType string, targetID int64) (bool, error) {
	var active bool
	err := db.ExecutorFrom(ctx, r.pool).
		QueryRow(ctx, sqlLikeIsActive, userID, targetType, targetID).Scan(&active)
	return active, err
}

// LikedPostIDs 返回用户最近点赞的帖子 ID（按点赞行 ID 倒序）。
func (r *LikeRepo) LikedPostIDs(ctx context.Context, userID int64, limit int) ([]int64, error) {
	rows, err := db.ExecutorFrom(ctx, r.pool).Query(ctx, sqlLikeLikedPostIDs, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]int64, 0, limit)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Activate 点赞；只有从失效翻转为生效时 changed 为 true。
func (r *LikeRepo) Activate(
	ctx context.Context, id, userID int64, targetType string, targetID int64, now time.Time,
) (likeID int64, changed bool, err error) {
	err = db.ExecutorFrom(ctx, r.pool).
		QueryRow(ctx, sqlLikeActivate, id, userID, targetType, targetID, now).
		Scan(&likeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return likeID, true, nil
}

// Deactivate 取消点赞；changed 表示本次是否真正翻转。
func (r *LikeRepo) Deactivate(
	ctx context.Context, userID int64, targetType string, targetID int64, now time.Time,
) (likeID int64, changed bool, err error) {
	err = db.ExecutorFrom(ctx, r.pool).
		QueryRow(ctx, sqlLikeDeactivate, userID, targetType, targetID, now).
		Scan(&likeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return likeID, true, nil
}

// ActiveStates 批量查询访问者已点赞的目标集合。
func (r *LikeRepo) ActiveStates(
	ctx context.Context, userID int64, targetType string, targetIDs []int64,
) (map[int64]bool, error) {
	result := map[int64]bool{}
	if userID <= 0 || len(targetIDs) == 0 {
		return result, nil
	}
	rows, err := db.ExecutorFrom(ctx, r.pool).Query(ctx, sqlLikeActiveStates, userID, targetType, targetIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result[id] = true
	}
	return result, rows.Err()
}
