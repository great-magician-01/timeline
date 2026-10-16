package migrate

import (
	"strings"
	"testing"
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
