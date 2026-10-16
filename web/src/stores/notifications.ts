// 通知未读数：登录后轮询（docs/REQUIREMENTS.md 11：首版通过轮询更新）
import { reactive } from 'vue'
import { fetchUnreadCount } from '../api/social'
import { authState } from './auth'

export const notificationState = reactive({
  unread: 0,
})

let timer: number | null = null

async function refresh(): Promise<void> {
  if (!authState.user) {
    notificationState.unread = 0
    return
  }
  try {
    const result = await fetchUnreadCount()
    notificationState.unread = result.unread_count
  } catch {
    // 轮询失败静默处理，不打断用户操作；下一轮会自动重试
  }
}

/** 启动轮询；重复调用安全。间隔 30 秒，页面隐藏时跳过请求。 */
export function startNotificationPolling(): void {
  if (timer !== null) return
  void refresh()
  timer = window.setInterval(() => {
    if (document.hidden) return
    void refresh()
  }, 30_000)
}

export function stopNotificationPolling(): void {
  if (timer === null) return
  window.clearInterval(timer)
  timer = null
  notificationState.unread = 0
}

export { refresh as refreshUnreadCount }
