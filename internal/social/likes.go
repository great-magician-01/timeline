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
	"github.com/great-magician-01/timeline/internal/notice"
)

// countDelta 返回状态翻转对应的计数增量：失效→生效 +1，生效→失效 -1，
// 状态未变化时为 0（重复点赞不重复计数）。
func countDelta(wasActive, nowActive bool) int {
	switch {
	case !wasActive && nowActive:
		return 1
	case wasActive && !nowActive:
		return -1
	default:
		return 0
	}
}

// LikePost 点赞帖子。
func (s *Service) LikePost(c *gin.Context) { s.setPostLike(c, true) }

// UnlikePost 取消点赞帖子。
func (s *Service) UnlikePost(c *gin.Context) { s.setPostLike(c, false) }

// LikeComment 点赞评论。
func (s *Service) LikeComment(c *gin.Context) { s.setCommentLike(c, true) }

// UnlikeComment 取消点赞评论。
func (s *Service) UnlikeComment(c *gin.Context) { s.setCommentLike(c, false) }

func (s *Service) setPostLike(c *gin.Context, active bool) {
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
		s.fail(c, "读取点赞目标失败", err)
		return
	}
	if err := access.RequireInteractivePost(ctx, pool, viewer, post); err != nil {
		s.fail(c, "检查互动权限失败", err)
		return
	}
	if !s.allowAction(c, s.likeLimiter, viewer.ID) {
		return
	}
	s.runLike(c, pool, viewer.ID, targetPost, postID, active)
}

func (s *Service) setCommentLike(c *gin.Context, active bool) {
	ctx := c.Request.Context()
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	viewer, ok := s.requireViewer(c)
	if !ok {
		return
	}
	commentID, err := api.ParseID(c.Param("id"))
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	comment, err := loadComment(ctx, pool, commentID)
	if err != nil {
		s.fail(c, "读取点赞目标失败", err)
		return
	}
	// 先过可见性，再看评论自身状态：不可见评论不应暴露存在性。
	post, err := LoadVisiblePost(ctx, pool, viewer, comment.PostID)
	if err != nil {
		s.fail(c, "读取评论所属帖子失败", err)
		return
	}
	if err := access.RequireInteractivePost(ctx, pool, viewer, post); err != nil {
		s.fail(c, "检查互动权限失败", err)
		return
	}
	if comment.Deleted() {
		s.fail(c, "点赞评论失败", api.ErrForbidden("该评论已删除，不能互动"))
		return
	}
	if !s.allowAction(c, s.likeLimiter, viewer.ID) {
		return
	}
	s.runLike(c, pool, viewer.ID, targetComment, commentID, active)
}

// runLike 在事务内执行点赞或取消点赞并输出统一响应。
func (s *Service) runLike(c *gin.Context, pool *pgxpool.Pool, actorID int64, targetType string, targetID int64, active bool) {
	tx, ok := s.beginTx(c, pool)
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(c.Request.Context()) }()

	var (
		liked bool
		count int
		err   error
	)
	if active {
		liked, count, err = s.LikeForActor(c.Request.Context(), tx, actorID, targetType, targetID)
	} else {
		liked, count, err = s.UnlikeForActor(c.Request.Context(), tx, actorID, targetType, targetID)
	}
	if err != nil {
		s.fail(c, "更新点赞状态失败", err)
		return
	}
	if !s.commit(c, tx) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"liked": liked, "like_count": count})
}

// LikeForActor 点赞（targetType = "post" | "comment"），幂等，返回点赞后的计数。
// 传入事务可保证点赞记录、计数与通知原子写入。
func (s *Service) LikeForActor(ctx context.Context, q access.Querier, actorID int64, targetType string, targetID int64) (bool, int, error) {
	return s.setLike(ctx, q, actorID, targetType, targetID, true)
}

// UnlikeForActor 取消点赞，幂等，返回取消后的计数。
func (s *Service) UnlikeForActor(ctx context.Context, q access.Querier, actorID int64, targetType string, targetID int64) (bool, int, error) {
	return s.setLike(ctx, q, actorID, targetType, targetID, false)
}

func (s *Service) setLike(
	ctx context.Context,
	q access.Querier,
	actorID int64,
	targetType string,
	targetID int64,
	active bool,
) (bool, int, error) {
	if targetType != targetPost && targetType != targetComment {
		return false, 0, api.ErrValidation("点赞目标类型无效")
	}
	if err := access.RequireActorWritable(ctx, q, actorID); err != nil {
		return false, 0, err
	}

	var (
		targetAuthorID int64
		postID         int64
	)
	if active {
		switch targetType {
		case targetPost:
			post, err := loadPostForInteraction(ctx, q, targetID, false)
			if err != nil {
				return false, 0, err
			}
			if err := ensureNonAIActor(ctx, q, actorID, post); err != nil {
				return false, 0, err
			}
			if post.AuthorID != actorID {
				if err := ensureAuthorActive(ctx, q, post.AuthorID); err != nil {
					return false, 0, err
				}
			}
			targetAuthorID, postID = post.AuthorID, post.ID
		case targetComment:
			comment, err := loadCommentTarget(ctx, q, targetID)
			if err != nil {
				return false, 0, err
			}
			post, err := loadPostForInteraction(ctx, q, comment.PostID, false)
			if err != nil {
				return false, 0, err
			}
			if err := ensureNonAIActor(ctx, q, actorID, post); err != nil {
				return false, 0, err
			}
			if comment.AuthorID != actorID {
				if err := ensureAuthorActive(ctx, q, comment.AuthorID); err != nil {
					return false, 0, err
				}
			}
			targetAuthorID, postID = comment.AuthorID, comment.PostID
		}
	}

	if active {
		id, err := s.ids.Next()
		if err != nil {
			return false, 0, err
		}
		var likeID int64
		// 唯一索引兜底幂等：只有从失效翻转为生效时才返回行。
		err = q.QueryRow(ctx, `
			INSERT INTO likes (id, user_id, target_type, target_id, is_active, created_at, updated_at)
			VALUES ($1, $2, $3, $4, TRUE, now(), now())
			ON CONFLICT (user_id, target_type, target_id)
			DO UPDATE SET is_active = TRUE, updated_at = now()
			WHERE likes.is_active = FALSE
			RETURNING id`, id, actorID, targetType, targetID,
		).Scan(&likeID)
		if errors.Is(err, pgx.ErrNoRows) {
			count, err := readLikeCount(ctx, q, targetType, targetID)
			return true, count, err
		}
		if err != nil {
			return false, 0, err
		}
		count, err := bumpLikeCount(ctx, q, targetType, targetID, countDelta(false, true))
		if err != nil {
			return false, 0, err
		}
		if err := s.notifyLike(ctx, q, actorID, targetType, likeID, targetAuthorID, postID, targetID); err != nil {
			return false, 0, err
		}
		return true, count, nil
	}

	var likeID int64
	err := q.QueryRow(ctx, `
		UPDATE likes SET is_active = FALSE, updated_at = now()
		WHERE user_id = $1 AND target_type = $2 AND target_id = $3 AND is_active
		RETURNING id`, actorID, targetType, targetID,
	).Scan(&likeID)
	if errors.Is(err, pgx.ErrNoRows) {
		count, err := readLikeCount(ctx, q, targetType, targetID)
		return false, count, err
	}
	if err != nil {
		return false, 0, err
	}
	count, err := bumpLikeCount(ctx, q, targetType, targetID, countDelta(true, false))
	if err != nil {
		return false, 0, err
	}
	return false, count, nil
}

// notifyLike 生成点赞通知；取消点赞不删除历史通知（docs/WORKFLOWS.md 9.2）。
func (s *Service) notifyLike(
	ctx context.Context,
	q access.Querier,
	actorID int64,
	targetType string,
	likeID, targetAuthorID, postID, targetID int64,
) error {
	params := notice.Params{
		RecipientID: targetAuthorID,
		ActorID:     actorID,
		TargetID:    targetID,
		PostID:      &postID,
	}
	if targetType == targetComment {
		commentID := targetID
		params.Type = notice.TypeCommentLike
		params.TargetType = targetComment
		params.CommentID = &commentID
		params.IdempotencyKey = notifyKey("comment_like", likeID)
	} else {
		params.Type = notice.TypePostLike
		params.TargetType = targetPost
		params.IdempotencyKey = notifyKey("post_like", likeID)
	}
	return notice.Create(ctx, q, s.ids, params)
}

// bumpLikeCount 在同一事务内更新点赞计数；计数不允许为负。
func bumpLikeCount(ctx context.Context, q access.Querier, targetType string, targetID int64, delta int) (int, error) {
	var count int
	var err error
	switch targetType {
	case targetPost:
		err = q.QueryRow(ctx,
			`UPDATE posts SET like_count = GREATEST(like_count + $2, 0) WHERE id = $1 RETURNING like_count`,
			targetID, delta).Scan(&count)
	case targetComment:
		err = q.QueryRow(ctx,
			`UPDATE comments SET like_count = GREATEST(like_count + $2, 0) WHERE id = $1 RETURNING like_count`,
			targetID, delta).Scan(&count)
	default:
		return 0, api.ErrValidation("点赞目标类型无效")
	}
	return count, err
}

// readLikeCount 读取当前点赞计数；状态未变化时用于返回幂等结果。
func readLikeCount(ctx context.Context, q access.Querier, targetType string, targetID int64) (int, error) {
	var count int
	var err error
	switch targetType {
	case targetPost:
		err = q.QueryRow(ctx, `SELECT like_count FROM posts WHERE id = $1`, targetID).Scan(&count)
	case targetComment:
		err = q.QueryRow(ctx, `SELECT like_count FROM comments WHERE id = $1`, targetID).Scan(&count)
	default:
		return 0, api.ErrValidation("点赞目标类型无效")
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return count, err
}
