package settings

import (
	"encoding/json"
	"testing"
	"time"
)

// 本文件覆盖 values.go 与 keys.go 的纯函数边界（不涉及数据库）。

func TestBoolValue(t *testing.T) {
	cases := []struct {
		raw    string
		want   bool
		wantOK bool
	}{
		{`true`, true, true},
		{`false`, false, true},
		{`"true"`, false, false},
		{`1`, false, false},
		{`null`, false, true}, // json.Unmarshal 对 null 不报错，语义上等同 false
		{``, false, false},
	}
	for _, tc := range cases {
		got, ok := boolValue([]byte(tc.raw))
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("boolValue(%q) = (%v, %v)，期望 (%v, %v)", tc.raw, got, ok, tc.want, tc.wantOK)
		}
	}
}

func TestInt64Value(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		want   int64
		wantOK bool
	}{
		{"JSON 整数", `42`, 42, true},
		{"JSON 负数", `-7`, -7, true},
		{"字符串数字", `"1024"`, 1024, true},
		{"字符串带空格", `" 512 "`, 512, true},
		{"字符串前缀数字", `"12abc"`, 12, true},
		{"字符串非数字", `"abc"`, 0, false},
		{"小数", `1.5`, 0, false},
		{"布尔值", `true`, 0, false},
		{"非法 JSON", `{`, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := int64Value([]byte(tc.raw))
			if got != tc.want || ok != tc.wantOK {
				t.Fatalf("int64Value(%q) = (%v, %v)，期望 (%v, %v)", tc.raw, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestStringValue(t *testing.T) {
	cases := []struct {
		raw    string
		want   string
		wantOK bool
	}{
		{`"https://example.com"`, "https://example.com", true},
		{`""`, "", true},
		{`123`, "", false},
		{`null`, "", true}, // json.Unmarshal 对 null 不报错，语义上等同空字符串
		{`not-json`, "", false},
	}
	for _, tc := range cases {
		got, ok := stringValue([]byte(tc.raw))
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("stringValue(%q) = (%q, %v)，期望 (%q, %v)", tc.raw, got, ok, tc.want, tc.wantOK)
		}
	}
}

// TestApplyUnknownKeyAndBoundaries 覆盖未知键、非正值忽略与请求上限兜底。
func TestApplyUnknownKeyAndBoundaries(t *testing.T) {
	values := Defaults()
	before := values
	apply(&values, "unknown.key", json.RawMessage(`1`))
	if values != before {
		t.Fatalf("未知键不应修改设置: %+v", values)
	}

	// 请求上限兜底逻辑对所有键都生效：请求上限必须至少为文件上限 + 1 MiB。
	values.MaxUploadBytes = 4096
	values.MaxRequestBytes = 1
	apply(&values, "unknown.key", json.RawMessage(`1`))
	if want := values.MaxUploadBytes + DefaultUploadOverhead; values.MaxRequestBytes != want {
		t.Fatalf("兜底后请求上限 = %d，期望 %d", values.MaxRequestBytes, want)
	}

	// 调用记录保留期允许 0（表示关闭自动清理），负数忽略。
	values = Defaults()
	apply(&values, KeyCallLogRetentionDays, json.RawMessage(`0`))
	if values.CallLogRetentionDays != 0 {
		t.Fatalf("保留期 0 应被接受: %+v", values)
	}
	apply(&values, KeyCallLogRetentionDays, json.RawMessage(`-1`))
	if values.CallLogRetentionDays != 0 {
		t.Fatalf("负数保留期应被忽略: %+v", values)
	}
}

func TestValidateBoundaries(t *testing.T) {
	if details := Defaults().Validate(); len(details) != 0 {
		t.Fatalf("默认设置应合法: %+v", details)
	}

	// 边界值恰好合法：请求上限刚好多 1 MiB、窗口刚好 1 秒、保留期刚好 1 小时。
	ok := Defaults()
	ok.MaxRequestBytes = ok.MaxUploadBytes + DefaultUploadOverhead
	ok.PostRateWindow = time.Second
	ok.UploadRateWindow = time.Second
	ok.TempAttachmentRetention = time.Hour
	ok.CallLogRetentionDays = 0
	if details := ok.Validate(); len(details) != 0 {
		t.Fatalf("边界合法值不应报错: %+v", details)
	}

	cases := []struct {
		name   string
		mutate func(*Values)
		want   string
	}{
		{"文件上限", func(v *Values) { v.MaxUploadBytes = 0 }, KeyUploadMaxFileBytes},
		{"请求上限", func(v *Values) { v.MaxRequestBytes = 1 }, KeyUploadMaxRequestBytes},
		{"存储配额", func(v *Values) { v.StorageQuotaBytes = 0 }, KeyStorageQuotaBytes},
		{"发帖次数", func(v *Values) { v.PostRateLimit = 0 }, KeyPostRateLimit},
		{"发帖窗口", func(v *Values) { v.PostRateWindow = time.Second - time.Nanosecond }, KeyPostRateWindowSeconds},
		{"上传次数", func(v *Values) { v.UploadRateLimit = 0 }, KeyUploadRateLimit},
		{"上传窗口", func(v *Values) { v.UploadRateWindow = 0 }, KeyUploadRateWindowSeconds},
		{"临时附件保留期", func(v *Values) { v.TempAttachmentRetention = time.Hour - time.Nanosecond }, KeyTempAttachmentRetentionHour},
		{"调用记录截断", func(v *Values) { v.CallLogMaxBytes = 0 }, KeyCallLogMaxBytes},
		{"调用记录保留期", func(v *Values) { v.CallLogRetentionDays = -1 }, KeyCallLogRetentionDays},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			values := Defaults()
			tc.mutate(&values)
			details := values.Validate()
			if len(details) != 1 {
				t.Fatalf("应只返回一个字段错误，实际 %+v", details)
			}
			if message, exists := details[tc.want]; !exists || message == "" {
				t.Fatalf("缺少字段 %q 的错误: %+v", tc.want, details)
			}
		})
	}

	// 多项非法时一次性返回全部字段错误（调用记录保留期 0 合法，故比用例数少 1）。
	details := (Values{}).Validate()
	if len(details) != len(cases)-1 {
		t.Fatalf("非法设置应返回全部 %d 个字段错误，实际 %+v", len(cases)-1, details)
	}
	for _, tc := range cases {
		if tc.want == KeyCallLogRetentionDays {
			continue
		}
		if message, exists := details[tc.want]; !exists || message == "" {
			t.Errorf("多项非法时缺少字段 %q 的错误: %+v", tc.want, details)
		}
	}
}

func TestToMap(t *testing.T) {
	values := Values{
		MaxUploadBytes:          1048576,
		MaxRequestBytes:         2097152,
		StorageQuotaBytes:       5 << 30,
		PostRateLimit:           12,
		PostRateWindow:          90 * time.Second,
		UploadRateLimit:         30,
		UploadRateWindow:        2 * time.Minute,
		TempAttachmentRetention: 48*time.Hour + 30*time.Minute,
		CallLogMaxBytes:         4096,
		CallLogRetentionDays:    7,
	}
	got := values.ToMap(true, false)
	want := map[string]any{
		"registration_enabled":            true,
		"ai_global_enabled":               false,
		"max_upload_bytes":                int64(1048576),
		"max_request_bytes":               int64(2097152),
		"storage_quota_bytes":             int64(5 << 30),
		"post_rate_limit":                 12,
		"post_rate_window_seconds":        90,
		"upload_rate_limit":               30,
		"upload_rate_window_seconds":      120,
		"temp_attachment_retention_hours": 48,
		"call_log_max_bytes":              4096,
		"call_log_retention_days":         7,
	}
	if len(got) != len(want) {
		t.Fatalf("ToMap 字段数量 = %d，期望 %d", len(got), len(want))
	}
	for key, wantValue := range want {
		gotValue, exists := got[key]
		if !exists || gotValue != wantValue {
			t.Errorf("ToMap[%q] = %v（存在=%v），期望 %v", key, gotValue, exists, wantValue)
		}
	}
}

// TestWebSearchValuesMatrix 覆盖 Configured/Ready 的布尔矩阵。
func TestWebSearchValuesMatrix(t *testing.T) {
	cases := []struct {
		name       string
		values     WebSearchValues
		wantConfig bool
		wantReady  bool
	}{
		{"都未配置", WebSearchValues{}, false, false},
		{"只有密钥", WebSearchValues{APIKeyCipher: "cipher"}, true, false},
		{"只有地址", WebSearchValues{BaseURL: "https://example.com"}, false, false},
		{"密钥与地址都有", WebSearchValues{APIKeyCipher: "cipher", BaseURL: "https://example.com"}, true, true},
		{"地址为空白", WebSearchValues{APIKeyCipher: "cipher", BaseURL: "   "}, true, false},
		{"密钥为空白", WebSearchValues{APIKeyCipher: "  ", BaseURL: "https://example.com"}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.values.Configured(); got != tc.wantConfig {
				t.Errorf("Configured = %v，期望 %v", got, tc.wantConfig)
			}
			if got := tc.values.Ready(); got != tc.wantReady {
				t.Errorf("Ready = %v，期望 %v", got, tc.wantReady)
			}
		})
	}
}

func TestNewStore(t *testing.T) {
	store := NewStore(nil)
	if store == nil {
		t.Fatal("NewStore 不应返回 nil")
	}
	if store.pool != nil {
		t.Fatal("传入 nil 连接池时应原样保存")
	}
}
