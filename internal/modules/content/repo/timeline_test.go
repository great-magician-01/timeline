package repo

import (
	"strings"
	"testing"
	"time"
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
	aiAuthor := true
	sql, args := buildPostCandidatesSQL(TimelineQuery{
		ViewerID:      7,
		FollowingIDs:  []int64{1, 2},
		ViewerIsAI:    true,
		AuthorIsAI:    &aiAuthor,
		IncludePosts:  true,
		FollowingOnly: true,
		QuotedOnly:    true,
		AuthorID:      9,
		TagID:         4,
		SearchText:    "keyword",
		Limit:         20,
	})

	for _, want := range []string{
		"p.author_id = $4",
		"p.author_id = ANY($2::bigint[])",
		"p.quoted_post_id IS NOT NULL",
		// 作者类型过滤直接按 author_is_ai 冗余列，不再用账号 ID 集合。
		"p.author_is_ai = $5",
		// 标签已解析为 tag_id，只做 post_tags 主键探测，不再 JOIN tags。
		"EXISTS (SELECT 1 FROM post_tags pt",
		"pt.tag_id = $6",
		"pt.is_active",
		"EXISTS (SELECT 1 FROM post_search",
		"LOWER(ps.plain_text) LIKE '%' || LOWER($7) || '%' ESCAPE '\\'",
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
	// 标签条件不应再逐行 JOIN tags 解析名称（P0-4）。
	if strings.Contains(sql, "JOIN tags") {
		t.Fatalf("标签条件不应再 JOIN tags:\n%s", sql)
	}
	if strings.Contains(sql, "WHERE (p.published_at") {
		t.Fatalf("首页查询不应包含游标条件:\n%s", sql)
	}
	if len(args) != 8 {
		t.Fatalf("参数数量应为 8，实际 %d: %#v", len(args), args)
	}
}

// TestBuildPostCandidatesSQLEscapesSearchLike 校验搜索 LIKE 显式声明 ESCAPE，
// 配合 app 侧 shared/search.EscapeLikePattern 让 %/_/\ 按字面量匹配。
func TestBuildPostCandidatesSQLEscapesSearchLike(t *testing.T) {
	sql, _ := buildPostCandidatesSQL(TimelineQuery{
		IncludePosts: true,
		SearchText:   "100\\%",
		Limit:        20,
	})
	if !strings.Contains(sql, "ESCAPE '\\'") {
		t.Fatalf("搜索 LIKE 必须显式声明 ESCAPE '\\':\n%s", sql)
	}
	// 未设置 TagID 时不追加标签 EXISTS 条件。
	if strings.Contains(sql, "FROM post_tags") {
		t.Fatalf("TagID 为 0 时不应追加标签条件:\n%s", sql)
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
