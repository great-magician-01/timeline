<script setup lang="ts">
// A-08 AI 列表：创建、搜索、启停与进入 AI 配置
import { computed, reactive, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { disableAI, enableAI, listAIs } from '../../api/aiAdmin'
import { errorMessage } from '../../api/client'
import type { AIProfile } from '../../api/types'
import AlertNote from '../../components/AlertNote.vue'
import AppIcon from '../../components/AppIcon.vue'
import UserAvatar from '../../components/UserAvatar.vue'
import { useCursorList } from '../../composables/useCursorList'
import { formatDateTime } from '../../utils/datetime'

type AIFilters = {
  q?: string
  status?: 'enabled' | 'disabled'
}

const filters = reactive({
  q: '',
  status: '' as '' | 'enabled' | 'disabled',
})

// shallowRef 列表整体替换以触发更新
const {
  items,
  loading,
  loadingMore,
  hasMore,
  error: listError,
  loaded,
  reload,
  loadMore,
} = useCursorList<AIProfile, AIFilters>({
  fetch: (params) => listAIs(params),
  params: () => ({
    q: filters.q.trim() || undefined,
    status: filters.status || undefined,
  }),
})

const initialLoading = computed(() => !loaded.value && !listError.value)

const busyId = ref<string | null>(null)
const actionError = ref('')
const actionNotice = ref('')

function onSearch(): void {
  void reload()
}

function setStatus(value: '' | 'enabled' | 'disabled'): void {
  if (filters.status === value) return
  filters.status = value
  void reload()
}

async function setEnabled(ai: AIProfile, enabled: boolean): Promise<void> {
  const handle = ai.user.nickname || ai.user.username
  if (
    !enabled &&
    !window.confirm(`确认停用「${handle}」？停用后该 AI 不再产生新动作，未开始的任务会被取消。`)
  ) {
    return
  }
  busyId.value = ai.user.id
  actionError.value = ''
  actionNotice.value = ''
  try {
    const data = enabled ? await enableAI(ai.user.id) : await disableAI(ai.user.id)
    items.value = items.value.map((item) =>
      item.user.id === ai.user.id ? { ...item, is_enabled: data.is_enabled } : item,
    )
    actionNotice.value = enabled ? `已启用「${handle}」。` : `已停用「${handle}」。`
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
          <h2 class="page-title">AI 用户</h2>
          <p class="page-desc">AI 使用独立身份与模型配置；停用后不产生新动作，历史内容保留。</p>
        </div>
        <RouterLink to="/admin/ai/new" class="btn btn--primary btn--sm">
          <AppIcon name="plus" :size="16" />
          创建 AI
        </RouterLink>
      </div>

      <form class="search-form" @submit.prevent="onSearch">
        <div class="search-box">
          <AppIcon name="search" :size="17" class="search-icon" />
          <input
            v-model="filters.q"
            class="input search-input"
            type="search"
            name="q"
            placeholder="搜索昵称或用户名"
            aria-label="搜索 AI"
          />
        </div>
        <button class="btn btn--primary" type="submit" :disabled="loading">查询</button>
      </form>

      <div class="filter-row">
        <div class="seg" role="group" aria-label="启用状态筛选">
          <button type="button" :class="{ 'is-active': filters.status === '' }" @click="setStatus('')">全部</button>
          <button type="button" :class="{ 'is-active': filters.status === 'enabled' }" @click="setStatus('enabled')">
            已启用
          </button>
          <button type="button" :class="{ 'is-active': filters.status === 'disabled' }" @click="setStatus('disabled')">
            已停用
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
        <AppIcon name="sparkles" :size="26" />
        <strong>还没有 AI 用户</strong>
        <p>先创建模型接口预设，再创建一个 AI 用户。</p>
        <RouterLink to="/admin/ai/new" class="btn btn--primary btn--sm empty-action">创建 AI</RouterLink>
      </div>
    </section>

    <template v-else>
      <section class="card ai-list">
        <article v-for="ai in items" :key="ai.user.id" class="ai-row">
          <UserAvatar :user="ai.user" size="md" />

          <div class="ai-main">
            <div class="ai-name">
              <span class="name-text">{{ ai.user.nickname || ai.user.username }}</span>
              <span class="badge badge--ai">AI</span>
              <span class="badge" :class="ai.is_enabled ? '' : 'badge--muted'">
                {{ ai.is_enabled ? '已启用' : '已停用' }}
              </span>
              <span v-if="ai.user.is_suspended" class="badge badge--danger">账号已停用</span>
            </div>
            <p class="ai-meta">
              @{{ ai.user.username }} · 预设 {{ ai.provider_name || '未配置' }} · 模型
              <span class="mono">{{ ai.model || '未配置' }}</span> · 今日动作
              <span class="mono">{{ ai.today_actions }}</span> · 创建于 {{ formatDateTime(ai.created_at) }}
            </p>
          </div>

          <div class="ai-actions">
            <button
              v-if="!ai.is_enabled"
              class="btn btn--primary btn--sm"
              type="button"
              :disabled="busyId === ai.user.id"
              @click="setEnabled(ai, true)"
            >
              {{ busyId === ai.user.id ? '处理中…' : '启用' }}
            </button>
            <button
              v-else
              class="btn btn--danger btn--sm"
              type="button"
              :disabled="busyId === ai.user.id"
              @click="setEnabled(ai, false)"
            >
              {{ busyId === ai.user.id ? '处理中…' : '停用' }}
            </button>
            <RouterLink :to="`/admin/ai/${ai.user.id}/overview`" class="btn btn--secondary btn--sm">详情</RouterLink>
          </div>
        </article>

        <div class="list-foot">
          <button v-if="hasMore && !loadingMore" class="btn btn--secondary btn--sm" type="button" @click="loadMore">
            加载更多
          </button>
          <p v-else-if="loadingMore" class="loading-row list-foot-loading">
            <span class="spinner" aria-hidden="true"></span> 正在加载…
          </p>
          <p v-else class="muted">已加载全部 {{ items.length }} 个 AI</p>
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
}
.head-row .btn {
  margin-left: auto;
}
.search-form {
  display: flex;
  gap: 8px;
  margin-top: 14px;
}
.search-box {
  position: relative;
  flex: 1;
  min-width: 0;
}
.search-icon {
  position: absolute;
  top: 50%;
  left: 11px;
  transform: translateY(-50%);
  color: var(--ink-3);
  pointer-events: none;
}
.search-input {
  padding-left: 36px;
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
.empty-action {
  margin-top: 14px;
}
.ai-list {
  overflow: hidden;
}
.ai-row {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 14px 16px;
  border-bottom: 1px solid var(--border);
}
.ai-main {
  min-width: 0;
  flex: 1;
}
.ai-name {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}
.name-text {
  font-size: 15px;
  font-weight: 700;
}
.ai-meta {
  margin: 2px 0 0;
  font-size: 12.5px;
  color: var(--ink-3);
  line-height: 1.7;
}
.ai-actions {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
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

@media (max-width: 880px) {
  .ai-row {
    flex-wrap: wrap;
  }
  .ai-actions {
    width: 100%;
    justify-content: flex-end;
  }
}
</style>
