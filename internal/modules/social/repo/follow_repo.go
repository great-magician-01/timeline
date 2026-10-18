package repo

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/modules/social/domain"
	"github.com/great-magician-01/timeline/internal/platform/db"
)

// FollowRepo 实现 app.FollowRepo。
type FollowRepo struct {
	pool *pgxpool.Pool
}

// NewFollowRepo 构造关注关系仓储；pool 由组合根注入。
func NewFollowRepo(pool *pgxpool.Pool) *FollowRepo { return &FollowRepo{pool: pool} }

const (
	sqlFollowing = `SELECT EXISTS (
		SELECT 1 FROM follows WHERE follower_id = $1 AND followee_id = $2 AND is_active
	)`

	// 单条三 EXISTS 合并读取关系状态：三项均为等值条件，分别命中
	// follows_pair_unique 与 follow_requests_pending_unique 部分唯一索引，
	// 把「关注状态」从 3 次往返降为 1 次（帖子详情/个人主页主链路）。
	sqlFollowState = `SELECT
		EXISTS (SELECT 1 FROM follows
			WHERE follower_id = $1 AND followee_id = $2 AND is_active),
		EXISTS (SELECT 1 FROM follow_requests
			WHERE requester_id = $1 AND target_id = $2 AND status = 'pending'),
		EXISTS (SELECT 1 FROM follow_requests
			WHERE requester_id = $2 AND target_id = $1 AND status = 'pending')`

	sqlFollowingSet = `SELECT followee_id FROM follows WHERE follower_id = $1 AND is_active`

	// 只有状态真的从失效翻转为生效时才返回行：唯一索引兜底并发幂等。
	sqlFollowInsertActive = `INSERT INTO follows (id, follower_id, followee_id, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, TRUE, $4, $4)
		ON CONFLICT (follower_id, followee_id)
		DO UPDATE SET is_active = TRUE, updated_at = $4
		WHERE follows.is_active = FALSE
		RETURNING id, updated_at`

	sqlFollowDeactivate = `UPDATE follows SET is_active = FALSE, updated_at = $3
		WHERE follower_id = $1 AND followee_id = $2 AND is_active`

	sqlListFollowersFirstPage = `SELECT f.id, f.follower_id FROM follows f
		WHERE f.followee_id = $1 AND f.is_active
		ORDER BY f.id DESC LIMIT $2`
	sqlListFollowersAfterCursor = `SELECT f.id, f.follower_id FROM follows f
		WHERE f.followee_id = $1 AND f.is_active AND f.id < $2
		ORDER BY f.id DESC LIMIT $3`

	sqlListFollowingFirstPage = `SELECT f.id, f.followee_id FROM follows f
		WHERE f.follower_id = $1 AND f.is_active
		ORDER BY f.id DESC LIMIT $2`
	sqlListFollowingAfterCursor = `SELECT f.id, f.followee_id FROM follows f
		WHERE f.follower_id = $1 AND f.is_active AND f.id < $2
		ORDER BY f.id DESC LIMIT $3`

	sqlListRecentFollowees = `SELECT f.id, f.followee_id FROM follows f
		WHERE f.follower_id = $1 AND f.is_active
		ORDER BY f.id DESC LIMIT $2`
)

// Following 报告 follower 是否已生效关注 followee。
func (r *FollowRepo) Following(ctx context.Context, followerID, followeeID int64) (bool, error) {
	var active bool
	err := db.ExecutorFrom(ctx, r.pool).QueryRow(ctx, sqlFollowing, followerID, followeeID).Scan(&active)
	return active, err
}

// FollowState 用单条三 EXISTS 查询返回访问者对目标的三项关系状态。
func (r *FollowRepo) FollowState(
	ctx context.Context, viewerID, targetID int64,
) (following, pending, incomingPending bool, err error) {
	err = db.ExecutorFrom(ctx, r.pool).QueryRow(ctx, sqlFollowState, viewerID, targetID).
		Scan(&following, &pending, &incomingPending)
	return following, pending, incomingPending, err
}

// FollowingSet 返回 follower 的生效关注集合；游客（ID<=0）返回空集。
func (r *FollowRepo) FollowingSet(ctx context.Context, followerID int64) (map[int64]bool, error) {
	result := map[int64]bool{}
	if followerID <= 0 {
		return result, nil
	}
	rows, err := db.ExecutorFrom(ctx, r.pool).Query(ctx, sqlFollowingSet, followerID)
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

// InsertActive 建立或恢复关注关系；只有从失效翻转为生效时 ok 为 true。
func (r *FollowRepo) InsertActive(
	ctx context.Context, id, followerID, followeeID int64, now time.Time,
) (rowID int64, activeAt time.Time, ok bool, err error) {
	err = db.ExecutorFrom(ctx, r.pool).QueryRow(ctx, sqlFollowInsertActive, id, followerID, followeeID, now).
		Scan(&rowID, &activeAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, time.Time{}, false, nil
	}
	if err != nil {
		return 0, time.Time{}, false, err
	}
	return rowID, activeAt, true, nil
}

// Deactivate 取关；关系原本不生效时 ok 为 false。
func (r *FollowRepo) Deactivate(ctx context.Context, followerID, followeeID int64, now time.Time) (bool, error) {
	tag, err := db.ExecutorFrom(ctx, r.pool).Exec(ctx, sqlFollowDeactivate, followerID, followeeID, now)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ListFollowers 列出目标的生效粉丝（游标按关注行 ID 倒序）。
func (r *FollowRepo) ListFollowers(ctx context.Context, followeeID, cursor int64, limit int) ([]domain.FollowRow, error) {
	exec := db.ExecutorFrom(ctx, r.pool)
	var rows pgx.Rows
	var err error
	if cursor > 0 {
		rows, err = exec.Query(ctx, sqlListFollowersAfterCursor, followeeID, cursor, limit)
	} else {
		rows, err = exec.Query(ctx, sqlListFollowersFirstPage, followeeID, limit)
	}
	if err != nil {
		return nil, err
	}
	return scanFollowRows(rows, limit)
}

// ListFollowing 列出目标的生效关注（游标按关注行 ID 倒序）。
func (r *FollowRepo) ListFollowing(ctx context.Context, followerID, cursor int64, limit int) ([]domain.FollowRow, error) {
	exec := db.ExecutorFrom(ctx, r.pool)
	var rows pgx.Rows
	var err error
	if cursor > 0 {
		rows, err = exec.Query(ctx, sqlListFollowingAfterCursor, followerID, cursor, limit)
	} else {
		rows, err = exec.Query(ctx, sqlListFollowingFirstPage, followerID, limit)
	}
	if err != nil {
		return nil, err
	}
	return scanFollowRows(rows, limit)
}

// ListRecentFollowees 读取用户最近关注的账号（按关注行 ID 倒序）。
func (r *FollowRepo) ListRecentFollowees(ctx context.Context, followerID int64, limit int) ([]domain.FollowRow, error) {
	rows, err := db.ExecutorFrom(ctx, r.pool).Query(ctx, sqlListRecentFollowees, followerID, limit)
	if err != nil {
		return nil, err
	}
	return scanFollowRows(rows, limit)
}

func scanFollowRows(rows pgx.Rows, limit int) ([]domain.FollowRow, error) {
	defer rows.Close()
	items := make([]domain.FollowRow, 0, limit)
	for rows.Next() {
		var row domain.FollowRow
		if err := rows.Scan(&row.RowID, &row.UserID); err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, rows.Err()
}
