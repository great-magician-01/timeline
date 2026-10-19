package migrations

import (
	"strings"
	"testing"
)

// 本文件锁定 SQL 最简原则改造迁移（0017–0020）的关键契约，防止后续误删索引或
// 引入数据库时间默认值/物理删除。断言只读迁移文本，不连接数据库。

// testNoTransactionDirective 报告迁移首行是否声明了 no-transaction。
func testNoTransactionDirective(t *testing.T, name string) bool {
	t.Helper()
	content, err := FS.ReadFile(name)
	if err != nil {
		t.Fatalf("读取迁移 %s 失败: %v", name, err)
	}
	for _, line := range strings.Split(string(content), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		return strings.TrimSpace(line) == "-- timeline:no-transaction"
	}
	return false
}

// assertNoTimeFunctions 断言可执行 SQL 不含数据库时间函数/运算。
func assertNoTimeFunctions(t *testing.T, name, sql string) {
	t.Helper()
	lower := strings.ToLower(sql)
	for _, forbidden := range []string{"now()", "current_timestamp", "current_date", "::date", "interval"} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("%s 含数据库时间函数/运算 %q（业务时间由应用传入）", name, forbidden)
		}
	}
}

// assertNoPhysicalDelete 断言迁移不含物理删除/外键类语句。
func assertNoPhysicalDelete(t *testing.T, name, sql string) {
	t.Helper()
	lower := strings.ToLower(sql)
	for _, forbidden := range []string{"delete from", "drop table", "truncate", "foreign key", "references"} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("%s 含禁止操作 %q（只允许逻辑删除与结构对象变更）", name, forbidden)
		}
	}
}

// Test0017IndexesContract 校验索引批次：no-transaction + 全部并发幂等创建 + 关键索引存在。
func Test0017IndexesContract(t *testing.T) {
	const name = "0017_sql_review_indexes.sql"
	if !testNoTransactionDirective(t, name) {
		t.Fatalf("%s 首行必须是 -- timeline:no-transaction（CREATE INDEX CONCURRENTLY 需要）", name)
	}
	sql := testMigrationSQL(t, name)
	assertNoTimeFunctions(t, name, sql)
	for _, want := range []string{
		"follows_following_cursor",
		"follows_follower_cursor",
		"comments_author_created",
		"comments_post_top",
		"comments_plain_text_trgm",
		"attachments_filename_trgm",
		"attachments_post_available",
		"attachments_unlinked_post",
		"mentions_user_created",
		"background_jobs_running",
		"ai_tasks_running",
		"ai_tasks_last_success",
		"users_human_exists",
		"users_username_trgm",
		"users_nickname_trgm",
		"users_email_trgm",
		"tags_name_prefix",
		"posts_author_quoted_published",
		"ai_action_counters_daily_unique",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("%s 缺少索引 %q", name, want)
		}
	}
	if count := getCreateIndexCount(sql); count < 19 {
		t.Errorf("%s 并发索引创建语句数=%d，少于预期 19 条", name, count)
	}
}

// getCreateIndexCount 统计并发创建索引语句数（含 UNIQUE 变体）。
func getCreateIndexCount(sql string) int {
	return strings.Count(sql, "CREATE INDEX CONCURRENTLY IF NOT EXISTS") +
		strings.Count(sql, "CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS")
}

// Test0018DropIndexesContract 校验删除冗余索引：no-transaction + 全部并发幂等删除 +
// 关键索引已删除，且保留仍被管理端查询使用的 comments_author。
func Test0018DropIndexesContract(t *testing.T) {
	const name = "0018_drop_redundant_indexes.sql"
	if !testNoTransactionDirective(t, name) {
		t.Fatalf("%s 首行必须是 -- timeline:no-transaction（DROP INDEX CONCURRENTLY 需要）", name)
	}
	sql := testMigrationSQL(t, name)
	for _, want := range []string{
		"DROP INDEX CONCURRENTLY IF EXISTS timeline.ai_action_counters_unique",
		"DROP INDEX CONCURRENTLY IF EXISTS timeline.follows_follower_active",
		"DROP INDEX CONCURRENTLY IF EXISTS timeline.follows_followee_active",
		"DROP INDEX CONCURRENTLY IF EXISTS timeline.attachments_unlinked",
		`DROP INDEX CONCURRENTLY IF EXISTS "user".auth_sessions_expiry`,
		"DROP INDEX CONCURRENTLY IF EXISTS timeline.admin_audit_logs_created",
		`DROP INDEX CONCURRENTLY IF EXISTS "user".users_admin_list`,
		"DROP INDEX CONCURRENTLY IF EXISTS timeline.post_versions_post",
		"DROP INDEX CONCURRENTLY IF EXISTS timeline.posts_quoted",
		"DROP INDEX CONCURRENTLY IF EXISTS timeline.ai_tasks_action",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("%s 缺少语句 %q", name, want)
		}
	}
	if strings.Contains(sql, "timeline.comments_author") {
		t.Error("0018 不得删除 comments_author：管理端评论审核列表按作者过滤时不过滤 status，新部分索引不能替代")
	}
}

// Test0019BackfillContract 校验结构变更+回填：作者类型快照、配额计数表与回填来源。
func Test0019BackfillContract(t *testing.T) {
	const name = "0019_author_is_ai_and_attachment_usage.sql"
	if testNoTransactionDirective(t, name) {
		t.Fatalf("%s 是普通事务迁移，不应声明 no-transaction", name)
	}
	sql := testMigrationSQL(t, name)
	assertNoTimeFunctions(t, name, sql)
	assertNoPhysicalDelete(t, name, sql)
	for _, want := range []string{
		"ALTER TABLE timeline.posts ADD COLUMN author_is_ai BOOLEAN NOT NULL DEFAULT FALSE",
		`UPDATE timeline.posts AS p`,
		`FROM "user".users AS u`,
		"SET author_is_ai = u.is_ai",
		"ALTER TABLE timeline.reposts ADD COLUMN author_is_ai BOOLEAN NOT NULL DEFAULT FALSE",
		`UPDATE timeline.reposts AS r`,
		"CREATE TABLE timeline.attachment_usage",
		"INSERT INTO timeline.attachment_usage",
		"SUM(size_bytes)",
		"GROUP BY uploader_id",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("%s 缺少 %q", name, want)
		}
	}
	// 回填必须限定未处理/不一致行（幂等、可重复执行）。
	for _, want := range []string{
		"p.author_is_ai <> u.is_ai",
		"r.author_is_ai <> u.is_ai",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("%s 回填缺少幂等条件 %q", name, want)
		}
	}
}

// Test0020AuthorTypeIndexesContract 校验作者类型快照索引为并发幂等创建。
func Test0020AuthorTypeIndexesContract(t *testing.T) {
	const name = "0020_author_is_ai_indexes.sql"
	if !testNoTransactionDirective(t, name) {
		t.Fatalf("%s 首行必须是 -- timeline:no-transaction", name)
	}
	sql := testMigrationSQL(t, name)
	for _, want := range []string{
		"CREATE INDEX CONCURRENTLY IF NOT EXISTS posts_author_type_timeline",
		"ON timeline.posts (author_is_ai, published_at DESC, id DESC)",
		"CREATE INDEX CONCURRENTLY IF NOT EXISTS reposts_author_type_timeline",
		"ON timeline.reposts (author_is_ai, created_at DESC, id DESC)",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("%s 缺少 %q", name, want)
		}
	}
}

// Test0021DirectMessagesContract 校验私信迁移：普通事务、无时间函数/物理删除，
// 接收策略列、规范化会话表、消息表与关键索引存在，且未读数依赖的部分索引带正确谓词。
func Test0021DirectMessagesContract(t *testing.T) {
	const name = "0021_direct_messages.sql"
	if testNoTransactionDirective(t, name) {
		t.Fatalf("%s 是普通事务迁移，不应声明 no-transaction", name)
	}
	sql := testMigrationSQL(t, name)
	assertNoTimeFunctions(t, name, sql)
	assertNoPhysicalDelete(t, name, sql)
	for _, want := range []string{
		`ALTER TABLE "user".users ADD COLUMN dm_policy TEXT NOT NULL DEFAULT 'all'`,
		"users_dm_policy_check",
		"dm_policy IN ('all', 'following', 'mutual', 'none')",
		"CREATE TABLE timeline.dm_conversations",
		"dm_conversations_pair_check CHECK (user_a_id < user_b_id)",
		"CREATE UNIQUE INDEX dm_conversations_pair_unique",
		"CREATE INDEX dm_conversations_user_a_list",
		"WHERE last_message_id IS NOT NULL AND user_a_hidden_at IS NULL",
		"CREATE INDEX dm_conversations_user_b_list",
		"WHERE last_message_id IS NOT NULL AND user_b_hidden_at IS NULL",
		"CREATE TABLE timeline.dm_messages",
		"dm_messages_status_check CHECK (status IN ('published', 'recalled'))",
		"dm_messages_not_self_check CHECK (sender_id <> recipient_id)",
		"CREATE INDEX dm_messages_conversation ON timeline.dm_messages (conversation_id, id DESC)",
		"CREATE INDEX dm_messages_recipient_unread ON timeline.dm_messages (recipient_id, conversation_id) WHERE read_at IS NULL",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("%s 缺少 %q", name, want)
		}
	}
}
