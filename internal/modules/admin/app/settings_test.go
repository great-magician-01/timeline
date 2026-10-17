package app

import (
	"testing"
	"time"

	platformsettings "github.com/great-magician-01/timeline/internal/platform/settings"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
)

// baseSettings 返回一份通过校验的基线设置快照。
func baseSettings() SettingsSnapshot {
	return SettingsSnapshot{
		RegistrationEnabled:     true,
		AIGlobalEnabled:         false,
		MaxUploadBytes:          100 << 20,
		MaxRequestBytes:         100<<20 + platformsettings.DefaultUploadOverhead,
		StorageQuotaBytes:       5 << 30,
		PostRateLimit:           30,
		PostRateWindow:          10 * time.Minute,
		UploadRateLimit:         60,
		UploadRateWindow:        10 * time.Minute,
		TempAttachmentRetention: 7 * 24 * time.Hour,
		CallLogMaxBytes:         64 << 10,
		CallLogRetentionDays:    90,
	}
}

func TestApplySettingsPatchKeepsUnspecifiedFields(t *testing.T) {
	current := baseSettings()
	limit := 200
	merged := applySettingsPatch(current, SettingsPatch{PostRateLimit: &limit})
	if merged.PostRateLimit != 200 {
		t.Fatalf("PostRateLimit = %d, want 200", merged.PostRateLimit)
	}
	if merged.UploadRateLimit != current.UploadRateLimit || merged.StorageQuotaBytes != current.StorageQuotaBytes {
		t.Fatalf("未提交字段不应被修改: %+v", merged)
	}
}

func TestApplySettingsPatchBumpsRequestLimit(t *testing.T) {
	current := baseSettings()
	upload := int64(200 << 20)
	merged := applySettingsPatch(current, SettingsPatch{MaxUploadBytes: &upload})
	want := upload + platformsettings.DefaultUploadOverhead
	if merged.MaxRequestBytes != want {
		t.Fatalf("MaxRequestBytes = %d, want %d", merged.MaxRequestBytes, want)
	}
}

func TestApplySettingsPatchRespectsExplicitRequestLimit(t *testing.T) {
	current := baseSettings()
	upload := int64(200 << 20)
	request := upload + 8<<20
	merged := applySettingsPatch(current, SettingsPatch{
		MaxUploadBytes:  &upload,
		MaxRequestBytes: &request,
	})
	if merged.MaxRequestBytes != request {
		t.Fatalf("显式请求上限不应被覆盖: %d", merged.MaxRequestBytes)
	}
}

func TestApplySettingsPatchConvertsDurations(t *testing.T) {
	current := baseSettings()
	window := 25
	retention := 48
	merged := applySettingsPatch(current, SettingsPatch{
		PostRateWindowSeconds:        &window,
		TempAttachmentRetentionHours: &retention,
	})
	if merged.PostRateWindow != 25*time.Second {
		t.Fatalf("PostRateWindow = %s", merged.PostRateWindow)
	}
	if merged.TempAttachmentRetention != 48*time.Hour {
		t.Fatalf("TempAttachmentRetention = %s", merged.TempAttachmentRetention)
	}
}

func TestValidateSettings(t *testing.T) {
	if details := validateSettings(baseSettings()); len(details) != 0 {
		t.Fatalf("基线设置应通过校验: %v", details)
	}
	invalid := SettingsSnapshot{
		MaxUploadBytes:          0,
		MaxRequestBytes:         0,
		StorageQuotaBytes:       0,
		PostRateLimit:           0,
		PostRateWindow:          0,
		UploadRateLimit:         0,
		UploadRateWindow:        0,
		TempAttachmentRetention: 0,
		CallLogMaxBytes:         0,
		CallLogRetentionDays:    -1,
	}
	details := validateSettings(invalid)
	if len(details) != 10 {
		t.Fatalf("应报告 10 项校验错误，实际 %d: %v", len(details), details)
	}
	if details[contracts.SettingKeyUploadMaxFileBytes] == "" {
		t.Fatal("缺少单文件上限错误")
	}
	if details[contracts.SettingKeyCallLogRetentionDays] == "" {
		t.Fatal("缺少调用记录保留期错误")
	}
}

func TestMapSettingDetails(t *testing.T) {
	mapped := mapSettingDetails(map[string]string{
		contracts.SettingKeyUploadMaxFileBytes: "x",
	})
	if mapped["max_upload_bytes"] != "x" {
		t.Fatalf("设置键应映射为请求字段名: %v", mapped)
	}
}

func TestSettingsPatchEmpty(t *testing.T) {
	if !(SettingsPatch{}).Empty() {
		t.Fatal("空 patch 应判定为空")
	}
	enabled := true
	if (SettingsPatch{RegistrationEnabled: &enabled}).Empty() {
		t.Fatal("携带字段的 patch 不应判定为空")
	}
}
