package settings

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDefaultsAndApply(t *testing.T) {
	v := Defaults()
	if v.MaxUploadBytes != DefaultMaxUploadBytes || v.PostRateLimit != DefaultPostRateLimit {
		t.Fatalf("默认值不正确: %+v", v)
	}

	apply(&v, KeyUploadMaxFileBytes, json.RawMessage(`1048576`))
	apply(&v, KeyStorageQuotaBytes, json.RawMessage(`"2097152"`))
	apply(&v, KeyPostRateLimit, json.RawMessage(`12`))
	apply(&v, KeyPostRateWindowSeconds, json.RawMessage(`60`))
	apply(&v, KeyTempAttachmentRetentionHour, json.RawMessage(`48`))
	apply(&v, KeyCallLogMaxBytes, json.RawMessage(`1024`))
	apply(&v, KeyCallLogRetentionDays, json.RawMessage(`0`))
	apply(&v, KeyRegistrationEnabled, json.RawMessage(`false`))

	if v.MaxUploadBytes != 1048576 {
		t.Fatalf("单文件上限未生效: %d", v.MaxUploadBytes)
	}
	if v.MaxRequestBytes < v.MaxUploadBytes+DefaultUploadOverhead {
		t.Fatalf("请求上限未按文件上限补齐: %d", v.MaxRequestBytes)
	}
	if v.StorageQuotaBytes != 2097152 {
		t.Fatalf("字符串数字未解析: %d", v.StorageQuotaBytes)
	}
	if v.PostRateLimit != 12 || v.PostRateWindow != time.Minute {
		t.Fatalf("发帖限频未生效: %+v", v)
	}
	if v.TempAttachmentRetention != 48*time.Hour {
		t.Fatalf("临时附件保留期未生效: %s", v.TempAttachmentRetention)
	}
	if v.CallLogRetentionDays != 0 {
		t.Fatalf("保留期 0 应表示关闭自动清理: %d", v.CallLogRetentionDays)
	}
	if v.RegistrationEnabled {
		t.Fatal("注册开关未生效")
	}
}

func TestApplyIgnoresInvalidValues(t *testing.T) {
	v := Defaults()
	apply(&v, KeyUploadMaxFileBytes, json.RawMessage(`-1`))
	apply(&v, KeyPostRateLimit, json.RawMessage(`"abc"`))
	apply(&v, KeyCallLogMaxBytes, json.RawMessage(`0`))
	if v.MaxUploadBytes != DefaultMaxUploadBytes || v.PostRateLimit != DefaultPostRateLimit || v.CallLogMaxBytes != DefaultCallLogMaxBytes {
		t.Fatalf("非法值不应覆盖默认值: %+v", v)
	}
}

func TestValidate(t *testing.T) {
	if details := Defaults().Validate(); len(details) != 0 {
		t.Fatalf("默认设置应合法: %+v", details)
	}
	v := Defaults()
	v.MaxRequestBytes = 1
	v.PostRateLimit = 0
	details := v.Validate()
	if details[KeyUploadMaxRequestBytes] == "" || details[KeyPostRateLimit] == "" {
		t.Fatalf("应返回字段错误: %+v", details)
	}
}
