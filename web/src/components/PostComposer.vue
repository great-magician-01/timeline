<script setup lang="ts">
// 帖子编辑器：新建、草稿自动保存与冲突处理、定时发布、已发布帖编辑
// 状态提示与冲突处理遵循 docs/WORKFLOWS.md §3.4 与 docs/REQUIREMENTS.md 6.4。
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import AppIcon from './AppIcon.vue'
import AlertNote from './AlertNote.vue'
import MediaUploader from './MediaUploader.vue'
import RichTextEditor from './RichTextEditor.vue'
import UserAvatar from './UserAvatar.vue'
import type { Attachment, PostDetail, Visibility } from '../api/types'
import { errorMessage, fieldErrors } from '../api/client'
import {
  cancelSchedule,
  createPost,
  getPost,
  publishPost,
  schedulePost,
  updatePost,
} from '../api/posts'
import { fromDatetimeLocal, toDatetimeLocal } from '../utils/format'

const props = withDefaults(
  defineProps<{
    /** 编辑已有帖子（草稿、定时或已发布）时传入 */
    postId?: string
    /** 引用转发时传入原帖作者信息由父组件加载 */
    quotedPost?: PostDetail | null
  }>(),
  { postId: undefined, quotedPost: null },
)

const emit = defineEmits<{ (event: 'published', id: string): void }>()
const router = useRouter()

const content = ref('')
const visibility = ref<Visibility>('public')
const attachments = ref<Attachment[]>([])
const quotedPostId = ref<string | null>(props.quotedPost?.id ?? null)

const loading = ref(false)
const busy = ref(false)
const errorText = ref('')
const fieldError = ref<Record<string, string>>({})
const status = ref<PostDetail | null>(null)
const revision = ref<number | null>(null)
/** 自动保存状态：'' | saving | saved | failed | conflict */
const saveState = ref<'' | 'saving' | 'saved' | 'failed' | 'conflict'>('')
const lastSavedAt = ref<string>('')
const conflict = ref<{ revision: number; content_source: string; content_html: string; updated_at: string } | null>(
  null,
)
const scheduleAt = ref('')
const showSchedule = ref(false)

const isPublished = computed(() => status.value?.status === 'published')
const isDraftLike = computed(() => !status.value || status.value.status === 'draft' || status.value.status === 'scheduled')
const isScheduled = computed(() => status.value?.status === 'scheduled')
const hasContent = computed(() => content.value.trim().length > 0)
const canSubmit = computed(() => hasContent.value && !busy.value)

let autosaveTimer: number | null = null
let dirty = false
/** 正在把服务端返回的结果写回编辑器：此时 content 变化不应把「已保存」重新标为未保存 */
let applyingServer = false

/**
 * 写入服务端返回的正文，并等本轮渲染前的 watcher 跑完再解除标记。
 * content 的 watcher（默认 flush: 'pre'）在赋值后的微任务中执行；
 * 不加标记时它会调用 markDirty()，把刚设好的「已保存」重新标成「有未保存的修改」，
 * 并让 20 秒后的自动保存多发一次请求。
 */
async function applyServerContent(html: string): Promise<void> {
  applyingServer = true
  content.value = html
  try {
    await nextTick()
  } finally {
    applyingServer = false
  }
}

function markDirty(): void {
  if (applyingServer) return
  dirty = true
  if (saveState.value === 'saved') saveState.value = ''
}

content.value = ''
watch(content, markDirty)

async function load(): Promise<void> {
  if (!props.postId) {
    // 引用转发：从查询参数带入的原帖
    if (props.quotedPost) {
      quotedPostId.value = props.quotedPost.id
      visibility.value = 'public'
    }
    return
  }
  loading.value = true
  try {
    const result = await getPost(props.postId)
    await applyPost(result.post)
  } catch (err) {
    errorText.value = errorMessage(err)
  } finally {
    loading.value = false
  }
}

async function applyPost(post: PostDetail): Promise<void> {
  status.value = post
  revision.value = post.revision ?? null
  visibility.value = post.visibility
  attachments.value = post.attachments
  quotedPostId.value = post.quoted_post?.post?.id ?? null
  if (post.scheduled_at) scheduleAt.value = toDatetimeLocal(post.scheduled_at)
  dirty = false
  saveState.value = ''
  await applyServerContent(post.content_html)
}

onMounted(() => {
  void load()
  // 草稿与定时帖每 20 秒自动保存一次；已发布帖不自动保存，避免静默生成版本
  autosaveTimer = window.setInterval(() => {
    if (!dirty || busy.value || conflict.value) return
    if (!isDraftLike.value || !status.value) return
    // 空正文没有可保存的内容，跳过请求，避免周期性产生虚假的「保存失败」
    if (!hasContent.value) return
    void submit('autosave')
  }, 20_000)
})

onBeforeUnmount(() => {
  if (autosaveTimer !== null) window.clearInterval(autosaveTimer)
})

interface SubmitOptions {
  /** autosave：仅草稿/定时帖的静默保存 */
  mode: 'autosave' | 'draft' | 'publish' | 'schedule' | 'save-published'
}

async function submit(mode: SubmitOptions['mode']): Promise<void> {
  if (busy.value) return
  busy.value = true
  if (mode === 'autosave') saveState.value = 'saving'
  errorText.value = ''
  fieldError.value = {}
  const payload = {
    content_format: 'richtext' as const,
    content_source: content.value,
    visibility: visibility.value,
    attachment_ids: attachments.value.map((item) => item.id),
    quoted_post_id: quotedPostId.value ?? undefined,
  }
  try {
    if (!status.value) {
      const action = mode === 'publish' || mode === 'save-published' ? 'publish' : mode === 'schedule' ? 'schedule' : 'draft'
      const result = await createPost({
        ...payload,
        action,
        scheduled_at: action === 'schedule' ? fromDatetimeLocal(scheduleAt.value) : undefined,
      })
      await applyPost(result.post)
      if (result.post.status === 'published') {
        emit('published', result.post.id)
        return
      }
      saveState.value = 'saved'
      lastSavedAt.value = result.post.published_at ?? ''
      // 保存后切到编辑路由，后续自动保存命中同一草稿
      void router.replace({ name: 'draft-edit', params: { draftId: result.post.id } })
      return
    }

    if (isPublished.value) {
      const result = await updatePost(status.value.id, payload)
      await applyPost(result.post)
      saveState.value = 'saved'
      return
    }

    const result = await updatePost(status.value.id, { ...payload, revision: revision.value ?? undefined })
    await applyPost(result.post)
    saveState.value = 'saved'
  } catch (err) {
    if (mode === 'autosave') {
      saveState.value = 'failed'
    } else {
      errorText.value = errorMessage(err)
      fieldError.value = fieldErrors(err)
    }
    handleConflict(err)
  } finally {
    busy.value = false
  }
}

function handleConflict(err: unknown): void {
  const details = (err as { details?: unknown }).details
  if (!details || typeof details !== 'object') return
  const payload = details as Record<string, unknown>
  if (typeof payload.revision !== 'number') return
  conflict.value = {
    revision: payload.revision,
    content_source: String(payload.content_source ?? ''),
    content_html: String(payload.content_html ?? ''),
    updated_at: String(payload.updated_at ?? ''),
  }
  saveState.value = 'conflict'
}

/** 采用服务端最新版本，放弃本地未保存内容 */
function useServerVersion(): void {
  if (!conflict.value) return
  revision.value = conflict.value.revision
  // 同样走 applyServerContent，避免 content watcher 把本次应用标成未保存修改
  void applyServerContent(conflict.value.content_html)
  conflict.value = null
  saveState.value = ''
  dirty = false
}

/** 用本地内容覆盖服务端（携带最新 revision 再次提交） */
async function overwriteServer(): Promise<void> {
  if (!conflict.value) return
  revision.value = conflict.value.revision
  conflict.value = null
  await submit('draft')
}

async function publishNow(): Promise<void> {
  if (!status.value) {
    await submit('publish')
    return
  }
  busy.value = true
  errorText.value = ''
  try {
    const saved = await updatePost(status.value.id, {
      content_format: 'richtext',
      content_source: content.value,
      visibility: visibility.value,
      attachment_ids: attachments.value.map((item) => item.id),
      quoted_post_id: quotedPostId.value ?? undefined,
      revision: isPublished.value ? undefined : revision.value ?? undefined,
    })
    await applyPost(saved.post)
    const result = await publishPost(status.value.id)
    await applyPost(result.post)
    emit('published', result.post.id)
  } catch (err) {
    errorText.value = errorMessage(err)
    handleConflict(err)
  } finally {
    busy.value = false
  }
}

async function confirmSchedule(): Promise<void> {
  if (!scheduleAt.value) {
    errorText.value = '请选择发布时间'
    return
  }
  if (!status.value) {
    await submit('schedule')
    return
  }
  busy.value = true
  errorText.value = ''
  try {
    await updatePost(status.value.id, {
      content_format: 'richtext',
      content_source: content.value,
      visibility: visibility.value,
      attachment_ids: attachments.value.map((item) => item.id),
      quoted_post_id: quotedPostId.value ?? undefined,
      revision: isPublished.value ? undefined : revision.value ?? undefined,
    })
    const result = await schedulePost(status.value.id, fromDatetimeLocal(scheduleAt.value))
    await applyPost(result.post)
  } catch (err) {
    errorText.value = errorMessage(err)
  } finally {
    busy.value = false
  }
}

async function cancelScheduled(): Promise<void> {
  if (!status.value) return
  busy.value = true
  try {
    const result = await cancelSchedule(status.value.id)
    await applyPost(result.post)
    scheduleAt.value = ''
  } catch (err) {
    errorText.value = errorMessage(err)
  } finally {
    busy.value = false
  }
}

function onAttachmentAdded(attachment: Attachment): void {
  attachments.value = [...attachments.value, attachment]
  markDirty()
}

function onAttachmentRemoved(id: string): void {
  attachments.value = attachments.value.filter((item) => item.id !== id)
  markDirty()
}

const saveLabel = computed(() => {
  switch (saveState.value) {
    case 'saving':
      return '保存中…'
    case 'saved':
      return '已保存'
    case 'failed':
      return '保存失败，内容仍保留在编辑器中'
    case 'conflict':
      return '存在冲突，请先处理'
    default:
      return dirty ? '有未保存的修改' : ''
  }
})
</script>

<template>
  <div class="composer stack">
    <AlertNote v-if="loading" kind="info">载入中…</AlertNote>
    <AlertNote v-if="errorText" kind="error">{{ errorText }}</AlertNote>

    <div v-if="conflict" class="conflict card card--pad">
      <h3>草稿已在其他位置被更新</h3>
      <p class="muted">服务端最新版本时间：{{ conflict.updated_at || '未知' }}。请选择保留哪一份内容。</p>
      <div class="conflict-actions">
        <button class="btn btn--secondary" type="button" @click="useServerVersion">使用服务端版本</button>
        <button class="btn" type="button" :disabled="busy" @click="overwriteServer">用我的内容覆盖</button>
      </div>
    </div>

    <div v-if="props.quotedPost" class="quote-banner card card--pad">
      <p class="muted">引用转发</p>
      <RouterLink :to="`/posts/${props.quotedPost.id}`" class="quote-head">
        <UserAvatar :user="props.quotedPost.author" size="sm" />
        <strong>{{ props.quotedPost.author.nickname || props.quotedPost.author.username }}</strong>
      </RouterLink>
      <div class="quote-content" v-html="props.quotedPost.content_html"></div>
    </div>

    <RichTextEditor v-model="content" :disabled="busy" placeholder="写点什么，支持 #话题# 与 @提及" />

    <div class="row">
      <label class="label" for="visibility">可见性</label>
      <select id="visibility" v-model="visibility" class="input" :disabled="busy">
        <option value="public">公开：所有人可见</option>
        <option value="followers">仅关注者</option>
        <option value="private">仅自己</option>
      </select>
      <span class="muted">只有公开帖子允许被转发或引用</span>
    </div>

    <MediaUploader
      :attachments="attachments"
      :disabled="busy"
      @added="onAttachmentAdded"
      @removed="onAttachmentRemoved"
    />

    <p v-if="fieldError.content_source" class="field-error">{{ fieldError.content_source }}</p>

    <div class="actions">
      <span class="save-state" :class="{ 'is-error': saveState === 'failed' || saveState === 'conflict' }">
        {{ saveLabel }}
      </span>
      <template v-if="!isPublished">
        <button class="btn btn--secondary" type="button" :disabled="busy || !canSubmit" @click="submit('draft')">
          保存草稿
        </button>
        <button class="btn btn--secondary" type="button" :disabled="busy || !canSubmit" @click="showSchedule = !showSchedule">
          {{ isScheduled ? '修改定时' : '定时发布' }}
        </button>
        <button class="btn btn--primary" type="button" :disabled="busy || !canSubmit" @click="publishNow">
          {{ isScheduled ? '立即发布' : '发布' }}
        </button>
      </template>
      <template v-else>
        <RouterLink class="btn btn--secondary" :to="{ name: 'post-history', params: { postId: status?.id } }">
          历史版本
        </RouterLink>
        <button class="btn btn--primary" type="button" :disabled="busy || !canSubmit" @click="submit('save-published')">
          保存修改
        </button>
      </template>
    </div>

    <div v-if="showSchedule && !isPublished" class="schedule card card--pad">
      <label class="label" for="schedule-at">发布时间（北京时间）</label>
      <input id="schedule-at" v-model="scheduleAt" class="input" type="datetime-local" :disabled="busy" />
      <div class="schedule-actions">
        <button class="btn btn--primary" type="button" :disabled="busy || !scheduleAt" @click="confirmSchedule">
          设为定时
        </button>
        <button v-if="isScheduled" class="btn btn--secondary" type="button" :disabled="busy" @click="cancelScheduled">
          取消定时
        </button>
      </div>
      <p class="muted">到点后由服务端任务按当前可见性发布；应用重启不会丢失。</p>
    </div>

    <p v-if="isPublished" class="muted">
      <AppIcon name="info" :size="14" />
      保存后会生成新版本，历史版本与其中的附件仍可被有权查看者查阅。
    </p>
  </div>
</template>

<style scoped>
.composer {
  gap: 12px;
}
.row {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.label {
  font-size: 13px;
  font-weight: 600;
  color: var(--ink-2);
}
.row .input {
  max-width: 220px;
}
.actions {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.save-state {
  margin-right: auto;
  font-size: 12.5px;
  color: var(--ink-3);
}
.save-state.is-error {
  color: var(--red);
}
.conflict h3 {
  margin: 0 0 6px;
  font-size: 15px;
}
.conflict-actions,
.schedule-actions {
  display: flex;
  gap: 8px;
  margin-top: 8px;
}
.quote-banner {
  display: grid;
  gap: 6px;
}
.quote-banner .muted {
  margin: 0;
  font-size: 12px;
}
.quote-head {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13.5px;
  color: var(--ink);
}
.quote-content {
  font-size: 13.5px;
  color: var(--ink-2);
}
.schedule {
  display: grid;
  gap: 8px;
}
</style>
