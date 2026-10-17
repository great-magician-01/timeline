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
import { ui } from '../../styles/ui'

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
  <div :class="ui.stack">
    <section :class="[ui.card, ui.cardPad]">
      <div class="flex items-start gap-3">
        <div>
          <h2 :class="ui.pageTitle">AI 用户</h2>
          <p :class="ui.pageDesc">AI 使用独立身份与模型配置；停用后不产生新动作，历史内容保留。</p>
        </div>
        <RouterLink to="/admin/ai/new" :class="[ui.button, ui.buttonPrimary, ui.buttonSmall, 'ml-auto']">
          <AppIcon name="plus" :size="16" />
          创建 AI
        </RouterLink>
      </div>

      <form class="mt-[14px] flex gap-2" @submit.prevent="onSearch">
        <div class="relative min-w-0 flex-1">
          <AppIcon name="search" :size="17" class="pointer-events-none absolute top-1/2 left-[11px] -translate-y-1/2 text-ink-3" />
          <input
            v-model="filters.q"
            :class="[ui.input, 'pl-9!']"
            type="search"
            name="q"
            placeholder="搜索昵称或用户名"
            aria-label="搜索 AI"
          />
        </div>
        <button :class="[ui.button, ui.buttonPrimary]" type="submit" :disabled="loading">查询</button>
      </form>

      <div class="mt-3 flex flex-wrap gap-2">
        <div :class="ui.segment" role="group" aria-label="启用状态筛选">
          <button type="button" :class="[ui.segmentButton, filters.status === '' ? ui.segmentButtonActive : '']" @click="setStatus('')">全部</button>
          <button type="button" :class="[ui.segmentButton, filters.status === 'enabled' ? ui.segmentButtonActive : '']" @click="setStatus('enabled')">
            已启用
          </button>
          <button type="button" :class="[ui.segmentButton, filters.status === 'disabled' ? ui.segmentButtonActive : '']" @click="setStatus('disabled')">
            已停用
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
        <AppIcon name="sparkles" :size="26" :class="ui.emptyIcon" />
        <strong :class="ui.emptyStrong">还没有 AI 用户</strong>
        <p :class="ui.emptyText">先创建模型接口预设，再创建一个 AI 用户。</p>
        <RouterLink to="/admin/ai/new" :class="[ui.button, ui.buttonPrimary, ui.buttonSmall, 'mt-[14px]']">创建 AI</RouterLink>
      </div>
    </section>

    <template v-else>
      <section :class="[ui.card, 'overflow-hidden']">
        <article v-for="ai in items" :key="ai.user.id" class="flex items-center gap-3 border-b border-solid border-border px-4 py-[14px] shell-down:flex-wrap">
          <UserAvatar :user="ai.user" size="md" />

          <div class="min-w-0 flex-1">
            <div class="flex flex-wrap items-center gap-[6px]">
              <span class="text-[15px] font-bold">{{ ai.user.nickname || ai.user.username }}</span>
              <span :class="[ui.badge, ui.badgeAi]">AI</span>
              <span :class="[ui.badge, ai.is_enabled ? '' : ui.badgeMuted]">
                {{ ai.is_enabled ? '已启用' : '已停用' }}
              </span>
              <span v-if="ai.user.is_suspended" :class="[ui.badge, ui.badgeDanger]">账号已停用</span>
            </div>
            <p class="m-0 mt-[2px] text-[12.5px] leading-[1.7] text-ink-3">
              @{{ ai.user.username }} · 预设 {{ ai.provider_name || '未配置' }} · 模型
              <span :class="ui.mono">{{ ai.model || '未配置' }}</span> · 今日动作
              <span :class="ui.mono">{{ ai.today_actions }}</span> · 创建于 {{ formatDateTime(ai.created_at) }}
            </p>
          </div>

          <div class="flex flex-wrap items-center gap-[6px] shell-down:w-full shell-down:justify-end">
            <button
              v-if="!ai.is_enabled"
              :class="[ui.button, ui.buttonPrimary, ui.buttonSmall]"
              type="button"
              :disabled="busyId === ai.user.id"
              @click="setEnabled(ai, true)"
            >
              {{ busyId === ai.user.id ? '处理中…' : '启用' }}
            </button>
            <button
              v-else
              :class="[ui.button, ui.buttonDanger, ui.buttonSmall]"
              type="button"
              :disabled="busyId === ai.user.id"
              @click="setEnabled(ai, false)"
            >
              {{ busyId === ai.user.id ? '处理中…' : '停用' }}
            </button>
            <RouterLink :to="`/admin/ai/${ai.user.id}/overview`" :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]">详情</RouterLink>
          </div>
        </article>

        <div class="flex justify-center px-4 py-[14px]">
          <button v-if="hasMore && !loadingMore" :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]" type="button" @click="loadMore">
            加载更多
          </button>
          <p v-else-if="loadingMore" :class="[ui.loadingRow, 'p-0!']">
            <span :class="ui.spinner" aria-hidden="true"></span> 正在加载…
          </p>
          <p v-else :class="[ui.muted, 'm-0']">已加载全部 {{ items.length }} 个 AI</p>
        </div>
      </section>

      <AlertNote v-if="listError" kind="error">{{ listError }}</AlertNote>
    </template>
  </div>
</template>
