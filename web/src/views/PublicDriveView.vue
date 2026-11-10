<script setup lang="ts">
// 用户公开网盘：只展示公开且有效的文件，按有公开文件的目录筛选。
// 不展示容量、私密目录、存储键或文章关联。
import { computed, watch, ref } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { errorMessage } from '../api/client'
import { driveFilePreviewURL, listPublicDriveFiles } from '../api/drive'
import type { DriveFile, PublicDriveFolder } from '../api/types'
import AlertNote from '../components/AlertNote.vue'
import AppIcon from '../components/AppIcon.vue'
import EmptyState from '../components/EmptyState.vue'
import { ui } from '../styles/ui'
import { formatBytes } from '../utils/format'

const route = useRoute()
const username = computed(() => String(route.params.username ?? ''))
const files = ref<DriveFile[]>([])
const folders = ref<PublicDriveFolder[]>([])
const currentFolder = ref<'all' | 'root' | string>('all')
const kind = ref<'' | 'image' | 'video' | 'file'>('')
const keyword = ref('')
const loading = ref(false)
const loadingMore = ref(false)
const hasMore = ref(false)
const cursor = ref<string | null>(null)
const error = ref('')
const previewFailed = ref<Record<string, boolean>>({})

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
    const page = await listPublicDriveFiles(username.value, {
      cursor: reset ? undefined : cursor.value ?? undefined,
      limit: 24,
      folder_id: currentFolder.value === 'all' ? undefined : currentFolder.value,
      kind: kind.value || undefined,
      q: keyword.value.trim() || undefined,
    })
    files.value = reset ? page.items : [...files.value, ...page.items]
    cursor.value = page.next_cursor
    hasMore.value = page.has_more
    if (reset) folders.value = page.folders ?? []
  } catch (err) {
    error.value = errorMessage(err)
  } finally {
    loading.value = false
    loadingMore.value = false
  }
}

function folderValue(folder: PublicDriveFolder): 'root' | string {
  return folder.id ?? 'root'
}

function chooseFolder(value: 'all' | 'root' | string): void {
  if (currentFolder.value === value) return
  currentFolder.value = value
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

watch(username, () => {
  currentFolder.value = 'all'
  kind.value = ''
  keyword.value = ''
  void load(true)
}, { immediate: true })
</script>

<template>
  <div :class="[ui.constrain]">
    <div :class="[ui.stack]">
      <section :class="[ui.card, ui.cardPad]">
        <div class="flex flex-wrap items-center gap-2">
          <div class="min-w-0">
            <h1 :class="[ui.pageHead, ui.pageTitle]">@{{ username }} 的公开网盘</h1>
            <p :class="[ui.pageDesc]">这里只展示 TA 设为公开的文件；私密文件与文章引用不会出现在这里。</p>
          </div>
          <RouterLink
            :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'ml-auto']"
            :to="{ name: 'user-profile', params: { username } }"
          >
            <AppIcon name="arrow-left" :size="15" /> 返回个人主页
          </RouterLink>
        </div>
      </section>

      <section :class="[ui.card, ui.cardPad]">
        <div class="flex flex-wrap items-center gap-1.5">
          <button
            type="button"
            :class="[ui.segmentButton, currentFolder === 'all' ? ui.segmentButtonActive : '']"
            @click="chooseFolder('all')"
          >
            全部公开文件
          </button>
          <button
            v-for="folder in folders"
            :key="folder.id ?? 'root'"
            type="button"
            :class="[ui.segmentButton, currentFolder === folderValue(folder) ? ui.segmentButtonActive : '']"
            @click="chooseFolder(folderValue(folder))"
          >
            <AppIcon :name="folder.id ? 'folder' : 'home'" :size="14" />
            {{ folder.name }}
            <span class="text-ink-3">{{ folder.file_count }}</span>
          </button>
        </div>

        <form class="mt-3 flex flex-wrap items-center gap-2" @submit.prevent="submitSearch">
          <input
            v-model="keyword"
            :class="[ui.input, 'w-auto! min-w-[160px] flex-1']"
            type="search"
            placeholder="按文件名搜索"
            aria-label="搜索公开网盘文件"
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

      <AlertNote v-if="error" kind="error">{{ error }}</AlertNote>

      <p v-if="loading" :class="[ui.card, ui.loadingRow]">
        <span :class="ui.spinner" aria-hidden="true"></span> 加载中…
      </p>
      <EmptyState v-else-if="files.length === 0" icon="folder" title="还没有公开文件">
        TA 还没有把网盘文件设为公开。
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
            <span :class="ui.badge">公开</span>
          </div>
          <a
            :href="file.content_url"
            target="_blank"
            rel="noopener"
            :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'mt-auto self-start']"
          >
            <AppIcon name="download" :size="15" /> 下载
          </a>
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
