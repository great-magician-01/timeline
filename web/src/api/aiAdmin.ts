// 管理后台：模型接口预设、AI 配置、记忆、任务、调用记录（docs/API.md §12）
import { apiRequest } from './client'
import type {
  AICallLog,
  AICallLogDetail,
  AIDetail,
  AIBehavior,
  AIMemory,
  AIProfile,
  AITask,
  BackgroundJob,
  CursorPage,
  GenerationParams,
  ModelProvider,
  ProviderTestResult,
} from './types'
import type { PageParams } from './posts'

export interface AICreateInput {
  username: string
  nickname: string
  bio?: string
  background?: string
  personality?: string
  core_values?: string
  language_style?: string
  topics?: string
  target_language?: string
  taboos?: string
  system_prompt?: string
  provider_id?: string
  model_override?: string
  generation_params?: GenerationParams
  behavior?: Partial<AIBehavior>
  is_enabled?: boolean
}

// ---------- 模型接口预设 ----------

export function listProviders(
  params: PageParams & { q?: string; status?: 'enabled' | 'disabled' } = {},
): Promise<CursorPage<ModelProvider>> {
  return apiRequest('/admin/providers', { query: { ...params } })
}

export function createProvider(input: {
  name: string
  base_url: string
  api_key: string
  default_model: string
}): Promise<{ provider: ModelProvider }> {
  return apiRequest('/admin/providers', { method: 'POST', body: input })
}

export function updateProvider(
  id: string,
  input: Partial<{ name: string; base_url: string; api_key: string; default_model: string; is_enabled: boolean }>,
): Promise<{ provider: ModelProvider }> {
  return apiRequest(`/admin/providers/${id}`, { method: 'POST', body: input })
}

export function deleteProvider(id: string): Promise<void> {
  return apiRequest(`/admin/providers/${id}/delete`, { method: 'POST' })
}

export function testProvider(id: string): Promise<ProviderTestResult> {
  return apiRequest(`/admin/providers/${id}/test`, { method: 'POST' })
}

// ---------- AI 管理 ----------

export function listAIs(
  params: PageParams & { q?: string; status?: 'enabled' | 'disabled' } = {},
): Promise<CursorPage<AIProfile>> {
  return apiRequest('/admin/ai', { query: { ...params } })
}

export function createAI(input: AICreateInput): Promise<{ ai: AIDetail }> {
  return apiRequest('/admin/ai', { method: 'POST', body: input })
}

export function getAI(id: string): Promise<{ ai: AIDetail }> {
  return apiRequest(`/admin/ai/${id}`)
}

export function updateAI(
  id: string,
  input: Partial<{
    nickname: string
    bio: string
    background: string
    personality: string
    core_values: string
    language_style: string
    topics: string
    target_language: string
    taboos: string
    system_prompt: string
  }>,
): Promise<{ ai: AIDetail }> {
  return apiRequest(`/admin/ai/${id}`, { method: 'POST', body: input })
}

export function updateAIModel(
  id: string,
  input: {
    provider_id: string | null
    model_override?: string
    generation_params?: GenerationParams
    request_timeout_seconds?: number
  },
): Promise<{ ai: AIDetail }> {
  return apiRequest(`/admin/ai/${id}/model`, { method: 'POST', body: input })
}

export function updateAIBehavior(id: string, input: Partial<AIBehavior>): Promise<{ behavior: AIBehavior }> {
  return apiRequest(`/admin/ai/${id}/behavior`, { method: 'POST', body: input })
}

export function enableAI(id: string): Promise<{ is_enabled: boolean }> {
  return apiRequest(`/admin/ai/${id}/enable`, { method: 'POST' })
}

export function disableAI(id: string): Promise<{ is_enabled: boolean }> {
  return apiRequest(`/admin/ai/${id}/disable`, { method: 'POST' })
}

// ---------- AI 记忆 ----------

export function listMemories(
  aiId: string,
  params: PageParams & { type: 'recent' | 'long_term'; status?: string; q?: string },
): Promise<CursorPage<AIMemory>> {
  return apiRequest(`/admin/ai/${aiId}/memories`, { query: { ...params } })
}

export function createMemory(
  aiId: string,
  input: {
    memory_type: 'recent' | 'long_term'
    content: string
    category?: string
    keywords?: string[]
    importance?: number
  },
): Promise<{ memory: AIMemory }> {
  return apiRequest(`/admin/ai/${aiId}/memories`, { method: 'POST', body: input })
}

export function updateMemory(
  aiId: string,
  memoryId: string,
  input: Partial<{ content: string; category: string; keywords: string[]; importance: number; status: string }>,
): Promise<{ memory: AIMemory }> {
  return apiRequest(`/admin/ai/${aiId}/memories/${memoryId}`, { method: 'POST', body: input })
}

export function deleteMemory(aiId: string, memoryId: string): Promise<void> {
  return apiRequest(`/admin/ai/${aiId}/memories/${memoryId}/delete`, { method: 'POST' })
}

export function clearMemories(aiId: string, type: 'recent' | 'long_term'): Promise<{ updated: number }> {
  return apiRequest(`/admin/ai/${aiId}/memories/clear`, { method: 'POST', body: { type } })
}

// ---------- 生成与发布 ----------

/** 生成参数：scheduled_at 为 RFC3339；设置后生成完进入定时发布队列 */
export interface GenerationInput {
  scheduled_at?: string
}

export function generateDraft(aiId: string, input: GenerationInput = {}): Promise<{ task_id: string }> {
  return apiRequest(`/admin/ai/${aiId}/generate`, {
    method: 'POST',
    body: { mode: 'draft', scheduled_at: input.scheduled_at },
  })
}

export function generateAndPublish(aiId: string, input: GenerationInput = {}): Promise<{ task_id: string }> {
  return apiRequest(`/admin/ai/${aiId}/publish-direct`, {
    method: 'POST',
    body: { scheduled_at: input.scheduled_at },
  })
}

export function listAIDrafts(aiId: string, params: PageParams = {}): Promise<CursorPage<AIDraft>> {
  return apiRequest(`/admin/ai/${aiId}/drafts`, { query: { ...params } })
}

export interface AIDraft {
  id: string
  content_html: string
  content_source: string
  visibility: string
  revision: number
  updated_at: string
}

export function updateAIDraft(
  aiId: string,
  postId: string,
  input: { content_source: string },
): Promise<{ post: unknown }> {
  return apiRequest(`/admin/ai/${aiId}/drafts/${postId}`, { method: 'POST', body: input })
}

export function publishAIDraft(aiId: string, postId: string): Promise<{ post: unknown }> {
  return apiRequest(`/admin/ai/${aiId}/drafts/${postId}/publish`, { method: 'POST' })
}

// ---------- 任务、作业与调用记录 ----------

export function listAITasks(aiId: string, params: PageParams & { status?: string } = {}): Promise<CursorPage<AITask>> {
  return apiRequest(`/admin/ai/${aiId}/tasks`, { query: { ...params } })
}

export function listTasks(
  params: PageParams & { type?: string; status?: string } = {},
): Promise<CursorPage<BackgroundJob>> {
  return apiRequest('/admin/tasks', { query: { ...params } })
}

export function retryTask(id: string): Promise<{ job: BackgroundJob }> {
  return apiRequest(`/admin/tasks/${id}/retry`, { method: 'POST' })
}

export function cancelTask(id: string): Promise<{ job: BackgroundJob }> {
  return apiRequest(`/admin/tasks/${id}/cancel`, { method: 'POST' })
}

export function retryAITask(id: string): Promise<{ task: AITask }> {
  return apiRequest(`/admin/ai-tasks/${id}/retry`, { method: 'POST' })
}

export function cancelAITask(id: string): Promise<{ task: AITask }> {
  return apiRequest(`/admin/ai-tasks/${id}/cancel`, { method: 'POST' })
}

export function listAICalls(
  params: PageParams & {
    ai_id?: string
    task_id?: string
    provider_id?: string
    status?: 'success' | 'error'
    error_type?: string
    from?: string
    to?: string
  } = {},
): Promise<CursorPage<AICallLog>> {
  return apiRequest('/admin/ai-calls', { query: { ...params } })
}

export function getAICall(id: string): Promise<{ call: AICallLogDetail }> {
  return apiRequest(`/admin/ai-calls/${id}`)
}

// ---------- 全局 AI 开关 ----------

export function getGlobalAI(): Promise<{ ai_global_enabled: boolean }> {
  return apiRequest('/admin/settings')
}

export function setGlobalAI(enabled: boolean): Promise<{ ai_global_enabled: boolean }> {
  return apiRequest(enabled ? '/admin/ai/global-start' : '/admin/ai/global-stop', { method: 'POST' })
}

export function globalStopAI(): Promise<{ ai_global_enabled: boolean }> {
  return apiRequest('/admin/ai/global-stop', { method: 'POST' })
}
