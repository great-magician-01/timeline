<script setup lang="ts">
// 附件展示：图片优先缩略图（未就绪回退原图），视频用封面 + 原生播放，其他文件提供下载
// 所有地址都是鉴权接口，派生文件未就绪时前端必须有回退（docs/REQUIREMENTS.md 7.1.6）
import { computed, ref } from 'vue'
import AppIcon from './AppIcon.vue'
import { API_BASE } from '../api/client'
import type { Attachment } from '../api/types'
import { formatBytes, formatDuration } from '../utils/format'

const props = withDefaults(defineProps<{ attachments: Attachment[]; compact?: boolean }>(), {
  compact: false,
})

const images = computed(() => props.attachments.filter((item) => item.kind === 'image'))
const videos = computed(() => props.attachments.filter((item) => item.kind === 'video'))
const files = computed(() => props.attachments.filter((item) => item.kind === 'file'))

const imageFailed = ref<Record<string, boolean>>({})
const thumbnailFailed = ref<Record<string, boolean>>({})

function imageSrc(item: Attachment): string {
  if (!thumbnailFailed.value[item.id] && item.thumbnail_url) return item.thumbnail_url
  return item.content_url
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
</script>

<template>
  <div v-if="attachments.length > 0" class="attachments" :class="{ 'is-compact': compact }">
    <div v-if="images.length > 0" class="grid" :class="`grid--${Math.min(images.length, 4)}`">
      <button
        v-for="item in images"
        :key="item.id"
        type="button"
        class="thumb"
        :aria-label="`查看图片 ${item.original_filename}`"
        @click="previewing = item"
      >
        <img
          :src="imageSrc(item)"
          :alt="item.original_filename"
          loading="lazy"
          @error="thumbnailFailed[item.id] = true"
        />
      </button>
    </div>

    <div v-for="item in videos" :key="item.id" class="video">
      <video
        controls
        preload="metadata"
        :poster="videoPoster(item)"
        :src="item.content_url"
        @error="thumbnailFailed[item.id] = true"
      ></video>
      <p class="meta">
        <AppIcon name="clock" :size="14" />
        <span>{{ item.original_filename }}</span>
        <span v-if="formatDuration(item.duration_ms)">· {{ formatDuration(item.duration_ms) }}</span>
        <span v-if="item.derivatives?.video_cover === 'failed'" class="muted">· 封面生成失败</span>
      </p>
    </div>

    <ul v-if="files.length > 0" class="files">
      <li v-for="item in files" :key="item.id">
        <AppIcon name="file-text" :size="16" />
        <a :href="item.content_url" :download="item.original_filename" rel="noopener">
          {{ item.original_filename }}
        </a>
        <span class="muted">{{ formatBytes(item.size_bytes) }}</span>
      </li>
    </ul>
  </div>

  <div v-if="previewing" class="lightbox" role="dialog" aria-modal="true" @click.self="previewing = null">
    <button type="button" class="close" aria-label="关闭" @click="previewing = null">
      <AppIcon name="x" :size="20" />
    </button>
    <img
      v-if="!imageFailed[previewing.id]"
      :src="previewing.content_url"
      :alt="previewing.original_filename"
      @error="imageFailed[previewing.id] = true"
    />
    <p v-else class="alert alert--error">图片加载失败</p>
  </div>
</template>

<style scoped>
.attachments {
  display: grid;
  gap: 10px;
  margin-top: 10px;
}
.grid {
  display: grid;
  gap: 6px;
  grid-template-columns: repeat(2, minmax(0, 1fr));
}
.grid--1 {
  grid-template-columns: minmax(0, 1fr);
  max-width: 420px;
}
.grid--3,
.grid--4 {
  grid-template-columns: repeat(2, minmax(0, 1fr));
}
.thumb {
  overflow: hidden;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--soft);
  padding: 0;
  cursor: zoom-in;
}
.thumb img {
  display: block;
  width: 100%;
  max-height: 320px;
  object-fit: cover;
}
.is-compact .thumb img {
  max-height: 180px;
}
.video video {
  display: block;
  width: 100%;
  max-height: 420px;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--media-bg);
}
.video .meta,
.files li {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 6px 0 0;
  font-size: 12.5px;
  color: var(--ink-3);
}
.files {
  display: grid;
  gap: 6px;
  margin: 0;
  padding: 0;
  list-style: none;
}
.files a {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--primary);
}
.lightbox {
  position: fixed;
  inset: 0;
  z-index: 60;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  background: var(--overlay);
}
.lightbox img {
  max-width: 100%;
  max-height: 100%;
  border-radius: 8px;
}
.lightbox .close {
  position: absolute;
  top: 18px;
  right: 18px;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border-radius: 50%;
  background: var(--on-media-soft);
  color: var(--on-media);
}
</style>
