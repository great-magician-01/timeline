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

const router = useRouter()
const filter = ref<NotificationFilter>('all')

const { items, loading, loadingMore, hasMore, error, loaded, reload, loadMore } = useCursorList<
  Notification,
  { filter: NotificationFilter }
>({
  fetch: (params) => listNotifications(params.filter, { cursor: params.cursor }),
  params: () => ({ filter: filter.value }),
})

function setFilter(value: NotificationFilter): void {
  if (filter.value === value) return
  filter.value = value
  void reload()
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
  <div class="constrain">
    <div class="stack">
      <section class="card card--pad">
        <div class="head-row">
          <div>
            <h1 class="page-head page-title">通知</h1>
            <p class="page-desc">点赞、评论、转发、关注与提及都会显示在这里。</p>
          </div>
          <button class="btn btn--secondary btn--sm" type="button" :disabled="busy" @click="markAll">
            <AppIcon name="check" :size="16" /> 全部已读
          </button>
        </div>

        <div class="seg filter" role="group" aria-label="通知筛选">
          <button type="button" :class="{ 'is-active': filter === 'all' }" @click="setFilter('all')">
            全部
          </button>
          <button type="button" :class="{ 'is-active': filter === 'unread' }" @click="setFilter('unread')">
            未读
          </button>
        </div>
      </section>

      <AlertNote v-if="notice" kind="error">{{ notice }}</AlertNote>

      <p v-if="loading" class="card card--pad loading-row">
        <span class="spinner" aria-hidden="true"></span> 加载中…
      </p>

      <template v-else-if="error && items.length === 0">
        <AlertNote kind="error">{{ error }}</AlertNote>
        <button class="btn btn--secondary btn--sm self-start" type="button" @click="reload">重试</button>
      </template>

      <EmptyState
        v-else-if="loaded && items.length === 0"
        icon="bell"
        :title="filter === 'unread' ? '没有未读通知' : '暂无通知'"
        description="有新的点赞、评论或关注时会出现在这里。"
      />

      <template v-else>
        <section class="card notif-list">
          <button
            v-for="item in items"
            :key="item.id"
            type="button"
            class="notif"
            :class="{ 'is-unread': !item.is_read }"
            :disabled="busy"
            @click="handleClick(item)"
          >
            <UserAvatar :user="item.actor" size="sm" />
            <span class="notif-main">
              <span class="notif-text">
                <strong>{{ item.actor.nickname || item.actor.username }}</strong>
                <span class="action">{{ ACTION_TEXT[item.type] }}</span>
                <span v-if="item.actor.is_ai" class="badge badge--ai">AI</span>
              </span>
              <span class="notif-preview" :class="{ 'is-muted': !item.preview }">
                {{ previewText(item) }}
              </span>
              <span class="notif-time" :title="timeTitle(item.created_at)">
                {{ relativeTime(item.created_at) }}
              </span>
            </span>
            <span v-if="!item.is_read" class="unread-dot" aria-label="未读"></span>
          </button>
        </section>

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
.head-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}
.filter {
  margin-top: 14px;
}
.notif-list {
  overflow: hidden;
}
.notif {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  width: 100%;
  padding: 12px 16px;
  border-bottom: 1px solid var(--border);
  text-align: left;
  color: var(--ink);
}
.notif:last-child {
  border-bottom: none;
}
.notif:hover:not(:disabled) {
  background: var(--hover);
}
.notif:disabled {
  cursor: progress;
}
.notif.is-unread {
  background: var(--primary-soft);
}
.notif-main {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  flex: 1;
}
.notif-text {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  font-size: 14px;
}
.notif-text .action {
  color: var(--ink-2);
}
.notif-preview {
  overflow: hidden;
  font-size: 13px;
  color: var(--ink-2);
  white-space: nowrap;
  text-overflow: ellipsis;
}
.notif-preview.is-muted {
  color: var(--ink-3);
}
.notif-time {
  font-size: 12px;
  color: var(--ink-3);
}
.unread-dot {
  flex: none;
  width: 8px;
  height: 8px;
  margin-top: 12px;
  border-radius: 50%;
  background: var(--primary);
}
.self-start {
  align-self: flex-start;
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
