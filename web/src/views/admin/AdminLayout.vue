<script setup lang="ts">
import { RouterLink, RouterView, useRoute } from 'vue-router'
import AppIcon from '../../components/AppIcon.vue'
import type { IconName } from '../../components/icons'

interface NavItem {
  label: string
  to: string
  icon: IconName
}

const route = useRoute()

// 管理分区导航（docs/PAGES_AND_PERMISSIONS.md §4.3 A-01..A-19）
const tabs: NavItem[] = [
  { label: '管理概览', to: '/admin/overview', icon: 'gauge' },
  { label: '用户管理', to: '/admin/users', icon: 'users' },
  { label: '帖子审核', to: '/admin/content/posts', icon: 'file-text' },
  { label: '评论审核', to: '/admin/content/comments', icon: 'message' },
  { label: '模型预设', to: '/admin/providers', icon: 'key' },
  { label: 'AI 管理', to: '/admin/ai', icon: 'sparkles' },
  { label: '全局任务', to: '/admin/tasks', icon: 'list' },
  { label: '调用记录', to: '/admin/ai-calls', icon: 'activity' },
  { label: '审计日志', to: '/admin/audit', icon: 'scroll' },
  { label: '系统设置', to: '/admin/settings', icon: 'toggle' },
]

function isActive(to: string): boolean {
  return route.path === to || route.path.startsWith(`${to}/`)
}
</script>

<template>
  <div class="stack">
    <header class="page-head">
      <h1 class="page-title">管理后台</h1>
      <p class="page-desc">仅管理员可见；所有管理操作都会由后端再次校验权限。</p>
    </header>

    <nav class="tabs admin-tabs" aria-label="管理分区">
      <RouterLink
        v-for="tab in tabs"
        :key="tab.to"
        :to="tab.to"
        class="tab-link"
        :class="{ 'is-active': isActive(tab.to) }"
      >
        <AppIcon :name="tab.icon" :size="16" />
        <span>{{ tab.label }}</span>
      </RouterLink>
    </nav>

    <RouterView />
  </div>
</template>

<style scoped>
/* 分区较多：允许换行，避免窄屏横向溢出 */
.admin-tabs {
  flex-wrap: wrap;
}
.tab-link {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
</style>
