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
  if (value === 'failed' || value === 'rejected') return 'badge--danger'
  if (value === 'succeeded') return ''
  return 'badge--muted'
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
  <div class="stack">
    <section class="card card--pad">
      <div class="head-row">
        <div>
          <h2 class="page-title">任务记录</h2>
          <p class="page-desc">每次动作执行都会留下记录，包含触发原因、决策摘要与失败原因。</p>
        </div>
        <RouterLink to="/admin/ai-calls" class="btn btn--secondary btn--sm">查看调用记录</RouterLink>
      </div>

      <div class="filter-row">
        <div class="seg" role="group" aria-label="任务状态筛选">
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
          <button type="button" :class="{ 'is-active': filters.status === 'noop' }" @click="setStatus('noop')">
            无动作
          </button>
          <button type="button" :class="{ 'is-active': filters.status === 'rejected' }" @click="setStatus('rejected')">
            被拒绝
          </button>
          <button type="button" :class="{ 'is-active': filters.status === 'failed' }" @click="setStatus('failed')">
            失败
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
        <strong>没有匹配的任务记录</strong>
        <p>调整状态筛选，或先在「生成与发布」中提交一次生成任务。</p>
      </div>
    </section>

    <template v-else>
      <section class="card table-card">
        <div class="table-scroll">
          <table class="table">
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
                  <span class="badge" :class="statusClass(task.status)">{{ statusLabel(task.status) }}</span>
                  <span class="muted mono block-id">{{ task.id }}</span>
                </td>
                <td class="nowrap">{{ triggerLabel(task.trigger_type) }}</td>
                <td class="nowrap">{{ task.action_type }}</td>
                <td class="col-summary">{{ task.decision_summary || '—' }}</td>
                <td class="col-target mono">
                  <template v-if="task.target_type">
                    {{ task.target_type }}<template v-if="task.target_id"> · {{ task.target_id }}</template>
                  </template>
                  <template v-else>—</template>
                </td>
                <td class="col-memories">
                  <ul v-if="task.memories && task.memories.length > 0" class="memory-list">
                    <li v-for="memory in task.memories" :key="memory.id" :title="memory.content">
                      <span class="badge badge--muted">{{ memory.category || memory.type }}</span>
                      <span class="memory-text">{{ memory.content }}</span>
                    </li>
                  </ul>
                  <span v-else class="muted">—</span>
                </td>
                <td class="col-result mono">
                  <template v-if="task.result_ref_type">
                    <RouterLink
                      v-if="resultLink(task)"
                      :to="resultLink(task)"
                      class="result-link"
                    >
                      {{ task.result_ref_type }} · {{ task.result_ref_id }}
                    </RouterLink>
                    <template v-else>{{ task.result_ref_type }} · {{ task.result_ref_id ?? '—' }}</template>
                  </template>
                  <span v-else class="muted">—</span>
                </td>
                <td class="mono nowrap">{{ task.model || '—' }}</td>
                <td class="mono">{{ task.attempts }}</td>
                <td class="nowrap col-time">{{ formatDateTime(task.created_at) }}</td>
                <td class="col-error">{{ task.error || '—' }}</td>
                <td class="col-actions">
                  <div class="actions">
                    <button
                      v-if="task.status === 'failed'"
                      class="btn btn--secondary btn--sm"
                      type="button"
                      :disabled="busyId === task.id"
                      @click="onRetry(task)"
                    >
                      {{ busyId === task.id ? '提交中…' : '重试' }}
                    </button>
                    <button
                      v-if="task.status === 'pending'"
                      class="btn btn--danger btn--sm"
                      type="button"
                      :disabled="busyId === task.id"
                      @click="onCancel(task)"
                    >
                      {{ busyId === task.id ? '处理中…' : '取消' }}
                    </button>
                    <RouterLink
                      class="btn btn--secondary btn--sm"
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

        <div class="list-foot">
          <button v-if="hasMore && !loadingMore" class="btn btn--secondary btn--sm" type="button" @click="loadMore">
            加载更多
          </button>
          <p v-else-if="loadingMore" class="loading-row list-foot-loading">
            <span class="spinner" aria-hidden="true"></span> 正在加载…
          </p>
          <p v-else class="muted">已加载全部 {{ items.length }} 条任务</p>
        </div>
      </section>

      <AlertNote v-if="listError" kind="error">{{ listError }}</AlertNote>
    </template>
  </div>
</template>

<style scoped>
.head-row {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  flex-wrap: wrap;
}
.head-row .btn {
  margin-left: auto;
}
.filter-row {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 14px;
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
.block-id {
  display: block;
  font-size: 11.5px;
  margin-top: 4px;
}
.col-summary {
  min-width: 180px;
  max-width: 300px;
}
.col-target {
  max-width: 170px;
  word-break: break-all;
}
.col-memories {
  min-width: 200px;
  max-width: 300px;
}
.memory-list {
  display: grid;
  gap: 4px;
  margin: 0;
  padding: 0;
  list-style: none;
}
.memory-list li {
  display: flex;
  align-items: flex-start;
  gap: 5px;
  font-size: 12px;
  line-height: 1.5;
  color: var(--ink-2);
}
.memory-text {
  display: -webkit-box;
  overflow: hidden;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  word-break: break-word;
}
.col-result {
  max-width: 200px;
  font-size: 12px;
  word-break: break-all;
}
.result-link {
  color: var(--primary);
}
.col-time {
  font-size: 12.5px;
  color: var(--ink-2);
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
</style>
