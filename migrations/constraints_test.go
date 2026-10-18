package migrations

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

var forbiddenPersistentOperations = regexp.MustCompile(`(?i)\b(REFERENCES|FOREIGN\s+KEY|DELETE\s+FROM|DROP\s+TABLE|TRUNCATE)\b`)

// 迁移不得引入外键或物理删除；关联查询使用普通索引，删除通过业务状态完成。
func TestMigrationsAvoidForeignKeysAndPhysicalDeletion(t *testing.T) {
	names, err := fs.Glob(FS, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		content, err := FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		var executable strings.Builder
		for line := range strings.SplitSeq(string(content), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "--") {
				continue
			}
			executable.WriteString(line)
			executable.WriteByte('\n')
		}
		if match := forbiddenPersistentOperations.FindString(executable.String()); match != "" {
			t.Errorf("迁移 %s 含禁止操作 %q", name, match)
		}
	}
}

// testMigrationSQL 读取迁移文件并剔除纯注释行，返回可执行 SQL（用于内容契约断言）。
func testMigrationSQL(t *testing.T, name string) string {
	t.Helper()
	content, err := FS.ReadFile(name)
	if err != nil {
		t.Fatalf("读取迁移 %s 失败: %v", name, err)
	}
	var executable strings.Builder
	for line := range strings.SplitSeq(string(content), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		executable.WriteString(line)
		executable.WriteByte('\n')
	}
	return executable.String()
}

// Test0016ReviewFixesContract 校验审查收尾迁移的关键契约：
//  1. 头像回填只更新 avatar_attachment_id、以 avatar_url 精确正则为条件、不更新 updated_at；
//  2. 可执行 SQL 不出现数据库时间函数（业务时间默认值已由 0011 移除，迁移也不得引入）；
//  3. 删除与查询不匹配的 ai_tasks_daily 索引，且不删除表/业务数据。
func Test0016ReviewFixesContract(t *testing.T) {
	sql := testMigrationSQL(t, "0016_review_fixes.sql")
	for _, want := range []string{
		`UPDATE "user".users`,
		"avatar_attachment_id = (regexp_match(avatar_url, '^/api/v1/attachments/([0-9]+)/content$'))[1]::bigint",
		"WHERE avatar_attachment_id IS NULL",
		"avatar_url ~ '^/api/v1/attachments/[0-9]+/content$'",
		"DROP INDEX IF EXISTS timeline.ai_tasks_daily",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("0016 可执行 SQL 缺少 %q", want)
		}
	}
	lower := strings.ToLower(sql)
	for _, forbidden := range []string{"now()", "current_timestamp", "current_date", "::date", "interval"} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("0016 可执行 SQL 含数据库时间函数/运算 %q（业务时间由应用传入）", forbidden)
		}
	}
	if strings.Contains(lower, "updated_at") {
		t.Error("0016 回填不应更新 updated_at（避免推后存量账号更新时间）")
	}
	if strings.Contains(lower, "delete from") || strings.Contains(lower, "drop table") {
		t.Error("0016 不得物理删除业务数据或表")
	}
}
