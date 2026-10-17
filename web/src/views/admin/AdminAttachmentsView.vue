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
  if (value === 'deleted' || value === 'failed') return 'badge--danger'
  if (value === 'processing') return 'badge--muted'
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
  <div class="stack">
    <section class="card card--pad">
      <h2 class="page-title">附件管理</h2>
      <p class="page-desc">按上传者、帖子、用途与状态筛查附件；删除为逻辑删除并写入审计日志。</p>

      <form class="filter-grid" @submit.prevent="onSearch">
        <label class="filter-field">
          <span class="muted">关键词</span>
          <input
            v-model="filters.q"
            class="input"
            type="search"
            name="q"
            placeholder="文件名或媒体类型"
            aria-label="搜索附件"
          />
        </label>

        <label class="filter-field">
          <span class="muted">上传者 ID</span>
          <input
            v-model="filters.uploader_id"
            class="input"
            type="search"
            name="uploader_id"
            placeholder="用户雪花 ID"
            aria-label="按上传者 ID 筛选"
          />
        </label>

        <label class="filter-field">
          <span class="muted">帖子 ID</span>
          <input
            v-model="filters.post_id"
            class="input"
            type="search"
            name="post_id"
            placeholder="帖子雪花 ID"
            aria-label="按帖子 ID 筛选"
          />
        </label>

        <label class="filter-field">
          <span class="muted">用途</span>
          <select v-model="filters.purpose" class="input" name="purpose" aria-label="按用途筛选">
            <option value="">全部用途</option>
            <option value="post">帖子附件</option>
            <option value="avatar">头像</option>
          </select>
        </label>

        <label class="filter-field">
          <span class="muted">状态</span>
          <select v-model="filters.status" class="input" name="status" aria-label="按状态筛选">
            <option value="">全部状态</option>
            <option value="available">可用</option>
            <option value="deleted">已删除</option>
          </select>
        </label>

        <div class="filter-actions">
          <button class="btn btn--primary btn--sm" type="submit" :disabled="loading">
            <span v-if="loading" class="spinner" aria-hidden="true"></span>
            {{ loading ? '查询中…' : '查询' }}
          </button>
          <button class="btn btn--secondary btn--sm" type="button" @click="resetFilters">重置</button>
        </div>
      </form>
    </section>

    <AlertNote v-if="actionNotice" kind="success">{{ actionNotice }}</AlertNote>
    <AlertNote v-if="actionError" kind="error">{{ actionError }}</AlertNote>

    <p v-if="initialLoading" class="card card--pad loading-row">
      <span class="spinner" aria-hidden="true"></span> 加载中…
    </p>

    <template v-else-if="listError && items.length === 0">
      <AlertNote kind="error">{{ listError }}</AlertNote>
      <button class="btn btn--secondary btn--sm self-start" type="button" @click="reload">重试</button>
    </template>

    <section v-else-if="items.length === 0" class="card card--pad">
      <div class="empty">
        <AppIcon name="image" :size="26" />
        <strong>没有匹配的附件</strong>
        <p>尝试调整关键词、上传者、帖子或筛选条件。</p>
      </div>
    </section>

    <template v-else>
      <section class="card table-card">
        <div class="table-scroll">
          <table class="table">
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
                <td class="col-id">
                  <span class="mono block-id" :title="item.id">{{ item.id }}</span>
                </td>
                <td>
                  <span v-if="item.uploader" class="uploader">
                    <UserAvatar :user="item.uploader" size="sm" />
                    <span class="uploader-text">
                      <span class="uploader-name">{{ item.uploader.nickname || item.uploader.username }}</span>
                      <span class="muted">@{{ item.uploader.username }}</span>
                    </span>
                  </span>
                  <span v-else class="muted">上传者不可见</span>
                </td>
                <td class="col-id">
                  <RouterLink v-if="item.post_id" class="mono id-link" :to="`/admin/content/posts/${item.post_id}`">
                    {{ item.post_id }}
                  </RouterLink>
                  <span v-else class="muted">—</span>
                </td>
                <td class="nowrap">{{ purposeLabel(item.purpose) }} / {{ kindLabel(item.kind) }}</td>
                <td class="col-name">{{ item.original_filename }}</td>
                <td class="mono nowrap">{{ formatBytes(item.size_bytes) }}</td>
                <td>
                  <span class="badge" :class="statusClass(item.status)">{{ statusLabel(item.status) }}</span>
                </td>
                <td class="col-time">{{ formatDateTime(item.created_at) }}</td>
                <td>
                  <button
                    v-if="item.status !== 'deleted'"
                    class="btn btn--danger btn--sm"
                    type="button"
                    :disabled="busyId === item.id"
                    @click="onDelete(item)"
                  >
                    {{ busyId === item.id ? '删除中…' : '删除' }}
                  </button>
                  <span v-else class="muted">—</span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <div class="list-foot">
          <button v-if="hasMore && !loadingMore" class="btn btn--secondary btn--sm" type="button" @click="loadMore">
            加载更多
          </button>
          <p v-else-if="loadingMore" class="loading-row list-foot-loading">
            <span class="spinner" aria-hidden="true"></span> 正在加载…
          </p>
          <p v-else class="muted">已加载全部 {{ items.length }} 个附件</p>
        </div>
      </section>

      <AlertNote v-if="listError" kind="error">{{ listError }}</AlertNote>
    </template>
  </div>
</template>

<style scoped>
.filter-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 10px 12px;
  margin-top: 14px;
}
.filter-field {
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: 12.5px;
}
.filter-actions {
  display: flex;
  align-items: flex-end;
  gap: 8px;
}
.self-start {
  align-self: flex-start;
}
.table-card {
  overflow: hidden;
}
.table-scroll {
  overflow-x: auto;
}
.table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}
.table th {
  padding: 10px 12px;
  border-bottom: 1px solid var(--border);
  color: var(--ink-3);
  font-size: 12.5px;
  font-weight: 600;
  text-align: left;
  white-space: nowrap;
}
.table td {
  padding: 10px 12px;
  border-bottom: 1px solid var(--border);
  vertical-align: middle;
}
.table tbody tr:last-child td {
  border-bottom: none;
}
.nowrap {
  white-space: nowrap;
}
.col-id {
  max-width: 140px;
}
.block-id {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  font-size: 11.5px;
}
.id-link {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
}
.uploader {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 130px;
}
.uploader-text {
  display: flex;
  flex-direction: column;
  min-width: 0;
  line-height: 1.4;
}
.uploader-name {
  font-weight: 700;
  white-space: nowrap;
}
.col-name {
  min-width: 160px;
  max-width: 260px;
  word-break: break-all;
}
.col-time {
  white-space: nowrap;
  font-size: 12.5px;
  color: var(--ink-2);
}
.list-foot {
  display: flex;
  justify-content: center;
  padding: 14px 16px;
}
.list-foot .muted {
  margin: 0;
}
.list-foot-loading {
  padding: 0;
}
</style>
