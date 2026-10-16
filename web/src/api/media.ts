// 附件上传与删除（multipart/form-data，docs/API.md 第 10 章）
// 上传使用 XHR 以便提供进度回调；鉴权与 CSRF 与其它接口一致。
import {
  API_BASE,
  ApiError,
  apiRequest,
  currentCsrfToken,
  notifyUnauthorized,
  refreshCsrfToken,
} from './client'
import type { Attachment } from './types'

export type UploadPurpose = 'post' | 'avatar'

interface ErrorPayload {
  error?: { code?: string; message?: string; details?: unknown }
}

function toApiError(status: number, payload: ErrorPayload | null): ApiError {
  const code = payload?.error?.code ?? `http_${status}`
  const message =
    payload?.error?.message ?? (status === 413 ? '文件超过大小限制' : '上传失败，请重试')
  return new ApiError(status, code, message, payload?.error?.details)
}

/** 上传单个附件；onProgress 接收 0-100 的百分比 */
export function uploadAttachment(
  file: File,
  purpose: UploadPurpose = 'post',
  onProgress?: (percent: number) => void,
): Promise<{ attachment: Attachment }> {
  return new Promise((resolve, reject) => {
    const send = (retry: boolean) => {
      const form = new FormData()
      form.append('file', file)
      form.append('purpose', purpose)

      const xhr = new XMLHttpRequest()
      xhr.open('POST', `${API_BASE}/attachments`)
      xhr.withCredentials = true
      xhr.setRequestHeader('X-CSRF-Token', currentCsrfToken() ?? '')
      xhr.upload.onprogress = (event) => {
        if (onProgress && event.lengthComputable) {
          onProgress(Math.round((event.loaded / event.total) * 100))
        }
      }
      xhr.onload = async () => {
        let payload: ErrorPayload | null = null
        try {
          payload = JSON.parse(xhr.responseText) as ErrorPayload
        } catch {
          payload = null
        }
        if (xhr.status >= 200 && xhr.status < 300) {
          resolve(payload as unknown as { attachment: Attachment })
          return
        }
        const error = toApiError(xhr.status, payload)
        // 与 apiRequest 一致：401 时触发注册的未授权回调（清空当前用户）
        if (error.status === 401) notifyUnauthorized()
        if (error.code === 'csrf_failed' && retry) {
          const token = await refreshCsrfToken()
          if (token) {
            send(false)
            return
          }
        }
        reject(error)
      }
      xhr.onerror = () =>
        reject(new ApiError(0, 'network_error', '网络连接失败，请确认服务可用后重试'))
      xhr.send(form)
    }

    send(true)
  })
}

/** 从草稿移除附件；已被有效内容或历史版本引用时仅解除关联 */
export function deleteAttachment(id: string): Promise<{ detached: boolean; deleted: boolean }> {
  return apiRequest(`/attachments/${id}/delete`, { method: 'POST' })
}

/** 更新头像：上传 purpose=avatar 的附件后由后端写回 avatar_url */
export async function updateAvatar(file: File): Promise<Attachment> {
  const result = await uploadAttachment(file, 'avatar')
  return result.attachment
}
