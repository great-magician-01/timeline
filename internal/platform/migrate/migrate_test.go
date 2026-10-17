package migrate

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/great-magician-01/timeline/migrations"
)

func TestSchemaNamesForIsolatedIntegration(t *testing.T) {
	if !validSchemaName("timeline") || !validSchemaName("user_test") || !validSchemaName("_timeline2") {
		t.Fatal("合法 schema 名称未通过校验")
	}
	for _, invalid := range []string{"", "2timeline", "user-test", `user"; DROP SCHEMA timeline;--`} {
		if validSchemaName(invalid) {
			t.Errorf("非法 schema 名称通过校验: %q", invalid)
		}
	}

	sql := `SELECT * FROM "user".users; SELECT * FROM timeline.admin_audit_logs;`
	rewritten := rewriteSchemaNames(sql, "timeline_test", "user_test")
	want := `SELECT * FROM "user_test".users; SELECT * FROM timeline_test.admin_audit_logs;`
	if rewritten != want {
		t.Fatalf("测试 schema 重写错误:\n got=%s\nwant=%s", rewritten, want)
	}
	if got := rewriteSchemaNames(sql, "timeline", "user"); got != sql {
		t.Fatalf("生产 schema 不应改变 SQL:\n got=%s\nwant=%s", got, sql)
	}
}

// 迁移文件里的行注释可能包含分号；分句必须先剔除注释，否则会把语句切断。
func TestSplitStatementsIgnoresCommentSemicolons(t *testing.T) {
	raw := `-- 说明：本文件不得出现含分号的复合语句（这里的 ; 是注释）
CREATE TABLE timeline.demo (
    id BIGINT PRIMARY KEY -- 主键；应用侧生成
);

INSERT INTO timeline.demo (id) VALUES (1);`
	statements := splitStatements(raw)
	if len(statements) != 2 {
		t.Fatalf("分句数量应为 2，实际 %d: %#v", len(statements), statements)
	}
	if strings.Contains(statements[0], "--") || strings.Contains(statements[1], "--") {
		t.Fatalf("分句后不应残留注释: %#v", statements)
	}
	if !strings.HasPrefix(statements[0], "CREATE TABLE") {
		t.Fatalf("首句应为建表语句: %q", statements[0])
	}
}

func TestHasNoTransactionDirective(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want bool
	}{
		{"首行指令", "-- timeline:no-transaction\nCREATE INDEX CONCURRENTLY IF NOT EXISTS idx ON timeline.posts (id);", true},
		{"首行指令无后续语句", "-- timeline:no-transaction", true},
		{"Windows 换行", "-- timeline:no-transaction\r\nCREATE INDEX CONCURRENTLY IF NOT EXISTS idx ON timeline.posts (id);", true},
		{"前后空白", "  -- timeline:no-transaction  \nSELECT 1;", true},
		{"指令不在首行", "\n-- timeline:no-transaction\nSELECT 1;", false},
		{"指令在第二行", "-- 说明\n-- timeline:no-transaction\nSELECT 1;", false},
		{"首行有其他文字", "-- timeline:no-transaction -- 说明\nSELECT 1;", false},
		{"大小写敏感", "-- TIMELINE:NO-TRANSACTION\nSELECT 1;", false},
		{"拼写错误", "-- timeline:no transaction\nSELECT 1;", false},
		{"普通注释", "-- 普通迁移\nSELECT 1;", false},
		{"空内容", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasNoTransactionDirective(tc.sql); got != tc.want {
				t.Fatalf("hasNoTransactionDirective = %v，期望 %v；SQL=%q", got, tc.want, tc.sql)
			}
		})
	}
}

func TestStripBOMBeforeDirective(t *testing.T) {
	raw := "\ufeff-- timeline:no-transaction\nSELECT 1;"
	if hasNoTransactionDirective(raw) {
		t.Fatal("BOM 未去除时首行指令不应被识别（Run 会先 stripBOM）")
	}
	if !hasNoTransactionDirective(stripBOM(raw)) {
		t.Fatal("去除 BOM 后应识别首行指令")
	}
}

// no-transaction 文件在分句与 schema 重写后仍应保留全部语句，且指令注释被剔除。
func TestNoTransactionFileStatements(t *testing.T) {
	raw := "-- timeline:no-transaction\n" +
		"CREATE INDEX CONCURRENTLY IF NOT EXISTS posts_created_idx ON timeline.posts (created_at);\n" +
		"CREATE INDEX CONCURRENTLY IF NOT EXISTS users_created_idx ON timeline.users (created_at);"
	sql := rewriteSchemaNames(stripBOM(raw), "timeline_test", "user_test")
	if !hasNoTransactionDirective(sql) {
		t.Fatal("重写后首行指令应保留")
	}
	statements := splitStatements(sql)
	if len(statements) != 2 {
		t.Fatalf("分句数量应为 2，实际 %d: %#v", len(statements), statements)
	}
	for _, stmt := range statements {
		if strings.Contains(stmt, noTransactionDirective) {
			t.Fatalf("分句后不应残留指令注释: %q", stmt)
		}
		if !strings.HasPrefix(stmt, "CREATE INDEX CONCURRENTLY") {
			t.Fatalf("语句内容错误: %q", stmt)
		}
		if !strings.Contains(stmt, "timeline_test.") {
			t.Fatalf("schema 未重写: %q", stmt)
		}
	}
}

// TestEmbeddedMigrationsParse 确认内嵌迁移文件可被分句器正常解析；同时保证
// no-transaction 指令只用于文件首行（出现在其他位置视为迁移编写错误）。
func TestEmbeddedMigrationsParse(t *testing.T) {
	names, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		t.Fatalf("读取内嵌迁移失败: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("内嵌迁移文件为空")
	}
	for _, name := range names {
		content, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatalf("读取迁移 %s 失败: %v", name, err)
		}
		sql := stripBOM(string(content))
		statements := splitStatements(sql)
		// 0001_baseline 是只有注释的基线标记（两个 schema 由迁移器引导创建），
		// 因此只要求“有可执行内容时必须能分句”。
		if strings.TrimSpace(stripLineComments(sql)) != "" && len(statements) == 0 {
			t.Errorf("迁移 %s 有内容但没有可执行语句", name)
		}
		if hasNoTransactionDirective(sql) {
			continue
		}
		for lineNo, line := range strings.Split(sql, "\n") {
			if strings.TrimSpace(strings.TrimSuffix(line, "\r")) == noTransactionDirective {
				t.Errorf("迁移 %s 第 %d 行的 no-transaction 指令不在首行，不会生效", name, lineNo+1)
			}
		}
	}
}
