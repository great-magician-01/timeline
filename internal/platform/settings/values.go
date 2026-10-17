package settings

import (
	"encoding/json"
	"fmt"
	"time"
)

// Values 是一次加载得到的系统设置快照。
type Values struct {
	RegistrationEnabled bool
	AIGlobalEnabled     bool

	MaxUploadBytes    int64
	MaxRequestBytes   int64
	StorageQuotaBytes int64
	PostRateLimit     int
	PostRateWindow    time.Duration
	UploadRateLimit   int
	UploadRateWindow  time.Duration

	TempAttachmentRetention time.Duration
	CallLogMaxBytes         int
	CallLogRetentionDays    int
}

// Defaults 返回代码内默认设置。
func Defaults() Values {
	return Values{
		RegistrationEnabled:     true,
		AIGlobalEnabled:         false,
		MaxUploadBytes:          DefaultMaxUploadBytes,
		MaxRequestBytes:         DefaultMaxUploadBytes + DefaultUploadOverhead,
		StorageQuotaBytes:       DefaultStorageQuotaBytes,
		PostRateLimit:           DefaultPostRateLimit,
		PostRateWindow:          DefaultPostRateWindow,
		UploadRateLimit:         DefaultUploadRateLimit,
		UploadRateWindow:        DefaultUploadRateWindow,
		TempAttachmentRetention: DefaultTempRetention,
		CallLogMaxBytes:         DefaultCallLogMaxBytes,
		CallLogRetentionDays:    DefaultCallLogRetentionDays,
	}
}

func apply(v *Values, key string, raw []byte) {
	switch key {
	case KeyRegistrationEnabled:
		if value, ok := boolValue(raw); ok {
			v.RegistrationEnabled = value
		}
	case KeyAIGlobalEnabled:
		if value, ok := boolValue(raw); ok {
			v.AIGlobalEnabled = value
		}
	case KeyUploadMaxFileBytes:
		if value, ok := int64Value(raw); ok && value > 0 {
			v.MaxUploadBytes = value
		}
	case KeyUploadMaxRequestBytes:
		if value, ok := int64Value(raw); ok && value > 0 {
			v.MaxRequestBytes = value
		}
	case KeyStorageQuotaBytes:
		if value, ok := int64Value(raw); ok && value > 0 {
			v.StorageQuotaBytes = value
		}
	case KeyPostRateLimit:
		if value, ok := int64Value(raw); ok && value > 0 {
			v.PostRateLimit = int(value)
		}
	case KeyPostRateWindowSeconds:
		if value, ok := int64Value(raw); ok && value > 0 {
			v.PostRateWindow = time.Duration(value) * time.Second
		}
	case KeyUploadRateLimit:
		if value, ok := int64Value(raw); ok && value > 0 {
			v.UploadRateLimit = int(value)
		}
	case KeyUploadRateWindowSeconds:
		if value, ok := int64Value(raw); ok && value > 0 {
			v.UploadRateWindow = time.Duration(value) * time.Second
		}
	case KeyTempAttachmentRetentionHour:
		if value, ok := int64Value(raw); ok && value > 0 {
			v.TempAttachmentRetention = time.Duration(value) * time.Hour
		}
	case KeyCallLogMaxBytes:
		if value, ok := int64Value(raw); ok && value > 0 {
			v.CallLogMaxBytes = int(value)
		}
	case KeyCallLogRetentionDays:
		if value, ok := int64Value(raw); ok && value >= 0 {
			v.CallLogRetentionDays = int(value)
		}
	}
	// 请求体上限必须至少容纳一个最大文件加 multipart 余量。
	if v.MaxRequestBytes < v.MaxUploadBytes+DefaultUploadOverhead {
		v.MaxRequestBytes = v.MaxUploadBytes + DefaultUploadOverhead
	}
}

func boolValue(raw []byte) (bool, bool) {
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, false
	}
	return value, true
}

func int64Value(raw []byte) (int64, bool) {
	var value int64
	if err := json.Unmarshal(raw, &value); err == nil {
		return value, true
	}
	// 兼容早期手工写入的字符串数字。
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, false
	}
	var parsed int64
	if _, err := fmt.Sscan(text, &parsed); err != nil {
		return 0, false
	}
	return parsed, true
}

// Validate 校验管理后台提交的设置；返回字段名到错误文案的映射。
func (v Values) Validate() map[string]string {
	details := map[string]string{}
	if v.MaxUploadBytes <= 0 {
		details[KeyUploadMaxFileBytes] = "单文件上限必须大于 0"
	}
	if v.MaxRequestBytes < v.MaxUploadBytes+DefaultUploadOverhead {
		details[KeyUploadMaxRequestBytes] = "单请求上限必须至少比单文件上限多 1 MiB"
	}
	if v.StorageQuotaBytes <= 0 {
		details[KeyStorageQuotaBytes] = "存储配额必须大于 0"
	}
	if v.PostRateLimit <= 0 {
		details[KeyPostRateLimit] = "发帖限频次数必须大于 0"
	}
	if v.PostRateWindow < time.Second {
		details[KeyPostRateWindowSeconds] = "发帖限频窗口至少为 1 秒"
	}
	if v.UploadRateLimit <= 0 {
		details[KeyUploadRateLimit] = "上传限频次数必须大于 0"
	}
	if v.UploadRateWindow < time.Second {
		details[KeyUploadRateWindowSeconds] = "上传限频窗口至少为 1 秒"
	}
	if v.TempAttachmentRetention < time.Hour {
		details[KeyTempAttachmentRetentionHour] = "临时附件保留期至少为 1 小时"
	}
	if v.CallLogMaxBytes <= 0 {
		details[KeyCallLogMaxBytes] = "调用记录截断上限必须大于 0"
	}
	if v.CallLogRetentionDays < 0 {
		details[KeyCallLogRetentionDays] = "调用记录保留期不能为负数"
	}
	return details
}

// ToMap 把设置快照转为管理接口响应字段。
func (v Values) ToMap(registrationEnabled, aiGlobalEnabled bool) map[string]any {
	return map[string]any{
		"registration_enabled":            registrationEnabled,
		"ai_global_enabled":               aiGlobalEnabled,
		"max_upload_bytes":                v.MaxUploadBytes,
		"max_request_bytes":               v.MaxRequestBytes,
		"storage_quota_bytes":             v.StorageQuotaBytes,
		"post_rate_limit":                 v.PostRateLimit,
		"post_rate_window_seconds":        int(v.PostRateWindow.Seconds()),
		"upload_rate_limit":               v.UploadRateLimit,
		"upload_rate_window_seconds":      int(v.UploadRateWindow.Seconds()),
		"temp_attachment_retention_hours": int(v.TempAttachmentRetention.Hours()),
		"call_log_max_bytes":              v.CallLogMaxBytes,
		"call_log_retention_days":         v.CallLogRetentionDays,
	}
}
