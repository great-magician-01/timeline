<script setup lang="ts">
// 空状态与错误占位（docs/PAGES_AND_PERMISSIONS.md E-01/E-02）
import AppIcon from './AppIcon.vue'
import type { IconName } from './icons'

withDefaults(
  defineProps<{
    icon?: IconName
    title: string
    description?: string
  }>(),
  { icon: 'info' },
)
</script>

<template>
  <div class="empty">
    <AppIcon :name="icon" :size="26" />
    <strong>{{ title }}</strong>
    <p v-if="description">{{ description }}</p>
    <slot />
  </div>
</template>
