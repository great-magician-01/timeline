package repo

import (
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/content/domain"
)

// TestBuildPostCandidatesSQLCursorAppendsCondition 防回归：游标条件必须与原 WHERE
// 合并为单个 WHERE，否则 PostgreSQL 会报 syntax error at or near "WHERE"。
func TestBuildPostCandidatesSQLCursorAppendsCondition(t *testing.T) {
	at := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	sql, args := buildPostCandidatesSQL(TimelineQuery{
		ViewerID:     7,
		FollowingIDs: []int64{1, 2},
		ViewerIsAI:   false,
		IncludePosts: true,
		Filter:       domain.FilterAll,
		AfterTime:    at,
		AfterID:      123,
		Limit:        21,
	})

	if got := strings.Count(sql, " WHERE "); got != 1 {
		t.Fatalf("SQL 必须只有一个 WHERE，实际 %d 个:\n%s", got, sql)
	}
	for _, want := range []string{
		"p.status = 'published'",
		"p.visibility = 'public'",
		"AND (p.published_at, p.id) < ($4::timestamptz, $5::bigint)",
		"ORDER BY p.published_at DESC, p.id DESC LIMIT $6",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("SQL 缺少片段 %q:\n%s", want, sql)
		}
	}
	if strings.Contains(sql, "WHERE (p.published_at") {
		t.Fatalf("游标条件不应再拼出第二个 WHERE:\n%s", sql)
	}
	if len(args) != 6 {
		t.Fatalf("参数数量应为 6，实际 %d: %#v", len(args), args)
	}
}

// TestBuildPostCandidatesSQLFilters 覆盖全部过滤分支的参数顺序与条件拼接。
func TestBuildPostCandidatesSQLFilters(t *testing.T) {
	sql, args := buildPostCandidatesSQL(TimelineQuery{
		ViewerID:      7,
		FollowingIDs:  []int64{1, 2},
		ViewerIsAI:    true,
		AuthorIDs:     []int64{5, 6},
		IncludePosts:  true,
		FollowingOnly: true,
		QuotedOnly:    true,
		AuthorID:      9,
		Filter:        domain.FilterAI,
		TagName:       "go",
		SearchText:    "keyword",
		Limit:         20,
	})

	for _, want := range []string{
		"p.author_id = $4",
		"p.author_id = ANY($2::bigint[])",
		"p.quoted_post_id IS NOT NULL",
		"p.author_id = ANY($5::bigint[])",
		"EXISTS (SELECT 1 FROM post_tags",
		"tg.name = $6",
		"EXISTS (SELECT 1 FROM post_search",
		"LOWER(ps.plain_text) LIKE '%' || LOWER($7) || '%'",
		"LIMIT $8",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("SQL 缺少片段 %q:\n%s", want, sql)
		}
	}
	// 搜索必须使用 LIKE 才能命中 0014 的 trigram 表达式索引，STRPOS 不走索引。
	if strings.Contains(sql, "STRPOS") {
		t.Fatalf("搜索条件不应再使用 STRPOS（不能命中 trigram 索引）:\n%s", sql)
	}
	if strings.Contains(sql, "WHERE (p.published_at") {
		t.Fatalf("首页查询不应包含游标条件:\n%s", sql)
	}
	if len(args) != 8 {
		t.Fatalf("参数数量应为 8，实际 %d: %#v", len(args), args)
	}
}

// TestBuildPostCandidatesSQLVisibilityForGuestAndAI 校验可见性表达式包含游客/AI 约束。
func TestBuildPostCandidatesSQLVisibility(t *testing.T) {
	sql, _ := buildPostCandidatesSQL(TimelineQuery{IncludePosts: true, Limit: 20})
	for _, want := range []string{
		"p.visibility = 'public'",
		"p.author_id = $1",
		"NOT $3::boolean",
		"p.author_id = ANY($2::bigint[])",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("可见性条件缺少片段 %q:\n%s", want, sql)
		}
	}
}
