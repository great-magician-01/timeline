<script setup lang="ts">
// A-14 AI 生成与发布：生成草稿、直接发布、编辑并发布草稿
// 子页面自行读取路由参数 aiId（由 /admin/ai/:id 提供）
import { computed, reactive, ref } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { generateAndPublish, generateDraft, listAIDrafts, publishAIDraft, updateAIDraft } from '../../api/aiAdmin'
import type { AIDraft } from '../../api/aiAdmin'
import { errorMessage } from '../../api/client'
import AlertNote from '../../components/AlertNote.vue'
import AppIcon from '../../components/AppIcon.vue'
import { useCursorList } from '../../composables/useCursorList'
import { formatDateTime } from '../../utils/datetime'

type DraftFilters = Record<string, never>

const route = useRoute()
const aiId = computed(() => String(route.params.id ?? ''))

const prompt = ref('')
const submitting = ref(false)
const actionError = ref('')
const actionNotice = ref('')
const lastTaskId = ref('')

const {
  items,
  loadingMore,
  hasMore,
  error: listError,
  loaded,
  reload,
  loadMore,
} = useCursorList<AIDraft, DraftFilters>({
  fetch: (params) => listAIDrafts(aiId.value, params),
  params: () => ({}),
})

const initialLoading = computed(() => !loaded.value && !listError.value)

async function onGenerate(mode: 'draft' | 'publish'): Promise<void> {
  if (
    mode === 'publish' &&
    !window.confirm('确认直接生成并发布？生成完成后内容会立即按可见性对读者展示，不再经过草稿审核。')
  ) {
    return
  }
  submitting.value = true
  actionError.value = ''
  actionNotice.value = ''
  lastTaskId.value = ''
  try {
    const data = mode === 'draft' ? await generateDraft(aiId.value, prompt.value.trim() || undefined) : await generateAndPublish(aiId.value, prompt.value.trim() || undefined)
    lastTaskId.value = data.task_id
    actionNotice.value =
      mode === 'draft'
        ? '已提交生成草稿任务，完成后会出现在下方草稿列表。'
        : '已提交生成并发布任务，完成后内容会直接发布。'
    if (mode === 'draft') await reload()
  } catch (error) {
    actionError.value = errorMessage(error)
  } finally {
    submitting.value = false
  }
}

// ---------- 草稿编辑与发布 ----------
const editingId = ref<string | null>(null)
const editForm = reactive({ content_source: '' })
const savingId = ref<string | null>(null)
const publishingId = ref<string | null>(null)

function startEdit(draft: AIDraft): void {
  editingId.value = draft.id
  editForm.content_source = draft.content_source
  actionError.value = ''
  actionNotice.value = ''
}

function cancelEdit(): void {
  editingId.value = null
}

async function onSaveDraft(draft: AIDraft): Promise<void> {
  const content = editForm.content_source.trim()
  if (!content) {
    actionError.value = '草稿正文不能为空。'
    return
  }
  savingId.value = draft.id
  actionError.value = ''
  actionNotice.value = ''
  try {
    await updateAIDraft(aiId.value, draft.id, { content_source: content })
    editingId.value = null
    actionNotice.value = '草稿已保存；保存后仍需发布才会对外展示。'
    await reload()
  } catch (error) {
    actionError.value = errorMessage(error)
  } finally {
    savingId.value = null
  }
}

async function onPublish(draft: AIDraft): Promise<void> {
  if (!window.confirm('确认发布这条草稿？发布后内容立即对可见范围内的读者展示。')) return
  publishingId.value = draft.id
  actionError.value = ''
  actionNotice.value = ''
  try {
    await publishAIDraft(aiId.value, draft.id)
    actionNotice.value = '草稿已发布。'
    await reload()
  } catch (error) {
    actionError.value = errorMessage(error)
  } finally {
    publishingId.value = null
  }
}
</script>

<template>
  <div class="stack">
    <section class="card card--pad">
      <h2 class="page-title">手动生成</h2>
      <p class="page-desc">
        生成与发布都是异步任务，可留空提示词让模型按身份设定自行选题；任务进度在「任务」页面查看。
      </p>

      <AlertNote v-if="actionNotice" kind="success">
        {{ actionNotice }}
        <template v-if="lastTaskId">
          任务已提交（<span class="mono">{{ lastTaskId }}</span>），可在任务页查看进度。
        </template>
      </AlertNote>
      <AlertNote v-if="actionError" kind="error">{{ actionError }}</AlertNote>

      <div class="form">
        <label class="field-label" for="publish-prompt">提示词（可选）</label>
        <textarea
          id="publish-prompt"
          v-model="prompt"
          class="textarea"
          name="prompt"
          placeholder="例如：写一条关于周末城市散步的短帖"
          :disabled="submitting"
        ></textarea>

        <div class="actions">
          <button class="btn btn--primary" type="button" :disabled="submitting" @click="onGenerate('draft')">
            <AppIcon name="file-text" :size="16" />
            生成草稿
          </button>
          <button class="btn btn--secondary" type="button" :disabled="submitting" @click="onGenerate('publish')">
            <AppIcon name="send" :size="16" />
            直接生成并发布
          </button>
          <RouterLink :to="`/admin/ai/${aiId}/tasks`" class="btn btn--secondary">查看任务</RouterLink>
        </div>

        <p v-if="submitting" class="loading-row">
          <span class="spinner" aria-hidden="true"></span> 正在提交任务…
        </p>
      </div>
    </section>

    <section class="card card--pad">
      <h2 class="page-title">AI 草稿</h2>
      <p class="page-desc">AI 草稿在发布前仅管理员可见，不会出现在作者主页或时间线上。</p>

      <p v-if="initialLoading" class="loading-row"><span class="spinner" aria-hidden="true"></span> 加载中…</p>

      <template v-else-if="listError && items.length === 0">
        <AlertNote kind="error">{{ listError }}</AlertNote>
        <button class="btn btn--secondary btn--sm self-start" type="button" @click="reload">重试</button>
      </template>

      <div v-else-if="items.length === 0" class="empty">
        <AppIcon name="file-text" :size="26" />
        <strong>暂无 AI 草稿</strong>
        <p>使用上方「生成草稿」提交任务，完成后草稿会出现在这里。</p>
      </div>

      <div v-else class="draft-list">
        <article v-for="draft in items" :key="draft.id" class="draft-row">
          <div class="draft-head">
            <span class="muted mono">草稿 {{ draft.id }}</span>
            <span class="badge badge--muted">{{ draft.visibility }}</span>
            <span class="muted mono">r{{ draft.revision }}</span>
            <span class="draft-time mono">更新于 {{ formatDateTime(draft.updated_at) }}</span>
          </div>

          <form v-if="editingId === draft.id" class="draft-edit" novalidate @submit.prevent="onSaveDraft(draft)">
            <label class="field-label" for="draft-content">正文（Markdown）</label>
            <textarea
              id="draft-content"
              v-model="editForm.content_source"
              class="textarea draft-textarea"
              name="content_source"
              :disabled="savingId === draft.id"
            ></textarea>
            <div class="actions">
              <button class="btn btn--primary btn--sm" type="submit" :disabled="savingId === draft.id">
                <span v-if="savingId === draft.id" class="spinner" aria-hidden="true"></span>
                {{ savingId === draft.id ? '保存中…' : '保存草稿' }}
              </button>
              <button
                class="btn btn--secondary btn--sm"
                type="button"
                :disabled="savingId === draft.id"
                @click="cancelEdit"
              >
                取消
              </button>
            </div>
          </form>

          <template v-else>
            <!-- content_html 由后端转换并清洗 -->
            <div v-if="draft.content_html" class="draft-body" v-html="draft.content_html"></div>
            <p v-else class="muted">（空正文）</p>
            <div class="actions">
              <button class="btn btn--secondary btn--sm" type="button" @click="startEdit(draft)">编辑</button>
              <button
                class="btn btn--primary btn--sm"
                type="button"
                :disabled="publishingId === draft.id"
                @click="onPublish(draft)"
              >
                <span v-if="publishingId === draft.id" class="spinner" aria-hidden="true"></span>
                {{ publishingId === draft.id ? '发布中…' : '发布' }}
              </button>
            </div>
          </template>
        </article>

        <div class="list-foot">
          <button v-if="hasMore && !loadingMore" class="btn btn--secondary btn--sm" type="button" @click="loadMore">
            加载更多
          </button>
          <p v-else-if="loadingMore" class="loading-row list-foot-loading">
            <span class="spinner" aria-hidden="true"></span> 正在加载…
          </p>
          <p v-else class="muted">已加载全部 {{ items.length }} 条草稿</p>
        </div>
      </div>

      <AlertNote v-if="listError && items.length > 0" kind="error">{{ listError }}</AlertNote>
    </section>
  </div>
</template>

<style scoped>
.self-start {
  align-self: flex-start;
}
.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 12px;
}
.draft-list {
  margin-top: 14px;
}
.draft-row {
  padding: 14px 0;
  border-bottom: 1px solid var(--border);
}
.draft-row:last-child {
  border-bottom: none;
}
.draft-head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  font-size: 12.5px;
}
.draft-time {
  margin-left: auto;
  color: var(--ink-3);
  white-space: nowrap;
}
.draft-body {
  max-height: 260px;
  overflow-y: auto;
  margin-top: 8px;
  padding: 12px 14px;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--surface-subtle);
  font-size: 14px;
  line-height: 1.8;
  word-break: break-word;
}
.draft-body :deep(img) {
  max-width: 100%;
  height: auto;
}
.draft-edit {
  margin-top: 10px;
}
.draft-textarea {
  min-height: 180px;
  font-family: inherit;
}
.list-foot {
  display: flex;
  justify-content: center;
  padding: 14px 0 0;
}
.list-foot .muted {
  margin: 0;
}
.list-foot-loading {
  padding: 0;
}
</style>
