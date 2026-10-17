<script setup lang="ts">
// 帖子“更多”操作菜单：编辑 / 历史版本 / 删除，首页卡片与帖子详情共用。
// 前端只按后端返回的 can_* 字段决定入口是否展示，实际授权仍由各个接口执行。
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import AppIcon from './AppIcon.vue'
import { apiRequest, errorMessage } from '../api/client'

const props = defineProps<{
  postId: string
  /** 作者可编辑 */
  canEdit?: boolean
  /** 有权查看当前帖子者可查看历史版本 */
  canViewHistory?: boolean
  /** 作者可删除 */
  canDelete?: boolean
}>()

const emit = defineEmits<{
  (event: 'removed', id: string): void
  (event: 'error', message: string): void
}>()

/** 没有任何可用操作时整个菜单不渲染 */
const hasActions = computed(() => Boolean(props.canEdit || props.canViewHistory || props.canDelete))

const menuOpen = ref(false)
const deleting = ref(false)
const menuRoot = ref<HTMLElement | null>(null)
const menuTrigger = ref<HTMLButtonElement | null>(null)

const menuItemClass =
  'flex items-center gap-2 rounded-[8px] px-[10px] py-[7px] text-left text-[13.5px] text-ink hover:bg-hover hover:no-underline'

// ---------- 点击菜单外任意位置或按 Esc 立即收起 ----------

/** 菜单内部的点击交给菜单项自身处理，避免确认框弹出前菜单先收起 */
function isInsideMenu(event: Event): boolean {
  const root = menuRoot.value
  return root !== null && event.target instanceof Node && root.contains(event.target)
}

function onDocumentPointerDown(event: PointerEvent): void {
  if (menuOpen.value && !isInsideMenu(event)) menuOpen.value = false
}

function onDocumentKeydown(event: KeyboardEvent): void {
  if (!menuOpen.value || event.key !== 'Escape') return
  menuOpen.value = false
  // Esc 收起后把焦点交还触发按钮，键盘用户不会丢失位置
  menuTrigger.value?.focus()
}

onMounted(() => {
  document.addEventListener('pointerdown', onDocumentPointerDown)
  document.addEventListener('keydown', onDocumentKeydown)
})

onBeforeUnmount(() => {
  document.removeEventListener('pointerdown', onDocumentPointerDown)
  document.removeEventListener('keydown', onDocumentKeydown)
})

async function removePost(): Promise<void> {
  if (!props.canDelete || deleting.value) return
  if (!window.confirm('删除后内容不再通过普通页面展示，历史版本与附件记录仍会保留。确认删除？')) return
  deleting.value = true
  try {
    await apiRequest(`/posts/${props.postId}/delete`, { method: 'POST' })
    menuOpen.value = false
    emit('removed', props.postId)
  } catch (err) {
    emit('error', errorMessage(err))
  } finally {
    deleting.value = false
  }
}
</script>

<template>
  <div v-if="hasActions" ref="menuRoot" class="relative">
    <button
      ref="menuTrigger"
      type="button"
      aria-label="更多操作"
      :aria-expanded="menuOpen"
      @click="menuOpen = !menuOpen"
    >
      <AppIcon name="more" :size="18" />
    </button>
    <div
      v-if="menuOpen"
      class="absolute right-0 top-8 z-20 grid min-w-[150px] rounded-[10px] border border-solid border-border bg-surface p-[6px] shadow-[var(--shadow-pop)]"
      @click="menuOpen = false"
    >
      <RouterLink
        v-if="canEdit"
        :to="{ name: 'post-edit', params: { postId } }"
        :class="menuItemClass"
      >
        <AppIcon name="pencil" :size="15" /> 编辑
      </RouterLink>
      <RouterLink
        v-if="canViewHistory"
        :to="{ name: 'post-history', params: { postId } }"
        :class="menuItemClass"
      >
        <AppIcon name="clock" :size="15" /> 历史版本
      </RouterLink>
      <button
        v-if="canDelete"
        type="button"
        class="flex items-center gap-2 rounded-[8px] px-[10px] py-[7px] text-left text-[13.5px] text-red hover:bg-hover"
        :disabled="deleting"
        @click="removePost"
      >
        <AppIcon name="trash" :size="15" /> 删除
      </button>
    </div>
  </div>
</template>
