package domain_test

import (
	"strings"
	"testing"

	"github.com/great-magician-01/timeline/internal/modules/social/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
)

func TestNormalizeBookmarkFolderName(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "常规名称", raw: "  技术文章  ", want: "技术文章"},
		{name: "去除首尾空白", raw: "\t读书笔记\n", want: "读书笔记"},
		{name: "单个字符", raw: "A", want: "A"},
		{name: "恰好 30 个 rune", raw: strings.Repeat("收", 30), want: strings.Repeat("收", 30)},
		{name: "超过 30 个 rune", raw: strings.Repeat("收", 31), wantErr: true},
		{name: "空白视为空", raw: "   ", wantErr: true},
		{name: "完全为空", raw: "", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := domain.NormalizeBookmarkFolderName(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望返回校验错误，实际得到 %q", got)
				}
				if _, ok := apperr.AsKind(err); !ok {
					t.Fatalf("期望领域错误，实际 %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("期望成功，实际错误 %v", err)
			}
			if got != tc.want {
				t.Fatalf("名称 = %q，期望 %q", got, tc.want)
			}
		})
	}
}
