<script setup lang="ts">
// 我的收藏（docs/PAGES_AND_PERMISSIONS.md U-13）
// 私有阅读清单：仅本人可见；按收藏夹过滤、管理收藏夹与移动归属。
// 已删除或当前不可见的帖子由后端过滤，收藏记录保留。
import { computed, onMounted, ref } from 'vue'
import AlertNote from '../components/AlertNote.vue'
import BookmarkFolderDialog from '../components/BookmarkFolderDialog.vue'
import PostCard from '../components/PostCard.vue'
import EmptyState from '../components/EmptyState.vue'
import { errorMessage } from '../api/client'
import { listBookmarks, listBookmarkFolders } from '../api/social'
import type { BookmarkFolder, PostBrief } from '../api/types'
import { useCursorList } from '../composables/useCursorList'
import { ui } from '../styles/ui'

/** 夹过滤三态：''=全部、'default'=未分组、其余为收藏夹 ID */
const activeFolder = ref('')
const folders = ref<BookmarkFolder[]>([])
const foldersLoading = ref(false)
const foldersError = ref('')

const { items, loading, loadingMore, hasMore, error, loadMore, reload } = useCursorList<
  PostBrief,
  { folder_id?: string }
>({
  fetch: (params) => listBookmarks(params),
  params: () => (activeFolder.value === '' ? {} : { folder_id: activeFolder.value }),
})

async function loadFolders(): Promise<void> {
  foldersLoading.value = true
  foldersError.value = ''
  try {
    const result = await listBookmarkFolders()
    folders.value = result.items
  } catch (err) {
    foldersError.value = errorMessage(err)
  } finally {
    foldersLoading.value = false
  }
}

onMounted(() => void loadFolders())

function switchFolder(folder: string): void {
  if (activeFolder.value === folder) return
  activeFolder.value = folder
  void reload()
}

// ---------- 收藏夹管理与移动 ----------

const dialogOpen = ref(false)
const dialogMode = ref<'manage' | 'move'>('manage')
const movingPostId = ref('')
const movingFolderId = ref<string | null>(null)

function openManage(): void {
  dialogMode.value = 'manage'
  dialogOpen.value = true
}

function openMove(item: PostBrief): void {
  dialogMode.value = 'move'
  movingPostId.value = item.id
  movingFolderId.value = item.bookmark?.folder_id ?? null
  dialogOpen.value = true
}

async function onFoldersChanged(): Promise<void> {
  await loadFolders()
  // 删除当前正在浏览的收藏夹后回到全部视图，避免停留在已失效的过滤条件上
  const stillExists =
    activeFolder.value === '' ||
    activeFolder.value === 'default' ||
    folders.value.some((folder) => folder.id === activeFolder.value)
  if (!stillExists) activeFolder.value = ''
  void reload()
}

function onMoved(): void {
  // 归属变化影响计数与当前过滤结果，整体刷新最直接
  void loadFolders()
  void reload()
}

// 收藏页条目在被取消收藏后即时移除（卡片按钮与操作提示已在 PostCard 内处理）
function onBookmarkChange(payload: { id: string; bookmarked: boolean }): void {
  if (payload.bookmarked) return
  items.value = items.value.filter((item) => item.id !== payload.id)
}

const activeFolderLabel = computed(() => {
  if (activeFolder.value === '') return '全部收藏'
  if (activeFolder.value === 'default') return '默认收藏'
  return folders.value.find((folder) => folder.id === activeFolder.value)?.name ?? '收藏'
})
</script>

<template>
  <div :class="[ui.constrain]">
    <div :class="[ui.stack]">
      <header :class="[ui.pageHead]">
        <h1 :class="[ui.pageTitle]">我的收藏</h1>
        <p :class="[ui.pageDesc]">收藏只有你自己可见；帖子被删除或不再可见时会自动从列表隐藏，恢复可见后重新出现。</p>
      </header>

      <div class="flex flex-wrap items-center gap-1 border-b border-solid border-border">
        <button
          type="button"
          :class="[ui.tabLink, activeFolder === '' ? ui.tabLinkActive : '']"
          @click="switchFolder('')"
        >
          全部收藏
        </button>
        <button
          type="button"
          :class="[ui.tabLink, activeFolder === 'default' ? ui.tabLinkActive : '']"
          @click="switchFolder('default')"
        >
          默认收藏
        </button>
        <button
          v-for="folder in folders"
          :key="folder.id"
          type="button"
          :class="[ui.tabLink, activeFolder === folder.id ? ui.tabLinkActive : '']"
          @click="switchFolder(folder.id)"
        >
          {{ folder.name }}
        </button>
        <button
          type="button"
          class="ml-auto flex items-center gap-1 px-3 py-2 text-[13.5px] text-ink-3 hover-not-disabled:text-ink"
          @click="openManage"
        >
          管理收藏夹
        </button>
      </div>

      <AlertNote v-if="error && items.length > 0" kind="error">{{ error }}</AlertNote>

      <div v-if="loading" :class="[ui.card, ui.cardPad]">
        <p :class="[ui.loadingRow]"><span :class="[ui.spinner]" aria-hidden="true"></span> 正在加载收藏…</p>
      </div>

      <template v-else-if="error && items.length === 0">
        <AlertNote kind="error">{{ error }}</AlertNote>
        <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'self-start']" type="button" @click="reload()">
          重试
        </button>
      </template>

      <section v-else-if="items.length === 0" :class="[ui.card]">
        <EmptyState
          icon="bookmark"
          :title="`「${activeFolderLabel}」还没有收藏`"
          description="在帖子卡片的收藏按钮上点击即可收藏；收藏夹分组可以在「管理收藏夹」中创建。"
        />
      </section>

      <template v-else>
        <div class="grid gap-2">
          <PostCard
            v-for="item in items"
            :key="item.id"
            :post="item"
            @bookmark-change="onBookmarkChange"
          >
            <template #actions-extra>
              <button
                type="button"
                class="inline-flex items-center gap-[5px] rounded-[8px] px-2 py-[5px] text-[13px] text-ink-3 hover-not-disabled:bg-hover hover-not-disabled:text-ink! disabled:cursor-not-allowed disabled:opacity-60"
                title="移动到收藏夹"
                @click="openMove(item)"
              >
                <span>移动</span>
              </button>
            </template>
          </PostCard>
        </div>

        <div class="flex justify-center">
          <button
            v-if="hasMore"
            :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]"
            type="button"
            :disabled="loadingMore"
            @click="loadMore()"
          >
            <span
              v-if="loadingMore"
              :class="[ui.spinner, 'size-[14px]! border-border! border-t-primary! opacity-80']"
              aria-hidden="true"
            ></span>
            {{ loadingMore ? '正在加载…' : '加载更多' }}
          </button>
          <p v-else :class="[ui.muted, 'm-0']">已加载全部收藏</p>
        </div>
      </template>

      <BookmarkFolderDialog
        v-if="dialogOpen"
        :folders="folders"
        :mode="dialogMode"
        :post-id="dialogMode === 'move' ? movingPostId : undefined"
        :current-folder-id="dialogMode === 'move' ? movingFolderId : undefined"
        @close="dialogOpen = false"
        @folders-changed="onFoldersChanged"
        @moved="onMoved"
      />
    </div>
  </div>
</template>
