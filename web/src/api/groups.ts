// 群聊接口：群与成员管理、消息、已读、隐藏与解散（docs/API.md 群聊章）。
import { apiRequest } from './client'
import type { PageParams } from './posts'
import type { CursorPage, GroupConversation, GroupMember, GroupMessage } from './types'

/** GET /groups/unread-count：群聊未读总数（前端轮询，独立于私信与通知） */
export function fetchGroupUnreadCount(): Promise<{ unread_count: number }> {
  return apiRequest('/groups/unread-count')
}

/** GET /groups：我的群列表（有效成员、未隐藏、有消息），按最后消息倒序 */
export function listGroups(params: PageParams = {}): Promise<CursorPage<GroupConversation>> {
  return apiRequest('/groups', { query: { ...params } })
}

/** GET /groups/:id：群详情（含访问者角色与未读数） */
export function getGroup(id: string): Promise<{ group: GroupConversation }> {
  return apiRequest(`/groups/${id}`)
}

/** POST /groups：建群（创建者即群主，usernames 为初始成员） */
export function createGroup(name: string, usernames: string[]): Promise<{ group: GroupConversation }> {
  return apiRequest('/groups', { method: 'POST', body: { name, usernames } })
}

/** GET /groups/:id/members：成员分页（按加入时间正序，cursor 为字符串游标） */
export function listGroupMembers(
  groupId: string,
  params: { cursor?: string; limit?: number } = {},
): Promise<CursorPage<GroupMember>> {
  return apiRequest(`/groups/${groupId}/members`, { query: { ...params } })
}

/** GET /groups/:id/messages：翻旧消息（cursor，id 倒序） */
export function listGroupMessages(
  groupId: string,
  params: PageParams = {},
): Promise<CursorPage<GroupMessage>> {
  return apiRequest(`/groups/${groupId}/messages`, { query: { ...params } })
}

/** GET /groups/:id/messages?after_id=：增量拉取新消息（id 升序） */
export function listNewGroupMessages(
  groupId: string,
  afterId: string,
): Promise<CursorPage<GroupMessage>> {
  return apiRequest(`/groups/${groupId}/messages`, {
    query: { after_id: afterId },
  })
}

/** POST /groups/:id/messages：发送纯文本消息 */
export function sendGroupMessage(
  groupId: string,
  input: { content_format: 'richtext'; content_source: string },
): Promise<{ message: GroupMessage }> {
  return apiRequest(`/groups/${groupId}/messages`, {
    method: 'POST',
    body: input,
  })
}

/** POST /groups/messages/:id/recall：撤回自己的消息（默认 2 分钟内，幂等） */
export function recallGroupMessage(messageId: string): Promise<{ message: GroupMessage }> {
  return apiRequest(`/groups/messages/${messageId}/recall`, { method: 'POST' })
}

/** POST /groups/:id/read：把已读游标推进到群最后消息（幂等） */
export function markGroupRead(groupId: string): Promise<{ updated: number }> {
  return apiRequest(`/groups/${groupId}/read`, { method: 'POST' })
}

/** POST /groups/:id/hide：隐藏自己的群会话视图（新消息后自动重现） */
export function hideGroup(groupId: string): Promise<void> {
  return apiRequest(`/groups/${groupId}/hide`, { method: 'POST' })
}

/** POST /groups/:id/rename：群主改名 */
export function renameGroup(groupId: string, name: string): Promise<{ group: GroupConversation }> {
  return apiRequest(`/groups/${groupId}/rename`, { method: 'POST', body: { name } })
}

/** POST /groups/:id/invite：群主拉人（1–20 人，重复拉在群成员幂等跳过） */
export function inviteMembers(groupId: string, usernames: string[]): Promise<{ members: GroupMember[] }> {
  return apiRequest(`/groups/${groupId}/invite`, { method: 'POST', body: { usernames } })
}

/** POST /groups/:id/members/:uid/remove：群主移除普通成员 */
export function removeMember(groupId: string, userId: string): Promise<void> {
  return apiRequest(`/groups/${groupId}/members/${userId}/remove`, { method: 'POST' })
}

/** POST /groups/:id/transfer：群主转让（新群主须为在群成员） */
export function transferOwner(groupId: string, userId: string): Promise<void> {
  return apiRequest(`/groups/${groupId}/transfer`, { method: 'POST', body: { user_id: userId } })
}

/** POST /groups/:id/leave：成员退出（群主须先转让或解散） */
export function leaveGroup(groupId: string): Promise<void> {
  return apiRequest(`/groups/${groupId}/leave`, { method: 'POST' })
}

/** POST /groups/:id/dissolve：群主解散（群置只读保留） */
export function dissolveGroup(groupId: string): Promise<void> {
  return apiRequest(`/groups/${groupId}/dissolve`, { method: 'POST' })
}
