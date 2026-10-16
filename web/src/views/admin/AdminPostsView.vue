<script setup lang="ts">
// A-04 帖子审核：全可见性帖子查询与逻辑删除
import { computed, reactive, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { deleteAdminPost, listAdminPosts } from '../../api/adminContent'
import { errorMessage } from '../../api/client'
import type { AdminPost } from '../../api/types'
import AlertNote from '../../components/AlertNote.vue'
import AppIcon from '../../components/AppIcon.vue'
import UserAvatar from '../../components/UserAvatar.vue'
import { useCursorList } from '../../composables/useCursorList'
import { formatDateTime } from '../../utils/datetime'
import { plainSummary } from '../../utils/format'

type VisibilityFilter = '' | 'public' | 'followers' | 'private'
type StatusFilter = '' | 'published' | 'draft' | 'scheduled' | 'deleted'

type PostFilters = {
  visibility?: string
  status?: string
  author?: string
  q?: string
}

const VISIBILITY_LABELS: Record<string, string> = {
  public: '公开',
  followers: '仅关注者',
  private: '仅自己',
}

const STATUS_LABELS: Record<string, string> = {
  published: '已发布',
  draft: '草稿',
  scheduled: '定时',
  deleted: '已删除',
}

const filters = reactive({
  q: '',
  author: '',
  visibility: '' as VisibilityFilter,
  status: '' as StatusFilter,
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
} = useCursorList<AdminPost, PostFilters>({
  fetch: (params) => listAdminPosts(params),
  params: () => ({
    visibility: filters.visibility || undefined,
    status: filters.status || undefined,
    author: filters.author.trim() || undefined,
    q: filters.q.trim() || undefined,
  }),
})

// 首屏（含筛选切换后）尚无任何结果时的加载态
const initialLoading = computed(() => !loaded.value && !listError.value)

const busyId = ref<string | null>(null)
const actionError = ref('')
const actionNotice = ref('')

function visibilityLabel(value: string): string {
  return VISIBILITY_LABELS[value] ?? value
}

function statusLabel(value: string): string {
  return STATUS_LABELS[value] ?? value
}

function statusClass(value: string): string {
  if (value === 'deleted') return 'badge--danger'
  if (value === 'published') return ''
  return 'badge--muted'
}

function setVisibility(value: VisibilityFilter): void {
  if (filters.visibility === value) return
  filters.visibility = value
  void reload()
}

function setStatus(value: StatusFilter): void {
  if (filters.status === value) return
  filters.status = value
  void reload()
}

function onSearch(): void {
  void reload()
}

async function onDelete(post: AdminPost): Promise<void> {
  const handle = post.author.nickname || post.author.username
  if (
    !window.confirm(
      `确认删除「${handle}」的这条帖子？删除为逻辑删除：内容不再对外展示，记录与附件保留，操作会记入审计日志。`,
    )
  ) {
    return
  }
  busyId.value = post.id
  actionError.value = ''
  actionNotice.value = ''
  try {
    await deleteAdminPost(post.id)
    actionNotice.value = `已逻辑删除帖子 ${post.id}。`
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
      <form class="search-form" @submit.prevent="onSearch">
        <div class="search-box">
          <AppIcon name="search" :size="17" class="search-icon" />
          <input
            v-model="filters.q"
            class="input search-input"
            type="search"
            name="q"
            placeholder="搜索正文关键词"
            aria-label="搜索帖子关键词"
          />
        </div>
        <input
          v-model="filters.author"
          class="input author-input"
          type="search"
          name="author"
          placeholder="作者用户名"
          aria-label="按作者用户名筛选"
        />
        <button class="btn btn--primary" type="submit" :disabled="loading">
          <span v-if="loading" class="spinner" aria-hidden="true"></span>
          {{ loading ? '查询中…' : '查询' }}
        </button>
      </form>

      <div class="filter-row">
        <div class="seg" role="group" aria-label="可见性筛选">
          <button type="button" :class="{ 'is-active': filters.visibility === '' }" @click="setVisibility('')">
            全部可见性
          </button>
          <button
            type="button"
            :class="{ 'is-active': filters.visibility === 'public' }"
            @click="setVisibility('public')"
          >
            公开
          </button>
          <button
            type="button"
            :class="{ 'is-active': filters.visibility === 'followers' }"
            @click="setVisibility('followers')"
          >
            仅关注者
          </button>
          <button
            type="button"
            :class="{ 'is-active': filters.visibility === 'private' }"
            @click="setVisibility('private')"
          >
            仅自己
          </button>
        </div>

        <div class="seg" role="group" aria-label="状态筛选">
          <button type="button" :class="{ 'is-active': filters.status === '' }" @click="setStatus('')">
            全部状态
          </button>
          <button
            type="button"
            :class="{ 'is-active': filters.status === 'published' }"
            @click="setStatus('published')"
          >
            已发布
          </button>
          <button type="button" :class="{ 'is-active': filters.status === 'draft' }" @click="setStatus('draft')">
            草稿
          </button>
          <button
            type="button"
            :class="{ 'is-active': filters.status === 'scheduled' }"
            @click="setStatus('scheduled')"
          >
            定时
          </button>
          <button
            type="button"
            :class="{ 'is-active': filters.status === 'deleted' }"
            @click="setStatus('deleted')"
          >
            已删除
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
        <AppIcon name="file-text" :size="26" />
        <strong>没有找到匹配的帖子</strong>
        <p>尝试调整关键词、作者或筛选条件。</p>
      </div>
    </section>

    <template v-else>
      <section class="card table-card">
        <div class="table-scroll">
          <table class="table">
            <thead>
              <tr>
                <th>ID</th>
                <th>作者</th>
                <th>可见性</th>
                <th>状态</th>
                <th>正文摘要</th>
                <th>赞 / 评 / 转</th>
                <th>发布时间</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="post in items" :key="post.id">
                <td class="col-id">
                  <RouterLink :to="`/admin/content/posts/${post.id}`" class="mono id-link" :title="post.id">
                    {{ post.id }}
                  </RouterLink>
                </td>
                <td>
                  <span class="author">
                    <UserAvatar :user="post.author" size="sm" />
                    <span class="author-text">
                      <span class="author-name">{{ post.author.nickname || post.author.username }}</span>
                      <span class="muted">@{{ post.author.username }}</span>
                    </span>
                  </span>
                </td>
                <td>{{ visibilityLabel(post.visibility) }}</td>
                <td>
                  <span class="badge" :class="statusClass(post.status)">{{ statusLabel(post.status) }}</span>
                </td>
                <td class="col-summary">{{ plainSummary(post.content_html, 60) || '（无正文）' }}</td>
                <td class="mono">{{ post.like_count }} / {{ post.comment_count }} / {{ post.repost_count }}</td>
                <td class="col-time">{{ post.published_at ? formatDateTime(post.published_at) : '—' }}</td>
                <td>
                  <button
                    class="btn btn--danger btn--sm"
                    type="button"
                    :disabled="busyId === post.id"
                    @click="onDelete(post)"
                  >
                    {{ busyId === post.id ? '删除中…' : '删除' }}
                  </button>
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
          <p v-else class="muted">已加载全部 {{ items.length }} 条记录</p>
        </div>
      </section>

      <AlertNote v-if="listError" kind="error">{{ listError }}</AlertNote>
    </template>
  </div>
</template>

<style scoped>
.search-form {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.search-box {
  position: relative;
  flex: 1 1 220px;
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
.author-input {
  flex: 0 1 180px;
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
  font-size: 13.5px;
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
  vertical-align: middle;
}
.table tbody tr:last-child td {
  border-bottom: none;
}
.table tbody tr:hover {
  background: var(--hover);
}
.col-id {
  max-width: 150px;
}
.id-link {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12.5px;
}
.author {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 140px;
}
.author-text {
  display: flex;
  flex-direction: column;
  min-width: 0;
  line-height: 1.4;
}
.author-name {
  font-weight: 700;
  white-space: nowrap;
}
.col-summary {
  min-width: 200px;
  max-width: 320px;
}
.col-time {
  white-space: nowrap;
  font-size: 12.5px;
  color: var(--ink-2);
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
