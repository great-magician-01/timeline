<script setup lang="ts">
import { RouterLink } from 'vue-router'
import AppIcon from '../components/AppIcon.vue'
import { authState } from '../stores/auth'

function goBack(): void {
  if (window.history.length > 1) {
    window.history.back()
  }
}
</script>

<template>
  <div class="constrain">
    <section class="card card--pad">
      <div class="empty">
        <AppIcon name="alert" :size="26" />
        <strong>页面不存在</strong>
        <p>你访问的地址无效，或页面已被移除。</p>
      </div>
      <div class="actions">
        <RouterLink class="btn btn--primary" to="/">返回首页</RouterLink>
        <button class="btn btn--secondary" type="button" @click="goBack">返回上一页</button>
        <RouterLink v-if="authState.user" class="btn btn--secondary" to="/settings/profile">账号设置</RouterLink>
      </div>
    </section>
  </div>
</template>

<style scoped>
.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  justify-content: center;
}
</style>
