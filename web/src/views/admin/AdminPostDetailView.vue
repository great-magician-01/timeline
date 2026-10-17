<script setup lang="ts">
// A-06 后台帖子详情：正文、历史版本、附件与逻辑删除
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { deleteAdminAttachment, deleteAdminPost, getAdminPost } from '../../api/adminContent'
import type { AdminPostAttachment, AdminPostDetail, AdminPostVersion } from '../../api/adminContent'
import { ApiError, errorMessage } from '../../api/client'
import AlertNote from '../../components/AlertNote.vue'
import AppIcon from '../../components/AppIcon.vue'
import UserAvatar from '../../components/UserAvatar.vue'
import { formatDateTime } from '../../utils/datetime'
import { ui } from '../../styles/ui'

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
  if (value === 'deleted') return ui.badgeDanger
  if (value === 'published') return ''
  return ui.badgeMuted
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

// ---------- 附件处置 ----------

const deletingAttachmentId = ref('')
const attachmentError = ref('')
const attachmentNotice = ref('')

async function onDeleteAttachment(attachment: AdminPostAttachment): Promise<void> {
  if (
    !window.confirm(
      `确认逻辑删除附件「${attachment.original_filename}」？附件记录与文件会保留，只标记为失效，操作会写入审计日志。`,
    )
  ) {
    return
  }
  deletingAttachmentId.value = attachment.id
  attachmentError.value = ''
  attachmentNotice.value = ''
  try {
    await deleteAdminAttachment(attachment.id)
    attachmentNotice.value = '附件已逻辑删除。'
    await load()
  } catch (error) {
    attachmentError.value = errorMessage(error)
  } finally {
    deletingAttachmentId.value = ''
  }
}

// ---------- 历史版本正文 ----------

const expandedVersionId = ref('')

function toggleVersion(version: AdminPostVersion): void {
  expandedVersionId.value = expandedVersionId.value === version.id ? '' : version.id
}
</script>

<template>
  <div :class="ui.stack">
    <RouterLink to="/admin/content/posts" class="inline-flex items-center gap-[6px] self-start text-[13.5px] text-ink-2 hover:text-ink hover:no-underline">
      <AppIcon name="arrow-left" :size="16" />
      返回帖子审核
    </RouterLink>

    <p v-if="loading" :class="[ui.card, ui.cardPad, ui.loadingRow]"><span :class="ui.spinner" aria-hidden="true"></span> 加载中…</p>

    <section v-else-if="notFound" :class="[ui.card, ui.cardPad]">
      <div :class="ui.empty">
        <AppIcon name="file-text" :size="26" :class="ui.emptyIcon" />
        <strong :class="ui.emptyStrong">帖子不存在或不可见</strong>
        <p :class="ui.emptyText">该 ID 可能无效，或帖子已被移除。</p>
      </div>
    </section>

    <template v-else-if="loadError">
      <AlertNote kind="error">{{ loadError }}</AlertNote>
      <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'self-start']" type="button" @click="load">重试</button>
    </template>

    <template v-else-if="detail">
      <section :class="[ui.card, ui.cardPad]">
        <div class="flex flex-wrap items-center gap-3">
          <UserAvatar :user="detail.post.author" size="md" />
          <div class="min-w-0">
            <p class="m-0 flex flex-wrap items-center gap-[6px] text-[16px] font-extrabold">
              <span class="font-extrabold">{{ detail.post.author.nickname || detail.post.author.username }}</span>
              <span v-if="detail.post.author.is_ai" :class="[ui.badge, ui.badgeAi]">AI</span>
              <span v-if="detail.post.author.is_suspended" :class="[ui.badge, ui.badgeDanger]">作者已停用</span>
            </p>
            <p :class="[ui.muted, 'm-0']">@{{ detail.post.author.username }}</p>
          </div>
          <div class="ml-auto flex items-center gap-[6px]">
            <span :class="[ui.badge, statusClass(detail.post.status)]">{{ statusLabel(detail.post.status) }}</span>
            <span :class="[ui.badge, ui.badgeMuted]">{{ visibilityLabel(detail.post.visibility) }}</span>
          </div>
        </div>

        <dl :class="[ui.infoList, 'mt-4']">
          <dt :class="ui.infoTerm">帖子 ID</dt>
          <dd :class="[ui.infoDetails, ui.mono]">{{ detail.post.id }}</dd>
          <dt :class="ui.infoTerm">作者 ID</dt>
          <dd :class="[ui.infoDetails, ui.mono]">{{ detail.post.author.id }}</dd>
          <dt :class="ui.infoTerm">发布时间</dt>
          <dd :class="ui.infoDetails">{{ detail.post.published_at ? formatDateTime(detail.post.published_at) : '—' }}</dd>
          <dt :class="ui.infoTerm">创建时间</dt>
          <dd :class="ui.infoDetails">{{ formatDateTime(detail.post.created_at) }}</dd>
          <dt :class="ui.infoTerm">互动计数</dt>
          <dd :class="[ui.infoDetails, ui.mono]">
            赞 {{ detail.post.like_count }} · 评论 {{ detail.post.comment_count }} · 转发 {{ detail.post.repost_count }} · 附件
            {{ detail.post.attachment_count }}
          </dd>
        </dl>

        <h2 class="mx-0 mb-2 mt-[22px] text-[14px] font-extrabold">正文（含草稿正文）</h2>
        <!-- content_html 由后端转换并清洗，禁止在前端拼装 HTML -->
        <div v-if="detail.post.content_html" class="max-h-[480px] overflow-y-auto rounded-[10px] border border-solid border-border bg-surface-subtle px-[14px] py-3 text-[14.5px] leading-[1.8] [word-break:break-word] [&_img]:h-auto [&_img]:max-w-full [&_pre]:overflow-x-auto" v-html="detail.post.content_html"></div>
        <p v-else :class="ui.muted">（无正文）</p>
      </section>

      <section :class="[ui.card, ui.cardPad]">
        <h2 :class="ui.pageTitle">历史版本</h2>
        <p :class="ui.pageDesc">已发布帖子的每次编辑都会追加版本；展开可查看该版本完整正文。</p>
        <ul v-if="detail.versions.length > 0" class="m-0 mt-[14px] list-none p-0">
          <li v-for="version in detail.versions" :key="version.id" class="p-0">
            <div class="flex flex-wrap items-center gap-[10px] border-b border-solid border-border py-[9px] text-[13.5px] last:border-b-0">
              <span class="min-w-0 [word-break:break-all]">第 {{ version.version_number }} 版</span>
              <span :class="[ui.badge, ui.badgeMuted]">{{ visibilityLabel(version.visibility) }}</span>
              <span :class="[ui.mono, 'ml-auto whitespace-nowrap text-[12.5px] text-ink-3']">{{ formatDateTime(version.created_at) }}</span>
              <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]" type="button" @click="toggleVersion(version)">
                {{ expandedVersionId === version.id ? '收起正文' : '查看正文' }}
              </button>
            </div>
            <div
              v-if="expandedVersionId === version.id"
              class="mb-[10px] mt-0 rounded-[10px] border border-solid border-border bg-surface-subtle px-3 py-[10px] text-[13.5px] leading-[1.8] [word-break:break-word] [&_img]:h-auto [&_img]:max-w-full"
              v-html="version.content_html || '<p>（该版本无正文）</p>'"
            ></div>
          </li>
        </ul>
        <p v-else :class="ui.muted">暂无历史版本。</p>
      </section>

      <section :class="[ui.card, ui.cardPad]">
        <h2 :class="ui.pageTitle">附件</h2>
        <p :class="ui.pageDesc">附件与衍生文件只标记状态，不做物理删除；删除会写入审计日志。</p>

        <AlertNote v-if="attachmentNotice" kind="success">{{ attachmentNotice }}</AlertNote>
        <AlertNote v-if="attachmentError" kind="error">{{ attachmentError }}</AlertNote>

        <ul v-if="detail.attachments.length > 0" class="m-0 mt-[14px] list-none p-0">
          <li v-for="attachment in detail.attachments" :key="attachment.id" class="flex flex-wrap items-center gap-[10px] border-b border-solid border-border py-[9px] text-[13.5px] last:border-b-0">
            <span class="min-w-0 [word-break:break-all]">{{ attachment.original_filename }}</span>
            <span :class="[ui.badge, ui.badgeMuted]">{{ kindLabel(attachment.kind) }}</span>
            <span :class="[ui.badge, attachment.status === 'deleted' ? ui.badgeDanger : '']">
              {{ attachment.status === 'deleted' ? '已删除' : attachment.status === 'processing' ? '处理中' : '可用' }}
            </span>
            <button
              v-if="attachment.status !== 'deleted'"
              :class="[ui.button, ui.buttonDanger, ui.buttonSmall, 'ml-auto']"
              type="button"
              :disabled="deletingAttachmentId === attachment.id"
              @click="onDeleteAttachment(attachment)"
            >
              {{ deletingAttachmentId === attachment.id ? '删除中…' : '删除附件' }}
            </button>
          </li>
        </ul>
        <p v-else :class="ui.muted">该帖子没有附件。</p>
      </section>

      <section :class="[ui.card, ui.cardPad]">
        <h2 :class="ui.pageTitle">评论上下文（{{ detail.comment_count }}）</h2>
        <p :class="ui.pageDesc">供审核判断评论语境；删除评论请前往评论审核页面。</p>
        <ul v-if="detail.comments.length > 0" class="m-0 mt-[14px] grid list-none gap-[10px] p-0">
          <li v-for="comment in detail.comments" :key="comment.id" class="rounded-[10px] border border-solid border-border bg-surface-subtle px-3 py-[10px]">
            <div class="flex flex-wrap items-center gap-2 text-[13px]">
              <UserAvatar :user="comment.author" size="sm" />
              <span class="font-bold">{{ comment.author.nickname || comment.author.username }}</span>
              <span :class="ui.muted">@{{ comment.author.username }}</span>
              <span :class="[ui.badge, comment.status === 'deleted' ? ui.badgeDanger : ui.badgeMuted]">
                {{ comment.status === 'deleted' ? '已删除' : '正常' }}
              </span>
              <span :class="[ui.mono, 'ml-auto whitespace-nowrap text-[12.5px] text-ink-3']">{{ formatDateTime(comment.created_at) }}</span>
            </div>
            <div v-if="comment.status === 'deleted'" :class="ui.muted">评论已删除，内容不展示。</div>
            <div v-else class="mt-[6px] text-[13.5px] leading-[1.7] [word-break:break-word] [&_img]:h-auto [&_img]:max-w-full" v-html="comment.content_html"></div>
          </li>
        </ul>
        <p v-else :class="ui.muted">该帖子暂无评论。</p>
      </section>

      <section :class="[ui.card, ui.cardPad]">
        <h2 :class="ui.pageTitle">处置</h2>
        <p :class="ui.pageDesc">逻辑删除后作者与读者都不再看到该内容，操作会写入审计日志。</p>

        <AlertNote v-if="actionError" kind="error">{{ actionError }}</AlertNote>

        <button :class="[ui.button, ui.buttonDanger]" type="button" :disabled="deleting" @click="onDelete">
          <span v-if="deleting" :class="[ui.spinner, ui.buttonSpinner]" aria-hidden="true"></span>
          {{ deleting ? '删除中…' : '逻辑删除帖子' }}
        </button>
      </section>
    </template>
  </div>
</template>
