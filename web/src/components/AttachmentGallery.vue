<script setup lang="ts">
// 附件展示：图片优先缩略图（未就绪回退原图），视频用封面 + 原生播放，其他文件提供下载
// 所有地址都是鉴权接口，派生文件未就绪时前端必须有回退（docs/REQUIREMENTS.md 7.1.6）
import { computed, ref } from 'vue'
import AppIcon from './AppIcon.vue'
import { API_BASE } from '../api/client'
import type { Attachment } from '../api/types'
import { formatBytes, formatDuration } from '../utils/format'
import { ui } from '../styles/ui'

const props = withDefaults(defineProps<{ attachments: Attachment[]; compact?: boolean }>(), {
  compact: false,
})

const images = computed(() => props.attachments.filter((item) => item.kind === 'image'))
const videos = computed(() => props.attachments.filter((item) => item.kind === 'video'))
const files = computed(() => props.attachments.filter((item) => item.kind === 'file'))

const imageFailed = ref<Record<string, boolean>>({})
const thumbnailFailed = ref<Record<string, boolean>>({})
const videoFailed = ref<Record<string, boolean>>({})

function imageSrc(item: Attachment): string {
  if (!thumbnailFailed.value[item.id] && item.thumbnail_url) return item.thumbnail_url
  return item.content_url
}

/** 缩略图失败先回退原图；原图也失败才显示占位文案 */
function onImageError(item: Attachment): void {
  if (!thumbnailFailed.value[item.id] && item.thumbnail_url) {
    thumbnailFailed.value[item.id] = true
    return
  }
  imageFailed.value[item.id] = true
}

function derivativeState(item: Attachment, key: string): string {
  return item.derivatives?.[key] ?? ''
}

/** 图片缩略图尚在生成或附件本身处理中（docs/PAGES_AND_PERMISSIONS.md E-04） */
function imageProcessing(item: Attachment): boolean {
  if (item.status === 'processing') return true
  const state = derivativeState(item, 'thumbnail')
  return state === 'pending' || state === 'processing'
}

function videoCoverState(item: Attachment): '' | 'processing' | 'failed' {
  const state = derivativeState(item, 'video_cover')
  if (state === 'failed') return 'failed'
  if (state === 'pending' || state === 'processing') return 'processing'
  if (!state && item.status === 'processing') return 'processing'
  return ''
}

function attachmentStatusLabel(item: Attachment): string {
  if (item.status === 'processing') return '处理中'
  if (item.status === 'failed') return '处理失败'
  return ''
}

function videoCoverURL(item: Attachment): string {
  return `${API_BASE}/attachments/${item.id}/derivatives/video_cover`
}

/** 封面未就绪时返回 undefined，浏览器显示首帧占位；失败后不再请求，避免刷屏 */
function videoPoster(item: Attachment): string | undefined {
  if (thumbnailFailed.value[item.id]) return undefined
  return item.derivatives?.video_cover === 'ready' ? videoCoverURL(item) : undefined
}

const previewing = ref<Attachment | null>(null)

/** 图片 1–4 张时的完整静态网格映射；单图按列宽比例展示，移动端恢复全宽 */
const gridClasses: Record<number, string> = {
  1: 'grid grid-cols-1 gap-[6px] max-w-[72%] shell-down:max-w-full',
  2: 'grid grid-cols-2 gap-[6px]',
  3: 'grid grid-cols-2 gap-[6px]',
  4: 'grid grid-cols-2 gap-[6px]',
}

// 媒体高度随视口缩放：窄屏保持原尺寸，宽屏信息流列变宽时同步放大，避免缩略图显得过小
const compactImageClass = 'max-h-[clamp(180px,13vw,372px)]'
const detailImageClass = 'max-h-[clamp(320px,22vw,560px)]'
const videoClass = 'max-h-[clamp(420px,28vw,780px)]'
</script>

<template>
  <div v-if="attachments.length > 0" class="mt-[10px] grid gap-[10px]">
    <div v-if="images.length > 0" :class="gridClasses[Math.min(images.length, 4)]">
      <button
        v-for="item in images"
        :key="item.id"
        type="button"
        class="relative cursor-zoom-in overflow-hidden rounded-[10px] border border-solid border-border bg-soft p-0"
        :aria-label="`查看图片 ${item.original_filename}`"
        @click="previewing = item"
      >
        <img
          v-if="!imageFailed[item.id]"
          :src="imageSrc(item)"
          :alt="item.original_filename"
          class="block w-full object-cover"
          :class="compact ? compactImageClass : detailImageClass"
          loading="lazy"
          @error="onImageError(item)"
        />
        <span
          v-else
          class="flex min-h-[120px] items-center justify-center rounded-[10px] border border-dashed border-border bg-surface-subtle p-4 text-center text-[13px] text-ink-3"
          >图片加载失败</span
        >
        <span
          v-if="imageProcessing(item)"
          class="absolute bottom-2 left-2 rounded-[999px] bg-glass px-2 py-[2px] text-[11.5px] font-semibold text-ink-2 backdrop-blur-[4px]"
          >处理中</span
        >
      </button>
    </div>

    <div v-for="item in videos" :key="item.id">
      <video
        v-if="!videoFailed[item.id]"
        controls
        preload="metadata"
        class="block w-full rounded-[10px] border border-solid border-border bg-media-bg"
        :class="videoClass"
        :poster="videoPoster(item)"
        :src="item.content_url"
        @error="videoFailed[item.id] = true"
      ></video>
      <div
        v-else
        class="flex min-h-[120px] items-center justify-center rounded-[10px] border border-dashed border-border bg-surface-subtle p-4 text-center text-[13px] text-ink-3"
      >
        视频加载失败，请稍后重试
      </div>
      <p class="mb-0 mt-[6px] flex items-center gap-[6px] text-[12.5px] text-ink-3">
        <AppIcon name="clock" :size="14" />
        <span>{{ item.original_filename }}</span>
        <span v-if="formatDuration(item.duration_ms)">· {{ formatDuration(item.duration_ms) }}</span>
        <span v-if="videoCoverState(item) === 'processing'" :class="ui.muted">· 封面处理中</span>
        <span v-else-if="videoCoverState(item) === 'failed'" :class="ui.muted">· 封面生成失败，可播放原视频</span>
        <span v-if="attachmentStatusLabel(item)" :class="ui.muted">· {{ attachmentStatusLabel(item) }}</span>
      </p>
    </div>

    <ul v-if="files.length > 0" class="m-0 grid list-none gap-[6px] p-0">
      <li
        v-for="item in files"
        :key="item.id"
        class="mt-[6px] flex items-center gap-[6px] text-[12.5px] text-ink-3"
      >
        <AppIcon name="file-text" :size="16" />
        <a
          :href="item.content_url"
          :download="item.original_filename"
          class="min-w-0 overflow-hidden text-ellipsis whitespace-nowrap text-primary"
          rel="noopener"
        >
          {{ item.original_filename }}
        </a>
        <span :class="ui.muted">{{ formatBytes(item.size_bytes) }}</span>
        <span v-if="attachmentStatusLabel(item)" :class="ui.muted">· {{ attachmentStatusLabel(item) }}</span>
      </li>
    </ul>
  </div>

  <div
    v-if="previewing"
    class="fixed inset-0 z-[60] flex items-center justify-center bg-overlay p-6"
    role="dialog"
    aria-modal="true"
    @click.self="previewing = null"
  >
    <button
      type="button"
      class="absolute right-[18px] top-[18px] flex size-9 items-center justify-center rounded-[50%] bg-on-media-soft text-on-media"
      aria-label="关闭"
      @click="previewing = null"
    >
      <AppIcon name="x" :size="20" />
    </button>
    <img
      v-if="!imageFailed[previewing.id]"
      :src="previewing.content_url"
      :alt="previewing.original_filename"
      class="max-h-full max-w-full rounded-[8px]"
      @error="imageFailed[previewing.id] = true"
    />
    <p v-else :class="[ui.alert, ui.alertVariants.error.root]">图片加载失败</p>
  </div>
</template>
