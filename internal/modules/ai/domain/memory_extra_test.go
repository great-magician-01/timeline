package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestNormalizeMemoryLimit 校验记忆检索条数归一化：非法值回退默认 20，超过上限截到 50。
func TestNormalizeMemoryLimit(t *testing.T) {
	cases := []struct {
		input int
		want  int
	}{
		{0, DefaultMemoryLimit},
		{-3, DefaultMemoryLimit},
		{1, 1},
		{20, 20},
		{MaxMemoryLimit, MaxMemoryLimit},
		{MaxMemoryLimit + 1, MaxMemoryLimit},
	}
	for _, tc := range cases {
		if got := NormalizeMemoryLimit(tc.input); got != tc.want {
			t.Errorf("NormalizeMemoryLimit(%d) = %d，期望 %d", tc.input, got, tc.want)
		}
	}
}

// TestNormalizeMemoryTerms 校验关键词/分类的去空去重：保持首次出现顺序，去重区分大小写
// （关键词归一化只去空白，不做大小写折叠）。
func TestNormalizeMemoryTerms(t *testing.T) {
	keywords := NormalizeKeywordTerms([]string{" Go ", "", "go", "Go", "  数据库  ", "go"})
	want := []string{"Go", "go", "数据库"}
	if len(keywords) != len(want) {
		t.Fatalf("关键词 = %v，期望 %v", keywords, want)
	}
	for i := range want {
		if keywords[i] != want[i] {
			t.Fatalf("关键词[%d] = %q，期望 %q（完整结果 %v）", i, keywords[i], want[i], keywords)
		}
	}
	if empty := NormalizeKeywordTerms(nil); len(empty) != 0 {
		t.Fatalf("nil 关键词应返回空切片，实际 %v", empty)
	}

	categories := NormalizeCategories([]string{" fact ", "fact", "", "topic"})
	if len(categories) != 2 || categories[0] != "fact" || categories[1] != "topic" {
		t.Fatalf("分类归一化结果异常: %v", categories)
	}
}

// TestEscapeLikePattern 校验 ILIKE 通配符转义：反斜杠、%、_ 都被转义，普通字符不变。
func TestEscapeLikePattern(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"普通关键词", "普通关键词"},
		{"100%", `100\%`},
		{"a_b", `a\_b`},
		{`c:\temp`, `c:\\temp`},
		{`%_\`, `\%\_\\`},
	}
	for _, tc := range cases {
		if got := EscapeLikePattern(tc.input); got != tc.want {
			t.Errorf("EscapeLikePattern(%q) = %q，期望 %q", tc.input, got, tc.want)
		}
	}
}

// TestNormalizeMemoryWriteDefaults 校验记忆写入的默认值与字段级校验：
// 分类默认 event、推断级别默认 fact、重要性默认 3；各字段越界返回 details。
func TestNormalizeMemoryWriteDefaults(t *testing.T) {
	write, details := NormalizeMemoryWrite("  一条记忆  ", "", nil, nil, "")
	if len(details) != 0 {
		t.Fatalf("合法输入不应有字段错误: %v", details)
	}
	if write.Content != "一条记忆" {
		t.Fatalf("正文应去除首尾空白: %q", write.Content)
	}
	if write.Category != MemoryCategoryEvent {
		t.Fatalf("分类默认值 = %q，期望 event", write.Category)
	}
	if write.InferenceLevel != MemoryInferenceFact {
		t.Fatalf("推断级别默认值 = %q，期望 fact", write.InferenceLevel)
	}
	if write.Importance != 3 {
		t.Fatalf("重要性默认值 = %d，期望 3", write.Importance)
	}

	invalid := []struct {
		name       string
		content    string
		category   string
		keywords   []string
		importance *int
		inference  string
		wantField  string
	}{
		{"空正文", "   ", "", nil, nil, "", "content"},
		{"正文超长", strings.Repeat("字", MaxMemoryContentRunes+1), "", nil, nil, "", "content"},
		{"非法分类", "正文", "unknown", nil, nil, "", "category"},
		{"关键词超长", "正文", "", []string{strings.Repeat("k", MaxMemoryKeywordRunes+1)}, nil, "", "keywords"},
		{"重要性为 0", "正文", "", nil, intPtr(0), "", "importance"},
		{"重要性为 6", "正文", "", nil, intPtr(6), "", "importance"},
		{"非法推断级别", "正文", "", nil, nil, "guess", "inference_level"},
	}
	for _, tc := range invalid {
		_, details := NormalizeMemoryWrite(tc.content, tc.category, tc.keywords, tc.importance, tc.inference)
		if _, ok := details[tc.wantField]; !ok {
			t.Errorf("%s: 期望字段 %s 报错，实际 %v", tc.name, tc.wantField, details)
		}
	}

	// 关键词超过 20 个整体报错。
	many := make([]string, 0, MaxMemoryKeywords+1)
	for i := 0; i <= MaxMemoryKeywords; i++ {
		many = append(many, strings.Repeat("k", 1)+string(rune('a'+i%26))+string(rune('0'+i%10)))
	}
	if _, details := NormalizeMemoryWrite("正文", "", many, nil, ""); details["keywords"] == "" {
		t.Errorf("关键词超过上限应报错，实际 %v", details)
	}

	// 关键词按小写折叠去重（保留首次出现的写法），空关键词忽略。
	write, _ = NormalizeMemoryWrite("正文", "", []string{"Go", " go ", "go", ""}, nil, "")
	if len(write.Keywords) != 1 || write.Keywords[0] != "Go" {
		t.Fatalf("关键词去重结果异常: %v", write.Keywords)
	}
}

// TestProviderFieldValidators 校验预设字段校验的边界：名称/模型长度、URL 合法性。
func TestProviderFieldValidators(t *testing.T) {
	if details := ValidateProviderFields("默认预设", "https://api.example.com/v1", "gpt-4o-mini"); len(details) != 0 {
		t.Fatalf("合法预设不应有字段错误: %v", details)
	}
	details := ValidateProviderFields("", "ftp://api.example.com", strings.Repeat("m", MaxProviderModelRunes+1))
	for _, field := range []string{"name", "base_url", "default_model"} {
		if _, ok := details[field]; !ok {
			t.Errorf("期望字段 %s 报错，实际 %v", field, details)
		}
	}
	details = ValidateProviderFields(strings.Repeat("名", MaxProviderNameRunes+1), "https://ok.example.com", "model")
	if _, ok := details["name"]; !ok {
		t.Errorf("超长名称应报错，实际 %v", details)
	}

	urlCases := []struct {
		name    string
		baseURL string
		wantErr bool
	}{
		{"https 合法", "https://api.example.com/v1", false},
		{"http 合法", "http://127.0.0.1:8080", false},
		{"空地址", "", true},
		{"缺少协议", "api.example.com/v1", true},
		{"不支持的协议", "ftp://api.example.com", true},
		{"缺少主机", "https://", true},
		{"超长地址", "https://example.com/" + strings.Repeat("a", MaxProviderBaseURLRunes), true},
	}
	for _, tc := range urlCases {
		err := ValidateProviderBaseURL(tc.baseURL)
		if tc.wantErr && err == nil {
			t.Errorf("%s: 期望报错，实际通过", tc.name)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("%s: 不应报错，实际 %v", tc.name, err)
		}
	}
}

// TestProviderDisableConflict 校验停用预设冲突：只有「启用 → 停用且有 AI 关联」才拒绝并返回关联数。
func TestProviderDisableConflict(t *testing.T) {
	cases := []struct {
		name          string
		before, after bool
		aiCount       int
		wantCount     int
		wantConflict  bool
	}{
		{"启用改停用且有 AI 关联", true, false, 3, 3, true},
		{"启用改停用但无关联", true, false, 0, 0, false},
		{"保持启用", true, true, 3, 0, false},
		{"保持停用", false, false, 3, 0, false},
		{"停用改启用", false, true, 3, 0, false},
	}
	for _, tc := range cases {
		count, conflict := ProviderDisableConflict(tc.before, tc.after, tc.aiCount)
		if count != tc.wantCount || conflict != tc.wantConflict {
			t.Errorf("%s: (%d,%v)，期望 (%d,%v)", tc.name, count, conflict, tc.wantCount, tc.wantConflict)
		}
	}
	if message := ProviderDisableConflictMessage(3); !strings.Contains(message, "3") ||
		!strings.Contains(message, "预设") {
		t.Fatalf("冲突文案应包含关联数量与预设说明: %q", message)
	}
}

// TestValidateModelOverride 校验模型覆盖名长度限制与首尾空白处理。
func TestValidateModelOverride(t *testing.T) {
	if err := ValidateModelOverride(""); err != nil {
		t.Fatalf("空覆盖名应允许（表示使用默认模型）: %v", err)
	}
	if err := ValidateModelOverride("  "); err != nil {
		t.Fatalf("空白覆盖名应允许: %v", err)
	}
	if err := ValidateModelOverride(strings.Repeat("m", MaxModelOverrideRunes)); err != nil {
		t.Fatalf("恰好 200 字符应允许: %v", err)
	}
	if err := ValidateModelOverride(strings.Repeat("m", MaxModelOverrideRunes+1)); err == nil {
		t.Fatal("超过 200 字符应报错")
	}
}

// TestWebSearchConfigAvailability 校验搜索配置可用性：BaseURL 与 APIKey 缺一不可。
func TestWebSearchConfigAvailability(t *testing.T) {
	cases := []struct {
		name   string
		config WebSearchConfig
		want   bool
	}{
		{"完整配置", WebSearchConfig{BaseURL: "https://s.example.com", APIKey: "sk"}, true},
		{"缺少地址", WebSearchConfig{APIKey: "sk"}, false},
		{"缺少密钥", WebSearchConfig{BaseURL: "https://s.example.com"}, false},
		{"空配置", WebSearchConfig{}, false},
	}
	for _, tc := range cases {
		if got := tc.config.Configured(); got != tc.want {
			t.Errorf("%s: Configured() = %v，期望 %v", tc.name, got, tc.want)
		}
	}
}

// TestBehaviorDailyLimit 校验行为策略的每日上限解析：total/空串读 daily_total，
// reply 回退 daily_comment，非正数视为不限制。
func TestBehaviorDailyLimit(t *testing.T) {
	limits := func(value map[string]int) json.RawMessage {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("构造 RateLimits 失败: %v", err)
		}
		return raw
	}
	cases := []struct {
		name   string
		raw    json.RawMessage
		action string
		want   int
	}{
		{"空 RateLimits", nil, "post", 0},
		{"非法 JSON", json.RawMessage("{bad"), "post", 0},
		{"total", limits(map[string]int{"daily_total": 5}), "total", 5},
		{"空动作读 total", limits(map[string]int{"daily_total": 5}), "", 5},
		{"具体动作", limits(map[string]int{"daily_like": 7}), "like", 7},
		{"reply 回退 comment", limits(map[string]int{"daily_comment": 4}), "reply", 4},
		{"reply 优先自身", limits(map[string]int{"daily_reply": 2, "daily_comment": 4}), "reply", 2},
		{"未配置不限制", limits(map[string]int{"daily_post": 1}), "comment", 0},
		{"零值不限制", limits(map[string]int{"daily_post": 0}), "post", 0},
	}
	for _, tc := range cases {
		behavior := BehaviorSettings{RateLimits: tc.raw}
		if got := behavior.DailyLimit(tc.action); got != tc.want {
			t.Errorf("%s: DailyLimit(%q) = %d，期望 %d", tc.name, tc.action, got, tc.want)
		}
	}
}
