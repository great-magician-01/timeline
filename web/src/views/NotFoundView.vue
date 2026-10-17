<script setup lang="ts">
import { RouterLink } from 'vue-router'
import AppIcon from '../components/AppIcon.vue'
import { authState } from '../stores/auth'
import { ui } from '../styles/ui'

function goBack(): void {
  if (window.history.length > 1) {
    window.history.back()
  }
}
</script>

<template>
  <div :class="[ui.constrain]">
    <section :class="[ui.card, ui.cardPad]">
      <div :class="[ui.empty]">
        <AppIcon name="alert" :size="26" :class="[ui.emptyIcon]" />
        <strong :class="[ui.emptyStrong]">页面不存在</strong>
        <p :class="[ui.emptyText]">你访问的地址无效，或页面已被移除。</p>
      </div>
      <div class="flex flex-wrap justify-center gap-2">
        <RouterLink :class="[ui.button, ui.buttonPrimary]" to="/">返回首页</RouterLink>
        <button :class="[ui.button, ui.buttonSecondary]" type="button" @click="goBack">返回上一页</button>
        <RouterLink v-if="authState.user" :class="[ui.button, ui.buttonSecondary]" to="/settings/profile">账号设置</RouterLink>
      </div>
    </section>
  </div>
</template>
