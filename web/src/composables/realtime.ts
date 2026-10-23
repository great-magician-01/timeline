// SSE 实时提示信号（docs/REQUIREMENTS.md）：登录后连接 GET /api/v1/events，
// 事件只携带种类（notification/dm）与私信会话 ID，不携带内容载荷；
// 收到信号后复用各 store 的既有只读接口拉取，可见性与一致性由服务端保证。
// 现有轮询保留为降级兜底：连接失败或断开期间数据仍按 30s 轮询刷新。
import { readonly, ref } from 'vue'
import { refreshDMUnreadCount } from '../stores/messages'
import { refreshUnreadCount } from '../stores/notifications'

export interface RealtimeSignal {
  kind: 'notification' | 'dm'
  conversationId: string | null
}

// 对话页等局部视图按会话 ID 订阅私信信号；回调注册表代替引入事件库
type DMListener = (signal: RealtimeSignal) => void
const dmListeners = new Set<DMListener>()

const connected = ref(false)
let source: EventSource | null = null
let started = false

/** 订阅私信会话信号；返回取消函数。 */
export function onDMSignal(listener: DMListener): () => void {
  dmListeners.add(listener)
  return () => {
    dmListeners.delete(listener)
  }
}

export const realtimeConnected = readonly(connected)

function dispatch(kind: RealtimeSignal['kind'], data: string): void {
  let conversationId: string | null = null
  if (data) {
    try {
      const payload = JSON.parse(data) as { conversation_id?: string }
      conversationId = payload.conversation_id ?? null
    } catch {
      // 载荷异常时仍按无会话信号处理
    }
  }
  const signal: RealtimeSignal = { kind, conversationId }
  for (const listener of dmListeners) listener(signal)
}

/** 建立 SSE 连接；重复调用安全。仅登录用户有意义（服务端 Login 策略）。 */
export function startRealtime(): void {
  if (started || typeof EventSource === 'undefined') return
  started = true
  const source = new EventSource('/api/v1/events')
  source.onopen = () => {
    connected.value = true
  }
  source.addEventListener('notification', () => {
    void refreshUnreadCount()
  })
  source.addEventListener('dm', (event) => {
    const data = 'data' in event ? String((event as MessageEvent).data) : ''
    void refreshDMUnreadCount()
    dispatch('dm', data)
  })
  source.onerror = () => {
    connected.value = false
    // 网络抖动时 EventSource 自动重连（readyState=CONNECTING）；
    // 会话过期等永久失败会关闭（CLOSED），此时停止避免无效重试，轮询兜底。
    if (source.readyState === EventSource.CLOSED) stopRealtime()
  }
}

/** 关闭 SSE 连接；登出或会话过期时由 App 调用。 */
export function stopRealtime(): void {
  started = false
  connected.value = false
  if (source) {
    source.close()
    source = null
  }
}
