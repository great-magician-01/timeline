package http

import (
	"testing"

	"github.com/great-magician-01/timeline/internal/modules/media/domain"
)

// TestContentDisposition 验证下载头的拼装与非 ASCII 文件名编码。
func TestContentDisposition(t *testing.T) {
	cases := []struct {
		name     string
		kind     string
		filename string
		want     string
	}{
		{"ASCII 文件名", domain.KindFile, "report.txt", `attachment; filename="report.txt"`},
		{
			"中文文件名走 RFC 5987",
			domain.KindFile,
			"报告.pdf",
			`attachment; filename="download"; filename*=UTF-8''%E6%8A%A5%E5%91%8A.pdf`,
		},
		{
			"含引号的文件名走 RFC 5987",
			domain.KindFile,
			`a"b.txt`,
			`attachment; filename="download"; filename*=UTF-8''a%22b.txt`,
		},
		{"图片内联", domain.KindImage, "照片.jpg", "inline"},
		{"视频内联", domain.KindVideo, "视频.mp4", "inline"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := contentDisposition(tc.kind, tc.filename); got != tc.want {
				t.Fatalf("contentDisposition(%q, %q) = %q，期望 %q", tc.kind, tc.filename, got, tc.want)
			}
		})
	}
}

func TestEncodeRFC5987(t *testing.T) {
	if got := encodeRFC5987("a b');"); got != "a%20b%27%29%3B" {
		t.Fatalf("encodeRFC5987 编码错误: %q", got)
	}
	if got := encodeRFC5987("abc-._~"); got != "abc-._~" {
		t.Fatalf("attr-char 不应被编码: %q", got)
	}
}

func TestASCIISafeFilename(t *testing.T) {
	if !asciiFilenameSafe("report.txt") {
		t.Fatal("普通 ASCII 文件名应安全")
	}
	for _, unsafe := range []string{"", "报告.pdf", `a"b`, `a\b`, "a;b", "a\x01b"} {
		if asciiFilenameSafe(unsafe) {
			t.Errorf("文件名 %q 应走 RFC 5987 编码", unsafe)
		}
	}
}
