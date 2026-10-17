<script setup lang="ts">
// A-04 帖子审核：全可见性帖子查询与逻辑删除
import { computed, reactive, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { deleteAdminPost, listAdminPosts } from '../../api/adminContent'
import { errorMessage } from '../../api/client'
import type { AdminPost } from '../../api/types'
import AlertNote from '../../components/AlertNote.vue'
import AppIcon from '../../components/AppIcon.vue'
import UserAvatar from '../../components/UserAvatar.vue'
import { useCursorList } from '../../composables/useCursorList'
import { formatDateTime } from '../../utils/datetime'
import { plainSummary } from '../../utils/format'
import { ui } from '../../styles/ui'

type VisibilityFilter = '' | 'public' | 'followers' | 'private'
type StatusFilter = '' | 'published' | 'draft' | 'scheduled' | 'deleted'

type PostFilters = {
  visibility?: string
  status?: string
  author?: string
  q?: string
}

const VISIBILITY_LABELS: Record<string, string> = {
  public: '公开',
  followers: '仅关注者',
  private: '仅自己',
}

const STATUS_LABELS: Record<string, string> = {
  published: '已发布',
  draft: '草稿',
  scheduled: '定时',
  deleted: '已删除',
}

const filters = reactive({
  q: '',
  author: '',
  visibility: '' as VisibilityFilter,
  status: '' as StatusFilter,
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
} = useCursorList<AdminPost, PostFilters>({
  fetch: (params) => listAdminPosts(params),
  params: () => ({
    visibility: filters.visibility || undefined,
    status: filters.status || undefined,
    author: filters.author.trim() || undefined,
    q: filters.q.trim() || undefined,
  }),
})

// 首屏（含筛选切换后）尚无任何结果时的加载态
const initialLoading = computed(() => !loaded.value && !listError.value)

const busyId = ref<string | null>(null)
const actionError = ref('')
const actionNotice = ref('')

function visibilityLabel(value: string): string {
  return VISIBILITY_LABELS[value] ?? value
}

function statusLabel(value: string): string {
  return STATUS_LABELS[value] ?? value
}

function statusClass(value: string): string {
  if (value === 'deleted') return ui.badgeDanger
  if (value === 'published') return ''
  return ui.badgeMuted
}

function setVisibility(value: VisibilityFilter): void {
  if (filters.visibility === value) return
  filters.visibility = value
  void reload()
}

function setStatus(value: StatusFilter): void {
  if (filters.status === value) return
  filters.status = value
  void reload()
}

function onSearch(): void {
  void reload()
}

async function onDelete(post: AdminPost): Promise<void> {
  const handle = post.author.nickname || post.author.username
  if (
    !window.confirm(
      `确认删除「${handle}」的这条帖子？删除为逻辑删除：内容不再对外展示，记录与附件保留，操作会记入审计日志。`,
    )
  ) {
    return
  }
  busyId.value = post.id
  actionError.value = ''
  actionNotice.value = ''
  try {
    await deleteAdminPost(post.id)
    actionNotice.value = `已逻辑删除帖子 ${post.id}。`
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
            placeholder="搜索正文关键词"
            aria-label="搜索帖子关键词"
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

      <div class="mt-3 flex flex-wrap gap-2">
        <div :class="ui.segment" role="group" aria-label="可见性筛选">
          <button type="button" :class="[ui.segmentButton, filters.visibility === '' ? ui.segmentButtonActive : '']" @click="setVisibility('')">
            全部可见性
          </button>
          <button
            type="button"
            :class="[ui.segmentButton, filters.visibility === 'public' ? ui.segmentButtonActive : '']"
            @click="setVisibility('public')"
          >
            公开
          </button>
          <button
            type="button"
            :class="[ui.segmentButton, filters.visibility === 'followers' ? ui.segmentButtonActive : '']"
            @click="setVisibility('followers')"
          >
            仅关注者
          </button>
          <button
            type="button"
            :class="[ui.segmentButton, filters.visibility === 'private' ? ui.segmentButtonActive : '']"
            @click="setVisibility('private')"
          >
            仅自己
          </button>
        </div>

        <div :class="ui.segment" role="group" aria-label="状态筛选">
          <button type="button" :class="[ui.segmentButton, filters.status === '' ? ui.segmentButtonActive : '']" @click="setStatus('')">
            全部状态
          </button>
          <button
            type="button"
            :class="[ui.segmentButton, filters.status === 'published' ? ui.segmentButtonActive : '']"
            @click="setStatus('published')"
          >
            已发布
          </button>
          <button type="button" :class="[ui.segmentButton, filters.status === 'draft' ? ui.segmentButtonActive : '']" @click="setStatus('draft')">
            草稿
          </button>
          <button
            type="button"
            :class="[ui.segmentButton, filters.status === 'scheduled' ? ui.segmentButtonActive : '']"
            @click="setStatus('scheduled')"
          >
            定时
          </button>
          <button
            type="button"
            :class="[ui.segmentButton, filters.status === 'deleted' ? ui.segmentButtonActive : '']"
            @click="setStatus('deleted')"
          >
            已删除
          </button>
        </div>
      </div>
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
        <AppIcon name="file-text" :size="26" :class="ui.emptyIcon" />
        <strong :class="ui.emptyStrong">没有找到匹配的帖子</strong>
        <p :class="ui.emptyText">尝试调整关键词、作者或筛选条件。</p>
      </div>
    </section>

    <template v-else>
      <section :class="[ui.card, 'overflow-hidden']">
        <div class="overflow-x-auto">
          <table class="w-full border-collapse text-[13.5px] [&_th]:whitespace-nowrap [&_th]:border-b [&_th]:border-solid [&_th]:border-border [&_th]:px-3 [&_th]:py-[10px] [&_th]:text-left [&_th]:text-[12.5px] [&_th]:font-semibold [&_th]:text-ink-3 [&_td]:border-b [&_td]:border-solid [&_td]:border-border [&_td]:px-3 [&_td]:py-[10px] [&_td]:align-middle [&_tbody_tr:last-child_td]:border-b-0 [&_tbody_tr:hover]:bg-hover">
            <thead>
              <tr>
                <th>ID</th>
                <th>作者</th>
                <th>可见性</th>
                <th>状态</th>
                <th>正文摘要</th>
                <th>赞 / 评 / 转</th>
                <th>发布时间</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="post in items" :key="post.id">
                <td class="max-w-[150px]">
                  <RouterLink :to="`/admin/content/posts/${post.id}`" :class="[ui.mono, 'block overflow-hidden text-ellipsis whitespace-nowrap text-[12.5px]']" :title="post.id">
                    {{ post.id }}
                  </RouterLink>
                </td>
                <td>
                  <span class="flex min-w-[140px] items-center gap-2">
                    <UserAvatar :user="post.author" size="sm" />
                    <span class="flex min-w-0 flex-col leading-[1.4]">
                      <span class="whitespace-nowrap font-bold">{{ post.author.nickname || post.author.username }}</span>
                      <span :class="ui.muted">@{{ post.author.username }}</span>
                    </span>
                  </span>
                </td>
                <td>{{ visibilityLabel(post.visibility) }}</td>
                <td>
                  <span :class="[ui.badge, statusClass(post.status)]">{{ statusLabel(post.status) }}</span>
                </td>
                <td class="min-w-[200px] max-w-[320px]">{{ plainSummary(post.content_html, 60) || '（无正文）' }}</td>
                <td :class="ui.mono">{{ post.like_count }} / {{ post.comment_count }} / {{ post.repost_count }}</td>
                <td class="whitespace-nowrap text-[12.5px] text-ink-2">{{ post.published_at ? formatDateTime(post.published_at) : '—' }}</td>
                <td>
                  <button
                    :class="[ui.button, ui.buttonDanger, ui.buttonSmall]"
                    type="button"
                    :disabled="busyId === post.id"
                    @click="onDelete(post)"
                  >
                    {{ busyId === post.id ? '删除中…' : '删除' }}
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>

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
