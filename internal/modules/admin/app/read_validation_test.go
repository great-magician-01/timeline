package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
)

// Read 是具体 *repo.ReadModel（只读 DB、不可 fake），因此读列表用例只覆盖
// 「不触库的参数校验路径」：校验失败必须在调用 read 之前返回，否则本组测试会 panic。
// 成功读路径与 read 端口失败路径留给需要真实数据库的集成测试。

func TestListUsersRejectsInvalidStatus(t *testing.T) {
	env := newAdminTestEnv(t)
	_, err := env.service.ListUsers(context.Background(), UsersQuery{Status: "deleted", Limit: 20})
	if !apperr.IsKind(err, apperr.KindInvalid) {
		t.Fatalf("期望 invalid，实际: %v", err)
	}
}

func TestListUsersRejectsOverlongKeyword(t *testing.T) {
	env := newAdminTestEnv(t)
	_, err := env.service.ListUsers(context.Background(), UsersQuery{
		Keyword: strings.Repeat("搜", 101), Limit: 20,
	})
	if !apperr.IsKind(err, apperr.KindInvalid) {
		t.Fatalf("期望 invalid，实际: %v", err)
	}
}

func TestListPostsRejectsInvalidFilters(t *testing.T) {
	cases := []struct {
		name  string
		query PostsQuery
	}{
		{"可见性非法", PostsQuery{Visibility: "secret", Limit: 20}},
		{"状态非法", PostsQuery{Status: "archived", Limit: 20}},
		{"关键词超长", PostsQuery{Keyword: strings.Repeat("搜", 101), Limit: 20}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newAdminTestEnv(t)
			if _, err := env.service.ListPosts(context.Background(), tc.query); !apperr.IsKind(err, apperr.KindInvalid) {
				t.Fatalf("期望 invalid，实际: %v", err)
			}
		})
	}
}

func TestListCommentsRejectsOverlongKeyword(t *testing.T) {
	env := newAdminTestEnv(t)
	_, err := env.service.ListComments(context.Background(), CommentsQuery{
		Keyword: strings.Repeat("评", 101), Limit: 20,
	})
	if !apperr.IsKind(err, apperr.KindInvalid) {
		t.Fatalf("期望 invalid，实际: %v", err)
	}
}

func TestListAttachmentsRejectsInvalidFilters(t *testing.T) {
	cases := []struct {
		name  string
		query AttachmentsQuery
	}{
		{"用途非法", AttachmentsQuery{Purpose: "video", Limit: 20}},
		{"状态非法", AttachmentsQuery{Status: "processing", Limit: 20}},
		{"关键词超长", AttachmentsQuery{Keyword: strings.Repeat("附", 101), Limit: 20}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newAdminTestEnv(t)
			if _, err := env.service.ListAttachments(context.Background(), tc.query); !apperr.IsKind(err, apperr.KindInvalid) {
				t.Fatalf("期望 invalid，实际: %v", err)
			}
		})
	}
}

func TestWrapInternalPreservesCause(t *testing.T) {
	sentinel := errors.New("底层查询失败")
	err := wrapInternal(sentinel)
	if !apperr.IsKind(err, apperr.KindInternal) || !errors.Is(err, sentinel) {
		t.Fatalf("期望 internal 且保留底层原因，实际: %v", err)
	}
}

func TestPageOfLimitLargerThanItems(t *testing.T) {
	items := []pageItem{{1}, {2}, {3}}
	page := pageOf(items, 5, func(item pageItem) int64 { return item.id })
	if len(page.Items) != 3 || page.LastID != 3 || page.HasMore {
		t.Fatalf("pageOf = %+v", page)
	}
}

func TestPageOfNilItems(t *testing.T) {
	page := pageOf[pageItem](nil, 20, func(item pageItem) int64 { return item.id })
	if len(page.Items) != 0 || page.LastID != 0 || page.HasMore {
		t.Fatalf("pageOf = %+v", page)
	}
}

func TestNormalizeKeywordBoundaries(t *testing.T) {
	// 恰好 100 个字符（含首尾空白时按去空白后的长度判断）应通过并小写化。
	exact := strings.Repeat("A", 100)
	keyword, err := normalizeKeyword("  " + exact + "  ")
	if err != nil || keyword != strings.Repeat("a", 100) {
		t.Fatalf("100 字符关键词应通过并小写化: keyword 长度=%d err=%v", len(keyword), err)
	}
	// 中文字符按 rune 计数：100 个中文通过，101 个失败。
	if _, err := normalizeKeyword(strings.Repeat("搜", 100)); err != nil {
		t.Fatalf("100 个中文字符应通过: %v", err)
	}
	if _, err := normalizeKeyword(strings.Repeat("搜", 101)); !apperr.IsKind(err, apperr.KindInvalid) {
		t.Fatalf("101 个中文字符期望 invalid，实际: %v", err)
	}
	// 纯空白归一化为空字符串且不报错。
	if keyword, err := normalizeKeyword(" \t \n "); err != nil || keyword != "" {
		t.Fatalf("空白关键词应归一化为空: keyword=%q err=%v", keyword, err)
	}
}
