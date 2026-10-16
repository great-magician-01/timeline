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
let counter = 0

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
  <div class="uploader">
    <div
      class="drop"
      :class="{ 'is-dragging': dragging, 'is-disabled': disabled }"
      @dragover.prevent="dragging = true"
      @dragleave.prevent="dragging = false"
      @drop.prevent="onDrop"
      @click="pick"
    >
      <input ref="input" type="file" multiple hidden @change="onSelect" />
      <AppIcon name="upload" :size="20" />
      <span>点击选择文件，或拖放到此处</span>
      <small class="muted">支持图片、视频与其他文件；单文件上限由服务端配置</small>
    </div>

    <ul v-if="attachments.length > 0" class="list">
      <li v-for="item in attachments" :key="item.id">
        <img v-if="item.kind === 'image'" :src="previewURL(item)" :alt="item.original_filename" />
        <AppIcon v-else :name="item.kind === 'video' ? 'image' : 'file-text'" :size="18" />
        <span class="name">{{ item.original_filename }}</span>
        <span class="muted">{{ formatBytes(item.size_bytes) }}</span>
        <button type="button" class="icon-btn" aria-label="移除附件" :disabled="disabled" @click="remove(item)">
          <AppIcon name="x" :size="15" />
        </button>
      </li>
    </ul>

    <ul v-if="tasks.length > 0" class="list">
      <li v-for="task in tasks" :key="task.key">
        <AppIcon :name="task.error ? 'alert' : 'upload'" :size="16" />
        <span class="name">{{ task.name }}</span>
        <span v-if="task.error" class="err">{{ task.error }}</span>
        <span v-else class="muted">{{ task.percent }}%</span>
        <button v-if="task.error" type="button" class="icon-btn" aria-label="关闭" @click="dismiss(task.key)">
          <AppIcon name="x" :size="15" />
        </button>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.uploader {
  display: grid;
  gap: 8px;
}
.drop {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  padding: 18px;
  border: 1px dashed var(--border);
  border-radius: 10px;
  background: var(--surface-subtle);
  color: var(--ink-2);
  font-size: 13.5px;
  cursor: pointer;
  text-align: center;
}
.drop:hover:not(.is-disabled) {
  border-color: var(--primary);
  color: var(--ink);
}
.drop.is-dragging {
  border-color: var(--primary);
  background: var(--primary-soft);
}
.drop.is-disabled {
  cursor: not-allowed;
  opacity: 0.7;
}
.drop small {
  font-size: 12px;
}
.list {
  display: grid;
  gap: 6px;
  margin: 0;
  padding: 0;
  list-style: none;
}
.list li {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 8px;
  border: 1px solid var(--border);
  border-radius: 8px;
  font-size: 13px;
}
.list img {
  width: 34px;
  height: 34px;
  border-radius: 6px;
  object-fit: cover;
}
.name {
  min-width: 0;
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.muted {
  color: var(--ink-3);
  font-size: 12px;
}
.err {
  color: var(--red);
  font-size: 12px;
}
.icon-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  border-radius: 6px;
  color: var(--ink-3);
}
.icon-btn:hover {
  background: var(--hover);
  color: var(--ink);
}
</style>
