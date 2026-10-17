package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/great-magician-01/timeline/internal/shared/contracts"
)

// fakeCipher 是测试用敏感配置加密器。
type fakeCipher struct {
	err        error
	canDecrypt bool
}

func (f fakeCipher) EncryptSecret(plain string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return "v1:" + plain, nil
}

func (f fakeCipher) CanDecryptSecret(encoded string) bool {
	if encoded == "" {
		return true
	}
	return f.canDecrypt
}

func configuredWebSearch() contracts.WebSearchSettings {
	return contracts.WebSearchSettings{
		BaseURL:      "https://api.example.com",
		APIKeyCipher: "v1:old-key",
	}
}

func TestApplyWebSearchPatchBaseURL(t *testing.T) {
	next, details, err := applyWebSearchPatch(contracts.WebSearchSettings{}, WebSearchPatch{
		BaseURL: ptr(" https://api.example.com/ "),
	}, fakeCipher{})
	if err != nil || len(details) != 0 {
		t.Fatalf("err=%v details=%v", err, details)
	}
	if next.BaseURL != "https://api.example.com" {
		t.Fatalf("BaseURL = %q", next.BaseURL)
	}
}

func TestApplyWebSearchPatchInvalidBaseURL(t *testing.T) {
	_, details, err := applyWebSearchPatch(contracts.WebSearchSettings{}, WebSearchPatch{
		BaseURL: ptr("ftp://example.com"),
	}, fakeCipher{})
	if err != nil || details["base_url"] == "" {
		t.Fatalf("err=%v details=%v", err, details)
	}
}

func TestApplyWebSearchPatchEncryptsKey(t *testing.T) {
	next, details, err := applyWebSearchPatch(configuredWebSearch(), WebSearchPatch{
		APIKey: ptr("  new-key  "),
	}, fakeCipher{})
	if err != nil || len(details) != 0 {
		t.Fatalf("err=%v details=%v", err, details)
	}
	if next.APIKeyCipher != "v1:new-key" {
		t.Fatalf("APIKeyCipher = %q", next.APIKeyCipher)
	}
}

func TestApplyWebSearchPatchEmptyKeyKeepsCurrent(t *testing.T) {
	current := configuredWebSearch()
	next, details, err := applyWebSearchPatch(current, WebSearchPatch{APIKey: ptr("   ")}, fakeCipher{})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if next.APIKeyCipher != current.APIKeyCipher {
		t.Fatalf("空 api_key 应保留原密钥: %q", next.APIKeyCipher)
	}
	if details["settings"] == "" {
		t.Fatalf("未发生变更时应给出提示: %v", details)
	}
}

func TestApplyWebSearchPatchClearKey(t *testing.T) {
	next, details, err := applyWebSearchPatch(configuredWebSearch(), WebSearchPatch{
		ClearAPIKey: ptrBool(true),
	}, fakeCipher{})
	if err != nil || len(details) != 0 {
		t.Fatalf("err=%v details=%v", err, details)
	}
	if next.APIKeyCipher != "" {
		t.Fatalf("clear_api_key 应清空密文: %q", next.APIKeyCipher)
	}
}

func TestApplyWebSearchPatchRejectsConflict(t *testing.T) {
	_, details, err := applyWebSearchPatch(configuredWebSearch(), WebSearchPatch{
		APIKey:      ptr("new-key"),
		ClearAPIKey: ptrBool(true),
	}, fakeCipher{})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if details["api_key"] == "" {
		t.Fatalf("同时保存与清除应被拒绝: %v", details)
	}
}

func TestApplyWebSearchPatchRejectsControlChars(t *testing.T) {
	_, details, err := applyWebSearchPatch(contracts.WebSearchSettings{}, WebSearchPatch{
		APIKey: ptr("bad\nkey"),
	}, fakeCipher{})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if details["api_key"] == "" {
		t.Fatalf("控制字符应被拒绝: %v", details)
	}
}

func TestApplyWebSearchPatchCipherUnavailable(t *testing.T) {
	_, _, err := applyWebSearchPatch(contracts.WebSearchSettings{}, WebSearchPatch{
		APIKey: ptr("new-key"),
	}, nil)
	if !errors.Is(err, errSearchCipherUnavailable) {
		t.Fatalf("err = %v, want errSearchCipherUnavailable", err)
	}
}

func TestApplyWebSearchPatchCipherError(t *testing.T) {
	sentinel := errors.New("boom")
	_, _, err := applyWebSearchPatch(contracts.WebSearchSettings{}, WebSearchPatch{
		APIKey: ptr("new-key"),
	}, fakeCipher{err: sentinel})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v", err)
	}
}

func TestApplyWebSearchPatchDefaultsBaseURL(t *testing.T) {
	next, details, err := applyWebSearchPatch(contracts.WebSearchSettings{}, WebSearchPatch{
		APIKey: ptr("new-key"),
	}, fakeCipher{})
	if err != nil || len(details) != 0 {
		t.Fatalf("err=%v details=%v", err, details)
	}
	if !strings.HasPrefix(next.BaseURL, "https://") {
		t.Fatalf("未配置 Base URL 时应回退默认地址: %q", next.BaseURL)
	}
}

func TestWebSearchUsable(t *testing.T) {
	values := configuredWebSearch()
	// 主密钥可解密 → 可用。
	service := &Service{cipher: fakeCipher{canDecrypt: true}}
	if !service.webSearchUsable(values) {
		t.Fatal("可解密时应判定可用")
	}
	// 主密钥不匹配（例如换了 ENCRYPTION_KEY）→ 不可用。
	broken := &Service{cipher: fakeCipher{canDecrypt: false}}
	if broken.webSearchUsable(values) {
		t.Fatal("不可解密时应判定不可用")
	}
	// 未配置密钥 → 不可用。
	if service.webSearchUsable(contracts.WebSearchSettings{BaseURL: "https://api.example.com"}) {
		t.Fatal("未配置密钥时不应判定可用")
	}
	// 加密端口未装配时按“已就绪即可用”降级。
	flexible := &Service{}
	if !flexible.webSearchUsable(values) {
		t.Fatal("未装配加密端口时应降级为可用")
	}
}

func TestWebSearchSnapshotMasksKey(t *testing.T) {
	view := webSearchSnapshot(configuredWebSearch(), true)
	if !view.Configured || !view.Usable || view.APIKeyMasked == "" {
		t.Fatalf("已配置时应有掩码: %+v", view)
	}
	if view.APIKeyMasked == "old-key" {
		t.Fatal("掩码不得包含明文")
	}
	empty := webSearchSnapshot(contracts.WebSearchSettings{}, false)
	if empty.Configured || empty.APIKeyMasked != "" {
		t.Fatalf("未配置时不应有掩码: %+v", empty)
	}
}

func ptr(value string) *string { return &value }

func ptrBool(value bool) *bool { return &value }
