<script setup lang="ts">
// 从网盘选择已有文件加入文章：多选、按目录/类型/文件名浏览。
// 同一文件可被多篇文章复用，不复制文件、不重复计算配额；公开文件加入非公开文章会被后端拒绝。
import { computed, ref, watch } from 'vue'
import AppIcon from './AppIcon.vue'
import { errorMessage } from '../api/client'
import { listDriveFiles, listDriveFolders, driveFilePreviewURL } from '../api/drive'
import type { DriveFile, DriveFolder } from '../api/types'
import { ui } from '../styles/ui'
import { formatBytes } from '../utils/format'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{
  (event: 'close'): void
  (event: 'pick', files: DriveFile[]): void
}>()

const folders = ref<DriveFolder[]>([])
const files = ref<DriveFile[]>([])
const folderId = ref<'all' | 'root' | string>('all')
const kind = ref<'' | 'image' | 'video' | 'file'>('')
const keyword = ref('')
const loading = ref(false)
const loadingMore = ref(false)
const hasMore = ref(false)
const cursor = ref<string | null>(null)
const error = ref('')
const selected = ref<Map<string, DriveFile>>(new Map())
const previewFailed = ref<Record<string, boolean>>({})

const selectedCount = computed(() => selected.value.size)

function isSelected(file: DriveFile): boolean {
  return selected.value.has(file.id)
}

function toggle(file: DriveFile): void {
  const next = new Map(selected.value)
  if (next.has(file.id)) next.delete(file.id)
  else next.set(file.id, file)
  selected.value = next
}

async function loadFolders(): Promise<void> {
  try {
    const result = await listDriveFolders()
    folders.value = result.items
  } catch (err) {
    error.value = errorMessage(err)
  }
}

async function load(reset: boolean): Promise<void> {
  if (reset) {
    loading.value = true
    cursor.value = null
  } else {
    if (loading.value || loadingMore.value || !hasMore.value) return
    loadingMore.value = true
  }
  error.value = ''
  try {
    const page = await listDriveFiles({
      cursor: reset ? undefined : cursor.value ?? undefined,
      limit: 30,
      folder_id: folderId.value === 'all' ? undefined : folderId.value,
      kind: kind.value || undefined,
      q: keyword.value.trim() || undefined,
    })
    files.value = reset ? page.items : [...files.value, ...page.items]
    cursor.value = page.next_cursor
    hasMore.value = page.has_more
  } catch (err) {
    error.value = errorMessage(err)
  } finally {
    loading.value = false
    loadingMore.value = false
  }
}

watch(
  () => props.open,
  (open) => {
    if (!open) return
    selected.value = new Map()
    previewFailed.value = {}
    folderId.value = 'all'
    kind.value = ''
    keyword.value = ''
    files.value = []
    hasMore.value = false
    void loadFolders()
    void load(true)
  },
  { immediate: true },
)

function chooseFolder(value: 'all' | 'root' | string): void {
  if (folderId.value === value) return
  folderId.value = value
  void load(true)
}

function submitSearch(): void {
  void load(true)
}

function resetFilter(): void {
  kind.value = ''
  keyword.value = ''
  void load(true)
}

function confirm(): void {
  if (selected.value.size === 0) return
  emit('pick', Array.from(selected.value.values()))
  emit('close')
}
</script>

<template>
  <Teleport to="body">
    <div
      v-if="props.open"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/45 px-4 py-6"
      role="dialog"
      aria-modal="true"
      aria-label="从网盘选择文件"
      @click.self="emit('close')"
    >
      <div :class="[ui.card, 'flex max-h-[82vh] w-full max-w-[760px] flex-col overflow-hidden']">
        <header class="flex items-center gap-3 border-b border-solid border-border px-[18px] py-3">
          <AppIcon name="folder" :size="19" />
          <h2 class="m-0 text-[15.5px] font-extrabold">从网盘选择</h2>
          <span :class="[ui.muted, 'ml-auto hidden sm:inline']">可多选，文件不会被复制</span>
          <button
            type="button"
            class="flex size-[30px] flex-none items-center justify-center rounded-[7px] text-ink-3 hover:bg-hover hover:text-ink"
            aria-label="关闭"
            @click="emit('close')"
          >
            <AppIcon name="x" :size="17" />
          </button>
        </header>

        <div class="flex flex-wrap gap-2 border-b border-solid border-border px-[18px] py-2.5">
          <button
            type="button"
            :class="[ui.segmentButton, folderId === 'all' ? ui.segmentButtonActive : '']"
            @click="chooseFolder('all')"
          >
            全部
          </button>
          <button
            type="button"
            :class="[ui.segmentButton, folderId === 'root' ? ui.segmentButtonActive : '']"
            @click="chooseFolder('root')"
          >
            根目录
          </button>
          <button
            v-for="folder in folders"
            :key="folder.id"
            type="button"
            :class="[ui.segmentButton, folderId === folder.id ? ui.segmentButtonActive : '']"
            @click="chooseFolder(folder.id)"
          >
            {{ folder.name }}
            <span class="text-ink-3">{{ folder.file_count }}</span>
          </button>
        </div>

        <form class="flex flex-wrap items-center gap-2 px-[18px] py-2.5" @submit.prevent="submitSearch">
          <input
            v-model="keyword"
            :class="[ui.input, 'w-auto! min-w-[160px] flex-1']"
            type="search"
            placeholder="按文件名搜索"
            aria-label="按文件名搜索网盘文件"
          />
          <select v-model="kind" :class="[ui.input, 'w-auto!']" aria-label="按类型筛选">
            <option value="">全部类型</option>
            <option value="image">图片</option>
            <option value="video">视频</option>
            <option value="file">文件</option>
          </select>
          <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]" type="submit">搜索</button>
          <button
            v-if="kind || keyword"
            :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]"
            type="button"
            @click="resetFilter"
          >
            重置
          </button>
        </form>

        <div class="min-h-0 flex-1 overflow-y-auto px-[18px] pb-2">
          <p v-if="error" :class="[ui.muted, 'py-4 text-red!']">{{ error }}</p>
          <p v-else-if="loading" :class="ui.loadingRow">
            <span :class="ui.spinner" aria-hidden="true"></span> 加载中…
          </p>
          <p v-else-if="files.length === 0" :class="ui.empty">这个目录还没有文件。</p>
          <ul v-else class="m-0 grid list-none gap-1.5 p-0 py-1.5">
            <li v-for="file in files" :key="file.id">
              <label
                :class="[
                  'flex cursor-pointer items-center gap-2.5 rounded-[9px] border border-solid px-2.5 py-2 text-[13.5px] transition-[background,border-color] duration-150 ease-[ease] hover:bg-hover',
                  isSelected(file) ? 'border-primary bg-primary-soft' : 'border-border',
                ]"
              >
                <input
                  type="checkbox"
                  class="size-4 flex-none accent-[var(--primary)]"
                  :checked="isSelected(file)"
                  @change="toggle(file)"
                />
                <img
                  v-if="file.kind === 'image' && !previewFailed[file.id]"
                  :src="driveFilePreviewURL(file)"
                  :alt="file.original_filename"
                  class="size-[40px] flex-none rounded-[6px] object-cover"
                  @error="previewFailed[file.id] = true"
                />
                <AppIcon
                  v-else
                  :name="file.kind === 'video' ? 'image' : 'file-text'"
                  :size="20"
                  class="flex-none text-ink-3"
                />
                <span class="min-w-0 flex-1">
                  <span class="block overflow-hidden text-ellipsis whitespace-nowrap font-semibold">{{ file.original_filename }}</span>
                  <span class="block text-[12px] text-ink-3">
                    {{ formatBytes(file.size_bytes) }}
                    <template v-if="file.folder_name"> · {{ file.folder_name }}</template>
                    <template v-else> · 根目录</template>
                  </span>
                </span>
                <span
                  :class="[ui.badge, file.visibility === 'public' ? '' : ui.badgeMuted]"
                >{{ file.visibility === 'public' ? '公开' : '私密' }}</span>
              </label>
            </li>
          </ul>
          <button
            v-if="hasMore"
            :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'my-2']"
            type="button"
            :disabled="loadingMore"
            @click="load(false)"
          >
            {{ loadingMore ? '加载中…' : '加载更多' }}
          </button>
        </div>

        <footer class="flex items-center gap-2 border-t border-solid border-border px-[18px] py-3">
          <span :class="ui.muted">已选择 {{ selectedCount }} 个文件</span>
          <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'ml-auto']" type="button" @click="emit('close')">
            取消
          </button>
          <button
            :class="[ui.button, ui.buttonPrimary, ui.buttonSmall]"
            type="button"
            :disabled="selectedCount === 0"
            @click="confirm"
          >
            加入文章
          </button>
        </footer>
      </div>
    </div>
  </Teleport>
</template>
