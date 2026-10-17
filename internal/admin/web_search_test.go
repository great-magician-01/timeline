package admin

import (
	"errors"
	"strings"
	"testing"

	"github.com/great-magician-01/timeline/internal/settings"
)

func webSearchStringPtr(value string) *string { return &value }

type fakeSecretCipher struct {
	plain      string
	err        error
	canDecrypt bool
}

func (f *fakeSecretCipher) EncryptSecret(plain string) (string, error) {
	f.plain = plain
	if f.err != nil {
		return "", f.err
	}
	return "v1:encrypted", nil
}

func (f *fakeSecretCipher) CanDecryptSecret(string) bool { return f.canDecrypt }

func TestApplyWebSearchPatch(t *testing.T) {
	current := settings.WebSearchValues{
		BaseURL:      "https://api.deepseek.com",
		APIKeyCipher: "v1:old",
	}
	cipher := &fakeSecretCipher{}
	next, details, err := applyWebSearchPatch(current, webSearchSettingsPatch{
		BaseURL: webSearchStringPtr(" https://gateway.example.com/deepseek/ "),
		APIKey:  webSearchStringPtr("  sk-new  "),
	}, cipher)
	if err != nil || len(details) > 0 {
		t.Fatalf("合法补丁不应失败: details=%v err=%v", details, err)
	}
	if next.BaseURL != "https://gateway.example.com/deepseek" {
		t.Fatalf("Base URL 未规范化: %q", next.BaseURL)
	}
	if next.APIKeyCipher != "v1:encrypted" || strings.Contains(next.APIKeyCipher, "sk-new") {
		t.Fatalf("密钥未加密或泄漏: %q", next.APIKeyCipher)
	}
	if cipher.plain != "sk-new" {
		t.Fatalf("交给加密器的明文不符: %q", cipher.plain)
	}
	if !next.Configured() {
		t.Fatal("保存密钥后应标记为已配置")
	}
}

func TestApplyWebSearchPatchBlankKeyKeepsCurrent(t *testing.T) {
	current := settings.WebSearchValues{
		BaseURL:      "https://api.deepseek.com",
		APIKeyCipher: "v1:old",
	}
	next, details, err := applyWebSearchPatch(current, webSearchSettingsPatch{
		BaseURL: webSearchStringPtr("https://api.deepseek.com/v1"),
		APIKey:  webSearchStringPtr("   "),
	}, &fakeSecretCipher{})
	if err != nil || len(details) > 0 {
		t.Fatalf("空密钥应保持原值: details=%v err=%v", details, err)
	}
	if next.APIKeyCipher != current.APIKeyCipher || next.BaseURL != "https://api.deepseek.com/v1" {
		t.Fatalf("补丁结果不符: %+v", next)
	}
}

func TestApplyWebSearchPatchClear(t *testing.T) {
	current := settings.WebSearchValues{
		BaseURL:      "https://api.deepseek.com",
		APIKeyCipher: "v1:old",
	}
	next, details, err := applyWebSearchPatch(current, webSearchSettingsPatch{
		ClearAPIKey: boolPtr(true),
	}, nil)
	if err != nil || len(details) > 0 {
		t.Fatalf("清除密钥不应失败: details=%v err=%v", details, err)
	}
	if next.Configured() || next.APIKeyCipher != "" {
		t.Fatalf("密钥应被逻辑清除: %+v", next)
	}
}

func TestApplyWebSearchPatchRejectsConflictingOrUnsafeInput(t *testing.T) {
	current := settings.DefaultWebSearch()
	cases := []struct {
		name  string
		patch webSearchSettingsPatch
		field string
	}{
		{"同时保存与清除", webSearchSettingsPatch{
			APIKey: webSearchStringPtr("sk-new"), ClearAPIKey: boolPtr(true),
		}, "api_key"},
		{"非法 Base URL", webSearchSettingsPatch{
			BaseURL: webSearchStringPtr("ftp://api.deepseek.com"),
		}, "base_url"},
		{"空补丁", webSearchSettingsPatch{
			APIKey: webSearchStringPtr(""),
		}, "settings"},
	}
	for _, tc := range cases {
		_, details, err := applyWebSearchPatch(current, tc.patch, &fakeSecretCipher{})
		if err != nil {
			t.Fatalf("%s: 不应返回内部错误: %v", tc.name, err)
		}
		if details[tc.field] == "" {
			t.Fatalf("%s: 应在 %s 返回校验错误: %v", tc.name, tc.field, details)
		}
	}
}

func TestApplyWebSearchPatchCipherFailures(t *testing.T) {
	current := settings.DefaultWebSearch()
	if _, _, err := applyWebSearchPatch(current, webSearchSettingsPatch{
		APIKey: webSearchStringPtr("sk-new"),
	}, nil); !errors.Is(err, errSearchCipherUnavailable) {
		t.Fatalf("缺少加密器应返回配置错误: %v", err)
	}
	wantErr := errors.New("encrypt failed")
	if _, _, err := applyWebSearchPatch(current, webSearchSettingsPatch{
		APIKey: webSearchStringPtr("sk-new"),
	}, &fakeSecretCipher{err: wantErr}); !errors.Is(err, wantErr) {
		t.Fatalf("应返回加密错误: %v", err)
	}
}

func TestWebSearchResponseNeverExposesCipher(t *testing.T) {
	response := webSearchResponse(settings.WebSearchValues{
		BaseURL:      "https://api.deepseek.com",
		APIKeyCipher: "v1:secret-cipher",
	}, true)
	if !response.Configured || !response.Usable || response.APIKeyMasked == "" {
		t.Fatalf("已配置状态不符: %+v", response)
	}
	if strings.Contains(response.APIKeyMasked, "secret") || strings.Contains(response.APIKeyMasked, "v1:") {
		t.Fatalf("掩码泄漏密文: %q", response.APIKeyMasked)
	}
}

func TestWebSearchConfiguredRequiresDecryptableKey(t *testing.T) {
	values := settings.WebSearchValues{
		BaseURL:      "https://api.deepseek.com",
		APIKeyCipher: "v1:secret-cipher",
	}
	cipher := &fakeSecretCipher{canDecrypt: false}
	service := &Service{secretCipher: cipher}
	if service.webSearchConfigured(values) {
		t.Fatal("无法解密的密钥不应显示为已配置")
	}
	cipher.canDecrypt = true
	if !service.webSearchConfigured(values) {
		t.Fatal("可解密且 Base URL 存在时应显示为已配置")
	}
	values.BaseURL = ""
	if service.webSearchConfigured(values) {
		t.Fatal("缺少 Base URL 时不应显示为已配置")
	}
}

func TestApplyWebSearchPatchRejectsControlCharacters(t *testing.T) {
	_, details, err := applyWebSearchPatch(settings.DefaultWebSearch(), webSearchSettingsPatch{
		APIKey: webSearchStringPtr("sk-ok\r\nInjected: yes"),
	}, &fakeSecretCipher{canDecrypt: true})
	if err != nil {
		t.Fatalf("不应返回内部错误: %v", err)
	}
	if details["api_key"] == "" {
		t.Fatalf("包含控制字符的密钥应被拒绝: %v", details)
	}
}
