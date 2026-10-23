package router

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Route 是一条声明式路由：方法、路径、策略与处理器选择器。
// 处理器通过 Deps 选择，路由表本身不依赖任何具体模块；策略由 policies.go 组装。
type Route struct {
	Method  string
	Path    string
	Policy  Policy
	Handler func(Deps) gin.HandlerFunc
}

// Routes 返回全部业务路由（133 条，基线 b6fed97 + 私信 11 条 + 置顶 3 条 + 收藏 7 条
// + 主页横幅移除 1 条 + 实时事件流 1 条；新增业务接口时同步维护并更新 routes_test.go
// 的数量断言）。
func Routes() []Route {
	return []Route{
		// ---------- 公开 / 可选会话 ----------
		{http.MethodGet, "/auth/config", Public, func(d Deps) gin.HandlerFunc { return d.Auth.Config }},
		{http.MethodGet, "/auth/csrf", Optional, func(d Deps) gin.HandlerFunc { return d.Auth.CSRF }},

		// ---------- 游客写（双提交 CSRF） ----------
		{http.MethodPost, "/auth/register", GuestWrite, func(d Deps) gin.HandlerFunc { return d.Auth.Register }},
		{http.MethodPost, "/auth/login", GuestWrite, func(d Deps) gin.HandlerFunc { return d.Auth.Login }},

		// ---------- 已登录只读 ----------
		{http.MethodGet, "/auth/me", Login, func(d Deps) gin.HandlerFunc { return d.Auth.Me }},
		{http.MethodGet, "/users/me/follow-settings", Login, func(d Deps) gin.HandlerFunc { return d.Auth.FollowSettings }},
		{http.MethodGet, "/users/me/dm-settings", Login, func(d Deps) gin.HandlerFunc { return d.Auth.DMSettings }},
		{http.MethodGet, "/follow-requests", Login, func(d Deps) gin.HandlerFunc { return d.Social.ListFollowRequests }},
		{http.MethodGet, "/me/drafts", Login, func(d Deps) gin.HandlerFunc { return d.Content.ListMyDrafts }},
		{http.MethodGet, "/me/scheduled", Login, func(d Deps) gin.HandlerFunc { return d.Content.ListMyScheduled }},
		{http.MethodGet, "/me/bookmarks", Login, func(d Deps) gin.HandlerFunc { return d.Content.ListBookmarks }},
		{http.MethodGet, "/me/bookmark-folders", Login, func(d Deps) gin.HandlerFunc { return d.Social.ListBookmarkFolders }},
		{http.MethodGet, "/timeline/following", Login, func(d Deps) gin.HandlerFunc { return d.Content.FollowingTimeline }},
		{http.MethodGet, "/notifications", Login, func(d Deps) gin.HandlerFunc { return d.Notice.List }},
		{http.MethodGet, "/notifications/unread-count", Login, func(d Deps) gin.HandlerFunc { return d.Notice.UnreadCount }},
		{http.MethodGet, "/events", Login, func(d Deps) gin.HandlerFunc { return d.Realtime.Events }},
		{http.MethodGet, "/dm/unread-count", Login, func(d Deps) gin.HandlerFunc { return d.DM.UnreadCount }},
		{http.MethodGet, "/dm/conversations", Login, func(d Deps) gin.HandlerFunc { return d.DM.ListConversations }},
		{http.MethodGet, "/dm/conversations/:id", Login, func(d Deps) gin.HandlerFunc { return d.DM.ConversationDetail }},
		{http.MethodGet, "/dm/conversations/:id/messages", Login, func(d Deps) gin.HandlerFunc { return d.DM.ListMessages }},

		// ---------- 已登录写（登录 + CSRF） ----------
		{http.MethodPost, "/auth/logout", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Auth.Logout }},
		{http.MethodPost, "/auth/password", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Auth.ChangePassword }},
		{http.MethodPost, "/users/me", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Auth.UpdateProfile }},
		{http.MethodPost, "/users/me/banner/delete", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Auth.DeleteBanner }},
		{http.MethodPost, "/users/me/follow-settings", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Auth.UpdateFollowSettings }},
		{http.MethodPost, "/users/me/dm-settings", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Auth.UpdateDMSettings }},
		{http.MethodPost, "/users/:username/follow", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Social.Follow }},
		{http.MethodPost, "/users/:username/unfollow", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Social.Unfollow }},
		{http.MethodPost, "/users/me/followers/:username/remove", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Social.RemoveFollower }},
		{http.MethodPost, "/follow-requests/:id/approve", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Social.ApproveFollowRequest }},
		{http.MethodPost, "/follow-requests/:id/reject", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Social.RejectFollowRequest }},
		{http.MethodPost, "/follow-requests/:id/cancel", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Social.CancelFollowRequest }},
		{http.MethodPost, "/posts", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Content.CreatePost }},
		{http.MethodPost, "/posts/pinned/reorder", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Content.ReorderPinnedPosts }},
		{http.MethodPost, "/posts/:id", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Content.UpdatePost }},
		{http.MethodPost, "/posts/:id/publish", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Content.PublishPost }},
		{http.MethodPost, "/posts/:id/schedule", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Content.SchedulePost }},
		{http.MethodPost, "/posts/:id/cancel-schedule", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Content.CancelSchedule }},
		{http.MethodPost, "/posts/:id/delete", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Content.DeletePost }},
		{http.MethodPost, "/posts/:id/pin", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Content.PinPost }},
		{http.MethodPost, "/posts/:id/unpin", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Content.UnpinPost }},
		{http.MethodPost, "/posts/:id/versions/:version_id/restore", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Content.RestoreVersion }},
		{http.MethodPost, "/posts/:id/repost", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Social.Repost }},
		{http.MethodPost, "/posts/:id/unrepost", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Social.Unrepost }},
		{http.MethodPost, "/posts/:id/like", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Social.LikePost }},
		{http.MethodPost, "/posts/:id/unlike", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Social.UnlikePost }},
		{http.MethodPost, "/posts/:id/bookmark", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Social.BookmarkPost }},
		{http.MethodPost, "/posts/:id/unbookmark", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Social.UnbookmarkPost }},
		{http.MethodPost, "/comments/:id/like", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Social.LikeComment }},
		{http.MethodPost, "/comments/:id/unlike", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Social.UnlikeComment }},
		{http.MethodPost, "/posts/:id/comments", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Social.CreateComment }},
		{http.MethodPost, "/comments/:id/delete", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Social.DeleteComment }},
		{http.MethodPost, "/notifications/read-all", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Notice.MarkAllRead }},
		{http.MethodPost, "/notifications/:id/read", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Notice.MarkRead }},
		{http.MethodPost, "/dm/conversations", HumanWrite, func(d Deps) gin.HandlerFunc { return d.DM.CreateConversation }},
		{http.MethodPost, "/dm/conversations/:id/messages", HumanWrite, func(d Deps) gin.HandlerFunc { return d.DM.SendMessage }},
		{http.MethodPost, "/dm/conversations/:id/read", HumanWrite, func(d Deps) gin.HandlerFunc { return d.DM.MarkRead }},
		{http.MethodPost, "/dm/conversations/:id/hide", HumanWrite, func(d Deps) gin.HandlerFunc { return d.DM.HideConversation }},
		{http.MethodPost, "/dm/messages/:id/recall", HumanWrite, func(d Deps) gin.HandlerFunc { return d.DM.RecallMessage }},
		{http.MethodPost, "/me/bookmark-folders", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Social.CreateBookmarkFolder }},
		{http.MethodPost, "/me/bookmark-folders/:id", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Social.RenameBookmarkFolder }},
		{http.MethodPost, "/me/bookmark-folders/:id/delete", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Social.DeleteBookmarkFolder }},
		{http.MethodPost, "/attachments", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Media.Upload }},
		{http.MethodPost, "/attachments/:id/delete", HumanWrite, func(d Deps) gin.HandlerFunc { return d.Media.Delete }},

		// ---------- 公开读（可选会话获得 viewer_state） ----------
		{http.MethodGet, "/users/search", Optional, func(d Deps) gin.HandlerFunc { return d.Auth.SearchUsers }},
		{http.MethodGet, "/users/:username", Optional, func(d Deps) gin.HandlerFunc { return d.Auth.GetProfile }},
		{http.MethodGet, "/users/:username/posts", Optional, func(d Deps) gin.HandlerFunc { return d.Content.UserTimeline }},
		{http.MethodGet, "/users/:username/followers", Optional, func(d Deps) gin.HandlerFunc { return d.Social.ListFollowers }},
		{http.MethodGet, "/users/:username/following", Optional, func(d Deps) gin.HandlerFunc { return d.Social.ListFollowing }},
		{http.MethodGet, "/posts/:id", Optional, func(d Deps) gin.HandlerFunc { return d.Content.GetPost }},
		{http.MethodGet, "/posts/:id/versions", Optional, func(d Deps) gin.HandlerFunc { return d.Content.ListVersions }},
		{http.MethodGet, "/posts/:id/versions/:version_id", Optional, func(d Deps) gin.HandlerFunc { return d.Content.GetVersion }},
		{http.MethodGet, "/timeline", Optional, func(d Deps) gin.HandlerFunc { return d.Content.Timeline }},
		{http.MethodGet, "/tags/suggest", Optional, func(d Deps) gin.HandlerFunc { return d.Content.SuggestTags }},
		{http.MethodGet, "/tags/:name/posts", Optional, func(d Deps) gin.HandlerFunc { return d.Content.TagTimeline }},
		{http.MethodGet, "/search/posts", Optional, func(d Deps) gin.HandlerFunc { return d.Content.SearchPosts }},
		{http.MethodGet, "/posts/:id/comments", Optional, func(d Deps) gin.HandlerFunc { return d.Social.ListComments }},
		{http.MethodGet, "/comments/:id/replies", Optional, func(d Deps) gin.HandlerFunc { return d.Social.ListReplies }},
		{http.MethodGet, "/attachments/:id/content", Optional, func(d Deps) gin.HandlerFunc { return d.Media.Content }},
		{http.MethodGet, "/attachments/:id/derivatives/:type", Optional, func(d Deps) gin.HandlerFunc { return d.Media.Derivative }},

		// ---------- 管理员只读 ----------
		{http.MethodGet, "/admin/overview", AdminRead, func(d Deps) gin.HandlerFunc { return d.Admin.GetOverview }},
		{http.MethodGet, "/admin/users", AdminRead, func(d Deps) gin.HandlerFunc { return d.Admin.ListUsers }},
		{http.MethodGet, "/admin/users/:id", AdminRead, func(d Deps) gin.HandlerFunc { return d.Admin.GetUser }},
		{http.MethodGet, "/admin/posts", AdminRead, func(d Deps) gin.HandlerFunc { return d.Admin.ListPosts }},
		{http.MethodGet, "/admin/posts/:id", AdminRead, func(d Deps) gin.HandlerFunc { return d.Admin.GetPost }},
		{http.MethodGet, "/admin/comments", AdminRead, func(d Deps) gin.HandlerFunc { return d.Admin.ListComments }},
		{http.MethodGet, "/admin/attachments", AdminRead, func(d Deps) gin.HandlerFunc { return d.Admin.ListAttachments }},
		{http.MethodGet, "/admin/providers", AdminRead, func(d Deps) gin.HandlerFunc { return d.AI.ListProviders }},
		{http.MethodGet, "/admin/ai", AdminRead, func(d Deps) gin.HandlerFunc { return d.AI.ListAIs }},
		{http.MethodGet, "/admin/ai/:id", AdminRead, func(d Deps) gin.HandlerFunc { return d.AI.GetAI }},
		{http.MethodGet, "/admin/ai/:id/memories", AdminRead, func(d Deps) gin.HandlerFunc { return d.AI.ListMemories }},
		{http.MethodGet, "/admin/ai/:id/drafts", AdminRead, func(d Deps) gin.HandlerFunc { return d.Agent.ListAIDrafts }},
		{http.MethodGet, "/admin/ai/:id/tasks", AdminRead, func(d Deps) gin.HandlerFunc { return d.Agent.ListAITasks }},
		{http.MethodGet, "/admin/ai-calls", AdminRead, func(d Deps) gin.HandlerFunc { return d.Agent.ListCallLogs }},
		{http.MethodGet, "/admin/ai-calls/:id", AdminRead, func(d Deps) gin.HandlerFunc { return d.Agent.GetCallLog }},
		{http.MethodGet, "/admin/tasks", AdminRead, func(d Deps) gin.HandlerFunc { return d.Admin.ListTasks }},
		{http.MethodGet, "/admin/audit-logs", AdminRead, func(d Deps) gin.HandlerFunc { return d.Admin.ListAuditLogs }},
		{http.MethodGet, "/admin/settings", AdminRead, func(d Deps) gin.HandlerFunc { return d.Admin.GetSettings }},
		{http.MethodGet, "/admin/settings/web-search", AdminRead, func(d Deps) gin.HandlerFunc { return d.Admin.GetWebSearchSettings }},

		// ---------- 管理员写 ----------
		{http.MethodPost, "/admin/users/:id/suspend", AdminWrite, func(d Deps) gin.HandlerFunc { return d.Admin.SuspendUser }},
		{http.MethodPost, "/admin/users/:id/unsuspend", AdminWrite, func(d Deps) gin.HandlerFunc { return d.Admin.UnsuspendUser }},
		{http.MethodPost, "/admin/users/:id/reset-password", AdminWrite, func(d Deps) gin.HandlerFunc { return d.Admin.ResetPassword }},
		{http.MethodPost, "/admin/posts/:id/delete", AdminWrite, func(d Deps) gin.HandlerFunc { return d.Admin.DeletePost }},
		{http.MethodPost, "/admin/comments/:id/delete", AdminWrite, func(d Deps) gin.HandlerFunc { return d.Admin.DeleteComment }},
		{http.MethodPost, "/admin/attachments/:id/delete", AdminWrite, func(d Deps) gin.HandlerFunc { return d.Admin.DeleteAttachment }},
		{http.MethodPost, "/admin/providers", AdminWrite, func(d Deps) gin.HandlerFunc { return d.AI.CreateProvider }},
		{http.MethodPost, "/admin/providers/:id", AdminWrite, func(d Deps) gin.HandlerFunc { return d.AI.UpdateProvider }},
		{http.MethodPost, "/admin/providers/:id/delete", AdminWrite, func(d Deps) gin.HandlerFunc { return d.AI.DeleteProvider }},
		{http.MethodPost, "/admin/providers/:id/test", AdminWrite, func(d Deps) gin.HandlerFunc { return d.AI.TestProvider }},
		{http.MethodPost, "/admin/ai", AdminWrite, func(d Deps) gin.HandlerFunc { return d.AI.CreateAI }},
		{http.MethodPost, "/admin/ai/global-start", AdminWrite, func(d Deps) gin.HandlerFunc { return d.Agent.GlobalStart }},
		{http.MethodPost, "/admin/ai/global-stop", AdminWrite, func(d Deps) gin.HandlerFunc { return d.Agent.GlobalStop }},
		{http.MethodPost, "/admin/ai/:id", AdminWrite, func(d Deps) gin.HandlerFunc { return d.AI.UpdateAI }},
		{http.MethodPost, "/admin/ai/:id/model", AdminWrite, func(d Deps) gin.HandlerFunc { return d.AI.UpdateAIModel }},
		{http.MethodPost, "/admin/ai/:id/behavior", AdminWrite, func(d Deps) gin.HandlerFunc { return d.AI.UpdateAIBehavior }},
		{http.MethodPost, "/admin/ai/:id/enable", AdminWrite, func(d Deps) gin.HandlerFunc { return d.AI.EnableAI }},
		{http.MethodPost, "/admin/ai/:id/disable", AdminWrite, func(d Deps) gin.HandlerFunc { return d.AI.DisableAI }},
		{http.MethodPost, "/admin/ai/:id/memories", AdminWrite, func(d Deps) gin.HandlerFunc { return d.AI.CreateMemory }},
		{http.MethodPost, "/admin/ai/:id/memories/clear", AdminWrite, func(d Deps) gin.HandlerFunc { return d.AI.ClearMemories }},
		{http.MethodPost, "/admin/ai/:id/memories/:memory_id", AdminWrite, func(d Deps) gin.HandlerFunc { return d.AI.UpdateMemory }},
		{http.MethodPost, "/admin/ai/:id/memories/:memory_id/delete", AdminWrite, func(d Deps) gin.HandlerFunc { return d.AI.DeleteMemory }},
		{http.MethodPost, "/admin/ai/:id/generate", AdminWrite, func(d Deps) gin.HandlerFunc { return d.Agent.GenerateDraft }},
		{http.MethodPost, "/admin/ai/:id/publish-direct", AdminWrite, func(d Deps) gin.HandlerFunc { return d.Agent.PublishDirect }},
		{http.MethodPost, "/admin/ai/:id/drafts/:post_id", AdminWrite, func(d Deps) gin.HandlerFunc { return d.Agent.UpdateAIDraft }},
		{http.MethodPost, "/admin/ai/:id/drafts/:post_id/publish", AdminWrite, func(d Deps) gin.HandlerFunc { return d.Agent.PublishAIDraft }},
		{http.MethodPost, "/admin/ai-tasks/:id/retry", AdminWrite, func(d Deps) gin.HandlerFunc { return d.Agent.RetryAITask }},
		{http.MethodPost, "/admin/ai-tasks/:id/cancel", AdminWrite, func(d Deps) gin.HandlerFunc { return d.Agent.CancelAITask }},
		{http.MethodPost, "/admin/tasks/:id/retry", AdminWrite, func(d Deps) gin.HandlerFunc { return d.Admin.RetryTask }},
		{http.MethodPost, "/admin/tasks/:id/cancel", AdminWrite, func(d Deps) gin.HandlerFunc { return d.Admin.CancelTask }},
		{http.MethodPost, "/admin/settings", AdminWrite, func(d Deps) gin.HandlerFunc { return d.Admin.UpdateSettings }},
		{http.MethodPost, "/admin/settings/web-search", AdminWrite, func(d Deps) gin.HandlerFunc { return d.Admin.UpdateWebSearchSettings }},

		// ---------- 超级管理员写（仅超管；CSRF 在超管校验之前） ----------
		{http.MethodPost, "/admin/users/:id/grant-admin", SuperAdminWrite, func(d Deps) gin.HandlerFunc { return d.Auth.GrantAdmin }},
		{http.MethodPost, "/admin/users/:id/revoke-admin", SuperAdminWrite, func(d Deps) gin.HandlerFunc { return d.Auth.RevokeAdmin }},
	}
}
