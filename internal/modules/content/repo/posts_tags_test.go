package repo

import (
	"strings"
	"testing"
)

// TestSqlInsertPostIncludesAuthorIsAI 锁定 content 写路径必须写入 author_is_ai 快照列，
// 否则时间线「人类 / AI」过滤（按冗余列）会与账号实际类型不一致。
func TestSqlInsertPostIncludesAuthorIsAI(t *testing.T) {
	for _, want := range []string{"author_is_ai", "VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)"} {
		if !strings.Contains(sqlInsertPost, want) {
			t.Errorf("INSERT 缺少片段 %q:\n%s", want, sqlInsertPost)
		}
	}
	if strings.Contains(sqlInsertPost, "DELETE") {
		t.Fatalf("写入语句不得包含物理删除:\n%s", sqlInsertPost)
	}
}

// TestListTagPrefixesSQL 锁定标签补全前缀查询形态：走 tags_name_prefix 索引的 LIKE 前缀，
// 显式 ESCAPE 保证 %/_/\ 按字面量匹配，且不做多表聚合。
func TestListTagPrefixesSQL(t *testing.T) {
	for _, want := range []string{
		"FROM tags",
		`name LIKE $1 || '%' ESCAPE '\'`,
		"ORDER BY name",
		"LIMIT $2",
	} {
		if !strings.Contains(sqlListTagPrefixes, want) {
			t.Errorf("前缀查询缺少片段 %q:\n%s", want, sqlListTagPrefixes)
		}
	}
	if strings.Contains(sqlListTagPrefixes, "JOIN") {
		t.Fatalf("前缀查询应是单表简单查询（P1-1）:\n%s", sqlListTagPrefixes)
	}
	if strings.Contains(sqlListTagPrefixes, "DELETE") {
		t.Fatalf("查询不得包含物理删除:\n%s", sqlListTagPrefixes)
	}
}

// TestPostCountsByTagIDsSQL 锁定标签计数查询形态：只对一批 tag_id 聚合、保留可见性过滤、
// 不再 COUNT(DISTINCT)（post_tags 主键保证同一标签内 post_id 唯一），也不再 JOIN tags。
func TestPostCountsByTagIDsSQL(t *testing.T) {
	for _, want := range []string{
		"pt.tag_id = ANY($1)",
		"pt.is_active",
		"p.status = 'published'",
		"p.visibility = 'public'",
		"p.visibility = 'followers'",
		"NOT $4::boolean",
		"GROUP BY pt.tag_id",
	} {
		if !strings.Contains(sqlPostCountsByTagIDs, want) {
			t.Errorf("计数查询缺少片段 %q:\n%s", want, sqlPostCountsByTagIDs)
		}
	}
	if strings.Contains(sqlPostCountsByTagIDs, "COUNT(DISTINCT") {
		t.Fatalf("候选标签内 post_id 唯一，不应再 COUNT(DISTINCT):\n%s", sqlPostCountsByTagIDs)
	}
	if strings.Contains(sqlPostCountsByTagIDs, "JOIN tags") {
		t.Fatalf("计数查询不应再 JOIN tags:\n%s", sqlPostCountsByTagIDs)
	}
	if strings.Contains(sqlPostCountsByTagIDs, "DELETE") {
		t.Fatalf("查询不得包含物理删除:\n%s", sqlPostCountsByTagIDs)
	}
}

// TestUniqueTagRecords 校验标签记录按 name 去重且保序（避免单条 upsert 语句内重复命中唯一键）。
func TestUniqueTagRecords(t *testing.T) {
	got := uniqueTagRecords([]TagRecord{
		{ID: 1, Name: "go", DisplayName: "Go"},
		{ID: 2, Name: "rust", DisplayName: "Rust"},
		{ID: 3, Name: "go", DisplayName: "GO"},
		{ID: 4, Name: "", DisplayName: "空"},
	})
	if len(got) != 2 {
		t.Fatalf("应去重为 2 条，实际 %d: %#v", len(got), got)
	}
	if got[0].ID != 1 || got[0].DisplayName != "Go" || got[1].ID != 2 {
		t.Fatalf("去重应保留首次出现的记录: %#v", got)
	}
	if empty := uniqueTagRecords(nil); empty == nil || len(empty) != 0 {
		t.Fatalf("空输入应返回空切片，实际 %#v", empty)
	}
}
