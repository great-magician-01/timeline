<script setup lang="ts">
// 表单字段包装：统一 label、提示与错误展示
import { ui } from '../styles/ui'

defineProps<{
  label: string
  inputId: string
  hint?: string
  error?: string
}>()
</script>

<template>
  <div :class="ui.field">
    <label :class="ui.fieldLabel" :for="inputId">{{ label }}</label>
    <slot />
    <p v-if="error" :class="ui.fieldError">{{ error }}</p>
    <p v-else-if="hint" :class="ui.fieldHint">{{ hint }}</p>
  </div>
</template>
