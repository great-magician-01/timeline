<script setup lang="ts">
import { RouterLink, RouterView } from 'vue-router'
import { ui } from '../../styles/ui'
</script>

<template>
  <div :class="[ui.narrow]">
    <div :class="[ui.stack]">
      <header :class="[ui.pageHead]">
        <h1 :class="[ui.pageTitle]">设置</h1>
        <p :class="[ui.pageDesc]">管理个人资料、账号安全与隐私偏好。</p>
      </header>

      <nav :class="[ui.tabs]" aria-label="设置分区">
        <RouterLink to="/settings/profile" :class="[ui.tabLink]" :exact-active-class="ui.tabLinkActive">编辑资料</RouterLink>
        <RouterLink to="/settings/account" :class="[ui.tabLink]" :exact-active-class="ui.tabLinkActive">账号与安全</RouterLink>
      </nav>

      <RouterView />
    </div>
  </div>
</template>
