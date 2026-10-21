// Package router 是唯一注册业务路由的地方：声明式路由表 + 授权策略组。
//
// 路由条目只声明「方法、路径、策略、处理器」；策略决定中间件链，避免逐条路由
// 手工拼 RequireAuth/RequireCSRF 造成遗漏。handler 以接口描述，模块 http 包
// 直接实现（路由包不 import 任何具体模块，便于无数据库单测）。
package router

import (
	"github.com/gin-gonic/gin"
)

// Readiness 表示运行时是否已就绪；*platform/db.Readiness 直接满足。
type Readiness interface {
	Ready() bool
}

// Deps 是组合根注入的全部 HTTP handler。
type Deps struct {
	Auth    AuthHandlers
	Content ContentHandlers
	Social  SocialHandlers
	Notice  NoticeHandlers
	DM      DMHandlers
	Media   MediaHandlers
	AI      AIHandlers
	Agent   AgentHandlers
	Admin   AdminHandlers
}

// AuthHandlers 覆盖认证域路由处理器与鉴权中间件。
type AuthHandlers interface {
	// 路由
	CSRF(c *gin.Context)
	Config(c *gin.Context)
	Register(c *gin.Context)
	Login(c *gin.Context)
	Me(c *gin.Context)
	Logout(c *gin.Context)
	ChangePassword(c *gin.Context)
	UpdateProfile(c *gin.Context)
	FollowSettings(c *gin.Context)
	UpdateFollowSettings(c *gin.Context)
	DMSettings(c *gin.Context)
	UpdateDMSettings(c *gin.Context)
	SearchUsers(c *gin.Context)
	GetProfile(c *gin.Context)
	GrantAdmin(c *gin.Context)
	RevokeAdmin(c *gin.Context)
	// 中间件
	ResolveSession(c *gin.Context)
	RequireLogin(c *gin.Context)
	RequireAdmin(c *gin.Context)
	RequireSuperAdmin(c *gin.Context)
	RequireCSRF(c *gin.Context)
	GuestCSRF(c *gin.Context)
}

// ContentHandlers 覆盖帖子/草稿/版本/时间线/标签/搜索与置顶。
type ContentHandlers interface {
	UserTimeline(c *gin.Context)
	GetPost(c *gin.Context)
	CreatePost(c *gin.Context)
	UpdatePost(c *gin.Context)
	PublishPost(c *gin.Context)
	SchedulePost(c *gin.Context)
	CancelSchedule(c *gin.Context)
	DeletePost(c *gin.Context)
	PinPost(c *gin.Context)
	UnpinPost(c *gin.Context)
	ReorderPinnedPosts(c *gin.Context)
	ListVersions(c *gin.Context)
	GetVersion(c *gin.Context)
	RestoreVersion(c *gin.Context)
	ListMyDrafts(c *gin.Context)
	ListMyScheduled(c *gin.Context)
	ListBookmarks(c *gin.Context)
	Timeline(c *gin.Context)
	FollowingTimeline(c *gin.Context)
	SuggestTags(c *gin.Context)
	TagTimeline(c *gin.Context)
	SearchPosts(c *gin.Context)
}

// SocialHandlers 覆盖关注/申请、点赞、评论、转发、收藏。
type SocialHandlers interface {
	ListFollowers(c *gin.Context)
	ListFollowing(c *gin.Context)
	Follow(c *gin.Context)
	Unfollow(c *gin.Context)
	RemoveFollower(c *gin.Context)
	ListFollowRequests(c *gin.Context)
	ApproveFollowRequest(c *gin.Context)
	RejectFollowRequest(c *gin.Context)
	CancelFollowRequest(c *gin.Context)
	Repost(c *gin.Context)
	Unrepost(c *gin.Context)
	LikePost(c *gin.Context)
	UnlikePost(c *gin.Context)
	BookmarkPost(c *gin.Context)
	UnbookmarkPost(c *gin.Context)
	ListBookmarkFolders(c *gin.Context)
	CreateBookmarkFolder(c *gin.Context)
	RenameBookmarkFolder(c *gin.Context)
	DeleteBookmarkFolder(c *gin.Context)
	LikeComment(c *gin.Context)
	UnlikeComment(c *gin.Context)
	ListComments(c *gin.Context)
	ListReplies(c *gin.Context)
	CreateComment(c *gin.Context)
	DeleteComment(c *gin.Context)
}

// NoticeHandlers 覆盖通知列表与已读。
type NoticeHandlers interface {
	List(c *gin.Context)
	UnreadCount(c *gin.Context)
	MarkAllRead(c *gin.Context)
	MarkRead(c *gin.Context)
}

// DMHandlers 覆盖 1:1 私信会话、消息、已读与撤回。
type DMHandlers interface {
	ListConversations(c *gin.Context)
	CreateConversation(c *gin.Context)
	ConversationDetail(c *gin.Context)
	MarkRead(c *gin.Context)
	HideConversation(c *gin.Context)
	UnreadCount(c *gin.Context)
	ListMessages(c *gin.Context)
	SendMessage(c *gin.Context)
	RecallMessage(c *gin.Context)
}

// MediaHandlers 覆盖附件上传、鉴权读取、衍生文件与删除。
type MediaHandlers interface {
	Upload(c *gin.Context)
	Content(c *gin.Context)
	Derivative(c *gin.Context)
	Delete(c *gin.Context)
}

// AIHandlers 覆盖模型预设、AI 身份/模型/行为与分层记忆。
type AIHandlers interface {
	ListProviders(c *gin.Context)
	CreateProvider(c *gin.Context)
	UpdateProvider(c *gin.Context)
	DeleteProvider(c *gin.Context)
	TestProvider(c *gin.Context)
	ListAIs(c *gin.Context)
	CreateAI(c *gin.Context)
	GetAI(c *gin.Context)
	UpdateAI(c *gin.Context)
	UpdateAIModel(c *gin.Context)
	UpdateAIBehavior(c *gin.Context)
	EnableAI(c *gin.Context)
	DisableAI(c *gin.Context)
	ListMemories(c *gin.Context)
	CreateMemory(c *gin.Context)
	ClearMemories(c *gin.Context)
	UpdateMemory(c *gin.Context)
	DeleteMemory(c *gin.Context)
}

// AgentHandlers 覆盖 AI 生成/草稿/任务/调用记录/全局开关。
type AgentHandlers interface {
	GlobalStart(c *gin.Context)
	GlobalStop(c *gin.Context)
	GenerateDraft(c *gin.Context)
	PublishDirect(c *gin.Context)
	ListAIDrafts(c *gin.Context)
	UpdateAIDraft(c *gin.Context)
	PublishAIDraft(c *gin.Context)
	ListAITasks(c *gin.Context)
	RetryAITask(c *gin.Context)
	CancelAITask(c *gin.Context)
	ListCallLogs(c *gin.Context)
	GetCallLog(c *gin.Context)
}

// AdminHandlers 覆盖概览、用户处置、内容审核、任务、审计与系统设置。
// 管理员授权/撤销在 AuthHandlers（属于 auth 域）。
type AdminHandlers interface {
	GetOverview(c *gin.Context)
	ListUsers(c *gin.Context)
	GetUser(c *gin.Context)
	SuspendUser(c *gin.Context)
	UnsuspendUser(c *gin.Context)
	ResetPassword(c *gin.Context)
	ListPosts(c *gin.Context)
	GetPost(c *gin.Context)
	DeletePost(c *gin.Context)
	ListComments(c *gin.Context)
	DeleteComment(c *gin.Context)
	ListAttachments(c *gin.Context)
	DeleteAttachment(c *gin.Context)
	ListTasks(c *gin.Context)
	RetryTask(c *gin.Context)
	CancelTask(c *gin.Context)
	ListAuditLogs(c *gin.Context)
	GetSettings(c *gin.Context)
	UpdateSettings(c *gin.Context)
	GetWebSearchSettings(c *gin.Context)
	UpdateWebSearchSettings(c *gin.Context)
}
