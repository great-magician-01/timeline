<script setup lang="ts">
// 富文本编辑器：基于 contenteditable + execCommand 的轻量实现。
// 输出 HTML 不可信，后端会用同一套白名单清洗（docs/REQUIREMENTS.md 6.1）。
import { onMounted, ref, watch } from 'vue'
import AppIcon from './AppIcon.vue'
import type { IconName } from './icons'

const props = withDefaults(
  defineProps<{
    modelValue: string
    placeholder?: string
    disabled?: boolean
    minHeight?: number
  }>(),
  { placeholder: '写点什么…', disabled: false, minHeight: 220 },
)

const emit = defineEmits<{ (event: 'update:modelValue', value: string): void }>()

const editor = ref<HTMLDivElement | null>(null)
const focused = ref(false)

function syncFromDom(): void {
  const html = editor.value?.innerHTML ?? ''
  emit('update:modelValue', isEffectivelyEmpty(html) ? '' : html)
}

/** 只有 <br> 与空标签时视为空内容，避免把占位符当成正文提交 */
function isEffectivelyEmpty(html: string): boolean {
  return html.replace(/<br\s*\/?>/gi, '').replace(/<[^>]*>/g, '').replace(/&nbsp;/g, ' ').trim() === ''
}

watch(
  () => props.modelValue,
  (value) => {
    if (!editor.value) return
    // 聚焦编辑时不回写 DOM，避免光标跳到开头
    if (focused.value) return
    if (editor.value.innerHTML !== value) editor.value.innerHTML = value
  },
)

onMounted(() => {
  if (editor.value) editor.value.innerHTML = props.modelValue
})

interface ToolAction {
  icon: IconName
  label: string
  command: string
  value?: string
}

const tools: ToolAction[] = [
  { icon: 'pencil', label: '大标题', command: 'formatBlock', value: 'h2' },
  { icon: 'list', label: '小标题', command: 'formatBlock', value: 'h3' },
  { icon: 'check', label: '加粗', command: 'bold' },
  { icon: 'info', label: '斜体', command: 'italic' },
  { icon: 'repeat', label: '删除线', command: 'strikeThrough' },
  { icon: 'message', label: '引用', command: 'formatBlock', value: 'blockquote' },
  { icon: 'list', label: '无序列表', command: 'insertUnorderedList' },
  { icon: 'list', label: '有序列表', command: 'insertOrderedList' },
  { icon: 'cpu', label: '代码块', command: 'formatBlock', value: 'pre' },
]

function run(tool: ToolAction): void {
  if (props.disabled) return
  editor.value?.focus()
  try {
    document.execCommand(tool.command, false, tool.value)
  } catch {
    // 某些浏览器对个别命令不支持；忽略即可，内容仍会经后端清洗
  }
  syncFromDom()
}

function insertLink(): void {
  if (props.disabled) return
  const url = window.prompt('链接地址（http/https）')
  if (!url) return
  if (!/^https?:\/\//i.test(url)) {
    window.alert('只支持 http 或 https 链接')
    return
  }
  editor.value?.focus()
  document.execCommand('createLink', false, url)
  syncFromDom()
}

function clearFormat(): void {
  if (props.disabled) return
  editor.value?.focus()
  document.execCommand('removeFormat')
  document.execCommand('formatBlock', false, 'p')
  syncFromDom()
}

function onPaste(event: ClipboardEvent): void {
  // 只保留纯文本，避免粘贴外部样式与脚本（后端仍会再次清洗）
  event.preventDefault()
  const text = event.clipboardData?.getData('text/plain') ?? ''
  document.execCommand('insertText', false, text)
  syncFromDom()
}
</script>

<template>
  <div class="overflow-hidden rounded-[10px] border border-solid border-border bg-surface focus-within:border-primary focus-within:shadow-[0_0_0_3px_var(--primary-soft)]">
    <div
      class="flex flex-wrap gap-[2px] border-b border-solid border-border bg-surface-subtle p-[6px]"
      :class="disabled ? 'opacity-60' : ''"
      role="toolbar"
      aria-label="格式工具"
    >
      <button
        v-for="tool in tools"
        :key="tool.label + (tool.value ?? '')"
        type="button"
        class="flex size-[30px] items-center justify-center rounded-[7px] text-ink-2 hover-not-disabled:bg-hover hover-not-disabled:text-ink"
        :title="tool.label"
        :aria-label="tool.label"
        :disabled="disabled"
        @mousedown.prevent
        @click="run(tool)"
      >
        <AppIcon :name="tool.icon" :size="16" />
      </button>
      <button
        type="button"
        class="flex size-[30px] items-center justify-center rounded-[7px] text-ink-2 hover-not-disabled:bg-hover hover-not-disabled:text-ink"
        title="插入链接"
        aria-label="插入链接"
        :disabled="disabled"
        @mousedown.prevent
        @click="insertLink"
      >
        <AppIcon name="globe" :size="16" />
      </button>
      <button
        type="button"
        class="flex size-[30px] items-center justify-center rounded-[7px] text-ink-2 hover-not-disabled:bg-hover hover-not-disabled:text-ink"
        title="清除格式"
        aria-label="清除格式"
        :disabled="disabled"
        @mousedown.prevent
        @click="clearFormat"
      >
        <AppIcon name="x" :size="16" />
      </button>
    </div>
    <div
      ref="editor"
      class="p-[14px] text-[15px] leading-[1.75] outline-none [word-break:break-word] empty:before:text-ink-3 empty:before:content-[attr(data-placeholder)] [&_blockquote]:m-0 [&_blockquote]:border-l-[3px] [&_blockquote]:border-solid [&_blockquote]:border-border [&_blockquote]:pl-3 [&_blockquote]:text-ink-2 [&_pre]:overflow-auto [&_pre]:rounded-[8px] [&_pre]:bg-soft [&_pre]:p-[10px] [&_pre]:text-[13px]"
      :style="{ minHeight: `${minHeight}px` }"
      :contenteditable="!disabled"
      role="textbox"
      aria-multiline="true"
      :data-placeholder="placeholder"
      @input="syncFromDom"
      @blur="focused = false; syncFromDom()"
      @focus="focused = true"
      @paste="onPaste"
    ></div>
  </div>
</template>
