<script setup lang="ts">
// 通用“更多”下拉菜单：幽灵图标触发 + 右对齐浮层，菜单项由调用方通过默认插槽提供。
// 交互与 PostActionMenu 一致：点击菜单外任意位置或按 Esc 收起，Esc 收起后焦点回到触发按钮。
import { onBeforeUnmount, onMounted, ref } from 'vue'
import AppIcon from './AppIcon.vue'

withDefaults(
  defineProps<{
    /** 触发按钮的无障碍名称 */
    label?: string
  }>(),
  { label: '更多操作' },
)

const open = ref(false)
const root = ref<HTMLElement | null>(null)
const trigger = ref<HTMLButtonElement | null>(null)

/** 菜单项样式统一由这里提供，调用方用插槽属性 itemClass 应用到链接或按钮上 */
const itemClass =
  'flex items-center gap-2 rounded-[8px] px-[10px] py-[7px] text-left text-[13.5px] text-ink hover:bg-hover hover:no-underline'

function isInside(event: Event): boolean {
  const element = root.value
  return element !== null && event.target instanceof Node && element.contains(event.target)
}

function onDocumentPointerDown(event: PointerEvent): void {
  if (open.value && !isInside(event)) open.value = false
}

function onDocumentKeydown(event: KeyboardEvent): void {
  if (!open.value || event.key !== 'Escape') return
  open.value = false
  // Esc 收起后把焦点交还触发按钮，键盘用户不会丢失位置
  trigger.value?.focus()
}

onMounted(() => {
  document.addEventListener('pointerdown', onDocumentPointerDown)
  document.addEventListener('keydown', onDocumentKeydown)
})

onBeforeUnmount(() => {
  document.removeEventListener('pointerdown', onDocumentPointerDown)
  document.removeEventListener('keydown', onDocumentKeydown)
})
</script>

<template>
  <div ref="root" class="relative">
    <button
      ref="trigger"
      type="button"
      :aria-label="label"
      :aria-expanded="open"
      :title="label"
      class="flex size-[34px] items-center justify-center rounded-[8px] text-ink-3 hover:bg-hover hover:text-ink"
      @click="open = !open"
    >
      <AppIcon name="more" :size="18" />
    </button>
    <div
      v-if="open"
      class="absolute top-[38px] right-0 z-20 grid min-w-[160px] rounded-[10px] border border-solid border-border bg-surface p-[6px] shadow-[var(--shadow-pop)]"
      @click="open = false"
    >
      <slot :item-class="itemClass" />
    </div>
  </div>
</template>
