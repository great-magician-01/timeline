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
