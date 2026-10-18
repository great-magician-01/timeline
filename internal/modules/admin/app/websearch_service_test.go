package app

import (
	"context"
	"errors"
	"testing"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/websearch"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
)

// ===== 读取全局网页搜索设置 =====

func TestGetWebSearchSettingsUnconfigured(t *testing.T) {
	env := newAdminTestEnv(t)
	snapshot, err := env.service.GetWebSearchSettings(context.Background())
	if err != nil {
		t.Fatalf("读取网页搜索设置失败: %v", err)
	}
	if snapshot.Configured || snapshot.Usable || snapshot.APIKeyMasked != "" || snapshot.BaseURL != "" {
		t.Fatalf("未配置时快照不符: %+v", snapshot)
	}
}

func TestGetWebSearchSettingsConfiguredUsesCipher(t *testing.T) {
	env := newAdminTestEnv(t)
	env.settings.webSearch = configuredWebSearch()
	env.service.cipher = fakeCipher{canDecrypt: true}

	snapshot, err := env.service.GetWebSearchSettings(context.Background())
	if err != nil {
		t.Fatalf("读取网页搜索设置失败: %v", err)
	}
	if !snapshot.Configured || !snapshot.Usable || snapshot.APIKeyMasked == "" {
		t.Fatalf("已配置且可解密时应标记可用: %+v", snapshot)
	}
	if snapshot.BaseURL != "https://api.example.com" {
		t.Fatalf("BaseURL = %q", snapshot.BaseURL)
	}
}

func TestGetWebSearchSettingsReadErrorIsWrappedAsInternal(t *testing.T) {
	env := newAdminTestEnv(t)
	sentinel := errors.New("搜索设置读取失败")
	env.service.settings = &scriptedSettings{webSearchErrs: []error{sentinel}}

	_, err := env.service.GetWebSearchSettings(context.Background())
	if !apperr.IsKind(err, apperr.KindInternal) || !errors.Is(err, sentinel) {
		t.Fatalf("期望 internal 且保留底层原因，实际: %v", err)
	}
}

// ===== 更新全局网页搜索设置 =====

func TestUpdateWebSearchSettingsRejectsEmptyPatch(t *testing.T) {
	env := newAdminTestEnv(t)
	_, err := env.service.UpdateWebSearchSettings(context.Background(), adminActor(), WebSearchPatch{})
	appErr, ok := apperr.AsKind(err)
	if !ok || appErr.Kind != apperr.KindValidation {
		t.Fatalf("期望 validation，实际: %v", err)
	}
	details, ok := appErr.Details.(map[string]string)
	if !ok || details["settings"] == "" {
		t.Fatalf("空 patch 应带 settings 详情: %#v", appErr.Details)
	}
}

func TestUpdateWebSearchSettingsReadErrorIsWrappedAsInternal(t *testing.T) {
	env := newAdminTestEnv(t)
	sentinel := errors.New("搜索设置读取失败")
	env.service.settings = &scriptedSettings{webSearchErrs: []error{sentinel}}

	_, err := env.service.UpdateWebSearchSettings(context.Background(), adminActor(),
		WebSearchPatch{BaseURL: ptr("https://search.example.com")})
	if !apperr.IsKind(err, apperr.KindInternal) || !errors.Is(err, sentinel) {
		t.Fatalf("期望 internal 且保留底层原因，实际: %v", err)
	}
}

func TestUpdateWebSearchSettingsRejectsInvalidBaseURL(t *testing.T) {
	env := newAdminTestEnv(t)
	_, err := env.service.UpdateWebSearchSettings(context.Background(), adminActor(),
		WebSearchPatch{BaseURL: ptr("ftp://search.example.com")})
	appErr, ok := apperr.AsKind(err)
	if !ok || appErr.Kind != apperr.KindValidation {
		t.Fatalf("期望 validation，实际: %v", err)
	}
	details, ok := appErr.Details.(map[string]string)
	if !ok || details["base_url"] == "" {
		t.Fatalf("应带 base_url 校验详情: %#v", appErr.Details)
	}
	if len(env.settings.sets) != 0 || len(env.audit.entries) != 0 {
		t.Fatalf("校验失败时不应写库或写审计")
	}
}

func TestUpdateWebSearchSettingsSavesBaseURLOnly(t *testing.T) {
	env := newAdminTestEnv(t)

	snapshot, err := env.service.UpdateWebSearchSettings(context.Background(), adminActor(),
		WebSearchPatch{BaseURL: ptr(" https://search.example.com/ ")})
	if err != nil {
		t.Fatalf("保存搜索 Base URL 失败: %v", err)
	}
	if got := env.settings.setKeys(); len(got) != 1 || got[0] != contracts.SettingKeyWebSearchBaseURL {
		t.Fatalf("应只写 Base URL，实际: %v", got)
	}
	if env.settings.sets[0].Value != "https://search.example.com" {
		t.Fatalf("Base URL 应归一化后写入: %v", env.settings.sets[0].Value)
	}
	entries := env.audit.entries
	if len(entries) != 1 || entries[0].Action != contracts.AuditActionSettingsUpdate ||
		entries[0].TargetType != "web_search_settings" {
		t.Fatalf("应写一条 settings_update/web_search_settings 审计，实际: %v", env.audit.actions())
	}
	detail := detailOf(t, entries[0].Detail)
	if detail["base_url"] != "https://search.example.com" || detail["configured"] != false {
		t.Fatalf("审计详情不符: %+v", detail)
	}
	if snapshot.BaseURL != "https://search.example.com" || snapshot.Configured {
		t.Fatalf("返回快照不符: %+v", snapshot)
	}
}

func TestUpdateWebSearchSettingsSavesKeyWithDefaultBaseURL(t *testing.T) {
	env := newAdminTestEnv(t)
	env.service.cipher = fakeCipher{canDecrypt: true}

	snapshot, err := env.service.UpdateWebSearchSettings(context.Background(), adminActor(),
		WebSearchPatch{APIKey: ptr("  new-key  ")})
	if err != nil {
		t.Fatalf("保存搜索密钥失败: %v", err)
	}
	keys := env.settings.setKeys()
	if len(keys) != 2 || keys[0] != contracts.SettingKeyWebSearchBaseURL ||
		keys[1] != contracts.SettingKeyWebSearchAPIKeyCipher {
		t.Fatalf("未配置 Base URL 时应先补默认地址再写密钥，实际: %v", keys)
	}
	if env.settings.sets[1].Value != "v1:new-key" {
		t.Fatalf("密钥应加密后写入: %v", env.settings.sets[1].Value)
	}
	detail := detailOf(t, env.audit.entries[0].Detail)
	if detail["configured"] != true {
		t.Fatalf("审计 detail 应标记已配置: %+v", detail)
	}
	if !snapshot.Configured || !snapshot.Usable || snapshot.APIKeyMasked == "" {
		t.Fatalf("返回快照不符: %+v", snapshot)
	}
	if snapshot.BaseURL != websearch.DefaultBaseURL {
		t.Fatalf("未配置时应回退默认 Base URL，实际: %q", snapshot.BaseURL)
	}
}

func TestUpdateWebSearchSettingsClearsKey(t *testing.T) {
	env := newAdminTestEnv(t)
	env.settings.webSearch = configuredWebSearch()
	env.service.cipher = fakeCipher{canDecrypt: true}

	snapshot, err := env.service.UpdateWebSearchSettings(context.Background(), adminActor(),
		WebSearchPatch{ClearAPIKey: ptrBool(true)})
	if err != nil {
		t.Fatalf("清除搜索密钥失败: %v", err)
	}
	keys := env.settings.setKeys()
	want := []string{contracts.SettingKeyWebSearchBaseURL, contracts.SettingKeyWebSearchAPIKeyCipher}
	if len(keys) != 2 || keys[0] != want[0] || keys[1] != want[1] {
		t.Fatalf("清除密钥的写入键不符: %v", keys)
	}
	if env.settings.sets[1].Value != "" {
		t.Fatalf("clear_api_key 应把密文清空: %v", env.settings.sets[1].Value)
	}
	if snapshot.Configured || snapshot.APIKeyMasked != "" {
		t.Fatalf("清除后不应再标记已配置: %+v", snapshot)
	}
}

func TestUpdateWebSearchSettingsCipherUnavailable(t *testing.T) {
	env := newAdminTestEnv(t)
	env.service.cipher = nil

	_, err := env.service.UpdateWebSearchSettings(context.Background(), adminActor(),
		WebSearchPatch{APIKey: ptr("new-key")})
	if !apperr.IsKind(err, apperr.KindInternal) || !errors.Is(err, errSearchCipherUnavailable) {
		t.Fatalf("期望 internal 且保留加密器缺失原因，实际: %v", err)
	}
	if len(env.settings.sets) != 0 || len(env.audit.entries) != 0 {
		t.Fatalf("加密器缺失时不应写库或写审计")
	}
}

func TestUpdateWebSearchSettingsCipherError(t *testing.T) {
	env := newAdminTestEnv(t)
	sentinel := errors.New("加密失败")
	env.service.cipher = fakeCipher{err: sentinel}

	_, err := env.service.UpdateWebSearchSettings(context.Background(), adminActor(),
		WebSearchPatch{APIKey: ptr("new-key")})
	if !apperr.IsKind(err, apperr.KindInternal) || !errors.Is(err, sentinel) {
		t.Fatalf("期望 internal 且保留底层原因，实际: %v", err)
	}
}

func TestUpdateWebSearchSettingsWriteErrorsAreWrappedAsInternal(t *testing.T) {
	sentinel := errors.New("搜索设置写库失败")
	cases := []struct {
		name  string
		patch WebSearchPatch
		key   string
	}{
		{"Base URL", WebSearchPatch{BaseURL: ptr("https://search.example.com")}, contracts.SettingKeyWebSearchBaseURL},
		{"密钥密文", WebSearchPatch{APIKey: ptr("new-key")}, contracts.SettingKeyWebSearchAPIKeyCipher},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newAdminTestEnv(t)
			env.service.cipher = fakeCipher{canDecrypt: true}
			env.service.settings = &scriptedSettings{
				setKeyErrs: map[string]error{tc.key: sentinel},
			}

			_, err := env.service.UpdateWebSearchSettings(context.Background(), adminActor(), tc.patch)
			if !apperr.IsKind(err, apperr.KindInternal) || !errors.Is(err, sentinel) {
				t.Fatalf("期望 internal 且保留底层原因，实际: %v", err)
			}
			if len(env.audit.entries) != 0 {
				t.Fatalf("写库失败时不应写审计")
			}
		})
	}
}

func TestUpdateWebSearchSettingsAuditErrorIsReturned(t *testing.T) {
	env := newAdminTestEnv(t)
	sentinel := errors.New("审计写入失败")
	env.service.audit = failingAudit{err: sentinel}

	_, err := env.service.UpdateWebSearchSettings(context.Background(), adminActor(),
		WebSearchPatch{BaseURL: ptr("https://search.example.com")})
	if !errors.Is(err, sentinel) {
		t.Fatalf("审计失败应返回错误，实际: %v", err)
	}
}

func TestUpdateWebSearchSettingsFinalReadErrorIsWrappedAsInternal(t *testing.T) {
	env := newAdminTestEnv(t)
	sentinel := errors.New("提交后读取失败")
	env.service.settings = &scriptedSettings{webSearchErrs: []error{nil, sentinel}}

	_, err := env.service.UpdateWebSearchSettings(context.Background(), adminActor(),
		WebSearchPatch{BaseURL: ptr("https://search.example.com")})
	if !apperr.IsKind(err, apperr.KindInternal) || !errors.Is(err, sentinel) {
		t.Fatalf("期望 internal 且保留底层原因，实际: %v", err)
	}
}
