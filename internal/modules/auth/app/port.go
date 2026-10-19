package app

import (
	"context"
	"time"

	"github.com/great-magician-01/timeline/internal/shared/model"
)

// UserRepo 是 auth app 依赖的账号仓储端口（实现：modules/auth/repo.UserRepo）。
// 所有方法通过 platform/db.ExecutorFrom(ctx) 自动加入 app 编排的事务。
type UserRepo interface {
	Insert(ctx context.Context, user model.User) error
	ByID(ctx context.Context, id int64) (model.User, error)
	ByIDForUpdate(ctx context.Context, id int64) (model.User, error)
	ByUsername(ctx context.Context, username string) (model.User, error)
	ByLoginIdentifier(ctx context.Context, identifier string) (model.User, error)
	UpdateProfile(ctx context.Context, id int64, nickname, bio, avatarURL *string, at time.Time) (model.User, error)
	UpdateProfileFields(ctx context.Context, id int64, username, nickname, bio *string, at time.Time) (model.User, error)
	UpdateFollowApproval(ctx context.Context, id int64, enabled bool, at time.Time) (bool, error)
	UpdateDMPolicy(ctx context.Context, id int64, policy string, at time.Time) (string, error)
	UpdateRole(ctx context.Context, id int64, role string, at time.Time) (model.User, error)
	UpdateStatus(ctx context.Context, id int64, status string, suspendedAt *time.Time, tokenVersion int64, at time.Time) (model.User, error)
	UpdatePassword(ctx context.Context, id int64, passwordHash string, tokenVersion int64, at time.Time) error
	UpdateAvatar(ctx context.Context, id int64, attachmentID *int64, avatarURL string, at time.Time) error
	Search(ctx context.Context, keyword string, cursor int64, limit int) ([]model.User, error)
	// HasHumanUser 用 EXISTS 判断是否已有至少一个人类账号（初始化判定）。
	HasHumanUser(ctx context.Context) (bool, error)
	// CountHumanUsers 统计人类账号数；仅启动日志使用，运行期初始化判定用 HasHumanUser。
	CountHumanUsers(ctx context.Context) (int64, error)
	CountSuperAdmins(ctx context.Context) (int64, error)
	GetUsers(ctx context.Context, ids []int64) (map[int64]model.User, error)
	GetUserBriefs(ctx context.Context, ids []int64) (map[int64]model.UserBrief, error)
	FindActiveUsersByUsernames(ctx context.Context, usernames []string) ([]model.UserBrief, error)
	ListUserIDsByAIFlag(ctx context.Context, isAI bool) ([]int64, error)
	ListAvatarAttachmentIDs(ctx context.Context) ([]int64, error)
}

// SessionRepo 是 auth app 依赖的会话仓储端口（实现：modules/auth/repo.SessionRepo）。
type SessionRepo interface {
	Insert(ctx context.Context, session model.Session, tokenHash string, createdAt time.Time) error
	ByTokenHash(ctx context.Context, tokenHash string) (model.User, model.Session, error)
	Revoke(ctx context.Context, sessionID int64, at time.Time) error
	RevokeAllForUserExcept(ctx context.Context, userID, exceptSessionID int64, at time.Time) error
	RevokeAllForUser(ctx context.Context, userID int64, at time.Time) error
	UpdateTokenVersion(ctx context.Context, sessionID, tokenVersion int64) error
}

// BootstrapLocker 提供首注册初始化的 advisory lock（实现：repo.LockRepo）。
type BootstrapLocker interface {
	AcquireBootstrapLock(ctx context.Context) error
}

// TxManager 是 app 层唯一的事务入口（实现：platform/db.TxManager）。
type TxManager interface {
	WithTx(ctx context.Context, fn func(context.Context) error) error
}

// IDGenerator 生成雪花 ID（实现：platform/id.Generator）。
type IDGenerator interface {
	Next() (int64, error)
}

// ProfileStats 是个人主页真正消费的三项统计（读模型，由组合根注入的端口提供）。
//
// 公开个人主页响应只有 post_count/following_count/follower_count 三个字段
// （modules/auth/http/view.go 的 profileResponse）；评论/点赞/转发项前端不消费，
// 因此不在本契约内计算。三项分别命中 posts_author_published、follows_follower_active、
// follows_followee_active 三个索引，不再是跨表多子查询聚合。
type ProfileStats struct {
	Posts     int64
	Followers int64
	Following int64
}

// ProfileStatsReader 读取个人主页统计；由组合根以只读适配器提供
// （缺失时 GetProfile 返回零值，不阻塞主页资料）。
type ProfileStatsReader interface {
	ProfileStats(ctx context.Context, userID int64) (ProfileStats, error)
}
