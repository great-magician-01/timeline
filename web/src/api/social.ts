// 关注、点赞、评论、转发与通知接口（docs/API.md 第 5、8、9 章）
import { apiRequest } from './client'
import type {
  Comment,
  ContentFormat,
  CursorPage,
  FollowRequest,
  FollowState,
  LikeState,
  Notification,
  NotificationType,
  PublicUser,
  RepostState,
} from './types'
import type { PageParams } from './posts'

export interface ProfileSummary {
  user: PublicUser
  bio: string
  post_count: number
  following_count: number
  follower_count: number
  follow_state: FollowState
}

export function fetchProfile(username: string): Promise<ProfileSummary> {
  return apiRequest(`/users/${username}`)
}

export function followUser(username: string): Promise<{ follow_state: FollowState }> {
  return apiRequest(`/users/${username}/follow`, { method: 'POST' })
}

export function unfollowUser(username: string): Promise<{ follow_state: FollowState }> {
  return apiRequest(`/users/${username}/unfollow`, { method: 'POST' })
}

export function removeFollower(username: string): Promise<void> {
  return apiRequest(`/users/me/followers/${username}/remove`, { method: 'POST' })
}

export function listFollowers(username: string, params: PageParams = {}): Promise<CursorPage<PublicUser>> {
  return apiRequest(`/users/${username}/followers`, { query: { ...params } })
}

export function listFollowing(username: string, params: PageParams = {}): Promise<CursorPage<PublicUser>> {
  return apiRequest(`/users/${username}/following`, { query: { ...params } })
}

export function listFollowRequests(
  direction: 'incoming' | 'outgoing',
  params: PageParams = {},
): Promise<CursorPage<FollowRequest>> {
  return apiRequest('/follow-requests', { query: { direction, ...params } })
}

export function approveFollowRequest(id: string): Promise<{ status: string }> {
  return apiRequest(`/follow-requests/${id}/approve`, { method: 'POST' })
}

export function rejectFollowRequest(id: string): Promise<{ status: string }> {
  return apiRequest(`/follow-requests/${id}/reject`, { method: 'POST' })
}

export function cancelFollowRequest(id: string): Promise<{ status: string }> {
  return apiRequest(`/follow-requests/${id}/cancel`, { method: 'POST' })
}

export function likePost(id: string): Promise<LikeState> {
  return apiRequest(`/posts/${id}/like`, { method: 'POST' })
}

export function unlikePost(id: string): Promise<LikeState> {
  return apiRequest(`/posts/${id}/unlike`, { method: 'POST' })
}

export function likeComment(id: string): Promise<LikeState> {
  return apiRequest(`/comments/${id}/like`, { method: 'POST' })
}

export function unlikeComment(id: string): Promise<LikeState> {
  return apiRequest(`/comments/${id}/unlike`, { method: 'POST' })
}

export function listComments(postId: string, params: PageParams = {}): Promise<CursorPage<Comment>> {
  return apiRequest(`/posts/${postId}/comments`, { query: { ...params } })
}

export function listReplies(commentId: string, params: PageParams = {}): Promise<CursorPage<Comment>> {
  return apiRequest(`/comments/${commentId}/replies`, { query: { ...params } })
}

export function createComment(
  postId: string,
  input: { content_format: ContentFormat; content_source: string; parent_comment_id?: string },
): Promise<{ comment: Comment }> {
  return apiRequest(`/posts/${postId}/comments`, { method: 'POST', body: input })
}

export function deleteComment(id: string): Promise<void> {
  return apiRequest(`/comments/${id}/delete`, { method: 'POST' })
}

export function repostPost(id: string): Promise<RepostState> {
  return apiRequest(`/posts/${id}/repost`, { method: 'POST' })
}

export function unrepostPost(id: string): Promise<RepostState> {
  return apiRequest(`/posts/${id}/unrepost`, { method: 'POST' })
}

export function listNotifications(
  filter: 'all' | 'unread',
  params: PageParams & { type?: NotificationType } = {},
): Promise<CursorPage<Notification>> {
  return apiRequest('/notifications', { query: { filter, ...params } })
}

export function fetchUnreadCount(): Promise<{ unread_count: number }> {
  return apiRequest('/notifications/unread-count')
}

export function markNotificationRead(id: string): Promise<{ id: string; is_read: boolean }> {
  return apiRequest(`/notifications/${id}/read`, { method: 'POST' })
}

export function markAllNotificationsRead(): Promise<{ updated: number }> {
  return apiRequest('/notifications/read-all', { method: 'POST' })
}
