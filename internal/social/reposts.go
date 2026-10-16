package social

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/access"
	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/model"
	"github.com/great-magician-01/timeline/internal/notice"
)

// Repost 纯转发：只允许公开且已发布的帖子，幂等。
func (s *Service) Repost(c *gin.Context) { s.setRepost(c, true) }

// Unrepost 取消纯转发，幂等。
func (s *Service) Unrepost(c *gin.Context) { s.setRepost(c, false) }

func (s *Service) setRepost(c *gin.Context, active bool) {
	ctx := c.Request.Context()
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	viewer, ok := s.requireViewer(c)
	if !ok {
		return
	}
	postID, err := api.ParseID(c.Param("id"))
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	post, err := LoadVisiblePost(ctx, pool, viewer, postID)
	if err != nil {
		s.fail(c, "读取转发目标失败", err)
		return
	}
	if err := access.RequireInteractivePost(ctx, pool, viewer, post); err != nil {
		s.fail(c, "检查转发权限失败", err)
		return
	}
	if !s.allowAction(c, s.likeLimiter, viewer.ID) {
		return
	}
	s.runRepost(c, pool, viewer.ID, postID, active)
}

// runRepost 在事务内执行转发或取消转发并输出统一响应。
func (s *Service) runRepost(c *gin.Context, pool *pgxpool.Pool, actorID, postID int64, active bool) {
	ctx := c.Request.Context()
	tx, ok := s.beginTx(c, pool)
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		reposted bool
		count    int
		err      error
	)
	if active {
		reposted, count, err = s.RepostForActor(ctx, tx, actorID, postID)
	} else {
		reposted, count, err = s.UnrepostForActor(ctx, tx, actorID, postID)
	}
	if err != nil {
		s.fail(c, "更新转发状态失败", err)
		return
	}
	if !s.commit(c, tx) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"reposted": reposted, "repost_count": count})
}

// RepostForActor 纯转发核心实现，供 AI 动作执行与管理后台复用：
// 只允许公开且已发布的帖子，幂等，返回转发后的计数。
func (s *Service) RepostForActor(ctx context.Context, q access.Querier, actorID, postID int64) (bool, int, error) {
	if err := access.RequireActorWritable(ctx, q, actorID); err != nil {
		return false, 0, err
	}
	post, err := loadPostForInteraction(ctx, q, postID, false)
	if err != nil {
		return false, 0, err
	}
	if post.Visibility != model.VisibilityPublic {
		return false, 0, api.ErrForbidden("仅公开内容可以转发")
	}
	if post.AuthorID != actorID {
		if err := ensureAuthorActive(ctx, q, post.AuthorID); err != nil {
			return false, 0, err
		}
	}

	id, err := s.ids.Next()
	if err != nil {
		return false, 0, err
	}
	var repostID int64
	// 唯一索引兜底幂等：只有从失效翻转为生效时才返回行。
	err = q.QueryRow(ctx, `
		INSERT INTO reposts (id, user_id, post_id, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, TRUE, now(), now())
		ON CONFLICT (user_id, post_id)
		DO UPDATE SET is_active = TRUE, updated_at = now()
		WHERE reposts.is_active = FALSE
		RETURNING id`, id, actorID, postID,
	).Scan(&repostID)
	if errors.Is(err, pgx.ErrNoRows) {
		count, err := readRepostCount(ctx, q, postID)
		return true, count, err
	}
	if err != nil {
		return false, 0, err
	}
	count, err := bumpRepostCount(ctx, q, postID, countDelta(false, true))
	if err != nil {
		return false, 0, err
	}
	if err := notice.Create(ctx, q, s.ids, notice.Params{
		IdempotencyKey: notifyKey("repost", repostID),
		RecipientID:    post.AuthorID,
		ActorID:        actorID,
		Type:           notice.TypeRepost,
		TargetType:     targetPost,
		TargetID:       postID,
		PostID:         &postID,
	}); err != nil {
		return false, 0, err
	}
	return true, count, nil
}

// UnrepostForActor 取消纯转发，幂等；原帖即使已删除也允许清理转发状态。
func (s *Service) UnrepostForActor(ctx context.Context, q access.Querier, actorID, postID int64) (bool, int, error) {
	if err := access.RequireActorWritable(ctx, q, actorID); err != nil {
		return false, 0, err
	}
	var repostID int64
	err := q.QueryRow(ctx, `
		UPDATE reposts SET is_active = FALSE, updated_at = now()
		WHERE user_id = $1 AND post_id = $2 AND is_active
		RETURNING id`, actorID, postID,
	).Scan(&repostID)
	if errors.Is(err, pgx.ErrNoRows) {
		count, err := readRepostCount(ctx, q, postID)
		return false, count, err
	}
	if err != nil {
		return false, 0, err
	}
	count, err := bumpRepostCount(ctx, q, postID, countDelta(true, false))
	if err != nil {
		return false, 0, err
	}
	return false, count, nil
}

// bumpRepostCount 在同一事务内更新转发计数；计数不允许为负。
func bumpRepostCount(ctx context.Context, q access.Querier, postID int64, delta int) (int, error) {
	var count int
	err := q.QueryRow(ctx,
		`UPDATE posts SET repost_count = GREATEST(repost_count + $2, 0) WHERE id = $1 RETURNING repost_count`,
		postID, delta).Scan(&count)
	return count, err
}

// readRepostCount 读取当前转发计数；状态未变化时用于返回幂等结果。
func readRepostCount(ctx context.Context, q access.Querier, postID int64) (int, error) {
	var count int
	err := q.QueryRow(ctx, `SELECT repost_count FROM posts WHERE id = $1`, postID).Scan(&count)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return count, err
}
