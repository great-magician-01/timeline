package ai

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/settings"
)

// 所有测试都不依赖数据库，只覆盖纯逻辑：加解密、掩码、参数解析、校验与导出契约。

func testKey() []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	return key
}

func floatPtr(v float64) *float64 { return &v }
func intPtr(v int) *int           { return &v }
func boolPtr(v bool) *bool        { return &v }
func stringPtr(v string) *string  { return &v }

func TestEncryptDecryptSecretRoundTrip(t *testing.T) {
	key := testKey()
	const plain = "sk-test-abcdefghijklmnop"
	encoded, err := EncryptSecret(key, plain)
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}
	if strings.Contains(encoded, plain) {
		t.Fatal("密文不得包含明文")
	}
	if !strings.HasPrefix(encoded, "v1:") {
		t.Fatalf("密文缺少版本前缀: %q", encoded)
	}
	decoded, err := DecryptSecret(key, encoded)
	if err != nil {
		t.Fatalf("解密失败: %v", err)
	}
	if decoded != plain {
		t.Fatalf("解密结果不一致: %q", decoded)
	}
	// 随机 nonce：同一明文两次加密结果不同。
	again, err := EncryptSecret(key, plain)
	if err != nil {
		t.Fatalf("二次加密失败: %v", err)
	}
	if again == encoded {
		t.Fatal("两次加密结果不应相同（nonce 应为随机值）")
	}
}

func TestDecodeWebSearchConfig(t *testing.T) {
	const apiKey = "sk-global-search"
	cipherText, err := EncryptSecret(testKey(), apiKey)
	if err != nil {
		t.Fatalf("加密搜索密钥失败: %v", err)
	}
	config, err := decodeWebSearchConfig(testKey(), settings.WebSearchValues{
		BaseURL:      "https://api.deepseek.com",
		APIKeyCipher: cipherText,
	})
	if err != nil || config.APIKey != apiKey || config.BaseURL != "https://api.deepseek.com" {
		t.Fatalf("搜索配置解密不符: %+v, %v", config, err)
	}
	empty, err := decodeWebSearchConfig(testKey(), settings.WebSearchValues{BaseURL: "https://example.com"})
	if err != nil || empty.APIKey != "" || empty.BaseURL != "https://example.com" {
		t.Fatalf("未配置密钥应返回空 API Key: %+v, %v", empty, err)
	}
	if _, err := decodeWebSearchConfig(testKey(), settings.WebSearchValues{
		BaseURL: "https://example.com", APIKeyCipher: "v1:broken",
	}); err == nil {
		t.Fatal("损坏密文应返回错误")
	}
	if _, err := decodeWebSearchConfig(testKey(), settings.WebSearchValues{
		APIKeyCipher: cipherText,
	}); err == nil {
		t.Fatal("有密钥但缺少 Base URL 时必须失败关闭")
	}
	if (settings.WebSearchValues{BaseURL: "https://example.com"}).Ready() {
		t.Fatal("没有密钥时不应 Ready")
	}
	service := &Service{secretKey: testKey()}
	if !service.CanDecryptSecret(cipherText) {
		t.Fatal("当前主密钥应能解开搜索密钥")
	}
	if service.CanDecryptSecret("v1:broken") {
		t.Fatal("损坏密文不应通过校验")
	}
}

func TestEncryptDecryptSecretEmpty(t *testing.T) {
	key := testKey()
	encoded, err := EncryptSecret(key, "")
	if err != nil || encoded != "" {
		t.Fatalf("空明文应返回空字符串，得到 %q, %v", encoded, err)
	}
	decoded, err := DecryptSecret(key, "")
	if err != nil || decoded != "" {
		t.Fatalf("空密文应返回空字符串，得到 %q, %v", decoded, err)
	}
}

func TestDecryptSecretTamperDetection(t *testing.T) {
	key := testKey()
	encoded, err := EncryptSecret(key, "sk-tamper-check-1234")
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(encoded, "v1:"))
	if err != nil {
		t.Fatalf("解码密文失败: %v", err)
	}
	raw[len(raw)-1] ^= 0x01
	tampered := "v1:" + base64.RawURLEncoding.EncodeToString(raw)
	if _, err := DecryptSecret(key, tampered); err == nil {
		t.Fatal("被篡改的密文必须解密失败")
	}
	// 前缀不符。
	if _, err := DecryptSecret(key, "v2:"+strings.TrimPrefix(encoded, "v1:")); err == nil {
		t.Fatal("未知版本前缀必须报错")
	}
	// base64 损坏。
	if _, err := DecryptSecret(key, "v1:!!!!"); err == nil {
		t.Fatal("非法 base64 必须报错")
	}
	// 长度不足。
	if _, err := DecryptSecret(key, "v1:AAAA"); err == nil {
		t.Fatal("长度不足的密文必须报错")
	}
}

func TestDecryptSecretWrongKey(t *testing.T) {
	encoded, err := EncryptSecret(testKey(), "sk-wrong-key-check")
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}
	wrong := make([]byte, 32)
	for i := range wrong {
		wrong[i] = byte(255 - i)
	}
	if _, err := DecryptSecret(wrong, encoded); err == nil {
		t.Fatal("错误密钥必须解密失败")
	}
}

func TestEncryptSecretInvalidKeyLength(t *testing.T) {
	if _, err := EncryptSecret(make([]byte, 16), "sk-short-key"); err == nil {
		t.Fatal("非 32 字节密钥必须报错")
	}
	if _, err := DecryptSecret(make([]byte, 8), "v1:AAAA"); err == nil {
		t.Fatal("非 32 字节密钥解密必须报错")
	}
}

func TestMaskSecret(t *testing.T) {
	cases := []struct {
		name  string
		plain string
		want  string
	}{
		{"空字符串", "", "****"},
		{"三位", "abc", "****"},
		{"七位", "1234567", "****"},
		{"八位", "12345678", "123...5678"},
		{"典型密钥", "sk-abcdefgh", "sk-...efgh"},
		{"长密钥", "sk-1234567890abcdef", "sk-...cdef"},
		{"多字节字符", "密钥一二三四五六七八", "密钥一...五六七八"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MaskSecret(tc.plain)
			if got != tc.want {
				t.Fatalf("MaskSecret(%q) = %q，期望 %q", tc.plain, got, tc.want)
			}
			if tc.plain != "" && got == tc.plain {
				t.Fatal("掩码不得等于明文")
			}
			if len([]rune(tc.plain)) >= 8 && strings.Contains(got, tc.plain[3:]) && tc.plain != "" {
				// 掩码只保留前 3 与后 4 个字符，中间内容不得出现。
			}
		})
	}
}

func TestGenerationParamsFromJSON(t *testing.T) {
	full, err := GenerationParamsFromJSON([]byte(`{"max_tokens":512,"temperature":0.7,"top_p":0.9,"stop":["\n\n","END"]}`))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if full.MaxTokens != 512 || full.Temperature == nil || *full.Temperature != 0.7 ||
		full.TopP == nil || *full.TopP != 0.9 || len(full.Stop) != 2 {
		t.Fatalf("解析结果不符合预期: %+v", full)
	}
	for _, raw := range []string{"", "null", "{}", "   "} {
		params, err := GenerationParamsFromJSON([]byte(raw))
		if err != nil {
			t.Fatalf("输入 %q 不应报错: %v", raw, err)
		}
		if params.MaxTokens != 0 || params.Temperature != nil || params.TopP != nil || len(params.Stop) != 0 {
			t.Fatalf("输入 %q 应返回零值: %+v", raw, params)
		}
	}
	// 未知键忽略（兼容后续扩展）。
	params, err := GenerationParamsFromJSON([]byte(`{"max_tokens":64,"future_key":1}`))
	if err != nil || params.MaxTokens != 64 {
		t.Fatalf("未知键应被忽略: %+v, %v", params, err)
	}
}

func TestGenerationParamsFromJSONInvalidInput(t *testing.T) {
	invalid := []string{
		`{"max_tokens":"many"}`,
		`{"max_tokens":1.5}`,
		`{"temperature":"hot"}`,
		`{"top_p":"high"}`,
		`{"stop":[1,2]}`,
		`{"stop":"END"}`,
		`[]`,
		`not-json`,
	}
	for _, raw := range invalid {
		if _, err := GenerationParamsFromJSON([]byte(raw)); err == nil {
			t.Fatalf("非法输入 %q 必须报错", raw)
		}
	}
}

func TestParseGenerationParamsInputStrict(t *testing.T) {
	if _, err := parseGenerationParamsInput(json.RawMessage(`{"max_tokens":128}`)); err != nil {
		t.Fatalf("合法输入不应报错: %v", err)
	}
	invalid := []string{
		`{"unknown":1}`,
		`{"max_tokens":0}`,
		`{"max_tokens":100001}`,
		`{"temperature":3}`,
		`{"top_p":2}`,
		`{"stop":[""]}`,
		`{"stop":["a","b","c","d","e","f","g","h","i"]}`,
		`{"stop":["` + strings.Repeat("长", 65) + `"]}`,
	}
	for _, raw := range invalid {
		if _, err := parseGenerationParamsInput(json.RawMessage(raw)); err == nil {
			t.Fatalf("非法生成参数 %q 必须报错", raw)
		}
	}
	params, err := parseGenerationParamsInput(nil)
	if err != nil || params.MaxTokens != 0 {
		t.Fatalf("空输入应返回零值: %+v, %v", params, err)
	}
}

func TestDailyLimit(t *testing.T) {
	settings := BehaviorSettings{
		RateLimits: json.RawMessage(`{"daily_like":5,"daily_comment":7,"daily_total":30,"daily_repost":0}`),
	}
	cases := []struct {
		action string
		want   int
	}{
		{"like", 5},
		{"comment", 7},
		{"reply", 7}, // 未单独配置 reply，回退到 comment 上限
		{"original_post", 0},
		{"repost", 0}, // 0 表示不限制
		{"total", 30},
		{"", 30},
	}
	for _, tc := range cases {
		if got := settings.DailyLimit(tc.action); got != tc.want {
			t.Fatalf("DailyLimit(%q) = %d，期望 %d", tc.action, got, tc.want)
		}
	}
	if got := (BehaviorSettings{}).DailyLimit("like"); got != 0 {
		t.Fatalf("空率限制应返回 0，得到 %d", got)
	}
	if got := (BehaviorSettings{RateLimits: json.RawMessage(`not-json`)}).DailyLimit("like"); got != 0 {
		t.Fatalf("非法率限制应返回 0，得到 %d", got)
	}
}

func TestDefaultBehaviorMatchesDatabaseDefaults(t *testing.T) {
	settings := defaultBehaviorSettings()
	if settings.AllowPassiveReply != true || settings.AllowOriginalPost || settings.AllowAIToAI || settings.AllowWebSearch {
		t.Fatalf("默认动作开关不符合数据库默认值: %+v", settings)
	}
	if settings.CandidateScopeType != "none" || settings.CooldownSeconds != 900 ||
		settings.ActiveHoursStart != 8 || settings.ActiveHoursEnd != 23 || settings.MaxReplyDepth != 2 {
		t.Fatalf("默认行为参数不符合数据库默认值: %+v", settings)
	}
}

func TestApplyBehaviorPatch(t *testing.T) {
	base := defaultBehaviorSettings()
	patched := applyBehaviorPatch(base, behaviorInput{
		AllowLike:            boolPtr(true),
		AllowWebSearch:       boolPtr(true),
		CandidateScopeType:   stringPtr("tags"),
		CandidateScopeParams: json.RawMessage(`{"tags":["go"]}`),
		ActiveHoursStart:     intPtr(10),
	})
	if !patched.AllowLike || !patched.AllowWebSearch || patched.CandidateScopeType != "tags" || patched.ActiveHoursStart != 10 {
		t.Fatalf("补丁未正确应用: %+v", patched)
	}
	if !patched.AllowPassiveReply || patched.CooldownSeconds != 900 {
		t.Fatalf("未提供的字段应保持默认值: %+v", patched)
	}
	if applyBehaviorPatch(patched, behaviorInput{AllowWebSearch: boolPtr(false)}).AllowWebSearch {
		t.Fatal("显式 false 应能关闭联网搜索")
	}
	if !applyBehaviorPatch(patched, behaviorInput{}).AllowWebSearch {
		t.Fatal("未提供搜索字段时应保留原值")
	}
}

func TestValidateBehaviorCandidateScope(t *testing.T) {
	valid := []behaviorInput{
		{CandidateScopeType: stringPtr("none")},
		{CandidateScopeType: stringPtr("global_public")},
		{CandidateScopeType: stringPtr("followed")},
		{CandidateScopeType: stringPtr("tags"), CandidateScopeParams: json.RawMessage(`{"tags":["go"]}`)},
		{CandidateScopeType: stringPtr("authors"), CandidateScopeParams: json.RawMessage(`{"usernames":["alice"]}`)},
	}
	for i, in := range valid {
		if _, details := patchAndValidateBehavior(defaultBehaviorSettings(), in); len(details) > 0 {
			t.Fatalf("合法候选范围 #%d 不应报错: %v", i, details)
		}
	}
	invalid := []behaviorInput{
		{CandidateScopeType: stringPtr("tags")},
		{CandidateScopeType: stringPtr("tags"), CandidateScopeParams: json.RawMessage(`{"tags":[]}`)},
		{CandidateScopeType: stringPtr("tags"), CandidateScopeParams: json.RawMessage(`{"tags":["  "]}`)},
		{CandidateScopeType: stringPtr("authors")},
		{CandidateScopeType: stringPtr("authors"), CandidateScopeParams: json.RawMessage(`{"usernames":[]}`)},
		{CandidateScopeType: stringPtr("tags"), CandidateScopeParams: json.RawMessage(`["go"]`)},
		{CandidateScopeType: stringPtr("everywhere")},
	}
	for i, in := range invalid {
		_, details := patchAndValidateBehavior(defaultBehaviorSettings(), in)
		if len(details) == 0 {
			t.Fatalf("非法候选范围 #%d 必须报错: %+v", i, in)
		}
	}
}

func TestValidateRateLimits(t *testing.T) {
	if message := validateRateLimits(json.RawMessage(`{}`)); message != "" {
		t.Fatalf("空率限制不应报错: %s", message)
	}
	if message := validateRateLimits(json.RawMessage(`{"daily_like":0,"daily_total":1000,"daily_comment":10}`)); message != "" {
		t.Fatalf("合法率限制不应报错: %s", message)
	}
	invalid := []string{
		`{"unknown_limit":1}`,
		`{"daily_like":-1}`,
		`{"daily_like":1001}`,
		`{"daily_like":1.5}`,
		`{"daily_like":"5"}`,
		`[]`,
		`"text"`,
	}
	for _, raw := range invalid {
		if message := validateRateLimits(json.RawMessage(raw)); message == "" {
			t.Fatalf("非法率限制 %q 必须报错", raw)
		}
	}
}

func TestValidateBehaviorRanges(t *testing.T) {
	settings := defaultBehaviorSettings()
	settings.CooldownSeconds = -1
	settings.ActiveHoursStart = 24
	settings.ActiveHoursEnd = -1
	settings.MaxReplyDepth = 6
	details := validateBehaviorSettings(settings)
	for _, field := range []string{"cooldown_seconds", "active_hours_start", "active_hours_end", "max_reply_depth"} {
		if details[field] == "" {
			t.Fatalf("字段 %s 越界必须报错: %v", field, details)
		}
	}
}

func validCreateInput() aiCreateInput {
	return aiCreateInput{
		Username:         "ai_writer",
		Nickname:         "写作助手",
		Bio:              "分享写作技巧",
		Background:       "长期写作的 AI",
		Personality:      "沉稳",
		CoreValues:       "真实",
		LanguageStyle:    "简洁",
		Topics:           "写作",
		TargetLanguage:   "zh-CN",
		Taboos:           "政治",
		SystemPrompt:     "你是写作助手",
		ProviderID:       "1234567890123456",
		ModelOverride:    "",
		GenerationParams: json.RawMessage(`{"max_tokens":256,"temperature":0.7}`),
		Behavior:         behaviorInput{AllowPassiveReply: boolPtr(true)},
	}
}

func TestValidateAICreateUsernameRules(t *testing.T) {
	invalid := []string{"", "ab", "1abc", "_abc", "abc-", "中文名", "abc def", strings.Repeat("a", 33)}
	for _, username := range invalid {
		in := validCreateInput()
		in.Username = username
		failure := validateAICreate(in)
		if failure == nil {
			t.Fatalf("用户名 %q 必须校验失败", username)
		}
		details, ok := failure.Details.(map[string]string)
		if !ok || details["username"] == "" {
			t.Fatalf("用户名 %q 的错误详情缺失: %+v", username, failure.Details)
		}
	}
	for _, username := range []string{"abc", "ai_writer", "A1_", strings.Repeat("a", 32)} {
		in := validCreateInput()
		in.Username = username
		in.Nickname = ""
		if failure := validateAICreate(in); failure != nil {
			t.Fatalf("用户名 %q 不应校验失败: %+v", username, failure)
		}
	}
}

func TestValidateAICreateFields(t *testing.T) {
	if failure := validateAICreate(validCreateInput()); failure != nil {
		t.Fatalf("合法创建请求不应报错: %+v", failure)
	}

	cases := []struct {
		name   string
		mutate func(*aiCreateInput)
		field  string
	}{
		{"缺少预设", func(in *aiCreateInput) { in.ProviderID = "" }, "provider_id"},
		{"非法预设 ID", func(in *aiCreateInput) { in.ProviderID = "0" }, "provider_id"},
		{"生成参数非法", func(in *aiCreateInput) { in.GenerationParams = json.RawMessage(`{"max_tokens":"many"}`) }, "generation_params"},
		{"生成参数未知键", func(in *aiCreateInput) { in.GenerationParams = json.RawMessage(`{"top_n":5}`) }, "generation_params"},
		{"昵称过长", func(in *aiCreateInput) { in.Nickname = strings.Repeat("长", 51) }, "nickname"},
		{"简介过长", func(in *aiCreateInput) { in.Bio = strings.Repeat("字", 501) }, "bio"},
		{"背景过长", func(in *aiCreateInput) { in.Background = strings.Repeat("字", 1001) }, "background"},
		{"提示词过长", func(in *aiCreateInput) { in.SystemPrompt = strings.Repeat("字", 8001) }, "system_prompt"},
		{"目标语言过长", func(in *aiCreateInput) { in.TargetLanguage = strings.Repeat("x", 21) }, "target_language"},
		{"模型覆盖过长", func(in *aiCreateInput) { in.ModelOverride = strings.Repeat("m", 201) }, "model_override"},
		{"候选范围缺参数", func(in *aiCreateInput) {
			in.Behavior = behaviorInput{CandidateScopeType: stringPtr("tags")}
		}, "candidate_scope_params"},
		{"率限制未知键", func(in *aiCreateInput) {
			in.Behavior = behaviorInput{RateLimits: json.RawMessage(`{"daily_unknown":1}`)}
		}, "rate_limits"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := validCreateInput()
			tc.mutate(&in)
			failure := validateAICreate(in)
			if failure == nil {
				t.Fatal("必须校验失败")
			}
			details, ok := failure.Details.(map[string]string)
			if !ok || details[tc.field] == "" {
				t.Fatalf("缺少字段 %s 的错误详情: %+v", tc.field, failure.Details)
			}
		})
	}
}

func TestValidateAIUpdateFields(t *testing.T) {
	if failure := validateAIUpdate(aiUpdateInput{Nickname: stringPtr("新昵称")}); failure != nil {
		t.Fatalf("合法更新不应报错: %+v", failure)
	}
	if failure := validateAIUpdate(aiUpdateInput{}); failure != nil {
		t.Fatalf("空更新不应报错: %+v", failure)
	}
	bad := []struct {
		name  string
		in    aiUpdateInput
		field string
	}{
		{"非法用户名", aiUpdateInput{Username: stringPtr("1bad")}, "username"},
		{"空昵称", aiUpdateInput{Nickname: stringPtr("  ")}, "nickname"},
		{"简介过长", aiUpdateInput{Bio: stringPtr(strings.Repeat("字", 501))}, "bio"},
		{"提示词过长", aiUpdateInput{SystemPrompt: stringPtr(strings.Repeat("字", 8001))}, "system_prompt"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			failure := validateAIUpdate(tc.in)
			if failure == nil {
				t.Fatal("必须校验失败")
			}
			details, _ := failure.Details.(map[string]string)
			if details[tc.field] == "" {
				t.Fatalf("缺少字段 %s 的错误详情: %+v", tc.field, failure.Details)
			}
		})
	}
}

func TestValidateProviderFields(t *testing.T) {
	if details := validateProviderFields("主预设", "https://api.example.com/v1", "gpt-5"); len(details) > 0 {
		t.Fatalf("合法预设不应报错: %v", details)
	}
	invalid := []struct {
		name, baseURL, model, field string
	}{
		{"", "https://api.example.com", "m", "name"},
		{"n", "ftp://api.example.com", "m", "base_url"},
		{"n", "api.example.com", "m", "base_url"},
		{"n", "https://", "m", "base_url"},
		{"n", "https://api.example.com", "", "default_model"},
	}
	for _, tc := range invalid {
		details := validateProviderFields(tc.name, tc.baseURL, tc.model)
		if details[tc.field] == "" {
			t.Fatalf("字段 %s 应校验失败: %v", tc.field, details)
		}
	}
}

func TestNormalizeMemoryWrite(t *testing.T) {
	content, category, keywords, importance, inference, details := normalizeMemoryWrite(
		"  用户喜欢 Go 语言  ", "", []string{"Go", " go ", ""}, nil, "")
	if len(details) > 0 {
		t.Fatalf("合法记忆不应报错: %v", details)
	}
	if content != "用户喜欢 Go 语言" || category != "event" || importance != 3 || inference != "fact" {
		t.Fatalf("归一化结果不符合预期: %q %q %d %q", content, category, importance, inference)
	}
	if len(keywords) != 1 || keywords[0] != "Go" {
		t.Fatalf("关键词去重/去空失败: %v", keywords)
	}

	_, _, _, _, _, details = normalizeMemoryWrite(
		"", "unknown", []string{strings.Repeat("长", 33)}, intPtr(6), "guess")
	for _, field := range []string{"content", "category", "keywords", "importance", "inference_level"} {
		if details[field] == "" {
			t.Fatalf("字段 %s 应校验失败: %v", field, details)
		}
	}
	tooMany := make([]string, 0, 21)
	for i := 0; i < 21; i++ {
		tooMany = append(tooMany, string(rune('a'+i)))
	}
	if _, _, _, _, _, details := normalizeMemoryWrite("正文", "fact", tooMany, intPtr(3), "fact"); details["keywords"] == "" {
		t.Fatal("超过 20 个关键词必须报错")
	}
}

func TestJsonOrEmpty(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, json.RawMessage(""), json.RawMessage("null")} {
		if got := string(jsonOrEmpty(raw)); got != "{}" {
			t.Fatalf("空值应回退为 {}，得到 %q", got)
		}
	}
	if got := string(jsonOrEmpty(json.RawMessage(`{"tags":["go"]}`))); got != `{"tags":["go"]}` {
		t.Fatalf("合法 JSON 不应被改写: %q", got)
	}
}

// TestExportedContractShape 以结构体字面量固定 internal/agent 依赖的字段名，
// 防止后续无意的重命名破坏跨包契约。
func TestExportedContractShape(t *testing.T) {
	provider := ProviderConfig{
		ID: 1, Name: "预设", BaseURL: "https://api.example.com", APIKey: "sk-x",
		DefaultModel: "gpt-5", IsEnabled: true,
	}
	params := GenerationParams{MaxTokens: 128, Temperature: floatPtr(0.5), TopP: floatPtr(0.9), Stop: []string{"END"}}
	behavior := BehaviorSettings{
		AllowOriginalPost: true, AllowActiveComment: true, AllowPassiveReply: true,
		AllowLike: true, AllowFollow: true, AllowUnfollow: true, AllowRepost: true,
		AllowQuote: true, AllowAIToAI: false,
		CandidateScopeType: "tags", CandidateScopeParams: json.RawMessage(`{"tags":["go"]}`),
		RateLimits: json.RawMessage(`{"daily_like":5}`), CooldownSeconds: 900,
		ActiveHoursStart: 8, ActiveHoursEnd: 23, Schedule: json.RawMessage(`{}`), MaxReplyDepth: 2,
	}
	runtime := Runtime{
		AIID: 10, Username: "ai_writer", Nickname: "写作助手",
		Profile: Profile{
			UserID: 10, Background: "b", Personality: "p", CoreValues: "v", LanguageStyle: "l",
			Topics: "t", TargetLanguage: "zh-CN", Taboos: "x", SystemPrompt: "sp", IsEnabled: true,
		},
		Provider: provider, Model: "gpt-5", Params: params, Timeout: 60 * time.Second, Behavior: behavior,
	}
	if runtime.AIID != 10 || runtime.Profile.UserID != 10 || runtime.Behavior.DailyLimit("like") != 5 {
		t.Fatal("Runtime 字段契约不符合预期")
	}
	memory := Memory{
		ID: 1, AID: 10, MemoryType: "recent", Status: "active", Content: "内容",
		Category: "event", Keywords: []string{"go"}, Importance: 3,
		SourceType: "manual", SourceRefType: "post", SourceRefID: nil,
		InferenceLevel: "fact", CreatedAt: time.Now(), UpdatedAt: time.Now(), LastUsedAt: nil,
	}
	if memory.AID != 10 || memory.MemoryType != "recent" {
		t.Fatal("Memory 字段契约不符合预期")
	}
}

// TestNewClientWiring 只验证 callable 客户端可以按预设与生成参数构造，不做网络调用。
func TestNewClientWiring(t *testing.T) {
	service := &Service{log: nil, secretKey: testKey()}
	client := service.NewClient(ProviderConfig{
		ID: 1, Name: "本地 mock", BaseURL: "http://127.0.0.1:9/v1",
		APIKey: "sk-mock", DefaultModel: "mock-model", IsEnabled: true,
	}, GenerationParams{MaxTokens: 64, Temperature: floatPtr(0.2), TopP: floatPtr(0.8), Stop: []string{"END"}}, time.Second)
	if client == nil {
		t.Fatal("NewClient 不应返回 nil")
	}
	// 零值超时应回退到默认值。
	if client := service.NewClient(ProviderConfig{BaseURL: "http://127.0.0.1:9/v1"}, GenerationParams{}, 0); client == nil {
		t.Fatal("NewClient 零超时应回退默认值")
	}
}

func TestTruncateRunes(t *testing.T) {
	if got := truncateRunes("中文内容超长", 4); got != "中文内容…" {
		t.Fatalf("截断结果不符合预期: %q", got)
	}
	if got := truncateRunes("短", 4); got != "短" {
		t.Fatalf("未超长不应改写: %q", got)
	}
}
