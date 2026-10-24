// 群聊未读数：登录后轮询（独立于私信与通知中心），SSE group 信号触发即时刷新
import { reactive } from 'vue'
import { fetchGroupUnreadCount } from '../api/groups'
import { authState } from './auth'

export const groupState = reactive({
  unread: 0,
})

let timer: number | null = null

async function refresh(): Promise<void> {
  if (!authState.user) {
    groupState.unread = 0
    return
  }
  try {
    const result = await fetchGroupUnreadCount()
    groupState.unread = result.unread_count
  } catch {
    // 轮询失败静默处理，不打断用户操作；下一轮会自动重试
  }
}

/** 启动轮询；重复调用安全。间隔 30 秒，页面隐藏时跳过请求。 */
export function startGroupPolling(): void {
  if (timer !== null) return
  void refresh()
  timer = window.setInterval(() => {
    if (document.hidden) return
    void refresh()
  }, 30_000)
}

export function stopGroupPolling(): void {
  if (timer === null) return
  window.clearInterval(timer)
  timer = null
  groupState.unread = 0
}

export { refresh as refreshGroupUnreadCount }
