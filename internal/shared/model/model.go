// Package model 定义目标架构中跨上下文复用的数据形状与枚举。
// 只放「行结构 + 与权限无关的纯方法」：SQL 列清单、Scan、事务与 HTTP 都不在这里；
// JSON 响应形状在 shared/view 或各 modules/*/http/view.go，本包不带 JSON tag。
package model

import "time"

// 账号角色（与 users.role 的 CHECK 约束一致）。超级管理员 = RoleAdmin + IsSuperAdmin。
const (
	RoleUser  = "user"
	RoleAdmin = "admin"
)

// 账号状态（与 users.status 的 CHECK 约束一致）。
const (
	StatusActive    = "active"
	StatusSuspended = "suspended"
)

// 帖子状态与可见性（与 migrations 中的 CHECK 约束一致）。
const (
	PostStatusDraft     = "draft"
	PostStatusScheduled = "scheduled"
	PostStatusPublished = "published"
	PostStatusDeleted   = "deleted"

	VisibilityPublic    = "public"
	VisibilityFollowers = "followers"
	VisibilityPrivate   = "private"
)

// 评论状态。
const (
	CommentStatusPublished = "published"
	CommentStatusDeleted   = "deleted"
)

// 附件与衍生文件状态。
const (
	AttachmentAvailable = "available"
	AttachmentDeleted   = "deleted"

	DerivativeThumbnail  = "thumbnail"
	DerivativeVideoCover = "video_cover"

	DerivativePending    = "pending"
	DerivativeProcessing = "processing"
	DerivativeReady      = "ready"
	DerivativeFailed     = "failed"
	DerivativeCanceled   = "canceled"
)

// 关注申请状态。
const (
	FollowRequestPending  = "pending"
	FollowRequestApproved = "approved"
	FollowRequestRejected = "rejected"
	FollowRequestCanceled = "canceled"
)

// 关注状态（面向视图的四态）。
const (
	FollowStateNone            = "none"
	FollowStateFollowing       = "following"
	FollowStatePending         = "pending"
	FollowStateIncomingPending = "incoming_pending"
)

// 私信接收策略（与 users.dm_policy 的 CHECK 约束一致）。
const (
	DMPolicyAll       = "all"
	DMPolicyFollowing = "following"
	DMPolicyMutual    = "mutual"
	DMPolicyNone      = "none"
)

// 私信消息状态（与 dm_messages.status 的 CHECK 约束一致）。
const (
	DMMessageStatusPublished = "published"
	DMMessageStatusRecalled  = "recalled"
)

// 通知类型（与 notices.type 的 CHECK 约束一致）。
const (
	NoticeTypePostLike      = "post_like"
	NoticeTypeCommentLike   = "comment_like"
	NoticeTypeComment       = "comment"
	NoticeTypeReply         = "reply"
	NoticeTypeRepost        = "repost"
	NoticeTypeQuote         = "quote"
	NoticeTypeFollow        = "follow"
	NoticeTypeFollowRequest = "follow_request"
	NoticeTypeFollowApprove = "follow_approved"
	NoticeTypeMention       = "mention"
)

// 通知来源类型（提及的来源对象）。
const (
	NoticeSourcePost    = "post"
	NoticeSourceComment = "comment"
)

// 通知目标类型（notifications.target_type，与 CHECK 约束一致）。
const (
	NoticeTargetPost    = "post"
	NoticeTargetComment = "comment"
	NoticeTargetUser    = "user"
)

// User 是账号行结构。PasswordHash 永远不会直接序列化到响应。
type User struct {
	ID                    int64
	Username              string
	Email                 string
	PasswordHash          string
	Nickname              string
	AvatarURL             string
	BannerURL             string
	Bio                   string
	Role                  string
	IsSuperAdmin          bool
	IsAI                  bool
	Status                string
	RequireFollowApproval bool
	DMPolicy              string
	TokenVersion          int64
	SuspendedAt           *time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// IsSuspended 表示账号已被停用。
func (u User) IsSuspended() bool { return u.Status == StatusSuspended }

// IsActive 表示账号处于正常状态。
func (u User) IsActive() bool { return u.Status == StatusActive }

// IsAdmin 表示账号拥有管理员角色（AI 账号不通过角色获得管理权限）。
func (u User) IsAdmin() bool { return u.Role == RoleAdmin }

// IsHuman 表示账号不是 AI 身份。
func (u User) IsHuman() bool { return !u.IsAI }

// Session 是服务端会话行结构。
type Session struct {
	ID           int64
	UserID       int64
	CSRFToken    string
	TokenVersion int64
	ExpiresAt    time.Time
	RevokedAt    *time.Time
}

// Revoked 表示会话已被显式吊销。
func (s Session) Revoked() bool { return s.RevokedAt != nil }

// Viewer 是一次请求的访问者身份。未登录时为零值（游客）。
type Viewer struct {
	ID            int64
	IsAdmin       bool
	IsSuperAdmin  bool
	IsAI          bool
	IsSuspended   bool
	Authenticated bool
}

// ViewerFrom 把登录账号映射为访问者；未登录使用 GuestViewer。
func ViewerFrom(u User) Viewer {
	return Viewer{
		ID:            u.ID,
		IsAdmin:       u.IsAdmin() && !u.IsAI,
		IsSuperAdmin:  u.IsSuperAdmin && !u.IsAI,
		IsAI:          u.IsAI,
		IsSuspended:   u.IsSuspended(),
		Authenticated: true,
	}
}

// GuestViewer 返回游客访问者。
func GuestViewer() Viewer { return Viewer{} }

// IsGuest 表示本次请求没有登录身份。
func (v Viewer) IsGuest() bool { return !v.Authenticated }

// UserBrief 是多处复用的作者摘要。
type UserBrief struct {
	ID          int64
	Username    string
	Nickname    string
	AvatarURL   string
	IsAI        bool
	IsSuspended bool
}

// UserBriefFromUser 从账号行组装作者摘要。
func UserBriefFromUser(u User) UserBrief {
	return UserBrief{
		ID:          u.ID,
		Username:    u.Username,
		Nickname:    u.Nickname,
		AvatarURL:   u.AvatarURL,
		IsAI:        u.IsAI,
		IsSuspended: u.IsSuspended(),
	}
}

// Post 对应 timeline.posts。
type Post struct {
	ID               int64
	AuthorID         int64
	Status           string
	Visibility       string
	CurrentVersionID *int64
	QuotedPostID     *int64
	ScheduledAt      *time.Time
	PublishedAt      *time.Time
	DeletedAt        *time.Time
	LikeCount        int
	CommentCount     int
	RepostCount      int
	CreatedAt        time.Time
	UpdatedAt        time.Time
	// PinnedAt/PinnedSort 是个人主页置顶状态（0022）：PinnedAt 非空表示已置顶，
	// PinnedSort 是展示顺序（1 = 最前，1..3）；两列同空同非空由 CHECK 约束保证。
	PinnedAt   *time.Time
	PinnedSort *int
}

// IsPinned 判断帖子当前是否处于置顶状态。
func (p Post) IsPinned() bool { return p.PinnedAt != nil }

// SelfPosted 判断访问者是否为该帖作者。
func (p Post) SelfPosted(viewer Viewer) bool {
	return viewer.Authenticated && viewer.ID == p.AuthorID
}

// PostVersion 对应 timeline.post_versions，只追加不修改。
type PostVersion struct {
	ID                    int64
	PostID                int64
	VersionNumber         int
	ContentFormat         string
	ContentSource         string
	ContentHTML           string
	PlainText             string
	Visibility            string
	RestoredFromVersionID *int64
	CreatedBy             int64
	CreatedAt             time.Time
}

// Comment 对应 timeline.comments。
type Comment struct {
	ID              int64
	PostID          int64
	AuthorID        int64
	ParentCommentID *int64
	RootCommentID   *int64
	ContentFormat   string
	ContentSource   string
	ContentHTML     string
	PlainText       string
	Status          string
	DeletedByAdmin  bool
	DeletedByAuthor bool
	LikeCount       int
	ReplyCount      int
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Deleted 表示评论已逻辑删除。
func (c Comment) Deleted() bool { return c.Status == CommentStatusDeleted }

// Attachment 对应 timeline.attachments。物理文件永不删除，status 只做逻辑失效。
type Attachment struct {
	ID               int64
	UploaderID       int64
	PostID           *int64
	Kind             string
	OriginalFilename string
	MediaType        string
	SizeBytes        int64
	StorageKey       string
	Status           string
	Width            *int
	Height           *int
	DurationMS       *int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Available 表示附件仍然有效。
func (a Attachment) Available() bool { return a.Status == AttachmentAvailable }

// Notification 对应 timeline.notifications。类型与目标类型取值见 NoticeType*/NoticeTarget* 常量。
type Notification struct {
	ID             int64
	RecipientID    int64
	ActorID        int64
	Type           string
	TargetType     string
	TargetID       int64
	PostID         *int64
	CommentID      *int64
	IsRead         bool
	IdempotencyKey string
	CreatedAt      time.Time
}

// DMConversation 对应 timeline.dm_conversations。双方按 UserAID < UserBID 规范化；
// LastMessageID 为空表示空会话，同时兼作列表排序键。
type DMConversation struct {
	ID            int64
	UserAID       int64
	UserBID       int64
	LastMessageID *int64
	UserAHiddenAt *time.Time
	UserBHiddenAt *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// PeerIDFor 返回会话中 userID 的对方 ID；非会话成员返回 false。
func (c DMConversation) PeerIDFor(userID int64) (int64, bool) {
	switch userID {
	case c.UserAID:
		return c.UserBID, true
	case c.UserBID:
		return c.UserAID, true
	default:
		return 0, false
	}
}

// HiddenFor 表示 userID 已删除自己的会话视图。
func (c DMConversation) HiddenFor(userID int64) bool {
	switch userID {
	case c.UserAID:
		return c.UserAHiddenAt != nil
	case c.UserBID:
		return c.UserBHiddenAt != nil
	default:
		return false
	}
}

// DMMessage 对应 timeline.dm_messages。RecipientID 为会话另一侧，
// ReadAt 为空表示接收方未读；撤回只置 Status，正文列保留。
type DMMessage struct {
	ID             int64
	ConversationID int64
	SenderID       int64
	RecipientID    int64
	ContentFormat  string
	ContentSource  string
	ContentHTML    string
	PlainText      string
	Status         string
	ReadAt         *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Recalled 表示消息已被发送者撤回。
func (m DMMessage) Recalled() bool { return m.Status == DMMessageStatusRecalled }

// Read 表示接收方已读。
func (m DMMessage) Read() bool { return m.ReadAt != nil }
