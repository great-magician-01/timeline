<script setup lang="ts">
// 关注列表（docs/PAGES_AND_PERMISSIONS.md P-11）：
// 只展示已生效的关注关系，不包含待审批申请。
import { computed, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { listFollowing } from '../api/social'
import type { PublicUser } from '../api/types'
import AlertNote from '../components/AlertNote.vue'
import AppIcon from '../components/AppIcon.vue'
import EmptyState from '../components/EmptyState.vue'
import UserAvatar from '../components/UserAvatar.vue'
import { useCursorList } from '../composables/useCursorList'
import { ui } from '../styles/ui'

const route = useRoute()
const username = computed(() => String(route.params.username ?? ''))

const { items, loading, loadingMore, hasMore, error, loaded, reload, loadMore } = useCursorList<
  PublicUser,
  { username: string }
>({
  fetch: (params) => listFollowing(params.username, { cursor: params.cursor }),
  params: () => ({ username: username.value }),
})

watch(username, () => {
  if (username.value) void reload()
})
</script>

<template>
  <div :class="[ui.constrain]">
    <div :class="[ui.stack]">
      <section :class="[ui.card, ui.cardPad]">
        <h1 :class="[ui.pageHead, ui.pageTitle]">@{{ username }} 关注的人</h1>
        <p :class="[ui.pageDesc]">这里只显示已经生效的关注关系。</p>
      </section>

      <p v-if="loading" :class="[ui.card, ui.loadingRow]">
        <span :class="[ui.spinner]" aria-hidden="true"></span> 加载中…
      </p>

      <template v-else-if="error && items.length === 0">
        <AlertNote kind="error">{{ error }}</AlertNote>
        <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'self-start']" type="button" @click="reload">重试</button>
      </template>

      <EmptyState
        v-else-if="loaded && items.length === 0"
        icon="users"
        title="还没有关注任何人"
        description="关注感兴趣的作者后，会显示在这里。"
      >
        <RouterLink :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'mt-3']" to="/search">去搜索用户</RouterLink>
      </EmptyState>

      <template v-else>
        <section :class="[ui.card, 'overflow-hidden']">
          <RouterLink
            v-for="item in items"
            :key="item.id"
            :to="`/users/${item.username}`"
            class="flex items-center gap-3 border-b border-solid border-border px-4 py-3 text-ink [&:last-of-type]:border-b-0 hover:bg-hover hover:no-underline"
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

          <div class="flex justify-center py-3">
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
        </section>

        <AlertNote v-if="error" kind="error">{{ error }}</AlertNote>
      </template>
    </div>
  </div>
</template>
