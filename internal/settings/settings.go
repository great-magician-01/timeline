// Package settings 读取与写入运行期系统设置（system_settings）。
//
// 设置项由管理后台维护，读取方在每次请求/任务执行时加载，保证修改后即时生效。
// 数据库缺失的键使用代码内默认值，避免升级后必须先执行数据回填。
package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

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

// Querier 是连接池与事务共用的查询接口（与 access.Querier 同形，避免包依赖倒置）。
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

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

// Load 读取全部已知设置；缺失或格式非法的键回退默认值。
// 数据库错误原样返回，调用方可选择使用 Defaults 继续运行。
func Load(ctx context.Context, q Querier) (Values, error) {
	values := Defaults()
	keys := []string{
		KeyRegistrationEnabled, KeyAIGlobalEnabled,
		KeyUploadMaxFileBytes, KeyUploadMaxRequestBytes, KeyStorageQuotaBytes,
		KeyPostRateLimit, KeyPostRateWindowSeconds,
		KeyUploadRateLimit, KeyUploadRateWindowSeconds,
		KeyTempAttachmentRetentionHour, KeyCallLogMaxBytes, KeyCallLogRetentionDays,
	}
	rows, err := q.Query(ctx, `SELECT key, value FROM system_settings WHERE key = ANY($1)`, keys)
	if err != nil {
		return values, err
	}
	defer rows.Close()

	for rows.Next() {
		var key string
		var raw []byte
		if err := rows.Scan(&key, &raw); err != nil {
			return values, err
		}
		apply(&values, key, raw)
	}
	if err := rows.Err(); err != nil {
		return values, err
	}
	return values, nil
}

// LoadOrDefaults 读取设置，数据库错误时记录由调用方处理并返回默认值。
func LoadOrDefaults(ctx context.Context, q Querier) Values {
	values, err := Load(ctx, q)
	if err != nil {
		return Defaults()
	}
	return values
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

// Set 在调用方事务内写入一个设置值，并记录更新管理员。
func Set(ctx context.Context, q Querier, key string, value any, adminID int64) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `
		INSERT INTO system_settings (key, value, updated_by, updated_at)
		VALUES ($1, $2::jsonb, $3, now())
		ON CONFLICT (key) DO UPDATE
		SET value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = now()`,
		key, string(encoded), adminID)
	return err
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

// Ensure interface compatibility with common query types at compile time.
var (
	_ Querier = (pgx.Tx)(nil)
)
