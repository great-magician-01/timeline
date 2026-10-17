package page

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestIDCursorRoundTrip(t *testing.T) {
	raw := EncodeIDCursor(987654321)
	got, err := DecodeIDCursor(raw)
	if err != nil {
		t.Fatalf("解析游标失败: %v", err)
	}
	if !got.Valid() || got.ID != 987654321 {
		t.Fatalf("游标往返错误: %+v", got)
	}
	// query 参数可能带空白，解析前应统一 trim。
	if padded, err := DecodeIDCursor("  " + raw + " "); err != nil || padded.ID != 987654321 {
		t.Fatalf("带空白的游标解析错误: %+v, %v", padded, err)
	}
}

func TestIDCursorEmptyMeansFirstPage(t *testing.T) {
	for _, raw := range []string{"", "   "} {
		got, err := DecodeIDCursor(raw)
		if err != nil || got.Valid() {
			t.Fatalf("空游标应表示首页: %+v, %v", got, err)
		}
	}
}

func TestIDCursorRejectsInvalidPayload(t *testing.T) {
	encode := func(value any) string {
		data, _ := json.Marshal(value)
		return base64.RawURLEncoding.EncodeToString(data)
	}
	cases := map[string]string{
		"非 base64": "!!!not-base64!!!",
		"未知字段":     encode(map[string]any{"id": 1, "x": 2}),
		"ID 非正数":   encode(map[string]any{"id": 0}),
		"ID 为字符串":  encode(map[string]any{"id": "1"}),
		"非对象":      encode([]int{1}),
	}
	for name, raw := range cases {
		if _, err := DecodeIDCursor(raw); !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("%s：应返回 ErrInvalidCursor，实际 %v", name, err)
		}
	}
}

func TestTimeCursorRoundTripKeepsUTC(t *testing.T) {
	at := time.Date(2026, 10, 17, 20, 0, 0, 123456789, time.FixedZone("CST", 8*60*60))
	raw := EncodeTimeCursor(at, 42)
	got, err := DecodeTimeCursor(raw)
	if err != nil {
		t.Fatalf("解析时间游标失败: %v", err)
	}
	if !got.Valid() || got.ID != 42 {
		t.Fatalf("时间游标往返错误: %+v", got)
	}
	if !got.Time.Equal(at) {
		t.Fatalf("时间不相等: got=%v want=%v", got.Time, at)
	}
	if got.Time.Location() != time.UTC {
		t.Fatalf("解码后的时间应为 UTC: %v", got.Time.Location())
	}
	// 内部 JSON 形状保持旧版字段名 t/i，避免升级瞬间已发出的游标失效。
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		t.Fatalf("游标不是 RawURLEncoding: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("游标内容不是 JSON: %v", err)
	}
	if _, ok := payload["t"]; !ok {
		t.Fatalf("时间游标缺少 t 字段: %s", data)
	}
	if _, ok := payload["i"]; !ok {
		t.Fatalf("时间游标缺少 i 字段: %s", data)
	}
}

func TestTimeCursorEmptyMeansFirstPage(t *testing.T) {
	got, err := DecodeTimeCursor("")
	if err != nil || got.Valid() {
		t.Fatalf("空游标应表示首页: %+v, %v", got, err)
	}
}

func TestTimeCursorRejectsInvalidPayload(t *testing.T) {
	encode := func(value any) string {
		data, _ := json.Marshal(value)
		return base64.RawURLEncoding.EncodeToString(data)
	}
	cases := map[string]string{
		"非 base64": "@@@",
		"未知字段":     encode(map[string]any{"t": "2026-10-17T20:00:00Z", "i": 1, "x": 1}),
		"ID 非正数":   encode(map[string]any{"t": "2026-10-17T20:00:00Z", "i": 0}),
		"时间缺失":     encode(map[string]any{"i": 1}),
		"时间格式错误":   encode(map[string]any{"t": "not-a-time", "i": 1}),
	}
	for name, raw := range cases {
		if _, err := DecodeTimeCursor(raw); !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("%s：应返回 ErrInvalidCursor，实际 %v", name, err)
		}
	}
}

func TestParseLimit(t *testing.T) {
	cases := []struct {
		raw   string
		want  int
		valid bool
	}{
		{"", DefaultLimit, true},
		{"   ", DefaultLimit, true},
		{"1", 1, true},
		{" 20 ", 20, true},
		{"50", 50, true},
		{"0", 0, false},
		{"-1", 0, false},
		{"51", 0, false},
		{"abc", 0, false},
		{"1.5", 0, false},
	}
	for _, tc := range cases {
		got, err := ParseLimit(tc.raw)
		if tc.valid {
			if err != nil || got != tc.want {
				t.Errorf("ParseLimit(%q) = %d, %v；期望 %d", tc.raw, got, err, tc.want)
			}
		} else if !errors.Is(err, ErrInvalidLimit) || got != 0 {
			t.Errorf("ParseLimit(%q) 应失败，实际 %d, %v", tc.raw, got, err)
		}
	}
}
