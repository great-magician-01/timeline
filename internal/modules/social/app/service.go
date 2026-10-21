package app

import (
	"context"
	"strconv"
	"time"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/audit"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/platform/ratelimit"
	"github.com/great-magician-01/timeline/internal/shared/access"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// 限频参数（单实例内存限频；真正的幂等由数据库唯一约束与状态字段保证）。
const (
	commentLimit  = 30
	commentWindow = 10 * time.Minute
	likeLimit     = 240
	likeWindow    = 10 * time.Minute
	followLimit   = 60
	followWindow  = time.Hour
	bookmarkLimit  = 240
	bookmarkWindow = 10 * time.Minute
)

// Deps 是 Service 的全部依赖；除 Clock 与限频器外必须完整注入。
type Deps struct {
	Clock clock.Clock
	IDs   IDGenerator
	Tx    TxRunner

	Users   contracts.UserReader
	Posts   contracts.ContentService
	Notices contracts.NoticeService
	Audit   audit.Writer

	Follows   FollowRepo
	Requests  FollowRequestRepo
	Likes     LikeRepo
	Comments  CommentRepo
	Reposts   RepostRepo
	Bookmarks BookmarkRepo
	Folders   BookmarkFolderRepo

	// 不注入时使用生产默认限频参数。
	CommentLimiter  *ratelimit.Limiter
	LikeLimiter     *ratelimit.Limiter
	FollowLimiter   *ratelimit.Limiter
	BookmarkLimiter *ratelimit.Limiter
}

// Service 实现社交域用例，并发布 contracts.RelationReader（content/notice/agent 复用）。
type Service struct {
	deps Deps
}

// 编译期断言：social app 实现 contracts 的关系读取端口。
var _ contracts.RelationReader = (*Service)(nil)

// New 组装社交域服务；nil Clock 回退系统时钟，nil 限频器使用生产默认参数。
func New(deps Deps) *Service {
	if deps.Clock == nil {
		deps.Clock = clock.System()
	}
	if deps.CommentLimiter == nil {
		deps.CommentLimiter = ratelimit.New(commentLimit, commentWindow)
	}
	if deps.LikeLimiter == nil {
		deps.LikeLimiter = ratelimit.New(likeLimit, likeWindow)
	}
	if deps.FollowLimiter == nil {
		deps.FollowLimiter = ratelimit.New(followLimit, followWindow)
	}
	if deps.BookmarkLimiter == nil {
		deps.BookmarkLimiter = ratelimit.New(bookmarkLimit, bookmarkWindow)
	}
	return &Service{deps: deps}
}

// ===== 读模型 =====

// Comment 是评论列表/详情项：行数据 + 作者摘要 + 访问者状态。
type Comment struct {
	Row       model.Comment
	Author    model.UserBrief
	LikedByMe bool
	CanDelete bool
}

// CommentPage 是评论分页结果；Previews 仅顶层评论列表填充（key 为顶层评论 ID）。
type CommentPage struct {
	Items      []Comment
	Previews   map[int64][]Comment
	HasMore    bool
	NextCursor int64
}

// FollowItem 是关注/粉丝列表项。
type FollowItem struct {
	RowID int64
	User  model.UserBrief
}

// FollowPage 是关注/粉丝分页结果。
type FollowPage struct {
	Items      []FollowItem
	HasMore    bool
	NextCursor int64
}

// RequestItem 是关注申请列表项。
type RequestItem struct {
	ID         int64
	Status     string
	Requester  model.UserBrief
	Target     model.UserBrief
	CreatedAt  time.Time
	ResolvedAt *time.Time
}

// RequestPage 是关注申请分页结果。
type RequestPage struct {
	Items      []RequestItem
	HasMore    bool
	NextCursor int64
}

// RequestResult 是处理关注申请的结果。
type RequestResult struct {
	Status      string
	FollowState string
}

// LikeResult 是点赞/取消点赞的结果。
type LikeResult struct {
	Liked bool
	Count int
}

// RepostResult 是转发/取消转发的结果。
// RepostID/RepostedAt 是本次生效转发行的标识与时间（未转发时为零值），
// 供 HTTP 响应组装 repost 对象，便于前端在当前列表即时插入转发项。
type RepostResult struct {
	Reposted   bool
	Count      int
	RepostID   int64
	RepostedAt time.Time
}

// BookmarkResult 是收藏/取消收藏的结果；FolderID 是收藏归属（未分组为 nil）。
type BookmarkResult struct {
	Bookmarked bool
	FolderID   *int64
}

// BookmarkFolder 是收藏夹列表项（含各自的生效收藏数）。
type BookmarkFolder struct {
	ID            int64
	Name          string
	BookmarkCount int
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ===== contracts.RelationReader 实现（供 content/notice/agent 复用） =====

// IsFollowing 判断 followerID 是否对 followeeID 存在生效关注关系。
func (s *Service) IsFollowing(ctx context.Context, followerID, followeeID int64) (bool, error) {
	if followerID <= 0 || followeeID <= 0 {
		return false, nil
	}
	return s.deps.Follows.Following(ctx, followerID, followeeID)
}

// FollowingSet 一次性载入访问者的全部生效关注对象。
func (s *Service) FollowingSet(ctx context.Context, followerID int64) (map[int64]bool, error) {
	return s.deps.Follows.FollowingSet(ctx, followerID)
}

// FollowState 返回访问者对目标用户的关系状态（model.FollowState* 四态）。
// 三项关系状态由仓储单条三 EXISTS 查询一次取回，避免每次 3 次数据库往返。
func (s *Service) FollowState(ctx context.Context, viewerID, targetID int64) (string, error) {
	if viewerID <= 0 || targetID <= 0 {
		return model.FollowStateNone, nil
	}
	following, pending, incomingPending, err := s.deps.Follows.FollowState(ctx, viewerID, targetID)
	if err != nil {
		return "", err
	}
	return access.ClassifyFollowState(following, pending, incomingPending), nil
}

// GetComments 批量读取评论（通知预览使用；已删除评论由调用方按状态处理）。
func (s *Service) GetComments(ctx context.Context, commentIDs []int64) (map[int64]model.Comment, error) {
	return s.deps.Comments.ByIDs(ctx, commentIDs)
}

// ===== 内部工具 =====

func (s *Service) now() time.Time { return s.deps.Clock.Now() }

// newID 生成雪花 ID；失败包装为 internal 错误，不把驱动错误暴露给调用方。
func (s *Service) newID() (int64, error) {
	id, err := s.deps.IDs.Next()
	if err != nil {
		return 0, apperr.Wrap(err, apperr.KindInternal, "生成 ID 失败")
	}
	if id <= 0 {
		return 0, apperr.Internal("生成 ID 失败")
	}
	return id, nil
}

// requireWritable 校验写操作最低条件（登录、未停用、非 AI）。
func (s *Service) requireWritable(viewer model.Viewer) error {
	return access.RequireWritable(viewer)
}

// loadVisiblePost 通过 content 的可见性策略读取帖子；不可见按不存在处理。
func (s *Service) loadVisiblePost(ctx context.Context, viewer model.Viewer, postID int64) (model.Post, error) {
	return s.deps.Posts.GetVisiblePost(ctx, viewer, postID)
}

// authorSuspended 报告作者账号是否已停用。
func (s *Service) authorSuspended(ctx context.Context, authorID int64) (bool, error) {
	return s.deps.Users.IsSuspended(ctx, authorID)
}

// requireInteractivePost 完成「登录 + 可见 + 可互动」三段校验。
func (s *Service) requireInteractivePost(ctx context.Context, viewer model.Viewer, postID int64) (model.Post, error) {
	if err := s.requireWritable(viewer); err != nil {
		return model.Post{}, err
	}
	post, err := s.loadVisiblePost(ctx, viewer, postID)
	if err != nil {
		return model.Post{}, err
	}
	suspended, err := s.authorSuspended(ctx, post.AuthorID)
	if err != nil {
		return model.Post{}, err
	}
	if err := access.CanInteract(viewer, post, suspended); err != nil {
		return model.Post{}, err
	}
	return post, nil
}

// bumpPostCounter 通过 content 端口原子调整帖子计数并返回最新值。
func (s *Service) bumpPostCounter(
	ctx context.Context, postID int64, counter string, delta int, at time.Time,
) (int, error) {
	return s.deps.Posts.BumpPostCounter(ctx, contracts.PostCounterInput{
		PostID:  postID,
		Counter: counter,
		Delta:   delta,
		At:      at,
	})
}

// allowAction 执行用户维度限频；超限返回带 RetryAfter 的 rate_limited 错误。
func (s *Service) allowAction(limiter *ratelimit.Limiter, viewer model.Viewer) error {
	allowed, retryAfter := limiter.Consume("user:" + strconv.FormatInt(viewer.ID, 10))
	if allowed {
		return nil
	}
	return apperr.RateLimited(retryAfter)
}

// trimPage 把「多查一条」的结果裁剪为 limit 条，并给出是否还有下一页。
func trimPage[T any](items []T, limit int) ([]T, bool) {
	if len(items) > limit {
		return items[:limit], true
	}
	return items, false
}

// notifyKey 生成形如 "<kind>:<id>" 的稳定幂等键。
func notifyKey(kind string, id int64) string {
	return kind + ":" + strconv.FormatInt(id, 10)
}

// followNotifyKey 在关注行 ID 之外带上当次生效时间：同一事件的重复投递仍被去重，
// 取关后重新关注却能再次通知。
func followNotifyKey(followsID int64, activeAt time.Time) string {
	return "follow:" + strconv.FormatInt(followsID, 10) + ":" + strconv.FormatInt(activeAt.Unix(), 10)
}
