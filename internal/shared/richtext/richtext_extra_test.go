package richtext

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// TestSafeLinkURLMatrix 校验链接白名单：只允许 http/https/mailto 与站内相对路径；
// 协议相对地址、反斜杠相对路径、控制字符与脚本协议一律拒绝。
func TestSafeLinkURLMatrix(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"https 绝对地址", "https://example.com/a?b=1#c", "https://example.com/a?b=1#c"},
		{"http 绝对地址", "http://example.com", "http://example.com"},
		{"大小写协议", "HTTPS://EXAMPLE.COM/Path", "HTTPS://EXAMPLE.COM/Path"},
		{"mailto", "mailto:user@example.com", "mailto:user@example.com"},
		{"站内相对路径", "/posts/123", "/posts/123"},
		{"首尾空白", "  https://example.com  ", "https://example.com"},
		{"空字符串", "", ""},
		{"协议相对地址", "//evil.example.com/x", ""},
		{"反斜杠相对路径", `/\evil.example.com`, ""},
		{"相对路径含反斜杠", `/a\b`, ""},
		{"javascript 协议", "javascript:alert(1)", ""},
		{"data 协议", "data:text/html,<script>alert(1)</script>", ""},
		{"vbscript 协议", "vbscript:msgbox(1)", ""},
		{"ftp 协议", "ftp://example.com/file", ""},
		{"绝对地址含换行", "https://example.com/\nnext", ""},
		{"绝对地址含制表符", "https://example.com/\tpath", ""},
		{"相对路径含换行", "/a\nb", ""},
		{"相对路径含 DEL", "/a\x7fb", ""},
		{"缺少协议", "example.com/path", ""},
	}
	for _, tc := range cases {
		if got := SafeLinkURL(tc.raw); got != tc.want {
			t.Errorf("%s: SafeLinkURL(%q) = %q，期望 %q", tc.name, tc.raw, got, tc.want)
		}
	}
}

// TestSafeAttributesRebuild 校验属性白名单重建：事件/样式/id 一律丢弃，
// 外链补 rel/target，代码语言 class 与有序列表 start 只在取值合法时保留。
func TestSafeAttributesRebuild(t *testing.T) {
	linkNode := func(href string) *html.Node {
		return &html.Node{
			Type: html.ElementNode,
			Data: "a",
			Attr: []html.Attribute{
				{Key: "href", Val: href},
				{Key: "title", Val: "提示"},
				{Key: "onclick", Val: "alert(1)"},
				{Key: "style", Val: "color:red"},
				{Key: "id", Val: "x"},
			},
		}
	}

	external := safeAttributes("a", linkNode("https://example.com"))
	if !strings.Contains(external, `href="https://example.com"`) ||
		!strings.Contains(external, `rel="nofollow noopener noreferrer"`) ||
		!strings.Contains(external, `target="_blank"`) ||
		!strings.Contains(external, `title="提示"`) {
		t.Fatalf("外链属性重建异常: %s", external)
	}
	for _, forbidden := range []string{"onclick", "style", "id="} {
		if strings.Contains(external, forbidden) {
			t.Errorf("外链属性不应包含 %q: %s", forbidden, external)
		}
	}

	internal := safeAttributes("a", linkNode("/posts/1"))
	if strings.Contains(internal, "rel=") || strings.Contains(internal, "target=") {
		t.Fatalf("站内链接不应补 rel/target: %s", internal)
	}
	if !strings.Contains(internal, `href="/posts/1"`) {
		t.Fatalf("站内链接 href 丢失: %s", internal)
	}

	unsafe := safeAttributes("a", linkNode("javascript:alert(1)"))
	if strings.Contains(unsafe, "href=") {
		t.Fatalf("危险协议不应输出 href: %s", unsafe)
	}

	codeNode := func(class string) *html.Node {
		return &html.Node{
			Type: html.ElementNode,
			Data: "code",
			Attr: []html.Attribute{{Key: "class", Val: class}},
		}
	}
	if got := safeAttributes("code", codeNode("language-go")); got != ` class="language-go"` {
		t.Fatalf("合法语言 class 应保留: %q", got)
	}
	if got := safeAttributes("code", codeNode("evil")); got != "" {
		t.Fatalf("非法 class 应丢弃: %q", got)
	}

	olNode := func(start string) *html.Node {
		return &html.Node{
			Type: html.ElementNode,
			Data: "ol",
			Attr: []html.Attribute{{Key: "start", Val: start}},
		}
	}
	if got := safeAttributes("ol", olNode("3")); got != ` start="3"` {
		t.Fatalf("合法 start 应保留: %q", got)
	}
	for _, invalid := range []string{"abc", "", "1234567", "-1"} {
		if got := safeAttributes("ol", olNode(invalid)); got != "" {
			t.Errorf("非法 start %q 应丢弃，实际 %q", invalid, got)
		}
	}
}

// TestIsLanguageClass 校验代码块语言标记：language- 前缀、1-20 位字母数字与 +-_。
func TestIsLanguageClass(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"language-go", true},
		{"language-c++", true},
		{"language-some_lang-1", true},
		{"language-", false},
		{"language", false},
		{"golang", false},
		{"language-go lang", false},
		{"language-" + strings.Repeat("a", 21), false},
		{"language-go;x", false},
	}
	for _, tc := range cases {
		if got := isLanguageClass(tc.value); got != tc.want {
			t.Errorf("isLanguageClass(%q) = %v，期望 %v", tc.value, got, tc.want)
		}
	}
}

// TestIsDigits 校验有序列表 start 取值：1-6 位数字。
func TestIsDigits(t *testing.T) {
	cases := map[string]bool{
		"":        false,
		"1":       true,
		"123456":  true,
		"1234567": false,
		"12a":     false,
		"-1":      false,
		" 1":      false,
	}
	for value, want := range cases {
		if got := isDigits(value); got != want {
			t.Errorf("isDigits(%q) = %v，期望 %v", value, got, want)
		}
	}
}

// TestHasControlChars 校验控制字符判定：C0 控制符与 DEL 视为非法，普通文本/中文合法。
func TestHasControlChars(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"", false},
		{"abc", false},
		{"中文内容", false},
		{"a\tb", true},
		{"a\nb", true},
		{"a\rb", true},
		{"a\x00b", true},
		{"a\x7fb", true},
	}
	for _, tc := range cases {
		if got := hasControlChars(tc.value); got != tc.want {
			t.Errorf("hasControlChars(%q) = %v，期望 %v", tc.value, got, tc.want)
		}
	}
}

// TestSanitizeHTMLLimitsEmbeddedImages 校验内嵌图片数量上限与外链图片丢弃：
// 只保留本站附件图片，且最多 50 张，避免渲染放大类攻击。
func TestSanitizeHTMLLimitsEmbeddedImages(t *testing.T) {
	var builder strings.Builder
	for i := 0; i < maxEmbeddedImages+5; i++ {
		builder.WriteString(`<img src="/api/v1/attachments/1/content" alt="图">`)
	}
	safe := SanitizeHTML(builder.String())
	if got := strings.Count(safe, "<img "); got != maxEmbeddedImages {
		t.Fatalf("内嵌图片数量 = %d，期望上限 %d", got, maxEmbeddedImages)
	}
	if !strings.Contains(safe, `loading="lazy"`) {
		t.Fatalf("图片必须带 loading=lazy: %s", safe)
	}

	external := SanitizeHTML(`<img src="https://evil.example.com/track.png"><img src="/api/v1/attachments/2/content">`)
	if strings.Contains(external, "evil.example.com") {
		t.Fatalf("外链图片必须丢弃: %s", external)
	}
	if !strings.Contains(external, "/api/v1/attachments/2/content") {
		t.Fatalf("本站附件图片应保留: %s", external)
	}

	// 衍生图地址只允许 thumbnail/video_cover 两种。
	for _, path := range []string{
		"/api/v1/attachments/3/derivatives/thumbnail",
		"/api/v1/attachments/3/derivatives/video_cover",
	} {
		safe := SanitizeHTML(`<img src="` + path + `">`)
		if !strings.Contains(safe, path) {
			t.Errorf("合法衍生图地址应保留: %s", path)
		}
	}
	if safe := SanitizeHTML(`<img src="/api/v1/attachments/3/derivatives/original">`); strings.Contains(safe, "original") {
		t.Fatalf("非法衍生图类型应丢弃: %s", safe)
	}
}
