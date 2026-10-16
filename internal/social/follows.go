package social

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/great-magician-01/timeline/internal/access"
	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/model"
	"github.com/great-magician-01/timeline/internal/notice"
)

// Follow 关注目标用户：对方未开启审批（或目标是 AI）时直接生效，
// 开启审批时创建待审批申请（docs/WORKFLOWS.md 4.2）。
func (s *Service) Follow(c *gin.Context) {
	ctx := c.Request.Context()
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	viewer, ok := s.requireViewer(c)
	if !ok {
		return
	}
	if err := access.RequireWritable(viewer); err != nil {
		s.fail(c, "检查关注权限失败", err)
		return
	}
	if !s.allowAction(c, s.followLimiter, viewer.ID) {
		return
	}
	target, err := resolveUsername(ctx, pool, c.Param("username"))
	if err != nil {
		s.fail(c, "读取关注目标失败", err)
		return
	}
	if target.ID == viewer.ID {
		api.Error(c, http.StatusBadRequest, "validation_failed", "不能关注自己")
		return
	}

	tx, ok := s.beginTx(c, pool)
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	state, err := s.FollowForActor(ctx, tx, viewer.ID, target.ID)
	if err != nil {
		s.fail(c, "关注失败", err)
		return
	}
	if !s.commit(c, tx) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"follow_state": state})
}

// FollowForActor 是关注的核心实现，供 AI 动作执行与管理后台复用：
// 目标不存在返回 404，目标停用返回 403，重复关注幂等。
func (s *Service) FollowForActor(ctx context.Context, q access.Querier, actorID, targetID int64) (string, error) {
	if actorID <= 0 || targetID <= 0 {
		return "", api.ErrValidation("关注对象无效")
	}
	if actorID == targetID {
		return "", api.ErrValidation("不能关注自己")
	}
	if err := access.RequireActorWritable(ctx, q, actorID); err != nil {
		return "", err
	}

	var (
		requireApproval bool
		targetIsAI      bool
		targetStatus    string
	)
	err := q.QueryRow(ctx, `
		SELECT require_follow_approval, is_ai, status
		FROM users WHERE id = $1`, targetID,
	).Scan(&requireApproval, &targetIsAI, &targetStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", api.ErrNotFound("用户不存在")
	}
	if err != nil {
		return "", err
	}
	if targetStatus != "active" {
		return "", api.ErrForbidden("对方账号已停用，不能关注")
	}

	// 已有生效关系时直接返回：审批开关的变化不改变既有关系（docs/WORKFLOWS.md 4.2）。
	active, err := access.IsFollowing(ctx, q, actorID, targetID)
	if err != nil {
		return "", err
	}
	if active {
		return model.FollowStateFollowing, nil
	}

	if !requireApproval || targetIsAI {
		id, err := s.ids.Next()
		if err != nil {
			return "", err
		}
		var (
			followsID int64
			activeAt  time.Time
		)
		// 只有状态真的从失效翻转为生效时才需要通知；重复关注不重复通知。
		err = q.QueryRow(ctx, `
			INSERT INTO follows (id, follower_id, followee_id, is_active, created_at, updated_at)
			VALUES ($1, $2, $3, TRUE, now(), now())
			ON CONFLICT (follower_id, followee_id)
			DO UPDATE SET is_active = TRUE, updated_at = now()
			WHERE follows.is_active = FALSE
			RETURNING id, updated_at`, id, actorID, targetID,
		).Scan(&followsID, &activeAt)
		if errors.Is(err, pgx.ErrNoRows) {
			// 并发请求已建立同样的关系，按幂等处理。
			return model.FollowStateFollowing, nil
		}
		if err != nil {
			return "", err
		}
		if err := notice.Create(ctx, q, s.ids, notice.Params{
			IdempotencyKey: followNotifyKey(followsID, activeAt),
			RecipientID:    targetID,
			ActorID:        actorID,
			Type:           notice.TypeFollow,
			TargetType:     "user",
			TargetID:       actorID,
		}); err != nil {
			return "", err
		}
		return model.FollowStateFollowing, nil
	}

	id, err := s.ids.Next()
	if err != nil {
		return "", err
	}
	var requestID int64
	err = q.QueryRow(ctx, `
		INSERT INTO follow_requests (id, requester_id, target_id, status, created_at)
		VALUES ($1, $2, $3, 'pending', now())
		ON CONFLICT (requester_id, target_id) WHERE status = 'pending' DO NOTHING
		RETURNING id`, id, actorID, targetID,
	).Scan(&requestID)
	if errors.Is(err, pgx.ErrNoRows) {
		// 同一对账号只保留一条待处理申请，重复申请幂等。
		return model.FollowStatePending, nil
	}
	if err != nil {
		return "", err
	}
	if err := notice.Create(ctx, q, s.ids, notice.Params{
		IdempotencyKey: notifyKey("follow_request", requestID),
		RecipientID:    targetID,
		ActorID:        actorID,
		Type:           notice.TypeFollowRequest,
		TargetType:     "user",
		TargetID:       actorID,
	}); err != nil {
		return "", err
	}
	return model.FollowStatePending, nil
}

// Unfollow 取关，或取消自己发出的待审批申请；两者都幂等。
func (s *Service) Unfollow(c *gin.Context) {
	ctx := c.Request.Context()
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	viewer, ok := s.requireViewer(c)
	if !ok {
		return
	}
	if err := access.RequireWritable(viewer); err != nil {
		s.fail(c, "检查取关权限失败", err)
		return
	}
	if !s.allowAction(c, s.followLimiter, viewer.ID) {
		return
	}
	target, err := resolveUsername(ctx, pool, c.Param("username"))
	if err != nil {
		s.fail(c, "读取取关目标失败", err)
		return
	}

	tx, ok := s.beginTx(c, pool)
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	state, err := s.UnfollowForActor(ctx, tx, viewer.ID, target.ID)
	if err != nil {
		s.fail(c, "取关失败", err)
		return
	}
	if !s.commit(c, tx) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"follow_state": state})
}

// UnfollowForActor 是取关与取消待审批申请的核心实现，供 AI 动作执行与管理后台复用。
func (s *Service) UnfollowForActor(ctx context.Context, q access.Querier, actorID, targetID int64) (string, error) {
	if actorID <= 0 || targetID <= 0 {
		return model.FollowStateNone, nil
	}
	if err := access.RequireActorWritable(ctx, q, actorID); err != nil {
		return "", err
	}
	if _, err := q.Exec(ctx, `
		UPDATE follows SET is_active = FALSE, updated_at = now()
		WHERE follower_id = $1 AND followee_id = $2 AND is_active`, actorID, targetID,
	); err != nil {
		return "", err
	}
	if _, err := q.Exec(ctx, `
		UPDATE follow_requests SET status = 'canceled', resolved_at = now()
		WHERE requester_id = $1 AND target_id = $2 AND status = 'pending'`, actorID, targetID,
	); err != nil {
		return "", err
	}
	return model.FollowStateNone, nil
}

// RemoveFollower 移除自己的粉丝：只解除关注关系，不创建通知（不等于拉黑，
// 对方可以重新关注或申请）。
func (s *Service) RemoveFollower(c *gin.Context) {
	ctx := c.Request.Context()
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	viewer, ok := s.requireViewer(c)
	if !ok {
		return
	}
	if err := access.RequireWritable(viewer); err != nil {
		s.fail(c, "检查移除粉丝权限失败", err)
		return
	}
	if !s.allowAction(c, s.followLimiter, viewer.ID) {
		return
	}
	follower, err := resolveUsername(ctx, pool, c.Param("username"))
	if err != nil {
		s.fail(c, "读取粉丝账号失败", err)
		return
	}
	// 不存在生效关系时不做任何修改，幂等返回成功。
	if _, err := pool.Exec(ctx, `
		UPDATE follows SET is_active = FALSE, updated_at = now()
		WHERE follower_id = $1 AND followee_id = $2 AND is_active`, follower.ID, viewer.ID,
	); err != nil {
		s.internalError(c, "移除粉丝失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"follow_state": model.FollowStateNone})
}

// ListFollowers 列出目标的生效粉丝（游标按关注行 ID 倒序）。
func (s *Service) ListFollowers(c *gin.Context) { s.listFollows(c, true) }

// ListFollowing 列出目标的生效关注（游标按关注行 ID 倒序）。
func (s *Service) ListFollowing(c *gin.Context) { s.listFollows(c, false) }

func (s *Service) listFollows(c *gin.Context, followers bool) {
	ctx := c.Request.Context()
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	target, err := resolveUsername(ctx, pool, c.Param("username"))
	if err != nil {
		s.fail(c, "读取用户失败", err)
		return
	}
	limit, cursor, err := listParams(c)
	if err != nil {
		s.fail(c, "分页参数无效", err)
		return
	}

	// 只列出生效关系；游标游走的是 follows.id。
	query := `
		SELECT f.id, u.id, u.username, u.nickname, u.avatar_url, u.is_ai, u.status
		FROM follows f
		JOIN users u ON u.id = f.follower_id
		WHERE f.followee_id = $1 AND f.is_active AND ($2 = 0 OR f.id < $2)
		ORDER BY f.id DESC
		LIMIT $3`
	if !followers {
		query = `
		SELECT f.id, u.id, u.username, u.nickname, u.avatar_url, u.is_ai, u.status
		FROM follows f
		JOIN users u ON u.id = f.followee_id
		WHERE f.follower_id = $1 AND f.is_active AND ($2 = 0 OR f.id < $2)
		ORDER BY f.id DESC
		LIMIT $3`
	}
	rows, err := pool.Query(ctx, query, target.ID, cursor, limit+1)
	if err != nil {
		s.internalError(c, "查询关注列表失败", err)
		return
	}
	defer rows.Close()

	items := make([]model.UserBriefResponse, 0, limit)
	rowIDs := make([]int64, 0, limit)
	for rows.Next() {
		var (
			rowID int64
			brief model.UserBrief
			state string
		)
		if err := rows.Scan(&rowID, &brief.ID, &brief.Username, &brief.Nickname,
			&brief.AvatarURL, &brief.IsAI, &state); err != nil {
			s.internalError(c, "读取关注列表失败", err)
			return
		}
		brief.IsSuspended = state != "active"
		items = append(items, brief.Response())
		rowIDs = append(rowIDs, rowID)
	}
	if err := rows.Err(); err != nil {
		s.internalError(c, "读取关注列表失败", err)
		return
	}

	trimmed, hasMore := trimPage(items, limit)
	var lastID int64
	if len(trimmed) > 0 {
		lastID = rowIDs[len(trimmed)-1]
	}
	c.JSON(http.StatusOK, api.ListResponse(trimmed, lastID, hasMore))
}
