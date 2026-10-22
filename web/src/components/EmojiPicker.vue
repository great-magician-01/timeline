<script setup lang="ts">
// 表情选取器：基于 emoji-picker-element（web component）的统一封装。
// 触发按钮由使用方经 #trigger 插槽自绘（编辑器工具栏钮与输入行钮样式不同）；
// 面板 fixed 定位锚定触发钮，避免被编辑器等容器的 overflow-hidden 裁剪，
// z 层级高于转发弹窗（z-[60]）；配色全部映射 tokens.css 令牌，随主题切换。
// 库按需懒加载（首次打开才拉取分包）；中文文案用库内置 zh_CN 语言包，
// 表情数据用本地 zh 注记数据包（含中文标签，内置搜索可直接用中文）；
// 「常用」分区由库经 IndexedDB 自动计数维护。
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { Picker } from 'emoji-picker-element'
import zhCN from 'emoji-picker-element/i18n/zh_CN.js'
import { ui } from '../styles/ui'

const props = withDefaults(
  defineProps<{
    /** 禁用时点击触发钮不打开（按钮自身的禁用态由插槽方负责） */
    disabled?: boolean
    /** 面板与触发钮的对齐侧：left 面板左缘对齐触发钮左缘 */
    align?: 'left' | 'right'
  }>(),
  { disabled: false, align: 'left' },
)

const emit = defineEmits<{
  (event: 'select', emoji: string): void
  (event: 'open-change', open: boolean): void
}>()

// 面板固定尺寸：定位无需测量，避免首帧闪跳
const PANEL_WIDTH = 340
const PANEL_HEIGHT = 380
const VIEWPORT_MARGIN = 8
const ANCHOR_GAP = 6

// 主题令牌映射：库在 :host 上声明自己的配色（含跟随系统的深色默认），
// :host 声明优先于从容器继承的变量，因此必须在 host 元素上以内联样式注入
// （内联优先级最高），令牌引用使面板随 tokens.css 全部主题切换。
const PICKER_THEME_VARS: [string, string][] = [
  ['--background', 'var(--surface)'],
  ['--border-color', 'transparent'],
  ['--border-radius', '0px'],
  ['--category-font-color', 'var(--ink-3)'],
  ['--indicator-color', 'var(--primary)'],
  ['--input-border-color', 'var(--border)'],
  ['--input-font-color', 'var(--ink)'],
  ['--input-placeholder-color', 'var(--ink-3)'],
  ['--outline-color', 'var(--primary)'],
  ['--button-active-background', 'var(--primary-soft)'],
  ['--button-hover-background', 'var(--hover)'],
]

const open = ref(false)
/** 一旦打开过就保持面板挂载（v-show 切换），复用已创建的 picker 实例 */
const everOpened = ref(false)
const loading = ref(false)
const root = ref<HTMLElement | null>(null)
const mountPoint = ref<HTMLDivElement | null>(null)
const pos = ref({ left: 0, top: 0 })
let picker: Picker | null = null

async function ensurePicker(): Promise<void> {
  if (picker || loading.value) return
  loading.value = true
  try {
    const [mod, data] = await Promise.all([
      import('emoji-picker-element'),
      import('emoji-picker-element-data/zh/emojibase/data.json'),
    ])
    if (!mountPoint.value || picker) return
    const instance = new mod.Picker({
      // 库默认从 CDN 拉取数据；这里把本地数据打包为稳定 data: URL 注入，
      // 既消除外链依赖，又让 IndexedDB 以同一字符串为缓存键跨会话复用
      dataSource: `data:application/json;charset=utf-8,${encodeURIComponent(JSON.stringify(data.default))}`,
      i18n: zhCN,
    })
    instance.addEventListener('emoji-click', (event) => {
      const { unicode, emoji } = event.detail
      const value = unicode ?? ('unicode' in emoji ? emoji.unicode : '')
      if (value) {
        emit('select', value)
        close()
      }
    })
    instance.style.width = '100%'
    instance.style.height = '100%'
    // color-scheme 跟随应用当前主题（库会在系统深色时强制 dark，导致表单控件与滚动条错配）
    instance.style.setProperty('color-scheme', 'inherit')
    for (const [name, value] of PICKER_THEME_VARS) instance.style.setProperty(name, value)
    picker = instance
    mountPoint.value.appendChild(instance)
  } finally {
    loading.value = false
  }
}

function updatePosition(): void {
  const anchor = root.value
  if (!anchor) return
  const rect = anchor.getBoundingClientRect()
  let left = props.align === 'right' ? rect.right - PANEL_WIDTH : rect.left
  left = Math.min(
    Math.max(left, VIEWPORT_MARGIN),
    Math.max(VIEWPORT_MARGIN, window.innerWidth - PANEL_WIDTH - VIEWPORT_MARGIN),
  )
  let top = rect.bottom + ANCHOR_GAP
  // 下方放不下且上方更宽松时翻转到触发钮上方
  if (top + PANEL_HEIGHT > window.innerHeight - VIEWPORT_MARGIN && rect.top - PANEL_HEIGHT - ANCHOR_GAP >= VIEWPORT_MARGIN) {
    top = rect.top - PANEL_HEIGHT - ANCHOR_GAP
  }
  top = Math.min(
    Math.max(top, VIEWPORT_MARGIN),
    Math.max(VIEWPORT_MARGIN, window.innerHeight - PANEL_HEIGHT - VIEWPORT_MARGIN),
  )
  pos.value = { left, top }
}

function toggle(): void {
  if (props.disabled) return
  open.value = !open.value
  if (open.value) {
    everOpened.value = true
    updatePosition()
    void ensurePicker()
  }
}

function close(): void {
  open.value = false
}

function isInside(event: Event): boolean {
  const element = root.value
  return element !== null && event.target instanceof Node && element.contains(event.target)
}

function onDocumentPointerDown(event: PointerEvent): void {
  if (open.value && !isInside(event)) close()
}

function onDocumentKeydown(event: KeyboardEvent): void {
  if (!open.value || event.key !== 'Escape') return
  close()
  // 面板打开时拦下 Esc：模态弹窗（如转发弹窗）在 window 级监听 Esc，
  // document 阶段先行冒泡，stopImmediatePropagation 可避免一次 Esc 同时关闭面板与弹窗
  event.stopImmediatePropagation()
}

function onReposition(): void {
  if (open.value) updatePosition()
}

watch(open, (value) => emit('open-change', value))

onMounted(() => {
  document.addEventListener('pointerdown', onDocumentPointerDown)
  document.addEventListener('keydown', onDocumentKeydown)
  // capture 捕获任意容器内的滚动（页面、弹窗正文等），保持面板跟随触发钮
  document.addEventListener('scroll', onReposition, { capture: true, passive: true })
  window.addEventListener('resize', onReposition)
})

onBeforeUnmount(() => {
  document.removeEventListener('pointerdown', onDocumentPointerDown)
  document.removeEventListener('keydown', onDocumentKeydown)
  document.removeEventListener('scroll', onReposition, { capture: true })
  window.removeEventListener('resize', onReposition)
  picker?.remove()
  picker = null
})
</script>

<template>
  <div ref="root" class="relative inline-flex">
    <slot name="trigger" :toggle="toggle" :open="open" />
    <div
      v-if="everOpened"
      v-show="open"
      class="fixed z-[70] overflow-hidden rounded-[10px] border border-solid border-border bg-surface shadow-[var(--shadow-pop)]"
      :style="{ left: `${pos.left}px`, top: `${pos.top}px`, width: `${PANEL_WIDTH}px`, height: `${PANEL_HEIGHT}px` }"
      role="dialog"
      aria-label="选择表情"
    >
      <div ref="mountPoint" class="h-full w-full"></div>
      <div v-if="loading" class="absolute inset-0 flex items-center justify-center bg-surface">
        <span :class="ui.spinner" aria-hidden="true"></span>
      </div>
    </div>
  </div>
</template>
