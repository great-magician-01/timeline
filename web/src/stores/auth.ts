// 认证状态（模块级单例，供路由守卫与所有页面共享）
// 启动流程：GET /auth/me 成功则记录用户与 CSRF token；401 时再取游客 CSRF token。
import { reactive } from 'vue'
import * as authApi from '../api/auth'
import { ApiError, refreshCsrfToken, setCsrfToken, setUnauthorizedHandler } from '../api/client'
import type { CurrentUser } from '../api/types'

export const authState = reactive<{
  user: CurrentUser | null
  /** 启动引导是否完成（完成后守卫才能判断登录态） */
  ready: boolean
  /** 引导请求失败（网络或服务异常），此时按游客处理但提示不可用 */
  bootstrapFailed: boolean
}>({
  user: null,
  ready: false,
  bootstrapFailed: false,
})

let readyPromise: Promise<void> | null = null

/** 启动引导：只执行一次，路由守卫与界面共享同一个 Promise */
export function ensureAuthReady(): Promise<void> {
  if (!readyPromise) readyPromise = bootstrap()
  return readyPromise
}

/** main.ts 启动时调用（与 ensureAuthReady 等价，提前触发请求） */
export function bootstrapAuth(): Promise<void> {
  return ensureAuthReady()
}

function bootstrap(): Promise<void> {
  setUnauthorizedHandler(() => {
    authState.user = null
  })
  return (async () => {
    try {
      const data = await authApi.me()
      authState.user = data.user
      setCsrfToken(data.csrf_token)
    } catch (error) {
      authState.user = null
      if (!(error instanceof ApiError) || error.status !== 401) authState.bootstrapFailed = true
      // 未登录（或引导失败）：获取游客 CSRF token，登录/注册等 POST 才能通过 CSRF 校验
      await refreshCsrfToken()
    } finally {
      authState.ready = true
    }
  })()
}

/** 更新当前用户（资料保存、管理端操作等场景由调用方传入最新对象） */
export function setCurrentUser(user: CurrentUser): void {
  authState.user = user
}

export function clearCurrentUser(): void {
  authState.user = null
}

/** 登录：成功后保存用户与新的 CSRF token */
export async function login(identifier: string, password: string): Promise<CurrentUser> {
  const data = await authApi.login(identifier, password)
  setCsrfToken(data.csrf_token)
  authState.user = data.user
  authState.bootstrapFailed = false
  return data.user
}

/** 退出登录：无论接口是否成功都清空本地状态，并重新获取游客 CSRF token */
export async function logout(): Promise<void> {
  try {
    await authApi.logout()
  } finally {
    authState.user = null
    setCsrfToken(null)
    await refreshCsrfToken()
  }
}
