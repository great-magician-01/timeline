package admin

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/audit"
	"github.com/great-magician-01/timeline/internal/auth"
)

func (s *Service) GetSettings(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	enabled, err := auth.RegistrationEnabled(c.Request.Context(), pool)
	if err != nil {
		s.internalError(c, "读取系统设置失败", err)
		return
	}
	globalAI, err := GlobalAIEnabled(c.Request.Context(), pool)
	if err != nil {
		s.internalError(c, "读取全局 AI 开关失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"registration_enabled": enabled,
		"ai_global_enabled":    globalAI,
	})
}

func (s *Service) UpdateSettings(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	var in struct {
		RegistrationEnabled *bool `json:"registration_enabled"`
		AIGlobalEnabled     *bool `json:"ai_global_enabled"`
	}
	if err := api.DecodeJSON(c, &in); err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", "请求体必须是有效的 JSON 对象")
		return
	}
	if in.RegistrationEnabled == nil && in.AIGlobalEnabled == nil {
		api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", "至少需要修改一项设置", gin.H{
			"registration_enabled": "注册开关与全局 AI 开关不能同时为空",
		})
		return
	}
	adminUser, _ := auth.CurrentUser(c)
	tx, err := pool.Begin(c.Request.Context())
	if err != nil {
		s.internalError(c, "开启系统设置事务失败", err)
		return
	}
	defer func() { _ = tx.Rollback(c.Request.Context()) }()

	if in.RegistrationEnabled != nil {
		if _, err = tx.Exec(c.Request.Context(), `
			INSERT INTO system_settings (key, value, updated_by, updated_at)
			VALUES ('registration.enabled', to_jsonb($1::boolean), $2, now())
			ON CONFLICT (key) DO UPDATE
			SET value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = now()`,
			*in.RegistrationEnabled, adminUser.ID,
		); err != nil {
			s.internalError(c, "更新注册开关失败", err)
			return
		}
		if err = s.insertAudit(c.Request.Context(), tx, adminUser.ID, audit.ActionRegistrationUpdate, "system_settings", nil, gin.H{
			"registration_enabled": *in.RegistrationEnabled,
		}); err != nil {
			s.internalError(c, "写入系统设置审计失败", err)
			return
		}
	}

	if in.AIGlobalEnabled != nil {
		if _, err = tx.Exec(c.Request.Context(), `
			INSERT INTO system_settings (key, value, updated_by, updated_at)
			VALUES ($1, to_jsonb($2::boolean), $3, now())
			ON CONFLICT (key) DO UPDATE
			SET value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = now()`,
			globalAIKey, *in.AIGlobalEnabled, adminUser.ID,
		); err != nil {
			s.internalError(c, "更新全局 AI 开关失败", err)
			return
		}
		action := audit.ActionGlobalAIStop
		if *in.AIGlobalEnabled {
			action = audit.ActionGlobalAIStart
		}
		if err = s.insertAudit(c.Request.Context(), tx, adminUser.ID, action, "system_settings", nil, gin.H{
			"enabled": *in.AIGlobalEnabled,
		}); err != nil {
			s.internalError(c, "写入全局 AI 开关审计失败", err)
			return
		}
		if !*in.AIGlobalEnabled {
			// 全局停止：取消所有待执行的 AI 任务与 AI 作业（不触碰已完成与运行中的记录）
			if _, err = tx.Exec(c.Request.Context(), `
				UPDATE ai_tasks SET status = 'canceled', finished_at = now()
				WHERE status = 'pending'`); err != nil {
				s.internalError(c, "取消待执行 AI 任务失败", err)
				return
			}
			if _, err = tx.Exec(c.Request.Context(), `
				UPDATE background_jobs SET status = 'paused', updated_at = now()
				WHERE job_type IN ('ai_task', 'ai_schedule', 'memory_consolidation') AND status = 'pending'`); err != nil {
				s.internalError(c, "暂停 AI 后台作业失败", err)
				return
			}
		}
	}

	if err = tx.Commit(c.Request.Context()); err != nil {
		s.internalError(c, "提交系统设置事务失败", err)
		return
	}

	registration, err := auth.RegistrationEnabled(c.Request.Context(), pool)
	if err != nil {
		s.internalError(c, "读取注册开关失败", err)
		return
	}
	globalAI, err := GlobalAIEnabled(c.Request.Context(), pool)
	if err != nil {
		s.internalError(c, "读取全局 AI 开关失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"registration_enabled": registration,
		"ai_global_enabled":    globalAI,
	})
}
