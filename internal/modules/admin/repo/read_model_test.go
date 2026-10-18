package repo

import (
	"strings"
	"testing"
)

// assertSingleTableSQL 守护单表单聚合语句形态：只引用一张表、不含 JOIN/EXISTS/嵌套子查询。
// 用户统计按「一条语句只做一件简单事」拆分，避免旧实现的跨表标量子查询。
func assertSingleTableSQL(t *testing.T, name, sql, fromTable string) {
	t.Helper()
	lower := strings.ToLower(sql)
	if !strings.Contains(lower, " from "+fromTable) {
		t.Fatalf("%s 应从 %s 单表读取: %s", name, fromTable, sql)
	}
	if strings.Count(lower, " from ") != 1 {
		t.Fatalf("%s 只允许引用一张表: %s", name, sql)
	}
	for _, forbidden := range []string{" join ", "exists", "(select"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("%s 不得包含 %q（跨表/嵌套子查询）: %s", name, forbidden, sql)
		}
	}
}

// TestUserProfileStatsQueries 守护公开个人主页统计：只计算前端真正消费的 3 项
// （已发布帖子 / 粉丝 / 关注），每条都是单表单聚合，不再白算评论/点赞/转发 4 项。
func TestUserProfileStatsQueries(t *testing.T) {
	queries := []struct {
		name string
		sql  string
		from string
	}{
		{"已发布帖子", sqlUserProfilePostCount, "posts"},
		{"粉丝", sqlFollowerCount, "follows"},
		{"关注", sqlFollowingCount, "follows"},
	}
	for _, query := range queries {
		assertSingleTableSQL(t, query.name, query.sql, query.from)
	}
	// 帖子口径是个人主页 post_count 契约：只计已发布。
	if !strings.Contains(strings.ToLower(sqlUserProfilePostCount), "status = 'published'") {
		t.Fatalf("个人主页帖子统计应只计「已发布」: %s", sqlUserProfilePostCount)
	}
}

// TestUserStatsQueries 守护管理端用户统计：6 条单表计数 + likes received 的 2 条单表
// SUM(like_count) 聚合（应用内相加），且两套帖子口径保持分离。
func TestUserStatsQueries(t *testing.T) {
	counts := []struct {
		name  string
		sql   string
		from  string
		scope string
	}{
		{"帖子", sqlUserStatsPostCount, "posts", "status <> 'deleted'"},
		{"评论", sqlUserStatsCommentCount, "comments", "status = 'published'"},
		{"粉丝", sqlFollowerCount, "follows", "is_active"},
		{"关注", sqlFollowingCount, "follows", "is_active"},
		{"点赞送出", sqlUserStatsLikesGivenCount, "likes", "is_active"},
		{"转发", sqlUserStatsRepostCount, "reposts", "is_active"},
	}
	if len(counts) != 6 {
		t.Fatalf("管理端用户统计应为 6 条单表计数，实际 %d 条", len(counts))
	}
	for _, count := range counts {
		assertSingleTableSQL(t, count.name, count.sql, count.from)
		lower := strings.ToLower(count.sql)
		if !strings.Contains(lower, "count(*)") {
			t.Fatalf("%s 应为 COUNT(*) 单表计数: %s", count.name, count.sql)
		}
		if !strings.Contains(lower, strings.ToLower(count.scope)) {
			t.Fatalf("%s 口径应包含 %q: %s", count.name, count.scope, count.sql)
		}
	}

	sums := []struct {
		name string
		sql  string
		from string
	}{
		{"帖子收到的点赞", sqlUserStatsLikesReceivedOnPosts, "posts"},
		{"评论收到的点赞", sqlUserStatsLikesReceivedOnComments, "comments"},
	}
	for _, sum := range sums {
		assertSingleTableSQL(t, sum.name, sum.sql, sum.from)
		lower := strings.ToLower(sum.sql)
		if !strings.Contains(lower, "sum(like_count)") || !strings.Contains(lower, "coalesce") {
			t.Fatalf("%s 应复用冗余计数列 SUM(like_count) 且空集归零: %s", sum.name, sum.sql)
		}
		// 旧 EXISTS 口径只按作者关联、不过滤内容状态；复用冗余计数列时同样不得过滤状态。
		if strings.Contains(lower, "status") {
			t.Fatalf("%s 不应过滤内容状态（与旧实时统计口径一致）: %s", sum.name, sum.sql)
		}
	}
}

// TestKeywordLikeCondition 守护关键词过滤 SQL 形态：必须是 LOWER(x) LIKE '%'||$n||'%'
// ESCAPE '\'（可命中 0017 起的 trigram 表达式索引）；STRPOS 不命中 trigram 索引，不得回归。
func TestKeywordLikeCondition(t *testing.T) {
	condition := keywordLikeCondition("ps.plain_text", "$2")
	lower := strings.ToLower(condition)
	for _, want := range []string{
		"lower(ps.plain_text)",
		"like '%' || $2 || '%'",
		`escape '\'`,
	} {
		if !strings.Contains(lower, want) {
			t.Fatalf("关键词条件应包含 %q: %s", want, condition)
		}
	}
	if strings.Contains(lower, "strpos") || strings.Contains(lower, "coalesce") {
		t.Fatalf("关键词条件不得回退到 STRPOS/COALESCE: %s", condition)
	}
}
