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
import { ui } from '../styles/ui'
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
  <div :class="[ui.constrain]">
    <div :class="[ui.stack]">
      <section :class="[ui.card, ui.cardPad]">
        <h1 :class="[ui.pageHead, ui.pageTitle]">关注申请</h1>
        <p :class="[ui.pageDesc]">开启关注审批后，新的关注需要你批准才会生效；待处理的申请不计入粉丝数。</p>

        <div :class="[ui.segment, 'mt-[14px]']" role="group" aria-label="关注申请方向">
          <button
            type="button"
            :class="[ui.segmentButton, direction === 'incoming' && ui.segmentButtonActive]"
            @click="setDirection('incoming')"
          >
            收到的
          </button>
          <button
            type="button"
            :class="[ui.segmentButton, direction === 'outgoing' && ui.segmentButtonActive]"
            @click="setDirection('outgoing')"
          >
            发出的
          </button>
        </div>
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
        icon="user-plus"
        :title="direction === 'incoming' ? '没有待处理的关注申请' : '没有已发出的关注申请'"
        :description="
          direction === 'incoming'
            ? '当有人申请关注你时，可以在这里批准或拒绝。'
            : '向开启审批的账号发起关注后，申请会显示在这里。'
        "
      />

      <template v-else>
        <section :class="[ui.card, 'overflow-hidden']">
          <article
            v-for="item in items"
            :key="item.id"
            class="flex items-center gap-3 border-b border-solid border-border px-4 py-3 [&:last-of-type]:border-b-0 shell-down:flex-wrap"
          >
            <RouterLink :to="`/users/${counterpart(item).username}`" class="flex min-w-0 flex-1 items-center gap-2.5 text-ink hover:no-underline">
              <UserAvatar :user="counterpart(item)" size="md" />
              <span class="flex min-w-0 flex-col leading-[1.35]">
                <span class="flex flex-wrap items-center gap-1.5 text-[14.5px]">
                  <strong>{{ counterpart(item).nickname || counterpart(item).username }}</strong>
                  <span v-if="counterpart(item).is_ai" :class="[ui.badge, ui.badgeAi]">AI</span>
                  <span v-if="counterpart(item).is_suspended" :class="[ui.badge, ui.badgeMuted]">已停用</span>
                </span>
                <span class="text-[12.5px] text-ink-3">@{{ counterpart(item).username }}</span>
              </span>
            </RouterLink>

            <span class="flex items-center gap-1.5 whitespace-nowrap text-[12.5px] text-ink-3 shell-down:order-3">
              <span :title="timeTitle(item.created_at)">{{ relativeTime(item.created_at) }}</span>
              <span :class="[ui.badge, ui.badgeMuted]">{{ item.status === 'pending' ? '待处理' : item.status }}</span>
            </span>

            <div v-if="direction === 'incoming'" class="flex gap-1.5 shell-down:order-4 shell-down:w-full shell-down:justify-end">
              <button
                :class="[ui.button, ui.buttonPrimary, ui.buttonSmall]"
                type="button"
                :disabled="Boolean(busyId)"
                @click="resolve(item, 'approve')"
              >
                <AppIcon name="check" :size="15" /> 批准
              </button>
              <button
                :class="[ui.button, ui.buttonDanger, ui.buttonSmall]"
                type="button"
                :disabled="Boolean(busyId)"
                @click="resolve(item, 'reject')"
              >
                <AppIcon name="x" :size="15" /> 拒绝
              </button>
            </div>
            <div v-else class="flex gap-1.5 shell-down:order-4 shell-down:w-full shell-down:justify-end">
              <button
                :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]"
                type="button"
                :disabled="Boolean(busyId)"
                @click="resolve(item, 'cancel')"
              >
                取消申请
              </button>
            </div>
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
