package contracts

import (
	"context"
	"time"

	"github.com/great-magician-01/timeline/internal/shared/model"
)

// 互动目标类型（与 likes.target_type 约束一致）。消费者：agent。
const (
	InteractiveTargetPost    = "post"
	InteractiveTargetComment = "comment"
	InteractiveTargetUser    = "user"
)

// ActorCommentInput 描述一次评论或回复；AI 动作执行与管理员处置共用。
// 消费者：agent（comment/reply 动作）。
type ActorCommentInput struct {
	ActorID       int64
	IsAI          bool
	IsAdmin       bool
	PostID        int64
	ParentID      *int64
	ContentFormat string
	ContentSource string
	At            time.Time
}

// DeleteCommentInput 逻辑删除评论（byAdmin=true 时由管理员执行并写审计）。
// 消费者：admin（评论审核删除）。
type DeleteCommentInput struct {
	ActorID   int64
	CommentID int64
	ByAdmin   bool
	At        time.Time
}

// LikeInput 点赞/取消点赞帖子或评论。消费者：agent（like/unlike 动作）。
type LikeInput struct {
	ActorID    int64
	TargetType string // InteractiveTargetPost / InteractiveTargetComment
	TargetID   int64
	At         time.Time
}

// FollowInput 关注/取关。消费者：agent（follow/unfollow 动作）。
type FollowInput struct {
	ActorID  int64
	TargetID int64
	At       time.Time
}

// RepostInput 纯转发/取消转发。消费者：agent（repost 动作）。
type RepostInput struct {
	ActorID int64
	PostID  int64
	At      time.Time
}

// CommentBrief 是评论的上下文投影（作者展示名 + 纯文本），供 agent 组装 prompt。
// 消费者：agent（loadThread）。
type CommentBrief struct {
	ID         int64
	AuthorName string
	Content    string
}

// CommentListQuery 读取公开帖子最近讨论。消费者：agent（loadThread）。
type CommentListQuery struct {
	PostID int64
	Limit  int
}

// ReplyQuery 按作者分页读取回复（个人主页 replies 页签）。
// 消费者：content（userTimelineReplies 需要 comment 行 + 所属帖子）。
type ReplyQuery struct {
	AuthorID int64
	AfterAt  time.Time
	AfterID  int64
	Limit    int
}

// RepostTimelineQuery 读取纯转发事件，供时间线合并分支使用。
// 消费者：content（全站/关注/个人主页时间线中的 repost 条目）。
type RepostTimelineQuery struct {
	AuthorID     int64   // >0 时只看该用户的转发
	FollowingIDs []int64 // 非空时只看这些用户（关注时间线）
	Filter       string  // all / human / ai（作者类型过滤）
	AfterAt      time.Time
	AfterID      int64
	Limit        int
}

// RepostEvent 是一条生效中的纯转发事件。消费者：content（时间线合并）。
type RepostEvent struct {
	ID        int64
	UserID    int64
	PostID    int64
	CreatedAt time.Time
}

// PostViewerStates 是访问者对一批帖子的互动状态。消费者：content（列表 viewer_state）。
type PostViewerStates struct {
	Liked    map[int64]bool
	Reposted map[int64]bool
}

// LikedPostRef 是 AI 已点赞且仍公开可用的帖子关系。消费者：agent（可取消关系上下文）。
type LikedPostRef struct {
	PostID  int64
	Summary string
}

// FolloweeRef 是 AI 已关注的用户关系。消费者：agent（可取消关系上下文）。
type FolloweeRef struct {
	UserID   int64
	Username string
	Nickname string
}

// CommentTriggerQuery 读取某作者公开帖子下的新评论（AI 被动回复触发）。
// 消费者：agent（scanCommentTriggers 兜底扫描）。
type CommentTriggerQuery struct {
	AuthorID int64
	// ExcludeAuthorID > 0 时排除该作者自己发表的评论：AI 回复用户评论后，
	// 自己在自己帖子下的评论不应再次触发“收到评论”任务（旧实现 SQL 的 `author_id <> $1`）。
	// 消费者应传 rt.AIID。
	ExcludeAuthorID int64
	Since           time.Time
	Limit           int
}

// RelationReader 是 social 模块发布的最小关系读取能力。
// 消费者：content（时间线可见性过滤/作者关注状态）、notice（通知预览可见性）、agent
// （follow/unfollow 前的关系判断）。实现方：modules/social/app。
type RelationReader interface {
	// IsFollowing 判断 followerID 是否对 followeeID 存在生效关注关系。
	IsFollowing(ctx context.Context, followerID, followeeID int64) (bool, error)

	// FollowingSet 一次性载入访问者的全部生效关注对象，供列表/时间线批量过滤。
	FollowingSet(ctx context.Context, followerID int64) (map[int64]bool, error)

	// FollowState 返回访问者对目标用户的关系状态（model.FollowState* 四态）。
	FollowState(ctx context.Context, viewerID, targetID int64) (string, error)
}

// SocialService 是 social 模块对外发布的全部跨模块能力。
//
// 消费者：agent、admin、content、notice。
// 实现方：modules/social/app 的 App 类型（组合根注入）。
type SocialService interface {
	// RelationReader 关系读取（content/notice/agent 共用同一实现）。
	RelationReader

	// ── 写路径（以指定作者身份执行，调用方已鉴权） ──

	// CreateCommentForActor 发表评论或回复（含计数、通知、提及）。消费者：agent。
	CreateCommentForActor(ctx context.Context, in ActorCommentInput) (model.Comment, error)

	// DeleteCommentAsAdmin 管理员逻辑删除评论（含通知/提及失效与审计）。
	// 消费者：admin。
	DeleteCommentAsAdmin(ctx context.Context, in DeleteCommentInput) error

	// LikeForActor 点赞；返回 (是否生效, 最新计数)。消费者：agent。
	LikeForActor(ctx context.Context, in LikeInput) (active bool, count int, err error)
	// UnlikeForActor 取消点赞；返回 (是否仍生效=false, 最新计数)。消费者：agent。
	UnlikeForActor(ctx context.Context, in LikeInput) (active bool, count int, err error)

	// FollowForActor 关注（含审批/通知），返回 model.FollowState*。消费者：agent。
	FollowForActor(ctx context.Context, in FollowInput) (state string, err error)
	// UnfollowForActor 取关，返回 model.FollowState*。消费者：agent。
	UnfollowForActor(ctx context.Context, in FollowInput) (state string, err error)

	// RepostForActor 纯转发；返回 (是否生效, 最新计数)。消费者：agent。
	RepostForActor(ctx context.Context, in RepostInput) (active bool, count int, err error)
	// UnrepostForActor 取消转发。消费者：agent。
	UnrepostForActor(ctx context.Context, in RepostInput) (active bool, count int, err error)

	// ── 读路径 ──

	// GetComment 按 ID 读取评论（不过滤状态）；不存在返回领域 NotFound。
	// 消费者：agent（触发来源上下文）。
	GetComment(ctx context.Context, commentID int64) (model.Comment, error)

	// GetComments 批量读取评论（通知预览使用；已删除评论由调用方按状态处理）。
	// 消费者：notice。
	GetComments(ctx context.Context, commentIDs []int64) (map[int64]model.Comment, error)

	// CommentDepth 返回评论在楼中楼中的深度（回复深度限制）。消费者：agent。
	CommentDepth(ctx context.Context, commentID int64) (int, error)

	// ListRecentComments 读取公开帖子的最近讨论（作者展示名 + 纯文本）。
	// 消费者：agent（被提及/收到评论时的上下文）。
	ListRecentComments(ctx context.Context, query CommentListQuery) ([]CommentBrief, error)

	// ListUserReplies 按作者分页读取回复评论；可见性由调用方结合帖子再过滤。
	// 消费者：content（个人主页 replies 页签）。
	ListUserReplies(ctx context.Context, query ReplyQuery) ([]model.Comment, error)

	// ListNewCommentsOnAuthorPosts 读取某作者公开帖子下、指定时间后的新评论。
	// 消费者：agent（被动回复触发扫描）。
	ListNewCommentsOnAuthorPosts(ctx context.Context, query CommentTriggerQuery) ([]model.Comment, error)

	// IsPostLiked 判断用户是否已点赞帖子（动作幂等核对）。消费者：agent。
	IsPostLiked(ctx context.Context, userID, postID int64) (bool, error)
	// IsCommentLiked 判断用户是否已点赞评论。消费者：agent。
	IsCommentLiked(ctx context.Context, userID, commentID int64) (bool, error)
	// IsReposted 判断用户是否已转发帖子。消费者：agent。
	IsReposted(ctx context.Context, userID, postID int64) (bool, error)

	// ViewerPostStates 批量读取访问者对帖子的点赞/转发状态。消费者：content（列表 viewer_state）。
	ViewerPostStates(ctx context.Context, userID int64, postIDs []int64) (PostViewerStates, error)

	// ListRecentLikedPosts 读取用户最近点赞且仍公开可用的帖子。消费者：agent（关系上下文）。
	ListRecentLikedPosts(ctx context.Context, userID int64, limit int) ([]LikedPostRef, error)
	// ListRecentFollowees 读取用户最近关注的用户。消费者：agent（关系上下文）。
	ListRecentFollowees(ctx context.Context, userID int64, limit int) ([]FolloweeRef, error)

	// ListTimelineReposts 读取生效中的纯转发事件，供 content 合并时间线。
	// 消费者：content（时间线 repost 分支）。
	ListTimelineReposts(ctx context.Context, query RepostTimelineQuery) ([]RepostEvent, error)
}
