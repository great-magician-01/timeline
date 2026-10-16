<script setup lang="ts">
// A-19 AI 调用记录：每次模型调用的明细与请求/响应内容
// 安全约定：接口不返回 API Key，页面也不显示任何密钥字段。
import { computed, onMounted, reactive, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { getAICall, listAICalls, listAIs, listProviders } from '../../api/aiAdmin'
import { errorMessage } from '../../api/client'
import type { AICallLog, AICallLogDetail, AIProfile, ModelProvider } from '../../api/types'
import AlertNote from '../../components/AlertNote.vue'
import AppIcon from '../../components/AppIcon.vue'
import { useCursorList } from '../../composables/useCursorList'
import { formatDateTime } from '../../utils/datetime'
import { fromDatetimeLocal } from '../../utils/format'

type CallFilters = {
  ai_id?: string
  provider_id?: string
  status?: 'success' | 'error'
  error_type?: string
  from?: string
  to?: string
}

const filters = reactive({
  ai_id: '',
  provider_id: '',
  status: '' as '' | 'success' | 'error',
  error_type: '',
  from: '',
  to: '',
})

const {
  items,
  loading,
  loadingMore,
  hasMore,
  error: listError,
  loaded,
  reload,
  loadMore,
} = useCursorList<AICallLog, CallFilters>({
  fetch: (params) => listAICalls(params),
  params: () => ({
    ai_id: filters.ai_id || undefined,
    provider_id: filters.provider_id || undefined,
    status: filters.status || undefined,
    error_type: filters.error_type.trim() || undefined,
    from: filters.from ? fromDatetimeLocal(filters.from) || undefined : undefined,
    to: filters.to ? fromDatetimeLocal(filters.to) || undefined : undefined,
  }),
})

const initialLoading = computed(() => !loaded.value && !listError.value)

// ---------- 下拉选项 ----------
const ais = ref<AIProfile[]>([])
const providers = ref<ModelProvider[]>([])
const optionsError = ref('')

async function loadOptions(): Promise<void> {
  optionsError.value = ''
  try {
    const [aiPage, providerPage] = await Promise.all([listAIs({ limit: 50 }), listProviders({ limit: 50 })])
    ais.value = aiPage.items
    providers.value = providerPage.items
  } catch (error) {
    optionsError.value = errorMessage(error)
  }
}

onMounted(() => void loadOptions())

function aiLabel(ai: AIProfile): string {
  return `${ai.user.nickname || ai.user.username}（@${ai.user.username}）`
}

function setStatus(value: '' | 'success' | 'error'): void {
  if (filters.status === value) return
  filters.status = value
  void reload()
}

function onFilter(): void {
  void reload()
}

function resetFilters(): void {
  filters.ai_id = ''
  filters.provider_id = ''
  filters.status = ''
  filters.error_type = ''
  filters.from = ''
  filters.to = ''
  void reload()
}

// ---------- 展开详情 ----------
const expandedId = ref<string | null>(null)
const detail = ref<AICallLogDetail | null>(null)
const detailLoading = ref(false)
const detailError = ref('')

async function toggleDetail(call: AICallLog): Promise<void> {
  if (expandedId.value === call.id) {
    expandedId.value = null
    detail.value = null
    detailError.value = ''
    return
  }
  expandedId.value = call.id
  detail.value = null
  detailError.value = ''
  detailLoading.value = true
  try {
    const data = await getAICall(call.id)
    if (expandedId.value !== call.id) return
    detail.value = data.call
  } catch (error) {
    if (expandedId.value !== call.id) return
    detailError.value = errorMessage(error)
  } finally {
    if (expandedId.value === call.id) detailLoading.value = false
  }
}

function paramsJson(value: Record<string, unknown> | undefined): string {
  if (!value || Object.keys(value).length === 0) return '（无参数快照）'
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return '（参数无法序列化）'
  }
}
</script>

<template>
  <div class="stack">
    <section class="card card--pad">
      <h2 class="page-title">AI 调用记录</h2>
      <p class="page-desc">
        每次模型调用（含重试）都会记录状态、用量与耗时；内容按后端截断规则保存，绝不包含 API Key。
      </p>

      <AlertNote v-if="optionsError" kind="error">筛选下拉加载失败：{{ optionsError }}</AlertNote>

      <form class="filter-grid" @submit.prevent="onFilter">
        <label class="filter-field">
          <span class="muted">AI</span>
          <select v-model="filters.ai_id" class="input" name="ai_id" aria-label="按 AI 筛选">
            <option value="">全部 AI</option>
            <option v-for="ai in ais" :key="ai.user.id" :value="ai.user.id">{{ aiLabel(ai) }}</option>
          </select>
        </label>

        <label class="filter-field">
          <span class="muted">预设</span>
          <select v-model="filters.provider_id" class="input" name="provider_id" aria-label="按预设筛选">
            <option value="">全部预设</option>
            <option v-for="provider in providers" :key="provider.id" :value="provider.id">
              {{ provider.name }}
            </option>
          </select>
        </label>

        <label class="filter-field">
          <span class="muted">错误类型</span>
          <input
            v-model="filters.error_type"
            class="input"
            type="search"
            name="error_type"
            placeholder="例如 timeout、http_401"
            aria-label="按错误类型筛选"
          />
        </label>

        <label class="filter-field">
          <span class="muted">开始时间</span>
          <input v-model="filters.from" class="input" type="datetime-local" name="from" aria-label="开始时间" />
        </label>

        <label class="filter-field">
          <span class="muted">结束时间</span>
          <input v-model="filters.to" class="input" type="datetime-local" name="to" aria-label="结束时间" />
        </label>

        <div class="filter-actions">
          <button class="btn btn--primary btn--sm" type="submit" :disabled="loading">
            <span v-if="loading" class="spinner" aria-hidden="true"></span>
            {{ loading ? '查询中…' : '查询' }}
          </button>
          <button class="btn btn--secondary btn--sm" type="button" @click="resetFilters">重置</button>
        </div>
      </form>

      <div class="filter-row">
        <div class="seg" role="group" aria-label="调用状态筛选">
          <button type="button" :class="{ 'is-active': filters.status === '' }" @click="setStatus('')">全部</button>
          <button type="button" :class="{ 'is-active': filters.status === 'success' }" @click="setStatus('success')">
            成功
          </button>
          <button type="button" :class="{ 'is-active': filters.status === 'error' }" @click="setStatus('error')">
            失败
          </button>
        </div>
      </div>
    </section>

    <p v-if="initialLoading" class="card card--pad loading-row"><span class="spinner" aria-hidden="true"></span> 加载中…</p>

    <template v-else-if="listError && items.length === 0">
      <AlertNote kind="error">{{ listError }}</AlertNote>
      <button class="btn btn--secondary btn--sm self-start" type="button" @click="reload">重试</button>
    </template>

    <section v-else-if="items.length === 0" class="card card--pad">
      <div class="empty">
        <AppIcon name="activity" :size="26" />
        <strong>没有匹配的调用记录</strong>
        <p>调整 AI、预设、状态或时间范围后重试。</p>
      </div>
    </section>

    <template v-else>
      <section class="card table-card">
        <div class="table-scroll">
          <table class="table">
            <thead>
              <tr>
                <th>时间</th>
                <th>AI</th>
                <th>预设 / 模型</th>
                <th>状态</th>
                <th>HTTP</th>
                <th>错误类型</th>
                <th>Token（入/出/推理）</th>
                <th>耗时</th>
                <th>尝试</th>
                <th>详情</th>
              </tr>
            </thead>
            <tbody>
              <template v-for="call in items" :key="call.id">
                <tr>
                  <td class="nowrap col-time">{{ formatDateTime(call.started_at) }}</td>
                  <td class="nowrap">
                    <template v-if="call.ai">@{{ call.ai.username }}</template>
                    <span v-else class="muted">AI 已不存在</span>
                  </td>
                  <td class="col-provider">
                    {{ call.provider_name || '—' }}
                    <span class="muted mono block-id">{{ call.model || '—' }}</span>
                  </td>
                  <td>
                    <span class="badge" :class="call.status === 'error' ? 'badge--danger' : ''">
                      {{ call.status === 'error' ? '失败' : '成功' }}
                    </span>
                  </td>
                  <td class="mono">{{ call.http_status ?? '—' }}</td>
                  <td class="col-error">{{ call.error_type || '—' }}</td>
                  <td class="mono nowrap">
                    {{ call.usage_input_tokens ?? '—' }} / {{ call.usage_output_tokens ?? '—' }} /
                    {{ call.usage_reasoning_tokens ?? '—' }}
                  </td>
                  <td class="mono nowrap">{{ call.latency_ms }} ms</td>
                  <td class="mono">{{ call.attempt }}</td>
                  <td>
                    <button class="btn btn--secondary btn--sm" type="button" @click="toggleDetail(call)">
                      {{ expandedId === call.id ? '收起' : '展开' }}
                    </button>
                  </td>
                </tr>

                <tr v-if="expandedId === call.id" class="detail-row">
                  <td colspan="10">
                    <p v-if="detailLoading" class="loading-row">
                      <span class="spinner" aria-hidden="true"></span> 正在加载调用详情…
                    </p>

                    <template v-else-if="detailError">
                      <AlertNote kind="error">{{ detailError }}</AlertNote>
                    </template>

                    <template v-else-if="detail">
                      <div class="detail-head">
                        <span class="muted mono">调用 {{ detail.id }}</span>
                        <span v-if="detail.truncated" class="badge badge--muted">内容已截断</span>
                        <RouterLink
                          v-if="detail.task_id && detail.ai"
                          :to="`/admin/ai/${detail.ai.id}/tasks`"
                          class="btn btn--secondary btn--sm detail-link"
                        >
                          查看该任务
                        </RouterLink>
                        <span v-else-if="detail.task_id" class="muted mono detail-link">
                          任务 {{ detail.task_id }}
                        </span>
                      </div>

                      <AlertNote v-if="detail.error_message" kind="error">
                        {{ detail.error_type ? `${detail.error_type}：` : '' }}{{ detail.error_message }}
                      </AlertNote>

                      <h3 class="detail-title">参数快照</h3>
                      <pre class="mono block-body">{{ paramsJson(detail.params) }}</pre>

                      <h3 class="detail-title">请求内容</h3>
                      <pre class="mono block-body">{{ detail.request_content || '（无可展示内容）' }}</pre>

                      <h3 class="detail-title">响应内容</h3>
                      <pre class="mono block-body">{{ detail.response_content || '（无可展示内容）' }}</pre>
                    </template>
                  </td>
                </tr>
              </template>
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
          <p v-else class="muted">已加载全部 {{ items.length }} 条记录</p>
        </div>
      </section>

      <AlertNote v-if="listError" kind="error">{{ listError }}</AlertNote>
    </template>
  </div>
</template>

<style scoped>
.filter-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 10px 12px;
  margin-top: 14px;
}
.filter-field {
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: 12.5px;
}
.filter-actions {
  display: flex;
  align-items: flex-end;
  gap: 8px;
}
.filter-row {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 12px;
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
.col-time {
  font-size: 12.5px;
  color: var(--ink-2);
}
.col-provider {
  min-width: 140px;
}
.block-id {
  display: block;
  font-size: 11.5px;
  margin-top: 2px;
}
.col-error {
  max-width: 160px;
  word-break: break-word;
}
.detail-row td {
  background: var(--surface-subtle);
}
.detail-head {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.detail-link {
  margin-left: auto;
}
.detail-title {
  margin: 16px 0 6px;
  font-size: 13px;
  font-weight: 700;
}
.block-body {
  max-height: 320px;
  overflow: auto;
  margin: 0;
  padding: 12px 14px;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--surface);
  font-size: 12.5px;
  line-height: 1.7;
  white-space: pre-wrap;
  word-break: break-word;
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
