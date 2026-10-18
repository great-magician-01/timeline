// Package search 提供跨模块复用的纯字符串检索辅助（不访问 IO、不依赖数据库）。
package search

import "strings"

// EscapeLikePattern 转义 SQL LIKE 模式中的特殊字符，使参数按字面量匹配：
//   - `\` → `\\`（先转义，避免二次转义）
//   - `%` → `\%`
//   - `_` → `\_`
//
// 调用方必须把转义后的值作为 `%` 拼接参数使用，并在 SQL 中显式声明 `ESCAPE '\'`：
//
//	LOWER(plain_text) LIKE '%' || $1 || '%' ESCAPE '\'
//
// 这样 `LOWER(x) LIKE` 仍能命中 0014 起的 trigram 表达式索引，同时恢复「字面量搜索」
// 语义（未转义时 `%`/`_` 会被当作通配符，导致搜索结果错误）。
func EscapeLikePattern(value string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`,
		`%`, `\%`,
		`_`, `\_`,
	)
	return replacer.Replace(value)
}
