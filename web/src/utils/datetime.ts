// 时间展示：全系统固定北京时间（与后端 Asia/Shanghai 约定一致），不做本地时区换算
const dateFormatter = new Intl.DateTimeFormat('zh-CN', {
  year: 'numeric',
  month: 'long',
  day: 'numeric',
  timeZone: 'Asia/Shanghai',
})

const dateTimeFormatter = new Intl.DateTimeFormat('zh-CN', {
  year: 'numeric',
  month: 'long',
  day: 'numeric',
  hour: '2-digit',
  minute: '2-digit',
  hour12: false,
  timeZone: 'Asia/Shanghai',
})

function toDate(value: string | null | undefined): Date | null {
  if (!value) return null
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? null : date
}

/** ISO 时间 → 「2026年10月16日」；无法解析时返回占位符 */
export function formatDate(value: string | null | undefined): string {
  const date = toDate(value)
  return date ? dateFormatter.format(date) : '—'
}

/** ISO 时间 → 「2026年10月16日 20:30」；无法解析时返回占位符 */
export function formatDateTime(value: string | null | undefined): string {
  const date = toDate(value)
  return date ? dateTimeFormatter.format(date) : '—'
}
