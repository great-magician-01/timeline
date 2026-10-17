<script setup lang="ts">
// 标签聚合（docs/PAGES_AND_PERMISSIONS.md P-06）：只包含当前访问者有权查看的已发布帖子
import { computed, watch } from 'vue'
import { useRoute } from 'vue-router'
import { fetchTagPosts } from '../api/posts'
import type { PostBrief } from '../api/types'
import AlertNote from '../components/AlertNote.vue'
import AppIcon from '../components/AppIcon.vue'
import EmptyState from '../components/EmptyState.vue'
import PostCard from '../components/PostCard.vue'
import { useCursorList } from '../composables/useCursorList'
import { ui } from '../styles/ui'

const route = useRoute()
const tag = computed(() => String(route.params.tag ?? ''))

const { items, loading, loadingMore, hasMore, error, loaded, reload, loadMore } = useCursorList<
  PostBrief,
  { tag: string }
>({
  fetch: (params) => fetchTagPosts(params.tag, { cursor: params.cursor }),
  params: () => ({ tag: tag.value }),
})

// 同一路由内切换标签时重新加载
watch(tag, () => {
  if (tag.value) void reload()
})

function removeItem(id: string): void {
  items.value = items.value.filter((item) => item.id !== id)
}
</script>

<template>
  <div :class="[ui.constrain]">
    <div :class="[ui.stack]">
      <section :class="[ui.card, ui.cardPad]">
        <h1 :class="[ui.pageHead, ui.pageTitle, 'flex items-center gap-1.5']">
          <AppIcon name="hash" :size="19" class="text-ink-3" />
          <span>#{{ tag }}#</span>
        </h1>
        <p :class="[ui.pageDesc]">该话题下的公开内容；非公开帖子不会出现在这里。</p>
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
        icon="hash"
        title="该话题下还没有内容"
        description="尝试搜索其他关键词，或自己发布一条带该话题的内容。"
      />

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
