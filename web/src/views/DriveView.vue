<script setup lang="ts">
// 我的网盘：一级文件夹、文件公开级别、移动、删除与容量。
// 文件最终权限在文件上；文件夹“统一设置”是批量操作，移动目录不改变公开级别。
import { computed, onMounted, ref } from 'vue'
import { errorMessage } from '../api/client'
import {
  createDriveFolder,
  deleteDriveFile,
  deleteDriveFolder,
  driveFilePreviewURL,
  listDriveFiles,
  listDriveFolders,
  moveDriveFile,
  renameDriveFolder,
  setDriveFileVisibility,
  setDriveFolderVisibility,
} from '../api/drive'
import { uploadAttachment } from '../api/media'
import type { DriveFile, DriveFolder, DriveVisibility } from '../api/types'
import AlertNote from '../components/AlertNote.vue'
import AppIcon from '../components/AppIcon.vue'
import EmptyState from '../components/EmptyState.vue'
import { ui } from '../styles/ui'
import { formatBytes } from '../utils/format'

const folders = ref<DriveFolder[]>([])
const files = ref<DriveFile[]>([])
const currentFolder = ref<'all' | 'root' | string>('all')
const kind = ref<'' | 'image' | 'video' | 'file'>('')
const keyword = ref('')
const loading = ref(false)
const loadingMore = ref(false)
const hasMore = ref(false)
const cursor = ref<string | null>(null)
const error = ref('')
const actionError = ref('')
const actionNotice = ref('')
const usedBytes = ref(0)
const quotaBytes = ref(0)

const uploading = ref(false)
const uploadPercent = ref(0)
const uploadName = ref('')
const fileInput = ref<HTMLInputElement | null>(null)
const previewFailed = ref<Record<string, boolean>>({})

const folderFormVisible = ref(false)
const folderFormName = ref('')
const folderBusy = ref(false)
const renaming = ref(false)
const renameName = ref('')
const renameBusy = ref(false)

const currentFolderObj = computed(() =>
  currentFolder.value === 'all' || currentFolder.value === 'root'
    ? null
    : folders.value.find((folder) => folder.id === currentFolder.value) ?? null,
)

const uploadTargetName = computed(() => {
  if (currentFolder.value === 'all' || currentFolder.value === 'root') return '根目录'
  return currentFolderObj.value?.name ?? '根目录'
})

const quotaPercent = computed(() => {
  if (quotaBytes.value <= 0) return 0
  return Math.min(100, Math.round((usedBytes.value / quotaBytes.value) * 100))
})

function visibilityLabel(value: DriveVisibility | 'mixed'): string {
  if (value === 'public') return '全部公开'
  if (value === 'mixed') return '部分公开'
  return '全部私密'
}

async function refreshFolders(): Promise<void> {
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
      limit: 24,
      folder_id: currentFolder.value === 'all' ? undefined : currentFolder.value,
      kind: kind.value || undefined,
      q: keyword.value.trim() || undefined,
    })
    files.value = reset ? page.items : [...files.value, ...page.items]
    cursor.value = page.next_cursor
    hasMore.value = page.has_more
    if (reset) {
      usedBytes.value = page.used_bytes ?? 0
      quotaBytes.value = page.quota_bytes ?? 0
    }
  } catch (err) {
    error.value = errorMessage(err)
  } finally {
    loading.value = false
    loadingMore.value = false
  }
}

function chooseFolder(value: 'all' | 'root' | string): void {
  if (currentFolder.value === value) return
  currentFolder.value = value
  renaming.value = false
  actionError.value = ''
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

function pickUpload(): void {
  if (uploading.value) return
  fileInput.value?.click()
}

async function onFilesSelected(event: Event): Promise<void> {
  const input = event.target as HTMLInputElement
  const selected = Array.from(input.files ?? [])
  input.value = ''
  if (selected.length === 0 || uploading.value) return
  const folderId = currentFolder.value === 'all' || currentFolder.value === 'root' ? null : currentFolder.value
  uploading.value = true
  actionError.value = ''
  actionNotice.value = ''
  try {
    for (const file of selected) {
      uploadName.value = file.name
      uploadPercent.value = 0
      await uploadAttachment(file, 'post', (percent) => {
        uploadPercent.value = percent
      }, { source: 'drive', folderId })
    }
    actionNotice.value = `已上传 ${selected.length} 个文件到${uploadTargetName.value}。`
    await refreshFolders()
    await load(true)
  } catch (err) {
    actionError.value = errorMessage(err)
  } finally {
    uploading.value = false
    uploadName.value = ''
    uploadPercent.value = 0
  }
}

async function submitCreateFolder(): Promise<void> {
  const name = folderFormName.value.trim()
  if (!name || folderBusy.value) return
  folderBusy.value = true
  actionError.value = ''
  try {
    const result = await createDriveFolder(name)
    folderFormName.value = ''
    folderFormVisible.value = false
    currentFolder.value = result.folder.id
    await refreshFolders()
    await load(true)
  } catch (err) {
    actionError.value = errorMessage(err)
  } finally {
    folderBusy.value = false
  }
}

function startRename(): void {
  if (!currentFolderObj.value) return
  renameName.value = currentFolderObj.value.name
  renaming.value = true
  actionError.value = ''
}

async function submitRename(): Promise<void> {
  const folder = currentFolderObj.value
  const name = renameName.value.trim()
  if (!folder || !name || renameBusy.value) return
  renameBusy.value = true
  actionError.value = ''
  try {
    await renameDriveFolder(folder.id, name)
    renaming.value = false
    await refreshFolders()
    await load(true)
  } catch (err) {
    actionError.value = errorMessage(err)
  } finally {
    renameBusy.value = false
  }
}

async function changeFolderVisibility(visibility: DriveVisibility): Promise<void> {
  const folder = currentFolderObj.value
  if (!folder || folderBusy.value) return
  if (visibility === 'public' && folder.file_count > 0) {
    const ok = window.confirm('把文件夹内全部文件设为公开？若任一文件被非公开或已删除文章引用，整批会失败。')
    if (!ok) return
  }
  folderBusy.value = true
  actionError.value = ''
  actionNotice.value = ''
  try {
    await setDriveFolderVisibility(folder.id, visibility)
    actionNotice.value = `已把“${folder.name}”内文件统一设为${visibility === 'public' ? '公开' : '私密'}。`
    await refreshFolders()
    await load(true)
  } catch (err) {
    actionError.value = errorMessage(err)
  } finally {
    folderBusy.value = false
  }
}

async function removeFolder(): Promise<void> {
  const folder = currentFolderObj.value
  if (!folder || folderBusy.value) return
  const ok = window.confirm(`删除文件夹“${folder.name}”？仅空文件夹可以删除，文件需先移出。`)
  if (!ok) return
  folderBusy.value = true
  actionError.value = ''
  try {
    await deleteDriveFolder(folder.id)
    currentFolder.value = 'all'
    await refreshFolders()
    await load(true)
  } catch (err) {
    actionError.value = errorMessage(err)
  } finally {
    folderBusy.value = false
  }
}

async function toggleFileVisibility(file: DriveFile): Promise<void> {
  const target: DriveVisibility = file.visibility === 'public' ? 'private' : 'public'
  actionError.value = ''
  try {
    await setDriveFileVisibility(file.id, target)
    file.visibility = target
    await refreshFolders()
  } catch (err) {
    actionError.value = errorMessage(err)
  }
}

async function changeFileFolder(file: DriveFile, event: Event): Promise<void> {
  const raw = (event.target as HTMLSelectElement).value
  const folderId = raw === '' ? null : raw
  if (file.folder_id === folderId) return
  actionError.value = ''
  try {
    await moveDriveFile(file.id, folderId)
    file.folder_id = folderId
    file.folder_name = folderId ? folders.value.find((folder) => folder.id === folderId)?.name ?? '' : ''
    await refreshFolders()
  } catch (err) {
    actionError.value = errorMessage(err)
    await load(true)
  }
}

async function removeFile(file: DriveFile): Promise<void> {
  const ok = window.confirm(`从网盘删除“${file.original_filename}”？文章中的引用仍可用，删除不会释放存储配额。`)
  if (!ok) return
  actionError.value = ''
  try {
    await deleteDriveFile(file.id)
    files.value = files.value.filter((item) => item.id !== file.id)
    await refreshFolders()
  } catch (err) {
    actionError.value = errorMessage(err)
  }
}

onMounted(() => {
  void refreshFolders()
  void load(true)
})
</script>

<template>
  <div :class="[ui.constrain]">
    <div :class="[ui.stack]">
      <section :class="[ui.card, ui.cardPad]">
        <div class="flex flex-wrap items-center gap-3">
          <div class="min-w-0">
            <h1 :class="[ui.pageHead, ui.pageTitle]">我的网盘</h1>
            <p :class="[ui.pageDesc]">文章里上传的附件会自动保存到“文章附件”；文件默认私密，可随时设为公开。</p>
          </div>
          <div class="ml-auto flex flex-wrap gap-2">
            <input ref="fileInput" type="file" multiple hidden @change="onFilesSelected" />
            <button :class="[ui.button, ui.buttonPrimary, ui.buttonSmall]" type="button" :disabled="uploading" @click="pickUpload">
              <AppIcon name="upload" :size="15" />
              {{ uploading ? '上传中…' : '上传文件' }}
            </button>
            <button
              :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]"
              type="button"
              :disabled="folderBusy"
              @click="folderFormVisible = !folderFormVisible"
            >
              <AppIcon name="plus" :size="15" /> 新建文件夹
            </button>
          </div>
        </div>

        <div class="mt-3 flex flex-wrap items-center gap-2 text-[12.5px] text-ink-3">
          <span>上传目标：<strong class="text-ink">{{ uploadTargetName }}</strong></span>
          <span v-if="quotaBytes > 0">
            已用 {{ formatBytes(usedBytes) }} / {{ formatBytes(quotaBytes) }}（{{ quotaPercent }}%）
          </span>
          <span v-if="uploading" class="inline-flex items-center gap-1.5 text-ink-2">
            <span :class="ui.spinner" aria-hidden="true"></span>
            {{ uploadName }} {{ uploadPercent }}%
          </span>
        </div>
        <div v-if="quotaBytes > 0" class="mt-2 h-[6px] w-full overflow-hidden rounded-[999px] bg-soft">
          <div class="h-full rounded-[999px] bg-primary transition-[width] duration-300 ease-[ease]" :style="{ width: `${quotaPercent}%` }"></div>
        </div>
        <p v-if="quotaBytes > 0" class="m-0 mt-1.5 text-[12px] text-ink-3">删除文件不会释放配额：数据库记录与物理文件按项目约定永久保留。</p>

        <form v-if="folderFormVisible" class="mt-3 flex flex-wrap items-center gap-2" @submit.prevent="submitCreateFolder">
          <input
            v-model="folderFormName"
            :class="[ui.input, 'w-auto! min-w-[180px] flex-1']"
            type="text"
            maxlength="50"
            placeholder="文件夹名称"
            aria-label="新文件夹名称"
            :disabled="folderBusy"
          />
          <button :class="[ui.button, ui.buttonPrimary, ui.buttonSmall]" type="submit" :disabled="folderBusy || !folderFormName.trim()">
            创建
          </button>
          <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]" type="button" @click="folderFormVisible = false">
            取消
          </button>
        </form>
      </section>

      <section :class="[ui.card, ui.cardPad]">
        <div class="flex flex-wrap items-center gap-1.5">
          <button
            type="button"
            :class="[ui.segmentButton, currentFolder === 'all' ? ui.segmentButtonActive : '']"
            @click="chooseFolder('all')"
          >
            全部文件
          </button>
          <button
            type="button"
            :class="[ui.segmentButton, currentFolder === 'root' ? ui.segmentButtonActive : '']"
            @click="chooseFolder('root')"
          >
            根目录
          </button>
          <button
            v-for="folder in folders"
            :key="folder.id"
            type="button"
            :class="[ui.segmentButton, currentFolder === folder.id ? ui.segmentButtonActive : '']"
            @click="chooseFolder(folder.id)"
          >
            <AppIcon name="folder" :size="14" />
            {{ folder.name }}
            <span class="text-ink-3">{{ folder.file_count }}</span>
            <span v-if="folder.system_key" :class="[ui.badge, ui.badgeMuted, 'px-1.5! py-0! text-[10px]!']">系统</span>
          </button>
        </div>

        <div v-if="currentFolderObj" class="mt-3 flex flex-wrap items-center gap-2 rounded-[10px] border border-solid border-border px-3 py-2">
          <template v-if="renaming">
            <form class="flex flex-wrap items-center gap-2" @submit.prevent="submitRename">
              <input
                v-model="renameName"
                :class="[ui.input, 'w-auto! min-w-[180px]']"
                type="text"
                maxlength="50"
                aria-label="文件夹新名称"
                :disabled="renameBusy"
              />
              <button :class="[ui.button, ui.buttonPrimary, ui.buttonSmall]" type="submit" :disabled="renameBusy || !renameName.trim()">
                保存
              </button>
              <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]" type="button" @click="renaming = false">
                取消
              </button>
            </form>
          </template>
          <template v-else>
            <AppIcon name="folder" :size="17" />
            <strong class="text-[14px]">{{ currentFolderObj.name }}</strong>
            <span :class="ui.badge">{{ visibilityLabel(currentFolderObj.visibility_summary) }}</span>
            <span class="text-[12.5px] text-ink-3">{{ currentFolderObj.file_count }} 个文件</span>
            <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'ml-auto']" type="button" :disabled="folderBusy" @click="startRename">
              重命名
            </button>
            <button
              :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]"
              type="button"
              :disabled="folderBusy || currentFolderObj.file_count === 0"
              @click="changeFolderVisibility('public')"
            >
              全部公开
            </button>
            <button
              :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]"
              type="button"
              :disabled="folderBusy || currentFolderObj.file_count === 0"
              @click="changeFolderVisibility('private')"
            >
              全部私密
            </button>
            <button
              v-if="!currentFolderObj.system_key"
              :class="[ui.button, ui.buttonDanger, ui.buttonSmall]"
              type="button"
              :disabled="folderBusy || currentFolderObj.file_count > 0"
              @click="removeFolder"
            >
              删除文件夹
            </button>
          </template>
        </div>
        <p class="m-0 mt-2 text-[12.5px] text-ink-3">
          文件夹批量设置会逐文件检查文章引用；公开文件不能加入非公开文章。移动文件不会改变公开级别。
        </p>

        <form class="mt-3 flex flex-wrap items-center gap-2" @submit.prevent="submitSearch">
          <input
            v-model="keyword"
            :class="[ui.input, 'w-auto! min-w-[160px] flex-1']"
            type="search"
            placeholder="按文件名搜索"
            aria-label="搜索网盘文件"
          />
          <select v-model="kind" :class="[ui.input, 'w-auto!']" aria-label="按类型筛选" @change="load(true)">
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
      </section>

      <AlertNote v-if="actionError" kind="error">{{ actionError }}</AlertNote>
      <AlertNote v-if="actionNotice" kind="success">{{ actionNotice }}</AlertNote>
      <AlertNote v-if="error" kind="error">{{ error }}</AlertNote>

      <p v-if="loading" :class="[ui.card, ui.loadingRow]">
        <span :class="ui.spinner" aria-hidden="true"></span> 加载中…
      </p>
      <EmptyState v-else-if="files.length === 0" icon="folder" title="这里还没有文件">
        上传文件，或在文章编辑器中上传附件；之后可以在这里管理公开级别、移动和删除。
      </EmptyState>

      <section v-else class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        <article
          v-for="file in files"
          :key="file.id"
          :class="[ui.card, 'flex min-w-0 flex-col gap-2 p-3']"
        >
          <a
            :href="file.content_url"
            target="_blank"
            rel="noopener"
            class="flex h-32 items-center justify-center overflow-hidden rounded-[9px] bg-soft"
            :aria-label="`打开 ${file.original_filename}`"
          >
            <img
              v-if="file.kind === 'image' && !previewFailed[file.id]"
              :src="driveFilePreviewURL(file)"
              :alt="file.original_filename"
              class="size-full object-cover"
              @error="previewFailed[file.id] = true"
            />
            <AppIcon
              v-else
              :name="file.kind === 'video' ? 'image' : 'file-text'"
              :size="34"
              class="text-ink-3"
            />
          </a>
          <div class="flex min-w-0 items-start gap-2">
            <span class="min-w-0 flex-1">
              <span class="block overflow-hidden text-ellipsis whitespace-nowrap text-[13.5px] font-bold" :title="file.original_filename">
                {{ file.original_filename }}
              </span>
              <span class="block text-[12px] text-ink-3">
                {{ formatBytes(file.size_bytes) }}
                <template v-if="file.folder_name"> · {{ file.folder_name }}</template>
                <template v-else> · 根目录</template>
              </span>
            </span>
            <span :class="[ui.badge, file.visibility === 'public' ? '' : ui.badgeMuted]">
              {{ file.visibility === 'public' ? '公开' : '私密' }}
            </span>
          </div>
          <div class="mt-auto flex flex-wrap items-center gap-1.5">
            <button
              :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]"
              type="button"
              @click="toggleFileVisibility(file)"
            >
              {{ file.visibility === 'public' ? '设为私密' : '设为公开' }}
            </button>
            <select
              :class="[ui.input, 'w-auto! max-w-[130px] py-1! text-[12.5px]!']"
              :value="file.folder_id ?? ''"
              aria-label="移动到文件夹"
              @change="changeFileFolder(file, $event)"
            >
              <option value="">根目录</option>
              <option v-for="folder in folders" :key="folder.id" :value="folder.id">{{ folder.name }}</option>
            </select>
            <a
              :href="file.content_url"
              target="_blank"
              rel="noopener"
              class="flex size-[30px] items-center justify-center rounded-[7px] text-ink-3 hover:bg-hover hover:text-ink"
              title="下载"
              aria-label="下载文件"
            >
              <AppIcon name="download" :size="16" />
            </a>
            <button
              type="button"
              class="flex size-[30px] items-center justify-center rounded-[7px] text-ink-3 hover:bg-red-soft hover:text-red"
              title="从网盘删除"
              aria-label="从网盘删除"
              @click="removeFile(file)"
            >
              <AppIcon name="trash" :size="16" />
            </button>
          </div>
        </article>
      </section>

      <button
        v-if="hasMore"
        :class="[ui.button, ui.buttonSecondary, 'self-center']"
        type="button"
        :disabled="loadingMore"
        @click="load(false)"
      >
        {{ loadingMore ? '加载中…' : '加载更多' }}
      </button>
    </div>
  </div>
</template>
