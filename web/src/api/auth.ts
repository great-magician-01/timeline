// 认证接口（docs/API.md §3）
import { apiRequest } from './client'
import type { CurrentUser } from './types'

export interface AuthResponse {
  user: CurrentUser
  csrf_token: string
}

/** POST /auth/login：identifier 可以是用户名或邮箱 */
export function login(identifier: string, password: string): Promise<AuthResponse> {
  return apiRequest<AuthResponse>('/auth/login', { method: 'POST', body: { identifier, password } })
}

/** POST /auth/register：成功返回 201 与用户对象，不自动登录 */
export function register(username: string, email: string, password: string): Promise<{ user: CurrentUser }> {
  return apiRequest<{ user: CurrentUser }>('/auth/register', {
    method: 'POST',
    body: { username, email, password },
  })
}

/** POST /auth/logout：成功返回 204 */
export function logout(): Promise<void> {
  return apiRequest<void>('/auth/logout', { method: 'POST' })
}

/** GET /auth/me：当前用户与 CSRF token */
export function me(): Promise<AuthResponse> {
  return apiRequest<AuthResponse>('/auth/me')
}

/** 公开注册配置（GET /auth/config）；未登录也可访问 */
export interface AuthConfig {
  registration_enabled: boolean
  /** 系统尚无人类用户（未初始化）：首个注册账号将自动成为超级管理员，且空库时忽略注册开关 */
  bootstrap_required: boolean
}

export function getAuthConfig(): Promise<AuthConfig> {
  return apiRequest<AuthConfig>('/auth/config')
}

/** POST /auth/password：修改密码，成功返回 204 */
export function changePassword(old_password: string, new_password: string): Promise<void> {
  return apiRequest<void>('/auth/password', { method: 'POST', body: { old_password, new_password } })
}
