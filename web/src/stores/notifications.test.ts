// 通知未读 store：未登录清零、登录拉取、失败静默、轮询生命周期与隐藏跳过
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { fetchUnreadCount } = vi.hoisted(() => ({ fetchUnreadCount: vi.fn() }))
const authState = vi.hoisted(() => ({ user: null as { id: string } | null }))
const { setIntervalMock, clearIntervalMock } = vi.hoisted(() => ({
  setIntervalMock: vi.fn((_callback: () => void, _ms: number): number => 1),
  clearIntervalMock: vi.fn(),
}))

vi.mock('../api/social', () => ({ fetchUnreadCount }))
vi.mock('./auth', () => ({ authState }))

let notifications: typeof import('./notifications')
let documentStub: { hidden: boolean; createElement: () => unknown }

beforeEach(async () => {
  vi.resetModules()
  vi.clearAllMocks()
  authState.user = null
  documentStub = { hidden: false, createElement: () => ({}) }
  vi.stubGlobal('window', { setInterval: setIntervalMock, clearInterval: clearIntervalMock })
  vi.stubGlobal('document', documentStub)
  notifications = await import('./notifications')
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('notifications 刷新', () => {
  it('未登录：清零且不发起请求', async () => {
    await notifications.refreshUnreadCount()
    expect(fetchUnreadCount).not.toHaveBeenCalled()
    expect(notifications.notificationState.unread).toBe(0)
  })

  it('已登录：写入接口返回的未读数', async () => {
    authState.user = { id: '1' }
    fetchUnreadCount.mockResolvedValue({ unread_count: 7 })
    await notifications.refreshUnreadCount()
    expect(notifications.notificationState.unread).toBe(7)
  })

  it('请求失败：静默保留旧值', async () => {
    authState.user = { id: '1' }
    notifications.notificationState.unread = 5
    fetchUnreadCount.mockRejectedValue(new Error('offline'))
    await notifications.refreshUnreadCount()
    expect(notifications.notificationState.unread).toBe(5)
  })
})

describe('notifications 轮询生命周期', () => {
  it('启动立即刷新并注册 30 秒间隔，重复启动安全', async () => {
    authState.user = { id: '1' }
    fetchUnreadCount.mockResolvedValue({ unread_count: 3 })

    notifications.startNotificationPolling()
    notifications.startNotificationPolling()

    expect(setIntervalMock).toHaveBeenCalledTimes(1)
    expect(setIntervalMock.mock.calls[0][1]).toBe(30_000)
    await vi.waitFor(() => expect(notifications.notificationState.unread).toBe(3))
  })

  it('间隔到点触发刷新，页面隐藏时跳过请求', async () => {
    authState.user = { id: '1' }
    fetchUnreadCount.mockResolvedValue({ unread_count: 2 })
    notifications.startNotificationPolling()
    await vi.waitFor(() => expect(notifications.notificationState.unread).toBe(2))

    const tick = setIntervalMock.mock.calls[0][0]
    fetchUnreadCount.mockClear()
    tick()
    expect(fetchUnreadCount).toHaveBeenCalledTimes(1)

    documentStub.hidden = true
    fetchUnreadCount.mockClear()
    tick()
    expect(fetchUnreadCount).not.toHaveBeenCalled()
  })

  it('停止清理定时器并清零未读，重复停止安全', async () => {
    authState.user = { id: '1' }
    fetchUnreadCount.mockResolvedValue({ unread_count: 4 })
    notifications.startNotificationPolling()
    await vi.waitFor(() => expect(notifications.notificationState.unread).toBe(4))

    notifications.stopNotificationPolling()
    expect(clearIntervalMock).toHaveBeenCalledWith(1)
    expect(notifications.notificationState.unread).toBe(0)

    notifications.stopNotificationPolling()
    expect(clearIntervalMock).toHaveBeenCalledTimes(1)
  })
})
