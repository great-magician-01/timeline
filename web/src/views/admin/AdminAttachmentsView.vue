<script setup lang="ts">
// 管理后台附件管理：按上传者、帖子、用途、状态与关键词筛选，支持逻辑删除（写审计）
import { computed, reactive, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { deleteAdminAttachment, listAdminAttachments } from '../../api/adminContent'
import { errorMessage } from '../../api/client'
import type { AdminAttachment } from '../../api/types'
import AlertNote from '../../components/AlertNote.vue'
import AppIcon from '../../components/AppIcon.vue'
import UserAvatar from '../../components/UserAvatar.vue'
import { useCursorList } from '../../composables/useCursorList'
import { formatDateTime } from '../../utils/datetime'
import { formatBytes } from '../../utils/format'
import { ui } from '../../styles/ui'

const STATUS_LABELS: Record<string, string> = {
  available: '可用',
  processing: '处理中',
  failed: '处理失败',
  deleted: '已删除',
}

const PURPOSE_LABELS: Record<string, string> = {
  post: '帖子附件',
  avatar: '头像',
}

const KIND_LABELS: Record<string, string> = {
  image: '图片',
  video: '视频',
  file: '文件',
}

const filters = reactive({
  q: '',
  uploader_id: '',
  post_id: '',
  purpose: '',
  status: '',
})

type AttachmentFilters = {
  q?: string
  uploader_id?: string
  post_id?: string
  purpose?: string
  status?: string
} & Record<string, unknown>

const { items, loading, loadingMore, hasMore, error: listError, loaded, reload, loadMore } =
  useCursorList<AdminAttachment, AttachmentFilters>({
    fetch: (params) => listAdminAttachments(params),
    params: () => ({
      q: filters.q.trim() || undefined,
      uploader_id: filters.uploader_id.trim() || undefined,
      post_id: filters.post_id.trim() || undefined,
      purpose: filters.purpose || undefined,
      status: filters.status || undefined,
    }),
  })

const initialLoading = computed(() => !loaded.value && !listError.value)

const busyId = ref('')
const actionError = ref('')
const actionNotice = ref('')

function statusLabel(value: string): string {
  return STATUS_LABELS[value] ?? value
}

function statusClass(value: string): string {
  if (value === 'deleted' || value === 'failed') return ui.badgeDanger
  if (value === 'processing') return ui.badgeMuted
  return ''
}

function purposeLabel(value: string): string {
  return PURPOSE_LABELS[value] ?? value
}

function kindLabel(value: string): string {
  return KIND_LABELS[value] ?? value
}

function onSearch(): void {
  void reload()
}

function resetFilters(): void {
  filters.q = ''
  filters.uploader_id = ''
  filters.post_id = ''
  filters.purpose = ''
  filters.status = ''
  void reload()
}

async function onDelete(attachment: AdminAttachment): Promise<void> {
  if (
    !window.confirm(
      `确认逻辑删除附件「${attachment.original_filename}」？文件与记录保留，只标记失效，操作会写入审计日志。`,
    )
  ) {
    return
  }
  busyId.value = attachment.id
  actionError.value = ''
  actionNotice.value = ''
  try {
    await deleteAdminAttachment(attachment.id)
    actionNotice.value = '附件已逻辑删除。'
    await reload()
  } catch (error) {
    actionError.value = errorMessage(error)
  } finally {
    busyId.value = ''
  }
}
</script>

<template>
  <div :class="ui.stack">
    <section :class="[ui.card, ui.cardPad]">
      <h2 :class="ui.pageTitle">附件管理</h2>
      <p :class="ui.pageDesc">按上传者、帖子、用途与状态筛查附件；删除为逻辑删除并写入审计日志。</p>

      <form class="mt-[14px] grid grid-cols-[repeat(auto-fit,minmax(180px,1fr))] gap-x-3 gap-y-[10px]" @submit.prevent="onSearch">
        <label class="flex flex-col gap-1 text-[12.5px]">
          <span :class="ui.muted">关键词</span>
          <input
            v-model="filters.q"
            :class="ui.input"
            type="search"
            name="q"
            placeholder="文件名或媒体类型"
            aria-label="搜索附件"
          />
        </label>

        <label class="flex flex-col gap-1 text-[12.5px]">
          <span :class="ui.muted">上传者 ID</span>
          <input
            v-model="filters.uploader_id"
            :class="ui.input"
            type="search"
            name="uploader_id"
            placeholder="用户雪花 ID"
            aria-label="按上传者 ID 筛选"
          />
        </label>

        <label class="flex flex-col gap-1 text-[12.5px]">
          <span :class="ui.muted">帖子 ID</span>
          <input
            v-model="filters.post_id"
            :class="ui.input"
            type="search"
            name="post_id"
            placeholder="帖子雪花 ID"
            aria-label="按帖子 ID 筛选"
          />
        </label>

        <label class="flex flex-col gap-1 text-[12.5px]">
          <span :class="ui.muted">用途</span>
          <select v-model="filters.purpose" :class="ui.input" name="purpose" aria-label="按用途筛选">
            <option value="">全部用途</option>
            <option value="post">帖子附件</option>
            <option value="avatar">头像</option>
          </select>
        </label>

        <label class="flex flex-col gap-1 text-[12.5px]">
          <span :class="ui.muted">状态</span>
          <select v-model="filters.status" :class="ui.input" name="status" aria-label="按状态筛选">
            <option value="">全部状态</option>
            <option value="available">可用</option>
            <option value="deleted">已删除</option>
          </select>
        </label>

        <div class="flex items-end gap-2">
          <button :class="[ui.button, ui.buttonPrimary, ui.buttonSmall]" type="submit" :disabled="loading">
            <span v-if="loading" :class="[ui.spinner, ui.buttonSpinner]" aria-hidden="true"></span>
            {{ loading ? '查询中…' : '查询' }}
          </button>
          <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]" type="button" @click="resetFilters">重置</button>
        </div>
      </form>
    </section>

    <AlertNote v-if="actionNotice" kind="success">{{ actionNotice }}</AlertNote>
    <AlertNote v-if="actionError" kind="error">{{ actionError }}</AlertNote>

    <p v-if="initialLoading" :class="[ui.card, ui.cardPad, ui.loadingRow]">
      <span :class="ui.spinner" aria-hidden="true"></span> 加载中…
    </p>

    <template v-else-if="listError && items.length === 0">
      <AlertNote kind="error">{{ listError }}</AlertNote>
      <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'self-start']" type="button" @click="reload">重试</button>
    </template>

    <section v-else-if="items.length === 0" :class="[ui.card, ui.cardPad]">
      <div :class="ui.empty">
        <AppIcon name="image" :size="26" :class="ui.emptyIcon" />
        <strong :class="ui.emptyStrong">没有匹配的附件</strong>
        <p :class="ui.emptyText">尝试调整关键词、上传者、帖子或筛选条件。</p>
      </div>
    </section>

    <template v-else>
      <section :class="[ui.card, 'overflow-hidden']">
        <div class="overflow-x-auto">
          <table class="w-full border-collapse text-[13px] [&_th]:whitespace-nowrap [&_th]:border-b [&_th]:border-solid [&_th]:border-border [&_th]:px-3 [&_th]:py-[10px] [&_th]:text-left [&_th]:text-[12.5px] [&_th]:font-semibold [&_th]:text-ink-3 [&_td]:border-b [&_td]:border-solid [&_td]:border-border [&_td]:px-3 [&_td]:py-[10px] [&_td]:align-middle [&_tbody_tr:last-child_td]:border-b-0">
            <thead>
              <tr>
                <th>ID</th>
                <th>上传者</th>
                <th>帖子</th>
                <th>用途 / 类型</th>
                <th>文件名</th>
                <th>大小</th>
                <th>状态</th>
                <th>上传时间</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="item in items" :key="item.id">
                <td class="max-w-[140px]">
                  <span :class="[ui.mono, 'block overflow-hidden text-ellipsis text-[11.5px]']" :title="item.id">{{ item.id }}</span>
                </td>
                <td>
                  <span v-if="item.uploader" class="flex min-w-[130px] items-center gap-2">
                    <UserAvatar :user="item.uploader" size="sm" />
                    <span class="flex min-w-0 flex-col leading-[1.4]">
                      <span class="whitespace-nowrap font-bold">{{ item.uploader.nickname || item.uploader.username }}</span>
                      <span :class="ui.muted">@{{ item.uploader.username }}</span>
                    </span>
                  </span>
                  <span v-else :class="ui.muted">上传者不可见</span>
                </td>
                <td class="max-w-[140px]">
                  <RouterLink v-if="item.post_id" :class="[ui.mono, 'block overflow-hidden text-ellipsis whitespace-nowrap text-[12px]']" :to="`/admin/content/posts/${item.post_id}`">
                    {{ item.post_id }}
                  </RouterLink>
                  <span v-else :class="ui.muted">—</span>
                </td>
                <td class="whitespace-nowrap">{{ purposeLabel(item.purpose) }} / {{ kindLabel(item.kind) }}</td>
                <td class="min-w-[160px] max-w-[260px] [word-break:break-all]">{{ item.original_filename }}</td>
                <td :class="[ui.mono, 'whitespace-nowrap']">{{ formatBytes(item.size_bytes) }}</td>
                <td>
                  <span :class="[ui.badge, statusClass(item.status)]">{{ statusLabel(item.status) }}</span>
                </td>
                <td class="whitespace-nowrap text-[12.5px] text-ink-2">{{ formatDateTime(item.created_at) }}</td>
                <td>
                  <button
                    v-if="item.status !== 'deleted'"
                    :class="[ui.button, ui.buttonDanger, ui.buttonSmall]"
                    type="button"
                    :disabled="busyId === item.id"
                    @click="onDelete(item)"
                  >
                    {{ busyId === item.id ? '删除中…' : '删除' }}
                  </button>
                  <span v-else :class="ui.muted">—</span>
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
          <p v-else :class="[ui.muted, 'm-0']">已加载全部 {{ items.length }} 个附件</p>
        </div>
      </section>

      <AlertNote v-if="listError" kind="error">{{ listError }}</AlertNote>
    </template>
  </div>
</template>
