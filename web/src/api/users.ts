// 用户与资料接口（docs/API.md §4）
import { apiRequest } from './client'
import type { CurrentUser, DMPolicy, PublicUser } from './types'

/** GET /users/:username：公开个人主页资料 */
export function getUserProfile(username: string): Promise<{ user: PublicUser }> {
  return apiRequest<{ user: PublicUser }>(`/users/${encodeURIComponent(username)}`)
}

/** POST /users/me：修改昵称与简介（头像与主页横幅通过附件上传设置） */
export function updateProfile(payload: { nickname: string; bio: string }): Promise<{ user: CurrentUser }> {
  return apiRequest<{ user: CurrentUser }>('/users/me', { method: 'POST', body: payload })
}

/** POST /users/me/banner/delete：移除主页横幅，回退默认占位 */
export function deleteBanner(): Promise<{ user: CurrentUser }> {
  return apiRequest<{ user: CurrentUser }>('/users/me/banner/delete', { method: 'POST' })
}

/** GET /users/me/follow-settings：关注审批开关 */
export function getFollowSettings(): Promise<{ require_follow_approval: boolean }> {
  return apiRequest<{ require_follow_approval: boolean }>('/users/me/follow-settings')
}

/** POST /users/me/follow-settings */
export function updateFollowSettings(require_follow_approval: boolean): Promise<{ require_follow_approval: boolean }> {
  return apiRequest<{ require_follow_approval: boolean }>('/users/me/follow-settings', {
    method: 'POST',
    body: { require_follow_approval },
  })
}

/** GET /users/me/dm-settings：私信接收策略 */
export function getDMSettings(): Promise<{ dm_policy: DMPolicy }> {
  return apiRequest<{ dm_policy: DMPolicy }>('/users/me/dm-settings')
}

/** POST /users/me/dm-settings */
export function updateDMSettings(dm_policy: DMPolicy): Promise<{ dm_policy: DMPolicy }> {
  return apiRequest<{ dm_policy: DMPolicy }>('/users/me/dm-settings', {
    method: 'POST',
    body: { dm_policy },
  })
}
