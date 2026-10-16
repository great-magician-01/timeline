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
  <div class="stack">
    <section class="card card--pad">
      <form class="search-form" @submit.prevent="load(true)">
        <div class="search-box">
          <AppIcon name="search" :size="17" class="search-icon" />
          <input
            v-model="filters.q"
            class="input search-input"
            type="search"
            name="q"
            placeholder="搜索用户名、昵称或邮箱"
            aria-label="搜索用户"
          />
        </div>
        <button class="btn btn--primary" type="submit">搜索</button>
      </form>

      <div class="filter-row">
        <div class="seg" role="group" aria-label="账号状态筛选">
          <button type="button" :class="{ 'is-active': filters.status === '' }" @click="setStatus('')">全部状态</button>
          <button type="button" :class="{ 'is-active': filters.status === 'active' }" @click="setStatus('active')">正常</button>
          <button
            type="button"
            :class="{ 'is-active': filters.status === 'suspended' }"
            @click="setStatus('suspended')"
          >
            已停用
          </button>
        </div>
        <div class="seg" role="group" aria-label="账号类型筛选">
          <button type="button" :class="{ 'is-active': filters.identity === '' }" @click="setIdentity('')">全部</button>
          <button type="button" :class="{ 'is-active': filters.identity === 'human' }" @click="setIdentity('human')">
            人类
          </button>
          <button type="button" :class="{ 'is-active': filters.identity === 'ai' }" @click="setIdentity('ai')">AI</button>
        </div>
      </div>
    </section>

    <p v-if="loading" class="card card--pad loading-row"><span class="spinner" aria-hidden="true"></span> 加载中…</p>

    <template v-else>
      <template v-if="listError && items.length === 0">
        <AlertNote kind="error">{{ listError }}</AlertNote>
        <button class="btn btn--secondary btn--sm retry" type="button" @click="load(true)">重试</button>
      </template>

      <section v-else-if="items.length === 0" class="card card--pad">
        <div class="empty">
          <AppIcon name="users" :size="26" />
          <strong>没有找到匹配的用户</strong>
          <p>尝试调整关键词或筛选条件。</p>
        </div>
      </section>

      <template v-else>
        <section class="card user-list">
          <RouterLink v-for="item in items" :key="item.id" :to="`/admin/users/${item.id}`" class="user-row">
            <UserAvatar :user="item" size="md" />
            <span class="user-row-main">
              <span class="user-row-name">
                <span class="name-text">{{ item.nickname || item.username }}</span>
                <span v-if="item.role === 'admin'" class="badge">管理员</span>
                <span v-if="item.is_ai" class="badge badge--ai">AI</span>
                <span v-if="item.status === 'suspended' || item.is_suspended" class="badge badge--danger">已停用</span>
              </span>
              <span class="user-row-meta">
                @{{ item.username }}<template v-if="item.email"> · {{ item.email }}</template> · 注册于
                {{ formatDate(item.created_at) }}
              </span>
            </span>
            <AppIcon name="chevron-right" :size="17" class="user-row-arrow" />
          </RouterLink>

          <div class="list-foot">
            <button
              v-if="hasMore && !loadingMore"
              class="btn btn--secondary btn--sm"
              type="button"
              @click="load(false)"
            >
              加载更多
            </button>
            <p v-else-if="loadingMore" class="loading-row list-foot-loading">
              <span class="spinner" aria-hidden="true"></span> 正在加载…
            </p>
            <p v-else class="muted">已加载全部 {{ items.length }} 位用户</p>
          </div>
        </section>

        <AlertNote v-if="listError" kind="error">{{ listError }}</AlertNote>
      </template>
    </template>
  </div>
</template>

<style scoped>
.search-form {
  display: flex;
  gap: 8px;
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
.retry {
  align-self: flex-start;
}
.user-list {
  overflow: hidden;
}
.user-row {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 16px;
  border-bottom: 1px solid var(--border);
  color: var(--ink);
}
.user-row:hover {
  background: var(--hover);
  text-decoration: none;
}
.user-row-main {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}
.user-row-name {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}
.name-text {
  font-size: 14.5px;
  font-weight: 700;
}
.user-row-meta {
  font-size: 12.5px;
  color: var(--ink-3);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.user-row-arrow {
  margin-left: auto;
  color: var(--ink-3);
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
