<script setup lang="ts">
// 通知中心（docs/PAGES_AND_PERMISSIONS.md U-07）：
// 全部/未读筛选、单条已读、全部已读；preview 为空时显示不可见占位。
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { errorMessage } from '../api/client'
import { listNotifications, markAllNotificationsRead, markNotificationRead } from '../api/social'
import type { Notification, NotificationType } from '../api/types'
import AlertNote from '../components/AlertNote.vue'
import AppIcon from '../components/AppIcon.vue'
import EmptyState from '../components/EmptyState.vue'
import UserAvatar from '../components/UserAvatar.vue'
import { useCursorList } from '../composables/useCursorList'
import { refreshUnreadCount } from '../stores/notifications'
import { ui } from '../styles/ui'
import { relativeTime, timeTitle } from '../utils/format'

type NotificationFilter = 'all' | 'unread'

/** 通知类型 → 中文动作文案（docs/REQUIREMENTS.md 11） */
const ACTION_TEXT: Record<NotificationType, string> = {
  post_like: '赞了你的帖子',
  comment_like: '赞了你的评论',
  comment: '评论了你的帖子',
  reply: '回复了你的评论',
  repost: '转发了你的帖子',
  quote: '引用了你的帖子',
  follow: '关注了你',
  follow_request: '请求关注你',
  follow_approved: '批准了你的关注请求',
  mention: '在内容中提到了你',
}

/** 类型筛选下拉：全部 + 每种通知类型 */
const TYPE_OPTIONS: { value: NotificationType; label: string }[] = [
  { value: 'post_like', label: '帖子点赞' },
  { value: 'comment_like', label: '评论点赞' },
  { value: 'comment', label: '评论' },
  { value: 'reply', label: '回复' },
  { value: 'repost', label: '转发' },
  { value: 'quote', label: '引用' },
  { value: 'follow', label: '关注' },
  { value: 'follow_request', label: '关注申请' },
  { value: 'follow_approved', label: '关注通过' },
  { value: 'mention', label: '提及' },
]

const router = useRouter()
const filter = ref<NotificationFilter>('all')
const typeFilter = ref<NotificationType | ''>('')

const { items, loading, loadingMore, hasMore, error, loaded, reload, loadMore } = useCursorList<
  Notification,
  { filter: NotificationFilter; type: NotificationType | '' }
>({
  fetch: (params) =>
    listNotifications(params.filter, {
      cursor: params.cursor,
      type: params.type || undefined,
    }),
  params: () => ({ filter: filter.value, type: typeFilter.value }),
})

function setFilter(value: NotificationFilter): void {
  if (filter.value === value) return
  filter.value = value
  void reload()
}

function setType(value: NotificationType | ''): void {
  if (typeFilter.value === value) return
  typeFilter.value = value
  void reload()
}

function onTypeChange(event: Event): void {
  setType((event.target as HTMLSelectElement).value as NotificationType | '')
}

const busy = ref(false)
const notice = ref('')

function previewText(item: Notification): string {
  if (!item.preview) return '内容不可见或已删除'
  return item.preview.length > 60 ? `${item.preview.slice(0, 60)}…` : item.preview
}

function markReadLocally(id: string): void {
  if (filter.value === 'unread') {
    items.value = items.value.filter((item) => item.id !== id)
    return
  }
  items.value = items.value.map((item) => (item.id === id ? { ...item, is_read: true } : item))
}

/** 简化规则：post → 帖子详情；user → 作者主页；comment → 只标记已读不跳转 */
function openNotification(item: Notification): void {
  if (item.target_type === 'post') {
    void router.push(`/posts/${item.target_id}`)
  } else if (item.target_type === 'user') {
    void router.push(`/users/${item.actor.username}`)
  }
}

async function handleClick(item: Notification): Promise<void> {
  if (busy.value) return
  notice.value = ''
  if (item.is_read) {
    openNotification(item)
    return
  }
  busy.value = true
  try {
    await markNotificationRead(item.id)
    markReadLocally(item.id)
    void refreshUnreadCount()
  } catch (err) {
    notice.value = errorMessage(err)
    busy.value = false
    return
  } finally {
    busy.value = false
  }
  openNotification(item)
}

async function markAll(): Promise<void> {
  if (busy.value) return
  busy.value = true
  notice.value = ''
  try {
    await markAllNotificationsRead()
    await reload()
    void refreshUnreadCount()
  } catch (err) {
    notice.value = errorMessage(err)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div :class="[ui.constrain]">
    <div :class="[ui.stack]">
      <section :class="[ui.card, ui.cardPad]">
        <div class="flex items-start justify-between gap-3">
          <div>
            <h1 :class="[ui.pageHead, ui.pageTitle]">通知</h1>
            <p :class="[ui.pageDesc]">点赞、评论、转发、关注与提及都会显示在这里。</p>
          </div>
          <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]" type="button" :disabled="busy" @click="markAll">
            <AppIcon name="check" :size="16" /> 全部已读
          </button>
        </div>

        <div :class="[ui.segment, 'mt-[14px]']" role="group" aria-label="通知筛选">
          <button type="button" :class="[ui.segmentButton, filter === 'all' && ui.segmentButtonActive]" @click="setFilter('all')">
            全部
          </button>
          <button type="button" :class="[ui.segmentButton, filter === 'unread' && ui.segmentButtonActive]" @click="setFilter('unread')">
            未读
          </button>
        </div>

        <label class="mt-2.5 inline-flex items-center gap-2 text-[12.5px]">
          <span :class="[ui.muted]">通知类型</span>
          <select :class="[ui.input, 'w-auto! min-w-[150px]']" :value="typeFilter" aria-label="按通知类型筛选" @change="onTypeChange">
            <option value="">全部类型</option>
            <option v-for="option in TYPE_OPTIONS" :key="option.value" :value="option.value">
              {{ option.label }}
            </option>
          </select>
        </label>
      </section>

      <AlertNote v-if="notice" kind="error">{{ notice }}</AlertNote>

      <p v-if="loading" :class="[ui.card, ui.loadingRow]">
        <span :class="[ui.spinner]" aria-hidden="true"></span> 加载中…
      </p>

      <template v-else-if="error && items.length === 0">
        <AlertNote kind="error">{{ error }}</AlertNote>
        <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'self-start']" type="button" @click="reload">重试</button>
      </template>

      <EmptyState
        v-else-if="loaded && items.length === 0"
        icon="bell"
        :title="filter === 'unread' ? '没有未读通知' : '暂无通知'"
        description="有新的点赞、评论或关注时会出现在这里。"
      />

      <template v-else>
        <section :class="[ui.card, 'overflow-hidden']">
          <button
            v-for="item in items"
            :key="item.id"
            type="button"
            class="flex w-full items-start gap-3 border-b border-solid border-border px-4 py-3 text-left text-ink last:border-b-0 hover-not-disabled:bg-hover disabled:cursor-progress"
            :class="!item.is_read && 'bg-primary-soft'"
            :disabled="busy"
            @click="handleClick(item)"
          >
            <UserAvatar :user="item.actor" size="sm" />
            <span class="flex min-w-0 flex-1 flex-col gap-0.5">
              <span class="flex flex-wrap items-center gap-1.5 text-[14px]">
                <strong>{{ item.actor.nickname || item.actor.username }}</strong>
                <span class="text-ink-2">{{ ACTION_TEXT[item.type] }}</span>
                <span v-if="item.actor.is_ai" :class="[ui.badge, ui.badgeAi]">AI</span>
              </span>
              <span class="overflow-hidden text-ellipsis whitespace-nowrap text-[13px]" :class="item.preview ? 'text-ink-2' : 'text-ink-3'">
                {{ previewText(item) }}
              </span>
              <span class="text-[12px] text-ink-3" :title="timeTitle(item.created_at)">
                {{ relativeTime(item.created_at) }}
              </span>
            </span>
            <span v-if="!item.is_read" class="mt-3 size-2 flex-none rounded-full bg-primary" aria-label="未读"></span>
          </button>
        </section>

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
