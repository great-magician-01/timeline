package richtext

import (
	"regexp"
	"strings"
)

// renderMarkdown 把 AI 输出的 Markdown 渲染为 HTML。原始 HTML 一律转义，
// 结果仍会进入 SanitizeHTML 白名单，因此这里只负责结构，不承担安全职责。
func renderMarkdown(source string) string {
	raw := strings.ReplaceAll(source, "\r\n", "\n")
	lines := strings.Split(raw, "\n")

	var out strings.Builder
	var paragraph []string
	flushParagraph := func() {
		if len(paragraph) == 0 {
			return
		}
		out.WriteString("<p>" + renderInline(strings.Join(paragraph, "\n")) + "</p>")
		paragraph = paragraph[:0]
	}

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			flushParagraph()
			fence := trimmed[:3]
			lang := sanitizeLanguage(strings.TrimSpace(trimmed[3:]))
			var code []string
			i++
			for ; i < len(lines); i++ {
				if strings.HasPrefix(strings.TrimSpace(lines[i]), fence) {
					break
				}
				code = append(code, lines[i])
			}
			class := ""
			if lang != "" {
				class = ` class="language-` + lang + `"`
			}
			out.WriteString("<pre><code" + class + ">" + escapeText(strings.Join(code, "\n")) + "</code></pre>")
			continue
		}

		if trimmed == "" {
			flushParagraph()
			continue
		}
		if level, title := headingLevel(trimmed); level > 0 {
			flushParagraph()
			out.WriteString("<h" + itoa(level) + ">" + renderInline(title) + "</h" + itoa(level) + ">")
			continue
		}
		if isHorizontalRule(trimmed) {
			flushParagraph()
			out.WriteString("<hr>")
			continue
		}
		if strings.HasPrefix(trimmed, ">") {
			flushParagraph()
			var quoted []string
			for ; i < len(lines); i++ {
				current := strings.TrimSpace(lines[i])
				if !strings.HasPrefix(current, ">") {
					i--
					break
				}
				quoted = append(quoted, strings.TrimPrefix(strings.TrimPrefix(current, ">"), " "))
			}
			out.WriteString("<blockquote>" + renderMarkdown(strings.Join(quoted, "\n")) + "</blockquote>")
			continue
		}
		if isUnorderedItem(trimmed) {
			flushParagraph()
			items := []string{}
			for ; i < len(lines); i++ {
				current := strings.TrimSpace(lines[i])
				if !isUnorderedItem(current) {
					i--
					break
				}
				items = append(items, "<li>"+renderInline(listItemText(current))+"</li>")
			}
			out.WriteString("<ul>" + strings.Join(items, "") + "</ul>")
			continue
		}
		if isOrderedItem(trimmed) {
			flushParagraph()
			items := []string{}
			for ; i < len(lines); i++ {
				current := strings.TrimSpace(lines[i])
				if !isOrderedItem(current) {
					i--
					break
				}
				items = append(items, "<li>"+renderInline(listItemText(current))+"</li>")
			}
			out.WriteString("<ol>" + strings.Join(items, "") + "</ol>")
			continue
		}
		paragraph = append(paragraph, trimmed)
	}
	flushParagraph()
	return out.String()
}

func headingLevel(line string) (int, string) {
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || level >= len(line) || line[level] != ' ' {
		return 0, ""
	}
	return level, strings.TrimSpace(line[level+1:])
}

func isHorizontalRule(line string) bool {
	if len(line) < 3 {
		return false
	}
	marker := line[0]
	if marker != '-' && marker != '*' && marker != '_' {
		return false
	}
	for i := 0; i < len(line); i++ {
		if line[i] != marker {
			return false
		}
	}
	return true
}

func isUnorderedItem(line string) bool {
	if len(line) < 2 {
		return false
	}
	if line[0] != '-' && line[0] != '*' && line[0] != '+' {
		return false
	}
	return line[1] == ' '
}

func isOrderedItem(line string) bool {
	index := 0
	for index < len(line) && line[index] >= '0' && line[index] <= '9' {
		index++
	}
	if index == 0 || index > 9 || index >= len(line) {
		return false
	}
	if line[index] != '.' && line[index] != ')' {
		return false
	}
	return index+1 < len(line) && line[index+1] == ' '
}

func listItemText(line string) string {
	index := 0
	if isUnorderedItem(line) {
		return strings.TrimSpace(line[2:])
	}
	for index < len(line) && line[index] >= '0' && line[index] <= '9' {
		index++
	}
	if index+1 <= len(line) {
		return strings.TrimSpace(line[index+1:])
	}
	return strings.TrimSpace(line)
}

func sanitizeLanguage(value string) string {
	var out strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '+' || r == '#' || r == '-' || r == '_' {
			out.WriteRune(r)
		}
		if out.Len() >= 20 {
			break
		}
	}
	return out.String()
}

func itoa(value int) string {
	if value < 10 {
		return string(rune('0' + value))
	}
	return string(rune('0'+value/10)) + string(rune('0'+value%10))
}

var (
	codeSpanPattern = regexp.MustCompile("`([^`\n]+)`")
	imagePattern    = regexp.MustCompile(`!\[([^\]\n]*)\]\(([^)\s]+)\)`)
	linkPattern     = regexp.MustCompile(`\[([^\]\n]*)\]\(([^)\s]+)\)`)
	boldPatternA    = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	boldPatternB    = regexp.MustCompile(`__([^_\n]+)__`)
	italicPatternA  = regexp.MustCompile(`\*([^*\n]+)\*`)
	italicPatternB  = regexp.MustCompile(`_([^_\n]+)_`)
	strikePattern   = regexp.MustCompile(`~~([^~\n]+)~~`)
)

// codePlaceholder 生成行内代码占位符；控制字符不会被 Markdown 标记规则命中，
// 也不出现在正常正文里，因此可以安全地先占位、后还原。
func codePlaceholder(index int) string {
	return "\x00" + itoa(index) + "\x00"
}

// renderInline 处理行内标记。输入先整体转义，再叠加受控标签，避免原始 HTML 生效。
func renderInline(text string) string {
	// 1) 先摘出行内代码，避免其中的标记被继续解析
	var codeSpans []string
	text = codeSpanPattern.ReplaceAllStringFunc(text, func(match string) string {
		codeSpans = append(codeSpans, match[1:len(match)-1])
		return codePlaceholder(len(codeSpans) - 1)
	})

	// 2) 转义全部 HTML 特殊字符
	escaped := escapeText(text)

	// 3) 行内标记：图片链接 → 链接 → 粗体 → 斜体 → 删除线
	escaped = imagePattern.ReplaceAllStringFunc(escaped, func(match string) string {
		parts := imagePattern.FindStringSubmatch(match)
		src := SafeLinkURL(parts[2])
		if src == "" || !attachmentContentPath.MatchString(src) {
			return parts[1]
		}
		return `<img src="` + escapeAttribute(src) + `" alt="` + escapeAttribute(parts[1]) + `" loading="lazy">`
	})
	escaped = linkPattern.ReplaceAllStringFunc(escaped, func(match string) string {
		parts := linkPattern.FindStringSubmatch(match)
		href := SafeLinkURL(parts[2])
		if href == "" {
			return parts[1]
		}
		extra := ""
		if isExternalURL(href) {
			extra = ` rel="nofollow noopener noreferrer" target="_blank"`
		}
		return `<a href="` + escapeAttribute(href) + `"` + extra + `>` + parts[1] + `</a>`
	})
	escaped = boldPatternA.ReplaceAllString(escaped, "<strong>$1</strong>")
	escaped = boldPatternB.ReplaceAllString(escaped, "<strong>$1</strong>")
	escaped = strikePattern.ReplaceAllString(escaped, "<del>$1</del>")
	escaped = italicPatternA.ReplaceAllString(escaped, "<em>$1</em>")
	escaped = italicPatternB.ReplaceAllString(escaped, "<em>$1</em>")

	// 4) 还同行内代码
	for i, code := range codeSpans {
		escaped = strings.ReplaceAll(escaped, codePlaceholder(i), "<code>"+escapeText(code)+"</code>")
	}

	// 5) 段内换行保留为 <br>（中文写作习惯）
	return strings.ReplaceAll(escaped, "\n", "<br>")
}
