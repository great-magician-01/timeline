<script setup lang="ts">
// 主题选择器（docs/DESIGN_SYSTEM.md §8）：调色板幽灵按钮 + 右对齐浮层，
// 交互与 MoreMenu 一致：点击菜单外任意位置或按 Esc 收起，Esc 收起后焦点回到触发按钮。
// 预览色点是主题注册表的元数据，经内联 style 渲染，不进入组件 CSS。
import { onBeforeUnmount, onMounted, ref } from 'vue'
import AppIcon from './AppIcon.vue'
import { useTheme } from '../composables/theme'

const { theme, themes, setTheme } = useTheme()

const open = ref(false)
const root = ref<HTMLElement | null>(null)
const trigger = ref<HTMLButtonElement | null>(null)

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
      aria-haspopup="menu"
      aria-label="选择主题"
      :aria-expanded="open"
      title="选择主题"
      class="flex size-[34px] items-center justify-center rounded-[8px] text-ink-3 transition-[background,color] duration-150 ease-[ease] hover:bg-hover hover:text-ink"
      @click="open = !open"
    >
      <AppIcon name="palette" :size="19" />
    </button>
    <div
      v-if="open"
      role="menu"
      class="absolute top-[38px] right-0 z-20 min-w-[176px] rounded-[10px] border border-solid border-border bg-surface p-[6px] shadow-[var(--shadow-pop)]"
      @click="open = false"
    >
      <button
        v-for="item in themes"
        :key="item.id"
        type="button"
        role="menuitemradio"
        :aria-checked="item.id === theme"
        class="flex w-full items-center gap-[9px] rounded-[8px] px-[10px] py-[7px] text-left text-[13.5px] text-ink hover:bg-hover hover:no-underline"
        @click="setTheme(item.id)"
      >
        <span class="flex flex-none items-center gap-[3px]" aria-hidden="true">
          <span
            v-for="(color, index) in item.swatch"
            :key="index"
            class="size-[12px] rounded-full border border-solid border-border"
            :style="{ backgroundColor: color }"
          ></span>
        </span>
        <span class="min-w-0 flex-1">{{ item.label }}</span>
        <AppIcon v-if="item.id === theme" name="check" :size="15" class="flex-none text-primary" />
      </button>
    </div>
  </div>
</template>
