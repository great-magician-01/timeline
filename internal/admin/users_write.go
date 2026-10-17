package admin

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/auth"
)

func (s *Service) SuspendUser(c *gin.Context)   { s.changeUserStatus(c, true) }
func (s *Service) UnsuspendUser(c *gin.Context) { s.changeUserStatus(c, false) }

func (s *Service) changeUserStatus(c *gin.Context, suspend bool) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	targetID, err := api.ParseID(c.Param("id"))
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	adminUser, _ := auth.CurrentUser(c)
	if suspend && targetID == adminUser.ID {
		api.Error(c, http.StatusBadRequest, "validation_failed", "不能停用自己的管理员账号")
		return
	}

	tx, err := pool.Begin(c.Request.Context())
	if err != nil {
		s.internalError(c, "开启账号状态事务失败", err)
		return
	}
	defer func() { _ = tx.Rollback(c.Request.Context()) }()

	target, err := auth.UserByID(c.Request.Context(), tx, targetID, true)
	if errors.Is(err, pgx.ErrNoRows) {
		api.Error(c, http.StatusNotFound, "not_found", "用户不存在")
		return
	}
	if err != nil {
		s.internalError(c, "读取待处置账号失败", err)
		return
	}
	// 超级管理员账号只能由超级管理员本人处置，普通管理员一律 403。
	if target.IsSuperAdmin && !adminUser.IsSuperAdmin {
		api.Error(c, http.StatusForbidden, "forbidden", "不能操作超级管理员账号")
		return
	}
	desired := auth.StatusActive
	action := "user_unsuspend"
	if suspend {
		desired = auth.StatusSuspended
		action = "user_suspend"
	}
	changed := target.Status != desired
	newTokenVersion := target.TokenVersion
	if suspend && changed {
		newTokenVersion++
	}
	updated := target
	if changed {
		updated, err = auth.ScanUser(tx.QueryRow(c.Request.Context(), `
			UPDATE users
			SET status = $2,
			    suspended_at = CASE WHEN $2 = 'suspended' THEN now() ELSE NULL END,
			    token_version = $3,
			    updated_at = now()
			WHERE id = $1
			RETURNING `+auth.UserColumns, targetID, desired, newTokenVersion,
		))
		if err != nil {
			s.internalError(c, "更新账号状态失败", err)
			return
		}
	}
	if suspend {
		if _, err = tx.Exec(c.Request.Context(), `
			UPDATE auth_sessions SET revoked_at = now()
			WHERE user_id = $1 AND revoked_at IS NULL`, targetID); err != nil {
			s.internalError(c, "吊销停用账号会话失败", err)
			return
		}
		// 未执行的定时发布任务进入 paused，不再自动发布（docs/WORKFLOWS.md 3.3、10.1）。
		// 逻辑状态变更，不删除作业记录；重新启用后不自动补发。
		if _, err = tx.Exec(c.Request.Context(), `
			UPDATE background_jobs SET status = 'paused', updated_at = now()
			WHERE job_type = 'post_publish' AND status = 'pending'
			  AND ref_type = 'post'
			  AND ref_id IN (SELECT id FROM posts WHERE author_id = $1)`, targetID); err != nil {
			s.internalError(c, "暂停停用账号的定时发布任务失败", err)
			return
		}
		if target.IsAI {
			// AI 账号停用时同样暂停其待执行的 AI 作业与任务
			if _, err = tx.Exec(c.Request.Context(), `
				UPDATE background_jobs SET status = 'paused', updated_at = now()
				WHERE status = 'pending' AND ref_type = 'ai' AND ref_id = $1`, targetID); err != nil {
				s.internalError(c, "暂停停用 AI 的后台作业失败", err)
				return
			}
			if _, err = tx.Exec(c.Request.Context(), `
				UPDATE ai_tasks SET status = 'canceled', finished_at = now()
				WHERE ai_id = $1 AND status = 'pending'`, targetID); err != nil {
				s.internalError(c, "取消停用 AI 的待执行任务失败", err)
				return
			}
		}
	}
	if err = s.insertAudit(c.Request.Context(), tx, adminUser.ID, action, "user", &targetID, gin.H{
		"previous_status": target.Status,
		"changed":         changed,
	}); err != nil {
		s.internalError(c, "写入账号状态审计失败", err)
		return
	}
	if err = tx.Commit(c.Request.Context()); err != nil {
		s.internalError(c, "提交账号状态事务失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": auth.PrivateUser(updated)})
}

func (s *Service) ResetPassword(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	targetID, err := api.ParseID(c.Param("id"))
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	var in struct {
		NewPassword string `json:"new_password"`
	}
	if err := api.DecodeJSON(c, &in); err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", "请求体必须是有效的 JSON 对象")
		return
	}
	if err := auth.ValidatePassword(in.NewPassword); err != nil {
		api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", "新密码不符合要求", gin.H{"new_password": err.Error()})
		return
	}
	newHash, err := auth.HashPassword(in.NewPassword)
	if err != nil {
		api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", "新密码不符合要求", gin.H{"new_password": err.Error()})
		return
	}
	adminUser, _ := auth.CurrentUser(c)
	tx, err := pool.Begin(c.Request.Context())
	if err != nil {
		s.internalError(c, "开启重置密码事务失败", err)
		return
	}
	defer func() { _ = tx.Rollback(c.Request.Context()) }()

	target, err := auth.UserByID(c.Request.Context(), tx, targetID, true)
	if errors.Is(err, pgx.ErrNoRows) {
		api.Error(c, http.StatusNotFound, "not_found", "用户不存在")
		return
	}
	if err != nil {
		s.internalError(c, "读取待重置账号失败", err)
		return
	}
	// 超级管理员账号只能由超级管理员本人处置，普通管理员一律 403。
	if target.IsSuperAdmin && !adminUser.IsSuperAdmin {
		api.Error(c, http.StatusForbidden, "forbidden", "不能操作超级管理员账号")
		return
	}
	if target.IsAI {
		api.Error(c, http.StatusBadRequest, "validation_failed", "AI 账号没有密码，不能重置")
		return
	}
	if _, err = tx.Exec(c.Request.Context(), `
		UPDATE users
		SET password_hash = $2, token_version = token_version + 1, updated_at = now()
		WHERE id = $1`, targetID, newHash); err != nil {
		s.internalError(c, "管理员重置密码失败", err)
		return
	}
	if _, err = tx.Exec(c.Request.Context(), `
		UPDATE auth_sessions SET revoked_at = now()
		WHERE user_id = $1 AND revoked_at IS NULL`, targetID); err != nil {
		s.internalError(c, "吊销重置账号会话失败", err)
		return
	}
	if err = s.insertAudit(c.Request.Context(), tx, adminUser.ID, "user_reset_password", "user", &targetID, gin.H{}); err != nil {
		s.internalError(c, "写入重置密码审计失败", err)
		return
	}
	if err = tx.Commit(c.Request.Context()); err != nil {
		s.internalError(c, "提交重置密码事务失败", err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Service) insertAudit(
	ctx context.Context,
	tx pgx.Tx,
	adminID int64,
	action, targetType string,
	targetID *int64,
	detail any,
) error {
	id, err := s.ids.Next()
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO admin_audit_logs (
			id, admin_id, action_type, target_type, target_id, detail
		) VALUES ($1, $2, $3, $4, $5, $6)`, id, adminID, action, targetType, targetID, detail,
	)
	return err
}
