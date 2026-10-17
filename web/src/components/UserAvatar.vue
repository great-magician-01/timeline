<script setup lang="ts">
import { computed, ref, watch } from 'vue'

interface AvatarUser {
  username: string
  nickname?: string | null
  avatar_url?: string | null
  is_ai?: boolean
}

const props = withDefaults(defineProps<{ user: AvatarUser; size?: 'sm' | 'md' | 'lg' }>(), { size: 'md' })

const sizeClasses: Record<'sm' | 'md' | 'lg', string> = {
  sm: 'size-8 text-[13.5px]',
  md: 'size-10 text-[15px]',
  lg: 'size-16 text-[24px]',
}

// 本阶段没有头像上传：优先展示接口返回的 avatar_url，加载失败或为空时退回昵称首字母默认头像
const imageFailed = ref(false)
watch(
  () => props.user.avatar_url,
  () => {
    imageFailed.value = false
  },
)

const showImage = computed(() => Boolean(props.user.avatar_url) && !imageFailed.value)

const letter = computed(() => {
  const base = (props.user.nickname || props.user.username || '').trim()
  return Array.from(base)[0]?.toUpperCase() ?? '?'
})
</script>

<template>
  <span
    :class="[
      'flex flex-none items-center justify-center overflow-hidden rounded-[50%] bg-avatar-bg font-bold text-avatar-fg select-none',
      sizeClasses[props.size],
      props.user.is_ai ? 'bg-violet-soft! text-violet!' : '',
    ]"
    aria-hidden="true"
  >
    <img
      v-if="showImage"
      :src="props.user.avatar_url ?? ''"
      alt=""
      class="block size-full object-cover"
      @error="imageFailed = true"
    />
    <template v-else>{{ letter }}</template>
  </span>
</template>
