<script setup lang="ts">
// A-19 AI 调用记录：每次模型调用的明细与请求/响应内容
// 安全约定：接口不返回 API Key，页面也不显示任何密钥字段。
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { getAICall, listAICalls, listAIs, listProviders } from '../../api/aiAdmin'
import { errorMessage } from '../../api/client'
import type { AICallLog, AICallLogDetail, AIProfile, ModelProvider } from '../../api/types'
import AlertNote from '../../components/AlertNote.vue'
import AppIcon from '../../components/AppIcon.vue'
import { useCursorList } from '../../composables/useCursorList'
import { formatDateTime } from '../../utils/datetime'
import { fromDatetimeLocal } from '../../utils/format'
import { ui } from '../../styles/ui'

type CallFilters = {
  ai_id?: string
  task_id?: string
  provider_id?: string
  status?: 'success' | 'error'
  error_type?: string
  from?: string
  to?: string
}

const route = useRoute()

/** 任务页「调用记录」通过 ?task_id= 跳转，进入页面即按任务筛选 */
const initialTaskId = typeof route.query.task_id === 'string' ? route.query.task_id : ''

const filters = reactive({
  ai_id: '',
  task_id: initialTaskId,
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
    task_id: filters.task_id.trim() || undefined,
    provider_id: filters.provider_id || undefined,
    status: filters.status || undefined,
    error_type: filters.error_type.trim() || undefined,
    from: filters.from ? fromDatetimeLocal(filters.from) || undefined : undefined,
    to: filters.to ? fromDatetimeLocal(filters.to) || undefined : undefined,
  }),
})

const initialLoading = computed(() => !loaded.value && !listError.value)

// 从任务页连续跳转不同任务时复用同一路由组件，需要跟随 query 更新筛选
watch(
  () => route.query.task_id,
  (value) => {
    const next = typeof value === 'string' ? value : ''
    if (filters.task_id === next) return
    filters.task_id = next
    void reload()
  },
)

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
  filters.task_id = ''
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
  <div :class="ui.stack">
    <section :class="[ui.card, ui.cardPad]">
      <h2 :class="ui.pageTitle">AI 调用记录</h2>
      <p :class="ui.pageDesc">
        每次模型调用（含重试）都会记录状态、用量与耗时；内容按后端截断规则保存，绝不包含 API Key。
      </p>

      <AlertNote v-if="optionsError" kind="error">筛选下拉加载失败：{{ optionsError }}</AlertNote>

      <form class="mt-[14px] grid grid-cols-[repeat(auto-fit,minmax(180px,1fr))] gap-x-3 gap-y-[10px]" @submit.prevent="onFilter">
        <label class="flex flex-col gap-1 text-[12.5px]">
          <span :class="ui.muted">AI</span>
          <select v-model="filters.ai_id" :class="ui.input" name="ai_id" aria-label="按 AI 筛选">
            <option value="">全部 AI</option>
            <option v-for="ai in ais" :key="ai.user.id" :value="ai.user.id">{{ aiLabel(ai) }}</option>
          </select>
        </label>

        <label class="flex flex-col gap-1 text-[12.5px]">
          <span :class="ui.muted">预设</span>
          <select v-model="filters.provider_id" :class="ui.input" name="provider_id" aria-label="按预设筛选">
            <option value="">全部预设</option>
            <option v-for="provider in providers" :key="provider.id" :value="provider.id">
              {{ provider.name }}
            </option>
          </select>
        </label>

        <label class="flex flex-col gap-1 text-[12.5px]">
          <span :class="ui.muted">任务 ID</span>
          <input
            v-model="filters.task_id"
            :class="ui.input"
            type="search"
            name="task_id"
            placeholder="按 AI 任务筛选"
            aria-label="按任务 ID 筛选"
          />
        </label>

        <label class="flex flex-col gap-1 text-[12.5px]">
          <span :class="ui.muted">错误类型</span>
          <input
            v-model="filters.error_type"
            :class="ui.input"
            type="search"
            name="error_type"
            placeholder="例如 timeout、http_401"
            aria-label="按错误类型筛选"
          />
        </label>

        <label class="flex flex-col gap-1 text-[12.5px]">
          <span :class="ui.muted">开始时间</span>
          <input v-model="filters.from" :class="ui.input" type="datetime-local" name="from" aria-label="开始时间" />
        </label>

        <label class="flex flex-col gap-1 text-[12.5px]">
          <span :class="ui.muted">结束时间</span>
          <input v-model="filters.to" :class="ui.input" type="datetime-local" name="to" aria-label="结束时间" />
        </label>

        <div class="flex items-end gap-2">
          <button :class="[ui.button, ui.buttonPrimary, ui.buttonSmall]" type="submit" :disabled="loading">
            <span v-if="loading" :class="[ui.spinner, ui.buttonSpinner]" aria-hidden="true"></span>
            {{ loading ? '查询中…' : '查询' }}
          </button>
          <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]" type="button" @click="resetFilters">重置</button>
        </div>
      </form>

      <div class="mt-3 flex flex-wrap gap-2">
        <div :class="ui.segment" role="group" aria-label="调用状态筛选">
          <button type="button" :class="[ui.segmentButton, filters.status === '' ? ui.segmentButtonActive : '']" @click="setStatus('')">全部</button>
          <button type="button" :class="[ui.segmentButton, filters.status === 'success' ? ui.segmentButtonActive : '']" @click="setStatus('success')">
            成功
          </button>
          <button type="button" :class="[ui.segmentButton, filters.status === 'error' ? ui.segmentButtonActive : '']" @click="setStatus('error')">
            失败
          </button>
        </div>
      </div>
    </section>

    <p v-if="initialLoading" :class="[ui.card, ui.cardPad, ui.loadingRow]"><span :class="ui.spinner" aria-hidden="true"></span> 加载中…</p>

    <template v-else-if="listError && items.length === 0">
      <AlertNote kind="error">{{ listError }}</AlertNote>
      <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'self-start']" type="button" @click="reload">重试</button>
    </template>

    <section v-else-if="items.length === 0" :class="[ui.card, ui.cardPad]">
      <div :class="ui.empty">
        <AppIcon name="activity" :size="26" :class="ui.emptyIcon" />
        <strong :class="ui.emptyStrong">没有匹配的调用记录</strong>
        <p :class="ui.emptyText">调整 AI、预设、状态或时间范围后重试。</p>
      </div>
    </section>

    <template v-else>
      <section :class="[ui.card, 'overflow-hidden']">
        <div class="overflow-x-auto">
          <table class="w-full border-collapse text-[13px] [&_th]:whitespace-nowrap [&_th]:border-b [&_th]:border-solid [&_th]:border-border [&_th]:px-3 [&_th]:py-[10px] [&_th]:text-left [&_th]:text-[12.5px] [&_th]:font-semibold [&_th]:text-ink-3 [&_td]:border-b [&_td]:border-solid [&_td]:border-border [&_td]:px-3 [&_td]:py-[10px] [&_td]:align-top [&_tbody_tr:last-child_td]:border-b-0">
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
                  <td class="whitespace-nowrap text-[12.5px] text-ink-2">{{ formatDateTime(call.started_at) }}</td>
                  <td class="whitespace-nowrap">
                    <template v-if="call.ai">@{{ call.ai.username }}</template>
                    <span v-else :class="ui.muted">AI 已不存在</span>
                  </td>
                  <td class="min-w-[140px]">
                    {{ call.provider_name || '—' }}
                    <span :class="[ui.mono, 'mt-[2px] block text-[11.5px] text-ink-3']">{{ call.model || '—' }}</span>
                  </td>
                  <td>
                    <span :class="[ui.badge, call.status === 'error' ? ui.badgeDanger : '']">
                      {{ call.status === 'error' ? '失败' : '成功' }}
                    </span>
                  </td>
                  <td :class="ui.mono">{{ call.http_status ?? '—' }}</td>
                  <td class="max-w-[160px] [word-break:break-word]">{{ call.error_type || '—' }}</td>
                  <td :class="[ui.mono, 'whitespace-nowrap']">
                    {{ call.usage_input_tokens ?? '—' }} / {{ call.usage_output_tokens ?? '—' }} /
                    {{ call.usage_reasoning_tokens ?? '—' }}
                  </td>
                  <td :class="[ui.mono, 'whitespace-nowrap']">{{ call.latency_ms }} ms</td>
                  <td :class="ui.mono">{{ call.attempt }}</td>
                  <td>
                    <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]" type="button" @click="toggleDetail(call)">
                      {{ expandedId === call.id ? '收起' : '展开' }}
                    </button>
                  </td>
                </tr>

                <tr v-if="expandedId === call.id" class="[&>td]:bg-surface-subtle">
                  <td colspan="10">
                    <p v-if="detailLoading" :class="ui.loadingRow">
                      <span :class="ui.spinner" aria-hidden="true"></span> 正在加载调用详情…
                    </p>

                    <template v-else-if="detailError">
                      <AlertNote kind="error">{{ detailError }}</AlertNote>
                    </template>

                    <template v-else-if="detail">
                      <div class="flex flex-wrap items-center gap-[10px]">
                        <span :class="[ui.muted, ui.mono]">调用 {{ detail.id }}</span>
                        <span v-if="detail.truncated" :class="[ui.badge, ui.badgeMuted]">内容已截断</span>
                        <RouterLink
                          v-if="detail.task_id && detail.ai"
                          :to="`/admin/ai/${detail.ai.id}/tasks`"
                          :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'ml-auto']"
                        >
                          查看该任务
                        </RouterLink>
                        <span v-else-if="detail.task_id" :class="[ui.muted, ui.mono, 'ml-auto']">
                          任务 {{ detail.task_id }}
                        </span>
                      </div>

                      <AlertNote v-if="detail.error_message" kind="error">
                        {{ detail.error_type ? `${detail.error_type}：` : '' }}{{ detail.error_message }}
                      </AlertNote>

                      <h3 class="mx-0 mb-[6px] mt-4 text-[13px] font-bold">参数快照</h3>
                      <pre :class="[ui.mono, 'm-0 max-h-[320px] overflow-auto rounded-[10px] border border-solid border-border bg-surface px-3 py-[10px] text-[12.5px] leading-[1.7] whitespace-pre-wrap [word-break:break-word]']">{{ paramsJson(detail.params) }}</pre>

                      <h3 class="mx-0 mb-[6px] mt-4 text-[13px] font-bold">请求内容</h3>
                      <pre :class="[ui.mono, 'm-0 max-h-[320px] overflow-auto rounded-[10px] border border-solid border-border bg-surface px-3 py-[10px] text-[12.5px] leading-[1.7] whitespace-pre-wrap [word-break:break-word]']">{{ detail.request_content || '（无可展示内容）' }}</pre>

                      <h3 class="mx-0 mb-[6px] mt-4 text-[13px] font-bold">响应内容</h3>
                      <pre :class="[ui.mono, 'm-0 max-h-[320px] overflow-auto rounded-[10px] border border-solid border-border bg-surface px-3 py-[10px] text-[12.5px] leading-[1.7] whitespace-pre-wrap [word-break:break-word]']">{{ detail.response_content || '（无可展示内容）' }}</pre>
                    </template>
                  </td>
                </tr>
              </template>
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
          <p v-else :class="[ui.muted, 'm-0']">已加载全部 {{ items.length }} 条记录</p>
        </div>
      </section>

      <AlertNote v-if="listError" kind="error">{{ listError }}</AlertNote>
    </template>
  </div>
</template>
