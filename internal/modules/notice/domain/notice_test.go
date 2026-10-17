package domain_test

import (
	"strings"
	"testing"

	"github.com/great-magician-01/timeline/internal/modules/notice/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// TestNormalizeType 覆盖通知类型参数校验。
func TestNormalizeType(t *testing.T) {
	if got, err := domain.NormalizeType(""); err != nil || got != "" {
		t.Fatalf("空 type 应表示不过滤, got %q/%v", got, err)
	}
	if got, err := domain.NormalizeType(" mention "); err != nil || got != model.NoticeTypeMention {
		t.Fatalf("合法 type 应通过并去空白, got %q/%v", got, err)
	}
	if _, err := domain.NormalizeType("bogus"); !apperr.IsKind(err, apperr.KindInvalid) {
		t.Fatalf("非法 type 应为 invalid, got %v", err)
	}
}

// TestNotificationTypesMatchConstants 保证 Types 覆盖 shared/model 的全部通知类型常量。
func TestNotificationTypesMatchConstants(t *testing.T) {
	all := []string{
		model.NoticeTypePostLike, model.NoticeTypeCommentLike, model.NoticeTypeComment,
		model.NoticeTypeReply, model.NoticeTypeRepost, model.NoticeTypeQuote,
		model.NoticeTypeFollow, model.NoticeTypeFollowRequest, model.NoticeTypeFollowApprove,
		model.NoticeTypeMention,
	}
	if len(domain.Types) != len(all) {
		t.Fatalf("Types 数量 = %d, want %d", len(domain.Types), len(all))
	}
	for _, noticeType := range all {
		if !domain.ValidType(noticeType) {
			t.Errorf("Types 缺少 %q", noticeType)
		}
	}
}

// TestNormalizeFilter 覆盖 filter 参数校验。
func TestNormalizeFilter(t *testing.T) {
	for _, raw := range []string{"", "all", " unread "} {
		if _, err := domain.NormalizeFilter(raw); err != nil {
			t.Errorf("NormalizeFilter(%q) 返回错误: %v", raw, err)
		}
	}
	if got, _ := domain.NormalizeFilter("unread"); got != "unread" {
		t.Fatalf("filter = %q, want unread", got)
	}
	if _, err := domain.NormalizeFilter("read"); !apperr.IsKind(err, apperr.KindInvalid) {
		t.Fatalf("非法 filter 应为 invalid, got %v", err)
	}
}

// TestTargetTypeFor 覆盖提及来源与目标类型映射。
func TestTargetTypeFor(t *testing.T) {
	if got := domain.TargetTypeFor(model.NoticeSourceComment); got != model.NoticeTargetComment {
		t.Fatalf("评论来源目标类型 = %q, want comment", got)
	}
	if got := domain.TargetTypeFor(model.NoticeSourcePost); got != model.NoticeTargetPost {
		t.Fatalf("帖子来源目标类型 = %q, want post", got)
	}
}

// TestTruncatePreview 覆盖空文本、短文本与超长截断。
func TestTruncatePreview(t *testing.T) {
	if got := domain.TruncatePreview("   "); got != nil {
		t.Fatalf("空白文本应返回 nil, got %v", got)
	}
	if got := domain.TruncatePreview(" 你好 "); got == nil || *got != "你好" {
		t.Fatalf("短文本应去空白返回, got %v", got)
	}
	long := strings.Repeat("字", domain.PreviewRunes+10)
	got := domain.TruncatePreview(long)
	if got == nil {
		t.Fatal("超长文本不应返回 nil")
	}
	runes := []rune(*got)
	if len(runes) != domain.PreviewRunes+1 || runes[len(runes)-1] != '…' {
		t.Fatalf("截断长度不符: %d", len(runes))
	}
}
