<script setup lang="ts">
// A-07 模型接口预设：OpenAI 兼容接口的增删改查与连接测试
// 安全约定：任何位置都不显示完整 API Key，接口只返回掩码。
import { computed, reactive, ref } from 'vue'
import { createProvider, deleteProvider, listProviders, testProvider, updateProvider } from '../../api/aiAdmin'
import { errorMessage, fieldErrors } from '../../api/client'
import type { ModelProvider, ProviderTestResult } from '../../api/types'
import AlertNote from '../../components/AlertNote.vue'
import AppIcon from '../../components/AppIcon.vue'
import FormField from '../../components/FormField.vue'
import { useCursorList } from '../../composables/useCursorList'
import { formatDateTime } from '../../utils/datetime'

type ProviderFilters = {
  q?: string
  status?: 'enabled' | 'disabled'
}

interface TestState {
  testing: boolean
  result: ProviderTestResult | null
  error: string
}

const filters = reactive({
  q: '',
  status: '' as '' | 'enabled' | 'disabled',
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
} = useCursorList<ModelProvider, ProviderFilters>({
  fetch: (params) => listProviders(params),
  params: () => ({
    q: filters.q.trim() || undefined,
    status: filters.status || undefined,
  }),
})

const initialLoading = computed(() => !loaded.value && !listError.value)

function onSearch(): void {
  void reload()
}

function setStatus(value: '' | 'enabled' | 'disabled'): void {
  if (filters.status === value) return
  filters.status = value
  void reload()
}

// ---------- 新建预设 ----------
const showCreate = ref(false)
const createForm = reactive({ name: '', base_url: '', api_key: '', default_model: '' })
const createErrors = ref<Record<string, string>>({})
const creating = ref(false)
const createError = ref('')
const createNotice = ref('')

function resetCreateForm(): void {
  createForm.name = ''
  createForm.base_url = ''
  createForm.api_key = ''
  createForm.default_model = ''
  createErrors.value = {}
}

async function onCreate(): Promise<void> {
  const next: Record<string, string> = {}
  if (!createForm.name.trim()) next.name = '请输入预设名称'
  if (!createForm.base_url.trim()) {
    next.base_url = '请输入 Base URL'
  } else if (!/^https?:\/\//i.test(createForm.base_url.trim())) {
    next.base_url = 'Base URL 需以 http:// 或 https:// 开头'
  }
  if (!createForm.api_key.trim()) next.api_key = '请输入 API Key'
  if (!createForm.default_model.trim()) next.default_model = '请输入默认模型'
  createErrors.value = next
  if (Object.keys(next).length > 0) return

  creating.value = true
  createError.value = ''
  createNotice.value = ''
  try {
    await createProvider({
      name: createForm.name.trim(),
      base_url: createForm.base_url.trim(),
      api_key: createForm.api_key.trim(),
      default_model: createForm.default_model.trim(),
    })
    resetCreateForm()
    showCreate.value = false
    createNotice.value = '预设已创建。密钥只提交给后端保存，页面不会回显。'
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
const editForm = reactive({ name: '', base_url: '', default_model: '', api_key: '', is_enabled: true })
const editErrors = ref<Record<string, string>>({})
const savingEdit = ref(false)

function startEdit(provider: ModelProvider): void {
  editingId.value = provider.id
  editForm.name = provider.name
  editForm.base_url = provider.base_url
  editForm.default_model = provider.default_model
  editForm.api_key = ''
  editForm.is_enabled = provider.is_enabled
  editErrors.value = {}
  actionError.value = ''
  actionNotice.value = ''
}

function cancelEdit(): void {
  editingId.value = null
  editErrors.value = {}
}

async function onSaveEdit(provider: ModelProvider): Promise<void> {
  const next: Record<string, string> = {}
  if (!editForm.name.trim()) next.name = '请输入预设名称'
  if (!editForm.base_url.trim()) {
    next.base_url = '请输入 Base URL'
  } else if (!/^https?:\/\//i.test(editForm.base_url.trim())) {
    next.base_url = 'Base URL 需以 http:// 或 https:// 开头'
  }
  if (!editForm.default_model.trim()) next.default_model = '请输入默认模型'
  editErrors.value = next
  if (Object.keys(next).length > 0) return

  savingEdit.value = true
  actionError.value = ''
  actionNotice.value = ''
  try {
    await updateProvider(provider.id, {
      name: editForm.name.trim(),
      base_url: editForm.base_url.trim(),
      default_model: editForm.default_model.trim(),
      is_enabled: editForm.is_enabled,
      // 留空表示不变更既有密钥
      ...(editForm.api_key.trim() ? { api_key: editForm.api_key.trim() } : {}),
    })
    editingId.value = null
    actionNotice.value = '预设已更新。'
    await reload()
  } catch (error) {
    const fields = fieldErrors(error)
    if (Object.keys(fields).length > 0) editErrors.value = fields
    else actionError.value = errorMessage(error)
  } finally {
    savingEdit.value = false
  }
}

// ---------- 连接测试 / 删除 ----------
const testStates = ref<Record<string, TestState>>({})
const busyId = ref<string | null>(null)
const actionError = ref('')
const actionNotice = ref('')

function testStateOf(id: string): TestState | undefined {
  return testStates.value[id]
}

async function onTest(provider: ModelProvider): Promise<void> {
  testStates.value[provider.id] = { testing: true, result: null, error: '' }
  try {
    const result = await testProvider(provider.id)
    testStates.value[provider.id] = { testing: false, result, error: '' }
  } catch (error) {
    testStates.value[provider.id] = { testing: false, result: null, error: errorMessage(error) }
  }
}

async function onDelete(provider: ModelProvider): Promise<void> {
  if (
    !window.confirm(
      `确认删除预设「${provider.name}」？删除为逻辑删除：已有 AI 引用该预设时后端会拒绝（需先停用并迁移关联 AI）。`,
    )
  ) {
    return
  }
  busyId.value = provider.id
  actionError.value = ''
  actionNotice.value = ''
  try {
    await deleteProvider(provider.id)
    actionNotice.value = `预设「${provider.name}」已删除。`
    await reload()
  } catch (error) {
    actionError.value = errorMessage(error)
  } finally {
    busyId.value = null
  }
}

function modelPreview(result: ProviderTestResult | null): string {
  if (!result?.models || result.models.length === 0) return ''
  const head = result.models.slice(0, 3).join('、')
  return result.models.length > 3 ? `${head} 等 ${result.models.length} 个模型` : head
}
</script>

<template>
  <div class="stack">
    <section class="card card--pad">
      <div class="head-row">
        <div>
          <h2 class="page-title">模型接口预设</h2>
          <p class="page-desc">维护 OpenAI 兼容接口地址与密钥；密钥仅以掩码展示，不提供查看明文入口。</p>
        </div>
        <button class="btn btn--primary btn--sm" type="button" @click="showCreate = !showCreate">
          <AppIcon :name="showCreate ? 'chevron-down' : 'plus'" :size="16" />
          {{ showCreate ? '收起新建表单' : '新建预设' }}
        </button>
      </div>

      <form v-if="showCreate" class="form" novalidate @submit.prevent="onCreate">
        <AlertNote v-if="createError" kind="error">{{ createError }}</AlertNote>

        <FormField label="预设名称" input-id="provider-name" :error="createErrors.name" hint="例如「内部网关」「生产 OpenAI」">
          <input
            id="provider-name"
            v-model="createForm.name"
            class="input"
            type="text"
            name="name"
            placeholder="预设名称"
            :disabled="creating"
          />
        </FormField>

        <FormField
          label="Base URL"
          input-id="provider-base-url"
          :error="createErrors.base_url"
          hint="不含 /v1 之外的自定义路径；需以 http:// 或 https:// 开头"
        >
          <input
            id="provider-base-url"
            v-model="createForm.base_url"
            class="input"
            type="url"
            name="base_url"
            placeholder="https://api.example.com/v1"
            :disabled="creating"
          />
        </FormField>

        <FormField
          label="API Key"
          input-id="provider-api-key"
          :error="createErrors.api_key"
          hint="仅保存在后端；创建后页面只显示掩码，不会回显明文"
        >
          <input
            id="provider-api-key"
            v-model="createForm.api_key"
            class="input"
            type="password"
            name="api_key"
            autocomplete="off"
            placeholder="sk-..."
            :disabled="creating"
          />
        </FormField>

        <FormField label="默认模型" input-id="provider-default-model" :error="createErrors.default_model">
          <input
            id="provider-default-model"
            v-model="createForm.default_model"
            class="input"
            type="text"
            name="default_model"
            placeholder="gpt-4o-mini"
            :disabled="creating"
          />
        </FormField>

        <div class="actions">
          <button class="btn btn--primary" type="submit" :disabled="creating">
            <span v-if="creating" class="spinner" aria-hidden="true"></span>
            {{ creating ? '创建中…' : '创建预设' }}
          </button>
          <button class="btn btn--secondary" type="button" :disabled="creating" @click="resetCreateForm">
            清空
          </button>
        </div>
      </form>
    </section>

    <section class="card card--pad">
      <form class="search-form" @submit.prevent="onSearch">
        <div class="search-box">
          <AppIcon name="search" :size="17" class="search-icon" />
          <input
            v-model="filters.q"
            class="input search-input"
            type="search"
            name="q"
            placeholder="搜索预设名称"
            aria-label="搜索预设名称"
          />
        </div>
        <button class="btn btn--primary" type="submit" :disabled="loading">查询</button>
      </form>

      <div class="filter-row">
        <div class="seg" role="group" aria-label="启用状态筛选">
          <button type="button" :class="{ 'is-active': filters.status === '' }" @click="setStatus('')">全部</button>
          <button type="button" :class="{ 'is-active': filters.status === 'enabled' }" @click="setStatus('enabled')">
            已启用
          </button>
          <button type="button" :class="{ 'is-active': filters.status === 'disabled' }" @click="setStatus('disabled')">
            已停用
          </button>
        </div>
      </div>
    </section>

    <AlertNote v-if="createNotice" kind="success">{{ createNotice }}</AlertNote>
    <AlertNote v-if="actionNotice" kind="success">{{ actionNotice }}</AlertNote>
    <AlertNote v-if="actionError" kind="error">{{ actionError }}</AlertNote>

    <p v-if="initialLoading" class="card card--pad loading-row"><span class="spinner" aria-hidden="true"></span> 加载中…</p>

    <template v-else-if="listError && items.length === 0">
      <AlertNote kind="error">{{ listError }}</AlertNote>
      <button class="btn btn--secondary btn--sm self-start" type="button" @click="reload">重试</button>
    </template>

    <section v-else-if="items.length === 0" class="card card--pad">
      <div class="empty">
        <AppIcon name="key" :size="26" />
        <strong>还没有模型接口预设</strong>
        <p>先创建一个 OpenAI 兼容预设，再为 AI 配置模型。</p>
      </div>
    </section>

    <template v-else>
      <section class="card table-card">
        <div class="table-scroll">
          <table class="table">
            <thead>
              <tr>
                <th>名称</th>
                <th>Base URL</th>
                <th>默认模型</th>
                <th>密钥</th>
                <th>状态</th>
                <th>关联 AI</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              <template v-for="provider in items" :key="provider.id">
                <tr>
                  <td class="col-name">
                    <span class="name-text">{{ provider.name }}</span>
                    <span class="muted mono block-id">{{ provider.id }}</span>
                  </td>
                  <td class="col-url mono">{{ provider.base_url }}</td>
                  <td class="mono">{{ provider.default_model }}</td>
                  <td class="mono muted">{{ provider.api_key_masked || '未设置' }}</td>
                  <td>
                    <span class="badge" :class="provider.is_enabled ? '' : 'badge--muted'">
                      {{ provider.is_enabled ? '已启用' : '已停用' }}
                    </span>
                  </td>
                  <td class="mono">{{ provider.ai_count }}</td>
                  <td class="col-actions">
                    <div class="actions">
                      <button
                        class="btn btn--secondary btn--sm"
                        type="button"
                        @click="editingId === provider.id ? cancelEdit() : startEdit(provider)"
                      >
                        {{ editingId === provider.id ? '收起编辑' : '编辑' }}
                      </button>
                      <button
                        class="btn btn--secondary btn--sm"
                        type="button"
                        :disabled="testStateOf(provider.id)?.testing === true"
                        @click="onTest(provider)"
                      >
                        {{ testStateOf(provider.id)?.testing ? '测试中…' : '测试连接' }}
                      </button>
                      <button
                        class="btn btn--danger btn--sm"
                        type="button"
                        :disabled="busyId === provider.id"
                        @click="onDelete(provider)"
                      >
                        {{ busyId === provider.id ? '删除中…' : '删除' }}
                      </button>
                    </div>

                    <p v-if="testStateOf(provider.id)?.testing" class="muted test-line">
                      <span class="spinner" aria-hidden="true"></span> 正在请求模型列表…
                    </p>
                    <p
                      v-else-if="testStateOf(provider.id)?.result"
                      class="muted test-line"
                      :class="{ 'test-ok': testStateOf(provider.id)?.result?.ok }"
                    >
                      <template v-if="testStateOf(provider.id)?.result?.ok">
                        连接正常 · {{ testStateOf(provider.id)?.result?.latency_ms }} ms<template
                          v-if="modelPreview(testStateOf(provider.id)?.result ?? null)"
                        >
                          · {{ modelPreview(testStateOf(provider.id)?.result ?? null) }}</template
                        >
                      </template>
                      <template v-else>
                        连接失败：{{ testStateOf(provider.id)?.result?.error || '未返回错误详情' }}
                      </template>
                    </p>
                    <p v-else-if="testStateOf(provider.id)?.error" class="muted test-line test-fail">
                      {{ testStateOf(provider.id)?.error }}
                    </p>

                    <p v-if="provider.ai_count > 0" class="muted test-line">
                      已有 {{ provider.ai_count }} 个 AI 关联，删除会被后端拒绝。
                    </p>
                  </td>
                </tr>

                <tr v-if="editingId === provider.id" class="edit-row">
                  <td colspan="7">
                    <form class="edit-form" novalidate @submit.prevent="onSaveEdit(provider)">
                      <div class="edit-grid">
                        <FormField label="名称" input-id="edit-name" :error="editErrors.name">
                          <input
                            id="edit-name"
                            v-model="editForm.name"
                            class="input"
                            type="text"
                            name="edit_name"
                            :disabled="savingEdit"
                          />
                        </FormField>

                        <FormField label="Base URL" input-id="edit-base-url" :error="editErrors.base_url">
                          <input
                            id="edit-base-url"
                            v-model="editForm.base_url"
                            class="input"
                            type="url"
                            name="edit_base_url"
                            :disabled="savingEdit"
                          />
                        </FormField>

                        <FormField label="默认模型" input-id="edit-default-model" :error="editErrors.default_model">
                          <input
                            id="edit-default-model"
                            v-model="editForm.default_model"
                            class="input"
                            type="text"
                            name="edit_default_model"
                            :disabled="savingEdit"
                          />
                        </FormField>

                        <FormField
                          label="API Key"
                          input-id="edit-api-key"
                          :error="editErrors.api_key"
                          hint="留空表示不变更既有密钥"
                        >
                          <input
                            id="edit-api-key"
                            v-model="editForm.api_key"
                            class="input"
                            type="password"
                            name="edit_api_key"
                            autocomplete="off"
                            placeholder="留空不修改"
                            :disabled="savingEdit"
                          />
                        </FormField>
                      </div>

                      <label class="switch">
                        <input v-model="editForm.is_enabled" type="checkbox" :disabled="savingEdit" />
                        <span>{{ editForm.is_enabled ? '启用该预设' : '停用该预设' }}</span>
                      </label>

                      <div class="actions actions-gap">
                        <button class="btn btn--primary btn--sm" type="submit" :disabled="savingEdit">
                          <span v-if="savingEdit" class="spinner" aria-hidden="true"></span>
                          {{ savingEdit ? '保存中…' : '保存修改' }}
                        </button>
                        <button class="btn btn--secondary btn--sm" type="button" :disabled="savingEdit" @click="cancelEdit">
                          取消
                        </button>
                      </div>

                      <p class="muted created-at">创建于 {{ formatDateTime(provider.created_at) }}</p>
                    </form>
                  </td>
                </tr>
              </template>
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
          <p v-else class="muted">已加载全部 {{ items.length }} 个预设</p>
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
}
.head-row .btn {
  margin-left: auto;
}
.search-form {
  display: flex;
  gap: 8px;
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
.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.actions-gap {
  margin-top: 14px;
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
  font-size: 13.5px;
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
  vertical-align: top;
}
.table tbody tr:last-child td {
  border-bottom: none;
}
.col-name {
  min-width: 140px;
}
.name-text {
  font-weight: 700;
}
.block-id {
  display: block;
  font-size: 11.5px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 150px;
}
.col-url {
  max-width: 260px;
  word-break: break-all;
  font-size: 12.5px;
}
.col-actions {
  min-width: 220px;
}
.test-line {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 8px 0 0;
  font-size: 12.5px;
  line-height: 1.6;
}
.test-ok {
  color: var(--green);
}
.test-fail {
  color: var(--red);
}
.edit-row td {
  background: var(--surface-subtle);
}
.edit-form {
  max-width: 620px;
}
.edit-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
  gap: 0 14px;
}
.created-at {
  margin: 12px 0 0;
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
