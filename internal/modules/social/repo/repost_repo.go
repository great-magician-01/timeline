package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/modules/social/domain"
	"github.com/great-magician-01/timeline/internal/platform/db"
)

// RepostRepo 实现 app.RepostRepo。
type RepostRepo struct {
	pool *pgxpool.Pool
}

// NewRepostRepo 构造纯转发仓储。
func NewRepostRepo(pool *pgxpool.Pool) *RepostRepo { return &RepostRepo{pool: pool} }

const (
	// 唯一索引兜底幂等：只有从失效翻转为生效时才返回行。
	// author_is_ai 是转发者账号类型的快照（账号 is_ai 创建后不可变），
	// 供时间线按 human/ai 过滤时直接命中 reposts_author_type_timeline 索引。
	sqlRepostActivate = `INSERT INTO reposts (id, user_id, post_id, author_is_ai, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, TRUE, $5, $5)
		ON CONFLICT (user_id, post_id)
		DO UPDATE SET is_active = TRUE, author_is_ai = $4, updated_at = $5
		WHERE reposts.is_active = FALSE
		RETURNING id`

	sqlRepostDeactivate = `UPDATE reposts SET is_active = FALSE, updated_at = $3
		WHERE user_id = $1 AND post_id = $2 AND is_active
		RETURNING id`

	sqlRepostIsActive = `SELECT EXISTS (
		SELECT 1 FROM reposts WHERE user_id = $1 AND post_id = $2 AND is_active
	)`

	sqlRepostActiveStates = `SELECT post_id FROM reposts
		WHERE user_id = $1 AND post_id = ANY($2) AND is_active`

	sqlRepostListEventsFirstPage = `SELECT id, user_id, post_id, created_at FROM reposts
		WHERE is_active%s
		ORDER BY created_at DESC, id DESC LIMIT $%d`
	sqlRepostListEventsAfterCursor = `SELECT id, user_id, post_id, created_at FROM reposts
		WHERE is_active%s AND (created_at, id) < ($%d, $%d)
		ORDER BY created_at DESC, id DESC LIMIT $%d`
)

// Activate 转发；只有从失效翻转为生效时 changed 为 true。
func (r *RepostRepo) Activate(
	ctx context.Context, id, userID, postID int64, authorIsAI bool, now time.Time,
) (repostID int64, changed bool, err error) {
	err = db.ExecutorFrom(ctx, r.pool).QueryRow(ctx, sqlRepostActivate, id, userID, postID, authorIsAI, now).Scan(&repostID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return repostID, true, nil
}

// Deactivate 取消转发；changed 表示本次是否真正翻转。
func (r *RepostRepo) Deactivate(
	ctx context.Context, userID, postID int64, now time.Time,
) (repostID int64, changed bool, err error) {
	err = db.ExecutorFrom(ctx, r.pool).QueryRow(ctx, sqlRepostDeactivate, userID, postID, now).Scan(&repostID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return repostID, true, nil
}

// IsActive 报告用户是否已转发帖子。
func (r *RepostRepo) IsActive(ctx context.Context, userID, postID int64) (bool, error) {
	var active bool
	err := db.ExecutorFrom(ctx, r.pool).QueryRow(ctx, sqlRepostIsActive, userID, postID).Scan(&active)
	return active, err
}

// ActiveStates 批量查询用户已转发的帖子集合。
func (r *RepostRepo) ActiveStates(ctx context.Context, userID int64, postIDs []int64) (map[int64]bool, error) {
	result := map[int64]bool{}
	if userID <= 0 || len(postIDs) == 0 {
		return result, nil
	}
	rows, err := db.ExecutorFrom(ctx, r.pool).Query(ctx, sqlRepostActiveStates, userID, postIDs)
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

// ListEvents 读取生效中的纯转发事件（时间+ID 倒序）。
func (r *RepostRepo) ListEvents(ctx context.Context, query domain.RepostEventQuery) ([]domain.RepostEvent, error) {
	args := make([]any, 0, 6)
	conditions := ""
	if query.AuthorID > 0 {
		args = append(args, query.AuthorID)
		conditions += fmt.Sprintf(" AND user_id = $%d", len(args))
	}
	if len(query.FollowingIDs) > 0 {
		args = append(args, query.FollowingIDs)
		conditions += fmt.Sprintf(" AND user_id = ANY($%d)", len(args))
	}
	if query.AuthorIsAI != nil {
		args = append(args, *query.AuthorIsAI)
		conditions += fmt.Sprintf(" AND author_is_ai = $%d", len(args))
	}
	exec := db.ExecutorFrom(ctx, r.pool)
	var (
		rows pgx.Rows
		err  error
	)
	if query.AfterID > 0 && !query.AfterAt.IsZero() {
		args = append(args, query.AfterAt, query.AfterID)
		atPos, idPos := len(args)-1, len(args)
		args = append(args, query.Limit)
		sql := fmt.Sprintf(sqlRepostListEventsAfterCursor, conditions, atPos, idPos, len(args))
		rows, err = exec.Query(ctx, sql, args...)
	} else {
		args = append(args, query.Limit)
		sql := fmt.Sprintf(sqlRepostListEventsFirstPage, conditions, len(args))
		rows, err = exec.Query(ctx, sql, args...)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]domain.RepostEvent, 0, query.Limit)
	for rows.Next() {
		var event domain.RepostEvent
		if err := rows.Scan(&event.ID, &event.UserID, &event.PostID, &event.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}
