package app

import (
	"context"
	"time"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	platformsettings "github.com/great-magician-01/timeline/internal/platform/settings"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// GetSettings 读取全部可配置系统设置。
func (s *Service) GetSettings(ctx context.Context) (SettingsSnapshot, error) {
	values, err := s.settings.Settings(ctx)
	if err != nil {
		return SettingsSnapshot{}, wrapInternal(err)
	}
	return values, nil
}

// UpdateSettings 部分更新系统设置：数值项、注册开关与全局 AI 开关分别写库并分别审计。
// 注册开关审计在本用例写；全局 AI 开关的审计由注入的 GlobalAIHalter（agent SetGlobal）
// 在同一事务内写入，本用例不再重复写。
func (s *Service) UpdateSettings(ctx context.Context, actor model.Viewer, patch SettingsPatch) (SettingsSnapshot, error) {
	if patch.Empty() {
		return SettingsSnapshot{}, apperr.Validation("至少需要修改一项设置").
			WithDetails(map[string]string{"settings": "请求没有包含任何设置项"})
	}
	now := s.now()
	var snapshot SettingsSnapshot
	err := s.tx.WithTx(ctx, func(txCtx context.Context) error {
		current, err := s.settings.Settings(txCtx)
		if err != nil {
			return wrapInternal(err)
		}
		merged := applySettingsPatch(current, patch)
		if details := validateSettings(merged); len(details) > 0 {
			return apperr.Validation("设置项不符合要求").WithDetails(mapSettingDetails(details))
		}

		// 数值设置项：只在请求中显式提交（或按规则联动抬升）时写入。
		writes := []struct {
			key   string
			field string
			value any
			set   bool
		}{
			{contracts.SettingKeyUploadMaxFileBytes, "max_upload_bytes", merged.MaxUploadBytes, patch.MaxUploadBytes != nil},
			{contracts.SettingKeyUploadMaxRequestBytes, "max_request_bytes", merged.MaxRequestBytes,
				patch.MaxRequestBytes != nil ||
					(patch.MaxUploadBytes != nil && merged.MaxRequestBytes != current.MaxRequestBytes)},
			{contracts.SettingKeyStorageQuotaBytes, "storage_quota_bytes", merged.StorageQuotaBytes, patch.StorageQuotaBytes != nil},
			{contracts.SettingKeyPostRateLimit, "post_rate_limit", merged.PostRateLimit, patch.PostRateLimit != nil},
			{contracts.SettingKeyPostRateWindowSeconds, "post_rate_window_seconds", int(merged.PostRateWindow.Seconds()), patch.PostRateWindowSeconds != nil},
			{contracts.SettingKeyUploadRateLimit, "upload_rate_limit", merged.UploadRateLimit, patch.UploadRateLimit != nil},
			{contracts.SettingKeyUploadRateWindowSecs, "upload_rate_window_seconds", int(merged.UploadRateWindow.Seconds()), patch.UploadRateWindowSeconds != nil},
			{contracts.SettingKeyTempAttachmentRetainHr, "temp_attachment_retention_hours", int(merged.TempAttachmentRetention.Hours()), patch.TempAttachmentRetentionHours != nil},
			{contracts.SettingKeyCallLogMaxBytes, "call_log_max_bytes", merged.CallLogMaxBytes, patch.CallLogMaxBytes != nil},
			{contracts.SettingKeyCallLogRetentionDays, "call_log_retention_days", merged.CallLogRetentionDays, patch.CallLogRetentionDays != nil},
		}
		changed := map[string]any{}
		for _, item := range writes {
			if !item.set {
				continue
			}
			if err := s.settings.Set(txCtx, contracts.SettingsWrite{
				Key: item.key, Value: item.value, AdminID: actor.ID, At: now,
			}); err != nil {
				return wrapInternal(err)
			}
			changed[item.field] = item.value
		}
		if len(changed) > 0 {
			if err := s.audit.Write(txCtx, contracts.AuditEntry{
				AdminID:    actor.ID,
				Action:     contracts.AuditActionSettingsUpdate,
				TargetType: "system_settings",
				Detail:     changed,
				At:         now,
			}); err != nil {
				return err
			}
		}

		if patch.RegistrationEnabled != nil {
			if err := s.settings.Set(txCtx, contracts.SettingsWrite{
				Key: contracts.SettingKeyRegistrationEnabled, Value: *patch.RegistrationEnabled,
				AdminID: actor.ID, At: now,
			}); err != nil {
				return wrapInternal(err)
			}
			if err := s.audit.Write(txCtx, contracts.AuditEntry{
				AdminID:    actor.ID,
				Action:     contracts.AuditActionRegistrationUpdate,
				TargetType: "system_settings",
				Detail:     map[string]any{"registration_enabled": *patch.RegistrationEnabled},
				At:         now,
			}); err != nil {
				return err
			}
		}

		if patch.AIGlobalEnabled != nil {
			if err := s.settings.Set(txCtx, contracts.SettingsWrite{
				Key: contracts.SettingKeyAIGlobalEnabled, Value: *patch.AIGlobalEnabled,
				AdminID: actor.ID, At: now,
			}); err != nil {
				return wrapInternal(err)
			}
			// 全局 AI 开关的审计由 agent 的 SetGlobal 写入（唯一写入方）：停止时暂停作业，
			// 启动时只写开关与审计；admin 侧不再重复写审计。未注入端口时退回仅写开关。
			if s.globalAI != nil {
				if *patch.AIGlobalEnabled {
					if err := s.globalAI.EnableGlobalAI(txCtx, actor.ID, now); err != nil {
						return err
					}
				} else if err := s.globalAI.HaltGlobalAI(txCtx, actor.ID, now); err != nil {
					return err
				}
			}
		}

		snapshot, err = s.settings.Settings(txCtx)
		if err != nil {
			return wrapInternal(err)
		}
		return nil
	})
	if err != nil {
		return SettingsSnapshot{}, err
	}
	return snapshot, nil
}

// applySettingsPatch 把部分更新合并到当前设置快照（纯函数，便于单元测试）：
// 未提交的字段保留当前值；单文件上限提高而请求上限未提交时，自动把请求上限
// 抬到「文件上限 + multipart 余量」，与 platform/settings 的兜底规则一致。
func applySettingsPatch(current SettingsSnapshot, patch SettingsPatch) SettingsSnapshot {
	values := current
	if patch.MaxUploadBytes != nil {
		values.MaxUploadBytes = *patch.MaxUploadBytes
	}
	if patch.MaxRequestBytes != nil {
		values.MaxRequestBytes = *patch.MaxRequestBytes
	} else if values.MaxRequestBytes < values.MaxUploadBytes+platformsettings.DefaultUploadOverhead {
		values.MaxRequestBytes = values.MaxUploadBytes + platformsettings.DefaultUploadOverhead
	}
	if patch.StorageQuotaBytes != nil {
		values.StorageQuotaBytes = *patch.StorageQuotaBytes
	}
	if patch.PostRateLimit != nil {
		values.PostRateLimit = *patch.PostRateLimit
	}
	if patch.PostRateWindowSeconds != nil {
		values.PostRateWindow = secondsDuration(*patch.PostRateWindowSeconds)
	}
	if patch.UploadRateLimit != nil {
		values.UploadRateLimit = *patch.UploadRateLimit
	}
	if patch.UploadRateWindowSeconds != nil {
		values.UploadRateWindow = secondsDuration(*patch.UploadRateWindowSeconds)
	}
	if patch.TempAttachmentRetentionHours != nil {
		values.TempAttachmentRetention = hoursDuration(*patch.TempAttachmentRetentionHours)
	}
	if patch.CallLogMaxBytes != nil {
		values.CallLogMaxBytes = *patch.CallLogMaxBytes
	}
	if patch.CallLogRetentionDays != nil {
		values.CallLogRetentionDays = *patch.CallLogRetentionDays
	}
	return values
}

// validateSettings 校验合并后的设置；返回 设置键 → 错误文案。
func validateSettings(v SettingsSnapshot) map[string]string {
	details := map[string]string{}
	if v.MaxUploadBytes <= 0 {
		details[contracts.SettingKeyUploadMaxFileBytes] = "单文件上限必须大于 0"
	}
	if v.MaxRequestBytes < v.MaxUploadBytes+platformsettings.DefaultUploadOverhead {
		details[contracts.SettingKeyUploadMaxRequestBytes] = "单请求上限必须至少比单文件上限多 1 MiB"
	}
	if v.StorageQuotaBytes <= 0 {
		details[contracts.SettingKeyStorageQuotaBytes] = "存储配额必须大于 0"
	}
	if v.PostRateLimit <= 0 {
		details[contracts.SettingKeyPostRateLimit] = "发帖限频次数必须大于 0"
	}
	if v.PostRateWindow < time.Second {
		details[contracts.SettingKeyPostRateWindowSeconds] = "发帖限频窗口至少为 1 秒"
	}
	if v.UploadRateLimit <= 0 {
		details[contracts.SettingKeyUploadRateLimit] = "上传限频次数必须大于 0"
	}
	if v.UploadRateWindow < time.Second {
		details[contracts.SettingKeyUploadRateWindowSecs] = "上传限频窗口至少为 1 秒"
	}
	if v.TempAttachmentRetention < time.Hour {
		details[contracts.SettingKeyTempAttachmentRetainHr] = "临时附件保留期至少为 1 小时"
	}
	if v.CallLogMaxBytes <= 0 {
		details[contracts.SettingKeyCallLogMaxBytes] = "调用记录截断上限必须大于 0"
	}
	if v.CallLogRetentionDays < 0 {
		details[contracts.SettingKeyCallLogRetentionDays] = "调用记录保留期不能为负数"
	}
	return details
}

// settingsFieldNames 把内部设置键映射为管理接口字段名，供 validation_failed 详情使用。
var settingsFieldNames = map[string]string{
	contracts.SettingKeyUploadMaxFileBytes:     "max_upload_bytes",
	contracts.SettingKeyUploadMaxRequestBytes:  "max_request_bytes",
	contracts.SettingKeyStorageQuotaBytes:      "storage_quota_bytes",
	contracts.SettingKeyPostRateLimit:          "post_rate_limit",
	contracts.SettingKeyPostRateWindowSeconds:  "post_rate_window_seconds",
	contracts.SettingKeyUploadRateLimit:        "upload_rate_limit",
	contracts.SettingKeyUploadRateWindowSecs:   "upload_rate_window_seconds",
	contracts.SettingKeyTempAttachmentRetainHr: "temp_attachment_retention_hours",
	contracts.SettingKeyCallLogMaxBytes:        "call_log_max_bytes",
	contracts.SettingKeyCallLogRetentionDays:   "call_log_retention_days",
}

// mapSettingDetails 把设置键详情转换为请求字段名详情。
func mapSettingDetails(details map[string]string) map[string]string {
	mapped := make(map[string]string, len(details))
	for key, message := range details {
		field := key
		if name, ok := settingsFieldNames[key]; ok {
			field = name
		}
		mapped[field] = message
	}
	return mapped
}

// 时间单位换算（纯函数，便于校验边界值）。
func secondsDuration(seconds int) time.Duration {
	return time.Duration(seconds) * time.Second
}

func hoursDuration(hours int) time.Duration {
	return time.Duration(hours) * time.Hour
}
