// Package audit 统一写入管理审计日志（docs/DATA_MODEL.md 6.29）。
// 管理处置（账号启停、内容删除、AI 与预设变更、记忆管理、全局开关）都必须在
// 同一事务内写入审计记录，便于追溯“谁在什么时候改了什么”。
package audit

import (
	"context"

	"github.com/great-magician-01/timeline/internal/access"
)

// IDGenerator 与 auth.IDGenerator 同形，避免包之间相互依赖。
type IDGenerator interface {
	Next() (int64, error)
}

// 审计动作类型。
const (
	ActionUserSuspend      = "user_suspend"
	ActionUserUnsuspend    = "user_unsuspend"
	ActionUserResetPwd     = "user_reset_password"
	ActionUserGrantAdmin   = "user_grant_admin"
	ActionUserRevokeAdmin  = "user_revoke_admin"
	ActionPostDelete       = "post_delete"
	ActionCommentDelete    = "comment_delete"
	ActionAttachmentDelete = "attachment_delete"
	ActionProviderCreate   = "provider_create"
	ActionProviderUpdate   = "provider_update"
	ActionProviderDelete   = "provider_delete"
	ActionAICreate         = "ai_create"
	ActionAIUpdate         = "ai_update"
	ActionAIModelUpdate    = "ai_model_update"
	ActionAIBehaviorUpdate = "ai_behavior_update"
	ActionAIEnable         = "ai_enable"
	ActionAIDisable        = "ai_disable"
	ActionAIGenerate       = "ai_generate"
	ActionAIPublish        = "ai_publish"
	ActionMemoryCreate     = "memory_create"
	ActionMemoryUpdate     = "memory_update"
	ActionMemoryDelete     = "memory_delete"
	ActionMemoryClear      = "memory_clear"
	ActionTaskRetry        = "task_retry"
	ActionTaskCancel       = "task_cancel"
	ActionGlobalAIStop     = "ai_global_stop"
	ActionGlobalAIStart    = "ai_global_start"
	ActionSettingsUpdate   = "settings_update"
	// ActionRegistrationUpdate 是注册开关的独立动作，便于后台按动作筛选。
	ActionRegistrationUpdate = "settings_registration_update"
)

// Write 写入一条审计记录；必须传入当前事务，保证与业务变更同生共死。
func Write(
	ctx context.Context,
	q access.Querier,
	ids IDGenerator,
	adminID int64,
	action, targetType string,
	targetID *int64,
	detail any,
) error {
	id, err := ids.Next()
	if err != nil {
		return err
	}
	if detail == nil {
		detail = map[string]any{}
	}
	_, err = q.Exec(ctx, `
		INSERT INTO admin_audit_logs (id, admin_id, action_type, target_type, target_id, detail)
		VALUES ($1, $2, $3, $4, $5, $6)`, id, adminID, action, targetType, targetID, detail)
	return err
}
