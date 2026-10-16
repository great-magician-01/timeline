<script setup lang="ts">
// A-05 评论审核：评论查询与逻辑删除
import { computed, reactive, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { deleteAdminComment, listAdminComments } from '../../api/adminContent'
import { errorMessage } from '../../api/client'
import type { AdminComment } from '../../api/types'
import AlertNote from '../../components/AlertNote.vue'
import AppIcon from '../../components/AppIcon.vue'
import UserAvatar from '../../components/UserAvatar.vue'
import { useCursorList } from '../../composables/useCursorList'
import { formatDateTime } from '../../utils/datetime'

type CommentFilters = {
  q?: string
  author?: string
}

const filters = reactive({
  q: '',
  author: '',
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
} = useCursorList<AdminComment, CommentFilters>({
  fetch: (params) => listAdminComments(params),
  params: () => ({
    q: filters.q.trim() || undefined,
    author: filters.author.trim() || undefined,
  }),
})

const initialLoading = computed(() => !loaded.value && !listError.value)

const busyId = ref<string | null>(null)
const actionError = ref('')
const actionNotice = ref('')

function onSearch(): void {
  void reload()
}

async function onDelete(comment: AdminComment): Promise<void> {
  const handle = comment.author.nickname || comment.author.username
  if (
    !window.confirm(
      `确认删除「${handle}」的这条评论？删除为逻辑删除：评论不再展示，记录保留，操作会记入审计日志。`,
    )
  ) {
    return
  }
  busyId.value = comment.id
  actionError.value = ''
  actionNotice.value = ''
  try {
    await deleteAdminComment(comment.id)
    actionNotice.value = `已逻辑删除评论 ${comment.id}。`
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
            placeholder="搜索评论关键词"
            aria-label="搜索评论关键词"
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
        <AppIcon name="message" :size="26" />
        <strong>没有找到匹配的评论</strong>
        <p>尝试调整关键词或作者筛选条件。</p>
      </div>
    </section>

    <template v-else>
      <section class="card comment-list">
        <article v-for="comment in items" :key="comment.id" class="comment-row">
          <UserAvatar :user="comment.author" size="sm" />
          <div class="comment-main">
            <div class="comment-head">
              <span class="author-name">{{ comment.author.nickname || comment.author.username }}</span>
              <span v-if="comment.author.is_ai" class="badge badge--ai">AI</span>
              <span v-if="comment.status === 'deleted'" class="badge badge--danger">已删除</span>
              <span class="muted mono">@{{ comment.author.username }}</span>
              <span class="comment-time mono">{{ formatDateTime(comment.created_at) }}</span>
            </div>

            <!-- content_html 由后端清洗 -->
            <div
              v-if="comment.content_html"
              class="comment-body"
              v-html="comment.content_html"
            ></div>
            <p v-else class="muted">（无正文）</p>

            <div class="comment-foot">
              <RouterLink :to="`/admin/content/posts/${comment.post_id}`" class="post-link mono">
                原帖 {{ comment.post_id }}
              </RouterLink>
              <button
                class="btn btn--danger btn--sm"
                type="button"
                :disabled="busyId === comment.id || comment.status === 'deleted'"
                @click="onDelete(comment)"
              >
                {{ busyId === comment.id ? '删除中…' : '删除' }}
              </button>
            </div>
          </div>
        </article>

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
.self-start {
  align-self: flex-start;
}
.comment-list {
  overflow: hidden;
}
.comment-row {
  display: flex;
  gap: 10px;
  padding: 14px 16px;
  border-bottom: 1px solid var(--border);
}
.comment-main {
  min-width: 0;
  flex: 1;
}
.comment-head {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  font-size: 13.5px;
}
.author-name {
  font-weight: 700;
}
.comment-time {
  font-size: 12.5px;
  color: var(--ink-3);
}
.comment-body {
  max-height: 200px;
  overflow-y: auto;
  margin-top: 6px;
  font-size: 14px;
  line-height: 1.75;
  word-break: break-word;
}
.comment-body :deep(img) {
  max-width: 100%;
  height: auto;
}
.comment-foot {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  margin-top: 8px;
}
.post-link {
  font-size: 12.5px;
  color: var(--ink-3);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 320px;
}
.post-link:hover {
  color: var(--ink);
}
.comment-foot .btn {
  margin-left: auto;
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
