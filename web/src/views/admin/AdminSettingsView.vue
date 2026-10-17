<script setup lang="ts">
// A-18 系统设置：注册开关、全局网页搜索、上传/请求大小、存储配额、
// 发帖/上传限频、临时附件保留期与调用记录截断/保留期；保存后立即刷新表单。
import { computed, onMounted, reactive, ref } from 'vue'
import {
  getAdminSettings,
  getWebSearchSettings,
  updateAdminSettings,
  updateWebSearchSettings,
} from '../../api/admin'
import type { AdminSettings, WebSearchSettings, WebSearchSettingsInput } from '../../api/admin'
import { setGlobalAI } from '../../api/aiAdmin'
import { errorMessage, fieldErrors } from '../../api/client'
import AlertNote from '../../components/AlertNote.vue'
import FormField from '../../components/FormField.vue'
import { siteState } from '../../stores/site'
import { ui } from '../../styles/ui'

function defaultSettings(): AdminSettings {
  return {
    registration_enabled: true,
    ai_global_enabled: false,
    max_upload_bytes: 100 * 1024 * 1024,
    max_request_bytes: 101 * 1024 * 1024,
    storage_quota_bytes: 5 * 1024 * 1024 * 1024,
    post_rate_limit: 30,
    post_rate_window_seconds: 600,
    upload_rate_limit: 60,
    upload_rate_window_seconds: 600,
    temp_attachment_retention_hours: 168,
    call_log_max_bytes: 65536,
    call_log_retention_days: 90,
  }
}

/** 后端字段级错误键 → 表单字段名（internal/settings.Key*） */
const SETTING_ERROR_KEYS: Record<string, keyof AdminSettings> = {
  'upload.max_file_bytes': 'max_upload_bytes',
  'upload.max_request_bytes': 'max_request_bytes',
  'storage.quota_bytes': 'storage_quota_bytes',
  'post.rate_limit': 'post_rate_limit',
  'post.rate_window_seconds': 'post_rate_window_seconds',
  'upload.rate_limit': 'upload_rate_limit',
  'upload.rate_window_seconds': 'upload_rate_window_seconds',
  'media.temp_attachment_retention_hours': 'temp_attachment_retention_hours',
  'ai.call_log_max_bytes': 'call_log_max_bytes',
  'ai.call_log_retention_days': 'call_log_retention_days',
}

const form = reactive<AdminSettings>(defaultSettings())
const original = ref<AdminSettings | null>(null)

const loading = ref(true)
const loadError = ref('')
const saving = ref(false)
const saved = ref(false)
const saveError = ref('')
const fieldErrorsMap = ref<Record<string, string>>({})

function cloneSettings(data: AdminSettings): AdminSettings {
  return { ...data }
}

const dirty = computed(() => {
  if (!original.value) return false
  const keys = Object.keys(form) as (keyof AdminSettings)[]
  return keys.some((key) => form[key] !== original.value?.[key])
})

async function load(): Promise<void> {
  loading.value = true
  loadError.value = ''
  saved.value = false
  try {
    const data = await getAdminSettings()
    Object.assign(form, data)
    original.value = cloneSettings(data)
    // 注册开关变化后同步导航（隐藏/显示注册入口）
    siteState.registrationEnabled = data.registration_enabled
  } catch (error) {
    loadError.value = errorMessage(error)
  } finally {
    loading.value = false
  }
}

// ---------- 全局网页搜索（独立密钥） ----------
const webSearchForm = reactive({
  base_url: 'https://api.deepseek.com',
  api_key: '',
})
const webSearchState = ref<WebSearchSettings | null>(null)
const webSearchLoading = ref(true)
const webSearchLoadError = ref('')
const webSearchSaving = ref(false)
const webSearchSaved = ref(false)
const webSearchError = ref('')
const webSearchFieldErrors = ref<Record<string, string>>({})

async function loadWebSearch(): Promise<void> {
  webSearchLoading.value = true
  webSearchLoadError.value = ''
  try {
    const data = await getWebSearchSettings()
    webSearchState.value = data
    webSearchForm.base_url = data.base_url
    webSearchForm.api_key = ''
  } catch (error) {
    webSearchLoadError.value = errorMessage(error)
  } finally {
    webSearchLoading.value = false
  }
}

function validateWebSearchBaseURL(value: string): string {
  if (!value) return '请输入搜索 Base URL'
  try {
    const parsed = new URL(value)
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
      return '搜索 Base URL 只支持 http 或 https'
    }
    if (parsed.username || parsed.password || parsed.search || parsed.hash) {
      return '搜索 Base URL 不能包含凭据、查询参数或片段'
    }
  } catch {
    return '请输入有效的绝对地址'
  }
  return ''
}

async function saveWebSearch(): Promise<void> {
  webSearchError.value = ''
  webSearchSaved.value = false
  const details: Record<string, string> = {}
  const baseURL = webSearchForm.base_url.trim()
  const baseError = validateWebSearchBaseURL(baseURL)
  if (baseError) details.base_url = baseError
  webSearchFieldErrors.value = details
  if (Object.keys(details).length > 0) return

  const payload: WebSearchSettingsInput = { base_url: baseURL }
  const apiKey = webSearchForm.api_key.trim()
  if (apiKey) payload.api_key = apiKey

  webSearchSaving.value = true
  try {
    const data = await updateWebSearchSettings(payload)
    webSearchState.value = data
    webSearchForm.base_url = data.base_url
    webSearchForm.api_key = ''
    webSearchSaved.value = true
  } catch (error) {
    const fields = fieldErrors(error)
    if (fields.settings) {
      webSearchError.value = fields.settings
      delete fields.settings
    }
    if (Object.keys(fields).length > 0) webSearchFieldErrors.value = fields
    else if (!webSearchError.value) webSearchError.value = errorMessage(error)
  } finally {
    webSearchSaving.value = false
  }
}

async function clearWebSearchKey(): Promise<void> {
  if (!webSearchState.value?.configured) return
  if (!window.confirm('确认清除全局网页搜索密钥？所有开启联网搜索的 AI 将立即降级为普通决策。')) return
  webSearchSaving.value = true
  webSearchError.value = ''
  webSearchSaved.value = false
  webSearchFieldErrors.value = {}
  try {
    const data = await updateWebSearchSettings({ clear_api_key: true })
    webSearchState.value = data
    // 清除密钥不应覆盖用户尚未保存的 Base URL 编辑。
    if (!webSearchForm.base_url.trim()) webSearchForm.base_url = data.base_url
    webSearchForm.api_key = ''
    webSearchSaved.value = true
  } catch (error) {
    webSearchError.value = errorMessage(error)
  } finally {
    webSearchSaving.value = false
  }
}

function positive(key: keyof AdminSettings, label: string, target: Record<string, string>): void {
  const value = Number(form[key])
  if (!Number.isInteger(value) || value <= 0) target[key] = `${label}必须为正整数`
}

function validate(): Record<string, string> {
  const next: Record<string, string> = {}
  positive('max_upload_bytes', '单文件上限', next)
  positive('max_request_bytes', '单请求上限', next)
  positive('storage_quota_bytes', '存储配额', next)
  positive('post_rate_limit', '发帖限频次数', next)
  positive('post_rate_window_seconds', '发帖限频窗口', next)
  positive('upload_rate_limit', '上传限频次数', next)
  positive('upload_rate_window_seconds', '上传限频窗口', next)
  positive('temp_attachment_retention_hours', '临时附件保留期', next)
  positive('call_log_max_bytes', '调用记录截断上限', next)
  if (Number(form.max_request_bytes) < Number(form.max_upload_bytes)) {
    next.max_request_bytes = '单请求上限不能小于单文件上限'
  }
  if (!Number.isInteger(Number(form.call_log_retention_days)) || Number(form.call_log_retention_days) < 0) {
    next.call_log_retention_days = '调用记录保留期不能为负数'
  }
  return next
}

function normalized(): Omit<AdminSettings, 'ai_global_enabled'> {
  return {
    registration_enabled: form.registration_enabled,
    max_upload_bytes: Number(form.max_upload_bytes),
    max_request_bytes: Number(form.max_request_bytes),
    storage_quota_bytes: Number(form.storage_quota_bytes),
    post_rate_limit: Number(form.post_rate_limit),
    post_rate_window_seconds: Number(form.post_rate_window_seconds),
    upload_rate_limit: Number(form.upload_rate_limit),
    upload_rate_window_seconds: Number(form.upload_rate_window_seconds),
    temp_attachment_retention_hours: Number(form.temp_attachment_retention_hours),
    call_log_max_bytes: Number(form.call_log_max_bytes),
    call_log_retention_days: Number(form.call_log_retention_days),
  }
}

function mapFieldErrors(source: Record<string, string>): Record<string, string> {
  const mapped: Record<string, string> = {}
  for (const [key, message] of Object.entries(source)) {
    // 后端 details 已映射为请求字段名；同时兼容 internal/settings 的原始键名
    const field = SETTING_ERROR_KEYS[key] ?? (key in form ? (key as keyof AdminSettings) : undefined)
    if (field) mapped[field] = message
  }
  return mapped
}

async function save(): Promise<void> {
  saveError.value = ''
  saved.value = false
  const errors = validate()
  fieldErrorsMap.value = errors
  if (Object.keys(errors).length > 0) return

  saving.value = true
  try {
    const data = await updateAdminSettings(normalized())
    Object.assign(form, data)
    original.value = cloneSettings(data)
    siteState.registrationEnabled = data.registration_enabled
    saved.value = true
  } catch (error) {
    const fields = mapFieldErrors(fieldErrors(error))
    if (Object.keys(fields).length > 0) {
      fieldErrorsMap.value = fields
    } else {
      saveError.value = errorMessage(error)
    }
  } finally {
    saving.value = false
  }
}

function resetForm(): void {
  if (!original.value) return
  Object.assign(form, original.value)
  fieldErrorsMap.value = {}
  saved.value = false
  saveError.value = ''
}

// ---------- 全局 AI 自动行为开关 ----------
// 立即生效：开启走 global-start，关闭走 global-stop（会取消所有待执行的 AI 任务）。
const aiSaving = ref(false)
const aiError = ref('')
const aiNotice = ref('')

async function onToggleAI(event: Event): Promise<void> {
  const input = event.target as HTMLInputElement
  const next = input.checked
  const previous = form.ai_global_enabled

  if (
    !next &&
    !window.confirm('确认全局停止 AI 自动行为？全局停止会取消所有待执行的 AI 任务，所有 AI 立即停止产生新动作。')
  ) {
    input.checked = previous
    return
  }

  aiSaving.value = true
  aiError.value = ''
  aiNotice.value = ''
  try {
    const data = await setGlobalAI(next)
    form.ai_global_enabled = data.ai_global_enabled
    if (original.value) original.value.ai_global_enabled = data.ai_global_enabled
    input.checked = data.ai_global_enabled
    aiNotice.value = data.ai_global_enabled
      ? '已恢复全局 AI 自动行为。'
      : '已全局停止 AI 自动行为，所有待执行的 AI 任务已取消。'
  } catch (error) {
    form.ai_global_enabled = previous
    input.checked = previous
    aiError.value = errorMessage(error)
  } finally {
    aiSaving.value = false
  }
}

// ---------- 单位提示 ----------
function mibHint(bytes: number, detail?: string): string {
  const value = Number(bytes)
  const text = Number.isFinite(value) && value > 0 ? `约 ${(value / 1024 / 1024).toFixed(2)} MiB` : '单位：字节'
  return detail ? `${text}；${detail}` : text
}

function kibHint(bytes: number): string {
  const value = Number(bytes)
  return Number.isFinite(value) && value > 0 ? `约 ${(value / 1024).toFixed(1)} KiB` : '单位：字节'
}

function windowHint(seconds: number): string {
  const value = Number(seconds)
  return Number.isFinite(value) && value > 0 ? `单位：秒（约 ${(value / 60).toFixed(1)} 分钟）` : '单位：秒'
}

onMounted(() => {
  void load()
  void loadWebSearch()
})
</script>

<template>
  <div :class="ui.stack">
    <p v-if="loading" :class="[ui.card, ui.cardPad, ui.loadingRow]"><span :class="ui.spinner" aria-hidden="true"></span> 加载中…</p>

    <template v-else-if="loadError">
      <AlertNote kind="error">{{ loadError }}</AlertNote>
      <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'self-start']" type="button" @click="load">重试</button>
    </template>

    <template v-else>
      <form :class="ui.stack" novalidate @submit.prevent="save">
        <section :class="[ui.card, ui.cardPad]">
          <h2 :class="ui.pageTitle">注册开关</h2>
          <p :class="ui.pageDesc">关闭后新用户无法注册，注册页与导航入口同步关闭；已注册账号不受影响。</p>

          <label :class="ui.switch">
            <input v-model="form.registration_enabled" :class="ui.switchInput" type="checkbox" :disabled="saving" />
            <span>{{ form.registration_enabled ? '当前开放注册' : '当前关闭注册' }}</span>
            <span :class="[ui.badge, form.registration_enabled ? '' : ui.badgeDanger]">
              {{ form.registration_enabled ? '开放' : '关闭' }}
            </span>
          </label>
        </section>

        <section :class="[ui.card, ui.cardPad]">
          <h2 :class="ui.pageTitle">上传与请求大小</h2>
          <p :class="ui.pageDesc">保存后立即生效；上传请求超过单请求上限会被后端拒绝。</p>

          <div class="mt-3 grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-x-4">
            <FormField
              label="单文件上传上限（字节）"
              input-id="settings-max-upload"
              :error="fieldErrorsMap.max_upload_bytes"
              :hint="mibHint(form.max_upload_bytes)"
            >
              <input
                id="settings-max-upload"
                v-model.number="form.max_upload_bytes"
                :class="ui.input"
                type="number"
                min="1"
                step="1"
                name="max_upload_bytes"
                :disabled="saving"
              />
            </FormField>

            <FormField
              label="单请求上限（字节）"
              input-id="settings-max-request"
              :error="fieldErrorsMap.max_request_bytes"
              :hint="mibHint(form.max_request_bytes, '必须不小于单文件上限')"
            >
              <input
                id="settings-max-request"
                v-model.number="form.max_request_bytes"
                :class="ui.input"
                type="number"
                min="1"
                step="1"
                name="max_request_bytes"
                :disabled="saving"
              />
            </FormField>

            <FormField
              label="每用户存储配额（字节）"
              input-id="settings-storage-quota"
              :error="fieldErrorsMap.storage_quota_bytes"
              :hint="mibHint(form.storage_quota_bytes)"
            >
              <input
                id="settings-storage-quota"
                v-model.number="form.storage_quota_bytes"
                :class="ui.input"
                type="number"
                min="1"
                step="1"
                name="storage_quota_bytes"
                :disabled="saving"
              />
            </FormField>
          </div>
        </section>

        <section :class="[ui.card, ui.cardPad]">
          <h2 :class="ui.pageTitle">发帖与上传限频</h2>
          <p :class="ui.pageDesc">按固定窗口统计；次数与窗口同时生效，修改后下一次请求即使用新配置。</p>

          <div class="mt-3 grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-x-4">
            <FormField
              label="发帖限频次数"
              input-id="settings-post-limit"
              :error="fieldErrorsMap.post_rate_limit"
              hint="每个窗口允许的发布次数"
            >
              <input
                id="settings-post-limit"
                v-model.number="form.post_rate_limit"
                :class="ui.input"
                type="number"
                min="1"
                step="1"
                name="post_rate_limit"
                :disabled="saving"
              />
            </FormField>

            <FormField
              label="发帖限频窗口（秒）"
              input-id="settings-post-window"
              :error="fieldErrorsMap.post_rate_window_seconds"
              :hint="windowHint(form.post_rate_window_seconds)"
            >
              <input
                id="settings-post-window"
                v-model.number="form.post_rate_window_seconds"
                :class="ui.input"
                type="number"
                min="1"
                step="1"
                name="post_rate_window_seconds"
                :disabled="saving"
              />
            </FormField>

            <FormField
              label="上传限频次数"
              input-id="settings-upload-limit"
              :error="fieldErrorsMap.upload_rate_limit"
              hint="每个窗口允许的上传次数"
            >
              <input
                id="settings-upload-limit"
                v-model.number="form.upload_rate_limit"
                :class="ui.input"
                type="number"
                min="1"
                step="1"
                name="upload_rate_limit"
                :disabled="saving"
              />
            </FormField>

            <FormField
              label="上传限频窗口（秒）"
              input-id="settings-upload-window"
              :error="fieldErrorsMap.upload_rate_window_seconds"
              :hint="windowHint(form.upload_rate_window_seconds)"
            >
              <input
                id="settings-upload-window"
                v-model.number="form.upload_rate_window_seconds"
                :class="ui.input"
                type="number"
                min="1"
                step="1"
                name="upload_rate_window_seconds"
                :disabled="saving"
              />
            </FormField>
          </div>
        </section>

        <section :class="[ui.card, ui.cardPad]">
          <h2 :class="ui.pageTitle">附件与调用记录</h2>
          <p :class="ui.pageDesc">临时附件超过保留期后由后台清理任务标记失效；调用记录超出截断上限的部分不保存。</p>

          <div class="mt-3 grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-x-4">
            <FormField
              label="临时附件保留期（小时）"
              input-id="settings-temp-retention"
              :error="fieldErrorsMap.temp_attachment_retention_hours"
              hint="单位：小时；至少为 1 小时"
            >
              <input
                id="settings-temp-retention"
                v-model.number="form.temp_attachment_retention_hours"
                :class="ui.input"
                type="number"
                min="1"
                step="1"
                name="temp_attachment_retention_hours"
                :disabled="saving"
              />
            </FormField>

            <FormField
              label="调用记录内容截断上限（字节）"
              input-id="settings-call-log-max"
              :error="fieldErrorsMap.call_log_max_bytes"
              :hint="kibHint(form.call_log_max_bytes)"
            >
              <input
                id="settings-call-log-max"
                v-model.number="form.call_log_max_bytes"
                :class="ui.input"
                type="number"
                min="1"
                step="1"
                name="call_log_max_bytes"
                :disabled="saving"
              />
            </FormField>

            <FormField
              label="调用记录保留期（天）"
              input-id="settings-call-log-retention"
              :error="fieldErrorsMap.call_log_retention_days"
              hint="单位：天；0 表示不按天数清理"
            >
              <input
                id="settings-call-log-retention"
                v-model.number="form.call_log_retention_days"
                :class="ui.input"
                type="number"
                min="0"
                step="1"
                name="call_log_retention_days"
                :disabled="saving"
              />
            </FormField>
          </div>
        </section>

        <section :class="[ui.card, ui.cardPad]">
          <AlertNote v-if="saved" kind="success">设置已保存并即时生效。</AlertNote>
          <AlertNote v-if="saveError" kind="error">{{ saveError }}</AlertNote>

          <div class="flex flex-wrap items-center gap-2">
            <button :class="[ui.button, ui.buttonPrimary]" type="submit" :disabled="!dirty || saving">
              <span v-if="saving" :class="[ui.spinner, ui.buttonSpinner]" aria-hidden="true"></span>
              {{ saving ? '保存中…' : '保存设置' }}
            </button>
            <button :class="[ui.button, ui.buttonSecondary]" type="button" :disabled="!dirty || saving" @click="resetForm">
              撤销更改
            </button>
            <span v-if="dirty" :class="[ui.muted, 'm-0']">有未保存的修改</span>
          </div>
        </section>
      </form>

      <section :class="[ui.card, ui.cardPad]">
        <h2 :class="ui.pageTitle">全局 AI 自动行为</h2>
        <p :class="ui.pageDesc">
          全局停止后所有 AI 立即停止产生新动作，并取消所有待执行的 AI 任务；已发布内容与记忆不受影响。
        </p>

        <AlertNote v-if="aiNotice" kind="success">{{ aiNotice }}</AlertNote>
        <AlertNote v-if="aiError" kind="error">{{ aiError }}</AlertNote>

        <label :class="ui.switch">
          <input :checked="form.ai_global_enabled" :class="ui.switchInput" type="checkbox" :disabled="aiSaving" @change="onToggleAI" />
          <span>{{ form.ai_global_enabled ? '全局自动行为已开启' : '全局自动行为已停止' }}</span>
          <span :class="[ui.badge, form.ai_global_enabled ? '' : ui.badgeDanger]">
            {{ form.ai_global_enabled ? '运行中' : '已停止' }}
          </span>
        </label>

        <p v-if="aiSaving" :class="ui.loadingRow">
          <span :class="ui.spinner" aria-hidden="true"></span> 正在提交全局开关…
        </p>

        <p :class="[ui.muted, 'm-0 mt-3']">切换立即生效并写入审计日志；全局停止会取消所有待执行的 AI 任务。</p>
      </section>

      <section :class="[ui.card, ui.cardPad]">
        <h2 :class="ui.pageTitle">网页搜索（全局）</h2>
        <p :class="ui.pageDesc">
          这里维护所有 AI 共用的独立搜索密钥，与各 AI 的模型接口预设无关。任意 OpenAI 兼容厂商的模型只要支持工具调用并在行为策略中开启联网搜索，都会使用此配置。
        </p>

        <p v-if="webSearchLoading" :class="ui.loadingRow">
          <span :class="ui.spinner" aria-hidden="true"></span> 加载搜索配置…
        </p>

        <template v-else-if="webSearchLoadError">
          <AlertNote kind="error">{{ webSearchLoadError }}</AlertNote>
          <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'self-start']" type="button" @click="loadWebSearch">
            重试
          </button>
        </template>

        <form v-else :class="ui.form" novalidate @submit.prevent="saveWebSearch">
          <AlertNote v-if="webSearchState && webSearchState.configured && !webSearchState.usable" kind="warning">
            搜索密钥已保存但当前不可用：请检查 Base URL 是否为空，或 ENCRYPTION_KEY 是否已更换。
          </AlertNote>
          <AlertNote v-else-if="webSearchState && !webSearchState.configured" kind="warning">
            尚未配置独立搜索密钥；即使 AI 开启联网搜索，也会降级为普通决策。
          </AlertNote>
          <AlertNote v-if="webSearchSaved" kind="success">网页搜索设置已保存并即时生效。</AlertNote>
          <AlertNote v-if="webSearchError" kind="error">{{ webSearchError }}</AlertNote>

          <div class="flex flex-wrap items-center gap-2">
            <span :class="[ui.badge, webSearchState?.usable ? '' : ui.badgeDanger]">
              {{ webSearchState?.usable ? `可用 ${webSearchState.api_key_masked}` : (webSearchState?.configured ? '已保存但不可用' : '未配置密钥') }}
            </span>
            <span :class="[ui.muted, 'm-0']">密钥使用 ENCRYPTION_KEY 加密保存，保存后不会回显。</span>
          </div>

          <FormField
            label="搜索 Base URL"
            input-id="settings-web-search-base"
            :error="webSearchFieldErrors.base_url"
            hint="默认 https://api.deepseek.com；可填写兼容 DeepSeek Search 的网关地址，服务器需能访问"
          >
            <input
              id="settings-web-search-base"
              v-model.trim="webSearchForm.base_url"
              :class="ui.input"
              type="url"
              name="web_search_base_url"
              placeholder="https://api.deepseek.com"
              :disabled="webSearchSaving"
            />
          </FormField>

          <FormField
            label="独立搜索 API Key"
            input-id="settings-web-search-key"
            :error="webSearchFieldErrors.api_key"
            :hint="webSearchState?.usable ? '留空表示保持当前密钥不变' : '请输入 DeepSeek 或其他兼容搜索服务的 API Key'"
          >
            <input
              id="settings-web-search-key"
              v-model="webSearchForm.api_key"
              :class="ui.input"
              type="password"
              name="web_search_api_key"
              autocomplete="new-password"
              :placeholder="webSearchState?.usable ? '留空保持不变' : '输入新的搜索密钥'"
              :disabled="webSearchSaving"
            />
          </FormField>

          <div class="flex flex-wrap items-center gap-2">
            <button :class="[ui.button, ui.buttonPrimary]" type="submit" :disabled="webSearchSaving">
              <span v-if="webSearchSaving" :class="[ui.spinner, ui.buttonSpinner]" aria-hidden="true"></span>
              {{ webSearchSaving ? '保存中…' : '保存搜索设置' }}
            </button>
            <button
              v-if="webSearchState?.configured"
              :class="[ui.button, ui.buttonSecondary]"
              type="button"
              :disabled="webSearchSaving"
              @click="clearWebSearchKey"
            >
              清除已保存密钥
            </button>
          </div>

          <p :class="[ui.muted, 'm-0']">
            搜索接口只读，不开放 Fetch 或任意 URL；搜索结果会被标记为不可信资料，不能改变动作权限或社区规则。
          </p>
        </form>
      </section>
    </template>
  </div>
</template>
