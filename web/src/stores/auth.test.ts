// 认证状态：启动引导（成功/401/网络异常）、请求去重、登录登出与 401 回调
import { beforeEach, describe, expect, it, vi } from 'vitest'

const { me, login, logout } = vi.hoisted(() => ({
  me: vi.fn(),
  login: vi.fn(),
  logout: vi.fn(),
}))
const { setCsrfToken, refreshCsrfToken, setUnauthorizedHandler } = vi.hoisted(() => ({
  setCsrfToken: vi.fn(),
  refreshCsrfToken: vi.fn(),
  setUnauthorizedHandler: vi.fn(),
}))

vi.mock('../api/auth', () => ({ me, login, logout }))
vi.mock('../api/client', () => ({
  ApiError: class ApiError extends Error {
    readonly status: number
    readonly code: string

    constructor(status: number, code: string, message: string) {
      super(message)
      this.status = status
      this.code = code
    }
  },
  setCsrfToken,
  refreshCsrfToken,
  setUnauthorizedHandler,
}))

import { ApiError } from '../api/client'

const sampleUser = { id: '1', username: 'alice' }

let auth: typeof import('./auth')

beforeEach(async () => {
  vi.resetModules()
  vi.clearAllMocks()
  refreshCsrfToken.mockResolvedValue('guest-csrf')
  auth = await import('./auth')
})

describe('启动引导', () => {
  it('已登录：记录用户与会话 CSRF，不请求游客令牌', async () => {
    me.mockResolvedValue({ user: sampleUser, csrf_token: 'session-csrf' })

    await auth.bootstrapAuth()

    expect(auth.authState.user).toEqual(sampleUser)
    expect(auth.authState.ready).toBe(true)
    expect(auth.authState.bootstrapFailed).toBe(false)
    expect(setCsrfToken).toHaveBeenCalledWith('session-csrf')
    expect(refreshCsrfToken).not.toHaveBeenCalled()
  })

  it('未登录（401）：按游客继续并获取游客 CSRF，不算引导失败', async () => {
    me.mockRejectedValue(new ApiError(401, 'unauthenticated', '请先登录'))

    await auth.bootstrapAuth()

    expect(auth.authState.user).toBeNull()
    expect(auth.authState.ready).toBe(true)
    expect(auth.authState.bootstrapFailed).toBe(false)
    expect(refreshCsrfToken).toHaveBeenCalledTimes(1)
  })

  it('网络或服务异常：bootstrapFailed 置位，仍完成引导', async () => {
    me.mockRejectedValue(new Error('network down'))

    await auth.bootstrapAuth()

    expect(auth.authState.ready).toBe(true)
    expect(auth.authState.bootstrapFailed).toBe(true)
    expect(refreshCsrfToken).toHaveBeenCalledTimes(1)
  })

  it('非 401 的接口错误同样算引导失败', async () => {
    me.mockRejectedValue(new ApiError(503, 'service_unavailable', '暂不可用'))

    await auth.bootstrapAuth()

    expect(auth.authState.bootstrapFailed).toBe(true)
  })

  it('并发调用共享同一次引导请求', async () => {
    let resolve!: (value: { user: unknown; csrf_token: string }) => void
    me.mockImplementationOnce(() => new Promise((r) => { resolve = r }))

    const first = auth.ensureAuthReady()
    const second = auth.ensureAuthReady()
    resolve({ user: sampleUser, csrf_token: 't' })
    await Promise.all([first, second])

    expect(me).toHaveBeenCalledTimes(1)
  })
})

describe('登录与登出', () => {
  it('登录成功：写入用户与新的 CSRF，并清除引导失败标记', async () => {
    await auth.bootstrapAuth()
    auth.authState.bootstrapFailed = true
    login.mockResolvedValue({ user: sampleUser, csrf_token: 'new-csrf' })

    await auth.login('alice', 'password')

    expect(auth.authState.user).toEqual(sampleUser)
    expect(auth.authState.bootstrapFailed).toBe(false)
    expect(setCsrfToken).toHaveBeenCalledWith('new-csrf')
  })

  it('登出：接口失败也清空本地状态并重取游客 CSRF', async () => {
    login.mockResolvedValue({ user: sampleUser, csrf_token: 't' })
    await auth.login('alice', 'password')
    logout.mockRejectedValue(new Error('offline'))

    // finally 保证本地状态清理，但接口错误仍向上抛给调用方
    await expect(auth.logout()).rejects.toThrow('offline')

    expect(auth.authState.user).toBeNull()
    expect(setCsrfToken).toHaveBeenCalledWith(null)
    expect(refreshCsrfToken).toHaveBeenCalledTimes(1)
  })
})

describe('401 回调', () => {
  it('unauthorized 回调清空当前用户（供 API 客户端会话过期时调用）', async () => {
    me.mockResolvedValue({ user: sampleUser, csrf_token: 't' })
    await auth.bootstrapAuth()
    login.mockResolvedValue({ user: sampleUser, csrf_token: 't' })
    await auth.login('alice', 'password')
    expect(auth.authState.user).not.toBeNull()

    const handler = setUnauthorizedHandler.mock.calls[0][0] as () => void
    handler()

    expect(auth.authState.user).toBeNull()
  })
})
