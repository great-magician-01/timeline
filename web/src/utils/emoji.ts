// 表情插入辅助：在 textarea 光标处插入文本并保持 v-model 同步。
// 选取面板弹出后点击表情会让 textarea 失焦，因此以点击时的选区为准插入。
export function insertAtSelection(textarea: HTMLTextAreaElement, text: string): void {
  const start = textarea.selectionStart ?? textarea.value.length
  const end = textarea.selectionEnd ?? start
  textarea.setRangeText(text, start, end, 'end')
  // setRangeText 不触发 input 事件，手动派发让 v-model 读到新值
  textarea.dispatchEvent(new Event('input', { bubbles: true }))
  textarea.focus()
  const caret = start + text.length
  textarea.setSelectionRange(caret, caret)
}
