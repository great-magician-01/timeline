package admin

import (
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/settings"
)

func boolPtr(v bool) *bool    { return &v }
func intPtr(v int) *int       { return &v }
func int64Ptr(v int64) *int64 { return &v }

func TestSettingsPatchEmpty(t *testing.T) {
	if !(settingsPatch{}).empty() {
		t.Fatal("空请求应判定为空")
	}
	if (settingsPatch{RegistrationEnabled: boolPtr(true)}).empty() {
		t.Fatal("带字段的请求不应判定为空")
	}
}

func TestApplySettingsPatchKeepsUnspecifiedFields(t *testing.T) {
	current := settings.Defaults()
	patch := settingsPatch{
		PostRateLimit:         intPtr(5),
		PostRateWindowSeconds: intPtr(120),
	}
	merged := applySettingsPatch(current, patch)
	if merged.PostRateLimit != 5 || merged.PostRateWindow != 2*time.Minute {
		t.Fatalf("应应用发帖限频: %+v", merged)
	}
	if merged.UploadRateLimit != current.UploadRateLimit {
		t.Fatalf("未提交字段应保留: %+v", merged)
	}
}

func TestApplySettingsPatchBumpsRequestLimit(t *testing.T) {
	current := settings.Defaults()
	patch := settingsPatch{MaxUploadBytes: int64Ptr(current.MaxRequestBytes + settings.DefaultUploadOverhead)}
	merged := applySettingsPatch(current, patch)
	want := merged.MaxUploadBytes + settings.DefaultUploadOverhead
	if merged.MaxRequestBytes != want {
		t.Fatalf("提高文件上限时应自动提高请求上限到 %d，实际 %d", want, merged.MaxRequestBytes)
	}
	if details := merged.Validate(); len(details) != 0 {
		t.Fatalf("自动调整后应通过校验: %v", details)
	}
}

func TestApplySettingsPatchRespectsExplicitRequestLimit(t *testing.T) {
	current := settings.Defaults()
	patch := settingsPatch{
		MaxUploadBytes:  int64Ptr(10 << 20),
		MaxRequestBytes: int64Ptr(1 << 20),
	}
	merged := applySettingsPatch(current, patch)
	if merged.MaxRequestBytes != 1<<20 {
		t.Fatalf("显式提交的请求上限不应被覆盖: %d", merged.MaxRequestBytes)
	}
	if details := merged.Validate(); len(details) == 0 {
		t.Fatal("请求上限小于文件上限时应校验失败")
	}
}

func TestMapSettingDetails(t *testing.T) {
	details := map[string]string{
		settings.KeyUploadMaxFileBytes:   "单文件上限必须大于 0",
		settings.KeyCallLogRetentionDays: "调用记录保留期不能为负数",
	}
	mapped := mapSettingDetails(details)
	if mapped["max_upload_bytes"] != "单文件上限必须大于 0" {
		t.Fatalf("应映射为接口字段名: %v", mapped)
	}
	if mapped["call_log_retention_days"] != "调用记录保留期不能为负数" {
		t.Fatalf("应映射为接口字段名: %v", mapped)
	}
	if _, ok := mapped[settings.KeyUploadMaxFileBytes]; ok {
		t.Fatalf("不应保留内部键名: %v", mapped)
	}
}

func TestApplySettingsPatchValidatesAllFields(t *testing.T) {
	current := settings.Defaults()
	merged := applySettingsPatch(current, settingsPatch{
		MaxUploadBytes:               int64Ptr(0),
		StorageQuotaBytes:            int64Ptr(-1),
		PostRateLimit:                intPtr(0),
		PostRateWindowSeconds:        intPtr(0),
		UploadRateLimit:              intPtr(0),
		UploadRateWindowSeconds:      intPtr(0),
		TempAttachmentRetentionHours: intPtr(0),
		CallLogMaxBytes:              intPtr(0),
		CallLogRetentionDays:         intPtr(-2),
	})
	details := merged.Validate()
	for _, key := range []string{
		settings.KeyUploadMaxFileBytes,
		settings.KeyStorageQuotaBytes,
		settings.KeyPostRateLimit,
		settings.KeyPostRateWindowSeconds,
		settings.KeyUploadRateLimit,
		settings.KeyUploadRateWindowSeconds,
		settings.KeyTempAttachmentRetentionHour,
		settings.KeyCallLogMaxBytes,
		settings.KeyCallLogRetentionDays,
	} {
		if _, ok := details[key]; !ok {
			t.Errorf("应报告字段 %s 的校验错误: %v", key, details)
		}
	}
}

func TestApplySettingsPatchDurationHelpers(t *testing.T) {
	if secondsDuration(90) != 90*time.Second {
		t.Fatal("秒换算错误")
	}
	if hoursDuration(7) != 7*time.Hour {
		t.Fatal("小时换算错误")
	}
}
