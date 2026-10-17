// Package richtext 负责把人类富文本与 AI Markdown 统一转换为安全 HTML，并提取纯文本、
// 话题标签与提及。安全边界只有一条：任何最终 HTML 都必须经过 SanitizeHTML。
// 渲染路径（富文本直接清洗、Markdown 先渲染再清洗）都收敛到同一清洗函数，
// 因此新增格式不会绕过白名单（docs/REQUIREMENTS.md 6.3、docs/WORKFLOWS.md）。
package richtext

import (
	"net/url"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// 内容格式取值，与 posts/comments 的 content_format 列约束一致。
const (
	FormatRichText = "richtext"
	FormatMarkdown = "markdown"
)

// ValidFormat 判断内容格式是否受支持。
func ValidFormat(format string) bool {
	return format == FormatRichText || format == FormatMarkdown
}

// Render 把编辑器原始内容渲染为安全 HTML 与纯文本。Markdown 路径先渲染再清洗，
// 富文本路径直接清洗，两者最终都经过同一白名单。
func Render(format, source string) (safeHTML, plain string) {
	switch format {
	case FormatMarkdown:
		safeHTML = SanitizeHTML(renderMarkdown(source))
	default:
		safeHTML = SanitizeHTML(source)
	}
	return safeHTML, PlainText(safeHTML)
}

// 允许保留的元素。其余元素一律“解包保留文本”（dangerousElements 例外：整个丢弃）。
var allowedElements = map[string]bool{
	"p": true, "br": true, "hr": true,
	"strong": true, "b": true, "em": true, "i": true, "u": true,
	"del": true, "s": true, "ins": true, "mark": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"ul": true, "ol": true, "li": true,
	"blockquote": true, "pre": true, "code": true,
	"a": true, "img": true, "span": true,
}

// 这些元素连内容一起丢弃（脚本、样式、外链嵌入、表单等）。
var dangerousElements = map[string]bool{
	"script": true, "style": true, "iframe": true, "object": true, "embed": true,
	"applet": true, "svg": true, "math": true, "template": true, "noscript": true,
	"form": true, "input": true, "button": true, "select": true, "option": true,
	"textarea": true, "title": true, "base": true, "meta": true, "link": true,
	"head": true, "frame": true, "frameset": true, "audio": true, "video": true,
	"source": true, "track": true, "canvas": true,
}

// 语义等价的元素统一为一种输出形式。
var elementAliases = map[string]string{
	"b": "strong",
	"i": "em",
	"s": "del",
}

// 需要保留段落级换行的元素，供纯文本提取使用。
var blockElements = map[string]bool{
	"p": true, "div": true, "br": true, "hr": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"ul": true, "ol": true, "li": true, "blockquote": true, "pre": true,
	"tr": true, "table": true, "section": true, "article": true,
}

// 附件地址白名单：内嵌图片只允许指向本站附件接口，禁止外链图片与追踪像素。
var attachmentContentPath = regexp.MustCompile(`^/api/v1/attachments/[0-9]{1,19}/(content|derivatives/(thumbnail|video_cover))$`)

const maxEmbeddedImages = 50

// SanitizeHTML 用白名单清洗 HTML：未知元素解包保留文本，危险元素整体丢弃，
// 所有属性重新构造，事件属性与样式一律不保留。
func SanitizeHTML(raw string) string {
	context := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(raw), context)
	if err != nil {
		// 解析失败时退化为纯文本转义，绝不回退到原始输入。
		return escapeText(raw)
	}

	var out strings.Builder
	state := &sanitizeState{images: 0}
	for _, node := range nodes {
		writeSanitized(&out, node, state)
	}
	return strings.TrimSpace(out.String())
}

// sanitizeState 携带递归过程中的上下文：内嵌图片数量，以及是否处于不参与
// 话题/提及链接化的节点内（代码块、已有的链接内部）。
type sanitizeState struct {
	images  int
	noLinks bool
}

func writeSanitized(out *strings.Builder, node *html.Node, state *sanitizeState) {
	switch node.Type {
	case html.TextNode:
		out.WriteString(renderText(node.Data, state.noLinks))
		return
	case html.CommentNode, html.DoctypeNode, html.DocumentNode:
		return
	case html.ElementNode:
		name := strings.ToLower(node.Data)
		if dangerousElements[name] {
			return
		}
		if !allowedElements[name] {
			// 未在白名单中的元素（如 div、table、font）：解包，保留子内容。
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				writeSanitized(out, child, state)
			}
			return
		}
		if name == "img" {
			writeImage(out, node, state)
			return
		}
		// 统一语义标签写法，避免同一格式出现两种输出。
		if alias, ok := elementAliases[name]; ok {
			name = alias
		}
		attrs := safeAttributes(name, node)
		out.WriteString("<" + name + attrs + ">")
		switch name {
		case "br", "hr":
			return
		}
		// 代码块与既有链接内部不做话题/提及链接化：前者保持原样，后者避免嵌套 <a>。
		inner := state
		if name == "pre" || name == "code" || name == "a" {
			inner = &sanitizeState{images: state.images, noLinks: true}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			writeSanitized(out, child, inner)
		}
		state.images = inner.images
		out.WriteString("</" + name + ">")
	}
}

// writeImage 只保留指向本站附件接口的图片，并强制带上尺寸约束用的属性。
func writeImage(out *strings.Builder, node *html.Node, state *sanitizeState) {
	if state.images >= maxEmbeddedImages {
		return
	}
	var src, alt string
	for _, attr := range node.Attr {
		switch strings.ToLower(attr.Key) {
		case "src":
			src = strings.TrimSpace(attr.Val)
		case "alt":
			alt = attr.Val
		}
	}
	if !attachmentContentPath.MatchString(src) {
		// 外链图片直接丢弃，避免追踪像素与任意外链资源。
		return
	}
	state.images++
	out.WriteString(`<img src="` + escapeAttribute(src) + `"` + ` alt="` + escapeAttribute(alt) + `" loading="lazy">`)
}

func safeAttributes(name string, node *html.Node) string {
	var out strings.Builder
	appendAttr := func(key, value string) {
		out.WriteString(" " + key + `="` + escapeAttribute(value) + `"`)
	}
	for _, attr := range node.Attr {
		key := strings.ToLower(attr.Key)
		if strings.HasPrefix(key, "on") || key == "style" || key == "srcdoc" || key == "id" {
			continue
		}
		switch name {
		case "a":
			switch key {
			case "href":
				if safe := SafeLinkURL(attr.Val); safe != "" {
					appendAttr("href", safe)
					if isExternalURL(safe) {
						appendAttr("rel", "nofollow noopener noreferrer")
						appendAttr("target", "_blank")
					}
				}
			case "title":
				appendAttr("title", attr.Val)
			}
		case "code", "pre":
			// 代码块语言标记，例如 class="language-go"
			if key == "class" && isLanguageClass(attr.Val) {
				appendAttr("class", attr.Val)
			}
		case "ol":
			if key == "start" && isDigits(attr.Val) {
				appendAttr("start", attr.Val)
			}
		}
	}
	return out.String()
}

func isLanguageClass(value string) bool {
	if !strings.HasPrefix(value, "language-") {
		return false
	}
	lang := strings.TrimPrefix(value, "language-")
	if lang == "" || len(lang) > 20 {
		return false
	}
	for _, r := range lang {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '+' || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

func isDigits(value string) bool {
	if value == "" || len(value) > 6 {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// SafeLinkURL 校验链接地址：只允许 http/https/mailto 与站内相对路径。
func SafeLinkURL(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "//") {
		return ""
	}
	if strings.HasPrefix(value, "/") {
		// 站内相对路径：禁止控制字符，避免响应拆分与怪异解析；
		// 反斜杠在浏览器的 URL 解析中等价于 "/"，`/\evil.com` 会被解析成 `//evil.com`
		// 跳转到外部站点，因此相对路径里一律不允许出现。
		if hasControlChars(value) || strings.Contains(value, `\`) {
			return ""
		}
		return value
	}
	lower := strings.ToLower(value)
	for _, scheme := range []string{"http://", "https://", "mailto:"} {
		if strings.HasPrefix(lower, scheme) {
			if hasControlChars(value) {
				return ""
			}
			return value
		}
	}
	return ""
}

func isExternalURL(value string) bool {
	lower := strings.ToLower(value)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func hasControlChars(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// renderText 输出文本节点：默认同时把 `#话题#` 与 `@用户名` 转换为站内链接，
// 便于统一渲染组件直接展示可点击的话题与提及（docs/REQUIREMENTS.md 9.2、10.5）。
// noLinks 为真时（代码块或既有链接内部）只做转义。
//
// 匹配在**未转义**的原文上进行：否则 `#A&B#` 会先变成 `#A&amp;B#`，链接地址与
// 真实标签名不一致（标签页将查不到内容）。转义只在输出每一段时进行。
func renderText(text string, noLinks bool) string {
	if noLinks {
		return escapeText(text)
	}
	type replacement struct {
		start, end int
		html       string
	}
	var spans []replacement
	for _, loc := range tagLinkPattern.FindAllStringSubmatchIndex(text, -1) {
		display := text[loc[2]:loc[3]]
		if strings.TrimSpace(display) == "" {
			continue
		}
		spans = append(spans, replacement{loc[0], loc[1],
			`<a class="tl-tag" href="/tags/` +
				escapeAttribute(url.PathEscape(strings.ToLower(display))) + `">#` +
				escapeText(display) + `#</a>`})
	}
	for _, loc := range mentionLinkPattern.FindAllStringSubmatchIndex(text, -1) {
		prefix := text[loc[2]:loc[3]]
		username := text[loc[4]:loc[5]]
		spans = append(spans, replacement{loc[0], loc[1],
			escapeText(prefix) + `<a class="tl-mention" href="/users/` +
				escapeAttribute(url.PathEscape(strings.ToLower(username))) + `">@` +
				escapeText(username) + `</a>`})
	}
	if len(spans) == 0 {
		return escapeText(text)
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })

	var out strings.Builder
	position := 0
	for _, span := range spans {
		if span.start < position {
			// 与前一个匹配重叠（例如提及出现在话题内部）：整段按普通文本处理。
			continue
		}
		out.WriteString(escapeText(text[position:span.start]))
		out.WriteString(span.html)
		position = span.end
	}
	out.WriteString(escapeText(text[position:]))
	return out.String()
}

// PlainText 从安全 HTML 提取纯文本，用于搜索投影与标签/提及解析。
func PlainText(safeHTML string) string {
	context := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(safeHTML), context)
	if err != nil {
		return collapseSpace(html.UnescapeString(safeHTML))
	}
	var out strings.Builder
	for _, node := range nodes {
		writePlainText(&out, node)
	}
	return collapseSpace(out.String())
}

func writePlainText(out *strings.Builder, node *html.Node) {
	switch node.Type {
	case html.TextNode:
		out.WriteString(node.Data)
		return
	case html.ElementNode:
		name := strings.ToLower(node.Data)
		if dangerousElements[name] {
			return
		}
		if blockElements[name] {
			out.WriteString("\n")
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			writePlainText(out, child)
		}
		if blockElements[name] {
			out.WriteString("\n")
		}
	}
}

// collapseSpace 压缩空白：保留换行语义，其余连续空白折叠为单个空格。
func collapseSpace(value string) string {
	lines := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

var (
	tagPattern     = regexp.MustCompile(`#([^#\s]{1,50})#`)
	mentionPattern = regexp.MustCompile(`@([A-Za-z][A-Za-z0-9_]{2,31})`)

	// 链接化用的模式：标签必须完整成对；提及要求前面不是字母数字下划线或 @，
	// 避免把邮箱域名片段误识别为提及。
	tagLinkPattern     = regexp.MustCompile(`#([^#\s<>]{1,50})#`)
	mentionLinkPattern = regexp.MustCompile(`(^|[^A-Za-z0-9_@])@([A-Za-z][A-Za-z0-9_]{2,31})`)
)

// Tag 是一次话题标签提取的结果。Name 为规范化键，DisplayName 保留原始写法。
type Tag struct {
	Name        string
	DisplayName string
}

// ExtractTags 从纯文本提取 `#话题#`；规范化键为小写，用于唯一索引与聚合。
func ExtractTags(plain string) []Tag {
	seen := map[string]bool{}
	var tags []Tag
	for _, match := range tagPattern.FindAllStringSubmatch(plain, -1) {
		display := strings.TrimSpace(match[1])
		if display == "" {
			continue
		}
		name := strings.ToLower(display)
		if seen[name] {
			continue
		}
		seen[name] = true
		tags = append(tags, Tag{Name: name, DisplayName: display})
	}
	return tags
}

// ExtractMentions 从纯文本提取 `@用户名`，返回去重后的用户名（原始大小写）。
func ExtractMentions(plain string) []string {
	seen := map[string]bool{}
	var names []string
	for _, loc := range mentionPattern.FindAllStringSubmatchIndex(plain, -1) {
		start := loc[0]
		if start > 0 {
			prev := plain[start-1]
			if prev == '@' || isWordByte(prev) {
				continue
			}
		}
		name := plain[loc[2]:loc[3]]
		key := strings.ToLower(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		names = append(names, name)
	}
	return names
}

func isWordByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}
