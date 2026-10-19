<script setup lang="ts">
// 私信对话（docs/PAGES_AND_PERMISSIONS.md U-12）：
// 消息按时间升序展示，顶部加载更早消息，底部发送纯文本；
// 10 秒增量轮询新消息，进入与新消息到达时标记已读；发送者可撤回窗口内自己的消息。
import { computed, nextTick, onUnmounted, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { ApiError, errorMessage } from '../api/client'
import {
  getConversation,
  listMessages,
  listNewMessages,
  markConversationRead,
  recallMessage,
  sendMessage,
} from '../api/dm'
import type { DMConversation, DMMessage } from '../api/types'
import AlertNote from '../components/AlertNote.vue'
import AppIcon from '../components/AppIcon.vue'
import EmptyState from '../components/EmptyState.vue'
import UserAvatar from '../components/UserAvatar.vue'
import { useBackNavigation } from '../composables/useBackNavigation'
import { authState } from '../stores/auth'
import { refreshDMUnreadCount } from '../stores/messages'
import { ui } from '../styles/ui'
import { relativeTime, timeTitle } from '../utils/format'
import { textToParagraphHTML } from '../utils/text'

const POLL_INTERVAL = 10_000

const route = useRoute()
const conversationId = computed(() => String(route.params.conversationId ?? ''))
const my = computed(() => authState.user)
const { goBack } = useBackNavigation('/messages')

const conversation = ref<DMConversation | null>(null)
const messages = ref<DMMessage[]>([])
const loading = ref(true)
const loadError = ref('')
const notFound = ref(false)

const hasMoreOlder = ref(false)
const olderCursor = ref<string | null>(null)
const loadingOlder = ref(false)

const text = ref('')
const sending = ref(false)
const sendError = ref('')
const actionError = ref('')
const recallingId = ref('')

const scroller = ref<HTMLElement | null>(null)
const peerSuspended = computed(() => Boolean(conversation.value?.peer.is_suspended))

let timer: number | null = null

function isOwn(message: DMMessage): boolean {
  return message.sender.id === my.value?.id
}

function isNearBottom(el: HTMLElement): boolean {
  return el.scrollHeight - el.scrollTop - el.clientHeight < 120
}

async function scrollToBottom(): Promise<void> {
  await nextTick()
  const el = scroller.value
  if (el) el.scrollTop = el.scrollHeight
}

async function markRead(): Promise<void> {
  try {
    await markConversationRead(conversationId.value)
    await refreshDMUnreadCount()
  } catch {
    // 已读失败不阻塞阅读；下次进入或轮询会重试
  }
}

async function loadLatest(): Promise<void> {
  const page = await listMessages(conversationId.value)
  messages.value = [...page.items].reverse()
  olderCursor.value = page.next_cursor
  hasMoreOlder.value = page.has_more
}

async function loadConversation(): Promise<void> {
  loading.value = true
  loadError.value = ''
  notFound.value = false
  conversation.value = null
  messages.value = []
  try {
    const data = await getConversation(conversationId.value)
    conversation.value = data.conversation
    await loadLatest()
    await scrollToBottom()
    void markRead()
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) notFound.value = true
    else loadError.value = errorMessage(err)
  } finally {
    loading.value = false
  }
}

async function loadOlder(): Promise<void> {
  const el = scroller.value
  if (!el || !hasMoreOlder.value || loadingOlder.value || !olderCursor.value) return
  const previousHeight = el.scrollHeight
  const previousTop = el.scrollTop
  loadingOlder.value = true
  actionError.value = ''
  try {
    const page = await listMessages(conversationId.value, { cursor: olderCursor.value })
    messages.value = [...[...page.items].reverse(), ...messages.value]
    olderCursor.value = page.next_cursor
    hasMoreOlder.value = page.has_more
    await nextTick()
    el.scrollTop = previousTop + (el.scrollHeight - previousHeight)
  } catch (err) {
    actionError.value = errorMessage(err)
  } finally {
    loadingOlder.value = false
  }
}

async function pollNew(): Promise<void> {
  if (document.hidden || loading.value || !conversation.value) return
  const el = scroller.value
  const nearBottom = el ? isNearBottom(el) : true
  const last = messages.value[messages.value.length - 1]
  try {
    const page = last
      ? await listNewMessages(conversationId.value, last.id)
      : await listMessages(conversationId.value)
    const incoming = last ? page.items : [...page.items].reverse()
    const known = new Set(messages.value.map((message) => message.id))
    const fresh = incoming.filter((message) => !known.has(message.id))
    if (fresh.length === 0) return
    messages.value = [...messages.value, ...fresh]
    if (nearBottom) await scrollToBottom()
    void markRead()
  } catch {
    // 轮询失败静默，下一轮自动重试
  }
}

function startPolling(): void {
  if (timer !== null) return
  timer = window.setInterval(() => void pollNew(), POLL_INTERVAL)
}

function stopPolling(): void {
  if (timer === null) return
  window.clearInterval(timer)
  timer = null
}

async function submit(): Promise<void> {
  const value = text.value.trim()
  if (!value || sending.value || peerSuspended.value) return
  sending.value = true
  sendError.value = ''
  try {
    const result = await sendMessage(conversationId.value, {
      content_format: 'richtext',
      content_source: textToParagraphHTML(value),
    })
    if (!messages.value.some((message) => message.id === result.message.id)) {
      messages.value = [...messages.value, result.message]
    }
    text.value = ''
    await scrollToBottom()
    void refreshDMUnreadCount()
  } catch (err) {
    sendError.value = errorMessage(err)
  } finally {
    sending.value = false
  }
}

function onKeydown(event: KeyboardEvent): void {
  if (event.key === 'Enter' && !event.shiftKey && !event.isComposing) {
    event.preventDefault()
    void submit()
  }
}

async function recall(message: DMMessage): Promise<void> {
  if (recallingId.value) return
  if (!window.confirm('撤回这条消息？双方都会看到「已撤回」占位。')) return
  recallingId.value = message.id
  actionError.value = ''
  try {
    const result = await recallMessage(message.id)
    messages.value = messages.value.map((item) =>
      item.id === result.message.id ? result.message : item,
    )
  } catch (err) {
    actionError.value = errorMessage(err)
  } finally {
    recallingId.value = ''
  }
}

watch(
  conversationId,
  () => {
    stopPolling()
    void loadConversation().then(() => {
      if (conversation.value) startPolling()
    })
  },
  { immediate: true },
)
onUnmounted(stopPolling)
</script>

<template>
  <div :class="[ui.constrain]">
    <div :class="[ui.stack]">
      <p v-if="loading" :class="[ui.card, ui.loadingRow]">
        <span :class="[ui.spinner]" aria-hidden="true"></span> 加载中…
      </p>

      <section v-else-if="notFound" :class="[ui.card, ui.cardPad]">
        <EmptyState icon="message" title="会话不存在或不可见" description="该会话可能已被删除，或你不是会话成员。">
          <RouterLink :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'mt-3']" to="/messages">返回私信列表</RouterLink>
        </EmptyState>
      </section>

      <template v-else-if="loadError">
        <AlertNote kind="error">{{ loadError }}</AlertNote>
        <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'self-start']" type="button" @click="loadConversation">重试</button>
      </template>

      <template v-else-if="conversation">
        <section :class="[ui.card, ui.cardPad]">
          <div class="flex items-center gap-2.5">
            <button
              :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]"
              type="button"
              aria-label="返回私信列表"
              @click="goBack"
            >
              <AppIcon name="arrow-left" :size="16" />
            </button>
            <RouterLink :to="`/users/${conversation.peer.username}`" class="flex min-w-0 items-center gap-2.5 text-ink hover:no-underline">
              <UserAvatar :user="conversation.peer" size="sm" />
              <span class="flex min-w-0 flex-col leading-[1.35]">
                <span class="flex flex-wrap items-center gap-1.5 text-[15px] font-bold">
                  {{ conversation.peer.nickname || conversation.peer.username }}
                  <span v-if="conversation.peer.is_ai" :class="[ui.badge, ui.badgeAi]">AI</span>
                  <span v-if="conversation.peer.is_suspended" :class="[ui.badge, ui.badgeMuted]">已停用</span>
                </span>
                <span class="text-[12.5px] text-ink-3">@{{ conversation.peer.username }}</span>
              </span>
            </RouterLink>
          </div>
        </section>

        <AlertNote v-if="peerSuspended" kind="warning">
          对方账号已停用，当前会话只读，不能再发送新消息。
        </AlertNote>
        <AlertNote v-if="actionError" kind="error">{{ actionError }}</AlertNote>

        <section :class="[ui.card, 'flex flex-col overflow-hidden']">
          <div ref="scroller" class="max-h-[min(60vh,640px)] min-h-[240px] overflow-y-auto px-3 py-3 shell-down:min-h-[48vh]">
            <div v-if="hasMoreOlder || loadingOlder" class="flex justify-center pb-2">
              <button
                v-if="hasMoreOlder && !loadingOlder"
                :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]"
                type="button"
                @click="loadOlder"
              >
                加载更早消息
              </button>
              <p v-else :class="[ui.loadingRow, 'p-0!']">
                <span :class="[ui.spinner]" aria-hidden="true"></span> 加载中…
              </p>
            </div>

            <p v-if="messages.length === 0" :class="[ui.muted, 'm-0 px-2 py-8 text-center']">
              还没有消息，打个招呼吧。
            </p>

            <div class="flex flex-col gap-2.5">
              <div
                v-for="message in messages"
                :key="message.id"
                :class="['flex items-end gap-2', isOwn(message) && 'flex-row-reverse']"
              >
                <RouterLink
                  v-if="!isOwn(message)"
                  :to="`/users/${message.sender.username}`"
                  class="shrink-0 text-ink hover:no-underline"
                >
                  <UserAvatar :user="message.sender" size="sm" />
                </RouterLink>

                <div :class="['flex max-w-[78%] min-w-0 flex-col gap-1', isOwn(message) && 'items-end']">
                  <div
                    :class="[
                      'rounded-[12px] px-3 py-2 text-[14.5px] leading-[1.7]',
                      isOwn(message) ? 'bg-primary-soft' : 'bg-soft',
                    ]"
                  >
                    <p v-if="message.status === 'recalled'" :class="[ui.muted, 'm-0 italic']">消息已撤回</p>
                    <div
                      v-else
                      class="[word-break:break-word]"
                      v-html="message.content_html"
                    ></div>
                  </div>
                  <span class="flex items-center gap-2 text-[11.5px] text-ink-3">
                    <span :title="timeTitle(message.created_at)">{{ relativeTime(message.created_at) }}</span>
                    <button
                      v-if="message.can_recall && message.status === 'published'"
                      type="button"
                      class="inline-flex items-center gap-[2px] text-[11.5px] text-ink-3 hover:text-ink"
                      :disabled="Boolean(recallingId)"
                      @click="recall(message)"
                    >
                      <AppIcon name="refresh" :size="12" /> 撤回
                    </button>
                  </span>
                </div>

                <UserAvatar v-if="isOwn(message)" :user="message.sender" size="sm" class="shrink-0" />
              </div>
            </div>
          </div>

          <div class="border-t border-solid border-border px-3 py-3">
            <AlertNote v-if="sendError" kind="error" class="mb-2!">{{ sendError }}</AlertNote>
            <div class="grid gap-2">
              <textarea
                v-model="text"
                :class="[ui.input, ui.textarea, 'max-h-[180px]']"
                rows="2"
                placeholder="输入消息，Enter 发送，Shift+Enter 换行"
                aria-label="私信内容"
                :disabled="sending || peerSuspended"
                @keydown="onKeydown"
              ></textarea>
              <div class="flex items-center justify-between gap-2">
                <span class="text-[12px] text-ink-3">仅支持纯文本，不支持附件。</span>
                <button
                  :class="[ui.button, ui.buttonPrimary, ui.buttonSmall]"
                  type="button"
                  :disabled="sending || peerSuspended || text.trim().length === 0"
                  @click="submit"
                >
                  <span v-if="sending" :class="[ui.spinner, ui.buttonSpinner]" aria-hidden="true"></span>
                  <AppIcon v-else name="send" :size="15" />
                  {{ sending ? '发送中…' : '发送' }}
                </button>
              </div>
            </div>
          </div>
        </section>
      </template>
    </div>
  </div>
</template>
