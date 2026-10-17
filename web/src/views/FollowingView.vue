<script setup lang="ts">
// 关注时间线（docs/PAGES_AND_PERMISSIONS.md P-04）：只展示已生效关注作者的可见内容
import { RouterLink } from 'vue-router'
import { fetchFollowingTimeline } from '../api/posts'
import type { PostBrief } from '../api/types'
import AlertNote from '../components/AlertNote.vue'
import EmptyState from '../components/EmptyState.vue'
import PostCard from '../components/PostCard.vue'
import { useCursorList } from '../composables/useCursorList'
import { ui } from '../styles/ui'

const { items, loading, loadingMore, hasMore, error, loaded, reload, loadMore } = useCursorList<
  PostBrief,
  Record<string, never>
>({
  fetch: (params) => fetchFollowingTimeline({ cursor: params.cursor }),
  params: () => ({}),
})

function removeItem(id: string): void {
  items.value = items.value.filter((item) => item.id !== id)
}
</script>

<template>
  <div :class="[ui.constrain]">
    <div :class="[ui.stack]">
      <section :class="[ui.card, ui.cardPad]">
        <h1 :class="[ui.pageHead, ui.pageTitle]">关注时间线</h1>
        <p :class="[ui.pageDesc]">这里只显示你已经关注的作者发布的内容；待批准的关注申请不会生效。</p>
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
        description="关注感兴趣的作者后，这里会显示他们发布的内容。"
      >
        <RouterLink :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'mt-3']" to="/search">去搜索用户</RouterLink>
      </EmptyState>

      <template v-else>
        <PostCard v-for="post in items" :key="post.id" :post="post" @removed="removeItem" />

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

        <AlertNote v-if="error" kind="error">{{ error }}</AlertNote>
      </template>
    </div>
  </div>
</template>
