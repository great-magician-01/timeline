// Package access 集中实现跨模块复用的可见性、只读与互动授权判断。
// 所有判断都在后端执行（docs/PAGES_AND_PERMISSIONS.md §8），返回 *api.Failure
// 以便 Handler 统一映射为 404/403（不应暴露存在性的场景一律返回 not_found）。
package access

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/model"
)

// Querier 是连接池与事务的公共查询接口。
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// IsFollowing 判断 followerID 是否对 followeeID 存在生效关注关系。
func IsFollowing(ctx context.Context, q Querier, followerID, followeeID int64) (bool, error) {
	if followerID <= 0 || followeeID <= 0 {
		return false, nil
	}
	var exists bool
	err := q.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM follows
			WHERE follower_id = $1 AND followee_id = $2 AND is_active
		)`, followerID, followeeID).Scan(&exists)
	return exists, err
}

// FollowState 返回访问者对目标用户的关系状态：
// none / following / pending（我已申请）/ incoming_pending（对方申请了我）。
func FollowState(ctx context.Context, q Querier, viewerID, targetID int64) (string, error) {
	if viewerID <= 0 || targetID <= 0 || viewerID == targetID {
		return model.FollowStateNone, nil
	}
	var state string
	err := q.QueryRow(ctx, `
		SELECT CASE
			WHEN EXISTS (SELECT 1 FROM follows WHERE follower_id = $1 AND followee_id = $2 AND is_active)
				THEN 'following'
			WHEN EXISTS (SELECT 1 FROM follow_requests WHERE requester_id = $1 AND target_id = $2 AND status = 'pending')
				THEN 'pending'
			WHEN EXISTS (SELECT 1 FROM follow_requests WHERE requester_id = $2 AND target_id = $1 AND status = 'pending')
				THEN 'incoming_pending'
			ELSE 'none'
		END`, viewerID, targetID).Scan(&state)
	return state, err
}

// IsSuspended 读取账号是否处于停用状态。
func IsSuspended(ctx context.Context, q Querier, userID int64) (bool, error) {
	var status string
	err := q.QueryRow(ctx, `SELECT status FROM users WHERE id = $1`, userID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, api.ErrNotFound("用户不存在")
	}
	if err != nil {
		return false, err
	}
	return status != "active", nil
}

// UserBriefByID 读取作者摘要，用于 DTO 组装。
func UserBriefByID(ctx context.Context, q Querier, userID int64) (model.UserBrief, error) {
	var b model.UserBrief
	var status string
	err := q.QueryRow(ctx, `
		SELECT id, username, nickname, avatar_url, is_ai, status
		FROM users WHERE id = $1`, userID,
	).Scan(&b.ID, &b.Username, &b.Nickname, &b.AvatarURL, &b.IsAI, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, api.ErrNotFound("用户不存在")
	}
	if err != nil {
		return b, err
	}
	b.IsSuspended = status != "active"
	return b, nil
}

// UserBriefsByID 批量读取作者摘要，避免列表接口逐条查询。
func UserBriefsByID(ctx context.Context, q Querier, ids []int64) (map[int64]model.UserBrief, error) {
	result := make(map[int64]model.UserBrief, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := q.Query(ctx, `
		SELECT id, username, nickname, avatar_url, is_ai, status
		FROM users WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var b model.UserBrief
		var status string
		if err := rows.Scan(&b.ID, &b.Username, &b.Nickname, &b.AvatarURL, &b.IsAI, &status); err != nil {
			return nil, err
		}
		b.IsSuspended = status != "active"
		result[b.ID] = b
	}
	return result, rows.Err()
}

// FollowingSet 一次性载入访问者的全部生效关注对象，用于列表场景的批量权限过滤。
func FollowingSet(ctx context.Context, q Querier, followerID int64) (map[int64]bool, error) {
	set := map[int64]bool{}
	if followerID <= 0 {
		return set, nil
	}
	rows, err := q.Query(ctx, `
		SELECT followee_id FROM follows WHERE follower_id = $1 AND is_active`, followerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		set[id] = true
	}
	return set, rows.Err()
}

// CanViewPostWithFollowing 是不访问数据库的可见性判断，供列表与会话内批量过滤使用。
// following 为该访问者的生效关注集合（可空，仅影响 followers 可见性）。
func CanViewPostWithFollowing(viewer model.Viewer, post model.Post, following map[int64]bool) bool {
	switch post.Status {
	case model.PostStatusDraft, model.PostStatusScheduled:
		return viewer.IsAdmin || post.SelfPosted(viewer)
	case model.PostStatusDeleted:
		return viewer.IsAdmin
	case model.PostStatusPublished:
	default:
		return false
	}
	if viewer.IsAdmin {
		return true
	}
	switch post.Visibility {
	case model.VisibilityPublic:
		return true
	case model.VisibilityFollowers:
		if !viewer.Authenticated || viewer.IsAI {
			return false
		}
		if post.SelfPosted(viewer) {
			return true
		}
		return following[post.AuthorID]
	case model.VisibilityPrivate:
		return post.SelfPosted(viewer)
	default:
		return false
	}
}

// CanViewPost 判断访问者能否查看当前帖子正文。
// 草稿、定时未发布与已删除内容只有作者与管理员可见；AI 只能使用公开内容。
func CanViewPost(ctx context.Context, q Querier, viewer model.Viewer, post model.Post) (bool, error) {
	if post.Status == model.PostStatusPublished && post.Visibility == model.VisibilityPublic {
		return true, nil
	}
	if viewer.ID == 0 && !viewer.IsAdmin {
		return CanViewPostWithFollowing(viewer, post, nil), nil
	}
	following, err := FollowingSet(ctx, q, viewer.ID)
	if err != nil {
		return false, err
	}
	return CanViewPostWithFollowing(viewer, post, following), nil
}

// RequireVisiblePost 在不可见时返回 not_found，不暴露资源存在性。
func RequireVisiblePost(ctx context.Context, q Querier, viewer model.Viewer, post model.Post) error {
	visible, err := CanViewPost(ctx, q, viewer, post)
	if err != nil {
		return err
	}
	if !visible {
		return api.ErrNotFound("内容不存在或无权访问")
	}
	return nil
}

// RequireInteractivePost 在 RequireVisiblePost 基础上检查互动条件：
// 访问者必须已登录、未停用、非 AI 会话身份；作者账号停用或内容已删除时禁止新互动。
func RequireInteractivePost(ctx context.Context, q Querier, viewer model.Viewer, post model.Post) error {
	if !viewer.Authenticated {
		return api.ErrUnauthenticated("请先登录")
	}
	if viewer.IsSuspended {
		return api.ErrSuspended("账号已停用，不能执行此操作")
	}
	if viewer.IsAI {
		return api.ErrForbidden("AI 账号不能通过此接口操作")
	}
	if err := RequireVisiblePost(ctx, q, viewer, post); err != nil {
		return err
	}
	if post.Status != model.PostStatusPublished {
		return api.ErrForbidden("该内容当前不可互动")
	}
	suspended, err := IsSuspended(ctx, q, post.AuthorID)
	if err != nil {
		return err
	}
	if suspended && !post.SelfPosted(viewer) {
		return api.ErrForbidden("对方账号已停用，不能产生新的互动")
	}
	return nil
}

// RequireWritable 检查写操作的最低条件（登录、未停用、非 AI）。
func RequireWritable(viewer model.Viewer) error {
	if !viewer.Authenticated {
		return api.ErrUnauthenticated("请先登录")
	}
	if viewer.IsSuspended {
		return api.ErrSuspended("账号已停用，不能执行此操作")
	}
	if viewer.IsAI {
		return api.ErrForbidden("AI 账号不能通过此接口操作")
	}
	return nil
}

// RequireActorWritable 供 AI 任务内部执行动作时复用同一套账号状态校验。
func RequireActorWritable(ctx context.Context, q Querier, actorID int64) error {
	suspended, err := IsSuspended(ctx, q, actorID)
	if err != nil {
		return err
	}
	if suspended {
		return api.ErrSuspended("账号已停用，不能执行此操作")
	}
	return nil
}

// PoolQuerier 让 *pgxpool.Pool 显式满足 Querier（便于编译期检查）。
var _ Querier = (*pgxpool.Pool)(nil)

// TxQuerier 让 pgx.Tx 显式满足 Querier。
var _ Querier = (pgx.Tx)(nil)
