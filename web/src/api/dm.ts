// 私信接口（docs/API.md 第 10 章）：1:1 会话、消息、已读与撤回。
import { apiRequest } from './client'
import type { PageParams } from './posts'
import type { CursorPage, DMConversation, DMMessage } from './types'

/** GET /dm/unread-count：私信未读总数（前端轮询，独立于通知） */
export function fetchDMUnreadCount(): Promise<{ unread_count: number }> {
  return apiRequest('/dm/unread-count')
}

/** GET /dm/conversations：会话列表（未隐藏且有消息），按最后消息倒序 */
export function listConversations(params: PageParams = {}): Promise<CursorPage<DMConversation>> {
  return apiRequest('/dm/conversations', { query: { ...params } })
}

/** GET /dm/conversations/:id：会话详情 */
export function getConversation(id: string): Promise<{ conversation: DMConversation }> {
  return apiRequest(`/dm/conversations/${id}`)
}

/** POST /dm/conversations：创建或返回与 username 的既有会话（幂等） */
export function createConversation(username: string): Promise<{ conversation: DMConversation }> {
  return apiRequest('/dm/conversations', { method: 'POST', body: { username } })
}

/** GET /dm/conversations/:id/messages：翻旧消息（cursor，id 倒序） */
export function listMessages(
  conversationId: string,
  params: PageParams = {},
): Promise<CursorPage<DMMessage>> {
  return apiRequest(`/dm/conversations/${conversationId}/messages`, { query: { ...params } })
}

/** GET /dm/conversations/:id/messages?after_id=：增量拉取新消息（id 升序） */
export function listNewMessages(
  conversationId: string,
  afterId: string,
): Promise<CursorPage<DMMessage>> {
  return apiRequest(`/dm/conversations/${conversationId}/messages`, {
    query: { after_id: afterId },
  })
}

/** POST /dm/conversations/:id/messages：发送纯文本消息 */
export function sendMessage(
  conversationId: string,
  input: { content_format: 'richtext'; content_source: string },
): Promise<{ message: DMMessage }> {
  return apiRequest(`/dm/conversations/${conversationId}/messages`, {
    method: 'POST',
    body: input,
  })
}

/** POST /dm/conversations/:id/read：会话内自己的未读全部已读（幂等） */
export function markConversationRead(conversationId: string): Promise<{ updated: number }> {
  return apiRequest(`/dm/conversations/${conversationId}/read`, { method: 'POST' })
}

/** POST /dm/conversations/:id/hide：删除自己的会话视图（仅对自己隐藏） */
export function hideConversation(conversationId: string): Promise<void> {
  return apiRequest(`/dm/conversations/${conversationId}/hide`, { method: 'POST' })
}

/** POST /dm/messages/:id/recall：撤回自己的消息（默认 2 分钟内，幂等） */
export function recallMessage(messageId: string): Promise<{ message: DMMessage }> {
  return apiRequest(`/dm/messages/${messageId}/recall`, { method: 'POST' })
}
