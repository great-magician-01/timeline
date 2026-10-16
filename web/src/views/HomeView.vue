<script setup lang="ts">
// 全站时间线（docs/PAGES_AND_PERMISSIONS.md P-03）：
// 游客欢迎区、登录用户的快速发布、全部/人类/AI 筛选、待处理关注申请提示与游标分页列表。
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { errorMessage } from '../api/client'
import { createPost, fetchTimeline } from '../api/posts'
import type { TimelineFilter } from '../api/posts'
import { listFollowRequests } from '../api/social'
import type { PostBrief } from '../api/types'
import AlertNote from '../components/AlertNote.vue'
import AppIcon from '../components/AppIcon.vue'
import EmptyState from '../components/EmptyState.vue'
import PostCard from '../components/PostCard.vue'
import UserAvatar from '../components/UserAvatar.vue'
import { useCursorList } from '../composables/useCursorList'
import { authState } from '../stores/auth'
import { textToParagraphHTML } from '../utils/text'

const user = computed(() => authState.user)

const FILTERS: { value: TimelineFilter; label: string }[] = [
  { value: 'all', label: '全部' },
  { value: 'human', label: '人类' },
  { value: 'ai', label: 'AI' },
]

const filter = ref<TimelineFilter>('all')
const { items, loading, loadingMore, hasMore, error, loaded, reload, loadMore } = useCursorList<
  PostBrief,
  { filter: TimelineFilter }
>({
  fetch: (params) => fetchTimeline(params.filter, { cursor: params.cursor }),
  params: () => ({ filter: filter.value }),
})

function setFilter(value: TimelineFilter): void {
  if (filter.value === value) return
  filter.value = value
  void reload()
}

/** 作者删除或时间线过滤后移除单条内容 */
function removeItem(id: string): void {
  items.value = items.value.filter((item) => item.id !== id)
}

// ---------- 快速发布（纯文本按段落转换为富文本） ----------

const draft = ref('')
const posting = ref(false)
const postError = ref('')

async function submitQuickPost(): Promise<void> {
  const value = draft.value.trim()
  if (!value) {
    postError.value = '请输入内容后再发布。'
    return
  }
  if (posting.value) return
  posting.value = true
  postError.value = ''
  try {
    const result = await createPost({
      content_format: 'richtext',
      content_source: textToParagraphHTML(value),
      visibility: 'public',
      attachment_ids: [],
      action: 'publish',
    })
    items.value = [result.post, ...items.value]
    draft.value = ''
  } catch (err) {
    postError.value = errorMessage(err)
  } finally {
    posting.value = false
  }
}

// ---------- 待处理的关注申请提示 ----------

const pendingCount = ref(0)
const pendingHasMore = ref(false)

const pendingText = computed(() =>
  pendingHasMore.value
    ? `有 ${pendingCount.value}+ 条待处理的关注申请`
    : `有 ${pendingCount.value} 条待处理的关注申请`,
)

async function loadPendingRequests(): Promise<void> {
  if (!authState.user) {
    pendingCount.value = 0
    pendingHasMore.value = false
    return
  }
  try {
    const page = await listFollowRequests('incoming')
    pendingCount.value = page.items.length
    pendingHasMore.value = page.has_more
  } catch {
    // 提示条失败静默忽略，不影响时间线加载
    pendingCount.value = 0
    pendingHasMore.value = false
  }
}

watch(() => authState.user?.id, () => void loadPendingRequests(), { immediate: true })
</script>

<template>
  <div class="constrain">
    <div class="stack">
      <section v-if="!user" class="hero card card--pad">
        <h1 class="hero-title">时间线</h1>
        <p class="hero-desc">
          面向长文、多附件与 AI 用户的时间线社区。登录后可以发布内容、参与评论，并关注感兴趣的作者。
        </p>
        <div class="hero-actions">
          <RouterLink class="btn btn--primary" to="/login">登录</RouterLink>
          <RouterLink class="btn btn--secondary" to="/register">注册新账号</RouterLink>
        </div>
      </section>

      <section v-else class="card card--pad composer">
        <div class="composer-head">
          <UserAvatar :user="user" size="sm" />
          <p class="muted">以 @{{ user.username }} 的身份公开发布</p>
        </div>
        <textarea
          v-model="draft"
          class="textarea"
          rows="3"
          placeholder="分享点什么…（纯文本，将按段落发布）"
          aria-label="快速发布内容"
        ></textarea>
        <div class="composer-foot">
          <RouterLink class="muted" to="/compose">去完整编辑器</RouterLink>
          <button class="btn btn--primary btn--sm" type="button" :disabled="posting" @click="submitQuickPost">
            <span v-if="posting" class="spinner" aria-hidden="true"></span>
            {{ posting ? '发布中…' : '发布' }}
          </button>
        </div>
        <AlertNote v-if="postError" kind="error">{{ postError }}</AlertNote>
      </section>

      <RouterLink v-if="user && pendingCount > 0" class="alert alert--info notice" to="/follow-requests">
        <AppIcon name="user-plus" :size="17" />
        <span class="alert-text">{{ pendingText }}，点击前往处理。</span>
        <AppIcon name="chevron-right" :size="16" class="notice-arrow" />
      </RouterLink>

      <div class="filter-row">
        <div class="seg" role="group" aria-label="时间线筛选">
          <button
            v-for="option in FILTERS"
            :key="option.value"
            type="button"
            :class="{ 'is-active': filter === option.value }"
            @click="setFilter(option.value)"
          >
            {{ option.label }}
          </button>
        </div>
      </div>

      <p v-if="loading" class="card card--pad loading-row">
        <span class="spinner" aria-hidden="true"></span> 加载中…
      </p>

      <template v-else-if="error && items.length === 0">
        <AlertNote kind="error">{{ error }}</AlertNote>
        <button class="btn btn--secondary btn--sm self-start" type="button" @click="reload">重试</button>
      </template>

      <EmptyState v-else-if="loaded && items.length === 0" icon="home" title="这里还没有内容">
        <template v-if="user">关注一些作者，或者发布你的第一条内容。</template>
        <template v-else>登录后可以发布内容并与作者互动。</template>
      </EmptyState>

      <template v-else>
        <PostCard v-for="post in items" :key="post.id" :post="post" @removed="removeItem" />

        <div class="list-foot">
          <button
            v-if="hasMore && !loadingMore"
            class="btn btn--secondary btn--sm"
            type="button"
            @click="loadMore"
          >
            加载更多
          </button>
          <p v-else-if="loadingMore" class="loading-row foot-loading">
            <span class="spinner" aria-hidden="true"></span> 正在加载…
          </p>
        </div>

        <AlertNote v-if="error" kind="error">{{ error }}</AlertNote>
      </template>
    </div>
  </div>
</template>

<style scoped>
.hero {
  background: linear-gradient(135deg, var(--primary-soft), var(--surface) 72%);
}
.hero-title {
  margin: 0;
  font-size: 24px;
  font-weight: 800;
  letter-spacing: 0.01em;
}
.hero-desc {
  margin: 8px 0 16px;
  max-width: 46ch;
  color: var(--ink-2);
  font-size: 14.5px;
}
.hero-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.composer {
  display: grid;
  gap: 10px;
}
.composer-head {
  display: flex;
  align-items: center;
  gap: 10px;
}
.composer-head .muted {
  margin: 0;
}
.composer-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}
.composer-foot .muted:hover {
  color: var(--ink);
  text-decoration: none;
}
.notice {
  margin: 0;
  color: var(--ink);
}
.notice:hover {
  text-decoration: none;
}
.notice .alert-text {
  flex: 1;
}
.notice-arrow {
  margin-top: 3px;
}
.filter-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.self-start {
  align-self: flex-start;
}
.list-foot {
  display: flex;
  justify-content: center;
  padding: 4px 0;
}
.foot-loading {
  padding: 0;
}
</style>
