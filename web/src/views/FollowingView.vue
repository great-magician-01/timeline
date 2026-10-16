<script setup lang="ts">
// 关注时间线（docs/PAGES_AND_PERMISSIONS.md P-04）：只展示已生效关注作者的可见内容
import { RouterLink } from 'vue-router'
import { fetchFollowingTimeline } from '../api/posts'
import type { PostBrief } from '../api/types'
import AlertNote from '../components/AlertNote.vue'
import EmptyState from '../components/EmptyState.vue'
import PostCard from '../components/PostCard.vue'
import { useCursorList } from '../composables/useCursorList'

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
  <div class="constrain">
    <div class="stack">
      <section class="card card--pad">
        <h1 class="page-head page-title">关注时间线</h1>
        <p class="page-desc">这里只显示你已经关注的作者发布的内容；待批准的关注申请不会生效。</p>
      </section>

      <p v-if="loading" class="card card--pad loading-row">
        <span class="spinner" aria-hidden="true"></span> 加载中…
      </p>

      <template v-else-if="error && items.length === 0">
        <AlertNote kind="error">{{ error }}</AlertNote>
        <button class="btn btn--secondary btn--sm self-start" type="button" @click="reload">重试</button>
      </template>

      <EmptyState
        v-else-if="loaded && items.length === 0"
        icon="users"
        title="还没有关注任何人"
        description="关注感兴趣的作者后，这里会显示他们发布的内容。"
      >
        <RouterLink class="btn btn--secondary btn--sm empty-action" to="/search">去搜索用户</RouterLink>
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
</style>
