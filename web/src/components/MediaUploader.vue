<script setup lang="ts">
// 附件上传：多选与拖放、进度、失败提示与移除（docs/REQUIREMENTS.md 7.4）
// 上传成功后再把附件 ID 交给上层，避免把尚未落库的本地文件混进正文提交。
import { ref } from 'vue'
import AppIcon from './AppIcon.vue'
import { deleteAttachment, uploadAttachment } from '../api/media'
import { errorMessage } from '../api/client'
import type { Attachment } from '../api/types'
import { formatBytes } from '../utils/format'

const props = defineProps<{
  /** 已关联的附件（来自服务端） */
  attachments: Attachment[]
  disabled?: boolean
}>()

const emit = defineEmits<{
  (event: 'added', attachment: Attachment): void
  (event: 'removed', id: string): void
}>()

interface UploadTask {
  key: number
  name: string
  percent: number
  error: string
}

const tasks = ref<UploadTask[]>([])
const dragging = ref(false)
const input = ref<HTMLInputElement | null>(null)
const previewFailed = ref<Record<string, boolean>>({})
let counter = 0

function statusLabel(attachment: Attachment): string {
  if (attachment.status === 'processing') return '处理中'
  if (attachment.status === 'failed') return '处理失败'
  return ''
}

function pick(): void {
  if (props.disabled) return
  input.value?.click()
}

async function upload(files: FileList | File[]): Promise<void> {
  for (const file of Array.from(files)) {
    const task: UploadTask = { key: ++counter, name: file.name, percent: 0, error: '' }
    tasks.value.push(task)
    try {
      const result = await uploadAttachment(file, 'post', (percent) => {
        task.percent = percent
      })
      emit('added', result.attachment)
      tasks.value = tasks.value.filter((item) => item.key !== task.key)
    } catch (err) {
      task.error = errorMessage(err)
      task.percent = 100
    }
  }
}

function onDrop(event: DragEvent): void {
  dragging.value = false
  if (props.disabled) return
  const files = event.dataTransfer?.files
  if (files && files.length > 0) void upload(files)
}

function onSelect(event: Event): void {
  const files = (event.target as HTMLInputElement).files
  if (files && files.length > 0) void upload(files)
  ;(event.target as HTMLInputElement).value = ''
}

async function remove(attachment: Attachment): Promise<void> {
  if (props.disabled) return
  try {
    await deleteAttachment(attachment.id)
    emit('removed', attachment.id)
  } catch (err) {
    const task: UploadTask = {
      key: ++counter,
      name: attachment.original_filename,
      percent: 100,
      error: errorMessage(err),
    }
    tasks.value.push(task)
  }
}

function dismiss(key: number): void {
  tasks.value = tasks.value.filter((item) => item.key !== key)
}

function previewURL(attachment: Attachment): string {
  return attachment.thumbnail_url || attachment.content_url
}
</script>

<template>
  <div class="grid gap-2">
    <div
      :class="[
        'flex cursor-pointer flex-col items-center gap-1 rounded-[10px] border border-dashed border-border bg-surface-subtle p-[18px] text-center text-[13.5px] text-ink-2',
        disabled ? 'cursor-not-allowed! opacity-70' : 'hover:border-primary hover:text-ink',
        dragging ? 'border-primary! bg-primary-soft!' : '',
      ]"
      @dragover.prevent="dragging = true"
      @dragleave.prevent="dragging = false"
      @drop.prevent="onDrop"
      @click="pick"
    >
      <input ref="input" type="file" multiple hidden @change="onSelect" />
      <AppIcon name="upload" :size="20" />
      <span>点击选择文件，或拖放到此处</span>
      <small class="text-[12px] text-ink-3">支持图片、视频与其他文件；单文件上限由服务端配置</small>
    </div>

    <ul v-if="attachments.length > 0" class="m-0 grid list-none gap-[6px] p-0">
      <li
        v-for="item in attachments"
        :key="item.id"
        class="flex items-center gap-2 rounded-[8px] border border-solid border-border px-2 py-[6px] text-[13px]"
      >
        <img
          v-if="item.kind === 'image' && !previewFailed[item.id]"
          :src="previewURL(item)"
          :alt="item.original_filename"
          class="size-[34px] rounded-[6px] object-cover"
          @error="previewFailed[item.id] = true"
        />
        <AppIcon v-else :name="item.kind === 'video' ? 'image' : 'file-text'" :size="18" />
        <span class="min-w-0 flex-1 overflow-hidden text-ellipsis whitespace-nowrap">{{ item.original_filename }}</span>
        <span
          v-if="statusLabel(item)"
          class="whitespace-nowrap rounded-[999px] bg-soft px-[7px] py-[1px] text-[11.5px] font-semibold text-ink-2"
          >{{ statusLabel(item) }}</span
        >
        <span class="text-[12px] text-ink-3">{{ formatBytes(item.size_bytes) }}</span>
        <button
          type="button"
          class="flex size-[26px] items-center justify-center rounded-[6px] text-ink-3 hover:bg-hover hover:text-ink"
          aria-label="移除附件"
          :disabled="disabled"
          @click="remove(item)"
        >
          <AppIcon name="x" :size="15" />
        </button>
      </li>
    </ul>

    <ul v-if="tasks.length > 0" class="m-0 grid list-none gap-[6px] p-0">
      <li
        v-for="task in tasks"
        :key="task.key"
        class="flex items-center gap-2 rounded-[8px] border border-solid border-border px-2 py-[6px] text-[13px]"
      >
        <AppIcon :name="task.error ? 'alert' : 'upload'" :size="16" />
        <span class="min-w-0 flex-1 overflow-hidden text-ellipsis whitespace-nowrap">{{ task.name }}</span>
        <span v-if="task.error" class="text-[12px] text-red">{{ task.error }}</span>
        <span v-else class="text-[12px] text-ink-3">{{ task.percent }}%</span>
        <button
          v-if="task.error"
          type="button"
          class="flex size-[26px] items-center justify-center rounded-[6px] text-ink-3 hover:bg-hover hover:text-ink"
          aria-label="关闭"
          @click="dismiss(task.key)"
        >
          <AppIcon name="x" :size="15" />
        </button>
      </li>
    </ul>
  </div>
</template>
