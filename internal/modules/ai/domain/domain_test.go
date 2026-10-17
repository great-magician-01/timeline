package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func floatPtr(v float64) *float64 { return &v }
func intPtr(v int) *int           { return &v }

func TestParseGenerationParamsInput(t *testing.T) {
	params, err := ParseGenerationParamsInput(json.RawMessage(
		`{"max_tokens":128,"temperature":0.5,"top_p":0.9,"stop":["END"]}`))
	if err != nil {
		t.Fatalf("合法参数不应报错: %v", err)
	}
	if params.MaxTokens != 128 || params.Temperature == nil || *params.Temperature != 0.5 ||
		params.TopP == nil || *params.TopP != 0.9 || len(params.Stop) != 1 {
		t.Fatalf("参数解析错误: %+v", params)
	}
	for _, invalid := range []string{
		`{"max_tokens":0}`,
		`{"max_tokens":100001}`,
		`{"temperature":2.5}`,
		`{"top_p":-0.1}`,
		`{"stop":[""]}`,
		`{"unknown":1}`,
		`{"max_tokens":"128"}`,
	} {
		if _, err := ParseGenerationParamsInput(json.RawMessage(invalid)); err == nil {
			t.Errorf("非法参数应被拒绝: %s", invalid)
		}
	}
	if params, err := ParseGenerationParamsInput(nil); err != nil || params.MaxTokens != 0 {
		t.Fatalf("空参数应返回零值: %+v %v", params, err)
	}
}

func TestGenerationParamsJSONRoundTrip(t *testing.T) {
	params := GenerationParams{MaxTokens: 64, Temperature: floatPtr(0.2), TopP: floatPtr(0.8), Stop: []string{"END"}}
	raw, err := GenerationParamsJSON(params)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	parsed, err := GenerationParamsFromJSON(raw)
	if err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}
	if parsed.MaxTokens != 64 || *parsed.Temperature != 0.2 || *parsed.TopP != 0.8 || len(parsed.Stop) != 1 {
		t.Fatalf("往返结果不一致: %+v", parsed)
	}
}

func TestBehaviorPatchAndValidate(t *testing.T) {
	if settings := DefaultBehaviorSettings(); settings.CooldownSeconds != 900 || settings.MaxReplyDepth != 2 {
		t.Fatalf("默认行为设置与数据库默认值不一致: %+v", settings)
	}
	settings, details := PatchAndValidateBehavior(DefaultBehaviorSettings(), BehaviorPatch{
		AllowOriginalPost:    boolPtr(true),
		CandidateScopeType:   strPtr("tags"),
		CandidateScopeParams: json.RawMessage(`{"tags":["go"]}`),
		RateLimits:           json.RawMessage(`{"daily_like":5}`),
	})
	if len(details) != 0 {
		t.Fatalf("合法补丁不应报错: %v", details)
	}
	if !settings.AllowOriginalPost || settings.CandidateScopeType != "tags" || settings.DailyLimit("like") != 5 {
		t.Fatalf("补丁应用错误: %+v", settings)
	}

	bad := []BehaviorPatch{
		{CandidateScopeType: strPtr("unknown")},
		{CandidateScopeType: strPtr("tags")},
		{CandidateScopeType: strPtr("authors")},
		{RateLimits: json.RawMessage(`{"daily_unknown":1}`)},
		{RateLimits: json.RawMessage(`{"daily_like":1001}`)},
		{CooldownSeconds: intPtr(-1)},
		{ActiveHoursStart: intPtr(24)},
		{MaxReplyDepth: intPtr(6)},
		{Schedule: json.RawMessage(`[]`)},
	}
	for _, patch := range bad {
		if _, details := PatchAndValidateBehavior(DefaultBehaviorSettings(), patch); len(details) == 0 {
			t.Errorf("非法补丁应被拒绝: %+v", patch)
		}
	}
}

func TestNormalizeMemoryWrite(t *testing.T) {
	write, details := NormalizeMemoryWrite(" 记住了  ", "", []string{" Go ", "go", ""}, nil, "")
	if len(details) != 0 {
		t.Fatalf("合法记忆不应报错: %v", details)
	}
	if write.Content != "记住了" || write.Category != MemoryCategoryEvent ||
		write.Importance != 3 || write.InferenceLevel != MemoryInferenceFact {
		t.Fatalf("默认值归一化错误: %+v", write)
	}
	if len(write.Keywords) != 1 || write.Keywords[0] != "Go" {
		t.Fatalf("关键词去重错误: %v", write.Keywords)
	}

	for _, content := range []string{"", strings.Repeat("超", MaxMemoryContentRunes+1)} {
		if _, details := NormalizeMemoryWrite(content, "", nil, nil, ""); details["content"] == "" {
			t.Errorf("非法正文应被拒绝: %q", content)
		}
	}
	if _, details := NormalizeMemoryWrite("x", "unknown", nil, nil, ""); details["category"] == "" {
		t.Error("非法分类应被拒绝")
	}
	if _, details := NormalizeMemoryWrite("x", "", nil, intPtr(6), ""); details["importance"] == "" {
		t.Error("非法重要性应被拒绝")
	}
	if _, details := NormalizeMemoryWrite("x", "", nil, nil, "unknown"); details["inference_level"] == "" {
		t.Error("非法推断级别应被拒绝")
	}
}

func TestProviderValidation(t *testing.T) {
	if details := ValidateProviderFields("预设", "https://api.example.com", "gpt-5"); len(details) != 0 {
		t.Fatalf("合法预设不应报错: %v", details)
	}
	for _, baseURL := range []string{"", "ftp://x", "not-a-url", "https://" + strings.Repeat("a", 600)} {
		if err := ValidateProviderBaseURL(baseURL); err == nil {
			t.Errorf("非法接口地址应被拒绝: %q", baseURL)
		}
	}
	if count, blocked := ProviderDisableConflict(true, false, 2); !blocked || count != 2 {
		t.Fatalf("停用冲突判断错误: %d %v", count, blocked)
	}
	if _, blocked := ProviderDisableConflict(true, true, 2); blocked {
		t.Error("启用方向不应冲突")
	}
	if _, blocked := ProviderDisableConflict(false, false, 0); blocked {
		t.Error("无关联 AI 时停用不应冲突")
	}
}

func TestValidateIdentity(t *testing.T) {
	in := TrimCreateInput(CreateInput{Username: " ai_writer "})
	if in.Username != "ai_writer" || in.Nickname != "ai_writer" || in.TargetLanguage != "zh-CN" {
		t.Fatalf("创建输入归一化错误: %+v", in)
	}
	if details := ValidateCreate(in); len(details) != 0 {
		t.Fatalf("合法创建输入不应报错: %v", details)
	}
	invalid := []CreateInput{
		{Username: "1abc"},
		{Username: "a"},
		{Username: "ai_writer", Nickname: strings.Repeat("名", MaxNicknameRunes+1)},
		{Username: "ai_writer", Bio: strings.Repeat("简", MaxBioRunes+1)},
		{Username: "ai_writer", SystemPrompt: strings.Repeat("p", MaxSystemPromptRunes+1)},
		{Username: "ai_writer", TargetLanguage: strings.Repeat("x", MaxLanguageTagRunes+1)},
	}
	for _, item := range invalid {
		if details := ValidateCreate(item); len(details) == 0 {
			t.Errorf("非法创建输入应被拒绝: %+v", item)
		}
	}
	if details := ValidateUpdate(UpdateInput{Username: strPtr("bad name")}); len(details) == 0 {
		t.Error("非法用户名更新应被拒绝")
	}
}

func boolPtr(v bool) *bool    { return &v }
func strPtr(v string) *string { return &v }
