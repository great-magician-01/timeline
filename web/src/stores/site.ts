// 站点公开配置（注册开关等）：游客与登录用户都需要，启动时加载一次
// GET /auth/config 是公开接口；读取失败时保持默认开放，实际注册仍由后端校验。
import { computed, reactive } from 'vue'
import { getAuthConfig } from '../api/auth'

export const siteState = reactive<{
  registrationEnabled: boolean
  /** 系统尚无人类用户（未初始化）：首个注册账号将自动成为超级管理员 */
  bootstrapRequired: boolean
  ready: boolean
}>({
  registrationEnabled: true,
  bootstrapRequired: false,
  ready: false,
})

/** 注册入口是否可用：注册开关打开，或空库初始化例外（首个注册者将成为超管） */
export const registrationOpen = computed(() => siteState.registrationEnabled || siteState.bootstrapRequired)

let configPromise: Promise<void> | null = null

/** 启动与注册页共用同一个请求；失败后允许再次调用重试 */
export function loadSiteConfig(): Promise<void> {
  if (configPromise) return configPromise
  configPromise = (async () => {
    try {
      const data = await getAuthConfig()
      siteState.registrationEnabled = data.registration_enabled
      siteState.bootstrapRequired = data.bootstrap_required
    } catch {
      // 公开配置读取失败不阻塞页面；注册提交时后端仍会拒绝
    } finally {
      siteState.ready = true
      configPromise = null
    }
  })()
  return configPromise
}
