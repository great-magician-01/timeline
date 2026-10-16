package social

import (
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

// 关注申请的处理动作。
const (
	actionApprove = "approve"
	actionReject  = "reject"
	actionCancel  = "cancel"
)

// followRequestResponse 是关注申请列表项（docs/API.md §5）。
type followRequestResponse struct {
	ID         string                  `json:"id"`
	Status     string                  `json:"status"`
	Requester  model.UserBriefResponse `json:"requester"`
	Target     model.UserBriefResponse `json:"target"`
	CreatedAt  string                  `json:"created_at"`
	ResolvedAt *string                 `json:"resolved_at"`
}

// requestTransition 计算申请状态机的下一步：
// pending 时按动作迁移；已经处于目标状态时幂等返回；其余情况视为冲突。
func requestTransition(current, action string) (next string, changed bool, err error) {
	var target string
	switch action {
	case actionApprove:
		target = model.FollowRequestApproved
	case actionReject:
		target = model.FollowRequestRejected
	case actionCancel:
		target = model.FollowRequestCanceled
	default:
		return "", false, api.ErrValidation("未知的申请操作")
	}
	switch current {
	case target:
		return current, false, nil
	case model.FollowRequestPending:
		return target, true, nil
	default:
		return "", false, api.ErrConflict("request_resolved", "该申请已处理，无法再次操作")
	}
}

// ListFollowRequests 列出关注申请：direction=incoming（默认）为发给我的，
// outgoing 为我发出的；只包含待处理申请，按 ID 倒序。
func (s *Service) ListFollowRequests(c *gin.Context) {
	ctx := c.Request.Context()
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	viewer, ok := s.requireViewer(c)
	if !ok {
		return
	}
	direction := c.Query("direction")
	if direction == "" {
		direction = "incoming"
	}
	if direction != "incoming" && direction != "outgoing" {
		api.Error(c, http.StatusBadRequest, "invalid_request", "direction 只支持 incoming 或 outgoing")
		return
	}
	limit, cursor, err := listParams(c)
	if err != nil {
		s.fail(c, "分页参数无效", err)
		return
	}

	column := "target_id"
	if direction == "outgoing" {
		column = "requester_id"
	}
	rows, err := pool.Query(ctx, `
		SELECT id, requester_id, target_id, status, created_at, resolved_at
		FROM follow_requests
		WHERE `+column+` = $1 AND status = 'pending' AND ($2 = 0 OR id < $2)
		ORDER BY id DESC
		LIMIT $3`, viewer.ID, cursor, limit+1)
	if err != nil {
		s.internalError(c, "查询关注申请失败", err)
		return
	}
	defer rows.Close()

	type requestRow struct {
		id          int64
		requesterID int64
		targetID    int64
		status      string
		createdAt   time.Time
		resolvedAt  *time.Time
	}
	requestRows := make([]requestRow, 0, limit)
	userIDs := make([]int64, 0, 2*limit)
	for rows.Next() {
		var row requestRow
		if err := rows.Scan(&row.id, &row.requesterID, &row.targetID, &row.status,
			&row.createdAt, &row.resolvedAt); err != nil {
			s.internalError(c, "读取关注申请失败", err)
			return
		}
		requestRows = append(requestRows, row)
		userIDs = append(userIDs, row.requesterID, row.targetID)
	}
	if err := rows.Err(); err != nil {
		s.internalError(c, "读取关注申请失败", err)
		return
	}

	briefs, err := access.UserBriefsByID(ctx, pool, userIDs)
	if err != nil {
		s.internalError(c, "读取申请者资料失败", err)
		return
	}
	trimmed, hasMore := trimPage(requestRows, limit)
	items := make([]followRequestResponse, 0, len(trimmed))
	var lastID int64
	for _, row := range trimmed {
		item := followRequestResponse{
			ID:        api.FormatID(row.id),
			Status:    row.status,
			Requester: briefs[row.requesterID].Response(),
			Target:    briefs[row.targetID].Response(),
			CreatedAt: api.FormatTime(row.createdAt),
		}
		if row.resolvedAt != nil {
			resolved := api.FormatTime(*row.resolvedAt)
			item.ResolvedAt = &resolved
		}
		items = append(items, item)
		lastID = row.id
	}
	c.JSON(http.StatusOK, api.ListResponse(items, lastID, hasMore))
}

// ApproveFollowRequest 批准发给自己的申请，并在同一事务内建立生效关注关系。
func (s *Service) ApproveFollowRequest(c *gin.Context) { s.resolveFollowRequest(c, actionApprove) }

// RejectFollowRequest 拒绝发给自己的申请。
func (s *Service) RejectFollowRequest(c *gin.Context) { s.resolveFollowRequest(c, actionReject) }

// CancelFollowRequest 取消自己发出的待审批申请。
func (s *Service) CancelFollowRequest(c *gin.Context) { s.resolveFollowRequest(c, actionCancel) }

func (s *Service) resolveFollowRequest(c *gin.Context, action string) {
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
		s.fail(c, "检查关注申请处理权限失败", err)
		return
	}
	requestID, err := api.ParseID(c.Param("id"))
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	tx, ok := s.beginTx(c, pool)
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		requesterID int64
		targetID    int64
		current     string
	)
	err = tx.QueryRow(ctx, `
		SELECT requester_id, target_id, status
		FROM follow_requests WHERE id = $1 FOR UPDATE`, requestID,
	).Scan(&requesterID, &targetID, &current)
	if errors.Is(err, pgx.ErrNoRows) {
		s.fail(c, "处理关注申请失败", api.ErrNotFound("申请不存在"))
		return
	}
	if err != nil {
		s.fail(c, "处理关注申请失败", err)
		return
	}

	// 非本人的申请按“不存在”处理，不暴露存在性。
	if action == actionCancel {
		if viewer.ID != requesterID {
			s.fail(c, "处理关注申请失败", api.ErrNotFound("申请不存在"))
			return
		}
	} else if viewer.ID != targetID {
		s.fail(c, "处理关注申请失败", api.ErrNotFound("申请不存在"))
		return
	}

	next, changed, err := requestTransition(current, action)
	if err != nil {
		s.fail(c, "处理关注申请失败", err)
		return
	}
	followState := model.FollowStateNone
	if action == actionApprove {
		followState = model.FollowStateFollowing
	}
	if changed {
		if _, err := tx.Exec(ctx, `
			UPDATE follow_requests SET status = $2, resolved_at = now()
			WHERE id = $1`, requestID, next,
		); err != nil {
			s.fail(c, "更新关注申请失败", err)
			return
		}
		if action == actionApprove {
			// 批准就是授权：直接建立生效关系，不受申请者当前审批设置影响。
			suspended, err := access.IsSuspended(ctx, tx, requesterID)
			if err != nil {
				s.fail(c, "批准关注申请失败", err)
				return
			}
			if suspended {
				s.fail(c, "批准关注申请失败", api.ErrForbidden("对方账号已停用，不能批准关注申请"))
				return
			}
			id, err := s.ids.Next()
			if err != nil {
				s.fail(c, "批准关注申请失败", err)
				return
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO follows (id, follower_id, followee_id, is_active, created_at, updated_at)
				VALUES ($1, $2, $3, TRUE, now(), now())
				ON CONFLICT (follower_id, followee_id)
				DO UPDATE SET is_active = TRUE, updated_at = now()`, id, requesterID, targetID,
			); err != nil {
				s.fail(c, "建立关注关系失败", err)
				return
			}
			if err := notice.Create(ctx, tx, s.ids, notice.Params{
				IdempotencyKey: notifyKey("follow_approve", requestID),
				RecipientID:    requesterID,
				ActorID:        targetID,
				Type:           notice.TypeFollowApprove,
				TargetType:     "user",
				TargetID:       targetID,
			}); err != nil {
				s.fail(c, "创建申请批准通知失败", err)
				return
			}
		}
	}

	if !s.commit(c, tx) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": next, "follow_state": followState})
}
