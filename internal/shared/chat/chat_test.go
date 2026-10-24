package chat

import (
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
)

func TestValidateSource(t *testing.T) {
	if err := ValidateSource(strings.Repeat("a", MaxMessageSourceBytes)); err != nil {
		t.Fatalf("正好达到上限不应报错：%v", err)
	}
	err := ValidateSource(strings.Repeat("a", MaxMessageSourceBytes+1))
	if !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("超限应返回 validation 错误，得到 %v", err)
	}
}

func TestValidateBody(t *testing.T) {
	cases := []struct {
		name     string
		safeHTML string
		plain    string
		wantErr  bool
	}{
		{"正常文本", "<p>你好</p>", "你好", false},
		{"空白拒绝", "<p></p>", "   ", true},
		{"图片拒绝", `<p><img src="x"></p>`, "你好", true},
		{"超长拒绝", "<p>a</p>", strings.Repeat("字", MaxMessageRunes+1), true},
		{"正好上限", "<p>a</p>", strings.Repeat("字", MaxMessageRunes), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateBody(tc.safeHTML, tc.plain)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateBody(%q) err=%v，期望出错=%v", tc.plain, err, tc.wantErr)
			}
		})
	}
}

func TestPreview(t *testing.T) {
	if got := Preview(true, "正文"); got != RecalledPreview {
		t.Fatalf("撤回预览 = %q，期望 %q", got, RecalledPreview)
	}
	if got := Preview(false, strings.Repeat("字", PreviewRunes+5)); len([]rune(got)) != PreviewRunes+1 {
		t.Fatalf("截断预览长度 = %d，期望 %d（含省略号）", len([]rune(got)), PreviewRunes+1)
	}
	if got := Preview(false, "短文本"); got != "短文本" {
		t.Fatalf("短文本预览 = %q", got)
	}
}

func TestConstants(t *testing.T) {
	if RecallWindow != 2*time.Minute {
		t.Fatalf("撤回窗口 = %v，期望 2 分钟", RecallWindow)
	}
}
