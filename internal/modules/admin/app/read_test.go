package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
)

type pageItem struct {
	id int64
}

func TestPageOfTrimsAndReportsHasMore(t *testing.T) {
	items := []pageItem{{1}, {2}, {3}}
	page := pageOf(items, 2, func(item pageItem) int64 { return item.id })
	if len(page.Items) != 2 || page.LastID != 2 || !page.HasMore {
		t.Fatalf("pageOf = %+v", page)
	}
}

func TestPageOfExactLastPage(t *testing.T) {
	items := []pageItem{{1}, {2}}
	page := pageOf(items, 2, func(item pageItem) int64 { return item.id })
	if len(page.Items) != 2 || page.LastID != 2 || page.HasMore {
		t.Fatalf("pageOf = %+v", page)
	}
}

func TestPageOfEmpty(t *testing.T) {
	page := pageOf([]pageItem{}, 20, func(item pageItem) int64 { return item.id })
	if len(page.Items) != 0 || page.LastID != 0 || page.HasMore {
		t.Fatalf("pageOf = %+v", page)
	}
}

func TestNormalizeKeyword(t *testing.T) {
	keyword, err := normalizeKeyword("  Alice  ")
	if err != nil || keyword != "alice" {
		t.Fatalf("keyword=%q err=%v", keyword, err)
	}
	empty, err := normalizeKeyword("   ")
	if err != nil || empty != "" {
		t.Fatalf("empty=%q err=%v", empty, err)
	}
	if _, err := normalizeKeyword(strings.Repeat("搜", 101)); err == nil {
		t.Fatal("超长关键词应返回错误")
	} else {
		var appErr *apperr.Error
		if !errors.As(err, &appErr) || appErr.Kind != apperr.KindInvalid {
			t.Fatalf("超长关键词应返回 invalid 领域错误: %v", err)
		}
	}
}

// TestNormalizeKeywordEscapesLikePattern 守护关键词按字面量搜索：SQL LIKE 的通配符
// %/_ 与转义符 \ 必须先转义，再由 repo 以 ESCAPE '\' 解释。
func TestNormalizeKeywordEscapesLikePattern(t *testing.T) {
	keyword, err := normalizeKeyword(`100%_done\`)
	if err != nil {
		t.Fatalf("关键字转义不应报错: %v", err)
	}
	if want := `100\%\_done\\`; keyword != want {
		t.Fatalf("LIKE 通配符应被转义: 得到 %q，期望 %q", keyword, want)
	}
}
