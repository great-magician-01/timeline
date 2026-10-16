package richtext

import "strings"

var textEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
)

var attributeEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
	"'", "&#39;",
)

// escapeText 转义文本节点中的 HTML 特殊字符。
func escapeText(value string) string { return textEscaper.Replace(value) }

// escapeAttribute 转义属性值，包含引号。
func escapeAttribute(value string) string { return attributeEscaper.Replace(value) }
