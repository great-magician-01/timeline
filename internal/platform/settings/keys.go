package settings

import "time"

// system_settings 键名。
const (
	KeyRegistrationEnabled = "registration.enabled"
	KeyAIGlobalEnabled     = "ai.global_enabled"

	KeyUploadMaxFileBytes          = "upload.max_file_bytes"
	KeyUploadMaxRequestBytes       = "upload.max_request_bytes"
	KeyStorageQuotaBytes           = "storage.quota_bytes"
	KeyPostRateLimit               = "post.rate_limit"
	KeyPostRateWindowSeconds       = "post.rate_window_seconds"
	KeyUploadRateLimit             = "upload.rate_limit"
	KeyUploadRateWindowSeconds     = "upload.rate_window_seconds"
	KeyTempAttachmentRetentionHour = "media.temp_attachment_retention_hours"
	KeyCallLogMaxBytes             = "ai.call_log_max_bytes"
	KeyCallLogRetentionDays        = "ai.call_log_retention_days"
)

// 全局网页搜索设置键。搜索能力独立于模型接口预设：所有开启联网搜索的 AI
// 共用这里的 Base URL 与加密密钥。
const (
	KeyWebSearchBaseURL      = "web_search.base_url"
	KeyWebSearchAPIKeyCipher = "web_search.api_key_cipher"
)

// 默认值。管理后台未配置或配置非法时使用这些安全保护阈值。
const (
	DefaultMaxUploadBytes       int64 = 100 << 20 // 100 MiB
	DefaultUploadOverhead       int64 = 1 << 20   // multipart 开销余量
	DefaultStorageQuotaBytes    int64 = 5 << 30   // 5 GiB / 用户
	DefaultPostRateLimit              = 30
	DefaultPostRateWindow             = 10 * time.Minute
	DefaultUploadRateLimit            = 60
	DefaultUploadRateWindow           = 10 * time.Minute
	DefaultTempRetention              = 7 * 24 * time.Hour
	DefaultCallLogMaxBytes            = 64 << 10 // 64 KiB
	DefaultCallLogRetentionDays       = 90
)
