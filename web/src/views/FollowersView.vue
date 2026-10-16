<script setup lang="ts">
// 粉丝列表（docs/PAGES_AND_PERMISSIONS.md P-10）：
// 只展示已生效的关注关系；主页所有者可以移除自己的粉丝。
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { errorMessage } from '../api/client'
import { listFollowers, removeFollower } from '../api/social'
import type { PublicUser } from '../api/types'
import AlertNote from '../components/AlertNote.vue'
import AppIcon from '../components/AppIcon.vue'
import EmptyState from '../components/EmptyState.vue'
import UserAvatar from '../components/UserAvatar.vue'
import { useCursorList } from '../composables/useCursorList'
import { authState } from '../stores/auth'

const route = useRoute()
const username = computed(() => String(route.params.username ?? ''))
const isOwner = computed(() => Boolean(authState.user && authState.user.username === username.value))

const { items, loading, loadingMore, hasMore, error, loaded, reload, loadMore } = useCursorList<
  PublicUser,
  { username: string }
>({
  fetch: (params) => listFollowers(params.username, { cursor: params.cursor }),
  params: () => ({ username: username.value }),
})

watch(username, () => {
  if (username.value) void reload()
})

const busyId = ref('')
const actionError = ref('')

async function remove(user: PublicUser): Promise<void> {
  if (busyId.value) return
  if (!window.confirm(`确认移除粉丝 @${user.username}？移除后对方会立即失去仅关注者内容的访问权。`)) return
  busyId.value = user.id
  actionError.value = ''
  try {
    await removeFollower(user.username)
    items.value = items.value.filter((entry) => entry.id !== user.id)
  } catch (err) {
    actionError.value = errorMessage(err)
  } finally {
    busyId.value = ''
  }
}
</script>

<template>
  <div class="constrain">
    <div class="stack">
      <section class="card card--pad">
        <h1 class="page-head page-title">@{{ username }} 的粉丝</h1>
        <p class="page-desc">这里只显示已生效的关注关系，待批准的申请不算粉丝。</p>
      </section>

      <AlertNote v-if="actionError" kind="error">{{ actionError }}</AlertNote>

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
        title="还没有粉丝"
        description="当其他用户关注这个账号后，会显示在这里。"
      />

      <template v-else>
        <section class="card user-list">
          <div v-for="item in items" :key="item.id" class="user-row">
            <RouterLink :to="`/users/${item.username}`" class="user-link">
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
            </RouterLink>

            <button
              v-if="isOwner"
              class="btn btn--secondary btn--sm"
              type="button"
              :disabled="Boolean(busyId)"
              @click="remove(item)"
            >
              移除
            </button>
            <AppIcon v-else name="chevron-right" :size="17" class="arrow" />
          </div>

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
}
.user-row:last-of-type {
  border-bottom: none;
}
.user-link {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
  flex: 1;
  color: var(--ink);
}
.user-link:hover {
  text-decoration: none;
}
.user-main {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
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
.list-foot {
  display: flex;
  justify-content: center;
  padding: 12px 0;
}
.foot-loading {
  padding: 0;
}
</style>
