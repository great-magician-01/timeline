// Package app 编排社交域用例：关注/取关/粉丝移除、关注申请审批、点赞、评论、纯转发。
//
// 事务边界由 app 决定（TxRunner.WithTx）；repo 通过 ctx 中事务自动加入。
// 跨模块依赖通过 shared/contracts 的发布接口注入：账号来自 auth（UserReader）、
// 帖子与帖子计数来自 content（ContentService）、通知写入来自 notice（NoticeService）；
// 组合根注入生产实现，测试使用 fake。
package app

import (
	"context"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/social/domain"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// IDGenerator 生成雪花 ID（platform/id.Generator 满足）。
type IDGenerator interface {
	Next() (int64, error)
}

// TxRunner 是事务入口（*db.TxManager 满足）。
type TxRunner interface {
	WithTx(ctx context.Context, fn func(context.Context) error) error
}

// FollowRow 是关注关系列表行（定义在 domain，repo 不依赖 app）。
type FollowRow = domain.FollowRow

// RequestRow 是关注申请行（定义在 domain，repo 不依赖 app）。
type RequestRow = domain.RequestRow

// RepostEvent 是纯转发事件行（定义在 domain，repo 不依赖 app）。
type RepostEvent = domain.RepostEvent

// RepostEventQuery 是纯转发事件查询条件（定义在 domain，repo 不依赖 app）。
type RepostEventQuery = domain.RepostEventQuery

// FollowRepo 是关注关系 SQL 端口。
type FollowRepo interface {
	// Following 报告 follower 是否已生效关注 followee。
	Following(ctx context.Context, followerID, followeeID int64) (bool, error)
	// FollowState 用单条查询返回访问者对目标的三项关系状态：
	// 是否已生效关注、是否已发出待处理申请、是否存在对方发来的待处理申请。
	FollowState(ctx context.Context, viewerID, targetID int64) (following, pending, incomingPending bool, err error)
	// FollowingSet 返回 follower 的生效关注集合；游客（ID<=0）返回空集。
	FollowingSet(ctx context.Context, followerID int64) (map[int64]bool, error)
	// InsertActive 建立或恢复关注关系；只有从失效翻转为生效时 ok 为 true，
	// 并返回关系行 ID 与本次生效时间（供稳定通知键使用）。
	InsertActive(ctx context.Context, id, followerID, followeeID int64, now time.Time) (rowID int64, activeAt time.Time, ok bool, err error)
	// Deactivate 取关；关系原本不生效时 ok 为 false。
	Deactivate(ctx context.Context, followerID, followeeID int64, now time.Time) (ok bool, err error)
	// ListFollowers / ListFollowing 按关注行 ID 倒序分页。
	ListFollowers(ctx context.Context, followeeID, cursor int64, limit int) ([]FollowRow, error)
	ListFollowing(ctx context.Context, followerID, cursor int64, limit int) ([]FollowRow, error)
	// ListRecentFollowees 读取用户最近关注的账号（按关注行 ID 倒序）。
	ListRecentFollowees(ctx context.Context, followerID int64, limit int) ([]FollowRow, error)
}

// FollowRequestRepo 是关注申请 SQL 端口。
type FollowRequestRepo interface {
	// InsertPending 创建待处理申请；同一对账号已有 pending 申请时 ok 为 false。
	InsertPending(ctx context.Context, id, requesterID, targetID int64, now time.Time) (requestID int64, ok bool, err error)
	// GetForUpdate 在事务内加锁读取申请；不存在返回 apperr.NotFound。
	GetForUpdate(ctx context.Context, id int64) (RequestRow, error)
	// UpdateStatus 迁移申请状态并写入处理时间。
	UpdateStatus(ctx context.Context, id int64, status string, resolvedAt time.Time) error
	// ListPendingIncoming / ListPendingOutgoing 按 ID 倒序分页待处理申请。
	ListPendingIncoming(ctx context.Context, targetID, cursor int64, limit int) ([]RequestRow, error)
	ListPendingOutgoing(ctx context.Context, requesterID, cursor int64, limit int) ([]RequestRow, error)
	// CancelPending 取消自己发出的待处理申请；没有可取消申请时 ok 为 false。
	CancelPending(ctx context.Context, requesterID, targetID int64, now time.Time) (ok bool, err error)
}

// LikeRepo 是点赞 SQL 端口。
type LikeRepo interface {
	// Activate 点赞；只有从失效翻转为生效时 changed 为 true。
	Activate(ctx context.Context, id, userID int64, targetType string, targetID int64, now time.Time) (likeID int64, changed bool, err error)
	// Deactivate 取消点赞；changed 表示本次是否真正翻转。
	Deactivate(ctx context.Context, userID int64, targetType string, targetID int64, now time.Time) (likeID int64, changed bool, err error)
	// ActiveStates 批量查询访问者已点赞的目标集合。
	ActiveStates(ctx context.Context, userID int64, targetType string, targetIDs []int64) (map[int64]bool, error)
	// IsActive 报告用户是否已点赞目标。
	IsActive(ctx context.Context, userID int64, targetType string, targetID int64) (bool, error)
	// LikedPostIDs 返回用户最近点赞的帖子 ID（按点赞行 ID 倒序）。
	LikedPostIDs(ctx context.Context, userID int64, limit int) ([]int64, error)
}

// CommentRepo 是评论 SQL 端口（comments 表归 social 所有）。
type CommentRepo interface {
	// Get 读取评论；不存在返回 apperr.NotFound。
	Get(ctx context.Context, id int64) (model.Comment, error)
	// GetForUpdate 在事务内加锁读取评论；不存在返回 apperr.NotFound。
	GetForUpdate(ctx context.Context, id int64) (model.Comment, error)
	// ByIDs 批量读取评论（含已删除），缺失 ID 不在结果中。
	ByIDs(ctx context.Context, ids []int64) (map[int64]model.Comment, error)
	// Insert 写入评论并返回数据库中的完整行。
	Insert(ctx context.Context, comment model.Comment) (model.Comment, error)
	// ListTopLevel 按 ID 升序分页顶层评论。
	ListTopLevel(ctx context.Context, postID, cursor int64, limit int) ([]model.Comment, error)
	// ListReplies 按 ID 升序分页某顶层评论下的回复。
	ListReplies(ctx context.Context, rootCommentID, cursor int64, limit int) ([]model.Comment, error)
	// ReplyPreviews 返回每个顶层评论的前 perRoot 条回复（按 ID 升序）。
	ReplyPreviews(ctx context.Context, rootIDs []int64, perRoot int) (map[int64][]model.Comment, error)
	// AdjustReplyCount / AdjustLikeCount 原子调整评论计数并返回新值。
	AdjustReplyCount(ctx context.Context, commentID int64, delta int, now time.Time) (int, error)
	AdjustLikeCount(ctx context.Context, commentID int64, delta int, now time.Time) (int, error)
	// MarkDeleted 逻辑删除评论并记录处置角色；已删除时 changed 为 false。
	MarkDeleted(ctx context.Context, id int64, byAdmin, byAuthor, byPostAuthor bool, now time.Time) (changed bool, err error)
	// ParentID 读取评论的父评论 ID（顶层返回 nil），用于计算回复深度。
	ParentID(ctx context.Context, commentID int64) (*int64, error)
	// RecentByPost 读取帖子最近讨论（已发布评论，按 ID 倒序）。
	RecentByPost(ctx context.Context, postID int64, limit int) ([]model.Comment, error)
	// ListByAuthor 按作者分页读取回复评论（(created_at, id) 倒序）。
	ListByAuthor(ctx context.Context, authorID int64, afterAt time.Time, afterID int64, limit int) ([]model.Comment, error)
	// ListByPostIDsSince 读取一组帖子在 since 之后的新评论（按 ID 倒序）；
	// excludeAuthorID > 0 时排除该作者自己的评论，避免 AI 回复自己的评论形成自循环。
	ListByPostIDsSince(ctx context.Context, postIDs []int64, excludeAuthorID int64, since time.Time, limit int) ([]model.Comment, error)
}

// RepostRepo 是纯转发 SQL 端口。
type RepostRepo interface {
	// Activate 转发；authorIsAI 是转发者账号类型快照（写入 reposts.author_is_ai）。
	// 返回本次生效转发行的 ID 与时间：从失效翻转为生效时 changed 为 true，
	// 重新转发会刷新 created_at；已生效的重复转发 changed 为 false，返回既有行。
	// 返回值供 HTTP 响应组装 repost 对象、供时间线展示最近一次转发时间。
	Activate(
		ctx context.Context, id, userID, postID int64, authorIsAI bool, now time.Time,
	) (repostID int64, repostedAt time.Time, changed bool, err error)
	// Deactivate 取消转发；changed 表示本次是否真正翻转。
	Deactivate(ctx context.Context, userID, postID int64, now time.Time) (repostID int64, changed bool, err error)
	// IsActive 报告用户是否已转发帖子。
	IsActive(ctx context.Context, userID, postID int64) (bool, error)
	// ActiveStates 批量查询用户已转发的帖子集合。
	ActiveStates(ctx context.Context, userID int64, postIDs []int64) (map[int64]bool, error)
	// ListEvents 读取生效中的纯转发事件（时间+ID 倒序）。
	ListEvents(ctx context.Context, query RepostEventQuery) ([]RepostEvent, error)
}
