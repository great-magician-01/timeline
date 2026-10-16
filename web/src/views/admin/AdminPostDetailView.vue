<script setup lang="ts">
// A-06 后台帖子详情：正文、历史版本、附件与逻辑删除
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { deleteAdminPost, getAdminPost } from '../../api/adminContent'
import type { AdminPostDetail } from '../../api/adminContent'
import { ApiError, errorMessage } from '../../api/client'
import AlertNote from '../../components/AlertNote.vue'
import AppIcon from '../../components/AppIcon.vue'
import UserAvatar from '../../components/UserAvatar.vue'
import { formatDateTime } from '../../utils/datetime'

const route = useRoute()
const router = useRouter()

const postId = computed(() => String(route.params.id ?? ''))

const loading = ref(true)
const notFound = ref(false)
const loadError = ref('')
const detail = ref<AdminPostDetail | null>(null)

const deleting = ref(false)
const actionError = ref('')

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

const KIND_LABELS: Record<string, string> = {
  image: '图片',
  video: '视频',
  file: '文件',
}

function visibilityLabel(value: string): string {
  return VISIBILITY_LABELS[value] ?? value
}

function statusLabel(value: string): string {
  return STATUS_LABELS[value] ?? value
}

function kindLabel(value: string): string {
  return KIND_LABELS[value] ?? value
}

function statusClass(value: string): string {
  if (value === 'deleted') return 'badge--danger'
  if (value === 'published') return ''
  return 'badge--muted'
}

/** 请求序号：快速切换 :id 时作废旧响应，避免旧帖子覆盖新帖子 */
let loadSequence = 0

async function load(): Promise<void> {
  const current = ++loadSequence
  loading.value = true
  notFound.value = false
  loadError.value = ''
  detail.value = null
  try {
    const data = await getAdminPost(postId.value)
    if (current !== loadSequence) return
    detail.value = data
  } catch (error) {
    if (current !== loadSequence) return
    if (error instanceof ApiError && error.status === 404) {
      notFound.value = true
    } else {
      loadError.value = errorMessage(error)
    }
  } finally {
    if (current === loadSequence) loading.value = false
  }
}

watch(postId, () => void load(), { immediate: true })

async function onDelete(): Promise<void> {
  const post = detail.value?.post
  if (!post) return
  if (
    !window.confirm(
      '确认删除这条帖子？删除为逻辑删除：内容不再对外展示，历史版本与附件记录保留，操作会记入审计日志。',
    )
  ) {
    return
  }
  deleting.value = true
  actionError.value = ''
  try {
    await deleteAdminPost(post.id)
    await router.push('/admin/content/posts')
  } catch (error) {
    actionError.value = errorMessage(error)
  } finally {
    deleting.value = false
  }
}
</script>

<template>
  <div class="stack">
    <RouterLink to="/admin/content/posts" class="back-link">
      <AppIcon name="arrow-left" :size="16" />
      返回帖子审核
    </RouterLink>

    <p v-if="loading" class="card card--pad loading-row"><span class="spinner" aria-hidden="true"></span> 加载中…</p>

    <section v-else-if="notFound" class="card card--pad">
      <div class="empty">
        <AppIcon name="file-text" :size="26" />
        <strong>帖子不存在或不可见</strong>
        <p>该 ID 可能无效，或帖子已被移除。</p>
      </div>
    </section>

    <template v-else-if="loadError">
      <AlertNote kind="error">{{ loadError }}</AlertNote>
      <button class="btn btn--secondary btn--sm self-start" type="button" @click="load">重试</button>
    </template>

    <template v-else-if="detail">
      <section class="card card--pad">
        <div class="post-head">
          <UserAvatar :user="detail.post.author" size="md" />
          <div class="post-meta">
            <p class="post-author">
              <span class="author-name">{{ detail.post.author.nickname || detail.post.author.username }}</span>
              <span v-if="detail.post.author.is_ai" class="badge badge--ai">AI</span>
              <span v-if="detail.post.author.is_suspended" class="badge badge--danger">作者已停用</span>
            </p>
            <p class="muted">@{{ detail.post.author.username }}</p>
          </div>
          <div class="post-badges">
            <span class="badge" :class="statusClass(detail.post.status)">{{ statusLabel(detail.post.status) }}</span>
            <span class="badge badge--muted">{{ visibilityLabel(detail.post.visibility) }}</span>
          </div>
        </div>

        <dl class="info-list detail-info">
          <dt>帖子 ID</dt>
          <dd class="mono">{{ detail.post.id }}</dd>
          <dt>作者 ID</dt>
          <dd class="mono">{{ detail.post.author.id }}</dd>
          <dt>发布时间</dt>
          <dd>{{ detail.post.published_at ? formatDateTime(detail.post.published_at) : '—' }}</dd>
          <dt>创建时间</dt>
          <dd>{{ formatDateTime(detail.post.created_at) }}</dd>
          <dt>互动计数</dt>
          <dd class="mono">
            赞 {{ detail.post.like_count }} · 评论 {{ detail.post.comment_count }} · 转发 {{ detail.post.repost_count }} · 附件
            {{ detail.post.attachment_count }}
          </dd>
        </dl>

        <h2 class="section-title">正文</h2>
        <!-- content_html 由后端转换并清洗，禁止在前端拼装 HTML -->
        <div v-if="detail.post.content_html" class="post-body" v-html="detail.post.content_html"></div>
        <p v-else class="muted">（无正文）</p>
      </section>

      <section class="card card--pad">
        <h2 class="page-title">历史版本</h2>
        <p class="page-desc">已发布帖子的每次编辑都会追加版本，不覆盖旧历史。</p>
        <ul v-if="detail.versions.length > 0" class="plain-list">
          <li v-for="version in detail.versions" :key="version.id" class="plain-row">
            <span class="row-main">第 {{ version.version_number }} 版</span>
            <span class="badge badge--muted">{{ visibilityLabel(version.visibility) }}</span>
            <span class="row-time mono">{{ formatDateTime(version.created_at) }}</span>
          </li>
        </ul>
        <p v-else class="muted">暂无历史版本。</p>
      </section>

      <section class="card card--pad">
        <h2 class="page-title">附件</h2>
        <p class="page-desc">附件与衍生文件只标记状态，不做物理删除。</p>
        <ul v-if="detail.attachments.length > 0" class="plain-list">
          <li v-for="attachment in detail.attachments" :key="attachment.id" class="plain-row">
            <span class="row-main">{{ attachment.original_filename }}</span>
            <span class="badge badge--muted">{{ kindLabel(attachment.kind) }}</span>
            <span class="badge" :class="attachment.status === 'deleted' ? 'badge--danger' : ''">
              {{ attachment.status === 'deleted' ? '已删除' : '可用' }}
            </span>
          </li>
        </ul>
        <p v-else class="muted">该帖子没有附件。</p>
      </section>

      <section class="card card--pad">
        <h2 class="page-title">处置</h2>
        <p class="page-desc">逻辑删除后作者与读者都不再看到该内容，操作会写入审计日志。</p>

        <AlertNote v-if="actionError" kind="error">{{ actionError }}</AlertNote>

        <button class="btn btn--danger" type="button" :disabled="deleting" @click="onDelete">
          <span v-if="deleting" class="spinner" aria-hidden="true"></span>
          {{ deleting ? '删除中…' : '逻辑删除帖子' }}
        </button>
      </section>
    </template>
  </div>
</template>

<style scoped>
.back-link {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  align-self: flex-start;
  font-size: 13.5px;
  color: var(--ink-2);
}
.back-link:hover {
  color: var(--ink);
  text-decoration: none;
}
.self-start {
  align-self: flex-start;
}
.post-head {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
.post-meta {
  min-width: 0;
}
.post-author {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  margin: 0;
  font-size: 16px;
  font-weight: 800;
}
.post-meta .muted {
  margin: 0;
}
.post-badges {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-left: auto;
}
.detail-info {
  margin-top: 16px;
}
.section-title {
  margin: 22px 0 8px;
  font-size: 14px;
  font-weight: 800;
}
.post-body {
  max-height: 480px;
  overflow-y: auto;
  padding: 12px 14px;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--surface-subtle);
  font-size: 14.5px;
  line-height: 1.8;
  word-break: break-word;
}
.post-body :deep(img) {
  max-width: 100%;
  height: auto;
}
.post-body :deep(pre) {
  overflow-x: auto;
}
.plain-list {
  margin: 14px 0 0;
  padding: 0;
  list-style: none;
}
.plain-row {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  padding: 9px 0;
  border-bottom: 1px solid var(--border);
  font-size: 13.5px;
}
.plain-row:last-child {
  border-bottom: none;
}
.row-main {
  min-width: 0;
  word-break: break-all;
}
.row-time {
  margin-left: auto;
  font-size: 12.5px;
  color: var(--ink-3);
  white-space: nowrap;
}
</style>
