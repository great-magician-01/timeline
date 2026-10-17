<script setup lang="ts">
import { computed } from 'vue'
import { icons } from './icons'
import type { IconName } from './icons'

const props = withDefaults(defineProps<{ name: IconName; size?: number }>(), { size: 18 })

const shape = computed(() => icons[props.name])
</script>

<template>
  <svg
    class="block flex-none"
    :width="props.size"
    :height="props.size"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="1.8"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
    focusable="false"
  >
    <path v-for="(d, index) in shape.paths ?? []" :key="`p-${index}`" :d="d" />
    <circle
      v-for="(circle, index) in shape.circles ?? []"
      :key="`c-${index}`"
      :cx="circle[0]"
      :cy="circle[1]"
      :r="circle[2]"
    />
    <rect
      v-for="(rect, index) in shape.rects ?? []"
      :key="`r-${index}`"
      :x="rect[0]"
      :y="rect[1]"
      :width="rect[2]"
      :height="rect[3]"
      :rx="rect[4]"
    />
  </svg>
</template>
