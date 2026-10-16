<script setup lang="ts">
import { computed } from 'vue'
import AppIcon from './AppIcon.vue'
import type { IconName } from './icons'

const props = withDefaults(defineProps<{ kind?: 'error' | 'success' | 'info' | 'warning' }>(), {
  kind: 'info',
})

const iconName = computed<IconName>(() => {
  if (props.kind === 'success') return 'check-circle'
  if (props.kind === 'info') return 'info'
  return 'alert'
})
</script>

<template>
  <p class="alert" :class="`alert--${props.kind}`">
    <AppIcon :name="iconName" :size="17" />
    <span class="alert-text"><slot /></span>
  </p>
</template>
