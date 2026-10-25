// 群聊未读 store：未登录清零、登录拉取、失败静默、轮询生命周期与隐藏跳过
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { fetchGroupUnreadCount } = vi.hoisted(() => ({ fetchGroupUnreadCount: vi.fn() }))
const authState = vi.hoisted(() => ({ user: null as { id: string } | null }))
const { setIntervalMock, clearIntervalMock } = vi.hoisted(() => ({
  setIntervalMock: vi.fn((_callback: () => void, _ms: number): number => 1),
  clearIntervalMock: vi.fn(),
}))

vi.mock('../api/groups', () => ({ fetchGroupUnreadCount }))
vi.mock('./auth', () => ({ authState }))

let groups: typeof import('./groups')
let documentStub: { hidden: boolean; createElement: () => unknown }

beforeEach(async () => {
  vi.resetModules()
  vi.clearAllMocks()
  authState.user = null
  documentStub = { hidden: false, createElement: () => ({}) }
  vi.stubGlobal('window', { setInterval: setIntervalMock, clearInterval: clearIntervalMock })
  vi.stubGlobal('document', documentStub)
  groups = await import('./groups')
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('groups 刷新', () => {
  it('未登录：清零且不发起请求', async () => {
    await groups.refreshGroupUnreadCount()
    expect(fetchGroupUnreadCount).not.toHaveBeenCalled()
    expect(groups.groupState.unread).toBe(0)
  })

  it('已登录：写入接口返回的未读数', async () => {
    authState.user = { id: '1' }
    fetchGroupUnreadCount.mockResolvedValue({ unread_count: 12 })
    await groups.refreshGroupUnreadCount()
    expect(groups.groupState.unread).toBe(12)
  })

  it('请求失败：静默保留旧值', async () => {
    authState.user = { id: '1' }
    groups.groupState.unread = 3
    fetchGroupUnreadCount.mockRejectedValue(new Error('offline'))
    await groups.refreshGroupUnreadCount()
    expect(groups.groupState.unread).toBe(3)
  })
})

describe('groups 轮询生命周期', () => {
  it('启动立即刷新并注册 30 秒间隔，重复启动安全', async () => {
    authState.user = { id: '1' }
    fetchGroupUnreadCount.mockResolvedValue({ unread_count: 5 })

    groups.startGroupPolling()
    groups.startGroupPolling()

    expect(setIntervalMock).toHaveBeenCalledTimes(1)
    expect(setIntervalMock.mock.calls[0][1]).toBe(30_000)
    await vi.waitFor(() => expect(groups.groupState.unread).toBe(5))
  })

  it('间隔到点触发刷新，页面隐藏时跳过请求', async () => {
    authState.user = { id: '1' }
    fetchGroupUnreadCount.mockResolvedValue({ unread_count: 2 })
    groups.startGroupPolling()
    await vi.waitFor(() => expect(groups.groupState.unread).toBe(2))

    const tick = setIntervalMock.mock.calls[0][0]
    fetchGroupUnreadCount.mockClear()
    tick()
    expect(fetchGroupUnreadCount).toHaveBeenCalledTimes(1)

    documentStub.hidden = true
    fetchGroupUnreadCount.mockClear()
    tick()
    expect(fetchGroupUnreadCount).not.toHaveBeenCalled()
  })

  it('停止清理定时器并清零未读，重复停止安全', async () => {
    authState.user = { id: '1' }
    fetchGroupUnreadCount.mockResolvedValue({ unread_count: 7 })
    groups.startGroupPolling()
    await vi.waitFor(() => expect(groups.groupState.unread).toBe(7))

    groups.stopGroupPolling()
    expect(clearIntervalMock).toHaveBeenCalledWith(1)
    expect(groups.groupState.unread).toBe(0)

    groups.stopGroupPolling()
    expect(clearIntervalMock).toHaveBeenCalledTimes(1)
  })
})
