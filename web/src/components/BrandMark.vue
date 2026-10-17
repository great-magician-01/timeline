<script setup lang="ts">
// 品牌标志：直接内联 demo/logo.svg（黑白手绘「潦草印章」），为设计系统 §7 规定的品牌源图，
// 是唯一允许保留原始黑白取值的图形资产。
</script>

<template>
  <svg class="block" viewBox="0 0 64 64" role="img" aria-label="时间线">
    <rect width="64" height="64" rx="14" fill="#fff" />
    <path
      d="M9.5 12.5C17.5 10 46.5 10 53.5 12.8C55.8 20.5 55.8 43.5 53.8 50.8C46 53.5 18 53.5 10.8 50.5C8.6 43.5 8.6 20.5 9.5 12.5Z"
      fill="none"
      stroke="#111"
      stroke-width="4.5"
      stroke-linecap="round"
      stroke-linejoin="round"
    />
    <g
      transform="rotate(-2 32 32)"
      fill="none"
      stroke="#111"
      stroke-width="4"
      stroke-linecap="round"
      stroke-linejoin="round"
    >
      <path
        d="M21.2 21C25.4 20.3 29.1 20.3 31.3 21C32.1 25.4 32.1 33.6 31.3 37.9C27.6 38.6 23.9 38.6 21.2 37.9C20.3 33.6 20.3 25.4 21.2 21Z"
      />
      <path d="M21.4 29.6C24.4 29 27.8 29 30.9 29.6" />
      <path d="M33.8 24.2C37.8 23.4 42.8 23.4 47.2 24.1" />
      <path d="M41.8 17C42.6 24.6 42.6 35.4 41.8 44.6C39.6 45.3 37.1 44.4 36.2 41.9" />
      <circle cx="36.4" cy="33.2" r="2.4" fill="#111" stroke="none" />
    </g>
  </svg>
</template>
