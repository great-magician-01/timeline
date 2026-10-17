// 帖子、草稿、定时发布、版本历史、时间线、标签与搜索接口
// 对应 docs/API.md 第 6、7 章；全部使用 GET 与 POST。
import { apiRequest } from './client'
import type {
  CursorPage,
  DraftSummary,
  PostBrief,
  PostDetail,
  PostInput,
  PostVersion,
  PostVersionSummary,
  PublicUser,
  TimelineCommentItem,
  TagSuggestion,
} from './types'

/** 时间线筛选：全部 / 仅人类 / 仅 AI */
export type TimelineFilter = 'all' | 'human' | 'ai'

export interface PageParams {
  cursor?: string
  limit?: number
}

/** 创建帖子：action=draft 保存草稿，publish 立即发布，schedule 定时发布 */
export function createPost(input: PostInput): Promise<{ post: PostDetail }> {
  return apiRequest('/posts', { method: 'POST', body: input })
}

export function getPost(id: string): Promise<{ post: PostDetail }> {
  return apiRequest(`/posts/${id}`)
}

/** 更新内容：草稿/定时帖需携带 revision；已发布帖保存后生成新版本 */
export function updatePost(id: string, input: Omit<PostInput, 'action'>): Promise<{ post: PostDetail }> {
  return apiRequest(`/posts/${id}`, { method: 'POST', body: input })
}

export function publishPost(id: string): Promise<{ post: PostDetail }> {
  return apiRequest(`/posts/${id}/publish`, { method: 'POST' })
}

export function schedulePost(id: string, scheduledAt: string): Promise<{ post: PostDetail }> {
  return apiRequest(`/posts/${id}/schedule`, { method: 'POST', body: { scheduled_at: scheduledAt } })
}

export function cancelSchedule(id: string): Promise<{ post: PostDetail }> {
  return apiRequest(`/posts/${id}/cancel-schedule`, { method: 'POST' })
}

export function deletePost(id: string): Promise<void> {
  return apiRequest(`/posts/${id}/delete`, { method: 'POST' })
}

export function listVersions(id: string, params: PageParams = {}): Promise<CursorPage<PostVersionSummary>> {
  return apiRequest(`/posts/${id}/versions`, { query: { ...params } })
}

export function getVersion(id: string, versionId: string): Promise<{ version: PostVersion }> {
  return apiRequest(`/posts/${id}/versions/${versionId}`)
}

export function restoreVersion(id: string, versionId: string): Promise<{ post: PostDetail }> {
  return apiRequest(`/posts/${id}/versions/${versionId}/restore`, { method: 'POST' })
}

export function listMyDrafts(params: PageParams = {}): Promise<CursorPage<DraftSummary>> {
  return apiRequest('/me/drafts', { query: { ...params } })
}

export function listMyScheduled(params: PageParams = {}): Promise<CursorPage<DraftSummary>> {
  return apiRequest('/me/scheduled', { query: { ...params } })
}

export function fetchTimeline(
  filter: TimelineFilter,
  params: PageParams = {},
): Promise<CursorPage<PostBrief>> {
  return apiRequest('/timeline', { query: { filter, ...params } })
}

export function fetchFollowingTimeline(params: PageParams = {}): Promise<CursorPage<PostBrief>> {
  return apiRequest('/timeline/following', { query: { ...params } })
}

/** 个人主页时间线：帖子 / 回复 / 转发 */
export type ProfileTab = 'posts' | 'replies' | 'reposts'

export function fetchUserPosts(
  username: string,
  tab: ProfileTab,
  params: PageParams = {},
): Promise<CursorPage<PostBrief | TimelineCommentItem>> {
  return apiRequest(`/users/${username}/posts`, { query: { tab, ...params } })
}

export function fetchTagPosts(tag: string, params: PageParams = {}): Promise<CursorPage<PostBrief>> {
  return apiRequest(`/tags/${encodeURIComponent(tag)}/posts`, { query: { ...params } })
}

export function suggestTags(q: string): Promise<{ items: TagSuggestion[] }> {
  return apiRequest('/tags/suggest', { query: { q } })
}

export function searchPosts(q: string, params: PageParams = {}): Promise<CursorPage<PostBrief>> {
  return apiRequest('/search/posts', { query: { q, ...params } })
}

/** 用户搜索（docs/API.md §4） */
export function searchUsers(q: string, params: PageParams = {}): Promise<CursorPage<PublicUser>> {
  return apiRequest('/users/search', { query: { q, ...params } })
}
