// Package social 实现关注关系与关注申请、粉丝移除、点赞、评论与回复、纯转发。
// 所有取消类操作只置失效标记（is_active = FALSE / status = 'deleted'），不做物理删除；
// 关系、计数与通知在同一事务内写入，通知使用稳定幂等键避免重复投递
// （docs/WORKFLOWS.md 第 4、5、9 章，docs/API.md 第 5、8 章）。
package social

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/access"
	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/auth"
	"github.com/great-magician-01/timeline/internal/model"
	"github.com/great-magician-01/timeline/internal/ratelimit"
)

// 互动目标类型，与 likes.target_type 约束一致。
const (
	targetPost    = "post"
	targetComment = "comment"
)

type PoolProvider interface {
	Pool() *pgxpool.Pool
}

type IDGenerator interface {
	Next() (int64, error)
}

// Service 承载社交互动接口。限频器只保存在当前进程内（单实例部署），
// 用于安全兜底，真正的幂等由数据库唯一约束与状态字段保证。
type Service struct {
	pools PoolProvider
	ids   IDGenerator
	log   *slog.Logger

	commentLimiter *ratelimit.Limiter // 每用户 30 次 / 10 分钟
	likeLimiter    *ratelimit.Limiter // 每用户 240 次 / 10 分钟
	followLimiter  *ratelimit.Limiter // 每用户 60 次 / 1 小时
}

func NewService(pools PoolProvider, ids IDGenerator, log *slog.Logger) *Service {
	return &Service{
		pools:          pools,
		ids:            ids,
		log:            log,
		commentLimiter: ratelimit.New(30, 10*time.Minute),
		likeLimiter:    ratelimit.New(240, 10*time.Minute),
		followLimiter:  ratelimit.New(60, time.Hour),
	}
}

func (s *Service) pool(c *gin.Context) (*pgxpool.Pool, bool) {
	pool := s.pools.Pool()
	if pool == nil {
		api.Error(c, http.StatusServiceUnavailable, "service_unavailable", "服务尚未就绪，请稍后重试")
		return nil, false
	}
	return pool, true
}

func (s *Service) internalError(c *gin.Context, message string, err error) {
	s.log.Error(message, "err", err, "path", c.Request.URL.Path)
	api.Error(c, http.StatusInternalServerError, "internal_error", "服务暂时异常，请稍后重试")
}

// fail 把业务错误写回响应；不是 *api.Failure 时按内部错误处理。
func (s *Service) fail(c *gin.Context, message string, err error) {
	if !api.Respond(c, err) {
		s.internalError(c, message, err)
	}
}

// requireViewer 读取当前登录身份；未登录时写回 401。
func (s *Service) requireViewer(c *gin.Context) (model.Viewer, bool) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		api.Error(c, http.StatusUnauthorized, "unauthenticated", "请先登录")
		return model.Viewer{}, false
	}
	return model.ViewerFrom(u), true
}

// viewerForRead 供游客可访问的只读列表使用。
func viewerForRead(c *gin.Context) model.Viewer {
	if u, ok := auth.CurrentUser(c); ok {
		return model.ViewerFrom(u)
	}
	return model.GuestViewer()
}

// retryAfterSeconds 把限频窗口剩余时间换算为 Retry-After 秒数（至少 1 秒）。
func retryAfterSeconds(retryAfter time.Duration) int {
	seconds := int(math.Ceil(retryAfter.Seconds()))
	if seconds < 1 {
		return 1
	}
	return seconds
}

// allowAction 执行用户维度限频；超限时写回 429（带 Retry-After）并返回 false。
func (s *Service) allowAction(c *gin.Context, limiter *ratelimit.Limiter, userID int64) bool {
	allowed, retryAfter := limiter.Consume("user:" + api.FormatID(userID))
	if allowed {
		return true
	}
	c.Header("Retry-After", strconv.Itoa(retryAfterSeconds(retryAfter)))
	api.Respond(c, api.ErrRateLimited())
	return false
}

// beginTx 开启事务，失败的写操作由调用方回滚。
func (s *Service) beginTx(c *gin.Context, pool *pgxpool.Pool) (pgx.Tx, bool) {
	tx, err := pool.Begin(c.Request.Context())
	if err != nil {
		s.internalError(c, "开启事务失败", err)
		return nil, false
	}
	return tx, true
}

// commit 提交事务；失败时写回 500。
func (s *Service) commit(c *gin.Context, tx pgx.Tx) bool {
	if err := tx.Commit(c.Request.Context()); err != nil {
		s.internalError(c, "提交事务失败", err)
		return false
	}
	return true
}

// listParams 解析并校验游标分页参数：limit 为 1-50（默认 20），cursor 为空表示第一页。
func listParams(c *gin.Context) (int, int64, error) {
	limit, err := api.PageLimit(c)
	if err != nil {
		return 0, 0, api.ErrInvalid(err.Error())
	}
	cursor, err := api.TrimPageCursor(c)
	if err != nil {
		return 0, 0, api.ErrInvalid(err.Error())
	}
	return limit, cursor, nil
}

// trimPage 把“多查一条”的结果裁剪为 limit 条，并给出是否还有下一页。
func trimPage[T any](items []T, limit int) ([]T, bool) {
	if len(items) > limit {
		return items[:limit], true
	}
	return items, false
}

// notifyKey 生成形如 "<kind>:<id>" 的稳定幂等键。
func notifyKey(kind string, id int64) string {
	return kind + ":" + api.FormatID(id)
}

// followNotifyKey 在关注行 ID 之外带上当次生效时间：同一事件的重复投递仍被去重，
// 取关后重新关注却能再次通知。
func followNotifyKey(followsID int64, activeAt time.Time) string {
	return "follow:" + api.FormatID(followsID) + ":" + api.FormatID(activeAt.Unix())
}

// resolveUsername 把路径中的用户名解析为账号；不存在时返回 404。
func resolveUsername(ctx context.Context, q access.Querier, username string) (auth.User, error) {
	u, err := auth.UserByUsername(ctx, q, username)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.User{}, api.ErrNotFound("用户不存在")
	}
	if err != nil {
		return auth.User{}, err
	}
	return u, nil
}

// loadPostForInteraction 读取帖子并确认其处于可互动状态（存在且已发布）。
// isAI 为真时，非公开内容一律按“不存在”处理，避免 AI 上下文接触非公开帖子。
func loadPostForInteraction(ctx context.Context, q access.Querier, postID int64, isAI bool) (model.Post, error) {
	post, err := model.ScanPost(q.QueryRow(ctx, `SELECT `+model.PostColumns+` FROM posts WHERE id = $1`, postID))
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Post{}, api.ErrNotFound("内容不存在或无权访问")
	}
	if err != nil {
		return model.Post{}, err
	}
	if post.Status != model.PostStatusPublished {
		if isAI {
			return model.Post{}, api.ErrNotFound("内容不存在或无权访问")
		}
		return model.Post{}, api.ErrForbidden("该内容当前不可互动")
	}
	if isAI && post.Visibility != model.VisibilityPublic {
		return model.Post{}, api.ErrNotFound("内容不存在或无权访问")
	}
	return post, nil
}

// loadComment 读取评论；不存在时返回 404。
func loadComment(ctx context.Context, q access.Querier, commentID int64) (model.Comment, error) {
	comment, err := model.ScanComment(q.QueryRow(ctx, `SELECT `+model.CommentColumns+` FROM comments WHERE id = $1`, commentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Comment{}, api.ErrNotFound("评论不存在或无权访问")
	}
	if err != nil {
		return model.Comment{}, err
	}
	return comment, nil
}

// loadCommentTarget 读取评论并要求其可接受新互动（已删除评论返回 403）。
func loadCommentTarget(ctx context.Context, q access.Querier, commentID int64) (model.Comment, error) {
	comment, err := loadComment(ctx, q, commentID)
	if err != nil {
		return model.Comment{}, err
	}
	if comment.Deleted() {
		return model.Comment{}, api.ErrForbidden("该评论已删除，不能互动")
	}
	return comment, nil
}

// ensureAuthorActive 拒绝与停用账号产生新互动。
func ensureAuthorActive(ctx context.Context, q access.Querier, authorID int64) error {
	suspended, err := access.IsSuspended(ctx, q, authorID)
	if err != nil {
		return err
	}
	if suspended {
		return api.ErrForbidden("对方账号已停用，不能产生新的互动")
	}
	return nil
}

// ensureNonAIActor 非公开内容只允许人类账号互动：AI 动作在服务端按“内容不存在”处理。
// 公开内容直接放行，因此常见路径没有额外查询。
func ensureNonAIActor(ctx context.Context, q access.Querier, actorID int64, post model.Post) error {
	if post.Visibility == model.VisibilityPublic {
		return nil
	}
	var isAI bool
	if err := q.QueryRow(ctx, `SELECT is_ai FROM users WHERE id = $1`, actorID).Scan(&isAI); err != nil {
		return err
	}
	if isAI {
		return api.ErrNotFound("内容不存在或无权访问")
	}
	return nil
}

// LoadVisiblePost 读取帖子并做访问者可见性检查；不存在或不可见时返回 404。
func LoadVisiblePost(ctx context.Context, q access.Querier, viewer model.Viewer, postID int64) (model.Post, error) {
	post, err := model.ScanPost(q.QueryRow(ctx, `SELECT `+model.PostColumns+` FROM posts WHERE id = $1`, postID))
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Post{}, api.ErrNotFound("内容不存在或无权访问")
	}
	if err != nil {
		return model.Post{}, err
	}
	if err := access.RequireVisiblePost(ctx, q, viewer, post); err != nil {
		return model.Post{}, err
	}
	return post, nil
}
