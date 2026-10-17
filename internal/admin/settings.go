package admin

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/audit"
	"github.com/great-magician-01/timeline/internal/auth"
	"github.com/great-magician-01/timeline/internal/settings"
)

// settingsPatch 是管理后台提交的设置项；全部使用指针以支持部分更新。
type settingsPatch struct {
	RegistrationEnabled *bool `json:"registration_enabled"`
	AIGlobalEnabled     *bool `json:"ai_global_enabled"`

	MaxUploadBytes               *int64 `json:"max_upload_bytes"`
	MaxRequestBytes              *int64 `json:"max_request_bytes"`
	StorageQuotaBytes            *int64 `json:"storage_quota_bytes"`
	PostRateLimit                *int   `json:"post_rate_limit"`
	PostRateWindowSeconds        *int   `json:"post_rate_window_seconds"`
	UploadRateLimit              *int   `json:"upload_rate_limit"`
	UploadRateWindowSeconds      *int   `json:"upload_rate_window_seconds"`
	TempAttachmentRetentionHours *int   `json:"temp_attachment_retention_hours"`
	CallLogMaxBytes              *int   `json:"call_log_max_bytes"`
	CallLogRetentionDays         *int   `json:"call_log_retention_days"`
}

// empty 判断请求是否没有携带任何设置项。
func (p settingsPatch) empty() bool {
	return p.RegistrationEnabled == nil && p.AIGlobalEnabled == nil &&
		p.MaxUploadBytes == nil && p.MaxRequestBytes == nil && p.StorageQuotaBytes == nil &&
		p.PostRateLimit == nil && p.PostRateWindowSeconds == nil &&
		p.UploadRateLimit == nil && p.UploadRateWindowSeconds == nil &&
		p.TempAttachmentRetentionHours == nil && p.CallLogMaxBytes == nil &&
		p.CallLogRetentionDays == nil
}

// applySettingsPatch 把部分更新合并到当前设置快照（纯函数，便于单元测试）。
// 未提交的字段保留当前值；单文件上限提高而请求上限未提交时，自动把请求上限
// 抬到“文件上限 + multipart 余量”，与 settings.Load 的兜底规则保持一致。
func applySettingsPatch(current settings.Values, patch settingsPatch) settings.Values {
	values := current
	if patch.MaxUploadBytes != nil {
		values.MaxUploadBytes = *patch.MaxUploadBytes
	}
	if patch.MaxRequestBytes != nil {
		values.MaxRequestBytes = *patch.MaxRequestBytes
	} else if values.MaxRequestBytes < values.MaxUploadBytes+settings.DefaultUploadOverhead {
		values.MaxRequestBytes = values.MaxUploadBytes + settings.DefaultUploadOverhead
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

// settingsFieldNames 把内部设置键映射为管理接口字段名，供 validation_failed 详情使用。
var settingsFieldNames = map[string]string{
	settings.KeyUploadMaxFileBytes:          "max_upload_bytes",
	settings.KeyUploadMaxRequestBytes:       "max_request_bytes",
	settings.KeyStorageQuotaBytes:           "storage_quota_bytes",
	settings.KeyPostRateLimit:               "post_rate_limit",
	settings.KeyPostRateWindowSeconds:       "post_rate_window_seconds",
	settings.KeyUploadRateLimit:             "upload_rate_limit",
	settings.KeyUploadRateWindowSeconds:     "upload_rate_window_seconds",
	settings.KeyTempAttachmentRetentionHour: "temp_attachment_retention_hours",
	settings.KeyCallLogMaxBytes:             "call_log_max_bytes",
	settings.KeyCallLogRetentionDays:        "call_log_retention_days",
}

// mapSettingDetails 把 settings.Values.Validate 的键名转换为请求字段名。
func mapSettingDetails(details map[string]string) gin.H {
	mapped := make(gin.H, len(details))
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

// GetSettings 返回全部可配置设置项：数值字段来自 settings.Values，
// registration_enabled / ai_global_enabled 保留原有语义。
func (s *Service) GetSettings(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	values, err := settings.Load(c.Request.Context(), pool)
	if err != nil {
		s.internalError(c, "读取系统设置失败", err)
		return
	}
	c.JSON(http.StatusOK, values.ToMap(values.RegistrationEnabled, values.AIGlobalEnabled))
}

// UpdateSettings 更新注册/全局 AI 开关与上传、限频、保留期等数值设置。
// 全部写入与审计在同一事务内完成；全局 AI 停止仍取消 pending 任务并暂停 AI 作业。
func (s *Service) UpdateSettings(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	var in settingsPatch
	if err := api.DecodeJSON(c, &in); err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", "请求体必须是有效的 JSON 对象")
		return
	}
	if in.empty() {
		api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", "至少需要修改一项设置",
			gin.H{"settings": "请求没有包含任何设置项"})
		return
	}
	ctx := c.Request.Context()
	current, err := settings.Load(ctx, pool)
	if err != nil {
		s.internalError(c, "读取系统设置失败", err)
		return
	}
	merged := applySettingsPatch(current, in)
	if details := merged.Validate(); len(details) > 0 {
		api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", "设置项不符合要求",
			mapSettingDetails(details))
		return
	}

	adminUser, _ := auth.CurrentUser(c)
	tx, err := pool.Begin(ctx)
	if err != nil {
		s.internalError(c, "开启系统设置事务失败", err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 数值设置项：只在请求中显式提交时写入，避免覆盖并发修改的其他字段。
	numeric := []struct {
		key   string
		value any
		set   bool
	}{
		{settings.KeyUploadMaxFileBytes, int64(merged.MaxUploadBytes), in.MaxUploadBytes != nil},
		{settings.KeyUploadMaxRequestBytes, int64(merged.MaxRequestBytes), in.MaxRequestBytes != nil ||
			(in.MaxUploadBytes != nil && merged.MaxRequestBytes != current.MaxRequestBytes)},
		{settings.KeyStorageQuotaBytes, int64(merged.StorageQuotaBytes), in.StorageQuotaBytes != nil},
		{settings.KeyPostRateLimit, merged.PostRateLimit, in.PostRateLimit != nil},
		{settings.KeyPostRateWindowSeconds, int(merged.PostRateWindow.Seconds()), in.PostRateWindowSeconds != nil},
		{settings.KeyUploadRateLimit, merged.UploadRateLimit, in.UploadRateLimit != nil},
		{settings.KeyUploadRateWindowSeconds, int(merged.UploadRateWindow.Seconds()), in.UploadRateWindowSeconds != nil},
		{settings.KeyTempAttachmentRetentionHour, int(merged.TempAttachmentRetention.Hours()), in.TempAttachmentRetentionHours != nil},
		{settings.KeyCallLogMaxBytes, merged.CallLogMaxBytes, in.CallLogMaxBytes != nil},
		{settings.KeyCallLogRetentionDays, merged.CallLogRetentionDays, in.CallLogRetentionDays != nil},
	}
	changed := gin.H{}
	for _, item := range numeric {
		if !item.set {
			continue
		}
		if err := settings.Set(ctx, tx, item.key, item.value, adminUser.ID); err != nil {
			s.internalError(c, "更新系统设置失败", err)
			return
		}
		changed[settingsFieldNames[item.key]] = item.value
	}
	if len(changed) > 0 {
		if err := s.insertAudit(ctx, tx, adminUser.ID, audit.ActionSettingsUpdate, "system_settings", nil, changed); err != nil {
			s.internalError(c, "写入系统设置审计失败", err)
			return
		}
	}

	if in.RegistrationEnabled != nil {
		if err := settings.Set(ctx, tx, settings.KeyRegistrationEnabled, *in.RegistrationEnabled, adminUser.ID); err != nil {
			s.internalError(c, "更新注册开关失败", err)
			return
		}
		if err := s.insertAudit(ctx, tx, adminUser.ID, audit.ActionRegistrationUpdate, "system_settings", nil, gin.H{
			"registration_enabled": *in.RegistrationEnabled,
		}); err != nil {
			s.internalError(c, "写入系统设置审计失败", err)
			return
		}
	}

	if in.AIGlobalEnabled != nil {
		if err := settings.Set(ctx, tx, settings.KeyAIGlobalEnabled, *in.AIGlobalEnabled, adminUser.ID); err != nil {
			s.internalError(c, "更新全局 AI 开关失败", err)
			return
		}
		action := audit.ActionGlobalAIStop
		if *in.AIGlobalEnabled {
			action = audit.ActionGlobalAIStart
		}
		if err := s.insertAudit(ctx, tx, adminUser.ID, action, "system_settings", nil, gin.H{
			"enabled": *in.AIGlobalEnabled,
		}); err != nil {
			s.internalError(c, "写入全局 AI 开关审计失败", err)
			return
		}
		if !*in.AIGlobalEnabled {
			// 全局停止：取消所有待执行的 AI 任务与 AI 作业（不触碰已完成与运行中的记录）。
			if _, err = tx.Exec(ctx, `
				UPDATE ai_tasks SET status = 'canceled', finished_at = now()
				WHERE status = 'pending'`); err != nil {
				s.internalError(c, "取消待执行 AI 任务失败", err)
				return
			}
			if _, err = tx.Exec(ctx, `
				UPDATE background_jobs SET status = 'paused', updated_at = now()
				WHERE job_type IN ('ai_task', 'ai_schedule', 'memory_consolidation') AND status = 'pending'`); err != nil {
				s.internalError(c, "暂停 AI 后台作业失败", err)
				return
			}
		}
	}

	if err = tx.Commit(ctx); err != nil {
		s.internalError(c, "提交系统设置事务失败", err)
		return
	}

	values, err := settings.Load(ctx, pool)
	if err != nil {
		s.internalError(c, "读取系统设置失败", err)
		return
	}
	c.JSON(http.StatusOK, values.ToMap(values.RegistrationEnabled, values.AIGlobalEnabled))
}
