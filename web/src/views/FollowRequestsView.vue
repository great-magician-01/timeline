<script setup lang="ts">
// 关注申请（docs/PAGES_AND_PERMISSIONS.md U-06）：
// 收到的申请可以批准或拒绝，发出的申请可以取消；批准后才形成有效关注关系。
import { ref } from 'vue'
import { RouterLink } from 'vue-router'
import { errorMessage } from '../api/client'
import {
  approveFollowRequest,
  cancelFollowRequest,
  listFollowRequests,
  rejectFollowRequest,
} from '../api/social'
import type { FollowRequest, UserBrief } from '../api/types'
import AlertNote from '../components/AlertNote.vue'
import AppIcon from '../components/AppIcon.vue'
import EmptyState from '../components/EmptyState.vue'
import UserAvatar from '../components/UserAvatar.vue'
import { useCursorList } from '../composables/useCursorList'
import { relativeTime, timeTitle } from '../utils/format'

type Direction = 'incoming' | 'outgoing'

const direction = ref<Direction>('incoming')

const { items, loading, loadingMore, hasMore, error, loaded, reload, loadMore } = useCursorList<
  FollowRequest,
  { direction: Direction }
>({
  fetch: (params) => listFollowRequests(params.direction, { cursor: params.cursor }),
  params: () => ({ direction: direction.value }),
})

function setDirection(value: Direction): void {
  if (direction.value === value) return
  direction.value = value
  void reload()
}

const busyId = ref('')
const actionError = ref('')

/** 当前方向下需要展示的对方用户：收到看申请者，发出看目标 */
function counterpart(item: FollowRequest): UserBrief {
  return direction.value === 'incoming' ? item.requester : item.target
}

async function resolve(item: FollowRequest, action: 'approve' | 'reject' | 'cancel'): Promise<void> {
  if (busyId.value) return
  busyId.value = item.id
  actionError.value = ''
  try {
    if (action === 'approve') await approveFollowRequest(item.id)
    else if (action === 'reject') await rejectFollowRequest(item.id)
    else await cancelFollowRequest(item.id)
    items.value = items.value.filter((entry) => entry.id !== item.id)
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
        <h1 class="page-head page-title">关注申请</h1>
        <p class="page-desc">开启关注审批后，新的关注需要你批准才会生效；待处理的申请不计入粉丝数。</p>

        <div class="seg filter" role="group" aria-label="关注申请方向">
          <button
            type="button"
            :class="{ 'is-active': direction === 'incoming' }"
            @click="setDirection('incoming')"
          >
            收到的
          </button>
          <button
            type="button"
            :class="{ 'is-active': direction === 'outgoing' }"
            @click="setDirection('outgoing')"
          >
            发出的
          </button>
        </div>
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
        icon="user-plus"
        :title="direction === 'incoming' ? '没有待处理的关注申请' : '没有已发出的关注申请'"
        :description="
          direction === 'incoming'
            ? '当有人申请关注你时，可以在这里批准或拒绝。'
            : '向开启审批的账号发起关注后，申请会显示在这里。'
        "
      />

      <template v-else>
        <section class="card request-list">
          <article v-for="item in items" :key="item.id" class="request">
            <RouterLink :to="`/users/${counterpart(item).username}`" class="who">
              <UserAvatar :user="counterpart(item)" size="md" />
              <span class="names">
                <span class="row">
                  <strong>{{ counterpart(item).nickname || counterpart(item).username }}</strong>
                  <span v-if="counterpart(item).is_ai" class="badge badge--ai">AI</span>
                  <span v-if="counterpart(item).is_suspended" class="badge badge--muted">已停用</span>
                </span>
                <span class="handle">@{{ counterpart(item).username }}</span>
              </span>
            </RouterLink>

            <span class="meta">
              <span :title="timeTitle(item.created_at)" class="time">{{ relativeTime(item.created_at) }}</span>
              <span class="badge badge--muted">{{ item.status === 'pending' ? '待处理' : item.status }}</span>
            </span>

            <div v-if="direction === 'incoming'" class="acts">
              <button
                class="btn btn--primary btn--sm"
                type="button"
                :disabled="Boolean(busyId)"
                @click="resolve(item, 'approve')"
              >
                <AppIcon name="check" :size="15" /> 批准
              </button>
              <button
                class="btn btn--danger btn--sm"
                type="button"
                :disabled="Boolean(busyId)"
                @click="resolve(item, 'reject')"
              >
                <AppIcon name="x" :size="15" /> 拒绝
              </button>
            </div>
            <div v-else class="acts">
              <button
                class="btn btn--secondary btn--sm"
                type="button"
                :disabled="Boolean(busyId)"
                @click="resolve(item, 'cancel')"
              >
                取消申请
              </button>
            </div>
          </article>

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
.filter {
  margin-top: 14px;
}
.request-list {
  overflow: hidden;
}
.request {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 16px;
  border-bottom: 1px solid var(--border);
}
.request:last-of-type {
  border-bottom: none;
}
.who {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  flex: 1;
  color: var(--ink);
}
.who:hover {
  text-decoration: none;
}
.names {
  display: flex;
  flex-direction: column;
  min-width: 0;
  line-height: 1.35;
}
.row {
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
.meta {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12.5px;
  color: var(--ink-3);
  white-space: nowrap;
}
.acts {
  display: flex;
  gap: 6px;
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
@media (max-width: 880px) {
  .request {
    flex-wrap: wrap;
  }
  .meta {
    order: 3;
  }
  .acts {
    order: 4;
    width: 100%;
    justify-content: flex-end;
  }
}
</style>
