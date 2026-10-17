package contracts

import (
	"context"
	"time"
)

// 审计动作类型（admin_audit_logs.action_type）。
// 与 platform/audit/actions.go 保持一致；本包复制常量是为了让模块 app 不 import
// platform/audit 也能表达审计动作（后续可让 platform/audit 引用本包常量消除重复）。
//
// 消费者：content、social、media、ai、agent、admin。
const (
	AuditActionUserSuspend           = "user_suspend"
	AuditActionUserUnsuspend         = "user_unsuspend"
	AuditActionUserResetPwd          = "user_reset_password"
	AuditActionUserGrantAdmin        = "user_grant_admin"
	AuditActionUserRevokeAdmin       = "user_revoke_admin"
	AuditActionPostDelete            = "post_delete"
	AuditActionCommentDelete         = "comment_delete"
	AuditActionAttachmentDelete      = "attachment_delete"
	AuditActionProviderCreate        = "provider_create"
	AuditActionProviderUpdate        = "provider_update"
	AuditActionProviderDelete        = "provider_delete"
	AuditActionAICreate              = "ai_create"
	AuditActionAIUpdate              = "ai_update"
	AuditActionAIModelUpdate         = "ai_model_update"
	AuditActionAIBehaviorUpdate      = "ai_behavior_update"
	AuditActionAIEnable              = "ai_enable"
	AuditActionAIDisable             = "ai_disable"
	AuditActionAIGenerate            = "ai_generate"
	AuditActionAIPublish             = "ai_publish"
	AuditActionMemoryCreate          = "memory_create"
	AuditActionMemoryUpdate          = "memory_update"
	AuditActionMemoryDelete          = "memory_delete"
	AuditActionMemoryClear           = "memory_clear"
	AuditActionTaskRetry             = "task_retry"
	AuditActionTaskCancel            = "task_cancel"
	AuditActionGlobalAIStop          = "ai_global_stop"
	AuditActionGlobalAIStart         = "ai_global_start"
	AuditActionSettingsUpdate        = "settings_update"
	AuditActionRegistrationUpdate    = "settings_registration_update"
	AuditActionWebSearchSettingsUpdt = "web_search_settings_update"
)

// AuditEntry 是一条待写入的管理审计记录。
// At 必须由调用方用应用时钟生成，且应与业务变更共用同一个 now。
type AuditEntry struct {
	AdminID    int64
	Action     string
	TargetType string
	TargetID   *int64
	Detail     any
	At         time.Time
}

// AuditWriter 写入管理审计日志；实现方通过 ctx 复用调用方事务，
// 保证审计与业务变更同生共死。
//
// 消费者：content、social、media、ai、agent、admin。
// 实现方：platform/audit 的 Writer（组合根注入轻量适配器）。
type AuditWriter interface {
	Write(ctx context.Context, entry AuditEntry) error
}
