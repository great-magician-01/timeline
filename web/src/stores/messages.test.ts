// 私信未读 store：未登录清零、登录拉取、失败静默、轮询生命周期与隐藏跳过
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { fetchDMUnreadCount } = vi.hoisted(() => ({ fetchDMUnreadCount: vi.fn() }))
const authState = vi.hoisted(() => ({ user: null as { id: string } | null }))
const { setIntervalMock, clearIntervalMock } = vi.hoisted(() => ({
  setIntervalMock: vi.fn((_callback: () => void, _ms: number): number => 1),
  clearIntervalMock: vi.fn(),
}))

vi.mock('../api/dm', () => ({ fetchDMUnreadCount }))
vi.mock('./auth', () => ({ authState }))

let messages: typeof import('./messages')
let documentStub: { hidden: boolean; createElement: () => unknown }

beforeEach(async () => {
  vi.resetModules()
  vi.clearAllMocks()
  authState.user = null
  documentStub = { hidden: false, createElement: () => ({}) }
  vi.stubGlobal('window', { setInterval: setIntervalMock, clearInterval: clearIntervalMock })
  vi.stubGlobal('document', documentStub)
  messages = await import('./messages')
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('messages 刷新', () => {
  it('未登录：清零且不发起请求', async () => {
    await messages.refreshDMUnreadCount()
    expect(fetchDMUnreadCount).not.toHaveBeenCalled()
    expect(messages.messageState.unread).toBe(0)
  })

  it('已登录：写入接口返回的未读数', async () => {
    authState.user = { id: '1' }
    fetchDMUnreadCount.mockResolvedValue({ unread_count: 9 })
    await messages.refreshDMUnreadCount()
    expect(messages.messageState.unread).toBe(9)
  })

  it('请求失败：静默保留旧值', async () => {
    authState.user = { id: '1' }
    messages.messageState.unread = 2
    fetchDMUnreadCount.mockRejectedValue(new Error('offline'))
    await messages.refreshDMUnreadCount()
    expect(messages.messageState.unread).toBe(2)
  })
})

describe('messages 轮询生命周期', () => {
  it('启动立即刷新并注册 30 秒间隔，重复启动安全', async () => {
    authState.user = { id: '1' }
    fetchDMUnreadCount.mockResolvedValue({ unread_count: 6 })

    messages.startMessagePolling()
    messages.startMessagePolling()

    expect(setIntervalMock).toHaveBeenCalledTimes(1)
    expect(setIntervalMock.mock.calls[0][1]).toBe(30_000)
    await vi.waitFor(() => expect(messages.messageState.unread).toBe(6))
  })

  it('间隔到点触发刷新，页面隐藏时跳过请求', async () => {
    authState.user = { id: '1' }
    fetchDMUnreadCount.mockResolvedValue({ unread_count: 1 })
    messages.startMessagePolling()
    await vi.waitFor(() => expect(messages.messageState.unread).toBe(1))

    const tick = setIntervalMock.mock.calls[0][0]
    fetchDMUnreadCount.mockClear()
    tick()
    expect(fetchDMUnreadCount).toHaveBeenCalledTimes(1)

    documentStub.hidden = true
    fetchDMUnreadCount.mockClear()
    tick()
    expect(fetchDMUnreadCount).not.toHaveBeenCalled()
  })

  it('停止清理定时器并清零未读，重复停止安全', async () => {
    authState.user = { id: '1' }
    fetchDMUnreadCount.mockResolvedValue({ unread_count: 8 })
    messages.startMessagePolling()
    await vi.waitFor(() => expect(messages.messageState.unread).toBe(8))

    messages.stopMessagePolling()
    expect(clearIntervalMock).toHaveBeenCalledWith(1)
    expect(messages.messageState.unread).toBe(0)

    messages.stopMessagePolling()
    expect(clearIntervalMock).toHaveBeenCalledTimes(1)
  })
})
