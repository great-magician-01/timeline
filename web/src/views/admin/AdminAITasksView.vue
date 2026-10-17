<script setup lang="ts">
// A-15 单个 AI 任务：触发原因、决策、动作、结果与失败重试
// 子页面自行读取路由参数 aiId（由 /admin/ai/:id 提供）
import { computed, reactive, ref } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { cancelAITask, listAITasks, retryAITask } from '../../api/aiAdmin'
import { errorMessage } from '../../api/client'
import type { AITask, AITaskStatus } from '../../api/types'
import AlertNote from '../../components/AlertNote.vue'
import AppIcon from '../../components/AppIcon.vue'
import { useCursorList } from '../../composables/useCursorList'
import { formatDateTime } from '../../utils/datetime'
import { ui } from '../../styles/ui'

type TaskFilters = {
  status?: string
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

const route = useRoute()
const aiId = computed(() => String(route.params.id ?? ''))

const filters = reactive({ status: '' })

const {
  items,
  loadingMore,
  hasMore,
  error: listError,
  loaded,
  reload,
  loadMore,
} = useCursorList<AITask, TaskFilters>({
  fetch: (params) => listAITasks(aiId.value, params),
  params: () => ({ status: filters.status || undefined }),
})

const initialLoading = computed(() => !loaded.value && !listError.value)

function statusLabel(value: AITaskStatus): string {
  return STATUS_LABELS[value] ?? value
}

function statusClass(value: AITaskStatus): string {
  if (value === 'failed' || value === 'rejected') return ui.badgeDanger
  if (value === 'succeeded') return ''
  return ui.badgeMuted
}

function triggerLabel(value: string): string {
  return TRIGGER_LABELS[value] ?? value
}

function setStatus(value: string): void {
  if (filters.status === value) return
  filters.status = value
  void reload()
}

/** 结果引用仅在帖子/用户等已知资源类型时提供管理端跳转 */
function resultLink(task: AITask): string {
  if (task.result_ref_type === 'post' && task.result_ref_id) {
    return `/admin/content/posts/${task.result_ref_id}`
  }
  if (task.result_ref_type === 'user' && task.result_ref_id) {
    return `/admin/users/${task.result_ref_id}`
  }
  return ''
}

const busyId = ref<string | null>(null)
const actionError = ref('')
const actionNotice = ref('')

async function onRetry(task: AITask): Promise<void> {
  if (
    !window.confirm(
      `确认重试这条失败任务？重试会重新执行该动作，生成内容可能产生新的帖子或评论（任务 ${task.id}）。`,
    )
  ) {
    return
  }
  busyId.value = task.id
  actionError.value = ''
  actionNotice.value = ''
  try {
    await retryAITask(task.id)
    actionNotice.value = '已重新提交任务，可在列表中查看新的状态。'
    await reload()
  } catch (error) {
    actionError.value = errorMessage(error)
  } finally {
    busyId.value = null
  }
}

async function onCancel(task: AITask): Promise<void> {
  if (!window.confirm('确认取消这条待执行任务？取消后该动作不会执行，也不会产生任何内容。')) return
  busyId.value = task.id
  actionError.value = ''
  actionNotice.value = ''
  try {
    await cancelAITask(task.id)
    actionNotice.value = '任务已取消。'
    await reload()
  } catch (error) {
    actionError.value = errorMessage(error)
  } finally {
    busyId.value = null
  }
}
</script>

<template>
  <div :class="ui.stack">
    <section :class="[ui.card, ui.cardPad]">
      <div class="flex flex-wrap items-start gap-3">
        <div>
          <h2 :class="ui.pageTitle">任务记录</h2>
          <p :class="ui.pageDesc">每次动作执行都会留下记录，包含触发原因、决策摘要与失败原因。</p>
        </div>
        <RouterLink to="/admin/ai-calls" :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'ml-auto']">查看调用记录</RouterLink>
      </div>

      <div class="mt-[14px] flex flex-wrap gap-2">
        <div :class="ui.segment" role="group" aria-label="任务状态筛选">
          <button type="button" :class="[ui.segmentButton, filters.status === '' ? ui.segmentButtonActive : '']" @click="setStatus('')">全部</button>
          <button type="button" :class="[ui.segmentButton, filters.status === 'pending' ? ui.segmentButtonActive : '']" @click="setStatus('pending')">
            待执行
          </button>
          <button type="button" :class="[ui.segmentButton, filters.status === 'running' ? ui.segmentButtonActive : '']" @click="setStatus('running')">
            执行中
          </button>
          <button
            type="button"
            :class="[ui.segmentButton, filters.status === 'succeeded' ? ui.segmentButtonActive : '']"
            @click="setStatus('succeeded')"
          >
            成功
          </button>
          <button type="button" :class="[ui.segmentButton, filters.status === 'noop' ? ui.segmentButtonActive : '']" @click="setStatus('noop')">
            无动作
          </button>
          <button type="button" :class="[ui.segmentButton, filters.status === 'rejected' ? ui.segmentButtonActive : '']" @click="setStatus('rejected')">
            被拒绝
          </button>
          <button type="button" :class="[ui.segmentButton, filters.status === 'failed' ? ui.segmentButtonActive : '']" @click="setStatus('failed')">
            失败
          </button>
          <button type="button" :class="[ui.segmentButton, filters.status === 'canceled' ? ui.segmentButtonActive : '']" @click="setStatus('canceled')">
            已取消
          </button>
        </div>
      </div>
    </section>

    <AlertNote v-if="actionNotice" kind="success">{{ actionNotice }}</AlertNote>
    <AlertNote v-if="actionError" kind="error">{{ actionError }}</AlertNote>

    <p v-if="initialLoading" :class="[ui.card, ui.cardPad, ui.loadingRow]"><span :class="ui.spinner" aria-hidden="true"></span> 加载中…</p>

    <template v-else-if="listError && items.length === 0">
      <AlertNote kind="error">{{ listError }}</AlertNote>
      <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'self-start']" type="button" @click="reload">重试</button>
    </template>

    <section v-else-if="items.length === 0" :class="[ui.card, ui.cardPad]">
      <div :class="ui.empty">
        <AppIcon name="list" :size="26" :class="ui.emptyIcon" />
        <strong :class="ui.emptyStrong">没有匹配的任务记录</strong>
        <p :class="ui.emptyText">调整状态筛选，或先在「生成与发布」中提交一次生成任务。</p>
      </div>
    </section>

    <template v-else>
      <section :class="[ui.card, 'overflow-hidden']">
        <div class="overflow-x-auto">
          <table class="w-full border-collapse text-[13px] [&_th]:whitespace-nowrap [&_th]:border-b [&_th]:border-solid [&_th]:border-border [&_th]:px-3 [&_th]:py-[10px] [&_th]:text-left [&_th]:text-[12.5px] [&_th]:font-semibold [&_th]:text-ink-3 [&_td]:border-b [&_td]:border-solid [&_td]:border-border [&_td]:px-3 [&_td]:py-[10px] [&_td]:align-top [&_tbody_tr:last-child_td]:border-b-0">
            <thead>
              <tr>
                <th>状态</th>
                <th>触发</th>
                <th>动作</th>
                <th>决策摘要</th>
                <th>目标</th>
                <th>注入记忆</th>
                <th>结果引用</th>
                <th>模型</th>
                <th>尝试</th>
                <th>时间</th>
                <th>错误</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="task in items" :key="task.id">
                <td>
                  <span :class="[ui.badge, statusClass(task.status)]">{{ statusLabel(task.status) }}</span>
                  <span :class="[ui.mono, 'mt-1 block text-[11.5px] text-ink-3']">{{ task.id }}</span>
                </td>
                <td class="whitespace-nowrap">{{ triggerLabel(task.trigger_type) }}</td>
                <td class="whitespace-nowrap">{{ task.action_type }}</td>
                <td class="min-w-[180px] max-w-[300px]">{{ task.decision_summary || '—' }}</td>
                <td :class="[ui.mono, 'max-w-[170px] [word-break:break-all]']">
                  <template v-if="task.target_type">
                    {{ task.target_type }}<template v-if="task.target_id"> · {{ task.target_id }}</template>
                  </template>
                  <template v-else>—</template>
                </td>
                <td class="min-w-[200px] max-w-[300px]">
                  <ul v-if="task.memories && task.memories.length > 0" class="m-0 grid list-none gap-1 p-0">
                    <li v-for="memory in task.memories" :key="memory.id" :title="memory.content" class="flex items-start gap-[5px] text-[12px] leading-[1.5] text-ink-2">
                      <span :class="[ui.badge, ui.badgeMuted]">{{ memory.category || memory.type }}</span>
                      <span class="line-clamp-2 [word-break:break-word]">{{ memory.content }}</span>
                    </li>
                  </ul>
                  <span v-else :class="ui.muted">—</span>
                </td>
                <td :class="[ui.mono, 'max-w-[200px] text-[12px] [word-break:break-all]']">
                  <template v-if="task.result_ref_type">
                    <RouterLink
                      v-if="resultLink(task)"
                      :to="resultLink(task)"
                      class="text-primary"
                    >
                      {{ task.result_ref_type }} · {{ task.result_ref_id }}
                    </RouterLink>
                    <template v-else>{{ task.result_ref_type }} · {{ task.result_ref_id ?? '—' }}</template>
                  </template>
                  <span v-else :class="ui.muted">—</span>
                </td>
                <td :class="[ui.mono, 'whitespace-nowrap']">{{ task.model || '—' }}</td>
                <td :class="ui.mono">{{ task.attempts }}</td>
                <td class="whitespace-nowrap text-[12.5px] text-ink-2">{{ formatDateTime(task.created_at) }}</td>
                <td class="min-w-[160px] max-w-[260px] text-red [word-break:break-word]">{{ task.error || '—' }}</td>
                <td class="min-w-[96px]">
                  <div class="flex flex-wrap gap-[6px]">
                    <button
                      v-if="task.status === 'failed'"
                      :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]"
                      type="button"
                      :disabled="busyId === task.id"
                      @click="onRetry(task)"
                    >
                      {{ busyId === task.id ? '提交中…' : '重试' }}
                    </button>
                    <button
                      v-if="task.status === 'pending'"
                      :class="[ui.button, ui.buttonDanger, ui.buttonSmall]"
                      type="button"
                      :disabled="busyId === task.id"
                      @click="onCancel(task)"
                    >
                      {{ busyId === task.id ? '处理中…' : '取消' }}
                    </button>
                    <RouterLink
                      :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]"
                      :to="`/admin/ai-calls?task_id=${task.id}`"
                    >
                      调用记录
                    </RouterLink>
                  </div>
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <div class="flex justify-center px-4 py-[14px]">
          <button v-if="hasMore && !loadingMore" :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]" type="button" @click="loadMore">
            加载更多
          </button>
          <p v-else-if="loadingMore" :class="[ui.loadingRow, 'p-0!']">
            <span :class="ui.spinner" aria-hidden="true"></span> 正在加载…
          </p>
          <p v-else :class="[ui.muted, 'm-0']">已加载全部 {{ items.length }} 条任务</p>
        </div>
      </section>

      <AlertNote v-if="listError" kind="error">{{ listError }}</AlertNote>
    </template>
  </div>
</template>
