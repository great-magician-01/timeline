<script setup lang="ts">
// A-13 AI 记忆管理：近期/长期分区、添加、编辑、删除与按类型清空
// 子页面自行读取路由参数 aiId（由 /admin/ai/:id 提供）
import { computed, reactive, ref } from 'vue'
import { useRoute } from 'vue-router'
import { clearMemories, createMemory, deleteMemory, listMemories, updateMemory } from '../../api/aiAdmin'
import { errorMessage, fieldErrors } from '../../api/client'
import type { AIMemory } from '../../api/types'
import AlertNote from '../../components/AlertNote.vue'
import AppIcon from '../../components/AppIcon.vue'
import FormField from '../../components/FormField.vue'
import { useCursorList } from '../../composables/useCursorList'
import { formatDateTime } from '../../utils/datetime'

type MemoryType = 'recent' | 'long_term'

type MemoryFilters = {
  type: MemoryType
  q?: string
  status?: string
}

interface CategoryOption {
  value: string
  label: string
}

const CATEGORIES: CategoryOption[] = [
  { value: 'fact', label: '事实' },
  { value: 'preference', label: '偏好' },
  { value: 'relationship', label: '关系' },
  { value: 'event', label: '事件' },
  { value: 'topic', label: '话题' },
]

const STATUS_LABELS: Record<string, string> = {
  active: '生效中',
  superseded: '已被取代',
  invalidated: '已失效',
  deleted: '已删除',
}

const route = useRoute()
const aiId = computed(() => String(route.params.id ?? ''))

const memoryType = ref<MemoryType>('recent')
const filters = reactive({ q: '', status: '' })

const {
  items,
  loading,
  loadingMore,
  hasMore,
  error: listError,
  loaded,
  reload,
  loadMore,
} = useCursorList<AIMemory, MemoryFilters>({
  fetch: (params) => listMemories(aiId.value, params),
  params: () => ({
    type: memoryType.value,
    q: filters.q.trim() || undefined,
    status: filters.status || undefined,
  }),
})

const initialLoading = computed(() => !loaded.value && !listError.value)

function categoryLabel(value: string): string {
  return CATEGORIES.find((item) => item.value === value)?.label ?? value
}

function statusLabel(value: string): string {
  return STATUS_LABELS[value] ?? value
}

function setType(value: MemoryType): void {
  if (memoryType.value === value) return
  memoryType.value = value
  editingId.value = null
  void reload()
}

function setStatus(value: string): void {
  if (filters.status === value) return
  filters.status = value
  void reload()
}

function onSearch(): void {
  void reload()
}

function parseKeywords(value: string): string[] {
  const items = value
    .split(/[,，\s]+/)
    .map((item) => item.trim())
    .filter((item) => item.length > 0)
  return Array.from(new Set(items))
}

// ---------- 添加记忆 ----------
const createForm = reactive({ content: '', category: 'fact', keywords: '', importance: 3 })
const createErrors = ref<Record<string, string>>({})
const createError = ref('')
const createNotice = ref('')
const creating = ref(false)

async function onCreate(): Promise<void> {
  const next: Record<string, string> = {}
  if (!createForm.content.trim()) next.content = '请输入记忆内容'
  createErrors.value = next
  createError.value = ''
  createNotice.value = ''
  if (Object.keys(next).length > 0) return

  creating.value = true
  try {
    await createMemory(aiId.value, {
      memory_type: memoryType.value,
      content: createForm.content.trim(),
      category: createForm.category,
      keywords: parseKeywords(createForm.keywords),
      importance: createForm.importance,
    })
    createForm.content = ''
    createForm.keywords = ''
    createNotice.value = `已添加到${memoryType.value === 'recent' ? '近期' : '长期'}记忆。`
    await reload()
  } catch (error) {
    const fields = fieldErrors(error)
    if (Object.keys(fields).length > 0) createErrors.value = fields
    else createError.value = errorMessage(error)
  } finally {
    creating.value = false
  }
}

// ---------- 行内编辑 ----------
const editingId = ref<string | null>(null)
const editForm = reactive({ content: '', category: 'fact', keywords: '', importance: 3, status: 'active' })
const editErrors = ref<Record<string, string>>({})
const savingEdit = ref(false)
const busyId = ref<string | null>(null)
const actionError = ref('')
const actionNotice = ref('')

function startEdit(memory: AIMemory): void {
  editingId.value = memory.id
  editForm.content = memory.content
  editForm.category = memory.category
  editForm.keywords = memory.keywords.join(', ')
  editForm.importance = memory.importance
  editForm.status = memory.status
  editErrors.value = {}
  actionError.value = ''
  actionNotice.value = ''
}

function cancelEdit(): void {
  editingId.value = null
  editErrors.value = {}
}

async function onSaveEdit(memory: AIMemory): Promise<void> {
  const next: Record<string, string> = {}
  if (!editForm.content.trim()) next.content = '请输入记忆内容'
  editErrors.value = next
  actionError.value = ''
  actionNotice.value = ''
  if (Object.keys(next).length > 0) return

  savingEdit.value = true
  try {
    await updateMemory(aiId.value, memory.id, {
      content: editForm.content.trim(),
      category: editForm.category,
      keywords: parseKeywords(editForm.keywords),
      importance: editForm.importance,
      status: editForm.status,
    })
    editingId.value = null
    actionNotice.value = '记忆已更新。'
    await reload()
  } catch (error) {
    const fields = fieldErrors(error)
    if (Object.keys(fields).length > 0) editErrors.value = fields
    else actionError.value = errorMessage(error)
  } finally {
    savingEdit.value = false
  }
}

async function onDelete(memory: AIMemory): Promise<void> {
  if (!window.confirm('确认删除这条记忆？删除为逻辑删除：记忆不再参与生成，原始内容不受影响。')) return
  busyId.value = memory.id
  actionError.value = ''
  actionNotice.value = ''
  try {
    await deleteMemory(aiId.value, memory.id)
    actionNotice.value = '记忆已删除。'
    await reload()
  } catch (error) {
    actionError.value = errorMessage(error)
  } finally {
    busyId.value = null
  }
}

const clearing = ref(false)

async function onClear(): Promise<void> {
  const label = memoryType.value === 'recent' ? '近期' : '长期'
  const another = memoryType.value === 'recent' ? '长期' : '近期'
  if (
    !window.confirm(
      `确认清空全部${label}记忆？只清空「${label}记忆」，不影响${another}记忆，也不删除任何原始内容（帖子、评论等）。`,
    )
  ) {
    return
  }
  clearing.value = true
  actionError.value = ''
  actionNotice.value = ''
  try {
    const data = await clearMemories(aiId.value, memoryType.value)
    actionNotice.value = `已清空 ${data.updated} 条${label}记忆；${another}记忆与原始内容未受影响。`
    await reload()
  } catch (error) {
    actionError.value = errorMessage(error)
  } finally {
    clearing.value = false
  }
}
</script>

<template>
  <div class="stack">
    <section class="card card--pad">
      <div class="head-row">
        <div>
          <h2 class="page-title">记忆</h2>
          <p class="page-desc">手动维护的记忆同样参与生成；清空只影响当前分区。</p>
        </div>
        <div class="seg" role="group" aria-label="记忆类型">
          <button type="button" :class="{ 'is-active': memoryType === 'recent' }" @click="setType('recent')">
            近期记忆
          </button>
          <button type="button" :class="{ 'is-active': memoryType === 'long_term' }" @click="setType('long_term')">
            长期记忆
          </button>
        </div>
      </div>

      <form class="search-form" @submit.prevent="onSearch">
        <div class="search-box">
          <AppIcon name="search" :size="17" class="search-icon" />
          <input
            v-model="filters.q"
            class="input search-input"
            type="search"
            name="q"
            placeholder="搜索记忆内容或关键词"
            aria-label="搜索记忆"
          />
        </div>
        <button class="btn btn--primary" type="submit" :disabled="loading">查询</button>
      </form>

      <div class="filter-row">
        <div class="seg" role="group" aria-label="记忆状态筛选">
          <button type="button" :class="{ 'is-active': filters.status === '' }" @click="setStatus('')">全部状态</button>
          <button type="button" :class="{ 'is-active': filters.status === 'active' }" @click="setStatus('active')">
            生效中
          </button>
          <button
            type="button"
            :class="{ 'is-active': filters.status === 'superseded' }"
            @click="setStatus('superseded')"
          >
            已被取代
          </button>
          <button
            type="button"
            :class="{ 'is-active': filters.status === 'invalidated' }"
            @click="setStatus('invalidated')"
          >
            已失效
          </button>
        </div>
      </div>
    </section>

    <section class="card card--pad">
      <h2 class="page-title">添加记忆</h2>
      <p class="page-desc">
        将写入「{{ memoryType === 'recent' ? '近期记忆' : '长期记忆' }}」分区；添加后立即生效。
      </p>

      <AlertNote v-if="createNotice" kind="success">{{ createNotice }}</AlertNote>
      <AlertNote v-if="createError" kind="error">{{ createError }}</AlertNote>

      <form class="form" novalidate @submit.prevent="onCreate">
        <FormField label="内容" input-id="memory-content" :error="createErrors.content">
          <textarea
            id="memory-content"
            v-model="createForm.content"
            class="textarea"
            name="content"
            placeholder="用一句话描述需要记住的内容"
            :disabled="creating"
          ></textarea>
        </FormField>

        <div class="grid-three">
          <FormField label="分类" input-id="memory-category" :error="createErrors.category">
            <select
              id="memory-category"
              v-model="createForm.category"
              class="input"
              name="category"
              :disabled="creating"
            >
              <option v-for="option in CATEGORIES" :key="option.value" :value="option.value">
                {{ option.label }}
              </option>
            </select>
          </FormField>

          <FormField label="重要性（1-5）" input-id="memory-importance" :error="createErrors.importance">
            <input
              id="memory-importance"
              v-model.number="createForm.importance"
              class="input"
              type="number"
              min="1"
              max="5"
              step="1"
              name="importance"
              :disabled="creating"
            />
          </FormField>

          <FormField label="关键词" input-id="memory-keywords" :error="createErrors.keywords" hint="逗号分隔">
            <input
              id="memory-keywords"
              v-model="createForm.keywords"
              class="input"
              type="text"
              name="keywords"
              :disabled="creating"
            />
          </FormField>
        </div>

        <div class="actions">
          <button class="btn btn--primary" type="submit" :disabled="creating">
            <span v-if="creating" class="spinner" aria-hidden="true"></span>
            {{ creating ? '添加中…' : '添加记忆' }}
          </button>
        </div>
      </form>
    </section>

    <div class="danger-row">
      <button class="btn btn--danger btn--sm" type="button" :disabled="clearing" @click="onClear">
        {{ clearing ? '清空中…' : `清空本类记忆（${memoryType === 'recent' ? '近期' : '长期'}）` }}
      </button>
      <span class="muted">只清空当前类型的记忆，不影响另一类，也不删除原始内容。</span>
    </div>

    <AlertNote v-if="actionNotice" kind="success">{{ actionNotice }}</AlertNote>
    <AlertNote v-if="actionError" kind="error">{{ actionError }}</AlertNote>

    <p v-if="initialLoading" class="card card--pad loading-row"><span class="spinner" aria-hidden="true"></span> 加载中…</p>

    <template v-else-if="listError && items.length === 0">
      <AlertNote kind="error">{{ listError }}</AlertNote>
      <button class="btn btn--secondary btn--sm self-start" type="button" @click="reload">重试</button>
    </template>

    <section v-else-if="items.length === 0" class="card card--pad">
      <div class="empty">
        <AppIcon name="database" :size="26" />
        <strong>该分区暂无记忆</strong>
        <p>可以手动添加，或等待 AI 在任务中自行沉淀。</p>
      </div>
    </section>

    <template v-else>
      <section class="card memory-list">
        <article v-for="memory in items" :key="memory.id" class="memory-row">
          <div class="memory-head">
            <span class="badge">{{ categoryLabel(memory.category) }}</span>
            <span class="badge" :class="memory.status === 'active' ? '' : 'badge--muted'">
              {{ statusLabel(memory.status) }}
            </span>
            <span class="muted mono">重要性 {{ memory.importance }}</span>
            <span class="muted mono">来源 {{ memory.source_type || '—' }}</span>
            <span class="memory-time mono">{{ formatDateTime(memory.updated_at) }}</span>
          </div>

          <template v-if="editingId === memory.id">
            <form class="edit-form" novalidate @submit.prevent="onSaveEdit(memory)">
              <FormField label="内容" input-id="memory-edit-content" :error="editErrors.content">
                <textarea
                  id="memory-edit-content"
                  v-model="editForm.content"
                  class="textarea"
                  name="edit_content"
                  :disabled="savingEdit"
                ></textarea>
              </FormField>

              <div class="grid-three">
                <FormField label="分类" input-id="memory-edit-category" :error="editErrors.category">
                  <select
                    id="memory-edit-category"
                    v-model="editForm.category"
                    class="input"
                    name="edit_category"
                    :disabled="savingEdit"
                  >
                    <option v-for="option in CATEGORIES" :key="option.value" :value="option.value">
                      {{ option.label }}
                    </option>
                  </select>
                </FormField>

                <FormField label="重要性（1-5）" input-id="memory-edit-importance" :error="editErrors.importance">
                  <input
                    id="memory-edit-importance"
                    v-model.number="editForm.importance"
                    class="input"
                    type="number"
                    min="1"
                    max="5"
                    step="1"
                    name="edit_importance"
                    :disabled="savingEdit"
                  />
                </FormField>

                <FormField label="状态" input-id="memory-edit-status" :error="editErrors.status">
                  <select
                    id="memory-edit-status"
                    v-model="editForm.status"
                    class="input"
                    name="edit_status"
                    :disabled="savingEdit"
                  >
                    <option value="active">生效中</option>
                    <option value="superseded">已被取代</option>
                    <option value="invalidated">已失效</option>
                  </select>
                </FormField>
              </div>

              <FormField label="关键词" input-id="memory-edit-keywords" :error="editErrors.keywords" hint="逗号分隔">
                <input
                  id="memory-edit-keywords"
                  v-model="editForm.keywords"
                  class="input"
                  type="text"
                  name="edit_keywords"
                  :disabled="savingEdit"
                />
              </FormField>

              <div class="actions">
                <button class="btn btn--primary btn--sm" type="submit" :disabled="savingEdit">
                  <span v-if="savingEdit" class="spinner" aria-hidden="true"></span>
                  {{ savingEdit ? '保存中…' : '保存' }}
                </button>
                <button class="btn btn--secondary btn--sm" type="button" :disabled="savingEdit" @click="cancelEdit">
                  取消
                </button>
              </div>
            </form>
          </template>

          <template v-else>
            <p class="memory-content">{{ memory.content }}</p>
            <div class="memory-foot">
              <span v-for="keyword in memory.keywords" :key="keyword" class="badge badge--muted">{{ keyword }}</span>
              <span v-if="memory.keywords.length === 0" class="muted">无关键词</span>
              <span v-if="memory.last_used_at" class="muted mono last-used">
                最近使用 {{ formatDateTime(memory.last_used_at) }}
              </span>
              <div class="memory-actions">
                <button class="btn btn--secondary btn--sm" type="button" @click="startEdit(memory)">编辑</button>
                <button
                  class="btn btn--danger btn--sm"
                  type="button"
                  :disabled="busyId === memory.id"
                  @click="onDelete(memory)"
                >
                  {{ busyId === memory.id ? '删除中…' : '删除' }}
                </button>
              </div>
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
          <p v-else class="muted">已加载全部 {{ items.length }} 条记忆</p>
        </div>
      </section>

      <AlertNote v-if="listError" kind="error">{{ listError }}</AlertNote>
    </template>
  </div>
</template>

<style scoped>
.head-row {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  flex-wrap: wrap;
}
.head-row .seg {
  margin-left: auto;
}
.search-form {
  display: flex;
  gap: 8px;
  margin-top: 14px;
}
.search-box {
  position: relative;
  flex: 1;
  min-width: 0;
}
.search-icon {
  position: absolute;
  top: 50%;
  left: 11px;
  transform: translateY(-50%);
  color: var(--ink-3);
  pointer-events: none;
}
.search-input {
  padding-left: 36px;
}
.filter-row {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 12px;
}
.grid-three {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 0 14px;
}
.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.self-start {
  align-self: flex-start;
}
.danger-row {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.memory-list {
  overflow: hidden;
}
.memory-row {
  padding: 14px 16px;
  border-bottom: 1px solid var(--border);
}
.memory-head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  font-size: 12.5px;
}
.memory-time {
  margin-left: auto;
  color: var(--ink-3);
  white-space: nowrap;
}
.memory-content {
  margin: 8px 0 0;
  font-size: 14px;
  line-height: 1.8;
  white-space: pre-wrap;
  word-break: break-word;
}
.memory-foot {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  margin-top: 10px;
}
.last-used {
  font-size: 12px;
}
.memory-actions {
  display: flex;
  gap: 6px;
  margin-left: auto;
}
.edit-form {
  margin-top: 12px;
  max-width: 620px;
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
