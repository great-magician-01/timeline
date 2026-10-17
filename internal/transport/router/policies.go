package router

import (
	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/transport/middleware"
)

// Policy 是一条路由的授权策略；每个策略对应固定中间件链，禁止在路由上手工拼中间件。
type Policy int

const (
	// Public 完全公开：仅要求服务就绪，不解析会话。
	Public Policy = iota
	// Optional 公开读但解析可选会话（viewer_state / follow_state）。
	Optional
	// Login 必须登录且为活跃人类账号。
	Login
	// HumanWrite 已登录写：登录校验 + CSRF。
	HumanWrite
	// AdminRead 管理员只读。
	AdminRead
	// AdminWrite 管理员写：管理员校验 + CSRF。
	AdminWrite
	// SuperAdminWrite 超级管理员写：CSRF 在超管校验之前，保持缺少令牌时统一 csrf_failed。
	SuperAdminWrite
	// GuestWrite 游客写：双提交 CSRF。
	GuestWrite
)

// policyGroups 保存每个策略对应的 Gin 路由组。
type policyGroups struct {
	groups map[Policy]*gin.RouterGroup
}

// buildPolicies 构造策略组。中间件顺序在此集中固化：
//
//	Ready → ResolveSession → RequireLogin → RequireAdmin → RequireCSRF → RequireSuperAdmin
func buildPolicies(engine *gin.Engine, deps Deps, readiness Readiness) policyGroups {
	base := engine.Group("/api/v1", middleware.RequireReady(readiness))
	public := base.Group("")
	optional := public.Group("", deps.Auth.ResolveSession)
	login := optional.Group("", deps.Auth.RequireLogin)
	humanWrite := login.Group("", deps.Auth.RequireCSRF)
	adminRead := login.Group("", deps.Auth.RequireAdmin)
	adminWrite := adminRead.Group("", deps.Auth.RequireCSRF)
	superAdminWrite := adminWrite.Group("", deps.Auth.RequireSuperAdmin)
	guestWrite := base.Group("", deps.Auth.GuestCSRF)

	return policyGroups{groups: map[Policy]*gin.RouterGroup{
		Public:          public,
		Optional:        optional,
		Login:           login,
		HumanWrite:      humanWrite,
		AdminRead:       adminRead,
		AdminWrite:      adminWrite,
		SuperAdminWrite: superAdminWrite,
		GuestWrite:      guestWrite,
	}}
}

// Register 注册全部业务路由。engine 与全局中间件由 internal/server 装配。
func Register(engine *gin.Engine, deps Deps, readiness Readiness) {
	groups := buildPolicies(engine, deps, readiness)
	for _, route := range Routes() {
		group, ok := groups.groups[route.Policy]
		if !ok {
			panic("router: 未知策略")
		}
		group.Handle(route.Method, route.Path, route.Handler(deps))
	}
}
