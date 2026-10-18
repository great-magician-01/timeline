package http

import (
	stdhttp "net/http"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/modules/auth/app"
	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// CSRF 处理 GET /api/v1/auth/csrf：已登录返回会话绑定的 CSRF 令牌；
// 游客或失效会话签发双提交令牌并写入 timeline_csrf Cookie。
func (h *Handler) CSRF(c *gin.Context) {
	if session, ok := httpx.Session(c); ok && session.CSRFToken != "" {
		c.JSON(stdhttp.StatusOK, csrfResponse{CSRFToken: session.CSRFToken})
		return
	}
	token, err := h.svc.GuestCSRFToken()
	if err != nil {
		h.fail(c, "生成 CSRF 令牌失败", err)
		return
	}
	h.setGuestCSRFCookie(c, token)
	c.JSON(stdhttp.StatusOK, csrfResponse{CSRFToken: token})
}

// Config 处理 GET /api/v1/auth/config（公开接口）：返回注册开关与初始化状态，
// 供前端在注册关闭时隐藏入口，并在空库时提示首个注册账号将成为超级管理员。
func (h *Handler) Config(c *gin.Context) {
	result, err := h.svc.Config(c.Request.Context())
	if err != nil {
		h.fail(c, "读取认证配置失败", err)
		return
	}
	c.JSON(stdhttp.StatusOK, configResponse{
		RegistrationEnabled: result.RegistrationEnabled,
		BootstrapRequired:   result.BootstrapRequired,
	})
}

// Register 处理 POST /api/v1/auth/register（GuestCSRF 之后）。
func (h *Handler) Register(c *gin.Context) {
	var in struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !h.decodeJSON(c, &in) {
		return
	}
	user, err := h.svc.Register(c.Request.Context(), app.RegisterInput{
		Username: in.Username,
		Email:    in.Email,
		Password: in.Password,
		IP:       clientIP(c),
	})
	if err != nil {
		h.fail(c, "注册账号失败", err)
		return
	}
	c.JSON(stdhttp.StatusCreated, userEnvelope{User: privateUser(user)})
}

// Login 处理 POST /api/v1/auth/login（GuestCSRF 之后）：成功时写会话 Cookie。
func (h *Handler) Login(c *gin.Context) {
	var in struct {
		Identifier string `json:"identifier"`
		Password   string `json:"password"`
	}
	if !h.decodeJSON(c, &in) {
		return
	}
	result, err := h.svc.Login(c.Request.Context(), app.LoginInput{
		Identifier: in.Identifier,
		Password:   in.Password,
		IP:         clientIP(c),
	})
	if err != nil {
		h.fail(c, "登录失败", err)
		return
	}
	h.setSessionCookie(c, result.SessionToken, result.ExpiresAt)
	c.JSON(stdhttp.StatusOK, loginResponse{
		User:      privateUser(result.User),
		CSRFToken: result.CSRFToken,
	})
}

// Me 处理 GET /api/v1/auth/me：返回当前账号私有资料与会话 CSRF 令牌。
// 账号行优先复用 ResolveSession 解析会话时 JOIN 读到的结果，不再二次按 ID 查询；
// 仅在上下文缺失该用户行（例如直接构造上下文的单元测试）时回退到 GetUser。
func (h *Handler) Me(c *gin.Context) {
	viewer, ok := httpx.RequireViewer(c)
	if !ok {
		return
	}
	session, ok := httpx.Session(c)
	if !ok {
		httpx.Error(c, stdhttp.StatusUnauthorized, "unauthenticated", "请先登录")
		return
	}
	user, ok := sessionUser(c)
	if !ok {
		var err error
		user, err = h.svc.GetUser(c.Request.Context(), viewer.ID)
		if err != nil {
			h.fail(c, "读取当前账号失败", err)
			return
		}
	}
	c.JSON(stdhttp.StatusOK, meResponse{
		User:      privateUser(user),
		CSRFToken: session.CSRFToken,
	})
}

// Logout 处理 POST /api/v1/auth/logout：吊销当前会话、清 Cookie，返回 204。
func (h *Handler) Logout(c *gin.Context) {
	session, ok := httpx.RequireSession(c)
	if !ok {
		return
	}
	if err := h.svc.Logout(c.Request.Context(), session.ID); err != nil {
		h.fail(c, "退出登录失败", err)
		return
	}
	h.clearSessionCookie(c)
	c.Status(stdhttp.StatusNoContent)
}

// ChangePassword 处理 POST /api/v1/auth/password：成功后保留当前会话、吊销其他会话，返回 204。
func (h *Handler) ChangePassword(c *gin.Context) {
	viewer, ok := httpx.RequireViewer(c)
	if !ok {
		return
	}
	session, ok := httpx.RequireSession(c)
	if !ok {
		return
	}
	var in struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if !h.decodeJSON(c, &in) {
		return
	}
	err := h.svc.ChangePassword(c.Request.Context(), app.ChangePasswordInput{
		UserID:      viewer.ID,
		SessionID:   session.ID,
		OldPassword: in.OldPassword,
		NewPassword: in.NewPassword,
	})
	if err != nil {
		h.fail(c, "修改密码失败", err)
		return
	}
	c.Status(stdhttp.StatusNoContent)
}
