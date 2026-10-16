<script setup lang="ts">
// 表单字段包装：统一 label、提示与错误展示
defineProps<{
  label: string
  inputId: string
  hint?: string
  error?: string
}>()
</script>

<template>
  <div class="field">
    <label class="field-label" :for="inputId">{{ label }}</label>
    <slot />
    <p v-if="error" class="field-error">{{ error }}</p>
    <p v-else-if="hint" class="field-hint">{{ hint }}</p>
  </div>
</template>
