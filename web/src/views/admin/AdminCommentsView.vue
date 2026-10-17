<script setup lang="ts">
// A-05 评论审核：评论查询与逻辑删除
import { computed, reactive, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { deleteAdminComment, listAdminComments } from '../../api/adminContent'
import { errorMessage } from '../../api/client'
import type { AdminComment } from '../../api/types'
import AlertNote from '../../components/AlertNote.vue'
import AppIcon from '../../components/AppIcon.vue'
import UserAvatar from '../../components/UserAvatar.vue'
import { useCursorList } from '../../composables/useCursorList'
import { formatDateTime } from '../../utils/datetime'
import { ui } from '../../styles/ui'

type CommentFilters = {
  q?: string
  author?: string
}

const filters = reactive({
  q: '',
  author: '',
})

const {
  items,
  loading,
  loadingMore,
  hasMore,
  error: listError,
  loaded,
  reload,
  loadMore,
} = useCursorList<AdminComment, CommentFilters>({
  fetch: (params) => listAdminComments(params),
  params: () => ({
    q: filters.q.trim() || undefined,
    author: filters.author.trim() || undefined,
  }),
})

const initialLoading = computed(() => !loaded.value && !listError.value)

const busyId = ref<string | null>(null)
const actionError = ref('')
const actionNotice = ref('')

function onSearch(): void {
  void reload()
}

async function onDelete(comment: AdminComment): Promise<void> {
  const handle = comment.author.nickname || comment.author.username
  if (
    !window.confirm(
      `确认删除「${handle}」的这条评论？删除为逻辑删除：评论不再展示，记录保留，操作会记入审计日志。`,
    )
  ) {
    return
  }
  busyId.value = comment.id
  actionError.value = ''
  actionNotice.value = ''
  try {
    await deleteAdminComment(comment.id)
    actionNotice.value = `已逻辑删除评论 ${comment.id}。`
    await reload()
  } catch (error) {
    actionError.value = errorMessage(error)
  } finally {
    busyId.value = null
  }
}
</script>

<template>
  <div :class="ui.stack">
    <section :class="[ui.card, ui.cardPad]">
      <form class="flex flex-wrap gap-2" @submit.prevent="onSearch">
        <div class="relative min-w-0 flex-[1_1_220px]">
          <AppIcon name="search" :size="17" class="pointer-events-none absolute top-1/2 left-[11px] -translate-y-1/2 text-ink-3" />
          <input
            v-model="filters.q"
            :class="[ui.input, 'pl-9!']"
            type="search"
            name="q"
            placeholder="搜索评论关键词"
            aria-label="搜索评论关键词"
          />
        </div>
        <input
          v-model="filters.author"
          :class="[ui.input, 'flex-[0_1_180px]']"
          type="search"
          name="author"
          placeholder="作者用户名"
          aria-label="按作者用户名筛选"
        />
        <button :class="[ui.button, ui.buttonPrimary]" type="submit" :disabled="loading">
          <span v-if="loading" :class="[ui.spinner, ui.buttonSpinner]" aria-hidden="true"></span>
          {{ loading ? '查询中…' : '查询' }}
        </button>
      </form>
    </section>

    <AlertNote v-if="actionNotice" kind="success">{{ actionNotice }}</AlertNote>
    <AlertNote v-if="actionError" kind="error">{{ actionError }}</AlertNote>

    <p v-if="initialLoading" :class="[ui.card, ui.cardPad, ui.loadingRow]"><span :class="ui.spinner" aria-hidden="true"></span> 加载中…</p>

    <template v-else-if="listError && items.length === 0">
      <AlertNote kind="error">{{ listError }}</AlertNote>
      <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'self-start']" type="button" @click="reload">重试</button>
    </template>

    <section v-else-if="items.length === 0" :class="[ui.card, ui.cardPad]">
      <div :class="ui.empty">
        <AppIcon name="message" :size="26" :class="ui.emptyIcon" />
        <strong :class="ui.emptyStrong">没有找到匹配的评论</strong>
        <p :class="ui.emptyText">尝试调整关键词或作者筛选条件。</p>
      </div>
    </section>

    <template v-else>
      <section :class="[ui.card, 'overflow-hidden']">
        <article v-for="comment in items" :key="comment.id" class="flex gap-[10px] border-b border-solid border-border px-4 py-[14px]">
          <UserAvatar :user="comment.author" size="sm" />
          <div class="min-w-0 flex-1">
            <div class="flex flex-wrap items-center gap-[6px] text-[13.5px]">
              <span class="font-bold">{{ comment.author.nickname || comment.author.username }}</span>
              <span v-if="comment.author.is_ai" :class="[ui.badge, ui.badgeAi]">AI</span>
              <span v-if="comment.status === 'deleted'" :class="[ui.badge, ui.badgeDanger]">已删除</span>
              <span :class="[ui.muted, ui.mono]">@{{ comment.author.username }}</span>
              <span :class="[ui.mono, 'text-[12.5px] text-ink-3']">{{ formatDateTime(comment.created_at) }}</span>
            </div>

            <!-- content_html 由后端清洗 -->
            <div
              v-if="comment.content_html"
              class="mt-[6px] max-h-[200px] overflow-y-auto text-[14px] leading-[1.75] [word-break:break-word] [&_img]:h-auto [&_img]:max-w-full"
              v-html="comment.content_html"
            ></div>
            <p v-else :class="ui.muted">（无正文）</p>

            <div class="mt-2 flex flex-wrap items-center gap-[10px]">
              <RouterLink :to="`/admin/content/posts/${comment.post_id}`" :class="[ui.mono, 'max-w-[320px] overflow-hidden text-ellipsis whitespace-nowrap text-[12.5px] text-ink-3 hover:text-ink']">
                原帖 {{ comment.post_id }}
              </RouterLink>
              <button
                :class="[ui.button, ui.buttonDanger, ui.buttonSmall, 'ml-auto']"
                type="button"
                :disabled="busyId === comment.id || comment.status === 'deleted'"
                @click="onDelete(comment)"
              >
                {{ busyId === comment.id ? '删除中…' : '删除' }}
              </button>
            </div>
          </div>
        </article>

        <div class="flex justify-center px-4 py-[14px]">
          <button v-if="hasMore && !loadingMore" :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]" type="button" @click="loadMore">
            加载更多
          </button>
          <p v-else-if="loadingMore" :class="[ui.loadingRow, 'p-0!']">
            <span :class="ui.spinner" aria-hidden="true"></span> 正在加载…
          </p>
          <p v-else :class="[ui.muted, 'm-0']">已加载全部 {{ items.length }} 条记录</p>
        </div>
      </section>

      <AlertNote v-if="listError" kind="error">{{ listError }}</AlertNote>
    </template>
  </div>
</template>
