// 管理后台接口（docs/API.md §8，仅 role=admin 可访问；后端会再次校验权限）
import { apiRequest } from './client'
import type { CurrentUser, CursorPage, UserListParams } from './types'

export interface UserDetailResponse {
  user: CurrentUser
  active_sessions: number
}

export interface AdminSettings {
  registration_enabled: boolean
}

/** GET /admin/users：游标分页的用户列表 */
export function listUsers(params: UserListParams): Promise<CursorPage<CurrentUser>> {
  return apiRequest<CursorPage<CurrentUser>>('/admin/users', {
    query: {
      q: params.q,
      status: params.status,
      is_ai: params.is_ai,
      cursor: params.cursor,
      limit: params.limit ?? 20,
    },
  })
}

/** GET /admin/users/:id：用户详情与活跃会话数 */
export function getUserDetail(id: string): Promise<UserDetailResponse> {
  return apiRequest<UserDetailResponse>(`/admin/users/${encodeURIComponent(id)}`)
}

/** POST /admin/users/:id/suspend：停用账号，会话立即失效 */
export function suspendUser(id: string): Promise<{ user: CurrentUser }> {
  return apiRequest<{ user: CurrentUser }>(`/admin/users/${encodeURIComponent(id)}/suspend`, { method: 'POST' })
}

/** POST /admin/users/:id/unsuspend：恢复账号 */
export function unsuspendUser(id: string): Promise<{ user: CurrentUser }> {
  return apiRequest<{ user: CurrentUser }>(`/admin/users/${encodeURIComponent(id)}/unsuspend`, { method: 'POST' })
}

/** POST /admin/users/:id/reset-password：管理员设置新密码，成功返回 204 */
export function resetUserPassword(id: string, new_password: string): Promise<void> {
  return apiRequest<void>(`/admin/users/${encodeURIComponent(id)}/reset-password`, {
    method: 'POST',
    body: { new_password },
  })
}

/** GET /admin/settings：系统设置 */
export function getAdminSettings(): Promise<AdminSettings> {
  return apiRequest<AdminSettings>('/admin/settings')
}

/** POST /admin/settings */
export function updateAdminSettings(payload: AdminSettings): Promise<AdminSettings> {
  return apiRequest<AdminSettings>('/admin/settings', { method: 'POST', body: payload })
}
