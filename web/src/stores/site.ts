// 站点公开配置（注册开关等）：游客与登录用户都需要，启动时加载一次
// GET /auth/config 是公开接口；读取失败时保持默认开放，实际注册仍由后端校验。
import { reactive } from 'vue'
import { getAuthConfig } from '../api/auth'

export const siteState = reactive<{
  registrationEnabled: boolean
  ready: boolean
}>({
  registrationEnabled: true,
  ready: false,
})

let configPromise: Promise<void> | null = null

/** 启动与注册页共用同一个请求；失败后允许再次调用重试 */
export function loadSiteConfig(): Promise<void> {
  if (configPromise) return configPromise
  configPromise = (async () => {
    try {
      const data = await getAuthConfig()
      siteState.registrationEnabled = data.registration_enabled
    } catch {
      // 公开配置读取失败不阻塞页面；注册提交时后端仍会拒绝
    } finally {
      siteState.ready = true
      configPromise = null
    }
  })()
  return configPromise
}
