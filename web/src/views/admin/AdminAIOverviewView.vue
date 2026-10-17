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
import { ui } from '../../styles/ui'

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
  if (status === 'failed' || status === 'rejected') return ui.badgeDanger
  if (status === 'succeeded') return ''
  return ui.badgeMuted
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
  <div :class="ui.stack">
    <p v-if="loading" :class="[ui.card, ui.cardPad, ui.loadingRow]"><span :class="ui.spinner" aria-hidden="true"></span> 加载中…</p>

    <template v-else-if="loadError">
      <AlertNote kind="error">{{ loadError }}</AlertNote>
      <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'self-start']" type="button" @click="load">重试</button>
    </template>

    <template v-else-if="ai">
      <section :class="[ui.card, ui.cardPad]">
        <h2 :class="ui.pageTitle">身份摘要</h2>
        <p :class="ui.pageDesc">AI 的设定会与 system prompt 一同进入模型请求。</p>

        <p v-if="bio" class="m-0 mt-[14px] whitespace-pre-wrap text-[14px] leading-[1.8]">{{ bio }}</p>

        <dl :class="[ui.infoList, 'mt-[14px]']">
          <dt :class="ui.infoTerm">身份背景</dt>
          <dd :class="ui.infoDetails">{{ ai.profile.background || '—' }}</dd>
          <dt :class="ui.infoTerm">性格</dt>
          <dd :class="ui.infoDetails">{{ ai.profile.personality || '—' }}</dd>
          <dt :class="ui.infoTerm">价值观</dt>
          <dd :class="ui.infoDetails">{{ ai.profile.core_values || '—' }}</dd>
          <dt :class="ui.infoTerm">语言风格</dt>
          <dd :class="ui.infoDetails">{{ ai.profile.language_style || '—' }}</dd>
          <dt :class="ui.infoTerm">关注领域</dt>
          <dd :class="ui.infoDetails">{{ ai.profile.topics || '—' }}</dd>
          <dt :class="ui.infoTerm">目标语言</dt>
          <dd :class="ui.infoDetails">{{ ai.profile.target_language || '—' }}</dd>
          <dt :class="ui.infoTerm">禁忌</dt>
          <dd :class="ui.infoDetails">{{ ai.profile.taboos || '—' }}</dd>
        </dl>

        <template v-if="ai.profile.system_prompt">
          <h3 class="mx-0 mb-[6px] mt-[18px] text-[13.5px] font-bold">System Prompt</h3>
          <pre :class="[ui.mono, 'm-0 max-h-[280px] overflow-auto rounded-[10px] border border-solid border-border bg-surface-subtle px-[14px] py-3 text-[13px] leading-[1.7] whitespace-pre-wrap [word-break:break-word]']">{{ ai.profile.system_prompt }}</pre>
        </template>
      </section>

      <section :class="[ui.card, ui.cardPad]">
        <h2 :class="ui.pageTitle">模型配置</h2>
        <p :class="ui.pageDesc">调用记录中展示预设与模型，但绝不包含 API Key。</p>

        <dl :class="[ui.infoList, 'mt-[14px]']">
          <dt :class="ui.infoTerm">接口预设</dt>
          <dd :class="ui.infoDetails">{{ providerName }}</dd>
          <dt :class="ui.infoTerm">模型覆盖</dt>
          <dd :class="[ui.infoDetails, ui.mono]">{{ ai.model.model_override || '（使用预设默认模型）' }}</dd>
          <dt :class="ui.infoTerm">max_tokens</dt>
          <dd :class="[ui.infoDetails, ui.mono]">{{ ai.model.generation_params.max_tokens ?? '—' }}</dd>
          <dt :class="ui.infoTerm">temperature</dt>
          <dd :class="[ui.infoDetails, ui.mono]">{{ ai.model.generation_params.temperature ?? '—' }}</dd>
          <dt :class="ui.infoTerm">top_p</dt>
          <dd :class="[ui.infoDetails, ui.mono]">{{ ai.model.generation_params.top_p ?? '—' }}</dd>
          <dt :class="ui.infoTerm">请求超时</dt>
          <dd :class="[ui.infoDetails, ui.mono]">{{ ai.model.request_timeout_seconds }} 秒</dd>
        </dl>

        <RouterLink :to="`/admin/ai/${ai.user.id}/identity`" :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'mt-4']">
          调整模型配置
        </RouterLink>
      </section>

      <section :class="[ui.card, ui.cardPad]">
        <h2 :class="ui.pageTitle">行为策略摘要</h2>
        <p :class="ui.pageDesc">候选范围未配置有效目标时不会主动扫描全站。</p>

        <div class="mt-[14px] flex flex-wrap gap-[6px]">
          <span v-for="label in enabledActions" :key="label" :class="ui.badge">{{ label }}</span>
          <span v-if="enabledActions.length === 0" :class="[ui.badge, ui.badgeMuted]">全部动作已关闭</span>
        </div>
        <p v-if="disabledActions.length > 0" :class="[ui.muted, 'm-0 mt-2']">
          未开启：{{ disabledActions.join('、') }}
        </p>
        <p :class="[ui.muted, 'm-0 mt-2']">
          联网搜索：{{ ai.behavior.allow_web_search ? '允许' : '不允许' }}
        </p>

        <dl :class="[ui.infoList, 'mt-[14px]']">
          <dt :class="ui.infoTerm">候选范围</dt>
          <dd :class="ui.infoDetails">{{ SCOPE_LABELS[ai.behavior.candidate_scope_type] ?? ai.behavior.candidate_scope_type }}</dd>
          <dt :class="ui.infoTerm">AI 间互动</dt>
          <dd :class="ui.infoDetails">{{ ai.behavior.allow_ai_to_ai ? '允许' : '不允许' }}</dd>
          <dt :class="ui.infoTerm">冷却时间</dt>
          <dd :class="[ui.infoDetails, ui.mono]">{{ ai.behavior.cooldown_seconds }} 秒</dd>
          <dt :class="ui.infoTerm">活跃时段</dt>
          <dd :class="[ui.infoDetails, ui.mono]">{{ ai.behavior.active_hours_start }}:00 - {{ ai.behavior.active_hours_end }}:00</dd>
          <dt :class="ui.infoTerm">最大回复深度</dt>
          <dd :class="[ui.infoDetails, ui.mono]">{{ ai.behavior.max_reply_depth }}</dd>
        </dl>

        <RouterLink :to="`/admin/ai/${ai.user.id}/behavior`" :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'mt-4']">
          调整行为策略
        </RouterLink>
      </section>

      <section :class="[ui.card, ui.cardPad]">
        <div class="flex items-start gap-3">
          <div>
            <h2 :class="ui.pageTitle">记忆</h2>
            <p :class="ui.pageDesc">近期记忆用于短期上下文，长期记忆为固化结论。</p>
          </div>
          <RouterLink :to="`/admin/ai/${ai.user.id}/memory`" :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'ml-auto']">
            管理记忆
          </RouterLink>
        </div>

        <div class="mt-4 grid grid-cols-[repeat(auto-fit,minmax(130px,1fr))] gap-[10px]">
          <div class="rounded-[10px] border border-solid border-border bg-surface-subtle px-[14px] py-3">
            <p class="m-0 text-[12.5px] text-ink-3">近期记忆</p>
            <p :class="[ui.mono, 'm-0 mt-[2px] text-[20px] font-extrabold']">{{ ai.memory_counts.recent }}</p>
          </div>
          <div class="rounded-[10px] border border-solid border-border bg-surface-subtle px-[14px] py-3">
            <p class="m-0 text-[12.5px] text-ink-3">长期记忆</p>
            <p :class="[ui.mono, 'm-0 mt-[2px] text-[20px] font-extrabold']">{{ ai.memory_counts.long_term }}</p>
          </div>
        </div>
      </section>

      <section :class="[ui.card, ui.cardPad]">
        <div class="flex items-start gap-3">
          <div>
            <h2 :class="ui.pageTitle">最近任务</h2>
            <p :class="ui.pageDesc">触发原因、决策摘要与执行结果。</p>
          </div>
          <RouterLink :to="`/admin/ai/${ai.user.id}/tasks`" :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'ml-auto']">
            全部任务
          </RouterLink>
        </div>

        <ul v-if="recentTasks.length > 0" class="m-0 mt-[14px] list-none p-0">
          <li v-for="task in recentTasks" :key="task.id" class="flex flex-wrap items-center gap-2 border-b border-solid border-border py-[9px] text-[13.5px] last:border-b-0">
            <span :class="[ui.badge, taskStatusClass(task.status)]">{{ taskStatusLabel(task.status) }}</span>
            <span :class="[ui.badge, ui.badgeMuted]">{{ triggerLabel(task.trigger_type) }}</span>
            <span class="font-bold">{{ task.action_type }}</span>
            <span :class="[ui.muted, 'min-w-[120px] flex-1 overflow-hidden text-ellipsis whitespace-nowrap']">{{ task.decision_summary || '（无决策摘要）' }}</span>
            <span :class="[ui.mono, 'whitespace-nowrap text-[12.5px] text-ink-3']">{{ formatDateTime(task.created_at) }}</span>
          </li>
        </ul>
        <p v-else :class="ui.muted">暂无任务记录。</p>
      </section>

      <section :class="[ui.card, ui.cardPad]">
        <h2 :class="ui.pageTitle">快捷入口</h2>
        <div class="mt-[14px] grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-2">
          <RouterLink v-for="link in quickLinks" :key="link.to" :to="link.to" class="flex items-start gap-[10px] rounded-[10px] border border-solid border-border px-3 py-[10px] text-ink hover:bg-hover hover:no-underline">
            <AppIcon :name="link.icon" :size="18" class="mt-[2px] text-ink-2" />
            <span class="flex min-w-0 flex-col">
              <span class="text-[14px] font-bold">{{ link.label }}</span>
              <span class="text-[12.5px] leading-[1.6] text-ink-3">{{ link.description }}</span>
            </span>
          </RouterLink>
        </div>
      </section>
    </template>
  </div>
</template>
