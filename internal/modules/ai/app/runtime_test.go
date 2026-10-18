package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/great-magician-01/timeline/internal/modules/ai/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
)

// 说明：runtime.go 的映射函数与全局搜索配置读取是纯逻辑（端口可 fake），
// 运行时快照整体组装因读取 repo 无法单测，只覆盖账号校验分支与配置解密分支。

// TestProfileToContracts 校验身份资料视图映射。
func TestProfileToContracts(t *testing.T) {
	profile := domain.Profile{
		UserID:         7,
		Background:     "背景",
		Personality:    "性格",
		CoreValues:     "价值观",
		LanguageStyle:  "语言风格",
		Topics:         "话题",
		TargetLanguage: "zh-CN",
		Taboos:         "禁忌",
		SystemPrompt:   "提示词",
		IsEnabled:      true,
	}
	got := profileToContracts(profile)
	if got.UserID != 7 || got.Background != "背景" || got.Personality != "性格" ||
		got.CoreValues != "价值观" || got.LanguageStyle != "语言风格" || got.Topics != "话题" ||
		got.TargetLanguage != "zh-CN" || got.Taboos != "禁忌" ||
		got.SystemPrompt != "提示词" || !got.IsEnabled {
		t.Fatalf("Profile 映射字段错误: %+v", got)
	}
}

// TestProviderConfigToContracts 校验预设运行时视图映射。
func TestProviderConfigToContracts(t *testing.T) {
	got := providerConfigToContracts(domain.ProviderConfig{
		ID: 3, Name: "预设", BaseURL: "https://api.example.com/v1",
		APIKey: "sk-runtime", DefaultModel: "gpt-demo", IsEnabled: true,
	})
	if got.ID != 3 || got.Name != "预设" || got.BaseURL != "https://api.example.com/v1" ||
		got.APIKey != "sk-runtime" || got.DefaultModel != "gpt-demo" || !got.IsEnabled {
		t.Fatalf("ProviderConfig 映射字段错误: %+v", got)
	}
}

// TestGenerationParamsToContracts 校验生成参数映射。
func TestGenerationParamsToContracts(t *testing.T) {
	params := domain.GenerationParams{
		MaxTokens:   256,
		Temperature: floatPtr(0.7),
		TopP:        floatPtr(0.9),
		Stop:        []string{"\n\n"},
	}
	got := generationParamsToContracts(params)
	if got.MaxTokens != 256 || got.Temperature == nil || *got.Temperature != 0.7 ||
		got.TopP == nil || *got.TopP != 0.9 || len(got.Stop) != 1 || got.Stop[0] != "\n\n" {
		t.Fatalf("GenerationParams 映射字段错误: %+v", got)
	}

	empty := generationParamsToContracts(domain.GenerationParams{})
	if empty.MaxTokens != 0 || empty.Temperature != nil || empty.TopP != nil || empty.Stop != nil {
		t.Fatalf("空生成参数应映射为零值: %+v", empty)
	}
}

// TestBehaviorSettingsToContracts 校验行为策略映射（含 JSONB 原样透传）。
func TestBehaviorSettingsToContracts(t *testing.T) {
	settings := domain.BehaviorSettings{
		AllowOriginalPost:    true,
		AllowActiveComment:   true,
		AllowPassiveReply:    true,
		AllowLike:            true,
		AllowFollow:          true,
		AllowUnfollow:        true,
		AllowRepost:          true,
		AllowQuote:           true,
		AllowAIToAI:          true,
		AllowWebSearch:       true,
		CandidateScopeType:   "tags",
		CandidateScopeParams: json.RawMessage(`{"tags":["go"]}`),
		RateLimits:           json.RawMessage(`{"daily_like":10}`),
		CooldownSeconds:      120,
		ActiveHoursStart:     9,
		ActiveHoursEnd:       22,
		Schedule:             json.RawMessage(`{"cron":"0 * * * *"}`),
		MaxReplyDepth:        3,
	}
	got := behaviorSettingsToContracts(settings)
	if !got.AllowOriginalPost || !got.AllowActiveComment || !got.AllowPassiveReply ||
		!got.AllowLike || !got.AllowFollow || !got.AllowUnfollow || !got.AllowRepost ||
		!got.AllowQuote || !got.AllowAIToAI || !got.AllowWebSearch {
		t.Fatalf("行为开关映射错误: %+v", got)
	}
	if got.CandidateScopeType != "tags" ||
		string(got.CandidateScopeParams) != `{"tags":["go"]}` ||
		string(got.RateLimits) != `{"daily_like":10}` ||
		got.CooldownSeconds != 120 || got.ActiveHoursStart != 9 || got.ActiveHoursEnd != 22 ||
		string(got.Schedule) != `{"cron":"0 * * * *"}` || got.MaxReplyDepth != 3 {
		t.Fatalf("行为字段映射错误: %+v", got)
	}
}

// TestWebSearchConfig 校验全局联网搜索配置读取、校验与解密。
func TestWebSearchConfig(t *testing.T) {
	ctx := context.Background()
	cipher, err := encryptSecret(testSecretKey, "search-key-plain")
	if err != nil {
		t.Fatalf("构造搜索密钥密文失败: %v", err)
	}

	t.Run("读取设置失败", func(t *testing.T) {
		cause := errors.New("设置表不可用")
		svc := testService(Deps{Settings: fakeSettings{err: cause}})
		_, err := svc.WebSearchConfig(ctx)
		requireKind(t, err, apperr.KindInternal)
		if !errors.Is(err, cause) {
			t.Fatal("应保留设置读取错误底因")
		}
	})

	t.Run("未配置密钥", func(t *testing.T) {
		svc := testService(Deps{Settings: fakeSettings{webSearch: contracts.WebSearchSettings{BaseURL: "https://search.example.com"}}})
		got, err := svc.WebSearchConfig(ctx)
		if err != nil {
			t.Fatalf("未配置密钥不应报错: %v", err)
		}
		if got.BaseURL != "https://search.example.com" || got.APIKey != "" {
			t.Fatalf("未配置时应只返回 BaseURL: %+v", got)
		}
		if got.Configured() {
			t.Fatal("未配置密钥时 Configured 应为 false")
		}
	})

	t.Run("密钥已保存但缺少地址", func(t *testing.T) {
		svc := testService(Deps{Settings: fakeSettings{webSearch: contracts.WebSearchSettings{APIKeyCipher: cipher}}})
		_, err := svc.WebSearchConfig(ctx)
		e := requireKind(t, err, apperr.KindInternal)
		if !strings.Contains(e.Error(), "Base URL 缺失") {
			t.Fatalf("应保留配置无效原因，实际 %v", e)
		}
	})

	t.Run("密钥损坏", func(t *testing.T) {
		svc := testService(Deps{Settings: fakeSettings{webSearch: contracts.WebSearchSettings{
			BaseURL: "https://search.example.com", APIKeyCipher: "v1:broken",
		}}})
		got, err := svc.WebSearchConfig(ctx)
		requireKind(t, err, apperr.KindInternal)
		if got.BaseURL != "https://search.example.com" {
			t.Fatalf("解密失败时仍应返回 BaseURL 供排障: %+v", got)
		}
	})

	t.Run("完整配置解密成功", func(t *testing.T) {
		svc := testService(Deps{Settings: fakeSettings{webSearch: contracts.WebSearchSettings{
			BaseURL: "https://search.example.com", APIKeyCipher: cipher,
		}}})
		got, err := svc.WebSearchConfig(ctx)
		if err != nil {
			t.Fatalf("完整配置应解密成功: %v", err)
		}
		if got.BaseURL != "https://search.example.com" || got.APIKey != "search-key-plain" {
			t.Fatalf("搜索配置解密结果错误: %+v", got)
		}
		if !got.Configured() {
			t.Fatal("完整配置 Configured 应为 true")
		}
	})
}

// TestRuntimeTypesZeroValue 确认运行时视图类型零值可用（结构防漂移）。
func TestRuntimeTypesZeroValue(t *testing.T) {
	var runtime contracts.Runtime
	if runtime.AIID != 0 || runtime.Username != "" || runtime.Model != "" ||
		runtime.Timeout != 0 || runtime.Provider.ID != 0 {
		t.Fatalf("Runtime 零值异常: %+v", runtime)
	}
	var settings domain.Profile
	converted := profileToContracts(settings)
	if converted.UserID != 0 || converted.TargetLanguage != "" {
		t.Fatalf("Profile 零值映射异常: %+v", converted)
	}
}
