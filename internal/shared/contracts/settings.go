package contracts

import (
	"context"
	"time"
)

// 系统设置键名（与 platform/settings/keys.go 一致）。
// 消费者：admin（设置读写）、agent（全局 AI 开关/调用记录上限）。
const (
	SettingKeyRegistrationEnabled    = "registration.enabled"
	SettingKeyAIGlobalEnabled        = "ai.global_enabled"
	SettingKeyUploadMaxFileBytes     = "upload.max_file_bytes"
	SettingKeyUploadMaxRequestBytes  = "upload.max_request_bytes"
	SettingKeyStorageQuotaBytes      = "storage.quota_bytes"
	SettingKeyPostRateLimit          = "post.rate_limit"
	SettingKeyPostRateWindowSeconds  = "post.rate_window_seconds"
	SettingKeyUploadRateLimit        = "upload.rate_limit"
	SettingKeyUploadRateWindowSecs   = "upload.rate_window_seconds"
	SettingKeyTempAttachmentRetainHr = "media.temp_attachment_retention_hours"
	SettingKeyCallLogMaxBytes        = "ai.call_log_max_bytes"
	SettingKeyCallLogRetentionDays   = "ai.call_log_retention_days"
	SettingKeyWebSearchBaseURL       = "web_search.base_url"
	SettingKeyWebSearchAPIKeyCipher  = "web_search.api_key_cipher"
)

// SystemSettings 是一次加载得到的系统设置快照（与 platform/settings.Values 对齐）。
// 消费者：auth（注册开关）、content（发帖限频）、media（上传限额/保留期）、
// ai（全局搜索配置已单独）、agent（全局 AI 开关/调用记录上限/保留期）、admin（设置页）。
type SystemSettings struct {
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

// WebSearchSettings 是全局网页搜索配置的读取快照。
// APIKeyCipher 只允许在服务端内部解密，不得进入管理接口响应或日志。
// 消费者：ai（解密为 WebSearchConfig）、admin（设置页展示/写入）。
type WebSearchSettings struct {
	BaseURL      string
	APIKeyCipher string
}

// Configured 表示是否已保存搜索密钥。
func (v WebSearchSettings) Configured() bool { return v.APIKeyCipher != "" }

// Ready 表示密钥与 Base URL 是否同时可用。
func (v WebSearchSettings) Ready() bool { return v.Configured() && v.BaseURL != "" }

// SettingsWrite 描述一次设置写入。消费者：admin（设置页保存）。
type SettingsWrite struct {
	Key     string
	Value   any
	AdminID int64
	At      time.Time
}

// SettingsReader 是系统设置的跨模块读取端口（每次请求即时读取，修改后立即生效）。
//
// 消费者：auth、content、media、ai、agent、admin。
// 实现方：platform/settings 的 Store（组合根注入轻量适配器）。
type SettingsReader interface {
	// Settings 读取全部已知设置；缺失/非法键回退默认值。
	Settings(ctx context.Context) (SystemSettings, error)
	// WebSearch 读取全局网页搜索设置快照（不解密密钥）。
	WebSearch(ctx context.Context) (WebSearchSettings, error)
}

// SettingsWriter 是系统设置的跨模块写入端口。消费者：admin。
type SettingsWriter interface {
	// Set 写入单个设置键；实现方显式使用 At 作为 updated_at。
	Set(ctx context.Context, in SettingsWrite) error
}

// SettingsService 是 settings 平台的完整发布接口（读 + 写）。消费者：admin。
type SettingsService interface {
	SettingsReader
	SettingsWriter
}
