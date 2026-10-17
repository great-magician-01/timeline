<script setup lang="ts">
// A-16 全局任务：后台作业状态、失败信息与重试/取消
import { computed, reactive, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { cancelTask, listTasks, retryTask } from '../../api/aiAdmin'
import { errorMessage } from '../../api/client'
import type { BackgroundJob } from '../../api/types'
import AlertNote from '../../components/AlertNote.vue'
import AppIcon from '../../components/AppIcon.vue'
import { useCursorList } from '../../composables/useCursorList'
import { formatDateTime } from '../../utils/datetime'
import { ui } from '../../styles/ui'

type TaskFilters = {
  type?: string
  status?: string
}

const JOB_TYPES: { value: string; label: string }[] = [
  { value: 'post_publish', label: '定时发布' },
  { value: 'media_derivative', label: '媒体衍生文件' },
  { value: 'ai_schedule', label: 'AI 调度' },
  { value: 'ai_task', label: 'AI 任务' },
  { value: 'memory_consolidation', label: '记忆整理' },
  { value: 'attachment_cleanup', label: '附件清理' },
  { value: 'ai_call_log_cleanup', label: '调用记录清理' },
]

const STATUS_LABELS: Record<string, string> = {
  pending: '待执行',
  running: '执行中',
  succeeded: '成功',
  failed: '失败',
  paused: '已暂停',
  canceled: '已取消',
}

const filters = reactive({ type: '', status: '' })

const {
  items,
  loadingMore,
  hasMore,
  error: listError,
  loaded,
  reload,
  loadMore,
} = useCursorList<BackgroundJob, TaskFilters>({
  fetch: (params) => listTasks(params),
  params: () => ({
    type: filters.type || undefined,
    status: filters.status || undefined,
  }),
})

const initialLoading = computed(() => !loaded.value && !listError.value)

function typeLabel(value: string): string {
  return JOB_TYPES.find((item) => item.value === value)?.label ?? value
}

function statusLabel(value: string): string {
  return STATUS_LABELS[value] ?? value
}

function statusClass(value: string): string {
  if (value === 'failed') return ui.badgeDanger
  if (value === 'succeeded') return ''
  return ui.badgeMuted
}

function onTypeChange(): void {
  void reload()
}

function setStatus(value: string): void {
  if (filters.status === value) return
  filters.status = value
  void reload()
}

const busyId = ref<string | null>(null)
const actionError = ref('')
const actionNotice = ref('')

async function onRetry(job: BackgroundJob): Promise<void> {
  if (
    !window.confirm(
      `确认重试该作业？重试会重新执行「${typeLabel(job.job_type)}」的后台处理，可能再次产生外部副作用（如发布内容）（作业 ${job.id}）。`,
    )
  ) {
    return
  }
  busyId.value = job.id
  actionError.value = ''
  actionNotice.value = ''
  try {
    await retryTask(job.id)
    actionNotice.value = '作业已重新排队。'
    await reload()
  } catch (error) {
    actionError.value = errorMessage(error)
  } finally {
    busyId.value = null
  }
}

async function onCancel(job: BackgroundJob): Promise<void> {
  if (!window.confirm('确认取消该作业？取消后不会再执行，已完成的业务数据保持不变。')) return
  busyId.value = job.id
  actionError.value = ''
  actionNotice.value = ''
  try {
    await cancelTask(job.id)
    actionNotice.value = '作业已取消。'
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
      <div class="flex items-start gap-3">
        <div>
          <h2 :class="ui.pageTitle">全局任务</h2>
          <p :class="ui.pageDesc">
            定时发布、媒体处理、记忆整理与 AI 动作都由后台作业驱动；失败作业可重试，待执行作业可取消。
          </p>
        </div>
      </div>

      <div class="mt-[14px] flex flex-wrap items-center gap-[10px]">
        <label class="flex items-center gap-2">
          <span :class="ui.muted">作业类型</span>
          <select v-model="filters.type" :class="[ui.input, 'w-[180px]!']" name="type" aria-label="作业类型筛选" @change="onTypeChange">
            <option value="">全部类型</option>
            <option v-for="item in JOB_TYPES" :key="item.value" :value="item.value">{{ item.label }}</option>
          </select>
        </label>

        <div :class="ui.segment" role="group" aria-label="作业状态筛选">
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
          <button type="button" :class="[ui.segmentButton, filters.status === 'failed' ? ui.segmentButtonActive : '']" @click="setStatus('failed')">
            失败
          </button>
          <button type="button" :class="[ui.segmentButton, filters.status === 'paused' ? ui.segmentButtonActive : '']" @click="setStatus('paused')">
            已暂停
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
        <strong :class="ui.emptyStrong">没有匹配的后台作业</strong>
        <p :class="ui.emptyText">调整作业类型或状态筛选条件。</p>
      </div>
    </section>

    <template v-else>
      <section :class="[ui.card, 'overflow-hidden']">
        <div class="overflow-x-auto">
          <table class="w-full border-collapse text-[13px] [&_th]:whitespace-nowrap [&_th]:border-b [&_th]:border-solid [&_th]:border-border [&_th]:px-3 [&_th]:py-[10px] [&_th]:text-left [&_th]:text-[12.5px] [&_th]:font-semibold [&_th]:text-ink-3 [&_td]:border-b [&_td]:border-solid [&_td]:border-border [&_td]:px-3 [&_td]:py-[10px] [&_td]:align-top [&_tbody_tr:last-child_td]:border-b-0">
            <thead>
              <tr>
                <th>ID</th>
                <th>作业类型</th>
                <th>引用对象</th>
                <th>状态</th>
                <th>运行时间</th>
                <th>尝试次数</th>
                <th>最后错误</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="job in items" :key="job.id">
                <td :class="[ui.mono, 'max-w-[140px] overflow-hidden text-ellipsis whitespace-nowrap text-[12.5px]']" :title="job.id">{{ job.id }}</td>
                <td class="whitespace-nowrap">{{ typeLabel(job.job_type) }}</td>
                <td :class="[ui.mono, 'max-w-[190px] [word-break:break-all]']">
                  <template v-if="job.ref_type">
                    {{ job.ref_type }}<template v-if="job.ref_id"> · {{ job.ref_id }}</template>
                  </template>
                  <template v-else>—</template>
                </td>
                <td>
                  <span :class="[ui.badge, statusClass(job.status)]">{{ statusLabel(job.status) }}</span>
                </td>
                <td class="whitespace-nowrap text-[12.5px] text-ink-2">
                  {{ formatDateTime(job.run_at) }}
                  <span class="mt-[2px] block text-[11.5px] text-ink-3">更新于 {{ formatDateTime(job.updated_at) }}</span>
                </td>
                <td :class="ui.mono">{{ job.attempts }} / {{ job.max_attempts }}</td>
                <td class="min-w-[160px] max-w-[260px] text-red [word-break:break-word]">{{ job.last_error || '—' }}</td>
                <td class="min-w-[96px]">
                  <div class="flex flex-wrap gap-[6px]">
                    <button
                      v-if="job.status === 'failed'"
                      :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]"
                      type="button"
                      :disabled="busyId === job.id"
                      @click="onRetry(job)"
                    >
                      {{ busyId === job.id ? '提交中…' : '重试' }}
                    </button>
                    <button
                      v-if="job.status === 'pending'"
                      :class="[ui.button, ui.buttonDanger, ui.buttonSmall]"
                      type="button"
                      :disabled="busyId === job.id"
                      @click="onCancel(job)"
                    >
                      {{ busyId === job.id ? '处理中…' : '取消' }}
                    </button>
                    <span v-if="job.status !== 'failed' && job.status !== 'pending'" :class="ui.muted">—</span>
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
          <p v-else :class="[ui.muted, 'm-0']">已加载全部 {{ items.length }} 个作业</p>
        </div>
      </section>

      <AlertNote v-if="listError" kind="error">{{ listError }}</AlertNote>
    </template>

    <p :class="[ui.muted, 'm-0']">
      单个 AI 的任务记录请在
      <RouterLink to="/admin/ai">AI 管理</RouterLink>
      中选择具体 AI 查看。
    </p>
  </div>
</template>
