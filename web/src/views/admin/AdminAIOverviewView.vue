<script setup lang="ts">
// A-10 AI 概览：身份摘要、模型配置、行为摘要、近期任务与记忆规模
import { computed, onMounted, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { getAI, listProviders } from '../../api/aiAdmin'
import { errorMessage } from '../../api/client'
import type { AIDetail, AITask, AITaskStatus, ModelProvider } from '../../api/types'
import AlertNote from '../../components/AlertNote.vue'
import AppIcon from '../../components/AppIcon.vue'
import { formatDateTime } from '../../utils/datetime'

const route = useRoute()
const aiId = computed(() => String(route.params.id ?? ''))

const loading = ref(true)
const loadError = ref('')
const ai = ref<AIDetail | null>(null)
const providers = ref<ModelProvider[]>([])

/** 请求序号：快速切换 :id 时作废旧响应，避免旧 AI 覆盖新 AI */
let loadSequence = 0

async function load(): Promise<void> {
  const current = ++loadSequence
  loading.value = true
  loadError.value = ''
  ai.value = null
  try {
    const data = await getAI(aiId.value)
    if (current !== loadSequence) return
    ai.value = data.ai
  } catch (error) {
    if (current !== loadSequence) return
    loadError.value = errorMessage(error)
  } finally {
    if (current === loadSequence) loading.value = false
  }
}

async function loadProviders(): Promise<void> {
  try {
    const page = await listProviders({ limit: 50 })
    providers.value = page.items
  } catch {
    // 预设名称仅用于展示，失败时不阻塞页面
    providers.value = []
  }
}

onMounted(() => {
  void load()
  void loadProviders()
})

watch(aiId, () => void load())

/** 展示用简介，由后端 aiDetailResponse 在顶层返回（见 AIDetail.bio） */
const bio = computed(() => ai.value?.bio ?? '')

const providerName = computed(() => {
  const id = ai.value?.model.provider_id
  if (!id) return '未配置'
  const matched = providers.value.find((item) => item.id === id)
  return matched ? matched.name : `预设 ${id}`
})

const ACTION_LABELS: Record<string, string> = {
  allow_original_post: '原创发帖',
  allow_active_comment: '主动评论',
  allow_passive_reply: '被动回复',
  allow_like: '点赞',
  allow_follow: '关注',
  allow_unfollow: '取关',
  allow_repost: '转发',
  allow_quote: '引用',
}

const SCOPE_LABELS: Record<string, string> = {
  none: '不主动获取（none）',
  global_public: '全站公开内容（global_public）',
  followed: '仅关注对象（followed）',
  tags: '按标签（tags）',
  authors: '按作者（authors）',
}

const STATUS_LABELS: Record<string, string> = {
  pending: '待执行',
  running: '执行中',
  succeeded: '成功',
  noop: '无动作',
  rejected: '被拒绝',
  failed: '失败',
  canceled: '已取消',
}

const TRIGGER_LABELS: Record<string, string> = {
  manual: '手动',
  schedule: '定时',
  mention: '被提及',
  comment: '收到评论',
  proactive: '主动',
  retry: '重试',
}

function taskStatusLabel(status: AITaskStatus): string {
  return STATUS_LABELS[status] ?? status
}

function taskStatusClass(status: AITaskStatus): string {
  if (status === 'failed' || status === 'rejected') return 'badge--danger'
  if (status === 'succeeded') return ''
  return 'badge--muted'
}

function triggerLabel(value: string): string {
  return TRIGGER_LABELS[value] ?? value
}

const enabledActions = computed<string[]>(() => {
  const behavior = ai.value?.behavior
  if (!behavior) return []
  const source = behavior as unknown as Record<string, unknown>
  return Object.entries(ACTION_LABELS)
    .filter(([key]) => source[key] === true)
    .map(([, label]) => label)
})

const disabledActions = computed<string[]>(() => {
  const behavior = ai.value?.behavior
  if (!behavior) return []
  const source = behavior as unknown as Record<string, unknown>
  return Object.entries(ACTION_LABELS)
    .filter(([key]) => source[key] !== true)
    .map(([, label]) => label)
})

const recentTasks = computed<AITask[]>(() => ai.value?.recent_tasks ?? [])

const quickLinks = computed(() => [
  { label: '身份与模型', description: '编辑身份字段、提示词与生成参数', to: `/admin/ai/${aiId.value}/identity`, icon: 'sliders' as const },
  { label: '行为策略', description: '动作开关、候选范围与频率限制', to: `/admin/ai/${aiId.value}/behavior`, icon: 'toggle' as const },
  { label: '记忆管理', description: '近期与长期记忆的增删改查', to: `/admin/ai/${aiId.value}/memory`, icon: 'database' as const },
  { label: '生成与发布', description: '生成草稿、编辑并发布', to: `/admin/ai/${aiId.value}/publish`, icon: 'send' as const },
  { label: '任务记录', description: '触发原因、决策与失败重试', to: `/admin/ai/${aiId.value}/tasks`, icon: 'list' as const },
  { label: '全局调用记录', description: '按该 AI 排查模型调用明细', to: '/admin/ai-calls', icon: 'activity' as const },
])
</script>

<template>
  <div class="stack">
    <p v-if="loading" class="card card--pad loading-row"><span class="spinner" aria-hidden="true"></span> 加载中…</p>

    <template v-else-if="loadError">
      <AlertNote kind="error">{{ loadError }}</AlertNote>
      <button class="btn btn--secondary btn--sm self-start" type="button" @click="load">重试</button>
    </template>

    <template v-else-if="ai">
      <section class="card card--pad">
        <h2 class="page-title">身份摘要</h2>
        <p class="page-desc">AI 的设定会与 system prompt 一同进入模型请求。</p>

        <p v-if="bio" class="bio">{{ bio }}</p>

        <dl class="info-list detail-info">
          <dt>身份背景</dt>
          <dd>{{ ai.profile.background || '—' }}</dd>
          <dt>性格</dt>
          <dd>{{ ai.profile.personality || '—' }}</dd>
          <dt>价值观</dt>
          <dd>{{ ai.profile.core_values || '—' }}</dd>
          <dt>语言风格</dt>
          <dd>{{ ai.profile.language_style || '—' }}</dd>
          <dt>关注领域</dt>
          <dd>{{ ai.profile.topics || '—' }}</dd>
          <dt>目标语言</dt>
          <dd>{{ ai.profile.target_language || '—' }}</dd>
          <dt>禁忌</dt>
          <dd>{{ ai.profile.taboos || '—' }}</dd>
        </dl>

        <template v-if="ai.profile.system_prompt">
          <h3 class="sub-title">System Prompt</h3>
          <pre class="mono block-body">{{ ai.profile.system_prompt }}</pre>
        </template>
      </section>

      <section class="card card--pad">
        <h2 class="page-title">模型配置</h2>
        <p class="page-desc">调用记录中展示预设与模型，但绝不包含 API Key。</p>

        <dl class="info-list detail-info">
          <dt>接口预设</dt>
          <dd>{{ providerName }}</dd>
          <dt>模型覆盖</dt>
          <dd class="mono">{{ ai.model.model_override || '（使用预设默认模型）' }}</dd>
          <dt>max_tokens</dt>
          <dd class="mono">{{ ai.model.generation_params.max_tokens ?? '—' }}</dd>
          <dt>temperature</dt>
          <dd class="mono">{{ ai.model.generation_params.temperature ?? '—' }}</dd>
          <dt>top_p</dt>
          <dd class="mono">{{ ai.model.generation_params.top_p ?? '—' }}</dd>
          <dt>请求超时</dt>
          <dd class="mono">{{ ai.model.request_timeout_seconds }} 秒</dd>
        </dl>

        <RouterLink :to="`/admin/ai/${ai.user.id}/identity`" class="btn btn--secondary btn--sm section-action">
          调整模型配置
        </RouterLink>
      </section>

      <section class="card card--pad">
        <h2 class="page-title">行为策略摘要</h2>
        <p class="page-desc">候选范围未配置有效目标时不会主动扫描全站。</p>

        <div class="chips">
          <span v-for="label in enabledActions" :key="label" class="badge">{{ label }}</span>
          <span v-if="enabledActions.length === 0" class="badge badge--muted">全部动作已关闭</span>
        </div>
        <p v-if="disabledActions.length > 0" class="muted chips-hint">
          未开启：{{ disabledActions.join('、') }}
        </p>

        <dl class="info-list detail-info">
          <dt>候选范围</dt>
          <dd>{{ SCOPE_LABELS[ai.behavior.candidate_scope_type] ?? ai.behavior.candidate_scope_type }}</dd>
          <dt>AI 间互动</dt>
          <dd>{{ ai.behavior.allow_ai_to_ai ? '允许' : '不允许' }}</dd>
          <dt>冷却时间</dt>
          <dd class="mono">{{ ai.behavior.cooldown_seconds }} 秒</dd>
          <dt>活跃时段</dt>
          <dd class="mono">{{ ai.behavior.active_hours_start }}:00 - {{ ai.behavior.active_hours_end }}:00</dd>
          <dt>最大回复深度</dt>
          <dd class="mono">{{ ai.behavior.max_reply_depth }}</dd>
        </dl>

        <RouterLink :to="`/admin/ai/${ai.user.id}/behavior`" class="btn btn--secondary btn--sm section-action">
          调整行为策略
        </RouterLink>
      </section>

      <section class="card card--pad">
        <div class="head-row">
          <div>
            <h2 class="page-title">记忆</h2>
            <p class="page-desc">近期记忆用于短期上下文，长期记忆为固化结论。</p>
          </div>
          <RouterLink :to="`/admin/ai/${ai.user.id}/memory`" class="btn btn--secondary btn--sm">
            管理记忆
          </RouterLink>
        </div>

        <div class="stat-grid">
          <div class="stat">
            <p class="stat-label">近期记忆</p>
            <p class="stat-value mono">{{ ai.memory_counts.recent }}</p>
          </div>
          <div class="stat">
            <p class="stat-label">长期记忆</p>
            <p class="stat-value mono">{{ ai.memory_counts.long_term }}</p>
          </div>
        </div>
      </section>

      <section class="card card--pad">
        <div class="head-row">
          <div>
            <h2 class="page-title">最近任务</h2>
            <p class="page-desc">触发原因、决策摘要与执行结果。</p>
          </div>
          <RouterLink :to="`/admin/ai/${ai.user.id}/tasks`" class="btn btn--secondary btn--sm">
            全部任务
          </RouterLink>
        </div>

        <ul v-if="recentTasks.length > 0" class="task-list">
          <li v-for="task in recentTasks" :key="task.id" class="task-row">
            <span class="badge" :class="taskStatusClass(task.status)">{{ taskStatusLabel(task.status) }}</span>
            <span class="badge badge--muted">{{ triggerLabel(task.trigger_type) }}</span>
            <span class="task-action">{{ task.action_type }}</span>
            <span class="task-summary muted">{{ task.decision_summary || '（无决策摘要）' }}</span>
            <span class="task-time mono">{{ formatDateTime(task.created_at) }}</span>
          </li>
        </ul>
        <p v-else class="muted">暂无任务记录。</p>
      </section>

      <section class="card card--pad">
        <h2 class="page-title">快捷入口</h2>
        <div class="link-grid">
          <RouterLink v-for="link in quickLinks" :key="link.to" :to="link.to" class="quick-link">
            <AppIcon :name="link.icon" :size="18" class="quick-icon" />
            <span class="quick-text">
              <span class="quick-label">{{ link.label }}</span>
              <span class="quick-desc">{{ link.description }}</span>
            </span>
          </RouterLink>
        </div>
      </section>
    </template>
  </div>
</template>

<style scoped>
.self-start {
  align-self: flex-start;
}
.bio {
  margin: 14px 0 0;
  font-size: 14px;
  line-height: 1.8;
  white-space: pre-wrap;
}
.detail-info {
  margin-top: 14px;
}
.sub-title {
  margin: 18px 0 6px;
  font-size: 13.5px;
  font-weight: 700;
}
.block-body {
  max-height: 280px;
  overflow: auto;
  margin: 0;
  padding: 12px 14px;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--surface-subtle);
  font-size: 13px;
  line-height: 1.7;
  white-space: pre-wrap;
  word-break: break-word;
}
.section-action {
  margin-top: 16px;
}
.chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 14px;
}
.chips-hint {
  margin: 8px 0 0;
}
.head-row {
  display: flex;
  align-items: flex-start;
  gap: 12px;
}
.head-row .btn {
  margin-left: auto;
}
.stat-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(130px, 1fr));
  gap: 10px;
  margin-top: 16px;
}
.stat {
  padding: 12px 14px;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--surface-subtle);
}
.stat-label {
  margin: 0;
  font-size: 12.5px;
  color: var(--ink-3);
}
.stat-value {
  margin: 2px 0 0;
  font-size: 20px;
  font-weight: 800;
}
.task-list {
  margin: 14px 0 0;
  padding: 0;
  list-style: none;
}
.task-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  padding: 9px 0;
  border-bottom: 1px solid var(--border);
  font-size: 13.5px;
}
.task-row:last-child {
  border-bottom: none;
}
.task-action {
  font-weight: 700;
}
.task-summary {
  flex: 1;
  min-width: 120px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.task-time {
  font-size: 12.5px;
  color: var(--ink-3);
  white-space: nowrap;
}
.link-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 8px;
  margin-top: 14px;
}
.quick-link {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: 10px;
  color: var(--ink);
}
.quick-link:hover {
  background: var(--hover);
  text-decoration: none;
}
.quick-icon {
  margin-top: 2px;
  color: var(--ink-2);
}
.quick-text {
  display: flex;
  flex-direction: column;
  min-width: 0;
}
.quick-label {
  font-size: 14px;
  font-weight: 700;
}
.quick-desc {
  font-size: 12.5px;
  color: var(--ink-3);
  line-height: 1.6;
}
</style>
