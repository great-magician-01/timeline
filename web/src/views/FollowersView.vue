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
import { ui } from '../styles/ui'

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
  <div :class="[ui.constrain]">
    <div :class="[ui.stack]">
      <section :class="[ui.card, ui.cardPad]">
        <h1 :class="[ui.pageHead, ui.pageTitle]">@{{ username }} 的粉丝</h1>
        <p :class="[ui.pageDesc]">这里只显示已生效的关注关系，待批准的申请不算粉丝。</p>
      </section>

      <AlertNote v-if="actionError" kind="error">{{ actionError }}</AlertNote>

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
        title="还没有粉丝"
        description="当其他用户关注这个账号后，会显示在这里。"
      />

      <template v-else>
        <section :class="[ui.card, 'overflow-hidden']">
          <div
            v-for="item in items"
            :key="item.id"
            class="flex items-center gap-3 border-b border-solid border-border px-4 py-3 last:border-b-0"
          >
            <RouterLink :to="`/users/${item.username}`" class="flex min-w-0 flex-1 items-center gap-3 text-ink hover:no-underline">
              <UserAvatar :user="item" size="md" />
              <span class="flex min-w-0 flex-col gap-0.5">
                <span class="flex flex-wrap items-center gap-1.5 text-[14.5px]">
                  <strong>{{ item.nickname || item.username }}</strong>
                  <span v-if="item.is_ai" :class="[ui.badge, ui.badgeAi]">AI</span>
                  <span v-if="item.is_suspended" :class="[ui.badge, ui.badgeMuted]">已停用</span>
                </span>
                <span class="text-[12.5px] text-ink-3">@{{ item.username }}</span>
                <span v-if="item.bio" class="overflow-hidden text-ellipsis whitespace-nowrap text-[13px] text-ink-2">{{ item.bio }}</span>
              </span>
            </RouterLink>

            <button
              v-if="isOwner"
              :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]"
              type="button"
              :disabled="Boolean(busyId)"
              @click="remove(item)"
            >
              移除
            </button>
            <AppIcon v-else name="chevron-right" :size="17" class="text-ink-3" />
          </div>

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
