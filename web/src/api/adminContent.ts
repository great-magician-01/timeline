// 管理后台：内容审核（帖子、评论、附件）与审计日志（docs/API.md §12.1、§12.2）
import { apiRequest } from './client'
import type { AdminAttachment, AdminComment, AdminPost, CursorPage, UserBrief } from './types'
import type { PageParams } from './posts'

export interface AuditLog {
  id: string
  admin: { id: string; username: string; nickname: string }
  action_type: string
  target_type: string
  target_id?: string
  detail: Record<string, unknown>
  created_at: string
}

export function listAdminPosts(
  params: PageParams & { visibility?: string; status?: string; author?: string; q?: string } = {},
): Promise<CursorPage<AdminPost>> {
  return apiRequest('/admin/posts', { query: { ...params } })
}

export interface AdminPostVersion {
  id: string
  version_number: number
  visibility: string
  created_at: string
  content_html: string
}

export interface AdminPostAttachment {
  id: string
  kind: string
  original_filename: string
  status: string
  media_type?: string
  size_bytes?: number
}

export interface AdminPostComment {
  id: string
  author: UserBrief
  content_html: string
  status: 'published' | 'deleted'
  created_at: string
}

/** A-06 后台帖子详情：草稿正文、历史版本正文、评论上下文与附件 */
export interface AdminPostDetail {
  post: AdminPost
  versions: AdminPostVersion[]
  attachments: AdminPostAttachment[]
  comments: AdminPostComment[]
  comment_count: number
}

export function getAdminPost(id: string): Promise<AdminPostDetail> {
  return apiRequest(`/admin/posts/${id}`)
}

export function deleteAdminPost(id: string): Promise<void> {
  return apiRequest(`/admin/posts/${id}/delete`, { method: 'POST' })
}

export function listAdminComments(
  params: PageParams & { author?: string; q?: string } = {},
): Promise<CursorPage<AdminComment>> {
  return apiRequest('/admin/comments', { query: { ...params } })
}

export function deleteAdminComment(id: string): Promise<void> {
  return apiRequest(`/admin/comments/${id}/delete`, { method: 'POST' })
}

export interface AdminAttachmentFilters {
  uploader_id?: string
  post_id?: string
  purpose?: string
  status?: string
  q?: string
}

/** GET /admin/attachments：管理端附件列表（按上传者、帖子、用途、状态与关键词筛选） */
export function listAdminAttachments(
  params: PageParams & AdminAttachmentFilters = {},
): Promise<CursorPage<AdminAttachment>> {
  return apiRequest('/admin/attachments', { query: { ...params } })
}

/** POST /admin/attachments/:id/delete：逻辑删除并写审计 */
export function deleteAdminAttachment(id: string): Promise<void> {
  return apiRequest(`/admin/attachments/${id}/delete`, { method: 'POST' })
}

export function listAuditLogs(
  params: PageParams & { action_type?: string; admin?: string } = {},
): Promise<CursorPage<AuditLog>> {
  return apiRequest('/admin/audit-logs', { query: { ...params } })
}

/** 管理概览：用户、内容、任务与 AI 概况 */
export interface AdminOverview {
  users: { total: number; suspended: number; ai: number }
  content: { posts: number; comments: number; drafts: number }
  ai: { enabled: number; total: number; global_enabled: boolean }
  tasks: { pending: number; failed: number; paused: number }
}

export function getOverview(): Promise<AdminOverview> {
  return apiRequest('/admin/overview')
}
