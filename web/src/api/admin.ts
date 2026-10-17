// 管理后台接口（docs/API.md §8，仅 role=admin 可访问；后端会再次校验权限）
import { apiRequest } from './client'
import type { AdminUserStats, CurrentUser, CursorPage, UserListParams } from './types'

export interface UserDetailResponse {
  user: CurrentUser
  active_sessions: number
  stats: AdminUserStats
}

/** 系统设置（GET/POST /admin/settings，字段后端契约见 internal/settings.ToMap） */
export interface AdminSettings {
  registration_enabled: boolean
  ai_global_enabled: boolean
  max_upload_bytes: number
  max_request_bytes: number
  storage_quota_bytes: number
  post_rate_limit: number
  post_rate_window_seconds: number
  upload_rate_limit: number
  upload_rate_window_seconds: number
  temp_attachment_retention_hours: number
  call_log_max_bytes: number
  call_log_retention_days: number
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

/** POST /admin/settings：支持部分更新；全局 AI 开关仍建议走 global-start/stop 以触发任务取消 */
export function updateAdminSettings(payload: Partial<AdminSettings>): Promise<AdminSettings> {
  return apiRequest<AdminSettings>('/admin/settings', { method: 'POST', body: payload })
}

/** 全局网页搜索配置；api_key 只在保存请求中出现，永不回显 */
export interface WebSearchSettings {
  base_url: string
  configured: boolean
  usable: boolean
  api_key_masked: string
}

export interface WebSearchSettingsInput {
  base_url?: string
  api_key?: string
  clear_api_key?: boolean
}

/** GET /admin/settings/web-search：读取全局网页搜索配置 */
export function getWebSearchSettings(): Promise<WebSearchSettings> {
  return apiRequest<WebSearchSettings>('/admin/settings/web-search')
}

/** POST /admin/settings/web-search：保存 Base URL 或独立搜索密钥 */
export function updateWebSearchSettings(payload: WebSearchSettingsInput): Promise<WebSearchSettings> {
  return apiRequest<WebSearchSettings>('/admin/settings/web-search', { method: 'POST', body: payload })
}
