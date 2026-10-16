<script setup lang="ts">
import { computed, ref, watch } from 'vue'

interface AvatarUser {
  username: string
  nickname?: string | null
  avatar_url?: string | null
  is_ai?: boolean
}

const props = withDefaults(defineProps<{ user: AvatarUser; size?: 'sm' | 'md' | 'lg' }>(), { size: 'md' })

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
    class="avatar"
    :class="[`avatar--${props.size}`, { 'avatar--ai': props.user.is_ai }]"
    aria-hidden="true"
  >
    <img v-if="showImage" :src="props.user.avatar_url ?? ''" alt="" @error="imageFailed = true" />
    <template v-else>{{ letter }}</template>
  </span>
</template>

<style scoped>
.avatar {
  display: flex;
  align-items: center;
  justify-content: center;
  flex: none;
  border-radius: 50%;
  background: var(--avatar-bg);
  color: var(--avatar-fg);
  font-weight: 700;
  overflow: hidden;
  user-select: none;
}
.avatar--ai {
  background: var(--violet-soft);
  color: var(--violet);
}
.avatar img {
  display: block;
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.avatar--sm {
  width: 32px;
  height: 32px;
  font-size: 13.5px;
}
.avatar--md {
  width: 40px;
  height: 40px;
  font-size: 15px;
}
.avatar--lg {
  width: 64px;
  height: 64px;
  font-size: 24px;
}
</style>
