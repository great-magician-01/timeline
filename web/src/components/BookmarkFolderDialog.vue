<script setup lang="ts">
// 收藏夹对话框：两种模式共用同一弹层骨架。
// manage 模式负责收藏夹管理（新建/重命名/删除，删除时夹内收藏归还默认收藏）；
// move 模式负责把已收藏帖子移动到指定收藏夹（含默认收藏）。
// 弹层样式对齐 RepostDialog：固定遮罩、点遮罩或 Esc 关闭，提交中禁止关闭。
import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import AppIcon from './AppIcon.vue'
import { errorMessage } from '../api/client'
import { bookmarkPost, createBookmarkFolder, deleteBookmarkFolder, renameBookmarkFolder } from '../api/social'
import type { BookmarkFolder } from '../api/types'
import { ui } from '../styles/ui'

const props = defineProps<{
  /** 当前用户的收藏夹列表（父组件持有并刷新） */
  folders: BookmarkFolder[]
  /** manage=收藏夹管理；move=移动收藏到指定夹 */
  mode: 'manage' | 'move'
  /** move 模式：被移动收藏的帖子 ID */
  postId?: string
  /** move 模式：当前归属（null=默认收藏） */
  currentFolderId?: string | null
}>()

const emit = defineEmits<{
  (event: 'close'): void
  /** 收藏夹集合发生变化（父组件刷新列表与计数） */
  (event: 'folders-changed'): void
  /** 移动完成（父组件刷新列表） */
  (event: 'moved', payload: { postId: string; folderId: string | null }): void
}>()

const name = ref('')
const error = ref('')
/** 提交中的操作标识：create/rename:<id>/delete:<id>/move */
const busyOp = ref('')
/** 重命名中的收藏夹 ID 与草稿名 */
const renamingId = ref('')
const renamingName = ref('')

const busy = () => busyOp.value !== ''

function requestClose(): void {
  if (busy()) return
  emit('close')
}

function onKeydown(event: KeyboardEvent): void {
  if (event.key !== 'Escape') return
  event.stopPropagation()
  requestClose()
}

onMounted(() => window.addEventListener('keydown', onKeydown))
onBeforeUnmount(() => window.removeEventListener('keydown', onKeydown))

async function run<T>(op: string, action: () => Promise<T>): Promise<T | null> {
  busyOp.value = op
  error.value = ''
  try {
    return await action()
  } catch (err) {
    error.value = errorMessage(err)
    return null
  } finally {
    busyOp.value = ''
  }
}

async function createFolder(): Promise<void> {
  const trimmed = name.value.trim()
  if (!trimmed || busy()) return
  const created = await run('create', () => createBookmarkFolder(trimmed))
  if (!created) return
  name.value = ''
  emit('folders-changed')
}

function startRename(folder: BookmarkFolder): void {
  renamingId.value = folder.id
  renamingName.value = folder.name
  void nextTick(() => {
    const input = document.getElementById('folder-rename-input') as HTMLInputElement | null
    input?.focus()
  })
}

async function confirmRename(): Promise<void> {
  const folderId = renamingId.value
  const trimmed = renamingName.value.trim()
  if (!folderId || !trimmed || busy()) return
  const updated = await run(`rename:${folderId}`, () => renameBookmarkFolder(folderId, trimmed))
  if (!updated) return
  renamingId.value = ''
  emit('folders-changed')
}

async function removeFolder(folder: BookmarkFolder): Promise<void> {
  const count = folder.bookmark_count
  const message =
    count > 0
      ? `删除「${folder.name}」后其中 ${count} 条收藏将回到默认收藏。确认删除？`
      : `确认删除收藏夹「${folder.name}」？`
  if (!window.confirm(message)) return
  const done = await run(`delete:${folder.id}`, () => deleteBookmarkFolder(folder.id))
  if (done === null) return
  emit('folders-changed')
}

/** 选择移动目标；与当前归属相同则视为取消 */
async function moveTo(folderId: string | null): Promise<void> {
  if (!props.postId || busy()) return
  if ((props.currentFolderId ?? null) === folderId) {
    emit('close')
    return
  }
  const result = await run('move', () => bookmarkPost(props.postId as string, folderId))
  if (!result) return
  emit('moved', { postId: props.postId, folderId })
  emit('close')
}
</script>

<template>
  <div
    class="fixed inset-0 z-[60] flex items-center justify-center bg-overlay p-6"
    role="dialog"
    aria-modal="true"
    aria-labelledby="bookmark-folder-dialog-title"
    @click.self="requestClose"
  >
    <section :class="[ui.card, ui.cardPad, 'grid w-full max-w-[460px] gap-3']">
      <header class="flex items-center gap-2">
        <h2 id="bookmark-folder-dialog-title" :class="ui.pageTitle">
          {{ mode === 'manage' ? '管理收藏夹' : '移动到收藏夹' }}
        </h2>
        <button
          type="button"
          class="ml-auto flex size-8 items-center justify-center rounded-[8px] text-ink-3 hover-not-disabled:bg-hover hover-not-disabled:text-ink disabled:cursor-not-allowed disabled:opacity-60"
          aria-label="关闭"
          :disabled="busy()"
          @click="requestClose"
        >
          <AppIcon name="x" :size="18" />
        </button>
      </header>

      <!-- 新建收藏夹（仅管理模式） -->
      <form v-if="mode === 'manage'" class="flex gap-2" @submit.prevent="createFolder">
        <input
          v-model="name"
          :class="[ui.input, 'min-w-0 flex-1']"
          type="text"
          maxlength="30"
          placeholder="新建收藏夹名称（1-30 个字符）"
          aria-label="新建收藏夹名称"
          :disabled="busy()"
        />
        <button :class="[ui.button, ui.buttonPrimary]" type="submit" :disabled="busy() || name.trim() === ''">
          创建
        </button>
      </form>

      <!-- 移动模式：目标选择 -->
      <div v-if="mode === 'move'" class="grid gap-1.5">
        <button
          type="button"
          class="flex items-center justify-between rounded-[8px] border border-solid border-border px-3 py-2 text-left text-[14px] text-ink hover-not-disabled:bg-hover disabled:cursor-not-allowed disabled:opacity-60"
          :class="(currentFolderId ?? null) === null && 'border-primary! font-semibold'"
          :disabled="busy()"
          @click="moveTo(null)"
        >
          <span>默认收藏（未分组）</span>
          <AppIcon v-if="(currentFolderId ?? null) === null" name="check" :size="16" />
        </button>
        <button
          v-for="folder in folders"
          :key="folder.id"
          type="button"
          class="flex items-center justify-between rounded-[8px] border border-solid border-border px-3 py-2 text-left text-[14px] text-ink hover-not-disabled:bg-hover disabled:cursor-not-allowed disabled:opacity-60"
          :class="currentFolderId === folder.id && 'border-primary! font-semibold'"
          :disabled="busy()"
          @click="moveTo(folder.id)"
        >
          <span class="min-w-0 truncate">{{ folder.name }}</span>
          <AppIcon v-if="currentFolderId === folder.id" name="check" :size="16" />
        </button>
      </div>

      <!-- 管理模式：收藏夹列表 -->
      <ul v-if="mode === 'manage'" class="m-0 grid list-none gap-1.5 p-0">
        <li
          v-for="folder in folders"
          :key="folder.id"
          class="flex flex-wrap items-center gap-2 rounded-[8px] border border-solid border-border px-3 py-2"
        >
          <template v-if="renamingId === folder.id">
            <input
              id="folder-rename-input"
              v-model="renamingName"
              :class="[ui.input, 'min-w-0 flex-1']"
              type="text"
              maxlength="30"
              aria-label="收藏夹新名称"
              :disabled="busy()"
              @keydown.enter.prevent="confirmRename"
            />
            <button
              :class="[ui.button, ui.buttonPrimary, ui.buttonSmall]"
              type="button"
              :disabled="busy() || renamingName.trim() === ''"
              @click="confirmRename"
            >
              保存
            </button>
            <button
              :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]"
              type="button"
              :disabled="busy()"
              @click="renamingId = ''"
            >
              取消
            </button>
          </template>
          <template v-else>
            <span class="min-w-0 flex-1 truncate text-[14px] text-ink">{{ folder.name }}</span>
            <span :class="ui.muted">{{ folder.bookmark_count }} 条</span>
            <button
              :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]"
              type="button"
              :disabled="busy()"
              @click="startRename(folder)"
            >
              重命名
            </button>
            <button
              :class="[ui.button, ui.buttonDanger, ui.buttonSmall]"
              type="button"
              :disabled="busy()"
              @click="removeFolder(folder)"
            >
              删除
            </button>
          </template>
        </li>
        <li v-if="folders.length === 0" :class="[ui.empty, 'm-0']">
          还没有收藏夹；收藏默认进入「默认收藏」，也可以创建收藏夹分组管理。
        </li>
      </ul>

      <p v-if="error" :class="[ui.alert, ui.alertVariants.warning.root]">
        <AppIcon name="alert" :size="17" :class="[ui.alertIcon, ui.alertVariants.warning.icon]" />
        <span :class="ui.alertText">{{ error }}</span>
      </p>
    </section>
  </div>
</template>
