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
  <div class="constrain">
    <div class="stack">
      <section class="card card--pad">
        <h1 class="page-head page-title">@{{ username }} 关注的人</h1>
        <p class="page-desc">这里只显示已经生效的关注关系。</p>
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
        description="关注感兴趣的作者后，会显示在这里。"
      >
        <RouterLink class="btn btn--secondary btn--sm empty-action" to="/search">去搜索用户</RouterLink>
      </EmptyState>

      <template v-else>
        <section class="card user-list">
          <RouterLink
            v-for="item in items"
            :key="item.id"
            :to="`/users/${item.username}`"
            class="user-row"
          >
            <UserAvatar :user="item" size="md" />
            <span class="user-main">
              <span class="user-name">
                <strong>{{ item.nickname || item.username }}</strong>
                <span v-if="item.is_ai" class="badge badge--ai">AI</span>
                <span v-if="item.is_suspended" class="badge badge--muted">已停用</span>
              </span>
              <span class="handle">@{{ item.username }}</span>
              <span v-if="item.bio" class="user-bio">{{ item.bio }}</span>
            </span>
            <AppIcon name="chevron-right" :size="17" class="arrow" />
          </RouterLink>

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
        </section>

        <AlertNote v-if="error" kind="error">{{ error }}</AlertNote>
      </template>
    </div>
  </div>
</template>

<style scoped>
.user-list {
  overflow: hidden;
}
.user-row {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 16px;
  border-bottom: 1px solid var(--border);
  color: var(--ink);
}
.user-row:last-of-type {
  border-bottom: none;
}
.user-row:hover {
  background: var(--hover);
  text-decoration: none;
}
.user-main {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  flex: 1;
}
.user-name {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  font-size: 14.5px;
}
.handle {
  font-size: 12.5px;
  color: var(--ink-3);
}
.user-bio {
  overflow: hidden;
  font-size: 13px;
  color: var(--ink-2);
  white-space: nowrap;
  text-overflow: ellipsis;
}
.arrow {
  color: var(--ink-3);
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
  padding: 12px 0;
}
.foot-loading {
  padding: 0;
}
</style>
