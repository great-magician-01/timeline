package search

import "testing"

// TestEscapeLikePattern 覆盖 LIKE 特殊字符的字面量转义。
func TestEscapeLikePattern(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"普通文本不变", "hello 世界", "hello 世界"},
		{"反斜杠", `a\b`, `a\\b`},
		{"百分号", "100%", `100\%`},
		{"下划线", "a_b", `a\_b`},
		{"混合", `50%_a\b`, `50\%\_a\\b`},
		{"空串", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EscapeLikePattern(tc.input); got != tc.want {
				t.Fatalf("EscapeLikePattern(%q) = %q，期望 %q", tc.input, got, tc.want)
			}
		})
	}
}
