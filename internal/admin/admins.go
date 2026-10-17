package admin

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/audit"
	"github.com/great-magician-01/timeline/internal/auth"
)

func (s *Service) GrantAdmin(c *gin.Context)  { s.changeAdminRole(c, true) }
func (s *Service) RevokeAdmin(c *gin.Context) { s.changeAdminRole(c, false) }

// changeAdminRole 授予或撤销目标人类账号的管理员角色，仅超级管理员可执行。
// 目标行加锁后复核操作者权限，角色变更与审计在同一事务内提交；
// 幂等场景（已是/已不是管理员）直接返回当前账号且不写审计。
func (s *Service) changeAdminRole(c *gin.Context, grant bool) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	targetID, err := api.ParseID(c.Param("id"))
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	current, _ := auth.CurrentUser(c)

	tx, err := pool.Begin(c.Request.Context())
	if err != nil {
		s.internalError(c, "开启管理员角色事务失败", err)
		return
	}
	defer func() { _ = tx.Rollback(c.Request.Context()) }()

	target, err := auth.UserByID(c.Request.Context(), tx, targetID, true)
	if errors.Is(err, pgx.ErrNoRows) {
		api.Error(c, http.StatusNotFound, "not_found", "用户不存在")
		return
	}
	if err != nil {
		s.internalError(c, "读取待调整账号失败", err)
		return
	}
	// 事务内复核操作者：中间件之外的双保险，避免权限在请求处理期间变化。
	actor, err := auth.UserByID(c.Request.Context(), tx, current.ID, false)
	if errors.Is(err, pgx.ErrNoRows) {
		api.Error(c, http.StatusForbidden, "forbidden", "需要超级管理员权限")
		return
	}
	if err != nil {
		s.internalError(c, "复核操作者权限失败", err)
		return
	}
	if actor.IsAI || actor.Role != auth.RoleAdmin || !actor.IsSuperAdmin {
		api.Error(c, http.StatusForbidden, "forbidden", "需要超级管理员权限")
		return
	}
	if target.IsAI {
		api.Error(c, http.StatusBadRequest, "validation_failed", "AI 账号不能成为管理员")
		return
	}
	if target.ID == actor.ID {
		api.Error(c, http.StatusBadRequest, "validation_failed", "不能修改自己的管理员权限")
		return
	}
	if target.IsSuperAdmin {
		api.Error(c, http.StatusBadRequest, "validation_failed", "不能修改超级管理员的管理员权限")
		return
	}

	desired := auth.RoleUser
	action := audit.ActionUserRevokeAdmin
	if grant {
		desired = auth.RoleAdmin
		action = audit.ActionUserGrantAdmin
	}
	// 目标角色已是期望值时幂等返回，不写审计。
	changed := target.Role != desired
	if grant && changed && target.Status != auth.StatusActive {
		api.Error(c, http.StatusBadRequest, "validation_failed", "已停用账号不能授权为管理员")
		return
	}
	updated := target
	if changed {
		updated, err = auth.ScanUser(tx.QueryRow(c.Request.Context(), `
			UPDATE users
			SET role = $2, updated_at = now()
			WHERE id = $1
			RETURNING `+auth.UserColumns, targetID, desired,
		))
		if err != nil {
			s.internalError(c, "更新管理员角色失败", err)
			return
		}
		if err = s.insertAudit(c.Request.Context(), tx, actor.ID, action, "user", &targetID, gin.H{
			"previous_role": target.Role,
			"new_role":      desired,
		}); err != nil {
			s.internalError(c, "写入管理员角色审计失败", err)
			return
		}
	}
	if err = tx.Commit(c.Request.Context()); err != nil {
		s.internalError(c, "提交管理员角色事务失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": auth.PrivateUser(updated)})
}
