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

/** POST /auth/password：修改密码，成功返回 204 */
export function changePassword(old_password: string, new_password: string): Promise<void> {
  return apiRequest<void>('/auth/password', { method: 'POST', body: { old_password, new_password } })
}
