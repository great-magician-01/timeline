package contracts

import (
	"context"
	"time"

	"github.com/great-magician-01/timeline/internal/shared/model"
)

// AvatarUpdateInput 更新用户头像（users 表由 auth 模块拥有，media 不得直接 UPDATE）。
// 消费者：media（上传头像后回写、SetAvatar）。
type AvatarUpdateInput struct {
	UserID       int64
	AttachmentID int64
	AvatarURL    string
	At           time.Time
}

// UserStatusInput 停用/启用账号；实现方在调用方事务内更新 users 并吊销会话。
// 消费者：admin（用户管理）。
type UserStatusInput struct {
	ActorID  int64 // 执行处置的管理员
	TargetID int64 // 被处置账号
	Suspend  bool  // true=停用，false=启用
	At       time.Time
}

// ResetPasswordInput 管理员重置密码；密码校验/哈希与会话吊销由 auth 实现。
// 消费者：admin。
type ResetPasswordInput struct {
	ActorID     int64
	TargetID    int64
	NewPassword string // 明文只在内存中传递，auth 负责校验与哈希
	At          time.Time
}

// AdminRoleInput 授权/撤销管理员；超级管理员保护等不变式由 auth 领域实现。
// 消费者：admin（若授权接口继续由 admin 路由承载；目标设计中接口在 auth/http）。
type AdminRoleInput struct {
	ActorID  int64
	TargetID int64
	Grant    bool // true=授予管理员，false=撤销管理员
	At       time.Time
}

// UserReader 是 auth 模块发布的最小账号读取能力。
//
// 消费者：content（作者/停用/AI 过滤）、social（用户名解析、作者摘要、停用判断）、
// notice（提及解析）、media（清理头像引用）、agent（actor/作者信息）、ai（运行时用户信息）、
// admin（用户详情，只读部分也可由 admin read-model 承担）。
type UserReader interface {
	// GetUser 按 ID 读取完整账号行（含 is_ai/status/role/超管标记）；不存在返回领域 NotFound。
	GetUser(ctx context.Context, userID int64) (model.User, error)
	// GetUserByUsername 按用户名读取账号行；不存在返回领域 NotFound。
	GetUserByUsername(ctx context.Context, username string) (model.User, error)
	// GetUsers 批量读取账号行，返回 id → User；缺失 ID 不在 map 中。
	GetUsers(ctx context.Context, userIDs []int64) (map[int64]model.User, error)
	// GetUserBrief 读取单个作者摘要；不存在返回领域 NotFound。
	GetUserBrief(ctx context.Context, userID int64) (model.UserBrief, error)
	// GetUserBriefs 批量读取作者摘要，返回 id → UserBrief；缺失 ID 不在 map 中。
	GetUserBriefs(ctx context.Context, userIDs []int64) (map[int64]model.UserBrief, error)
	// BriefsByIDs 与 GetUserBriefs 同义（social 模块使用的命名）；新增方法不改旧签名。
	// 消费者：social、content。
	BriefsByIDs(ctx context.Context, userIDs []int64) (map[int64]model.UserBrief, error)
	// FindActiveUsersByUsernames 按用户名（大小写不敏感）解析生效账号，用于 @提及。
	FindActiveUsersByUsernames(ctx context.Context, usernames []string) ([]model.UserBrief, error)
	// IsSuspended 判断账号是否停用；账号不存在返回领域 NotFound。
	IsSuspended(ctx context.Context, userID int64) (bool, error)
	// ListUserIDsByAIFlag 列出全部（或全部 AI/人类）账号 ID，供时间线按作者类型过滤。
	ListUserIDsByAIFlag(ctx context.Context, isAI bool) ([]int64, error)
	// ListAvatarAttachmentIDs 列出全部用户当前头像引用的附件 ID（媒体清理防误删）。
	ListAvatarAttachmentIDs(ctx context.Context) ([]int64, error)
}

// CreateAIUserInput 创建一个 AI 身份账号（users.is_ai=true，role=user，无密码）。
// 消费者：ai（AI 身份创建）、admin（如仍由管理后台入口创建）。
type CreateAIUserInput struct {
	Username string
	Nickname string
	Bio      string
	At       time.Time
}

// UpdateUserProfileInput 更新账号资料字段（ai 更新 AI 账号、auth 自身资料修改）。
// 消费者：ai（AI 身份 username/nickname/bio 更新）。
type UpdateUserProfileInput struct {
	UserID   int64
	Username *string
	Nickname *string
	Bio      *string
	At       time.Time
}

// UserWriter 是 auth 模块发布的账号写能力；所有写操作必须在调用方事务内执行。
//
// 消费者：media（头像）、admin（用户处置与角色管理）、ai（AI 账号创建/资料更新）。
type UserWriter interface {
	// CreateAIUser 创建 AI 身份账号；username 冲突返回领域 Conflict。
	CreateAIUser(ctx context.Context, in CreateAIUserInput) (model.User, error)
	// UpdateUserProfile 更新资料字段（指针为 nil 表示不修改）；auth 负责唯一性与校验。
	UpdateUserProfile(ctx context.Context, in UpdateUserProfileInput) error
	// SetAvatar 更新头像引用；失败时返回领域错误。
	SetAvatar(ctx context.Context, in AvatarUpdateInput) error
	// SetUserStatus 停用/启用账号并吊销/保留会话，返回更新后的账号行。
	SetUserStatus(ctx context.Context, in UserStatusInput) (model.User, error)
	// ResetPassword 重置密码并吊销会话。
	ResetPassword(ctx context.Context, in ResetPasswordInput) error
	// ChangeAdminRole 授予/撤销管理员，返回更新后的账号行；幂等场景不写审计。
	ChangeAdminRole(ctx context.Context, in AdminRoleInput) (model.User, error)
}

// AuthService 是 auth 模块对外的完整发布接口（UserReader + UserWriter）。
//
// 消费者：admin（角色/状态/密码）、media（头像）、content/social/notice/agent/ai（只读）。
// 实现方：modules/auth/app 的 App 类型（组合根注入）；只读消费者应依赖 UserReader 窄接口。
type AuthService interface {
	UserReader
	UserWriter
}

// AuthUserWriter 是 UserWriter 的别名，便于按「auth 用户的写端口」语义引用。
type AuthUserWriter = UserWriter
