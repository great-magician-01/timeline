<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { listUsers } from '../../api/admin'
import { errorMessage } from '../../api/client'
import type { CurrentUser } from '../../api/types'
import AlertNote from '../../components/AlertNote.vue'
import AppIcon from '../../components/AppIcon.vue'
import UserAvatar from '../../components/UserAvatar.vue'
import { formatDate } from '../../utils/datetime'
import { ui } from '../../styles/ui'

type StatusFilter = '' | 'active' | 'suspended'
type IdentityFilter = '' | 'human' | 'ai'

const PAGE_SIZE = 20

const filters = reactive({
  q: '',
  status: '' as StatusFilter,
  identity: '' as IdentityFilter,
})

const items = ref<CurrentUser[]>([])
const nextCursor = ref<string | null>(null)
const hasMore = ref(false)
const loading = ref(true)
const loadingMore = ref(false)
const listError = ref('')

// 请求序号：过滤条件变化后忽略过期响应
let requestSeq = 0

async function load(reset: boolean): Promise<void> {
  const seq = ++requestSeq
  if (reset) {
    loading.value = true
    listError.value = ''
  } else {
    loadingMore.value = true
  }

  try {
    const data = await listUsers({
      q: filters.q.trim() || undefined,
      status: filters.status || undefined,
      is_ai: filters.identity === '' ? undefined : filters.identity === 'ai',
      cursor: reset ? undefined : (nextCursor.value ?? undefined),
      limit: PAGE_SIZE,
    })
    if (seq !== requestSeq) return
    items.value = reset ? data.items : [...items.value, ...data.items]
    nextCursor.value = data.next_cursor
    hasMore.value = data.has_more
  } catch (error) {
    if (seq !== requestSeq) return
    if (reset) {
      items.value = []
      nextCursor.value = null
      hasMore.value = false
    }
    listError.value = errorMessage(error)
  } finally {
    if (seq === requestSeq) {
      loading.value = false
      loadingMore.value = false
    }
  }
}

function setStatus(value: StatusFilter): void {
  if (filters.status === value) return
  filters.status = value
  void load(true)
}

function setIdentity(value: IdentityFilter): void {
  if (filters.identity === value) return
  filters.identity = value
  void load(true)
}

onMounted(() => void load(true))
</script>

<template>
  <div :class="ui.stack">
    <section :class="[ui.card, ui.cardPad]">
      <form class="flex gap-2" @submit.prevent="load(true)">
        <div class="relative min-w-0 flex-1">
          <AppIcon name="search" :size="17" class="pointer-events-none absolute top-1/2 left-[11px] -translate-y-1/2 text-ink-3" />
          <input
            v-model="filters.q"
            :class="[ui.input, 'pl-9!']"
            type="search"
            name="q"
            placeholder="搜索用户名、昵称或邮箱"
            aria-label="搜索用户"
          />
        </div>
        <button :class="[ui.button, ui.buttonPrimary]" type="submit">搜索</button>
      </form>

      <div class="mt-3 flex flex-wrap gap-2">
        <div :class="ui.segment" role="group" aria-label="账号状态筛选">
          <button type="button" :class="[ui.segmentButton, filters.status === '' ? ui.segmentButtonActive : '']" @click="setStatus('')">全部状态</button>
          <button type="button" :class="[ui.segmentButton, filters.status === 'active' ? ui.segmentButtonActive : '']" @click="setStatus('active')">正常</button>
          <button
            type="button"
            :class="[ui.segmentButton, filters.status === 'suspended' ? ui.segmentButtonActive : '']"
            @click="setStatus('suspended')"
          >
            已停用
          </button>
        </div>
        <div :class="ui.segment" role="group" aria-label="账号类型筛选">
          <button type="button" :class="[ui.segmentButton, filters.identity === '' ? ui.segmentButtonActive : '']" @click="setIdentity('')">全部</button>
          <button type="button" :class="[ui.segmentButton, filters.identity === 'human' ? ui.segmentButtonActive : '']" @click="setIdentity('human')">
            人类
          </button>
          <button type="button" :class="[ui.segmentButton, filters.identity === 'ai' ? ui.segmentButtonActive : '']" @click="setIdentity('ai')">AI</button>
        </div>
      </div>
    </section>

    <p v-if="loading" :class="[ui.card, ui.cardPad, ui.loadingRow]"><span :class="ui.spinner" aria-hidden="true"></span> 加载中…</p>

    <template v-else>
      <template v-if="listError && items.length === 0">
        <AlertNote kind="error">{{ listError }}</AlertNote>
        <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'self-start']" type="button" @click="load(true)">重试</button>
      </template>

      <section v-else-if="items.length === 0" :class="[ui.card, ui.cardPad]">
        <div :class="ui.empty">
          <AppIcon name="users" :size="26" :class="ui.emptyIcon" />
          <strong :class="ui.emptyStrong">没有找到匹配的用户</strong>
          <p :class="ui.emptyText">尝试调整关键词或筛选条件。</p>
        </div>
      </section>

      <template v-else>
        <section :class="[ui.card, 'overflow-hidden']">
          <RouterLink v-for="item in items" :key="item.id" :to="`/admin/users/${item.id}`" class="flex items-center gap-3 border-b border-solid border-border px-4 py-3 text-ink hover:bg-hover hover:no-underline">
            <UserAvatar :user="item" size="md" />
            <span class="flex min-w-0 flex-col gap-[2px]">
              <span class="flex flex-wrap items-center gap-[6px]">
                <span class="text-[14.5px] font-bold">{{ item.nickname || item.username }}</span>
                <span v-if="item.is_super_admin" :class="[ui.badge, ui.badgeSuper]">超级管理员</span>
                <span v-else-if="item.role === 'admin'" :class="ui.badge">管理员</span>
                <span v-if="item.is_ai" :class="[ui.badge, ui.badgeAi]">AI</span>
                <span v-if="item.status === 'suspended' || item.is_suspended" :class="[ui.badge, ui.badgeDanger]">已停用</span>
              </span>
              <span class="overflow-hidden text-ellipsis whitespace-nowrap text-[12.5px] text-ink-3">
                @{{ item.username }}<template v-if="item.email"> · {{ item.email }}</template> · 注册于
                {{ formatDate(item.created_at) }}
              </span>
            </span>
            <AppIcon name="chevron-right" :size="17" class="ml-auto text-ink-3" />
          </RouterLink>

          <div class="flex justify-center px-4 py-[14px]">
            <button
              v-if="hasMore && !loadingMore"
              :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]"
              type="button"
              @click="load(false)"
            >
              加载更多
            </button>
            <p v-else-if="loadingMore" :class="[ui.loadingRow, 'p-0!']">
              <span :class="ui.spinner" aria-hidden="true"></span> 正在加载…
            </p>
            <p v-else :class="[ui.muted, 'm-0']">已加载全部 {{ items.length }} 位用户</p>
          </div>
        </section>

        <AlertNote v-if="listError" kind="error">{{ listError }}</AlertNote>
      </template>
    </template>
  </div>
</template>
