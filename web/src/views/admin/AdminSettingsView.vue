<script setup lang="ts">
// A-18 系统设置：注册开关、上传/请求大小、存储配额、发帖/上传限频、
// 临时附件保留期与调用记录截断/保留期；保存后立即读取后端返回值刷新表单。
import { computed, onMounted, reactive, ref } from 'vue'
import { getAdminSettings, updateAdminSettings } from '../../api/admin'
import type { AdminSettings } from '../../api/admin'
import { setGlobalAI } from '../../api/aiAdmin'
import { errorMessage, fieldErrors } from '../../api/client'
import AlertNote from '../../components/AlertNote.vue'
import FormField from '../../components/FormField.vue'
import { siteState } from '../../stores/site'

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

onMounted(() => void load())
</script>

<template>
  <div class="stack">
    <p v-if="loading" class="card card--pad loading-row"><span class="spinner" aria-hidden="true"></span> 加载中…</p>

    <template v-else-if="loadError">
      <AlertNote kind="error">{{ loadError }}</AlertNote>
      <button class="btn btn--secondary btn--sm self-start" type="button" @click="load">重试</button>
    </template>

    <template v-else>
      <form class="stack" novalidate @submit.prevent="save">
        <section class="card card--pad">
          <h2 class="page-title">注册开关</h2>
          <p class="page-desc">关闭后新用户无法注册，注册页与导航入口同步关闭；已注册账号不受影响。</p>

          <label class="switch">
            <input v-model="form.registration_enabled" type="checkbox" :disabled="saving" />
            <span>{{ form.registration_enabled ? '当前开放注册' : '当前关闭注册' }}</span>
            <span class="badge" :class="form.registration_enabled ? '' : 'badge--danger'">
              {{ form.registration_enabled ? '开放' : '关闭' }}
            </span>
          </label>
        </section>

        <section class="card card--pad">
          <h2 class="page-title">上传与请求大小</h2>
          <p class="page-desc">保存后立即生效；上传请求超过单请求上限会被后端拒绝。</p>

          <div class="form-grid">
            <FormField
              label="单文件上传上限（字节）"
              input-id="settings-max-upload"
              :error="fieldErrorsMap.max_upload_bytes"
              :hint="mibHint(form.max_upload_bytes)"
            >
              <input
                id="settings-max-upload"
                v-model.number="form.max_upload_bytes"
                class="input"
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
                class="input"
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
                class="input"
                type="number"
                min="1"
                step="1"
                name="storage_quota_bytes"
                :disabled="saving"
              />
            </FormField>
          </div>
        </section>

        <section class="card card--pad">
          <h2 class="page-title">发帖与上传限频</h2>
          <p class="page-desc">按固定窗口统计；次数与窗口同时生效，修改后下一次请求即使用新配置。</p>

          <div class="form-grid">
            <FormField
              label="发帖限频次数"
              input-id="settings-post-limit"
              :error="fieldErrorsMap.post_rate_limit"
              hint="每个窗口允许的发布次数"
            >
              <input
                id="settings-post-limit"
                v-model.number="form.post_rate_limit"
                class="input"
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
                class="input"
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
                class="input"
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
                class="input"
                type="number"
                min="1"
                step="1"
                name="upload_rate_window_seconds"
                :disabled="saving"
              />
            </FormField>
          </div>
        </section>

        <section class="card card--pad">
          <h2 class="page-title">附件与调用记录</h2>
          <p class="page-desc">临时附件超过保留期后由后台清理任务标记失效；调用记录超出截断上限的部分不保存。</p>

          <div class="form-grid">
            <FormField
              label="临时附件保留期（小时）"
              input-id="settings-temp-retention"
              :error="fieldErrorsMap.temp_attachment_retention_hours"
              hint="单位：小时；至少为 1 小时"
            >
              <input
                id="settings-temp-retention"
                v-model.number="form.temp_attachment_retention_hours"
                class="input"
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
                class="input"
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
                class="input"
                type="number"
                min="0"
                step="1"
                name="call_log_retention_days"
                :disabled="saving"
              />
            </FormField>
          </div>
        </section>

        <section class="card card--pad">
          <AlertNote v-if="saved" kind="success">设置已保存并即时生效。</AlertNote>
          <AlertNote v-if="saveError" kind="error">{{ saveError }}</AlertNote>

          <div class="actions">
            <button class="btn btn--primary" type="submit" :disabled="!dirty || saving">
              <span v-if="saving" class="spinner" aria-hidden="true"></span>
              {{ saving ? '保存中…' : '保存设置' }}
            </button>
            <button class="btn btn--secondary" type="button" :disabled="!dirty || saving" @click="resetForm">
              撤销更改
            </button>
            <span v-if="dirty" class="muted dirty-hint">有未保存的修改</span>
          </div>
        </section>
      </form>

      <section class="card card--pad">
        <h2 class="page-title">全局 AI 自动行为</h2>
        <p class="page-desc">
          全局停止后所有 AI 立即停止产生新动作，并取消所有待执行的 AI 任务；已发布内容与记忆不受影响。
        </p>

        <AlertNote v-if="aiNotice" kind="success">{{ aiNotice }}</AlertNote>
        <AlertNote v-if="aiError" kind="error">{{ aiError }}</AlertNote>

        <label class="switch">
          <input :checked="form.ai_global_enabled" type="checkbox" :disabled="aiSaving" @change="onToggleAI" />
          <span>{{ form.ai_global_enabled ? '全局自动行为已开启' : '全局自动行为已停止' }}</span>
          <span class="badge" :class="form.ai_global_enabled ? '' : 'badge--danger'">
            {{ form.ai_global_enabled ? '运行中' : '已停止' }}
          </span>
        </label>

        <p v-if="aiSaving" class="loading-row">
          <span class="spinner" aria-hidden="true"></span> 正在提交全局开关…
        </p>

        <p class="muted hint">切换立即生效并写入审计日志；全局停止会取消所有待执行的 AI 任务。</p>
      </section>
    </template>
  </div>
</template>

<style scoped>
.self-start {
  align-self: flex-start;
}
.actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}
.dirty-hint {
  margin: 0;
}
.form-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 0 16px;
  margin-top: 12px;
}
.hint {
  margin: 12px 0 0;
}
</style>
