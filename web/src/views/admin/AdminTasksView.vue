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
  if (value === 'failed') return 'badge--danger'
  if (value === 'succeeded') return ''
  return 'badge--muted'
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
  <div class="stack">
    <section class="card card--pad">
      <div class="head-row">
        <div>
          <h2 class="page-title">全局任务</h2>
          <p class="page-desc">
            定时发布、媒体处理、记忆整理与 AI 动作都由后台作业驱动；失败作业可重试，待执行作业可取消。
          </p>
        </div>
      </div>

      <div class="filter-row">
        <label class="type-field">
          <span class="muted">作业类型</span>
          <select v-model="filters.type" class="input" name="type" aria-label="作业类型筛选" @change="onTypeChange">
            <option value="">全部类型</option>
            <option v-for="item in JOB_TYPES" :key="item.value" :value="item.value">{{ item.label }}</option>
          </select>
        </label>

        <div class="seg" role="group" aria-label="作业状态筛选">
          <button type="button" :class="{ 'is-active': filters.status === '' }" @click="setStatus('')">全部</button>
          <button type="button" :class="{ 'is-active': filters.status === 'pending' }" @click="setStatus('pending')">
            待执行
          </button>
          <button type="button" :class="{ 'is-active': filters.status === 'running' }" @click="setStatus('running')">
            执行中
          </button>
          <button
            type="button"
            :class="{ 'is-active': filters.status === 'succeeded' }"
            @click="setStatus('succeeded')"
          >
            成功
          </button>
          <button type="button" :class="{ 'is-active': filters.status === 'failed' }" @click="setStatus('failed')">
            失败
          </button>
          <button type="button" :class="{ 'is-active': filters.status === 'paused' }" @click="setStatus('paused')">
            已暂停
          </button>
          <button type="button" :class="{ 'is-active': filters.status === 'canceled' }" @click="setStatus('canceled')">
            已取消
          </button>
        </div>
      </div>
    </section>

    <AlertNote v-if="actionNotice" kind="success">{{ actionNotice }}</AlertNote>
    <AlertNote v-if="actionError" kind="error">{{ actionError }}</AlertNote>

    <p v-if="initialLoading" class="card card--pad loading-row"><span class="spinner" aria-hidden="true"></span> 加载中…</p>

    <template v-else-if="listError && items.length === 0">
      <AlertNote kind="error">{{ listError }}</AlertNote>
      <button class="btn btn--secondary btn--sm self-start" type="button" @click="reload">重试</button>
    </template>

    <section v-else-if="items.length === 0" class="card card--pad">
      <div class="empty">
        <AppIcon name="list" :size="26" />
        <strong>没有匹配的后台作业</strong>
        <p>调整作业类型或状态筛选条件。</p>
      </div>
    </section>

    <template v-else>
      <section class="card table-card">
        <div class="table-scroll">
          <table class="table">
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
                <td class="col-id mono" :title="job.id">{{ job.id }}</td>
                <td class="nowrap">{{ typeLabel(job.job_type) }}</td>
                <td class="col-ref mono">
                  <template v-if="job.ref_type">
                    {{ job.ref_type }}<template v-if="job.ref_id"> · {{ job.ref_id }}</template>
                  </template>
                  <template v-else>—</template>
                </td>
                <td>
                  <span class="badge" :class="statusClass(job.status)">{{ statusLabel(job.status) }}</span>
                </td>
                <td class="nowrap col-time">
                  {{ formatDateTime(job.run_at) }}
                  <span class="muted block-id">更新于 {{ formatDateTime(job.updated_at) }}</span>
                </td>
                <td class="mono">{{ job.attempts }} / {{ job.max_attempts }}</td>
                <td class="col-error">{{ job.last_error || '—' }}</td>
                <td class="col-actions">
                  <div class="actions">
                    <button
                      v-if="job.status === 'failed'"
                      class="btn btn--secondary btn--sm"
                      type="button"
                      :disabled="busyId === job.id"
                      @click="onRetry(job)"
                    >
                      {{ busyId === job.id ? '提交中…' : '重试' }}
                    </button>
                    <button
                      v-if="job.status === 'pending'"
                      class="btn btn--danger btn--sm"
                      type="button"
                      :disabled="busyId === job.id"
                      @click="onCancel(job)"
                    >
                      {{ busyId === job.id ? '处理中…' : '取消' }}
                    </button>
                    <span v-if="job.status !== 'failed' && job.status !== 'pending'" class="muted">—</span>
                  </div>
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <div class="list-foot">
          <button v-if="hasMore && !loadingMore" class="btn btn--secondary btn--sm" type="button" @click="loadMore">
            加载更多
          </button>
          <p v-else-if="loadingMore" class="loading-row list-foot-loading">
            <span class="spinner" aria-hidden="true"></span> 正在加载…
          </p>
          <p v-else class="muted">已加载全部 {{ items.length }} 个作业</p>
        </div>
      </section>

      <AlertNote v-if="listError" kind="error">{{ listError }}</AlertNote>
    </template>

    <p class="muted foot-note">
      单个 AI 的任务记录请在
      <RouterLink to="/admin/ai">AI 管理</RouterLink>
      中选择具体 AI 查看。
    </p>
  </div>
</template>

<style scoped>
.head-row {
  display: flex;
  align-items: flex-start;
  gap: 12px;
}
.filter-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
  margin-top: 14px;
}
.type-field {
  display: flex;
  align-items: center;
  gap: 8px;
}
.type-field .input {
  width: 180px;
}
.self-start {
  align-self: flex-start;
}
.table-card {
  overflow: hidden;
}
.table-scroll {
  overflow-x: auto;
}
.table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}
.table th {
  padding: 10px 12px;
  border-bottom: 1px solid var(--border);
  color: var(--ink-3);
  font-size: 12.5px;
  font-weight: 600;
  text-align: left;
  white-space: nowrap;
}
.table td {
  padding: 10px 12px;
  border-bottom: 1px solid var(--border);
  vertical-align: top;
}
.table tbody tr:last-child td {
  border-bottom: none;
}
.nowrap {
  white-space: nowrap;
}
.col-id {
  max-width: 140px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12.5px;
}
.col-ref {
  max-width: 190px;
  word-break: break-all;
}
.col-time {
  font-size: 12.5px;
  color: var(--ink-2);
}
.block-id {
  display: block;
  font-size: 11.5px;
  margin-top: 2px;
}
.col-error {
  min-width: 160px;
  max-width: 260px;
  color: var(--red);
  word-break: break-word;
}
.col-actions {
  min-width: 96px;
}
.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.list-foot {
  display: flex;
  justify-content: center;
  padding: 14px 16px;
}
.list-foot .muted {
  margin: 0;
}
.list-foot-loading {
  padding: 0;
}
.foot-note {
  margin: 0;
}
</style>
