package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// 本文件覆盖 transport/router 的注册与策略中间件链（无数据库、无业务模块）：
//   - Routes() 声明的 124 条路由必须全部注册到 Gin 引擎；
//   - 每种 Policy 必须挂载固定顺序的中间件链（Ready → ResolveSession → RequireLogin
//     → RequireAdmin → RequireCSRF → RequireSuperAdmin，GuestWrite 只走 GuestCSRF）；
//   - 就绪检查与认证/CSRF 失败必须中止后续中间件与业务 handler。

// stubHandler 为全部 handler 接口提供空实现；方法只是把“业务 handler 已被调用”
// 写进响应头，便于断言中间件是否放行。路由注册只需要方法值可求值，不真正执行业务。
type stubHandler struct{}

func (stubHandler) CSRF(c *gin.Context)                    { c.Header("X-Handler", "reached") }
func (stubHandler) Config(c *gin.Context)                  { c.Header("X-Handler", "reached") }
func (stubHandler) Register(c *gin.Context)                { c.Header("X-Handler", "reached") }
func (stubHandler) Login(c *gin.Context)                   { c.Header("X-Handler", "reached") }
func (stubHandler) Me(c *gin.Context)                      { c.Header("X-Handler", "reached") }
func (stubHandler) Logout(c *gin.Context)                  { c.Header("X-Handler", "reached") }
func (stubHandler) ChangePassword(c *gin.Context)          { c.Header("X-Handler", "reached") }
func (stubHandler) UpdateProfile(c *gin.Context)           { c.Header("X-Handler", "reached") }
func (stubHandler) DeleteBanner(c *gin.Context)            { c.Header("X-Handler", "reached") }
func (stubHandler) FollowSettings(c *gin.Context)          { c.Header("X-Handler", "reached") }
func (stubHandler) UpdateFollowSettings(c *gin.Context)    { c.Header("X-Handler", "reached") }
func (stubHandler) DMSettings(c *gin.Context)              { c.Header("X-Handler", "reached") }
func (stubHandler) UpdateDMSettings(c *gin.Context)        { c.Header("X-Handler", "reached") }
func (stubHandler) SearchUsers(c *gin.Context)             { c.Header("X-Handler", "reached") }
func (stubHandler) GetProfile(c *gin.Context)              { c.Header("X-Handler", "reached") }
func (stubHandler) GrantAdmin(c *gin.Context)              { c.Header("X-Handler", "reached") }
func (stubHandler) RevokeAdmin(c *gin.Context)             { c.Header("X-Handler", "reached") }
func (stubHandler) ResolveSession(c *gin.Context)          { c.Header("X-Handler", "reached") }
func (stubHandler) RequireLogin(c *gin.Context)            { c.Header("X-Handler", "reached") }
func (stubHandler) RequireAdmin(c *gin.Context)            { c.Header("X-Handler", "reached") }
func (stubHandler) RequireSuperAdmin(c *gin.Context)       { c.Header("X-Handler", "reached") }
func (stubHandler) RequireCSRF(c *gin.Context)             { c.Header("X-Handler", "reached") }
func (stubHandler) GuestCSRF(c *gin.Context)               { c.Header("X-Handler", "reached") }
func (stubHandler) Events(c *gin.Context)                  { c.Header("X-Handler", "reached") }
func (stubHandler) UserTimeline(c *gin.Context)            { c.Header("X-Handler", "reached") }
func (stubHandler) GetPost(c *gin.Context)                 { c.Header("X-Handler", "reached") }
func (stubHandler) CreatePost(c *gin.Context)              { c.Header("X-Handler", "reached") }
func (stubHandler) UpdatePost(c *gin.Context)              { c.Header("X-Handler", "reached") }
func (stubHandler) PublishPost(c *gin.Context)             { c.Header("X-Handler", "reached") }
func (stubHandler) SchedulePost(c *gin.Context)            { c.Header("X-Handler", "reached") }
func (stubHandler) CancelSchedule(c *gin.Context)          { c.Header("X-Handler", "reached") }
func (stubHandler) DeletePost(c *gin.Context)              { c.Header("X-Handler", "reached") }
func (stubHandler) PinPost(c *gin.Context)                 { c.Header("X-Handler", "reached") }
func (stubHandler) UnpinPost(c *gin.Context)               { c.Header("X-Handler", "reached") }
func (stubHandler) ReorderPinnedPosts(c *gin.Context)      { c.Header("X-Handler", "reached") }
func (stubHandler) ListVersions(c *gin.Context)            { c.Header("X-Handler", "reached") }
func (stubHandler) GetVersion(c *gin.Context)              { c.Header("X-Handler", "reached") }
func (stubHandler) RestoreVersion(c *gin.Context)          { c.Header("X-Handler", "reached") }
func (stubHandler) ListMyDrafts(c *gin.Context)            { c.Header("X-Handler", "reached") }
func (stubHandler) ListMyScheduled(c *gin.Context)         { c.Header("X-Handler", "reached") }
func (stubHandler) ListBookmarks(c *gin.Context)           { c.Header("X-Handler", "reached") }
func (stubHandler) Timeline(c *gin.Context)                { c.Header("X-Handler", "reached") }
func (stubHandler) FollowingTimeline(c *gin.Context)       { c.Header("X-Handler", "reached") }
func (stubHandler) SuggestTags(c *gin.Context)             { c.Header("X-Handler", "reached") }
func (stubHandler) TagTimeline(c *gin.Context)             { c.Header("X-Handler", "reached") }
func (stubHandler) SearchPosts(c *gin.Context)             { c.Header("X-Handler", "reached") }
func (stubHandler) ListFollowers(c *gin.Context)           { c.Header("X-Handler", "reached") }
func (stubHandler) ListFollowing(c *gin.Context)           { c.Header("X-Handler", "reached") }
func (stubHandler) Follow(c *gin.Context)                  { c.Header("X-Handler", "reached") }
func (stubHandler) Unfollow(c *gin.Context)                { c.Header("X-Handler", "reached") }
func (stubHandler) RemoveFollower(c *gin.Context)          { c.Header("X-Handler", "reached") }
func (stubHandler) ListFollowRequests(c *gin.Context)      { c.Header("X-Handler", "reached") }
func (stubHandler) ApproveFollowRequest(c *gin.Context)    { c.Header("X-Handler", "reached") }
func (stubHandler) RejectFollowRequest(c *gin.Context)     { c.Header("X-Handler", "reached") }
func (stubHandler) CancelFollowRequest(c *gin.Context)     { c.Header("X-Handler", "reached") }
func (stubHandler) Repost(c *gin.Context)                  { c.Header("X-Handler", "reached") }
func (stubHandler) Unrepost(c *gin.Context)                { c.Header("X-Handler", "reached") }
func (stubHandler) LikePost(c *gin.Context)                { c.Header("X-Handler", "reached") }
func (stubHandler) UnlikePost(c *gin.Context)              { c.Header("X-Handler", "reached") }
func (stubHandler) BookmarkPost(c *gin.Context)            { c.Header("X-Handler", "reached") }
func (stubHandler) UnbookmarkPost(c *gin.Context)          { c.Header("X-Handler", "reached") }
func (stubHandler) ListBookmarkFolders(c *gin.Context)     { c.Header("X-Handler", "reached") }
func (stubHandler) CreateBookmarkFolder(c *gin.Context)    { c.Header("X-Handler", "reached") }
func (stubHandler) RenameBookmarkFolder(c *gin.Context)    { c.Header("X-Handler", "reached") }
func (stubHandler) DeleteBookmarkFolder(c *gin.Context)    { c.Header("X-Handler", "reached") }
func (stubHandler) LikeComment(c *gin.Context)             { c.Header("X-Handler", "reached") }
func (stubHandler) UnlikeComment(c *gin.Context)           { c.Header("X-Handler", "reached") }
func (stubHandler) ListComments(c *gin.Context)            { c.Header("X-Handler", "reached") }
func (stubHandler) ListReplies(c *gin.Context)             { c.Header("X-Handler", "reached") }
func (stubHandler) CreateComment(c *gin.Context)           { c.Header("X-Handler", "reached") }
func (stubHandler) DeleteComment(c *gin.Context)           { c.Header("X-Handler", "reached") }
func (stubHandler) List(c *gin.Context)                    { c.Header("X-Handler", "reached") }
func (stubHandler) UnreadCount(c *gin.Context)             { c.Header("X-Handler", "reached") }
func (stubHandler) MarkAllRead(c *gin.Context)             { c.Header("X-Handler", "reached") }
func (stubHandler) MarkRead(c *gin.Context)                { c.Header("X-Handler", "reached") }
func (stubHandler) ListConversations(c *gin.Context)       { c.Header("X-Handler", "reached") }
func (stubHandler) CreateConversation(c *gin.Context)      { c.Header("X-Handler", "reached") }
func (stubHandler) ConversationDetail(c *gin.Context)      { c.Header("X-Handler", "reached") }
func (stubHandler) HideConversation(c *gin.Context)        { c.Header("X-Handler", "reached") }
func (stubHandler) ListMessages(c *gin.Context)            { c.Header("X-Handler", "reached") }
func (stubHandler) SendMessage(c *gin.Context)             { c.Header("X-Handler", "reached") }
func (stubHandler) RecallMessage(c *gin.Context)           { c.Header("X-Handler", "reached") }
func (stubHandler) Upload(c *gin.Context)                  { c.Header("X-Handler", "reached") }
func (stubHandler) Content(c *gin.Context)                 { c.Header("X-Handler", "reached") }
func (stubHandler) Derivative(c *gin.Context)              { c.Header("X-Handler", "reached") }
func (stubHandler) Delete(c *gin.Context)                  { c.Header("X-Handler", "reached") }
func (stubHandler) ListProviders(c *gin.Context)           { c.Header("X-Handler", "reached") }
func (stubHandler) CreateProvider(c *gin.Context)          { c.Header("X-Handler", "reached") }
func (stubHandler) UpdateProvider(c *gin.Context)          { c.Header("X-Handler", "reached") }
func (stubHandler) DeleteProvider(c *gin.Context)          { c.Header("X-Handler", "reached") }
func (stubHandler) TestProvider(c *gin.Context)            { c.Header("X-Handler", "reached") }
func (stubHandler) ListAIs(c *gin.Context)                 { c.Header("X-Handler", "reached") }
func (stubHandler) CreateAI(c *gin.Context)                { c.Header("X-Handler", "reached") }
func (stubHandler) GetAI(c *gin.Context)                   { c.Header("X-Handler", "reached") }
func (stubHandler) UpdateAI(c *gin.Context)                { c.Header("X-Handler", "reached") }
func (stubHandler) UpdateAIModel(c *gin.Context)           { c.Header("X-Handler", "reached") }
func (stubHandler) UpdateAIBehavior(c *gin.Context)        { c.Header("X-Handler", "reached") }
func (stubHandler) EnableAI(c *gin.Context)                { c.Header("X-Handler", "reached") }
func (stubHandler) DisableAI(c *gin.Context)               { c.Header("X-Handler", "reached") }
func (stubHandler) ListMemories(c *gin.Context)            { c.Header("X-Handler", "reached") }
func (stubHandler) CreateMemory(c *gin.Context)            { c.Header("X-Handler", "reached") }
func (stubHandler) ClearMemories(c *gin.Context)           { c.Header("X-Handler", "reached") }
func (stubHandler) UpdateMemory(c *gin.Context)            { c.Header("X-Handler", "reached") }
func (stubHandler) DeleteMemory(c *gin.Context)            { c.Header("X-Handler", "reached") }
func (stubHandler) GlobalStart(c *gin.Context)             { c.Header("X-Handler", "reached") }
func (stubHandler) GlobalStop(c *gin.Context)              { c.Header("X-Handler", "reached") }
func (stubHandler) GenerateDraft(c *gin.Context)           { c.Header("X-Handler", "reached") }
func (stubHandler) PublishDirect(c *gin.Context)           { c.Header("X-Handler", "reached") }
func (stubHandler) ListAIDrafts(c *gin.Context)            { c.Header("X-Handler", "reached") }
func (stubHandler) UpdateAIDraft(c *gin.Context)           { c.Header("X-Handler", "reached") }
func (stubHandler) PublishAIDraft(c *gin.Context)          { c.Header("X-Handler", "reached") }
func (stubHandler) ListAITasks(c *gin.Context)             { c.Header("X-Handler", "reached") }
func (stubHandler) RetryAITask(c *gin.Context)             { c.Header("X-Handler", "reached") }
func (stubHandler) CancelAITask(c *gin.Context)            { c.Header("X-Handler", "reached") }
func (stubHandler) ListCallLogs(c *gin.Context)            { c.Header("X-Handler", "reached") }
func (stubHandler) GetCallLog(c *gin.Context)              { c.Header("X-Handler", "reached") }
func (stubHandler) GetOverview(c *gin.Context)             { c.Header("X-Handler", "reached") }
func (stubHandler) ListUsers(c *gin.Context)               { c.Header("X-Handler", "reached") }
func (stubHandler) GetUser(c *gin.Context)                 { c.Header("X-Handler", "reached") }
func (stubHandler) SuspendUser(c *gin.Context)             { c.Header("X-Handler", "reached") }
func (stubHandler) UnsuspendUser(c *gin.Context)           { c.Header("X-Handler", "reached") }
func (stubHandler) ResetPassword(c *gin.Context)           { c.Header("X-Handler", "reached") }
func (stubHandler) ListPosts(c *gin.Context)               { c.Header("X-Handler", "reached") }
func (stubHandler) ListAttachments(c *gin.Context)         { c.Header("X-Handler", "reached") }
func (stubHandler) DeleteAttachment(c *gin.Context)        { c.Header("X-Handler", "reached") }
func (stubHandler) ListTasks(c *gin.Context)               { c.Header("X-Handler", "reached") }
func (stubHandler) RetryTask(c *gin.Context)               { c.Header("X-Handler", "reached") }
func (stubHandler) CancelTask(c *gin.Context)              { c.Header("X-Handler", "reached") }
func (stubHandler) ListAuditLogs(c *gin.Context)           { c.Header("X-Handler", "reached") }
func (stubHandler) GetSettings(c *gin.Context)             { c.Header("X-Handler", "reached") }
func (stubHandler) UpdateSettings(c *gin.Context)          { c.Header("X-Handler", "reached") }
func (stubHandler) GetWebSearchSettings(c *gin.Context)    { c.Header("X-Handler", "reached") }
func (stubHandler) UpdateWebSearchSettings(c *gin.Context) { c.Header("X-Handler", "reached") }

// fakeAuth 在 stubHandler 之上覆写认证中间件：记录执行顺序并按需模拟失败。
type fakeAuth struct {
	stubHandler
	// abort 指定在哪一步中止：""（全部放行）/ login / admin / csrf / guest-csrf / super。
	abort string
}

// appendChain 把中间件步骤追加到响应头 X-Chain，用于断言执行顺序。
func appendChain(c *gin.Context, step string) {
	existing := c.Writer.Header().Get("X-Chain")
	if existing != "" {
		existing += ","
	}
	c.Writer.Header().Set("X-Chain", existing+step)
}

// step 记录一个中间件步骤；命中 abort 时写回错误响应并返回 false。
func (f *fakeAuth) step(c *gin.Context, name string, status int, code string) bool {
	appendChain(c, name)
	if f.abort != name {
		return true
	}
	httpx.Error(c, status, code, "测试注入的中间件拒绝")
	return false
}

// ResolveSession 可选会话解析：始终放行，仅记录步骤。
func (f *fakeAuth) ResolveSession(c *gin.Context) {
	appendChain(c, "session")
	c.Next()
}

// RequireLogin 登录校验。
func (f *fakeAuth) RequireLogin(c *gin.Context) {
	if !f.step(c, "login", http.StatusUnauthorized, "unauthenticated") {
		return
	}
	c.Next()
}

// RequireAdmin 管理员校验。
func (f *fakeAuth) RequireAdmin(c *gin.Context) {
	if !f.step(c, "admin", http.StatusForbidden, "forbidden") {
		return
	}
	c.Next()
}

// RequireCSRF 已登录写请求的 CSRF 校验。
func (f *fakeAuth) RequireCSRF(c *gin.Context) {
	if !f.step(c, "csrf", http.StatusForbidden, "csrf_failed") {
		return
	}
	c.Next()
}

// GuestCSRF 游客写请求的双提交 CSRF 校验。
func (f *fakeAuth) GuestCSRF(c *gin.Context) {
	if !f.step(c, "guest-csrf", http.StatusForbidden, "csrf_failed") {
		return
	}
	c.Next()
}

// RequireSuperAdmin 超级管理员校验（必须排在 CSRF 之后）。
func (f *fakeAuth) RequireSuperAdmin(c *gin.Context) {
	if !f.step(c, "super", http.StatusForbidden, "forbidden") {
		return
	}
	c.Next()
}

// fakeReadiness 是 Readiness 的假实现。
type fakeReadiness struct{ ready bool }

func (f fakeReadiness) Ready() bool { return f.ready }

// newTestDeps 构造只含 stub handler 的依赖；auth 使用可注入失败的 fakeAuth。
func newTestDeps(auth *fakeAuth) Deps {
	return Deps{
		Auth:     auth,
		Content:  stubHandler{},
		Social:   stubHandler{},
		Notice:   stubHandler{},
		DM:       stubHandler{},
		Media:    stubHandler{},
		AI:       stubHandler{},
		Agent:    stubHandler{},
		Admin:    stubHandler{},
		Realtime: stubHandler{},
	}
}

// newRegisteredEngine 用给定 readiness/auth 构造并注册全部路由的引擎。
func newRegisteredEngine(t *testing.T, auth *fakeAuth, ready bool) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	Register(engine, newTestDeps(auth), fakeReadiness{ready: ready})
	return engine
}

// doRequest 发起一次请求并返回响应记录器。
func doRequest(engine *gin.Engine, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	resp := httptest.NewRecorder()
	engine.ServeHTTP(resp, req)
	return resp
}

// TestRegisterRegistersAllDeclaredRoutes 校验 Routes() 的每一条路由都注册到了
// /api/v1 下且没有重复/遗漏；这是路由表与引擎之间的完整性契约。
func TestRegisterRegistersAllDeclaredRoutes(t *testing.T) {
	engine := newRegisteredEngine(t, &fakeAuth{}, true)

	registered := map[string]bool{}
	for _, info := range engine.Routes() {
		registered[info.Method+" "+info.Path] = true
	}
	if len(engine.Routes()) != len(Routes()) {
		t.Fatalf("引擎注册路由数 = %d，期望与 Routes() 一致 %d", len(engine.Routes()), len(Routes()))
	}
	for _, route := range Routes() {
		key := route.Method + " /api/v1" + route.Path
		if !registered[key] {
			t.Errorf("路由未注册到引擎: %s", key)
		}
	}
}

// TestPolicyMiddlewareChains 校验每种 Policy 的中间件链与顺序：
// 公开路由不解析会话；可选会话只解析；写策略必须包含 CSRF；超管写在 CSRF 之后。
func TestPolicyMiddlewareChains(t *testing.T) {
	cases := []struct {
		name      string
		method    string
		path      string
		wantChain string
	}{
		{"Public 不解析会话", http.MethodGet, "/api/v1/auth/config", ""},
		{"Optional 解析可选会话", http.MethodGet, "/api/v1/timeline", "session"},
		{"Login 要求登录", http.MethodGet, "/api/v1/auth/me", "session,login"},
		{"HumanWrite 登录加 CSRF", http.MethodPost, "/api/v1/posts", "session,login,csrf"},
		{"AdminRead 管理员只读", http.MethodGet, "/api/v1/admin/overview", "session,login,admin"},
		{"AdminWrite 管理员写加 CSRF", http.MethodPost, "/api/v1/admin/ai", "session,login,admin,csrf"},
		{"SuperAdminWrite CSRF 在超管之前", http.MethodPost, "/api/v1/admin/users/1/grant-admin",
			"session,login,admin,csrf,super"},
		{"GuestWrite 只做游客 CSRF", http.MethodPost, "/api/v1/auth/register", "guest-csrf"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			engine := newRegisteredEngine(t, &fakeAuth{}, true)
			resp := doRequest(engine, tc.method, tc.path)
			if resp.Code != http.StatusOK {
				t.Fatalf("%s %s status=%d body=%s", tc.method, tc.path, resp.Code, resp.Body.String())
			}
			if got := resp.Header().Get("X-Chain"); got != tc.wantChain {
				t.Fatalf("中间件链 = %q，期望 %q", got, tc.wantChain)
			}
			if got := resp.Header().Get("X-Handler"); got != "reached" {
				t.Fatalf("中间件应放行到业务 handler，X-Handler=%q", got)
			}
		})
	}
}

// TestReadinessGateRejectsBeforePolicyMiddleware 校验就绪检查位于全部业务中间件之前：
// 未就绪时即使公开/游客写路由也直接 503，且不进入会话解析或业务 handler。
func TestReadinessGateRejectsBeforePolicyMiddleware(t *testing.T) {
	for _, ready := range []bool{false} {
		engine := newRegisteredEngine(t, &fakeAuth{}, ready)
		for _, target := range [][2]string{
			{http.MethodGet, "/api/v1/timeline"},
			{http.MethodPost, "/api/v1/auth/register"},
		} {
			resp := doRequest(engine, target[0], target[1])
			if resp.Code != http.StatusServiceUnavailable {
				t.Fatalf("%s %s status=%d，期望 503", target[0], target[1], resp.Code)
			}
			if got := resp.Header().Get("X-Chain"); got != "" {
				t.Fatalf("未就绪时不应执行策略中间件，X-Chain=%q", got)
			}
			if got := resp.Header().Get("X-Handler"); got != "" {
				t.Fatalf("未就绪时不应进入业务 handler，X-Handler=%q", got)
			}
			if body := resp.Body.String(); body == "" || !strings.Contains(body, "service_unavailable") {
				t.Fatalf("未就绪错误响应缺少 service_unavailable: %s", body)
			}
		}
	}

	// nil readiness 与未就绪等价（新 server 的默认状态）。
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	Register(engine, newTestDeps(&fakeAuth{}), nil)
	if code := doRequest(engine, http.MethodGet, "/api/v1/timeline").Code; code != http.StatusServiceUnavailable {
		t.Fatalf("nil readiness status=%d，期望 503", code)
	}
}

// TestMiddlewareAbortStopsFollowingSteps 校验认证/CSRF 失败会中止后续中间件：
// 顺序固定后，登录失败不得执行 CSRF；CSRF 失败不得执行超管校验；业务 handler 不被调用。
func TestMiddlewareAbortStopsFollowingSteps(t *testing.T) {
	cases := []struct {
		name      string
		abort     string
		method    string
		path      string
		wantCode  int
		wantChain string
	}{
		{"登录失败不执行 CSRF", "login", http.MethodPost, "/api/v1/posts",
			http.StatusUnauthorized, "session,login"},
		{"CSRF 失败不执行超管校验", "csrf", http.MethodPost, "/api/v1/admin/users/1/grant-admin",
			http.StatusForbidden, "session,login,admin,csrf"},
		{"管理员校验失败不执行 CSRF", "admin", http.MethodPost, "/api/v1/admin/ai",
			http.StatusForbidden, "session,login,admin"},
		{"游客 CSRF 失败", "guest-csrf", http.MethodPost, "/api/v1/auth/login",
			http.StatusForbidden, "guest-csrf"},
		{"超管校验失败", "super", http.MethodPost, "/api/v1/admin/users/1/revoke-admin",
			http.StatusForbidden, "session,login,admin,csrf,super"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			engine := newRegisteredEngine(t, &fakeAuth{abort: tc.abort}, true)
			resp := doRequest(engine, tc.method, tc.path)
			if resp.Code != tc.wantCode {
				t.Fatalf("status=%d，期望 %d，body=%s", resp.Code, tc.wantCode, resp.Body.String())
			}
			if got := resp.Header().Get("X-Chain"); got != tc.wantChain {
				t.Fatalf("中间件链 = %q，期望 %q", got, tc.wantChain)
			}
			if got := resp.Header().Get("X-Handler"); got != "" {
				t.Fatalf("被拒绝的请求不应进入业务 handler，X-Handler=%q", got)
			}
		})
	}
}
