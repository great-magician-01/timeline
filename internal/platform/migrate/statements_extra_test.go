package migrate

import (
	"context"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/great-magician-01/timeline/migrations"
)

// 本文件补齐分句、注释、schema 名与内嵌迁移文件的边界用例；不连接数据库。

func TestSplitStatementsEdgeCases(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want []string
	}{
		{"空字符串", "", nil},
		{"仅空白", "  \n\t\r\n  ", nil},
		{"仅分号", ";;; ;", nil},
		{"单句无分号", "SELECT 1", []string{"SELECT 1"}},
		{"单句带分号", "SELECT 1;", []string{"SELECT 1"}},
		{"两个语句", "SELECT 1; SELECT 2;", []string{"SELECT 1", "SELECT 2"}},
		{"空语句被跳过", "SELECT 1;\n\n;SELECT 2", []string{"SELECT 1", "SELECT 2"}},
		{"首尾空白被裁剪", "  SELECT 1  ;\n\tSELECT 2\t;", []string{"SELECT 1", "SELECT 2"}},
		{"仅注释", "-- 只有注释\n-- 第二行;含分号\n", nil},
		{"语句内行注释分号", "SELECT 1 -- 注释;里的分号\n;", []string{"SELECT 1"}},
		{"Windows 换行", "CREATE TABLE a (\r\n id BIGINT\r\n);\r\nSELECT 1;", []string{"CREATE TABLE a (\r\n id BIGINT\r\n)", "SELECT 1"}},
		// 已知限制：分句器不识别字符串字面量内的分号（迁移文件不得使用这类写法）。
		{"字符串内分号按已知限制切分", "INSERT INTO t VALUES ('a;b')", []string{"INSERT INTO t VALUES ('a", "b')"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitStatements(tc.sql)
			if len(got) != len(tc.want) {
				t.Fatalf("分句数量 = %d，期望 %d: %#v", len(got), len(tc.want), got)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("语句[%d] = %q，期望 %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestStripLineComments(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want string
	}{
		{"无注释", "SELECT 1", "SELECT 1\n"},
		{"多行无注释", "SELECT 1\nSELECT 2", "SELECT 1\nSELECT 2\n"},
		{"整行注释", "-- 注释\nSELECT 1", "\nSELECT 1\n"},
		{"行尾注释", "SELECT 1 -- 注释\nSELECT 2", "SELECT 1 \nSELECT 2\n"},
		{"空内容仍补换行", "", "\n"},
		{"注释在字符串字面量内按已知限制剔除", "SELECT '--'", "SELECT '\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripLineComments(tc.sql); got != tc.want {
				t.Fatalf("stripLineComments(%q) = %q，期望 %q", tc.sql, got, tc.want)
			}
		})
	}
	// 保留换行以维持语句结构：单行输入去掉注释后仍要多行不合并。
	if got := stripLineComments("a -- x\nb -- y\nc"); got != "a \nb \nc\n" {
		t.Fatalf("多行注释剔除错误: %q", got)
	}
}

func TestStripBOM(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want string
	}{
		{"有 BOM", "\ufeffSELECT 1", "SELECT 1"},
		{"仅 BOM", "\ufeff", ""},
		{"无 BOM", "SELECT 1", "SELECT 1"},
		{"BOM 不在开头", "SELECT \ufeff1", "SELECT \ufeff1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripBOM(tc.sql); got != tc.want {
				t.Fatalf("stripBOM(%q) = %q，期望 %q", tc.sql, got, tc.want)
			}
		})
	}
}

func TestValidSchemaNameTable(t *testing.T) {
	valid := []string{"timeline", "user", "user_test", "_timeline2", "Timeline", "A1_b2"}
	for _, name := range valid {
		if !validSchemaName(name) {
			t.Errorf("合法 schema 名被拒绝: %q", name)
		}
	}
	invalid := []string{
		"", "1timeline", "user-test", "user name", "用户", `"user"`,
		"timeline;drop", "user.test", "timeline\n", "timeline\x00", "a-b",
	}
	for _, name := range invalid {
		if validSchemaName(name) {
			t.Errorf("非法 schema 名通过校验: %q", name)
		}
	}
}

func TestQuoteIdentifier(t *testing.T) {
	cases := map[string]string{
		"user":     `"user"`,
		"timeline": `"timeline"`,
		"A_b1":     `"A_b1"`,
		`a"b`:      `"a"b"`, // 函数只做简单包裹；调用前必须经过 validSchemaName 白名单
	}
	for name, want := range cases {
		if got := quoteIdentifier(name); got != want {
			t.Errorf("quoteIdentifier(%q) = %q，期望 %q", name, got, want)
		}
	}
}

func TestRewriteSchemaNamesEdges(t *testing.T) {
	cases := []struct {
		name           string
		sql            string
		timelineSchema string
		userSchema     string
		want           string
	}{
		{"空 SQL", "", "timeline_test", "user_test", ""},
		{"无 schema 限定", "SELECT 1", "timeline_test", "user_test", "SELECT 1"},
		{"多次出现", `SELECT * FROM "user".a JOIN "user".b ON "user".a.id = "user".b.id`, "timeline_test", "user_test",
			`SELECT * FROM "user_test".a JOIN "user_test".b ON "user_test".a.id = "user_test".b.id`},
		{"timeline 多次出现", "CREATE INDEX i1 ON timeline.posts (id); CREATE INDEX i2 ON timeline.comments (id)", "timeline_test", "user_test",
			"CREATE INDEX i1 ON timeline_test.posts (id); CREATE INDEX i2 ON timeline_test.comments (id)"},
		{"未加引号的 user 不重写", "SELECT * FROM user.users", "timeline_test", "user_test", "SELECT * FROM user.users"},
		{"生产 schema 保持原样", `SELECT * FROM "user".a JOIN timeline.b`, "timeline", "user", `SELECT * FROM "user".a JOIN timeline.b`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rewriteSchemaNames(tc.sql, tc.timelineSchema, tc.userSchema); got != tc.want {
				t.Fatalf("rewriteSchemaNames = %q，期望 %q", got, tc.want)
			}
		})
	}
}

// TestRunWithSchemasRejectsInvalidSchemaNames 覆盖 schema 白名单校验在访问数据库前返回错误。
func TestRunWithSchemasRejectsInvalidSchemaNames(t *testing.T) {
	cases := []struct {
		name     string
		timeline string
		user     string
	}{
		{"timeline 为空", "", "user"},
		{"timeline 以数字开头", "2timeline", "user"},
		{"user 为空", "timeline", ""},
		{"user 含连字符", "timeline", "user-test"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 传入 nil 连接池：校验必须发生在访问数据库之前，否则会触发空指针。
			err := RunWithSchemas(context.Background(), nil, nil, tc.timeline, tc.user)
			if err == nil || !strings.Contains(err.Error(), "测试 schema 名称只能包含字母、数字和下划线") {
				t.Fatalf("非法 schema 名应被拒绝，实际 %v", err)
			}
		})
	}
}

// TestEmbeddedMigrationFilesOrderedAndNamed 覆盖内嵌迁移文件的命名、排序与唯一版本号。
func TestEmbeddedMigrationFilesOrderedAndNamed(t *testing.T) {
	names, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		t.Fatalf("读取内嵌迁移失败: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("内嵌迁移文件为空")
	}
	if !sort.StringsAreSorted(names) {
		t.Fatalf("fs.Glob 结果应按文件名升序: %v", names)
	}
	namePattern := regexp.MustCompile(`^[0-9]{4}_[a-z0-9_]+\.sql$`)
	seenVersions := make(map[string]string, len(names))
	for _, name := range names {
		if !namePattern.MatchString(name) {
			t.Errorf("迁移文件名不符合 NNNN_描述.sql: %q", name)
			continue
		}
		version := name[:4]
		if previous, exists := seenVersions[version]; exists {
			t.Errorf("迁移版本号重复: %s 与 %s", previous, name)
		}
		seenVersions[version] = name
	}
	// 迁移文件只允许追加：排序后版本号必须严格递增。
	previous := ""
	for _, name := range names {
		if len(name) < 4 {
			continue
		}
		version := name[:4]
		if version <= previous {
			t.Errorf("迁移版本未严格递增: %q 在 %q 之后", name, previous)
		}
		previous = version
	}
}

// TestEmbeddedMigrationsParseAfterSchemaRewrite 覆盖每个内嵌迁移在测试 schema 重写后仍可解析。
func TestEmbeddedMigrationsParseAfterSchemaRewrite(t *testing.T) {
	names, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		t.Fatalf("读取内嵌迁移失败: %v", err)
	}
	for _, name := range names {
		content, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatalf("读取迁移 %s 失败: %v", name, err)
		}
		if strings.HasPrefix(string(content), "\ufeff") {
			t.Errorf("迁移 %s 不应含 UTF-8 BOM", name)
		}
		sql := rewriteSchemaNames(stripBOM(string(content)), "timeline_test", "user_test")
		if hasNoTransactionDirective(sql) != hasNoTransactionDirective(stripBOM(string(content))) {
			t.Errorf("迁移 %s 的 no-transaction 指令在重写后丢失", name)
		}
		if strings.Contains(stripLineComments(sql), `"user".`) {
			t.Errorf("迁移 %s 的 user schema 未重写", name)
		}
		if strings.Contains(stripLineComments(sql), "timeline.") {
			t.Errorf("迁移 %s 的 timeline schema 未重写", name)
		}
		// 首行指令之外不得再出现指令文本（否则不会被识别为 no-transaction）。
		lines := strings.Split(sql, "\n")
		for lineNo, line := range lines {
			if lineNo == 0 {
				continue
			}
			if strings.TrimSpace(strings.TrimSuffix(line, "\r")) == noTransactionDirective {
				t.Errorf("迁移 %s 第 %d 行的指令不在首行", name, lineNo+1)
			}
		}
	}
}
