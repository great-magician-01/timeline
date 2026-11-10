// 个人网盘接口（docs/API.md §13）
// 文件最终公开级别在文件上；文件夹“统一设置”是批量操作，不做继承。
import { apiRequest } from './client'
import type { DriveFile, DriveFilePage, DriveFolder, DriveVisibility } from './types'

export interface DriveListParams {
  cursor?: string
  limit?: number
  /** 'root' 表示根目录；缺省表示全部文件 */
  folder_id?: string
  kind?: 'image' | 'video' | 'file'
  q?: string
}

/** 本人一级文件夹列表（含文章附件系统目录） */
export function listDriveFolders(): Promise<{ items: DriveFolder[] }> {
  return apiRequest('/drive/folders')
}

export function createDriveFolder(name: string): Promise<{ folder: DriveFolder }> {
  return apiRequest('/drive/folders', { method: 'POST', body: { name } })
}

export function renameDriveFolder(id: string, name: string): Promise<{ folder: DriveFolder }> {
  return apiRequest(`/drive/folders/${id}`, { method: 'POST', body: { name } })
}

export function setDriveFolderVisibility(
  id: string,
  visibility: DriveVisibility,
): Promise<{ visibility: DriveVisibility }> {
  return apiRequest(`/drive/folders/${id}/visibility`, { method: 'POST', body: { visibility } })
}

export function deleteDriveFolder(id: string): Promise<{ deleted: boolean }> {
  return apiRequest(`/drive/folders/${id}/delete`, { method: 'POST' })
}

/** 本人网盘文件列表；响应同时带容量 used_bytes/quota_bytes */
export function listDriveFiles(params: DriveListParams = {}): Promise<DriveFilePage> {
  return apiRequest('/drive/files', {
    query: {
      cursor: params.cursor,
      limit: params.limit,
      folder_id: params.folder_id,
      kind: params.kind,
      q: params.q,
    },
  })
}

/** 指定用户的公开文件列表；响应带 folders（仅包含有公开文件的目录） */
export function listPublicDriveFiles(username: string, params: DriveListParams = {}): Promise<DriveFilePage> {
  return apiRequest(`/users/${encodeURIComponent(username)}/drive/files`, {
    query: {
      cursor: params.cursor,
      limit: params.limit,
      folder_id: params.folder_id,
      kind: params.kind,
      q: params.q,
    },
  })
}

export function setDriveFileVisibility(
  id: string,
  visibility: DriveVisibility,
): Promise<{ visibility: DriveVisibility }> {
  return apiRequest(`/drive/files/${id}`, { method: 'POST', body: { visibility } })
}

/** 移动文件；null 表示移动到根目录，不改变公开级别 */
export function moveDriveFile(id: string, folderId: string | null): Promise<{ folder_id: string | null }> {
  return apiRequest(`/drive/files/${id}/move`, {
    method: 'POST',
    body: { folder_id: folderId },
  })
}

/** 删除网盘条目；仍被文章引用时文章附件继续可用 */
export function deleteDriveFile(id: string): Promise<{ deleted: boolean }> {
  return apiRequest(`/drive/files/${id}/delete`, { method: 'POST' })
}

/** 从指定文章工作区解除一条附件关联，不影响网盘与其他文章 */
export function detachPostAttachment(postId: string, attachmentId: string): Promise<{ detached: boolean }> {
  return apiRequest(`/posts/${postId}/attachments/${attachmentId}/detach`, { method: 'POST' })
}

/** 供网盘页/选择器共用的文件展示辅助 */
export function driveFilePreviewURL(file: DriveFile): string {
  return file.thumbnail_url || file.content_url
}
