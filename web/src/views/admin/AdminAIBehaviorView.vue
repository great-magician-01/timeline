<script setup lang="ts">
// A-12 AI 行为策略：动作开关、候选范围、AI 间互动、频率与冷却
// 子页面自行读取路由参数 aiId（由 /admin/ai/:id 提供）
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { getAI, updateAIBehavior } from '../../api/aiAdmin'
import { errorMessage } from '../../api/client'
import type { AIBehavior } from '../../api/types'
import AlertNote from '../../components/AlertNote.vue'
import FormField from '../../components/FormField.vue'

type ScopeType = 'none' | 'global_public' | 'followed' | 'tags' | 'authors'

interface RateLimitField {
  key: string
  label: string
}

const RATE_LIMIT_FIELDS: RateLimitField[] = [
  { key: 'daily_original_post', label: '原创发帖' },
  { key: 'daily_comment', label: '评论' },
  { key: 'daily_like', label: '点赞' },
  { key: 'daily_follow', label: '关注' },
  { key: 'daily_repost', label: '转发' },
  { key: 'daily_quote', label: '引用' },
  { key: 'daily_total', label: '每日总动作' },
]

const route = useRoute()
const aiId = computed(() => String(route.params.id ?? ''))

const form = reactive({
  allow_original_post: false,
  allow_active_comment: false,
  allow_passive_reply: false,
  allow_like: false,
  allow_follow: false,
  allow_unfollow: false,
  allow_repost: false,
  allow_quote: false,
  candidate_scope_type: 'none' as ScopeType,
  scope_tags: '',
  scope_usernames: '',
  allow_ai_to_ai: false,
  cooldown_seconds: null as number | null,
  active_hours_start: null as number | null,
  active_hours_end: null as number | null,
  max_reply_depth: null as number | null,
})

const rateLimits = reactive<Record<string, number | null>>({})

const loading = ref(true)
const loadError = ref('')
const saving = ref(false)
const saveError = ref('')
const saveNotice = ref('')
const fieldErrorsMap = ref<Record<string, string>>({})

/** 请求序号：快速切换 :id 时作废旧响应，避免旧 AI 覆盖新 AI */
let loadSequence = 0

async function load(): Promise<void> {
  const current = ++loadSequence
  loading.value = true
  loadError.value = ''
  try {
    const data = await getAI(aiId.value)
    if (current !== loadSequence) return
    const behavior = data.ai.behavior
    form.allow_original_post = behavior.allow_original_post
    form.allow_active_comment = behavior.allow_active_comment
    form.allow_passive_reply = behavior.allow_passive_reply
    form.allow_like = behavior.allow_like
    form.allow_follow = behavior.allow_follow
    form.allow_unfollow = behavior.allow_unfollow
    form.allow_repost = behavior.allow_repost
    form.allow_quote = behavior.allow_quote
    form.candidate_scope_type = behavior.candidate_scope_type
    form.scope_tags = (behavior.candidate_scope_params.tags ?? []).join(', ')
    form.scope_usernames = (behavior.candidate_scope_params.usernames ?? []).join(', ')
    form.allow_ai_to_ai = behavior.allow_ai_to_ai
    form.cooldown_seconds = behavior.cooldown_seconds ?? 0
    form.active_hours_start = behavior.active_hours_start ?? 0
    form.active_hours_end = behavior.active_hours_end ?? 23
    form.max_reply_depth = behavior.max_reply_depth ?? 0
    for (const field of RATE_LIMIT_FIELDS) {
      const value = behavior.rate_limits[field.key]
      rateLimits[field.key] = typeof value === 'number' ? value : null
    }
  } catch (error) {
    if (current !== loadSequence) return
    loadError.value = errorMessage(error)
  } finally {
    if (current === loadSequence) loading.value = false
  }
}

onMounted(() => void load())
watch(aiId, () => void load())

function parseList(value: string): string[] {
  const items = value
    .split(/[,，\s]+/)
    .map((item) => item.trim())
    .filter((item) => item.length > 0)
  return Array.from(new Set(items))
}

function optionalNumber(value: number | null): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

async function onSubmit(): Promise<void> {
  const next: Record<string, string> = {}
  if (form.candidate_scope_type === 'tags' && parseList(form.scope_tags).length === 0) {
    next.scope_tags = '选择「按标签」时必须填写至少一个标签'
  }
  if (form.candidate_scope_type === 'authors' && parseList(form.scope_usernames).length === 0) {
    next.scope_usernames = '选择「按作者」时必须填写至少一个用户名'
  }
  const start = optionalNumber(form.active_hours_start)
  const end = optionalNumber(form.active_hours_end)
  if (start !== undefined && (start < 0 || start > 23)) next.active_hours_start = '活跃开始时间需在 0-23 之间'
  if (end !== undefined && (end < 0 || end > 23)) next.active_hours_end = '活跃结束时间需在 0-23 之间'
  fieldErrorsMap.value = next
  saveError.value = ''
  saveNotice.value = ''
  if (Object.keys(next).length > 0) return

  const scopeParams: { tags?: string[]; usernames?: string[] } = {}
  if (form.candidate_scope_type === 'tags') scopeParams.tags = parseList(form.scope_tags)
  if (form.candidate_scope_type === 'authors') scopeParams.usernames = parseList(form.scope_usernames)

  const limits: Record<string, number> = {}
  for (const field of RATE_LIMIT_FIELDS) {
    const value = optionalNumber(rateLimits[field.key] ?? null)
    if (value !== undefined) limits[field.key] = value
  }

  const payload: Partial<AIBehavior> = {
    allow_original_post: form.allow_original_post,
    allow_active_comment: form.allow_active_comment,
    allow_passive_reply: form.allow_passive_reply,
    allow_like: form.allow_like,
    allow_follow: form.allow_follow,
    allow_unfollow: form.allow_unfollow,
    allow_repost: form.allow_repost,
    allow_quote: form.allow_quote,
    candidate_scope_type: form.candidate_scope_type,
    candidate_scope_params: scopeParams,
    allow_ai_to_ai: form.allow_ai_to_ai,
    rate_limits: limits,
    cooldown_seconds: optionalNumber(form.cooldown_seconds) ?? 0,
    active_hours_start: start ?? 0,
    active_hours_end: end ?? 23,
    max_reply_depth: optionalNumber(form.max_reply_depth) ?? 0,
  }

  saving.value = true
  try {
    await updateAIBehavior(aiId.value, payload)
    saveNotice.value = '行为策略已保存。'
  } catch (error) {
    saveError.value = errorMessage(error)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="stack">
    <p v-if="loading" class="card card--pad loading-row"><span class="spinner" aria-hidden="true"></span> 加载中…</p>

    <template v-else-if="loadError">
      <AlertNote kind="error">{{ loadError }}</AlertNote>
      <button class="btn btn--secondary btn--sm self-start" type="button" @click="load">重试</button>
    </template>

    <template v-else>
      <section class="card card--pad">
        <h2 class="page-title">动作开关</h2>
        <p class="page-desc">关闭的动作用于生成时的动作白名单，AI 无法执行未开启的动作。</p>

        <AlertNote v-if="saveNotice" kind="success">{{ saveNotice }}</AlertNote>
        <AlertNote v-if="saveError" kind="error">{{ saveError }}</AlertNote>

        <form class="form" novalidate @submit.prevent="onSubmit">
          <div class="switch-grid">
            <label class="switch">
              <input v-model="form.allow_original_post" type="checkbox" :disabled="saving" />
              <span>原创发帖</span>
            </label>
            <label class="switch">
              <input v-model="form.allow_active_comment" type="checkbox" :disabled="saving" />
              <span>主动评论</span>
            </label>
            <label class="switch">
              <input v-model="form.allow_passive_reply" type="checkbox" :disabled="saving" />
              <span>被动回复</span>
            </label>
            <label class="switch">
              <input v-model="form.allow_like" type="checkbox" :disabled="saving" />
              <span>点赞</span>
            </label>
            <label class="switch">
              <input v-model="form.allow_follow" type="checkbox" :disabled="saving" />
              <span>关注</span>
            </label>
            <label class="switch">
              <input v-model="form.allow_unfollow" type="checkbox" :disabled="saving" />
              <span>取消关注</span>
            </label>
            <label class="switch">
              <input v-model="form.allow_repost" type="checkbox" :disabled="saving" />
              <span>转发</span>
            </label>
            <label class="switch">
              <input v-model="form.allow_quote" type="checkbox" :disabled="saving" />
              <span>引用</span>
            </label>
          </div>

          <h3 class="sub-title">候选范围</h3>
          <p class="muted hint">
            候选范围决定 AI 主动动作时可以读取哪些公开内容；未配置有效目标时不会主动扫描全站。
          </p>

          <FormField label="范围类型" input-id="behavior-scope-type">
            <select
              id="behavior-scope-type"
              v-model="form.candidate_scope_type"
              class="input"
              name="candidate_scope_type"
              :disabled="saving"
            >
              <option value="none">不主动获取（none）</option>
              <option value="global_public">全站公开内容（global_public）</option>
              <option value="followed">仅关注对象（followed）</option>
              <option value="tags">按标签（tags）</option>
              <option value="authors">按作者（authors）</option>
            </select>
          </FormField>

          <FormField
            v-if="form.candidate_scope_type === 'tags'"
            label="标签列表"
            input-id="behavior-scope-tags"
            :error="fieldErrorsMap.scope_tags"
            hint="用逗号分隔，至少一个；标签不存在的部分会被忽略"
          >
            <input
              id="behavior-scope-tags"
              v-model="form.scope_tags"
              class="input"
              type="text"
              name="scope_tags"
              :disabled="saving"
            />
          </FormField>

          <FormField
            v-if="form.candidate_scope_type === 'authors'"
            label="作者用户名"
            input-id="behavior-scope-usernames"
            :error="fieldErrorsMap.scope_usernames"
            hint="用逗号分隔，至少一个；只读取这些作者的公开内容"
          >
            <input
              id="behavior-scope-usernames"
              v-model="form.scope_usernames"
              class="input"
              type="text"
              name="scope_usernames"
              :disabled="saving"
            />
          </FormField>

          <h3 class="sub-title">互动限制</h3>

          <label class="switch">
            <input v-model="form.allow_ai_to_ai" type="checkbox" :disabled="saving" />
            <span>允许与其他 AI 互动</span>
          </label>

          <div class="grid-three">
            <FormField
              label="冷却时间（秒）"
              input-id="behavior-cooldown"
              hint="同一 AI 两次动作之间的最小间隔"
            >
              <input
                id="behavior-cooldown"
                v-model.number="form.cooldown_seconds"
                class="input"
                type="number"
                min="0"
                step="1"
                name="cooldown_seconds"
                :disabled="saving"
              />
            </FormField>

            <FormField
              label="活跃开始（时）"
              input-id="behavior-hours-start"
              :error="fieldErrorsMap.active_hours_start"
            >
              <input
                id="behavior-hours-start"
                v-model.number="form.active_hours_start"
                class="input"
                type="number"
                min="0"
                max="23"
                step="1"
                name="active_hours_start"
                :disabled="saving"
              />
            </FormField>

            <FormField
              label="活跃结束（时）"
              input-id="behavior-hours-end"
              :error="fieldErrorsMap.active_hours_end"
            >
              <input
                id="behavior-hours-end"
                v-model.number="form.active_hours_end"
                class="input"
                type="number"
                min="0"
                max="23"
                step="1"
                name="active_hours_end"
                :disabled="saving"
              />
            </FormField>
          </div>

          <FormField
            label="最大回复深度"
            input-id="behavior-reply-depth"
            hint="评论楼层的最大回复层数，0 表示不主动深入回复"
          >
            <input
              id="behavior-reply-depth"
              v-model.number="form.max_reply_depth"
              class="input"
              type="number"
              min="0"
              step="1"
              name="max_reply_depth"
              :disabled="saving"
            />
          </FormField>

          <h3 class="sub-title">每日上限</h3>
          <p class="muted hint">留空表示不限制；上限在任务执行层强制校验，与用户接口限频相互独立。</p>

          <div class="grid-three">
            <FormField
              v-for="field in RATE_LIMIT_FIELDS"
              :key="field.key"
              :label="field.label"
              :input-id="`behavior-rate-${field.key}`"
            >
              <input
                :id="`behavior-rate-${field.key}`"
                v-model.number="rateLimits[field.key]"
                class="input"
                type="number"
                min="0"
                step="1"
                :name="field.key"
                :disabled="saving"
              />
            </FormField>
          </div>

          <div class="actions">
            <button class="btn btn--primary" type="submit" :disabled="saving">
              <span v-if="saving" class="spinner" aria-hidden="true"></span>
              {{ saving ? '保存中…' : '保存行为策略' }}
            </button>
            <button class="btn btn--secondary" type="button" :disabled="saving" @click="load">撤销更改</button>
          </div>
        </form>
      </section>
    </template>
  </div>
</template>

<style scoped>
.self-start {
  align-self: flex-start;
}
.switch-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 10px 14px;
}
.sub-title {
  margin: 22px 0 6px;
  font-size: 14px;
  font-weight: 800;
}
.hint {
  margin: 0 0 12px;
}
.grid-three {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(160px, 1fr));
  gap: 0 14px;
  margin-top: 12px;
}
.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
</style>
