<script setup lang="ts">
// 群聊列表（docs/PAGES_AND_PERMISSIONS.md）：有效成员、未隐藏且有消息的群，
// 按最后消息倒序；未读高亮，可隐藏自己的会话视图；顶部提供建群表单。
import { ref } from 'vue'
import { RouterLink } from 'vue-router'
import { errorMessage } from '../api/client'
import { createGroup, hideGroup, listGroups } from '../api/groups'
import type { GroupConversation } from '../api/types'
import AlertNote from '../components/AlertNote.vue'
import AppIcon from '../components/AppIcon.vue'
import EmptyState from '../components/EmptyState.vue'
import { useCursorList } from '../composables/useCursorList'
import { refreshGroupUnreadCount } from '../stores/groups'
import { ui } from '../styles/ui'
import { relativeTime, timeTitle } from '../utils/format'

const { items, loading, loadingMore, hasMore, error, loaded, reload, loadMore, prepend } =
  useCursorList<GroupConversation, Record<string, never>>({
    fetch: (params) => listGroups({ cursor: params.cursor }),
    params: () => ({}),
  })

const creating = ref(false)
const createError = ref('')
const groupName = ref('')
const memberInput = ref('')
const actionError = ref('')
const busyId = ref('')

async function submitCreate(): Promise<void> {
  const name = groupName.value.trim()
  if (!name || creating.value) return
  const usernames = memberInput.value
    .split(/[\s,，]+/)
    .map((entry) => entry.trim())
    .filter(Boolean)
  creating.value = true
  createError.value = ''
  try {
    const result = await createGroup(name, usernames)
    groupName.value = ''
    memberInput.value = ''
    prepend(result.group)
  } catch (err) {
    createError.value = errorMessage(err)
  } finally {
    creating.value = false
  }
}

async function removeGroup(item: GroupConversation): Promise<void> {
  if (busyId.value) return
  const dissolved = item.status === 'dissolved'
  const message = dissolved
    ? '从列表隐藏这个已解散的群？群不会再有新消息，隐藏后不再出现在列表（历史仍可经链接访问）。'
    : '从列表隐藏这个群？有新消息时会自动重新出现。'
  if (!window.confirm(message)) return
  busyId.value = item.id
  actionError.value = ''
  try {
    await hideGroup(item.id)
    items.value = items.value.filter((entry) => entry.id !== item.id)
    await refreshGroupUnreadCount()
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
        <h1 :class="[ui.pageHead, ui.pageTitle]">群聊</h1>
        <p :class="[ui.pageDesc]">和多人一起聊天。你创建的群由你管理，成员可随时退出。</p>
      </section>

      <section :class="[ui.card, ui.cardPad]">
        <h2 class="m-0 mb-2 text-[14.5px] font-bold">发起新群</h2>
        <form class="grid gap-2" @submit.prevent="submitCreate">
          <input
            v-model="groupName"
            :class="[ui.input]"
            type="text"
            maxlength="30"
            placeholder="群名称（1–30 字）"
            aria-label="群名称"
            :disabled="creating"
          />
          <input
            v-model="memberInput"
            :class="[ui.input]"
            type="text"
            placeholder="初始成员用户名，用空格或逗号分隔（可留空）"
            aria-label="初始成员"
            :disabled="creating"
          />
          <AlertNote v-if="createError" kind="error">{{ createError }}</AlertNote>
          <div class="flex justify-end">
            <button
              :class="[ui.button, ui.buttonPrimary, ui.buttonSmall]"
              type="submit"
              :disabled="creating || groupName.trim().length === 0"
            >
              <span v-if="creating" :class="[ui.spinner, ui.buttonSpinner]" aria-hidden="true"></span>
              {{ creating ? '创建中…' : '创建群聊' }}
            </button>
          </div>
        </form>
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
        title="还没有群聊"
        description="用上方的表单创建一个群，或让朋友把你拉进群。"
      />

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
              :to="`/groups/${item.id}`"
              class="flex min-w-0 flex-1 items-center gap-2.5 text-ink hover:no-underline"
            >
              <span
                :class="[
                  'flex size-10 shrink-0 items-center justify-center rounded-[10px] text-[17px] font-bold',
                  item.status === 'dissolved' ? 'bg-soft text-ink-3' : 'bg-primary-soft text-primary',
                ]"
                aria-hidden="true"
              >
                {{ item.name.slice(0, 1) }}
              </span>
              <span class="flex min-w-0 flex-1 flex-col leading-[1.35]">
                <span class="flex flex-wrap items-center gap-1.5 text-[14.5px]">
                  <strong>{{ item.name }}</strong>
                  <span v-if="item.status === 'dissolved'" :class="[ui.badge, ui.badgeMuted]">已解散</span>
                  <span v-if="item.role === 'owner'" :class="[ui.badge, ui.badgeMuted]">我管理</span>
                  <span class="ml-auto whitespace-nowrap text-[12.5px] text-ink-3" :title="timeTitle(item.last_message?.created_at || item.updated_at)">
                    {{ relativeTime(item.last_message?.created_at || item.updated_at) }}
                  </span>
                </span>
                <span class="mt-0.5 flex min-w-0 items-center gap-2">
                  <span class="min-w-0 flex-1 overflow-hidden text-ellipsis whitespace-nowrap text-[13px] text-ink-3">
                    {{ item.last_message?.preview || '还没有消息' }}
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
              aria-label="隐藏群聊"
              :title="item.status === 'dissolved' ? '隐藏（解散群不再出现）' : '隐藏（有新消息会重新出现）'"
              @click="removeGroup(item)"
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
