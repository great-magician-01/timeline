package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	platformsettings "github.com/great-magician-01/timeline/internal/platform/settings"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
)

// ===== 读取系统设置 =====

func TestGetSettingsReturnsSnapshot(t *testing.T) {
	env := newAdminTestEnv(t)
	snapshot, err := env.service.GetSettings(context.Background())
	if err != nil {
		t.Fatalf("读取系统设置失败: %v", err)
	}
	if snapshot.PostRateLimit != env.settings.values.PostRateLimit ||
		snapshot.TempAttachmentRetention != env.settings.values.TempAttachmentRetention {
		t.Fatalf("读取结果与端口快照不一致: %+v", snapshot)
	}
}

func TestGetSettingsReadErrorIsWrappedAsInternal(t *testing.T) {
	env := newAdminTestEnv(t)
	sentinel := errors.New("设置读取失败")
	env.service.settings = &scriptedSettings{settingsErrs: []error{sentinel}}

	_, err := env.service.GetSettings(context.Background())
	if !apperr.IsKind(err, apperr.KindInternal) || !errors.Is(err, sentinel) {
		t.Fatalf("期望 internal 且保留底层原因，实际: %v", err)
	}
}

// ===== 更新系统设置 =====

func TestUpdateSettingsRejectsEmptyPatch(t *testing.T) {
	env := newAdminTestEnv(t)
	_, err := env.service.UpdateSettings(context.Background(), adminActor(), SettingsPatch{})
	appErr, ok := apperr.AsKind(err)
	if !ok || appErr.Kind != apperr.KindValidation {
		t.Fatalf("期望 validation，实际: %v", err)
	}
	details, ok := appErr.Details.(map[string]string)
	if !ok || details["settings"] == "" {
		t.Fatalf("空 patch 应带 settings 详情: %#v", appErr.Details)
	}
	if len(env.settings.sets) != 0 || len(env.audit.entries) != 0 {
		t.Fatalf("空 patch 不应读写任何设置")
	}
}

func TestUpdateSettingsReadErrorIsWrappedAsInternal(t *testing.T) {
	env := newAdminTestEnv(t)
	sentinel := errors.New("设置读取失败")
	env.service.settings = &scriptedSettings{settingsErrs: []error{sentinel}}
	limit := 10

	_, err := env.service.UpdateSettings(context.Background(), adminActor(), SettingsPatch{PostRateLimit: &limit})
	if !apperr.IsKind(err, apperr.KindInternal) || !errors.Is(err, sentinel) {
		t.Fatalf("期望 internal 且保留底层原因，实际: %v", err)
	}
	if len(env.audit.entries) != 0 {
		t.Fatalf("读取失败时不应写审计")
	}
}

func TestUpdateSettingsRejectsInvalidMergedValues(t *testing.T) {
	env := newAdminTestEnv(t)
	upload := int64(0)
	postLimit := 0
	retention := -1

	_, err := env.service.UpdateSettings(context.Background(), adminActor(), SettingsPatch{
		MaxUploadBytes:       &upload,
		PostRateLimit:        &postLimit,
		CallLogRetentionDays: &retention,
	})
	appErr, ok := apperr.AsKind(err)
	if !ok || appErr.Kind != apperr.KindValidation {
		t.Fatalf("期望 validation，实际: %v", err)
	}
	details, ok := appErr.Details.(map[string]string)
	if !ok {
		t.Fatalf("详情类型 = %T，期望 map[string]string", appErr.Details)
	}
	// 详情使用请求字段名（而非内部设置键名）。
	for _, field := range []string{"max_upload_bytes", "post_rate_limit", "call_log_retention_days"} {
		if details[field] == "" {
			t.Fatalf("缺少字段 %q 的校验详情: %v", field, details)
		}
	}
	if len(env.settings.sets) != 0 || len(env.audit.entries) != 0 {
		t.Fatalf("校验失败时不应写库或写审计")
	}
}

func TestUpdateSettingsWritesAllNumericFieldsAndSingleAudit(t *testing.T) {
	env := newAdminTestEnv(t)
	env.clock.Advance(2 * time.Minute)
	now := env.clock.Now()

	upload := int64(200 << 20)
	request := upload + platformsettings.DefaultUploadOverhead
	quota := int64(10 << 30)
	postLimit := 12
	postWindow := 15
	uploadLimit := 20
	uploadWindow := 30
	retention := 5
	callLogMax := 32 << 10
	callLogDays := 30
	patch := SettingsPatch{
		MaxUploadBytes:               &upload,
		MaxRequestBytes:              &request,
		StorageQuotaBytes:            &quota,
		PostRateLimit:                &postLimit,
		PostRateWindowSeconds:        &postWindow,
		UploadRateLimit:              &uploadLimit,
		UploadRateWindowSeconds:      &uploadWindow,
		TempAttachmentRetentionHours: &retention,
		CallLogMaxBytes:              &callLogMax,
		CallLogRetentionDays:         &callLogDays,
	}
	if _, err := env.service.UpdateSettings(context.Background(), adminActor(), patch); err != nil {
		t.Fatalf("更新系统设置失败: %v", err)
	}

	wantKeys := []string{
		contracts.SettingKeyUploadMaxFileBytes,
		contracts.SettingKeyUploadMaxRequestBytes,
		contracts.SettingKeyStorageQuotaBytes,
		contracts.SettingKeyPostRateLimit,
		contracts.SettingKeyPostRateWindowSeconds,
		contracts.SettingKeyUploadRateLimit,
		contracts.SettingKeyUploadRateWindowSecs,
		contracts.SettingKeyTempAttachmentRetainHr,
		contracts.SettingKeyCallLogMaxBytes,
		contracts.SettingKeyCallLogRetentionDays,
	}
	if got := strings.Join(env.settings.setKeys(), ","); got != strings.Join(wantKeys, ",") {
		t.Fatalf("设置写入键不符:\n got=%v\nwant=%v", env.settings.setKeys(), wantKeys)
	}
	wantValues := []any{upload, request, quota, postLimit, postWindow, uploadLimit, uploadWindow, retention, callLogMax, callLogDays}
	for i, want := range wantValues {
		set := env.settings.sets[i]
		if set.Value != want {
			t.Fatalf("第 %d 项设置值 = %v，期望 %v", i, set.Value, want)
		}
		if set.AdminID != adminActor().ID || !set.At.Equal(now) {
			t.Fatalf("第 %d 项设置写库时间/操作者不符: %+v", i, set)
		}
	}

	entries := env.audit.entries
	if len(entries) != 1 || entries[0].Action != contracts.AuditActionSettingsUpdate {
		t.Fatalf("数值设置应只写一条 settings_update 审计，实际: %v", env.audit.actions())
	}
	detail := detailOf(t, entries[0].Detail)
	if len(detail) != len(wantKeys) {
		t.Fatalf("审计详情字段数 = %d，期望 %d: %v", len(detail), len(wantKeys), detail)
	}
	if detail["max_upload_bytes"] != upload || detail["post_rate_window_seconds"] != postWindow {
		t.Fatalf("审计详情数值不符: %v", detail)
	}
	if entries[0].AdminID != adminActor().ID || !entries[0].At.Equal(now) {
		t.Fatalf("审计主体/时间不符: %+v", entries[0])
	}
}

func TestUpdateSettingsBumpsRequestLimitWithUploadLimit(t *testing.T) {
	env := newAdminTestEnv(t)
	upload := int64(200 << 20)

	if _, err := env.service.UpdateSettings(context.Background(), adminActor(),
		SettingsPatch{MaxUploadBytes: &upload}); err != nil {
		t.Fatalf("更新系统设置失败: %v", err)
	}
	wantRequest := upload + platformsettings.DefaultUploadOverhead
	keys := env.settings.setKeys()
	if len(keys) != 2 || keys[0] != contracts.SettingKeyUploadMaxFileBytes ||
		keys[1] != contracts.SettingKeyUploadMaxRequestBytes {
		t.Fatalf("提高单文件上限应联动写入请求上限，实际: %v", keys)
	}
	if env.settings.sets[0].Value != upload || env.settings.sets[1].Value != wantRequest {
		t.Fatalf("联动写入值不符: %+v", env.settings.sets)
	}
}

func TestUpdateSettingsWritesRegistrationAndGlobalAISeparately(t *testing.T) {
	env := newAdminTestEnv(t)
	env.settings.values.AIGlobalEnabled = true
	registration := false
	enabled := false

	snapshot, err := env.service.UpdateSettings(context.Background(), adminActor(), SettingsPatch{
		RegistrationEnabled: &registration,
		AIGlobalEnabled:     &enabled,
	})
	if err != nil {
		t.Fatalf("更新系统设置失败: %v", err)
	}
	keys := env.settings.setKeys()
	if len(keys) != 2 || keys[0] != contracts.SettingKeyRegistrationEnabled ||
		keys[1] != contracts.SettingKeyAIGlobalEnabled {
		t.Fatalf("设置写入键不符: %v", keys)
	}
	actions := env.audit.actions()
	if len(actions) != 2 || actions[0] != contracts.AuditActionRegistrationUpdate ||
		actions[1] != contracts.AuditActionGlobalAIStop {
		t.Fatalf("注册开关与全局 AI 开关应各写一条审计，实际: %v", actions)
	}
	if env.globalAI.haltCalls != 1 || env.globalAI.enableCalls != 0 {
		t.Fatalf("关闭全局 AI 应调用 agent 端口一次: halt=%d enable=%d",
			env.globalAI.haltCalls, env.globalAI.enableCalls)
	}
	if snapshot.RegistrationEnabled || snapshot.AIGlobalEnabled {
		t.Fatalf("返回快照未反映更新结果: %+v", snapshot)
	}
}

func TestUpdateSettingsGlobalAIWithoutHalterWritesOnlySetting(t *testing.T) {
	env := newAdminTestEnv(t)
	env.service.globalAI = nil
	enabled := true

	snapshot, err := env.service.UpdateSettings(context.Background(), adminActor(),
		SettingsPatch{AIGlobalEnabled: &enabled})
	if err != nil {
		t.Fatalf("更新系统设置失败: %v", err)
	}
	keys := env.settings.setKeys()
	if len(keys) != 1 || keys[0] != contracts.SettingKeyAIGlobalEnabled {
		t.Fatalf("未注入编排端口时应只写开关，实际: %v", keys)
	}
	if len(env.audit.entries) != 0 {
		t.Fatalf("未注入编排端口时不应写审计（由端口负责），实际: %v", env.audit.actions())
	}
	if !snapshot.AIGlobalEnabled {
		t.Fatalf("返回快照未反映开启状态: %+v", snapshot)
	}
}

func TestUpdateSettingsWriteErrorsAreWrappedAsInternal(t *testing.T) {
	sentinel := errors.New("设置写库失败")
	cases := []struct {
		name  string
		patch func() SettingsPatch
	}{
		{"数值设置", func() SettingsPatch {
			limit := 10
			return SettingsPatch{PostRateLimit: &limit}
		}},
		{"注册开关", func() SettingsPatch {
			registration := false
			return SettingsPatch{RegistrationEnabled: &registration}
		}},
		{"全局 AI 开关", func() SettingsPatch {
			enabled := true
			return SettingsPatch{AIGlobalEnabled: &enabled}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newAdminTestEnv(t)
			env.service.settings = &scriptedSettings{values: baseSettings(), setErr: sentinel}

			_, err := env.service.UpdateSettings(context.Background(), adminActor(), tc.patch())
			if !apperr.IsKind(err, apperr.KindInternal) || !errors.Is(err, sentinel) {
				t.Fatalf("期望 internal 且保留底层原因，实际: %v", err)
			}
			if len(env.audit.entries) != 0 {
				t.Fatalf("写库失败时不应写审计")
			}
		})
	}
}

func TestUpdateSettingsFinalReadErrorIsWrappedAsInternal(t *testing.T) {
	env := newAdminTestEnv(t)
	sentinel := errors.New("提交后读取失败")
	env.service.settings = &scriptedSettings{
		values:       baseSettings(),
		settingsErrs: []error{nil, sentinel},
	}
	limit := 10

	_, err := env.service.UpdateSettings(context.Background(), adminActor(), SettingsPatch{PostRateLimit: &limit})
	if !apperr.IsKind(err, apperr.KindInternal) || !errors.Is(err, sentinel) {
		t.Fatalf("期望 internal 且保留底层原因，实际: %v", err)
	}
}

func TestUpdateSettingsAuditErrorIsReturned(t *testing.T) {
	cases := []struct {
		name  string
		patch SettingsPatch
	}{
		{"数值设置审计", SettingsPatch{PostRateLimit: intPtr(10)}},
		{"注册开关审计", SettingsPatch{RegistrationEnabled: boolPtr(false)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newAdminTestEnv(t)
			sentinel := errors.New("审计写入失败")
			env.service.audit = failingAudit{err: sentinel}

			if _, err := env.service.UpdateSettings(context.Background(), adminActor(), tc.patch); !errors.Is(err, sentinel) {
				t.Fatalf("审计失败应返回错误，实际: %v", err)
			}
		})
	}
}

func TestUpdateSettingsHalterErrorIsReturned(t *testing.T) {
	env := newAdminTestEnv(t)
	sentinel := errors.New("暂停 AI 编排失败")
	env.service.globalAI = errGlobalAI{err: sentinel}
	enabled := false

	if _, err := env.service.UpdateSettings(context.Background(), adminActor(),
		SettingsPatch{AIGlobalEnabled: &enabled}); !errors.Is(err, sentinel) {
		t.Fatalf("期望透传编排错误，实际: %v", err)
	}
}

func TestUpdateSettingsHalterEnableErrorIsReturned(t *testing.T) {
	env := newAdminTestEnv(t)
	sentinel := errors.New("开启 AI 编排失败")
	env.service.globalAI = errGlobalAI{err: sentinel}
	enabled := true

	if _, err := env.service.UpdateSettings(context.Background(), adminActor(),
		SettingsPatch{AIGlobalEnabled: &enabled}); !errors.Is(err, sentinel) {
		t.Fatalf("期望透传编排错误，实际: %v", err)
	}
}

func TestApplySettingsPatchRemainingFields(t *testing.T) {
	current := baseSettings()
	quota := int64(9 << 30)
	uploadLimit := 40
	uploadWindow := 90
	callLogMax := 128 << 10
	callLogDays := 7
	request := current.MaxUploadBytes + 8<<20
	merged := applySettingsPatch(current, SettingsPatch{
		StorageQuotaBytes:       &quota,
		UploadRateLimit:         &uploadLimit,
		UploadRateWindowSeconds: &uploadWindow,
		CallLogMaxBytes:         &callLogMax,
		CallLogRetentionDays:    &callLogDays,
		MaxRequestBytes:         &request,
	})
	if merged.StorageQuotaBytes != quota || merged.UploadRateLimit != uploadLimit ||
		merged.UploadRateWindow != time.Duration(uploadWindow)*time.Second ||
		merged.CallLogMaxBytes != callLogMax || merged.CallLogRetentionDays != callLogDays ||
		merged.MaxRequestBytes != request {
		t.Fatalf("合并结果不符: %+v", merged)
	}
}

func intPtr(value int) *int { return &value }

func boolPtr(value bool) *bool { return &value }
