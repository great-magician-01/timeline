<script setup lang="ts">
import { computed } from 'vue'
import AppIcon from './AppIcon.vue'
import type { IconName } from './icons'
import { ui } from '../styles/ui'

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
  <p :class="[ui.alert, ui.alertVariants[props.kind].root]">
    <AppIcon
      :name="iconName"
      :size="17"
      :class="[ui.alertIcon, ui.alertVariants[props.kind].icon]"
    />
    <span :class="ui.alertText"><slot /></span>
  </p>
</template>
