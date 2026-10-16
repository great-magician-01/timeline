package richtext

import (
	"regexp"
	"strings"
	"testing"
)

func TestSanitizeHTMLDropsScriptsAndEventHandlers(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		mustNot  []string
		mustKeep []string
	}{
		{
			name:     "script 标签整体丢弃",
			input:    `<p>正文</p><script>alert(1)</script>`,
			mustNot:  []string{"script", "alert"},
			mustKeep: []string{"<p>正文</p>"},
		},
		{
			name:     "事件属性被移除",
			input:    `<img src="/api/v1/attachments/12/content" onerror="alert(1)">`,
			mustNot:  []string{"onerror", "alert"},
			mustKeep: []string{`src="/api/v1/attachments/12/content"`},
		},
		{
			name:     "javascript 链接被移除",
			input:    `<a href="javascript:alert(1)">点我</a>`,
			mustNot:  []string{"javascript:"},
			mustKeep: []string{"点我"},
		},
		{
			name:     "iframe 与 svg 被丢弃",
			input:    `<iframe src="https://evil.example"></iframe><svg onload="x()"></svg>安全文本`,
			mustNot:  []string{"iframe", "svg", "onload", "evil.example"},
			mustKeep: []string{"安全文本"},
		},
		{
			name:     "未白名单元素解包保留文本",
			input:    `<div class="x"><table><tr><td>单元格</td></tr></table></div>`,
			mustNot:  []string{"<div", "<table", "class="},
			mustKeep: []string{"单元格"},
		},
		{
			name:     "style 属性被移除",
			input:    `<p style="position:fixed">正文</p>`,
			mustNot:  []string{"style="},
			mustKeep: []string{"<p>正文</p>"},
		},
		{
			name:     "外链图片被丢弃",
			input:    `<img src="https://tracker.example/pixel.gif">`,
			mustNot:  []string{"tracker.example", "<img"},
			mustKeep: nil,
		},
		{
			name:     "站内附件图片保留",
			input:    `<img src="/api/v1/attachments/9007199254740993/content" alt="图">`,
			mustKeep: []string{`/api/v1/attachments/9007199254740993/content`, `alt="图"`},
		},
		{
			name:     "外部链接补充安全属性",
			input:    `<a href="https://example.com/x">链接</a>`,
			mustKeep: []string{`rel="nofollow noopener noreferrer"`, `target="_blank"`},
		},
		{
			name:     "注释与 doctype 被移除",
			input:    `<!-- 注释 --><!DOCTYPE html><p>正文</p>`,
			mustNot:  []string{"注释", "DOCTYPE"},
			mustKeep: []string{"<p>正文</p>"},
		},
		{
			name:     "文本中的标签字符被转义",
			input:    `&lt;script&gt; 与 <b>粗体</b>`,
			mustKeep: []string{"&lt;script&gt;", "<strong>", "</strong>"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeHTML(tc.input)
			for _, bad := range tc.mustNot {
				if strings.Contains(strings.ToLower(got), strings.ToLower(bad)) {
					t.Errorf("输出不应包含 %q，实际: %s", bad, got)
				}
			}
			for _, good := range tc.mustKeep {
				if !strings.Contains(got, good) {
					t.Errorf("输出应包含 %q，实际: %s", good, got)
				}
			}
		})
	}
}

func TestRenderMarkdown(t *testing.T) {
	source := "# 标题\n\n这是**粗体**和*斜体*以及 `代码`。\n\n- 项目一\n- 项目二\n\n> 引用\n\n```go\nfunc main() {}\n```\n\n[链接](https://example.com)"
	got, plain := Render(FormatMarkdown, source)
	for _, want := range []string{"<h1>标题</h1>", "<strong>粗体</strong>", "<em>斜体</em>", "<code>代码</code>", "<ul><li>项目一</li><li>项目二</li></ul>", "<blockquote><p>引用</p></blockquote>", "<pre><code", "func main() {}", "<a href=\"https://example.com\""} {
		if !strings.Contains(got, want) {
			t.Errorf("Markdown 渲染缺少 %q，实际: %s", want, got)
		}
	}
	if !strings.Contains(plain, "标题") || !strings.Contains(plain, "项目二") {
		t.Errorf("纯文本提取异常: %q", plain)
	}
}

func TestRenderMarkdownEscapesRawHTML(t *testing.T) {
	got, _ := Render(FormatMarkdown, "正常文本 <script>alert(1)</script>")
	if strings.Contains(got, "<script") {
		t.Fatalf("Markdown 中的原始 HTML 必须被转义: %s", got)
	}
	if !strings.Contains(got, "正常文本") {
		t.Fatalf("正文丢失: %s", got)
	}
}

func TestRenderRichTextKeepsFormatting(t *testing.T) {
	got, plain := Render(FormatRichText, `<p>你好 <strong>世界</strong></p><ul><li>一</li></ul>`)
	if !strings.Contains(got, "<strong>世界</strong>") || !strings.Contains(got, "<li>一</li>") {
		t.Fatalf("富文本格式丢失: %s", got)
	}
	if plain != "你好 世界\n一" {
		t.Fatalf("纯文本结果异常: %q", plain)
	}
}

func TestExtractTags(t *testing.T) {
	tags := ExtractTags("今天聊聊 #Go语言# 与 #go语言# 还有 #未闭合 和 #a#")
	if len(tags) != 2 {
		t.Fatalf("标签数量错误: %+v", tags)
	}
	if tags[0].Name != "go语言" || tags[0].DisplayName != "Go语言" {
		t.Fatalf("首个标签错误: %+v", tags[0])
	}
	if tags[1].Name != "a" {
		t.Fatalf("第二个标签错误: %+v", tags[1])
	}
}

func TestExtractMentions(t *testing.T) {
	mentions := ExtractMentions("你好 @alice 和 @Bob_2，还有邮箱 a@b 与 @@carol 以及 @x")
	want := []string{"alice", "Bob_2"}
	if len(mentions) != len(want) {
		t.Fatalf("提及解析结果错误: %+v", mentions)
	}
	for i := range want {
		if mentions[i] != want[i] {
			t.Fatalf("提及解析结果错误: %+v", mentions)
		}
	}
}

func TestRenderLinkifiesTagsAndMentions(t *testing.T) {
	html, plain := Render(FormatRichText, `<p>聊聊 #Go语言# 并 @alice 一起</p>`)
	if !strings.Contains(html, `href="/tags/go%E8%AF%AD%E8%A8%80"`) {
		t.Fatalf("话题未链接化: %s", html)
	}
	if !strings.Contains(html, `href="/users/alice"`) {
		t.Fatalf("提及未链接化: %s", html)
	}
	// 纯文本提取仍应保留原始写法，供标签与提及解析使用
	if !strings.Contains(plain, "#Go语言#") || !strings.Contains(plain, "@alice") {
		t.Fatalf("纯文本被破坏: %q", plain)
	}
}

func TestRenderDoesNotLinkifyInsideCodeOrLinks(t *testing.T) {
	html, _ := Render(FormatMarkdown, "`#不是话题#` 与 [@alice](https://example.com)")
	if strings.Contains(html, `<code><a`) {
		t.Fatalf("代码块内不应链接化: %s", html)
	}
	if strings.Contains(html, `href="/users/alice"`) {
		t.Fatalf("已有链接内不应嵌套提及链接: %s", html)
	}
}

func TestRenderEscapesTagsWithoutInjection(t *testing.T) {
	html, _ := Render(FormatRichText, `<p>#a" onmouseover="alert(1)#</p>`)
	eventAttribute := regexp.MustCompile(`(?i)<[a-z]+[^>]*\son[a-z]+\s*=`)
	if eventAttribute.MatchString(html) {
		t.Fatalf("话题链接化不得引入事件属性: %s", html)
	}
	if strings.Contains(html, `"alert(1)#"`) {
		t.Fatalf("属性值不得被截断后拼回: %s", html)
	}
}

func TestPlainTextStripsTags(t *testing.T) {
	plain := PlainText("<p>第一段</p><p>第二段 <a href=\"/x\">链接</a></p>")
	if strings.Contains(plain, "<") || !strings.Contains(plain, "第一段") || !strings.Contains(plain, "链接") {
		t.Fatalf("纯文本提取异常: %q", plain)
	}
}

func TestRenderLinkifiesTagsWithAmpersand(t *testing.T) {
	html, _ := Render(FormatRichText, `<p>聊聊 #A&B# 与 /\evil.com 链接</p>`)
	if strings.Contains(html, "%26amp%3B") {
		t.Fatalf("话题链接不应包含被二次转义的实体: %s", html)
	}
	if !strings.Contains(html, `href="/tags/a&amp;b"`) {
		t.Fatalf("话题链接地址应为转义后的原始标签名: %s", html)
	}
}

func TestSafeLinkURLRejectsBackslashRelativePath(t *testing.T) {
	for _, bad := range []string{`/\evil.com`, `/\evil.com/x`, `/ok\evil`} {
		if got := SafeLinkURL(bad); got != "" {
			t.Errorf("相对路径含反斜杠必须拒绝: %q -> %q", bad, got)
		}
	}
	if got := SafeLinkURL("/posts/1?x=1"); got != "/posts/1?x=1" {
		t.Errorf("正常站内路径应保留: %q", got)
	}
	html, _ := Render(FormatMarkdown, `[点我](/\evil.com)`)
	if strings.Contains(html, `\evil.com`) || strings.Contains(html, `href="//evil.com"`) {
		t.Fatalf("Markdown 链接不应产生反斜杠跳转: %s", html)
	}
}
