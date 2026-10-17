package migrate

import "strings"

// noTransactionDirective 是迁移文件首行的运行模式指令：该文件的语句不包事务，
// 直接在连接上逐条执行（用于 CREATE INDEX CONCURRENTLY 等）。
const noTransactionDirective = "-- timeline:no-transaction"

// hasNoTransactionDirective 判断迁移文件首行是否为 no-transaction 指令。
// 要求指令独占首行（允许前后空白与 Windows 换行）；其他位置出现相同文本无效。
func hasNoTransactionDirective(sql string) bool {
	firstLine, _, _ := strings.Cut(sql, "\n")
	return strings.TrimSpace(strings.TrimSuffix(firstLine, "\r")) == noTransactionDirective
}

// stripBOM 去除文件头部的 UTF-8 BOM，避免首行指令因不可见字符而失效。
func stripBOM(sql string) string {
	return strings.TrimPrefix(sql, "\ufeff")
}

func validSchemaName(schema string) bool {
	if schema == "" {
		return false
	}
	for i, r := range schema {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

func quoteIdentifier(identifier string) string {
	return `"` + identifier + `"`
}

// rewriteSchemaNames 只重写迁移 SQL 中显式的 schema 限定符，标识符已经过白名单校验。
func rewriteSchemaNames(sql, timelineSchema, userSchema string) string {
	sql = strings.ReplaceAll(sql, `"user".`, quoteIdentifier(userSchema)+".")
	sql = strings.ReplaceAll(sql, "timeline.", timelineSchema+".")
	return sql
}

// splitStatements 按 ";" 分句。行注释先剔除再分句，避免注释文本里的分号把
// 语句切断（历史实现先分句后清注释，注释中出现 ASCII 分号会导致迁移失败）。
// 注意：剔除注释只按 "--" 文本匹配，因此迁移里的字符串字面量不得包含 "--"。
func splitStatements(sql string) []string {
	var stmts []string
	for part := range strings.SplitSeq(stripLineComments(sql), ";") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		stmts = append(stmts, strings.TrimSpace(part))
	}
	return stmts
}

// stripLineComments 删除 "--" 之后的内容，保留换行以维持语句结构。
func stripLineComments(sql string) string {
	var b strings.Builder
	for line := range strings.SplitSeq(sql, "\n") {
		if idx := strings.Index(line, "--"); idx >= 0 {
			line = line[:idx]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}
