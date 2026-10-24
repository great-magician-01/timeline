import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { refreshDMUnreadCount, refreshUnreadCount } = vi.hoisted(() => ({
  refreshDMUnreadCount: vi.fn(),
  refreshUnreadCount: vi.fn(),
}))

vi.mock('../stores/messages', () => ({ refreshDMUnreadCount }))
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
  readonly addEventListener = vi.fn()
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
