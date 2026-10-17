package domain

import (
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

func TestSanitizeFilename(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"POSIX 路径", "/tmp/upload/photo.jpg", "photo.jpg"},
		{"Windows 路径", `C:\Users\evil\报告.pdf`, "报告.pdf"},
		{"目录穿越", "../../etc/passwd", "passwd"},
		{"Windows 穿越", `..\..\secret.txt`, "secret.txt"},
		{"控制字符", "a\x00b\nc\r.jpg", "abc.jpg"},
		{"空文件名", "", DefaultFilename},
		{"只有路径", "///", DefaultFilename},
		{"上级目录", "..", DefaultFilename},
		{"保留中文与空格", "  假期 照片.png  ", "假期 照片.png"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SanitizeFilename(tc.raw); got != tc.want {
				t.Fatalf("SanitizeFilename(%q) = %q，期望 %q", tc.raw, got, tc.want)
			}
		})
	}

	long := strings.Repeat("字", 300) + ".jpg"
	got := SanitizeFilename(long)
	if runes := []rune(got); len(runes) != MaxOriginalFilenameRunes {
		t.Fatalf("超长文件名截断后长度 = %d，期望 %d", len(runes), MaxOriginalFilenameRunes)
	}
	if strings.ContainsAny(got, "/\\") {
		t.Fatalf("净化后的文件名不应含路径分隔符，得到 %q", got)
	}
}

func TestDerivativeTypeForKind(t *testing.T) {
	cases := map[string]string{
		KindImage: model.DerivativeThumbnail,
		KindVideo: model.DerivativeVideoCover,
		KindFile:  "",
		"":        "",
		"unknown": "",
	}
	for kind, want := range cases {
		if got := DerivativeTypeForKind(kind); got != want {
			t.Errorf("DerivativeTypeForKind(%q) = %q，期望 %q", kind, got, want)
		}
	}
}

func TestDerivativeJobKey(t *testing.T) {
	if got := DerivativeJobKey(12345, model.DerivativeThumbnail); got != "media_derivative:12345:thumbnail" {
		t.Fatalf("幂等键 = %q", got)
	}
}

func TestValidDerivativeType(t *testing.T) {
	if !ValidDerivativeType(model.DerivativeThumbnail) || !ValidDerivativeType(model.DerivativeVideoCover) {
		t.Fatal("合法衍生文件类型未通过校验")
	}
	for _, invalid := range []string{"", "thumbnail2", "Thumbnail", "video"} {
		if ValidDerivativeType(invalid) {
			t.Errorf("非法衍生文件类型通过校验: %q", invalid)
		}
	}
}

func TestNormalizePurpose(t *testing.T) {
	if got, err := NormalizePurpose(""); err != nil || got != PurposePost {
		t.Fatalf("空用途应回退 post，得到 %q, %v", got, err)
	}
	if got, err := NormalizePurpose(" avatar "); err != nil || got != PurposeAvatar {
		t.Fatalf("头像用途解析错误：%q, %v", got, err)
	}
	if _, err := NormalizePurpose("other"); !apperr.IsKind(err, apperr.KindInvalid) {
		t.Fatalf("非法用途应返回 invalid 领域错误，得到 %v", err)
	}
}

func TestDecodableImageType(t *testing.T) {
	for _, mediaType := range []string{"image/jpeg", "image/png", "image/gif"} {
		if !DecodableImageType(mediaType) {
			t.Errorf("%q 应可解码", mediaType)
		}
	}
	for _, mediaType := range []string{"image/webp", "image/bmp", "text/plain", ""} {
		if DecodableImageType(mediaType) {
			t.Errorf("%q 不应可解码", mediaType)
		}
	}
}

func TestExceedsStorageQuota(t *testing.T) {
	cases := []struct {
		name             string
		used, incoming   int64
		quota            int64
		wantExceedsQuota bool
	}{
		{name: "远低于配额", used: 100, incoming: 50, quota: 1000},
		{name: "恰好等于配额", used: 950, incoming: 50, quota: 1000},
		{name: "超过配额", used: 960, incoming: 50, quota: 1000, wantExceedsQuota: true},
		{name: "已用空间占满", used: 1000, incoming: 0, quota: 1000},
		{name: "已用空间超出", used: 1200, incoming: 0, quota: 1000, wantExceedsQuota: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExceedsStorageQuota(tc.used, tc.incoming, tc.quota); got != tc.wantExceedsQuota {
				t.Fatalf("ExceedsStorageQuota(%d,%d,%d) = %v，期望 %v",
					tc.used, tc.incoming, tc.quota, got, tc.wantExceedsQuota)
			}
		})
	}
}

func TestCleanupTimeComparison(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	retention := CleanupRetention(0)
	if retention != DefaultTempRetention {
		t.Fatalf("空设置应回退默认保留期，得到 %s", retention)
	}
	custom := CleanupRetention(48 * time.Hour)
	if custom != 48*time.Hour {
		t.Fatalf("应使用设置中的保留期，得到 %s", custom)
	}
	if got := CleanupCutoff(now, retention); !got.Equal(now.Add(-7 * 24 * time.Hour)) {
		t.Fatalf("截止时间 = %s，期望 7 天前", got)
	}
}

func TestTruncateRunes(t *testing.T) {
	if got := TruncateRunes("你好世界", 2); got != "你好" {
		t.Fatalf("截断结果 = %q", got)
	}
	if got := TruncateRunes("abc", 5); got != "abc" {
		t.Fatalf("短文本不应被修改，得到 %q", got)
	}
	if got := TruncateRunes("abc", 0); got != "" {
		t.Fatalf("零长度截断应返回空串，得到 %q", got)
	}
	if got := TruncateRunes("abc", -3); got != "" {
		t.Fatalf("负数长度截断应返回空串，得到 %q", got)
	}
}
