<script setup lang="ts">
// 搜索（docs/PAGES_AND_PERMISSIONS.md P-05）：帖子与用户两个标签页
// 关键词同步到 route.query.q（使用 replace，不新增历史记录）；空关键词不发请求。
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { searchPosts, searchUsers } from '../api/posts'
import type { PostBrief, PublicUser } from '../api/types'
import AlertNote from '../components/AlertNote.vue'
import AppIcon from '../components/AppIcon.vue'
import EmptyState from '../components/EmptyState.vue'
import PostCard from '../components/PostCard.vue'
import UserAvatar from '../components/UserAvatar.vue'
import { useCursorList } from '../composables/useCursorList'
import { ui } from '../styles/ui'

type SearchTab = 'posts' | 'users'

const route = useRoute()
const router = useRouter()

const TABS: { value: SearchTab; label: string }[] = [
  { value: 'posts', label: '帖子' },
  { value: 'users', label: '用户' },
]

const keyword = ref(typeof route.query.q === 'string' ? route.query.q : '')
/** 已提交的关键词；两个列表都以它作为查询条件 */
const searched = ref(keyword.value.trim())
const tab = ref<SearchTab>('posts')

const posts = useCursorList<PostBrief, { q: string }>({
  fetch: (params) => searchPosts(params.q, { cursor: params.cursor }),
  params: () => ({ q: searched.value }),
  immediate: false,
})

const users = useCursorList<PublicUser, { q: string }>({
  fetch: (params) => searchUsers(params.q, { cursor: params.cursor }),
  params: () => ({ q: searched.value }),
  immediate: false,
})

/** 记录各标签页数据对应的关键词，切换标签时按需重新加载 */
const loadedFor = ref<Record<SearchTab, string>>({ posts: '', users: '' })

async function reloadTab(target: SearchTab): Promise<void> {
  if (target === 'posts') {
    await posts.reload()
    loadedFor.value = { ...loadedFor.value, posts: searched.value }
  } else {
    await users.reload()
    loadedFor.value = { ...loadedFor.value, users: searched.value }
  }
}

function runSearch(value: string): void {
  const next = value.trim()
  searched.value = next
  if (!next) {
    // 空关键词：清空结果，只显示提示，不发请求
    posts.items.value = []
    users.items.value = []
    posts.loaded.value = false
    users.loaded.value = false
    loadedFor.value = { posts: '', users: '' }
    return
  }
  void reloadTab(tab.value)
}

function submit(): void {
  const next = keyword.value.trim()
  const current = typeof route.query.q === 'string' ? route.query.q : ''
  if (next !== current) {
    void router.replace({ name: 'search', query: next ? { q: next } : {} })
  }
  runSearch(next)
}

function removePost(id: string): void {
  if (tab.value !== 'posts') return
  posts.items.value = posts.items.value.filter((item) => item.id !== id)
}

function setTab(value: SearchTab): void {
  if (tab.value === value) return
  tab.value = value
  if (!searched.value) return
  if (loadedFor.value[value] !== searched.value) void reloadTab(value)
}

// 地址栏关键词变化（外部链接或前进后退）时同步结果
watch(
  () => route.query.q,
  (value) => {
    const next = typeof value === 'string' ? value : ''
    if (next.trim() === searched.value) return
    keyword.value = next
    runSearch(next)
  },
)

const activeList = computed(() => (tab.value === 'posts' ? posts : users))

// 首次进入带关键词的地址时立即搜索（useCursorList 关闭了自动加载）
if (searched.value) void reloadTab('posts')
</script>

<template>
  <div :class="[ui.constrain]">
    <div :class="[ui.stack]">
      <section :class="[ui.card, ui.cardPad]">
        <h1 :class="[ui.pageHead, ui.pageTitle]">搜索</h1>
        <p :class="[ui.pageDesc]">按关键词查找帖子正文、话题标签，或按用户名、昵称查找用户。</p>

        <form class="mt-[14px] flex gap-2" @submit.prevent="submit">
          <div class="relative min-w-0 flex-1">
            <AppIcon name="search" :size="17" class="pointer-events-none absolute top-1/2 left-[11px] -translate-y-1/2 text-ink-3" />
            <input
              v-model="keyword"
              :class="[ui.input, 'pl-9']"
              type="search"
              name="q"
              placeholder="输入关键词"
              aria-label="搜索关键词"
            />
          </div>
          <button :class="[ui.button, ui.buttonPrimary]" type="submit">搜索</button>
        </form>

        <div :class="[ui.tabs, 'mt-4']">
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

      <EmptyState
        v-if="!searched"
        icon="search"
        title="输入关键词开始搜索"
        description="支持帖子正文与话题标签，用户结果按昵称或用户名匹配。"
      />

      <template v-else>
        <p v-if="activeList.loading.value" :class="[ui.card, ui.loadingRow]">
          <span :class="[ui.spinner]" aria-hidden="true"></span> 搜索中…
        </p>

        <template v-else-if="activeList.error.value && activeList.items.value.length === 0">
          <AlertNote kind="error">{{ activeList.error.value }}</AlertNote>
          <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'self-start']" type="button" @click="reloadTab(tab)">重试</button>
        </template>

        <EmptyState
          v-else-if="activeList.loaded.value && activeList.items.value.length === 0"
          icon="search"
          :title="tab === 'posts' ? '没有找到相关帖子' : '没有找到相关用户'"
          description="换一个关键词试试，或检查筛选条件。"
        />

        <template v-else>
          <template v-if="tab === 'posts'">
            <PostCard
              v-for="post in posts.items.value"
              :key="post.id"
              :post="post"
              @removed="removePost"
            />
          </template>

          <section v-else :class="[ui.card, 'overflow-hidden']">
            <RouterLink
              v-for="item in users.items.value"
              :key="item.id"
              :to="`/users/${item.username}`"
              class="flex items-center gap-3 border-b border-solid border-border px-4 py-3 text-ink last:border-b-0 hover:bg-hover hover:no-underline"
            >
              <UserAvatar :user="item" size="md" />
              <span class="flex min-w-0 flex-1 flex-col gap-0.5">
                <span class="flex flex-wrap items-center gap-1.5 text-[14.5px]">
                  <strong>{{ item.nickname || item.username }}</strong>
                  <span v-if="item.is_ai" :class="[ui.badge, ui.badgeAi]">AI</span>
                  <span v-if="item.is_suspended" :class="[ui.badge, ui.badgeMuted]">已停用</span>
                </span>
                <span class="text-[12.5px] text-ink-3">@{{ item.username }}</span>
                <span v-if="item.bio" class="overflow-hidden text-ellipsis whitespace-nowrap text-[13px] text-ink-2">{{ item.bio }}</span>
              </span>
              <AppIcon name="chevron-right" :size="17" class="text-ink-3" />
            </RouterLink>
          </section>

          <div class="flex justify-center py-1">
            <button
              v-if="activeList.hasMore.value && !activeList.loadingMore.value"
              :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]"
              type="button"
              @click="activeList.loadMore()"
            >
              加载更多
            </button>
            <p v-else-if="activeList.loadingMore.value" :class="[ui.loadingRow, 'p-0!']">
              <span :class="[ui.spinner]" aria-hidden="true"></span> 正在加载…
            </p>
          </div>

          <AlertNote v-if="activeList.error.value" kind="error">{{ activeList.error.value }}</AlertNote>
        </template>
      </template>
    </div>
  </div>
</template>
