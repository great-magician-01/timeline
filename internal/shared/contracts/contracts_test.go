package contracts

import (
	"encoding/json"
	"testing"
)

// TestBehaviorSettingsDailyLimit 校验率限制 JSON 的解析与回退规则：
// 空/非法 JSON 视为不限制；total/空串读 daily_total；reply 未单独配置时回退 daily_comment；
// 非正数视为不限制。
func TestBehaviorSettingsDailyLimit(t *testing.T) {
	limits := func(value map[string]int) json.RawMessage {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("构造 RateLimits JSON 失败: %v", err)
		}
		return raw
	}
	cases := []struct {
		name   string
		raw    json.RawMessage
		action string
		want   int
	}{
		{"空 RateLimits 不限制", nil, "post", 0},
		{"非法 JSON 不限制", json.RawMessage(`{not-json`), "post", 0},
		{"total 读取 daily_total", limits(map[string]int{"daily_total": 5, "daily_post": 2}), "total", 5},
		{"空动作读取 daily_total", limits(map[string]int{"daily_total": 5}), "", 5},
		{"具体动作读取对应键", limits(map[string]int{"daily_post": 3}), "post", 3},
		{"reply 回退 daily_comment", limits(map[string]int{"daily_comment": 4}), "reply", 4},
		{"reply 优先自己的键", limits(map[string]int{"daily_reply": 2, "daily_comment": 4}), "reply", 2},
		{"未配置动作不限制", limits(map[string]int{"daily_post": 3}), "comment", 0},
		{"非正数视为不限制", limits(map[string]int{"daily_post": 0}), "post", 0},
		{"负数视为不限制", limits(map[string]int{"daily_post": -1}), "post", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			settings := BehaviorSettings{RateLimits: tc.raw}
			if got := settings.DailyLimit(tc.action); got != tc.want {
				t.Fatalf("DailyLimit(%q) = %d，期望 %d", tc.action, got, tc.want)
			}
		})
	}
}

// TestWebSearchConfigConfigured 校验搜索能力可用性只取决于 BaseURL 与 APIKey 同时存在。
func TestWebSearchConfigConfigured(t *testing.T) {
	cases := []struct {
		name   string
		config WebSearchConfig
		want   bool
	}{
		{"两者都存在", WebSearchConfig{BaseURL: "https://search.example.com", APIKey: "sk"}, true},
		{"缺少 BaseURL", WebSearchConfig{APIKey: "sk"}, false},
		{"缺少 APIKey", WebSearchConfig{BaseURL: "https://search.example.com"}, false},
		{"都缺失", WebSearchConfig{}, false},
	}
	for _, tc := range cases {
		if got := tc.config.Configured(); got != tc.want {
			t.Errorf("%s: Configured() = %v，期望 %v", tc.name, got, tc.want)
		}
	}
}

// TestWebSearchSettingsReady 校验设置快照：已保存密文才算 Configured，
// Configured 且 BaseURL 非空才 Ready。
func TestWebSearchSettingsReady(t *testing.T) {
	cases := []struct {
		name       string
		settings   WebSearchSettings
		configured bool
		ready      bool
	}{
		{"完整配置", WebSearchSettings{BaseURL: "https://s.example.com", APIKeyCipher: "v1:x"}, true, true},
		{"有密钥无地址", WebSearchSettings{APIKeyCipher: "v1:x"}, true, false},
		{"有地址无密钥", WebSearchSettings{BaseURL: "https://s.example.com"}, false, false},
		{"空配置", WebSearchSettings{}, false, false},
	}
	for _, tc := range cases {
		if got := tc.settings.Configured(); got != tc.configured {
			t.Errorf("%s: Configured() = %v，期望 %v", tc.name, got, tc.configured)
		}
		if got := tc.settings.Ready(); got != tc.ready {
			t.Errorf("%s: Ready() = %v，期望 %v", tc.name, got, tc.ready)
		}
	}
}
