// 站点公开配置：加载成功/失败、请求去重与重试、注册入口开放条件
import { beforeEach, describe, expect, it, vi } from 'vitest'

const { getAuthConfig } = vi.hoisted(() => ({ getAuthConfig: vi.fn() }))

vi.mock('../api/auth', () => ({ getAuthConfig }))

let site: typeof import('./site')

beforeEach(async () => {
  vi.resetModules()
  vi.clearAllMocks()
  site = await import('./site')
})

describe('loadSiteConfig', () => {
  it('加载成功写入注册开关与初始化标记', async () => {
    getAuthConfig.mockResolvedValue({ registration_enabled: false, bootstrap_required: false })

    await site.loadSiteConfig()

    expect(site.siteState.registrationEnabled).toBe(false)
    expect(site.siteState.bootstrapRequired).toBe(false)
    expect(site.siteState.ready).toBe(true)
    expect(site.registrationOpen.value).toBe(false)
  })

  it('空库初始化窗口：注册开关关闭时入口仍开放', async () => {
    getAuthConfig.mockResolvedValue({ registration_enabled: false, bootstrap_required: true })

    await site.loadSiteConfig()

    expect(site.registrationOpen.value).toBe(true)
  })

  it('并发调用共享同一次请求', async () => {
    let resolve!: (value: { registration_enabled: boolean; bootstrap_required: boolean }) => void
    getAuthConfig.mockImplementationOnce(() => new Promise((r) => { resolve = r }))

    const first = site.loadSiteConfig()
    const second = site.loadSiteConfig()
    resolve({ registration_enabled: true, bootstrap_required: false })
    await Promise.all([first, second])

    expect(getAuthConfig).toHaveBeenCalledTimes(1)
  })

  it('读取失败保持默认开放，并允许再次调用重试', async () => {
    getAuthConfig.mockRejectedValueOnce(new Error('offline'))
    await site.loadSiteConfig()
    expect(site.siteState.ready).toBe(true)
    expect(site.registrationOpen.value).toBe(true)

    getAuthConfig.mockResolvedValueOnce({ registration_enabled: false, bootstrap_required: false })
    await site.loadSiteConfig()
    expect(site.registrationOpen.value).toBe(false)
  })
})
