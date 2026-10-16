// 统一 API 客户端
// - 基础路径 /api/v1；错误结构 { error: { code, message, details? } }
// - 只使用 GET 与 POST（全局约束，不使用 PATCH/PUT/DELETE）
// - CSRF token 只保存在内存中（不写 localStorage）：启动时来自 /auth/me，游客来自 /auth/csrf
// - 所有非 GET 请求携带 X-CSRF-Token；遇到 csrf_failed 刷新 token 后重试一次
// - 401 时通知注册的回调（清空当前用户），由调用方决定后续跳转

/** 业务 API 基础路径；附件上传、衍生文件地址等绕过 apiRequest 的场景复用它拼接 URL */
export const API_BASE = '/api/v1'

let csrfToken: string | null = null
let unauthorizedHandler: (() => void) | null = null

/** 更新内存中的 CSRF token */
export function setCsrfToken(token: string | null): void {
  csrfToken = token
}

/** 注册 401 处理回调（通常由认证状态模块挂载，用于清空当前用户） */
export function setUnauthorizedHandler(handler: (() => void) | null): void {
  unauthorizedHandler = handler
}

/** 触发 401 处理回调：供绕过 apiRequest 的请求（如附件上传 XHR）复用同一套未授权逻辑 */
export function notifyUnauthorized(): void {
  unauthorizedHandler?.()
}

/** 读取内存中的 CSRF token（附件上传走 multipart，需要自行拼请求头） */
export function currentCsrfToken(): string | null {
  return csrfToken
}

/** 后端错误对象；details 结构随接口而定，字段级错误可用 fieldErrors() 提取 */
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly details: unknown

  constructor(status: number, code: string, message: string, details?: unknown) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.details = details
  }
}

export type QueryValue = string | number | boolean | undefined | null

export interface RequestOptions {
  // 全局约束：业务接口只允许 GET 与 POST（不使用 PATCH/PUT/DELETE）
  method?: 'GET' | 'POST'
  body?: unknown
  query?: Record<string, QueryValue>
  /** 内部使用：csrf_failed 时是否允许刷新 token 后重试（最多一次） */
  retryOnCsrf?: boolean
}

function buildUrl(path: string, query?: Record<string, QueryValue>): string {
  const url = `${API_BASE}${path}`
  if (!query) return url
  const search = new URLSearchParams()
  for (const [key, value] of Object.entries(query)) {
    if (value === undefined || value === null || value === '') continue
    search.append(key, String(value))
  }
  const qs = search.toString()
  return qs ? `${url}?${qs}` : url
}

interface ErrorPayload {
  error?: { code?: unknown; message?: unknown; details?: unknown }
}

async function readJson(response: Response): Promise<unknown> {
  if (response.status === 204) return null
  try {
    return await response.json()
  } catch {
    return null
  }
}

function fallbackMessage(status: number): string {
  if (status === 429) return '操作过于频繁，请稍后再试'
  if (status >= 500) return '服务暂时不可用，请稍后重试'
  return `请求失败（HTTP ${status}）`
}

function toApiError(response: Response, payload: unknown): ApiError {
  const source = (payload as ErrorPayload | null)?.error
  const code = typeof source?.code === 'string' ? source.code : `http_${response.status}`
  const message =
    typeof source?.message === 'string' && source.message ? source.message : fallbackMessage(response.status)
  return new ApiError(response.status, code, message, source?.details)
}

let csrfRefresh: Promise<string | null> | null = null

/** GET /auth/csrf：获取游客（或当前会话）的 CSRF token；失败返回 null，由调用方决定是否继续 */
export function refreshCsrfToken(): Promise<string | null> {
  if (!csrfRefresh) {
    csrfRefresh = (async () => {
      try {
        const data = await apiRequest<{ csrf_token: string }>('/auth/csrf', { retryOnCsrf: false })
        csrfToken = data.csrf_token
        return data.csrf_token
      } catch {
        return null
      } finally {
        csrfRefresh = null
      }
    })()
  }
  return csrfRefresh
}

export async function apiRequest<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const method = options.method ?? 'GET'
  const headers: Record<string, string> = { Accept: 'application/json' }
  if (method !== 'GET') {
    headers['X-CSRF-Token'] = csrfToken ?? ''
    if (options.body !== undefined) headers['Content-Type'] = 'application/json'
  }

  let response: Response
  try {
    response = await fetch(buildUrl(path, options.query), {
      method,
      headers,
      credentials: 'same-origin',
      body: options.body === undefined ? undefined : JSON.stringify(options.body),
    })
  } catch {
    throw new ApiError(0, 'network_error', '网络连接失败，请确认服务可用后重试')
  }

  const payload = await readJson(response)

  if (!response.ok) {
    const error = toApiError(response, payload)
    if (error.status === 401) notifyUnauthorized()
    if (error.status === 403 && error.code === 'csrf_failed' && method !== 'GET' && options.retryOnCsrf !== false) {
      const token = await refreshCsrfToken()
      if (token) return apiRequest<T>(path, { ...options, retryOnCsrf: false })
    }
    throw error
  }

  if (response.status === 204) return undefined as T
  if (payload === null) {
    // 例如被前端回退页（index.html）或代理错误页吞掉了接口路径
    throw new ApiError(response.status, 'invalid_response', '接口返回内容无法解析，请确认后端服务已部署并可访问')
  }

  return payload as T
}

/** 从 ApiError.details 提取字段级错误（兼容 details 直接是字段映射或包裹在 details.fields 中） */
export function fieldErrors(error: unknown): Record<string, string> {
  if (!(error instanceof ApiError)) return {}
  const details = error.details
  if (!details || typeof details !== 'object') return {}
  const container = details as Record<string, unknown>
  const source =
    container.fields && typeof container.fields === 'object'
      ? (container.fields as Record<string, unknown>)
      : container
  const result: Record<string, string> = {}
  for (const [key, value] of Object.entries(source)) {
    if (typeof value === 'string') {
      result[key] = value
    } else if (Array.isArray(value) && value.every((item) => typeof item === 'string')) {
      result[key] = value.join('；')
    }
  }
  return result
}

/** 统一提取可展示给用户的中文错误文案（优先使用后端 message） */
export function errorMessage(error: unknown): string {
  if (error instanceof ApiError) return error.message
  return '发生未知错误，请稍后重试'
}
