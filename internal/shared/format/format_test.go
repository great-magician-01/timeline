package format

import (
	"testing"
	"time"
)

func TestFormatID(t *testing.T) {
	if got := FormatID(9007199254740993); got != "9007199254740993" {
		t.Fatalf("FormatID = %q", got)
	}
	if got := FormatID(0); got != "0" {
		t.Fatalf("FormatID(0) = %q", got)
	}
	if got := FormatID(-1); got != "-1" {
		t.Fatalf("FormatID(-1) = %q", got)
	}
}

func TestFormatTimeUsesBeijingOffset(t *testing.T) {
	// UTC 12:00 必须输出 +08:00 的 20:00，且不依赖进程 time.Local。
	at := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	if got := FormatTime(at); got != "2026-10-17T20:00:00+08:00" {
		t.Fatalf("FormatTime = %q", got)
	}
	if got := FormatTime(time.Time{}); got != "0001-01-01T08:00:00+08:00" {
		t.Fatalf("零值时间语义不应改变: %q", got)
	}
}

func TestFormatTimePtr(t *testing.T) {
	if got := FormatTimePtr(nil); got != nil {
		t.Fatalf("nil 时间应返回 nil，得到 %v", got)
	}
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	got := FormatTimePtr(&at)
	if got == nil || *got != "2026-01-02T11:04:05+08:00" {
		t.Fatalf("FormatTimePtr = %v", got)
	}
}

func TestAttachmentURLsMatchOldContract(t *testing.T) {
	if got := AttachmentContentURL(42); got != "/api/v1/attachments/42/content" {
		t.Fatalf("AttachmentContentURL = %q", got)
	}
	if got := AttachmentDerivativeURL(42, "video_cover"); got != "/api/v1/attachments/42/derivatives/video_cover" {
		t.Fatalf("AttachmentDerivativeURL = %q", got)
	}
	if got := ThumbnailURL(9007199254740993); got != "/api/v1/attachments/9007199254740993/derivatives/thumbnail" {
		t.Fatalf("ThumbnailURL = %q", got)
	}
}
