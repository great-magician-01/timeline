// 纯文本处理：快速发布与评论把纯文本转换为富文本源时使用。
// 只在本地做转义与分段，最终仍由后端把富文本转换为安全 HTML。

/** 转义 & < >，避免把用户输入的文本当作 HTML 标记 */
export function escapeHtml(value: string): string {
  return value.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

/** 纯文本按换行拆段，逐行转义后包 <p>；空行会被忽略 */
export function textToParagraphHTML(text: string): string {
  return text
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter((line) => line.length > 0)
    .map((line) => `<p>${escapeHtml(line)}</p>`)
    .join('')
}
