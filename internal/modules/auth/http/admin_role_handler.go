package http

import (
	stdhttp "net/http"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// GrantAdmin 处理 POST /api/v1/admin/users/:id/grant-admin（超级管理员接口）。
func (h *Handler) GrantAdmin(c *gin.Context) { h.changeAdminRole(c, true) }

// RevokeAdmin 处理 POST /api/v1/admin/users/:id/revoke-admin（超级管理员接口）。
func (h *Handler) RevokeAdmin(c *gin.Context) { h.changeAdminRole(c, false) }

// changeAdminRole 授予或撤销目标账号的管理员角色：路由链已由 RequireSuperAdmin
// 把关，app 层在事务内再次复核操作者权限并写审计；幂等场景不写审计。
func (h *Handler) changeAdminRole(c *gin.Context, grant bool) {
	viewer, ok := httpx.RequireViewer(c)
	if !ok {
		return
	}
	targetID, err := httpx.ParseID(c.Param("id"))
	if err != nil {
		httpx.Error(c, stdhttp.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	user, err := h.svc.SetAdminRole(c.Request.Context(), viewer.ID, targetID, grant)
	if err != nil {
		h.fail(c, "变更管理员角色失败", err)
		return
	}
	c.JSON(stdhttp.StatusOK, userEnvelope{User: privateUser(user)})
}
