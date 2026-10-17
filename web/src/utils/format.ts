// 展示格式化工具（时间、计数、文件大小）

/** 相对时间：1 分钟内「刚刚」，其余按分钟/小时/天，超过 7 天显示日期 */
export function relativeTime(iso: string | null | undefined): string {
  if (!iso) return ''
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return ''
  const diff = Date.now() - date.getTime()
  const minute = 60_000
  const hour = 60 * minute
  const day = 24 * hour
  if (diff < 0) return absoluteTime(iso, true)
  if (diff < minute) return '刚刚'
  if (diff < hour) return `${Math.floor(diff / minute)} 分钟前`
  if (diff < day) return `${Math.floor(diff / hour)} 小时前`
  if (diff < 7 * day) return `${Math.floor(diff / day)} 天前`
  return absoluteTime(iso)
}

/** 固定按北京时间（UTC+8，无夏令时）拆解，不跟随浏览器本地时区。 */
function beijingParts(date: Date): { year: number; month: number; day: number; hour: number; minute: number; second: number } {
  const shifted = new Date(date.getTime() + 8 * 60 * 60 * 1000)
  return {
    year: shifted.getUTCFullYear(),
    month: shifted.getUTCMonth() + 1,
    day: shifted.getUTCDate(),
    hour: shifted.getUTCHours(),
    minute: shifted.getUTCMinutes(),
    second: shifted.getUTCSeconds(),
  }
}

const pad = (value: number) => String(value).padStart(2, '0')

/** 绝对时间：默认「2026-10-17 20:30」，始终按北京时间展示 */
export function absoluteTime(iso: string | null | undefined, withSeconds = false): string {
  if (!iso) return ''
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return ''
  const parts = beijingParts(date)
  const base = `${parts.year}-${pad(parts.month)}-${pad(parts.day)} ${pad(parts.hour)}:${pad(parts.minute)}`
  return withSeconds ? `${base}:${pad(parts.second)}` : base
}

/** 帖子卡片使用相对时间，悬停显示完整时间 */
export function timeTitle(iso: string | null | undefined): string {
  return iso ? absoluteTime(iso, true) : ''
}

/** datetime-local 输入框需要的北京时间值（不随浏览器时区变化） */
export function toDatetimeLocal(iso: string | null | undefined): string {
  if (!iso) return ''
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return ''
  const parts = beijingParts(date)
  return `${parts.year}-${pad(parts.month)}-${pad(parts.day)}T${pad(parts.hour)}:${pad(parts.minute)}`
}

/** datetime-local 的值按北京时间解释并转 ISO；后端统一使用 Asia/Shanghai。 */
export function fromDatetimeLocal(value: string): string {
  const text = value.trim()
  if (!text) return ''
  // datetime-local 可能是 YYYY-MM-DDTHH:mm 或带秒；没有时区信息时按 +08:00 解释。
  const withZone = /[zZ]|[+-]\d{2}:?\d{2}$/.test(text)
    ? text
    : `${text.length === 16 ? `${text}:00` : text}+08:00`
  const date = new Date(withZone)
  if (Number.isNaN(date.getTime())) return ''
  return date.toISOString()
}

export function formatCount(value: number): string {
  if (value < 1000) return String(value)
  if (value < 10_000) return `${(value / 1000).toFixed(1).replace(/\.0$/, '')}k`
  return `${Math.round(value / 1000)}k`
}

export function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`
  return `${(bytes / 1024 / 1024 / 1024).toFixed(2)} GB`
}

/** 视频/音频时长：12.3s → 0:12 */
export function formatDuration(ms: number | null | undefined): string {
  if (!ms || ms <= 0) return ''
  const total = Math.round(ms / 1000)
  const minutes = Math.floor(total / 60)
  const seconds = total % 60
  return `${minutes}:${String(seconds).padStart(2, '0')}`
}

/** 正文摘要：去掉 HTML 标签后的纯文本，用于列表与提示 */
export function plainSummary(html: string, limit = 80): string {
  const text = html
    .replace(/<[^>]*>/g, ' ')
    .replace(/&nbsp;/g, ' ')
    .replace(/&amp;/g, '&')
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"')
    .replace(/\s+/g, ' ')
    .trim()
  return text.length > limit ? `${text.slice(0, limit)}…` : text
}
