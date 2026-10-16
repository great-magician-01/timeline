<script setup lang="ts">
import { RouterLink, RouterView } from 'vue-router'
</script>

<template>
  <div class="constrain">
    <div class="stack">
      <header class="page-head">
        <h1 class="page-title">设置</h1>
        <p class="page-desc">管理个人资料、账号安全与隐私偏好。</p>
      </header>

      <nav class="tabs" aria-label="设置分区">
        <RouterLink to="/settings/profile" class="tab-link" exact-active-class="is-active">编辑资料</RouterLink>
        <RouterLink to="/settings/account" class="tab-link" exact-active-class="is-active">账号与安全</RouterLink>
      </nav>

      <RouterView />
    </div>
  </div>
</template>
