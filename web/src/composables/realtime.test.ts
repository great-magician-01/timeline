import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { refreshDMUnreadCount, refreshGroupUnreadCount, refreshUnreadCount } = vi.hoisted(() => ({
  refreshDMUnreadCount: vi.fn(),
  refreshGroupUnreadCount: vi.fn(),
  refreshUnreadCount: vi.fn(),
}))

vi.mock('../stores/messages', () => ({ refreshDMUnreadCount }))
vi.mock('../stores/groups', () => ({ refreshGroupUnreadCount }))
vi.mock('../stores/notifications', () => ({ refreshUnreadCount }))

class FakeEventSource {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSED = 2
  static instances: FakeEventSource[] = []

  readonly url: string
  readyState = FakeEventSource.CONNECTING
  onopen: (() => void) | null = null
  onerror: (() => void) | null = null
  readonly listeners = new Map<string, Array<(event: unknown) => void>>()
  readonly close = vi.fn(() => {
    this.readyState = FakeEventSource.CLOSED
  })

  constructor(url: string) {
    this.url = url
    FakeEventSource.instances.push(this)
  }

  open(): void {
    this.readyState = FakeEventSource.OPEN
    this.onopen?.()
  }

  fail(readyState: number): void {
    this.readyState = readyState
    this.onerror?.()
  }

  addEventListener(type: string, listener: (event: unknown) => void): void {
    const list = this.listeners.get(type) ?? []
    list.push(listener)
    this.listeners.set(type, list)
  }

  emit(type: string, data: unknown): void {
    for (const listener of this.listeners.get(type) ?? []) listener({ data })
  }
}

let realtime: typeof import('./realtime')

beforeEach(async () => {
  vi.resetModules()
  vi.clearAllMocks()
  FakeEventSource.instances = []
  vi.stubGlobal('EventSource', FakeEventSource)
  realtime = await import('./realtime')
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('realtime 生命周期', () => {
  it('重复启动复用连接，停止时关闭并允许重新建立', () => {
    realtime.startRealtime()
    realtime.startRealtime()

    expect(FakeEventSource.instances).toHaveLength(1)
    const first = FakeEventSource.instances[0]
    expect(first.url).toBe('/api/v1/events')

    first.open()
    expect(realtime.realtimeConnected.value).toBe(true)

    realtime.stopRealtime()
    expect(first.close).toHaveBeenCalledTimes(1)
    expect(realtime.realtimeConnected.value).toBe(false)

    realtime.stopRealtime()
    expect(first.close).toHaveBeenCalledTimes(1)

    realtime.startRealtime()
    expect(FakeEventSource.instances).toHaveLength(2)
    expect(FakeEventSource.instances[1]).not.toBe(first)
    expect(first.close).toHaveBeenCalledTimes(1)
  })

  it('网络错误保留自动重连，永久关闭后允许重新建立', () => {
    realtime.startRealtime()
    const first = FakeEventSource.instances[0]

    first.fail(FakeEventSource.CONNECTING)
    expect(realtime.realtimeConnected.value).toBe(false)
    expect(first.close).not.toHaveBeenCalled()

    realtime.startRealtime()
    expect(FakeEventSource.instances).toHaveLength(1)

    first.fail(FakeEventSource.CLOSED)
    expect(first.close).toHaveBeenCalledTimes(1)

    realtime.startRealtime()
    expect(FakeEventSource.instances).toHaveLength(2)
    expect(FakeEventSource.instances[1]).not.toBe(first)
  })
})

describe('realtime 事件分发', () => {
  it('group 信号刷新群未读并分发给订阅者（载荷会话 ID 字符串化）', () => {
    realtime.startRealtime()
    const source = FakeEventSource.instances[0]

    const seen: Array<{ kind: string; conversationId: string | null }> = []
    const stop = realtime.onGroupSignal((signal) => seen.push(signal))

    source.emit('group', JSON.stringify({ conversation_id: '12345' }))

    expect(refreshGroupUnreadCount).toHaveBeenCalledTimes(1)
    expect(seen).toEqual([{ kind: 'group', conversationId: '12345' }])

    stop()
    source.emit('group', JSON.stringify({ conversation_id: '67890' }))
    expect(seen).toHaveLength(1)
  })

  it('dm 信号不影响群监听，群信号不影响私信监听', () => {
    realtime.startRealtime()
    const source = FakeEventSource.instances[0]

    const dmSeen: string[] = []
    const groupSeen: string[] = []
    realtime.onDMSignal((signal) => dmSeen.push(signal.kind))
    realtime.onGroupSignal((signal) => groupSeen.push(signal.kind))

    source.emit('dm', JSON.stringify({ conversation_id: '1' }))
    source.emit('group', JSON.stringify({ conversation_id: '2' }))

    expect(dmSeen).toEqual(['dm'])
    expect(groupSeen).toEqual(['group'])
    expect(refreshDMUnreadCount).toHaveBeenCalledTimes(1)
    expect(refreshGroupUnreadCount).toHaveBeenCalledTimes(1)
  })

  it('载荷异常时仍按无会话信号分发', () => {
    realtime.startRealtime()
    const source = FakeEventSource.instances[0]

    const seen: Array<{ kind: string; conversationId: string | null }> = []
    realtime.onGroupSignal((signal) => seen.push(signal))

    source.emit('group', 'not-json')

    expect(seen).toEqual([{ kind: 'group', conversationId: null }])
    expect(refreshGroupUnreadCount).toHaveBeenCalledTimes(1)
  })

  it('notification 信号只刷新通知未读', () => {
    realtime.startRealtime()
    const source = FakeEventSource.instances[0]

    source.emit('notification', '')

    expect(refreshUnreadCount).toHaveBeenCalledTimes(1)
    expect(refreshGroupUnreadCount).not.toHaveBeenCalled()
    expect(refreshDMUnreadCount).not.toHaveBeenCalled()
  })
})
