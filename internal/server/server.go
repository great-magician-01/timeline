// Package server 提供 HTTP 服务（基于 Gin）：健康检查、就绪检查、业务接口与前端静态托管。
// 路径约定（docs/API.md）：业务接口在 /api/v1 下，只使用 GET 与 POST；
// /healthz 与 /readyz 不参与业务，不被 SPA 回退覆盖；未注册的 /api 路径返回 JSON 404。
//
// 中间件顺序：requireReady（迁移完成前不落到业务表）→ OptionalAuth（公开接口识别登录态，
// 用于 viewer_state / follow_state）→ 分组上的 RequireAuth / RequireAdmin / RequireCSRF。
package server

import (
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/admin"
	"github.com/great-magician-01/timeline/internal/agent"
	"github.com/great-magician-01/timeline/internal/ai"
	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/auth"
	"github.com/great-magician-01/timeline/internal/config"
	"github.com/great-magician-01/timeline/internal/content"
	"github.com/great-magician-01/timeline/internal/db"
	"github.com/great-magician-01/timeline/internal/jobs"
	"github.com/great-magician-01/timeline/internal/media"
	"github.com/great-magician-01/timeline/internal/notice"
	"github.com/great-magician-01/timeline/internal/social"
	"github.com/great-magician-01/timeline/internal/storage"
	"github.com/great-magician-01/timeline/web"
)

// State 保存运行期就绪状态，供 /readyz 查询。
type State struct {
	mu             sync.RWMutex
	pool           *pgxpool.Pool
	migrationsDone bool
}

func NewState() *State { return &State{} }

func (s *State) SetPool(p *pgxpool.Pool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pool = p
}

func (s *State) Pool() *pgxpool.Pool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pool
}

func (s *State) MarkMigrationsDone() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.migrationsDone = true
}

func (s *State) MigrationsDone() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.migrationsDone
}

type Server struct {
	cfg    *config.Config
	log    *slog.Logger
	state  *State
	engine *gin.Engine
}

// New 组装全部业务模块并注册路由。runner 由调用方启动（main 在 HTTP 起来后 Start）。
func New(
	cfg *config.Config,
	log *slog.Logger,
	state *State,
	ids auth.IDGenerator,
	runner *jobs.Runner,
	store *storage.Store,
) *Server {
	if cfg.LogLevel != "debug" {
		gin.SetMode(gin.ReleaseMode)
	}

	s := &Server{cfg: cfg, log: log, state: state, engine: gin.New()}
	// 默认不信任代理头；只有显式配置受信代理 IP/CIDR 时才采用转发头中的客户端 IP。
	if err := s.engine.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		log.Error("受信代理配置无效，已回退为不信任代理头", "err", err)
		_ = s.engine.SetTrustedProxies(nil)
	}
	s.engine.Use(gin.Recovery(), s.requestLogger(), s.securityHeaders())

	s.engine.GET("/healthz", s.handleHealthz)
	s.engine.GET("/readyz", s.handleReadyz)

	authService := auth.NewService(state, ids, log, cfg.CookieSecure)
	contentService := content.NewService(state, ids, log, runner)
	socialService := social.NewService(state, ids, log)
	noticeService := notice.NewService(state, ids, log)
	mediaService := media.NewService(state, ids, log, store, runner)
	aiService := ai.NewService(state, ids, log, cfg.SecretKey)
	agentService := agent.NewService(state, ids, log, aiService, contentService, socialService, runner)
	adminService := admin.NewService(state, ids, log, contentService, socialService, aiService, mediaService)

	s.registerBackgroundJobs(runner, contentService, mediaService, agentService)
	s.registerAPI(authService, contentService, socialService, noticeService, mediaService, aiService, agentService, adminService)
	s.registerFrontend()

	return s
}

// registerBackgroundJobs 把各模块的作业处理器与周期任务接入运行器。
func (s *Server) registerBackgroundJobs(
	runner *jobs.Runner,
	contentService *content.Service,
	mediaService *media.Service,
	agentService *agent.Service,
) {
	runner.Register(jobs.TypePostPublish, contentService.HandlePublishJob)
	runner.Register(jobs.TypeMediaDerivative, mediaService.HandleDerivativeJob)
	runner.Register(jobs.TypeAITask, agentService.HandleAITaskJob)
	runner.Register(jobs.TypeMemoryConsolidate, agentService.HandleMemoryJob)

	// 周期任务：AI 调度（定时原创、主动互动、提及/评论触发）、附件失效清理与调用记录保留期清理。
	runner.RegisterPeriodic("ai_schedule", time.Minute, agentService.RunScheduler)
	runner.RegisterPeriodic("attachment_cleanup", 6*time.Hour, mediaService.RunCleanup)
	runner.RegisterPeriodic("ai_call_log_cleanup", 24*time.Hour, agentService.RunCallLogCleanup)
}

func (s *Server) Handler() http.Handler { return s.engine }

type apiServices struct {
	auth    *auth.Service
	content *content.Service
	social  *social.Service
	notice  *notice.Service
	media   *media.Service
	ai      *ai.Service
	agent   *agent.Service
	admin   *admin.Service
}

// registerAPI 注册全部业务路由。业务写操作统一使用 POST（修改与资源同路径，
// 删除/取消使用语义化 action 路径），全部非 GET 请求都要求 CSRF 令牌。
func (s *Server) registerAPI(
	authService *auth.Service,
	contentService *content.Service,
	socialService *social.Service,
	noticeService *notice.Service,
	mediaService *media.Service,
	aiService *ai.Service,
	agentService *agent.Service,
	adminService *admin.Service,
) {
	services := apiServices{
		auth: authService, content: contentService, social: socialService,
		notice: noticeService, media: mediaService, ai: aiService,
		agent: agentService, admin: adminService,
	}

	// 公开接口也做一次可选认证，让公开列表带上 likes/reposted 等访问者状态。
	v1 := s.engine.Group("/api/v1", s.requireReady(), authService.OptionalAuth)
	secured := v1.Group("", authService.RequireAuth)
	csrf := authService.RequireCSRF

	s.registerAuthRoutes(v1, secured, services, csrf)
	s.registerProfileRoutes(v1, secured, services, csrf)
	s.registerPostRoutes(v1, secured, services, csrf)
	s.registerInteractionRoutes(v1, secured, services, csrf)
	s.registerDiscoveryRoutes(v1, secured, services)
	s.registerNoticeRoutes(secured, services, csrf)
	s.registerAttachmentRoutes(v1, secured, services, csrf)
	s.registerAdminRoutes(v1, services, csrf)
}

func (s *Server) registerAuthRoutes(v1, secured *gin.RouterGroup, services apiServices, csrf gin.HandlerFunc) {
	v1.GET("/auth/csrf", services.auth.CSRF)
	v1.GET("/auth/config", services.auth.Config)
	v1.POST("/auth/register", services.auth.GuestCSRF, services.auth.Register)
	v1.POST("/auth/login", services.auth.GuestCSRF, services.auth.Login)

	secured.GET("/auth/me", services.auth.Me)
	secured.POST("/auth/logout", csrf, services.auth.Logout)
	secured.POST("/auth/password", csrf, services.auth.ChangePassword)
	secured.POST("/users/me", csrf, services.auth.UpdateProfile)
	secured.GET("/users/me/follow-settings", services.auth.FollowSettings)
	secured.POST("/users/me/follow-settings", csrf, services.auth.UpdateFollowSettings)
}

func (s *Server) registerProfileRoutes(v1, secured *gin.RouterGroup, services apiServices, csrf gin.HandlerFunc) {
	v1.GET("/users/search", services.auth.SearchUsers)
	v1.GET("/users/:username", services.auth.GetProfile)
	v1.GET("/users/:username/posts", services.content.UserTimeline)
	v1.GET("/users/:username/followers", services.social.ListFollowers)
	v1.GET("/users/:username/following", services.social.ListFollowing)

	secured.POST("/users/:username/follow", csrf, services.social.Follow)
	secured.POST("/users/:username/unfollow", csrf, services.social.Unfollow)
	secured.POST("/users/me/followers/:username/remove", csrf, services.social.RemoveFollower)
	secured.GET("/follow-requests", services.social.ListFollowRequests)
	secured.POST("/follow-requests/:id/approve", csrf, services.social.ApproveFollowRequest)
	secured.POST("/follow-requests/:id/reject", csrf, services.social.RejectFollowRequest)
	secured.POST("/follow-requests/:id/cancel", csrf, services.social.CancelFollowRequest)
}

func (s *Server) registerPostRoutes(v1, secured *gin.RouterGroup, services apiServices, csrf gin.HandlerFunc) {
	v1.GET("/posts/:id", services.content.GetPost)
	secured.POST("/posts", csrf, services.content.CreatePost)
	secured.POST("/posts/:id", csrf, services.content.UpdatePost)
	secured.POST("/posts/:id/publish", csrf, services.content.PublishPost)
	secured.POST("/posts/:id/schedule", csrf, services.content.SchedulePost)
	secured.POST("/posts/:id/cancel-schedule", csrf, services.content.CancelSchedule)
	secured.POST("/posts/:id/delete", csrf, services.content.DeletePost)

	v1.GET("/posts/:id/versions", services.content.ListVersions)
	v1.GET("/posts/:id/versions/:version_id", services.content.GetVersion)
	secured.POST("/posts/:id/versions/:version_id/restore", csrf, services.content.RestoreVersion)

	secured.GET("/me/drafts", services.content.ListMyDrafts)
	secured.GET("/me/scheduled", services.content.ListMyScheduled)

	secured.POST("/posts/:id/repost", csrf, services.social.Repost)
	secured.POST("/posts/:id/unrepost", csrf, services.social.Unrepost)
}

func (s *Server) registerInteractionRoutes(v1, secured *gin.RouterGroup, services apiServices, csrf gin.HandlerFunc) {
	secured.POST("/posts/:id/like", csrf, services.social.LikePost)
	secured.POST("/posts/:id/unlike", csrf, services.social.UnlikePost)
	secured.POST("/comments/:id/like", csrf, services.social.LikeComment)
	secured.POST("/comments/:id/unlike", csrf, services.social.UnlikeComment)

	v1.GET("/posts/:id/comments", services.social.ListComments)
	v1.GET("/comments/:id/replies", services.social.ListReplies)
	secured.POST("/posts/:id/comments", csrf, services.social.CreateComment)
	secured.POST("/comments/:id/delete", csrf, services.social.DeleteComment)
}

func (s *Server) registerDiscoveryRoutes(v1, secured *gin.RouterGroup, services apiServices) {
	v1.GET("/timeline", services.content.Timeline)
	secured.GET("/timeline/following", services.content.FollowingTimeline)
	v1.GET("/tags/suggest", services.content.SuggestTags)
	v1.GET("/tags/:name/posts", services.content.TagTimeline)
	v1.GET("/search/posts", services.content.SearchPosts)
}

func (s *Server) registerNoticeRoutes(secured *gin.RouterGroup, services apiServices, csrf gin.HandlerFunc) {
	secured.GET("/notifications", services.notice.List)
	secured.GET("/notifications/unread-count", services.notice.UnreadCount)
	secured.POST("/notifications/read-all", csrf, services.notice.MarkAllRead)
	secured.POST("/notifications/:id/read", csrf, services.notice.MarkRead)
}

func (s *Server) registerAttachmentRoutes(v1, secured *gin.RouterGroup, services apiServices, csrf gin.HandlerFunc) {
	secured.POST("/attachments", csrf, services.media.Upload)
	v1.GET("/attachments/:id/content", services.media.Content)
	v1.GET("/attachments/:id/derivatives/:type", services.media.Derivative)
	secured.POST("/attachments/:id/delete", csrf, services.media.Delete)
}

func (s *Server) registerAdminRoutes(v1 *gin.RouterGroup, services apiServices, csrf gin.HandlerFunc) {
	adminRoutes := v1.Group("/admin", services.auth.RequireAuth, services.auth.RequireAdmin)

	// 概览与用户管理
	adminRoutes.GET("/overview", services.admin.GetOverview)
	adminRoutes.GET("/users", services.admin.ListUsers)
	adminRoutes.GET("/users/:id", services.admin.GetUser)
	adminRoutes.POST("/users/:id/suspend", csrf, services.admin.SuspendUser)
	adminRoutes.POST("/users/:id/unsuspend", csrf, services.admin.UnsuspendUser)
	adminRoutes.POST("/users/:id/reset-password", csrf, services.admin.ResetPassword)
	// 仅超级管理员可授权/撤销管理员；csrf 在前，保证缺少 CSRF 时仍统一返回 csrf_failed。
	adminRoutes.POST("/users/:id/grant-admin", csrf, services.auth.RequireSuperAdmin, services.admin.GrantAdmin)
	adminRoutes.POST("/users/:id/revoke-admin", csrf, services.auth.RequireSuperAdmin, services.admin.RevokeAdmin)

	// 内容审核
	adminRoutes.GET("/posts", services.admin.ListPosts)
	adminRoutes.GET("/posts/:id", services.admin.GetPost)
	adminRoutes.POST("/posts/:id/delete", csrf, services.admin.DeletePost)
	adminRoutes.GET("/comments", services.admin.ListComments)
	adminRoutes.POST("/comments/:id/delete", csrf, services.admin.DeleteComment)
	adminRoutes.GET("/attachments", services.admin.ListAttachments)
	adminRoutes.POST("/attachments/:id/delete", csrf, services.admin.DeleteAttachment)

	// 模型接口预设
	adminRoutes.GET("/providers", services.ai.ListProviders)
	adminRoutes.POST("/providers", csrf, services.ai.CreateProvider)
	adminRoutes.POST("/providers/:id", csrf, services.ai.UpdateProvider)
	adminRoutes.POST("/providers/:id/delete", csrf, services.ai.DeleteProvider)
	adminRoutes.POST("/providers/:id/test", csrf, services.ai.TestProvider)

	// AI 身份、模型与行为策略
	adminRoutes.GET("/ai", services.ai.ListAIs)
	adminRoutes.POST("/ai", csrf, services.ai.CreateAI)
	adminRoutes.POST("/ai/global-start", csrf, services.agent.GlobalStart)
	adminRoutes.POST("/ai/global-stop", csrf, services.agent.GlobalStop)
	adminRoutes.GET("/ai/:id", services.ai.GetAI)
	adminRoutes.POST("/ai/:id", csrf, services.ai.UpdateAI)
	adminRoutes.POST("/ai/:id/model", csrf, services.ai.UpdateAIModel)
	adminRoutes.POST("/ai/:id/behavior", csrf, services.ai.UpdateAIBehavior)
	adminRoutes.POST("/ai/:id/enable", csrf, services.ai.EnableAI)
	adminRoutes.POST("/ai/:id/disable", csrf, services.ai.DisableAI)

	// 分层记忆
	adminRoutes.GET("/ai/:id/memories", services.ai.ListMemories)
	adminRoutes.POST("/ai/:id/memories", csrf, services.ai.CreateMemory)
	adminRoutes.POST("/ai/:id/memories/clear", csrf, services.ai.ClearMemories)
	adminRoutes.POST("/ai/:id/memories/:memory_id", csrf, services.ai.UpdateMemory)
	adminRoutes.POST("/ai/:id/memories/:memory_id/delete", csrf, services.ai.DeleteMemory)

	// 生成与发布
	adminRoutes.POST("/ai/:id/generate", csrf, services.agent.GenerateDraft)
	adminRoutes.POST("/ai/:id/publish-direct", csrf, services.agent.PublishDirect)
	adminRoutes.GET("/ai/:id/drafts", services.agent.ListAIDrafts)
	adminRoutes.POST("/ai/:id/drafts/:post_id", csrf, services.agent.UpdateAIDraft)
	adminRoutes.POST("/ai/:id/drafts/:post_id/publish", csrf, services.agent.PublishAIDraft)
	adminRoutes.GET("/ai/:id/tasks", services.agent.ListAITasks)
	adminRoutes.POST("/ai-tasks/:id/retry", csrf, services.agent.RetryAITask)
	adminRoutes.POST("/ai-tasks/:id/cancel", csrf, services.agent.CancelAITask)

	// 调用记录、任务、审计与设置
	adminRoutes.GET("/ai-calls", services.agent.ListCallLogs)
	adminRoutes.GET("/ai-calls/:id", services.agent.GetCallLog)
	adminRoutes.GET("/tasks", services.admin.ListTasks)
	adminRoutes.POST("/tasks/:id/retry", csrf, services.admin.RetryTask)
	adminRoutes.POST("/tasks/:id/cancel", csrf, services.admin.CancelTask)
	adminRoutes.GET("/audit-logs", services.admin.ListAuditLogs)
	adminRoutes.GET("/settings", services.admin.GetSettings)
	adminRoutes.POST("/settings", csrf, services.admin.UpdateSettings)
	adminRoutes.GET("/settings/web-search", services.admin.GetWebSearchSettings)
	adminRoutes.POST("/settings/web-search", csrf, services.admin.UpdateWebSearchSettings)
}

// requireReady 防止迁移完成前请求落到尚不存在的业务表。
func (s *Server) requireReady() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.state.Pool() == nil || !s.state.MigrationsDone() {
			api.Error(c, http.StatusServiceUnavailable, "service_unavailable", "服务尚未就绪，请稍后重试")
			return
		}
		c.Next()
	}
}

func (s *Server) securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "same-origin")
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.Header("Cache-Control", "no-store")
		}
		c.Next()
	}
}

// requestLogger 用 slog 记录访问日志（5xx→Error，4xx→Warn，其余 Info）。
func (s *Server) requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		status := c.Writer.Status()
		attrs := []any{
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", status,
			"latency_ms", time.Since(start).Milliseconds(),
			"ip", c.ClientIP(),
		}
		if len(c.Errors) > 0 {
			attrs = append(attrs, "err", c.Errors.Last().Error())
		}
		switch {
		case status >= 500:
			s.log.Error("http", attrs...)
		case status >= 400:
			s.log.Warn("http", attrs...)
		default:
			s.log.Info("http", attrs...)
		}
	}
}

func (s *Server) handleHealthz(c *gin.Context) {
	// 存活检查：进程可响应即 200（容器健康检查用）
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (s *Server) handleReadyz(c *gin.Context) {
	pool := s.state.Pool()
	database := "disconnected"
	if pool != nil {
		if err := db.Ping(c.Request.Context(), pool); err == nil {
			database = "ok"
		}
	}
	migrations := "pending"
	if s.state.MigrationsDone() {
		migrations = "done"
	}

	if database == "ok" && migrations == "done" {
		c.JSON(http.StatusOK, gin.H{"status": "ready", "database": database, "migrations": migrations})
		return
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not-ready", "database": database, "migrations": migrations})
}

// registerFrontend 注册前端托管与 SPA 回退。
func (s *Server) registerFrontend() {
	dist, built := web.Dist()
	if !built {
		s.log.Warn("前端产物未构建（web/dist 无 index.html），使用内置占位页")
		s.engine.NoRoute(func(c *gin.Context) {
			p := strings.TrimPrefix(path.Clean("/"+c.Request.URL.Path), "/")
			if p == "api" || strings.HasPrefix(p, "api/") {
				c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"code": "not_found", "message": "接口不存在"}})
				return
			}
			c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(placeholderHTML))
		})
		return
	}

	s.engine.GET("/", func(c *gin.Context) { serveIndex(c, dist) })
	s.engine.NoRoute(func(c *gin.Context) {
		p := strings.TrimPrefix(path.Clean("/"+c.Request.URL.Path), "/")
		if p == "api" || strings.HasPrefix(p, "api/") {
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"code": "not_found", "message": "接口不存在"}})
			return
		}
		// 命中真实静态文件则直接返回（FileFromFS 按原始请求路径从 dist 读取）
		if f, err := fs.Stat(dist, p); err == nil && !f.IsDir() {
			c.FileFromFS(c.Request.URL.Path, http.FS(dist))
			return
		}
		// 其余路径回退到 SPA 入口
		serveIndex(c, dist)
	})
}

func serveIndex(c *gin.Context, dist fs.FS) {
	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		c.String(http.StatusInternalServerError, "index.html 缺失")
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", index)
}

const placeholderHTML = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><title>时间线</title></head>
<body style="font-family:sans-serif;padding:2rem">
<h1>时间线</h1>
<p>前端尚未构建（web/dist 为空）。开发模式请运行 <code>cd web && npm run dev</code>，或在 web 目录执行 <code>npm run build</code> 后重启本服务。</p>
<p>后端状态：<a href="/healthz">/healthz</a> · <a href="/readyz">/readyz</a></p>
</body></html>`
