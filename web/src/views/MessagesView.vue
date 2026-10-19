<script setup lang="ts">
// 私信会话列表（docs/PAGES_AND_PERMISSIONS.md U-11）：
// 未隐藏且有消息的会话，按最后消息倒序；未读高亮，可删除自己的会话视图。
import { ref } from 'vue'
import { RouterLink } from 'vue-router'
import { errorMessage } from '../api/client'
import { hideConversation, listConversations } from '../api/dm'
import type { DMConversation } from '../api/types'
import AlertNote from '../components/AlertNote.vue'
import AppIcon from '../components/AppIcon.vue'
import EmptyState from '../components/EmptyState.vue'
import UserAvatar from '../components/UserAvatar.vue'
import { useCursorList } from '../composables/useCursorList'
import { refreshDMUnreadCount } from '../stores/messages'
import { ui } from '../styles/ui'
import { relativeTime, timeTitle } from '../utils/format'

const { items, loading, loadingMore, hasMore, error, loaded, reload, loadMore } = useCursorList<
  DMConversation,
  Record<string, never>
>({
  fetch: (params) => listConversations({ cursor: params.cursor }),
  params: () => ({}),
})

const busyId = ref('')
const actionError = ref('')

function previewText(item: DMConversation): string {
  return item.last_message?.preview || '还没有消息'
}

function itemTime(item: DMConversation): string {
  return item.last_message?.created_at || item.updated_at
}

async function removeConversation(item: DMConversation): Promise<void> {
  if (busyId.value) return
  if (!window.confirm('删除这个会话？仅从你的列表中移除，对方不受影响。')) return
  busyId.value = item.id
  actionError.value = ''
  try {
    await hideConversation(item.id)
    items.value = items.value.filter((entry) => entry.id !== item.id)
    await refreshDMUnreadCount()
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
        <h1 :class="[ui.pageHead, ui.pageTitle]">私信</h1>
        <p :class="[ui.pageDesc]">与其他用户一对一对话。你可以在账号设置中调整谁能给你发私信。</p>
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
        icon="message"
        title="还没有私信会话"
        description="在别人的个人主页点击「发私信」，就能开始对话。"
      >
        <RouterLink :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'mt-3']" to="/search">去找人聊聊</RouterLink>
      </EmptyState>

      <template v-else>
        <section :class="[ui.card, 'overflow-hidden']">
          <article
            v-for="item in items"
            :key="item.id"
            :class="[
              'flex items-center gap-3 border-b border-solid border-border px-4 py-3 [&:last-of-type]:border-b-0',
              item.unread_count > 0 && 'bg-primary-soft',
            ]"
          >
            <RouterLink
              :to="`/messages/${item.id}`"
              class="flex min-w-0 flex-1 items-center gap-2.5 text-ink hover:no-underline"
            >
              <UserAvatar :user="item.peer" size="md" />
              <span class="flex min-w-0 flex-1 flex-col leading-[1.35]">
                <span class="flex flex-wrap items-center gap-1.5 text-[14.5px]">
                  <strong>{{ item.peer.nickname || item.peer.username }}</strong>
                  <span v-if="item.peer.is_ai" :class="[ui.badge, ui.badgeAi]">AI</span>
                  <span v-if="item.peer.is_suspended" :class="[ui.badge, ui.badgeMuted]">已停用</span>
                  <span class="ml-auto whitespace-nowrap text-[12.5px] text-ink-3" :title="timeTitle(itemTime(item))">
                    {{ relativeTime(itemTime(item)) }}
                  </span>
                </span>
                <span class="mt-0.5 flex min-w-0 items-center gap-2">
                  <span class="min-w-0 flex-1 overflow-hidden text-ellipsis whitespace-nowrap text-[13px] text-ink-3">
                    {{ previewText(item) }}
                  </span>
                  <span
                    v-if="item.unread_count > 0"
                    class="min-w-[20px] rounded-[999px] bg-primary px-[6px] text-center text-[11.5px] font-bold leading-[20px] text-on-media"
                  >
                    {{ item.unread_count > 99 ? '99+' : item.unread_count }}
                  </span>
                </span>
              </span>
            </RouterLink>

            <button
              :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'shrink-0']"
              type="button"
              :disabled="Boolean(busyId)"
              aria-label="删除会话"
              title="删除会话（仅对自己隐藏）"
              @click="removeConversation(item)"
            >
              <AppIcon name="trash" :size="16" />
            </button>
          </article>

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
