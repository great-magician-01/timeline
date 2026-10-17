<script setup lang="ts">
// A-14 AI 生成与发布：生成草稿、直接发布、编辑并发布草稿
// 子页面自行读取路由参数 aiId（由 /admin/ai/:id 提供）
import { computed, onBeforeUnmount, reactive, ref } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import {
  generateAndPublish,
  generateDraft,
  listAIDrafts,
  listAITasks,
  publishAIDraft,
  updateAIDraft,
} from '../../api/aiAdmin'
import type { AIDraft } from '../../api/aiAdmin'
import { errorMessage } from '../../api/client'
import type { AITask, AITaskStatus } from '../../api/types'
import AlertNote from '../../components/AlertNote.vue'
import AppIcon from '../../components/AppIcon.vue'
import { useCursorList } from '../../composables/useCursorList'
import { formatDateTime } from '../../utils/datetime'
import { fromDatetimeLocal } from '../../utils/format'
import { ui } from '../../styles/ui'

type DraftFilters = Record<string, never>
type GenerateMode = 'draft' | 'publish' | 'schedule'

const route = useRoute()
const aiId = computed(() => String(route.params.id ?? ''))

const scheduledAt = ref('')
const submitting = ref(false)
const actionError = ref('')
const actionNotice = ref('')
const lastTaskId = ref('')
const lastScheduledAt = ref('')

// ---------- 生成任务状态：轮询 /admin/ai/:id/tasks ----------
const TASK_STATUS_LABELS: Record<AITaskStatus, string> = {
  pending: '待执行',
  running: '执行中',
  succeeded: '成功',
  noop: '无动作',
  rejected: '被拒绝',
  failed: '失败',
  canceled: '已取消',
}

const TERMINAL_STATUS: AITaskStatus[] = ['succeeded', 'noop', 'rejected', 'failed', 'canceled']
const currentTask = ref<AITask | null>(null)
const taskPolling = ref(false)
const taskPollNote = ref('')
let pollTimer: number | null = null
let pollToken = 0

function taskStatusLabel(status: AITaskStatus): string {
  return TASK_STATUS_LABELS[status] ?? status
}

function taskStatusClass(status: AITaskStatus): string {
  if (status === 'failed' || status === 'rejected') return ui.badgeDanger
  if (status === 'succeeded') return ''
  return ui.badgeMuted
}

function stopTaskPolling(): void {
  if (pollTimer !== null) {
    window.clearTimeout(pollTimer)
    pollTimer = null
  }
  taskPolling.value = false
}

async function pollTask(taskId: string, token: number, attempt: number): Promise<void> {
  try {
    const page = await listAITasks(aiId.value, { limit: 20 })
    if (token !== pollToken) return
    const found = page.items.find((item) => item.id === taskId)
    if (found) {
      currentTask.value = found
      if (TERMINAL_STATUS.includes(found.status)) {
        taskPolling.value = false
        if (found.status === 'succeeded') {
          actionNotice.value =
            lastScheduledAt.value !== ''
              ? '生成任务已完成，内容已进入定时发布队列。'
              : '生成任务已完成。'
          await reload()
        }
        return
      }
    }
  } catch {
    // 单次轮询失败不终止，继续按间隔重试
  }
  if (attempt >= 100) {
    taskPolling.value = false
    taskPollNote.value = '任务状态轮询超时，可前往任务页查看最终结果。'
    return
  }
  pollTimer = window.setTimeout(() => void pollTask(taskId, token, attempt + 1), 3000)
}

function startTaskPolling(taskId: string): void {
  stopTaskPolling()
  const token = ++pollToken
  currentTask.value = null
  taskPollNote.value = ''
  taskPolling.value = true
  pollTimer = window.setTimeout(() => void pollTask(taskId, token, 1), 1000)
}

onBeforeUnmount(stopTaskPolling)

const {
  items,
  loadingMore,
  hasMore,
  error: listError,
  loaded,
  reload,
  loadMore,
} = useCursorList<AIDraft, DraftFilters>({
  fetch: (params) => listAIDrafts(aiId.value, params),
  params: () => ({}),
})

const initialLoading = computed(() => !loaded.value && !listError.value)

async function onGenerate(mode: GenerateMode): Promise<void> {
  const scheduledIso = scheduledAt.value ? fromDatetimeLocal(scheduledAt.value) : ''
  if (mode === 'schedule' && !scheduledIso) {
    actionError.value = '请先选择定时发布时间。'
    return
  }
  if (mode === 'publish' && !window.confirm('确认直接生成并发布？生成完成后内容会立即按可见性对读者展示，不再经过草稿审核。')) {
    return
  }
  if (
    mode === 'schedule' &&
    !window.confirm(`确认生成并定时发布？生成完成后将在 ${formatDateTime(scheduledIso)} 进入定时发布队列。`)
  ) {
    return
  }
  submitting.value = true
  actionError.value = ''
  actionNotice.value = ''
  lastTaskId.value = ''
  lastScheduledAt.value = mode === 'schedule' ? scheduledIso : ''
  try {
    const data =
      mode === 'draft'
        ? await generateDraft(aiId.value)
        : mode === 'schedule'
          ? await generateDraft(aiId.value, { scheduled_at: scheduledIso })
          : await generateAndPublish(aiId.value)
    lastTaskId.value = data.task_id
    actionNotice.value =
      mode === 'draft'
        ? '已提交生成草稿任务，完成后会出现在下方草稿列表。'
        : mode === 'schedule'
          ? '已提交生成任务，完成后会进入定时发布队列。'
          : '已提交生成并发布任务，完成后内容会直接发布。'
    if (mode === 'draft') await reload()
    startTaskPolling(data.task_id)
  } catch (error) {
    actionError.value = errorMessage(error)
  } finally {
    submitting.value = false
  }
}

function resultLink(task: AITask): string {
  if (task.result_ref_type === 'post' && task.result_ref_id) {
    return `/admin/content/posts/${task.result_ref_id}`
  }
  return `/admin/ai/${aiId.value}/tasks`
}

function resultLabel(task: AITask): string {
  if (!task.result_ref_type) return '—'
  return `${task.result_ref_type} · ${task.result_ref_id ?? '—'}`
}

// ---------- 草稿编辑与发布 ----------
const editingId = ref<string | null>(null)
const editForm = reactive({ content_source: '' })
const savingId = ref<string | null>(null)
const publishingId = ref<string | null>(null)

function startEdit(draft: AIDraft): void {
  editingId.value = draft.id
  editForm.content_source = draft.content_source
  actionError.value = ''
  actionNotice.value = ''
}

function cancelEdit(): void {
  editingId.value = null
}

async function onSaveDraft(draft: AIDraft): Promise<void> {
  const content = editForm.content_source.trim()
  if (!content) {
    actionError.value = '草稿正文不能为空。'
    return
  }
  savingId.value = draft.id
  actionError.value = ''
  actionNotice.value = ''
  try {
    await updateAIDraft(aiId.value, draft.id, { content_source: content })
    editingId.value = null
    actionNotice.value = '草稿已保存；保存后仍需发布才会对外展示。'
    await reload()
  } catch (error) {
    actionError.value = errorMessage(error)
  } finally {
    savingId.value = null
  }
}

async function onPublish(draft: AIDraft): Promise<void> {
  if (!window.confirm('确认发布这条草稿？发布后内容立即对可见范围内的读者展示。')) return
  publishingId.value = draft.id
  actionError.value = ''
  actionNotice.value = ''
  try {
    await publishAIDraft(aiId.value, draft.id)
    actionNotice.value = '草稿已发布。'
    await reload()
  } catch (error) {
    actionError.value = errorMessage(error)
  } finally {
    publishingId.value = null
  }
}
</script>

<template>
  <div :class="ui.stack">
    <section :class="[ui.card, ui.cardPad]">
      <h2 :class="ui.pageTitle">手动生成</h2>
      <p :class="ui.pageDesc">
        生成与发布都是异步任务，模型按身份设定自行选题；提交后下方会轮询展示任务状态与结果。
      </p>

      <AlertNote v-if="actionNotice" kind="success">
        {{ actionNotice }}
        <template v-if="lastTaskId">
          任务已提交（<span :class="ui.mono">{{ lastTaskId }}</span>），可在任务页查看进度。
        </template>
      </AlertNote>
      <AlertNote v-if="actionError" kind="error">{{ actionError }}</AlertNote>

      <div :class="ui.form">
        <p class="m-0 text-[12.5px] text-ink-3">
          模型会按 AI 身份设定与当前上下文自行选题；生成内容仅进入草稿或发布队列，不会在前端回显模型原始输出。
        </p>

        <label :class="ui.fieldLabel" for="publish-schedule">定时发布时间（可选，北京时间）</label>
        <input
          id="publish-schedule"
          v-model="scheduledAt"
          :class="[ui.input, 'max-w-[260px]']"
          type="datetime-local"
          name="scheduled_at"
          :disabled="submitting"
        />
        <p class="m-0 text-[12.5px] text-ink-3">填写后使用「生成并定时发布」；到点由服务端按可见性发布，应用重启不会丢失。</p>

        <div class="mt-3 flex flex-wrap gap-2">
          <button :class="[ui.button, ui.buttonPrimary]" type="button" :disabled="submitting" @click="onGenerate('draft')">
            <AppIcon name="file-text" :size="16" />
            生成草稿
          </button>
          <button :class="[ui.button, ui.buttonSecondary]" type="button" :disabled="submitting" @click="onGenerate('publish')">
            <AppIcon name="send" :size="16" />
            直接生成并发布
          </button>
          <button
            :class="[ui.button, ui.buttonSecondary]"
            type="button"
            :disabled="submitting || !scheduledAt"
            @click="onGenerate('schedule')"
          >
            <AppIcon name="calendar" :size="16" />
            生成并定时发布
          </button>
          <RouterLink :to="`/admin/ai/${aiId}/tasks`" :class="[ui.button, ui.buttonSecondary]">查看任务</RouterLink>
        </div>

        <p v-if="submitting" :class="ui.loadingRow">
          <span :class="ui.spinner" aria-hidden="true"></span> 正在提交任务…
        </p>
      </div>

      <div v-if="lastTaskId" class="mt-4 grid gap-[6px] rounded-[10px] border border-solid border-border bg-surface-subtle px-[14px] py-3">
        <p class="m-0 flex flex-wrap items-center gap-2 text-[13.5px] font-semibold">
          <AppIcon name="activity" :size="16" />
          <span>生成任务 <span :class="ui.mono">{{ lastTaskId }}</span></span>
          <span :class="[ui.badge, currentTask ? taskStatusClass(currentTask.status) : ui.badgeMuted, 'ml-auto']">
            {{ currentTask ? taskStatusLabel(currentTask.status) : '已提交' }}
          </span>
        </p>

        <template v-if="currentTask">
          <p class="m-0 text-[12.5px] text-ink-3 [overflow-wrap:anywhere]">
            动作 {{ currentTask.action_type || '—' }} · 触发 {{ currentTask.trigger_type }} · 尝试
            {{ currentTask.attempts }}
          </p>
          <p v-if="currentTask.decision_summary" class="m-0 text-[12.5px] text-ink-3 [overflow-wrap:anywhere]">决策：{{ currentTask.decision_summary }}</p>
          <p v-if="currentTask.error" class="m-0 text-[12.5px] text-red [overflow-wrap:anywhere]">错误：{{ currentTask.error }}</p>
          <p class="m-0 text-[12.5px] text-ink-3 [overflow-wrap:anywhere]">
            结果引用：
            <RouterLink :to="resultLink(currentTask)">{{ resultLabel(currentTask) }}</RouterLink>
          </p>
          <div v-if="currentTask.memories && currentTask.memories.length > 0" class="mt-1">
            <p :class="[ui.fieldLabel, 'm-0 mb-1']">注入记忆</p>
            <ul class="m-0 grid list-none gap-1 p-0">
              <li v-for="memory in currentTask.memories" :key="memory.id" class="flex items-start gap-[6px] text-[12.5px] leading-[1.6] text-ink-2">
                <span :class="[ui.badge, ui.badgeMuted]">{{ memory.category || memory.type }}</span>
                {{ memory.content }}
              </li>
            </ul>
          </div>
        </template>
        <p v-else-if="taskPolling" :class="ui.loadingRow">
          <span :class="ui.spinner" aria-hidden="true"></span> 正在获取任务状态…
        </p>
        <p v-if="taskPollNote" class="m-0 text-[12.5px] text-ink-3 [overflow-wrap:anywhere]">{{ taskPollNote }}</p>
      </div>
    </section>

    <section :class="[ui.card, ui.cardPad]">
      <h2 :class="ui.pageTitle">AI 草稿</h2>
      <p :class="ui.pageDesc">AI 草稿在发布前仅管理员可见，不会出现在作者主页或时间线上。</p>

      <p v-if="initialLoading" :class="ui.loadingRow"><span :class="ui.spinner" aria-hidden="true"></span> 加载中…</p>

      <template v-else-if="listError && items.length === 0">
        <AlertNote kind="error">{{ listError }}</AlertNote>
        <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'self-start']" type="button" @click="reload">重试</button>
      </template>

      <div v-else-if="items.length === 0" :class="ui.empty">
        <AppIcon name="file-text" :size="26" :class="ui.emptyIcon" />
        <strong :class="ui.emptyStrong">暂无 AI 草稿</strong>
        <p :class="ui.emptyText">使用上方「生成草稿」提交任务，完成后草稿会出现在这里。</p>
      </div>

      <div v-else class="mt-[14px]">
        <article v-for="draft in items" :key="draft.id" class="border-b border-solid border-border py-[14px] last:border-b-0">
          <div class="flex flex-wrap items-center gap-2 text-[12.5px]">
            <span :class="[ui.muted, ui.mono]">草稿 {{ draft.id }}</span>
            <span :class="[ui.badge, ui.badgeMuted]">{{ draft.visibility }}</span>
            <span :class="[ui.muted, ui.mono]">r{{ draft.revision }}</span>
            <span :class="[ui.mono, 'ml-auto whitespace-nowrap text-ink-3']">更新于 {{ formatDateTime(draft.updated_at) }}</span>
          </div>

          <form v-if="editingId === draft.id" class="mt-[10px]" novalidate @submit.prevent="onSaveDraft(draft)">
            <label :class="ui.fieldLabel" for="draft-content">正文（Markdown）</label>
            <textarea
              id="draft-content"
              v-model="editForm.content_source"
              :class="[ui.input, ui.textarea, 'min-h-[180px]!']"
              name="content_source"
              :disabled="savingId === draft.id"
            ></textarea>
            <div class="mt-3 flex flex-wrap gap-2">
              <button :class="[ui.button, ui.buttonPrimary, ui.buttonSmall]" type="submit" :disabled="savingId === draft.id">
                <span v-if="savingId === draft.id" :class="[ui.spinner, ui.buttonSpinner]" aria-hidden="true"></span>
                {{ savingId === draft.id ? '保存中…' : '保存草稿' }}
              </button>
              <button
                :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]"
                type="button"
                :disabled="savingId === draft.id"
                @click="cancelEdit"
              >
                取消
              </button>
            </div>
          </form>

          <template v-else>
            <!-- content_html 由后端转换并清洗 -->
            <div v-if="draft.content_html" class="mt-2 max-h-[260px] overflow-y-auto rounded-[10px] border border-solid border-border bg-surface-subtle px-[14px] py-3 text-[14px] leading-[1.8] [word-break:break-word] [&_img]:h-auto [&_img]:max-w-full" v-html="draft.content_html"></div>
            <p v-else :class="ui.muted">（空正文）</p>
            <div class="mt-3 flex flex-wrap gap-2">
              <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]" type="button" @click="startEdit(draft)">编辑</button>
              <button
                :class="[ui.button, ui.buttonPrimary, ui.buttonSmall]"
                type="button"
                :disabled="publishingId === draft.id"
                @click="onPublish(draft)"
              >
                <span v-if="publishingId === draft.id" :class="[ui.spinner, ui.buttonSpinner]" aria-hidden="true"></span>
                {{ publishingId === draft.id ? '发布中…' : '发布' }}
              </button>
            </div>
          </template>
        </article>

        <div class="flex justify-center px-0 pt-[14px] pb-0">
          <button v-if="hasMore && !loadingMore" :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]" type="button" @click="loadMore">
            加载更多
          </button>
          <p v-else-if="loadingMore" :class="[ui.loadingRow, 'p-0!']">
            <span :class="ui.spinner" aria-hidden="true"></span> 正在加载…
          </p>
          <p v-else :class="[ui.muted, 'm-0']">已加载全部 {{ items.length }} 条草稿</p>
        </div>
      </div>

      <AlertNote v-if="listError && items.length > 0" kind="error">{{ listError }}</AlertNote>
    </section>
  </div>
</template>
