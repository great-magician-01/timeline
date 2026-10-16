// 管理后台：内容审核（帖子、评论、附件）与审计日志（docs/API.md §12.1、§12.2）
import { apiRequest } from './client'
import type { AdminComment, AdminPost, CursorPage } from './types'
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

export interface AdminPostDetail {
  post: AdminPost
  versions: { id: string; version_number: number; visibility: string; created_at: string }[]
  attachments: { id: string; kind: string; original_filename: string; status: string }[]
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
