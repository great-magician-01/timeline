// 用户与资料接口（docs/API.md §4）
import { apiRequest } from './client'
import type { CurrentUser, PublicUser } from './types'

/** GET /users/:username：公开个人主页资料 */
export function getUserProfile(username: string): Promise<{ user: PublicUser }> {
  return apiRequest<{ user: PublicUser }>(`/users/${encodeURIComponent(username)}`)
}

/** POST /users/me：修改昵称与简介（本阶段不做头像上传） */
export function updateProfile(payload: { nickname: string; bio: string }): Promise<{ user: CurrentUser }> {
  return apiRequest<{ user: CurrentUser }>('/users/me', { method: 'POST', body: payload })
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
