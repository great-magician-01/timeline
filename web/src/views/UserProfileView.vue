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
import { ui } from '../styles/ui'
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

/** 当前用户对指定原帖的纯转发项（用于插入前去重与取消后移除） */
function isMyRepostItem(
  entry: PostBrief | TimelineCommentItem,
  userId: string,
  originalId: string,
): boolean {
  return (
    !isCommentItem(entry) &&
    entry.kind === 'repost' &&
    entry.author.id === userId &&
    entry.repost_of?.id === originalId
  )
}

/**
 * 转发状态变化：仅本人主页的「转发」页签同步插入/移除；
 * 别人的主页展示的是对方的转发，其它页签的接口不返回纯转发，均只依赖卡片提示反馈。
 */
function onRepostChange(payload: { reposted: boolean; postId: string; item?: PostBrief }): void {
  const currentUser = my.value
  if (!currentUser || !isSelf.value || tab.value !== 'reposts') return
  if (!payload.reposted) {
    items.value = items.value.filter((entry) => !isMyRepostItem(entry, currentUser.id, payload.postId))
    return
  }
  const created = payload.item
  if (!created) return
  items.value = [created, ...items.value.filter((entry) => !isMyRepostItem(entry, currentUser.id, payload.postId))]
}

/** 引用转发发布成功：仅本人主页的「帖子」/「转发」页签把新帖插到顶部 */
function onPostCreated(post: PostBrief): void {
  if (!isSelf.value || (tab.value !== 'posts' && tab.value !== 'reposts')) return
  if (items.value.some((entry) => entry.id === post.id)) return
  items.value = [post, ...items.value]
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
  <div :class="[ui.constrain]">
    <div :class="[ui.stack]">
      <p v-if="loading" :class="[ui.card, ui.loadingRow]">
        <span :class="[ui.spinner]" aria-hidden="true"></span> 加载中…
      </p>

      <section v-else-if="notFound" :class="[ui.card, ui.cardPad]">
        <EmptyState icon="user" title="用户不存在或不可见" description="该用户名可能未被注册，或账号不应被展示。">
          <RouterLink :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'mt-3']" to="/">返回首页</RouterLink>
        </EmptyState>
      </section>

      <template v-else-if="loadError">
        <AlertNote kind="error">{{ loadError }}</AlertNote>
        <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'self-start']" type="button" @click="loadProfile">重试</button>
      </template>

      <template v-else-if="profile">
        <section :class="[ui.card, ui.cardPad]">
          <div class="flex items-center gap-3.5 shell-down:flex-wrap">
            <UserAvatar :user="profile.user" size="lg" />
            <div class="min-w-0">
              <p class="m-0 flex flex-wrap items-center gap-1.5 text-[17px] font-extrabold">
                <span>{{ profile.user.nickname || profile.user.username }}</span>
                <span v-if="profile.user.is_ai" :class="[ui.badge, ui.badgeAi]">AI</span>
                <span v-if="profile.user.is_suspended" :class="[ui.badge, ui.badgeDanger]">已停用</span>
                <span v-else-if="isSelf" :class="[ui.badge, ui.badgeMuted]">这是你</span>
              </p>
              <p :class="[ui.muted, 'm-0 mt-0.5']">@{{ profile.user.username }}</p>
            </div>

            <div class="ml-auto flex flex-wrap gap-2 shell-down:ml-0 shell-down:w-full">
              <template v-if="!isSelf && !isSuspended">
                <button
                  v-if="profile.follow_state !== 'incoming_pending'"
                  :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]"
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
                  :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]"
                  :to="{ name: 'follow-requests' }"
                >
                  对方申请关注你
                </RouterLink>
              </template>
              <RouterLink v-if="isSelf" :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]" to="/settings/profile">
                <AppIcon name="pencil" :size="16" /> 编辑资料
              </RouterLink>
            </div>
          </div>

          <p v-if="profile.bio" class="m-0 mt-3.5 text-[14.5px] leading-[1.8] whitespace-pre-wrap [word-break:break-word]">{{ profile.bio }}</p>
          <p v-else :class="[ui.muted, 'm-0 mt-3.5']">还没有填写简介。</p>

          <ul class="m-0 mt-4 flex list-none gap-[22px] p-0 shell-down:gap-[18px]">
            <li>
              <strong class="block text-[17px] font-extrabold leading-[1.3]">{{ profile.post_count }}</strong>
              <span class="text-[12.5px] text-ink-3">帖子</span>
            </li>
            <li>
              <RouterLink :to="`/users/${profile.user.username}/following`" class="text-ink hover:text-primary hover:no-underline">
                <strong class="block text-[17px] font-extrabold leading-[1.3]">{{ profile.following_count }}</strong>
                <span class="text-[12.5px] text-ink-3">关注</span>
              </RouterLink>
            </li>
            <li>
              <RouterLink :to="`/users/${profile.user.username}/followers`" class="text-ink hover:text-primary hover:no-underline">
                <strong class="block text-[17px] font-extrabold leading-[1.3]">{{ profile.follower_count }}</strong>
                <span class="text-[12.5px] text-ink-3">粉丝</span>
              </RouterLink>
            </li>
          </ul>
        </section>

        <AlertNote v-if="isSuspended" kind="warning">
          该账号已被停用，资料与内容仅只读展示，所有互动入口已关闭。
        </AlertNote>

        <AlertNote v-if="actionError" kind="error">{{ actionError }}</AlertNote>

        <section :class="[ui.card, 'px-[22px] pt-2.5 pb-2.5']">
          <div :class="[ui.tabs]" role="tablist">
            <button
              v-for="option in TABS"
              :key="option.value"
              type="button"
              :class="[ui.tabLink, tab === option.value && ui.tabLinkActive]"
              @click="setTab(option.value)"
            >
              {{ option.label }}
            </button>
          </div>
        </section>

        <p v-if="listLoading" :class="[ui.card, ui.loadingRow]">
          <span :class="[ui.spinner]" aria-hidden="true"></span> 加载中…
        </p>

        <template v-else-if="listError && rows.length === 0">
          <AlertNote kind="error">{{ listError }}</AlertNote>
          <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'self-start']" type="button" @click="reloadList">重试</button>
        </template>

        <EmptyState
          v-else-if="listLoaded && rows.length === 0"
          icon="home"
          :title="emptyTitle"
          :description="emptyDescription"
        />

        <template v-else>
          <template v-for="row in rows" :key="row.item.id">
            <PostCard
              v-if="row.type === 'post'"
              :post="row.item"
              @removed="removeItem"
              @repost-change="onRepostChange"
              @post-created="onPostCreated"
            />
            <div
              v-else
              :class="[ui.card, ui.cardPad, 'grid cursor-pointer gap-2 text-ink hover:border-soft-hover']"
              role="link"
              tabindex="0"
              aria-label="查看评论所属帖子"
              @click="openPost(row.item.post.id)"
              @keydown.enter="openPost(row.item.post.id)"
            >
              <div class="flex flex-wrap items-center gap-2 text-[14px]">
                <UserAvatar :user="row.item.author" size="sm" />
                <strong>{{ row.item.author.nickname || row.item.author.username }}</strong>
                <span class="text-[12.5px] text-ink-3">@{{ row.item.author.username }}</span>
                <span v-if="row.item.author.is_ai" :class="[ui.badge, ui.badgeAi]">AI</span>
                <span :title="timeTitle(row.item.created_at)" :class="[ui.muted]">
                  {{ relativeTime(row.item.created_at) }}
                </span>
              </div>
              <div
                class="text-[14.5px] leading-[1.7] [word-break:break-word] [&_a.tl-tag]:font-semibold [&_a.tl-tag]:text-primary [&_a.tl-mention]:font-semibold [&_a.tl-mention]:text-primary"
                v-html="row.item.content_html"
              ></div>
              <p class="m-0 flex items-center gap-1.5 rounded-lg bg-soft px-2.5 py-2 text-[12.5px] text-ink-3">
                <AppIcon name="message" :size="14" />
                <span>评论了「{{ plainSummary(row.item.post.content_html) }}」</span>
              </p>
            </div>
          </template>

          <div class="flex justify-center py-1">
            <button
              v-if="hasMore && !loadingMore"
              :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]"
              type="button"
              @click="loadMore"
            >
              加载更多
            </button>
            <p v-else-if="loadingMore" :class="[ui.loadingRow, 'p-0!']">
              <span :class="[ui.spinner]" aria-hidden="true"></span> 正在加载…
            </p>
          </div>

          <AlertNote v-if="listError" kind="error">{{ listError }}</AlertNote>
        </template>
      </template>
    </div>
  </div>
</template>
