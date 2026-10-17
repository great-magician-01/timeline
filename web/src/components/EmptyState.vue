<script setup lang="ts">
// 空状态与错误占位（docs/PAGES_AND_PERMISSIONS.md E-01/E-02）
import AppIcon from './AppIcon.vue'
import type { IconName } from './icons'
import { ui } from '../styles/ui'

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
  <div :class="ui.empty">
    <AppIcon :name="icon" :size="26" :class="ui.emptyIcon" />
    <strong :class="ui.emptyStrong">{{ title }}</strong>
    <p v-if="description" :class="ui.emptyText">{{ description }}</p>
    <slot />
  </div>
</template>
