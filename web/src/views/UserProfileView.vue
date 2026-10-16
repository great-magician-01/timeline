<script setup lang="ts">
// 个人主页（docs/PAGES_AND_PERMISSIONS.md P-09）：
// 资料与统计、关注操作，以及帖子 / 回复 / 转发三个标签页。
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { ApiError, errorMessage } from '../api/client'
import { fetchUserPosts } from '../api/posts'
import type { ProfileTab } from '../api/posts'
import { fetchProfile, followUser, unfollowUser } from '../api/social'
import type { ProfileSummary } from '../api/social'
import type { PostBrief, TimelineCommentItem } from '../api/types'
import AlertNote from '../components/AlertNote.vue'
import AppIcon from '../components/AppIcon.vue'
import EmptyState from '../components/EmptyState.vue'
import PostCard from '../components/PostCard.vue'
import UserAvatar from '../components/UserAvatar.vue'
import { useCursorList } from '../composables/useCursorList'
import { authState } from '../stores/auth'
import { plainSummary, relativeTime, timeTitle } from '../utils/format'

const route = useRoute()
const router = useRouter()
const username = computed(() => String(route.params.username ?? ''))
const my = computed(() => authState.user)

const profile = ref<ProfileSummary | null>(null)
const loading = ref(true)
const notFound = ref(false)
const loadError = ref('')

const isSelf = computed(
  () => Boolean(my.value && profile.value && my.value.username === profile.value.user.username),
)
const isSuspended = computed(() => Boolean(profile.value?.user.is_suspended))

const TABS: { value: ProfileTab; label: string }[] = [
  { value: 'posts', label: '帖子' },
  { value: 'replies', label: '回复' },
  { value: 'reposts', label: '转发' },
]
const tab = ref<ProfileTab>('posts')

const {
  items,
  loading: listLoading,
  loadingMore,
  hasMore,
  error: listError,
  loaded: listLoaded,
  reload: reloadList,
  loadMore,
} = useCursorList<PostBrief | TimelineCommentItem, { username: string; tab: ProfileTab }>({
  fetch: (params) => fetchUserPosts(params.username, params.tab, { cursor: params.cursor }),
  params: () => ({ username: username.value, tab: tab.value }),
  immediate: false,
})

/** 回复标签页返回评论条目，用判别联合区分渲染分支 */
type ProfileRow =
  | { type: 'post'; item: PostBrief }
  | { type: 'comment'; item: TimelineCommentItem }

function isCommentItem(item: PostBrief | TimelineCommentItem): item is TimelineCommentItem {
  return (item as { kind?: string }).kind === 'comment'
}

const rows = computed<ProfileRow[]>(() =>
  items.value.map((item) =>
    isCommentItem(item) ? { type: 'comment', item } : { type: 'post', item },
  ),
)

const emptyTitle = computed(() => {
  if (tab.value === 'replies') return '还没有回复'
  if (tab.value === 'reposts') return '还没有转发'
  return '还没有帖子'
})

const emptyDescription = computed(() => {
  if (isSelf.value) return '发布内容后会显示在这里。'
  return 'TA 发布的内容会显示在这里。'
})

function removeItem(id: string): void {
  items.value = items.value.filter((item) => item.id !== id)
}

function openPost(postId: string): void {
  void router.push(`/posts/${postId}`)
}

// ---------- 资料加载 ----------

async function loadProfile(): Promise<void> {
  loading.value = true
  notFound.value = false
  loadError.value = ''
  profile.value = null
  try {
    const data = await fetchProfile(username.value)
    profile.value = data
    await reloadList()
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) notFound.value = true
    else loadError.value = errorMessage(err)
  } finally {
    loading.value = false
  }
}

watch(
  username,
  () => {
    tab.value = 'posts'
    void loadProfile()
  },
  { immediate: true },
)

function setTab(value: ProfileTab): void {
  if (tab.value === value) return
  tab.value = value
  void reloadList()
}

// ---------- 关注操作 ----------

const followBusy = ref(false)
const actionError = ref('')

const followLabel = computed(() => {
  if (profile.value?.follow_state === 'following') return '已关注'
  if (profile.value?.follow_state === 'pending') return '已申请'
  return '关注'
})

async function toggleFollow(): Promise<void> {
  const current = profile.value
  if (!current || followBusy.value || isSelf.value) return
  if (!my.value) {
    void router.push({ name: 'login', query: { redirect: route.fullPath } })
    return
  }
  const state = current.follow_state
  if (state === 'following' || state === 'pending') {
    if (!window.confirm(state === 'pending' ? '确认取消关注申请？' : '确认取消关注？')) return
    followBusy.value = true
    actionError.value = ''
    try {
      const result = await unfollowUser(current.user.username)
      current.follow_state = result.follow_state
    } catch (err) {
      actionError.value = errorMessage(err)
    } finally {
      followBusy.value = false
    }
    return
  }
  followBusy.value = true
  actionError.value = ''
  try {
    const result = await followUser(current.user.username)
    current.follow_state = result.follow_state
  } catch (err) {
    actionError.value = errorMessage(err)
  } finally {
    followBusy.value = false
  }
}
</script>

<template>
  <div class="constrain">
    <div class="stack">
      <p v-if="loading" class="card card--pad loading-row">
        <span class="spinner" aria-hidden="true"></span> 加载中…
      </p>

      <section v-else-if="notFound" class="card card--pad">
        <EmptyState icon="user" title="用户不存在或不可见" description="该用户名可能未被注册，或账号不应被展示。">
          <RouterLink class="btn btn--secondary btn--sm empty-action" to="/">返回首页</RouterLink>
        </EmptyState>
      </section>

      <template v-else-if="loadError">
        <AlertNote kind="error">{{ loadError }}</AlertNote>
        <button class="btn btn--secondary btn--sm self-start" type="button" @click="loadProfile">重试</button>
      </template>

      <template v-else-if="profile">
        <section class="card card--pad">
          <div class="profile-head">
            <UserAvatar :user="profile.user" size="lg" />
            <div class="profile-info">
              <p class="profile-name">
                <span class="name-text">{{ profile.user.nickname || profile.user.username }}</span>
                <span v-if="profile.user.is_ai" class="badge badge--ai">AI</span>
                <span v-if="profile.user.is_suspended" class="badge badge--danger">已停用</span>
                <span v-else-if="isSelf" class="badge badge--muted">这是你</span>
              </p>
              <p class="muted">@{{ profile.user.username }}</p>
            </div>

            <div class="profile-actions">
              <template v-if="!isSelf && !isSuspended">
                <button
                  v-if="profile.follow_state !== 'incoming_pending'"
                  class="btn btn--secondary btn--sm"
                  type="button"
                  :disabled="followBusy"
                  @click="toggleFollow"
                >
                  <AppIcon v-if="profile.follow_state === 'none'" name="user-plus" :size="16" />
                  <AppIcon v-else name="check" :size="16" />
                  {{ followLabel }}
                </button>
                <RouterLink
                  v-else-if="my"
                  class="btn btn--secondary btn--sm"
                  :to="{ name: 'follow-requests' }"
                >
                  对方申请关注你
                </RouterLink>
              </template>
              <RouterLink v-if="isSelf" class="btn btn--secondary btn--sm" to="/settings/profile">
                <AppIcon name="pencil" :size="16" /> 编辑资料
              </RouterLink>
            </div>
          </div>

          <p v-if="profile.bio" class="profile-bio">{{ profile.bio }}</p>
          <p v-else class="muted no-bio">还没有填写简介。</p>

          <ul class="stats">
            <li class="stat">
              <strong>{{ profile.post_count }}</strong>
              <span>帖子</span>
            </li>
            <li class="stat">
              <RouterLink :to="`/users/${profile.user.username}/following`">
                <strong>{{ profile.following_count }}</strong>
                <span>关注</span>
              </RouterLink>
            </li>
            <li class="stat">
              <RouterLink :to="`/users/${profile.user.username}/followers`">
                <strong>{{ profile.follower_count }}</strong>
                <span>粉丝</span>
              </RouterLink>
            </li>
          </ul>
        </section>

        <AlertNote v-if="isSuspended" kind="warning">
          该账号已被停用，资料与内容仅只读展示，所有互动入口已关闭。
        </AlertNote>

        <AlertNote v-if="actionError" kind="error">{{ actionError }}</AlertNote>

        <section class="card card--pad tabs-card">
          <div class="tabs" role="tablist">
            <button
              v-for="option in TABS"
              :key="option.value"
              type="button"
              class="tab-link"
              :class="{ 'is-active': tab === option.value }"
              @click="setTab(option.value)"
            >
              {{ option.label }}
            </button>
          </div>
        </section>

        <p v-if="listLoading" class="card card--pad loading-row">
          <span class="spinner" aria-hidden="true"></span> 加载中…
        </p>

        <template v-else-if="listError && rows.length === 0">
          <AlertNote kind="error">{{ listError }}</AlertNote>
          <button class="btn btn--secondary btn--sm self-start" type="button" @click="reloadList">重试</button>
        </template>

        <EmptyState
          v-else-if="listLoaded && rows.length === 0"
          icon="home"
          :title="emptyTitle"
          :description="emptyDescription"
        />

        <template v-else>
          <template v-for="row in rows" :key="row.item.id">
            <PostCard v-if="row.type === 'post'" :post="row.item" @removed="removeItem" />
            <div
              v-else
              class="reply-row card card--pad"
              role="link"
              tabindex="0"
              aria-label="查看评论所属帖子"
              @click="openPost(row.item.post.id)"
              @keydown.enter="openPost(row.item.post.id)"
            >
              <div class="reply-head">
                <UserAvatar :user="row.item.author" size="sm" />
                <strong>{{ row.item.author.nickname || row.item.author.username }}</strong>
                <span class="handle">@{{ row.item.author.username }}</span>
                <span v-if="row.item.author.is_ai" class="badge badge--ai">AI</span>
                <span :title="timeTitle(row.item.created_at)" class="muted">
                  {{ relativeTime(row.item.created_at) }}
                </span>
              </div>
              <div class="reply-content" v-html="row.item.content_html"></div>
              <p class="reply-context">
                <AppIcon name="message" :size="14" />
                <span>评论了「{{ plainSummary(row.item.post.content_html) }}」</span>
              </p>
            </div>
          </template>

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

          <AlertNote v-if="listError" kind="error">{{ listError }}</AlertNote>
        </template>
      </template>
    </div>
  </div>
</template>

<style scoped>
.profile-head {
  display: flex;
  align-items: center;
  gap: 14px;
}
.profile-info {
  min-width: 0;
}
.profile-name {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  margin: 0;
  font-size: 17px;
  font-weight: 800;
}
.profile-info .muted {
  margin: 2px 0 0;
}
.profile-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-left: auto;
}
.profile-bio {
  margin: 14px 0 0;
  font-size: 14.5px;
  line-height: 1.8;
  white-space: pre-wrap;
  word-break: break-word;
}
.no-bio {
  margin: 14px 0 0;
}
.stats {
  display: flex;
  gap: 22px;
  margin: 16px 0 0;
  padding: 0;
  list-style: none;
}
.stat strong {
  display: block;
  font-size: 17px;
  font-weight: 800;
  line-height: 1.3;
}
.stat span {
  font-size: 12.5px;
  color: var(--ink-3);
}
.stat a {
  color: var(--ink);
}
.stat a:hover {
  text-decoration: none;
  color: var(--primary);
}
.tabs-card {
  padding-top: 10px;
  padding-bottom: 10px;
}
.reply-row {
  display: grid;
  gap: 8px;
  color: var(--ink);
  cursor: pointer;
}
.reply-row:hover {
  border-color: var(--soft-hover);
}
.reply-head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  font-size: 14px;
}
.handle {
  font-size: 12.5px;
  color: var(--ink-3);
}
.reply-content {
  font-size: 14.5px;
  line-height: 1.7;
  word-break: break-word;
}
.reply-content :deep(a.tl-tag),
.reply-content :deep(a.tl-mention) {
  color: var(--primary);
  font-weight: 600;
}
.reply-context {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 0;
  padding: 8px 10px;
  border-radius: 8px;
  background: var(--soft);
  color: var(--ink-3);
  font-size: 12.5px;
}
.self-start {
  align-self: flex-start;
}
.empty-action {
  margin-top: 12px;
}
.list-foot {
  display: flex;
  justify-content: center;
  padding: 4px 0;
}
.foot-loading {
  padding: 0;
}
@media (max-width: 880px) {
  .profile-head {
    flex-wrap: wrap;
  }
  .profile-actions {
    margin-left: 0;
    width: 100%;
  }
  .stats {
    gap: 18px;
  }
}
</style>
