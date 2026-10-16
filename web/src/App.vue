<script setup lang="ts">
import { computed, onUnmounted, watch } from 'vue'
import { RouterLink, RouterView, useRoute } from 'vue-router'
import AppIcon from './components/AppIcon.vue'
import BrandMark from './components/BrandMark.vue'
import UserAvatar from './components/UserAvatar.vue'
import type { IconName } from './components/icons'
import { useTheme } from './composables/theme'
import { authState } from './stores/auth'
import { notificationState, startNotificationPolling, stopNotificationPolling } from './stores/notifications'

interface NavEntry {
  label: string
  to: string
  icon: IconName
}

const { theme, toggleTheme } = useTheme()
const route = useRoute()

// 未读通知轮询只对登录用户开启
watch(
  () => authState.user?.id,
  (id) => {
    if (id) startNotificationPolling()
    else stopNotificationPolling()
  },
  { immediate: true },
)
onUnmounted(stopNotificationPolling)

const user = computed(() => authState.user)
const isAdmin = computed(() => user.value?.role === 'admin')
const themeLabel = computed(() => (theme.value === 'dark' ? '切换到浅色主题' : '切换到深色主题'))

// 导航与 docs/PAGES_AND_PERMISSIONS.md §3 一致：游客只有时间线、搜索、登录与注册
const mainNav = computed<NavEntry[]>(() => {
  const items: NavEntry[] = [
    { label: '首页', to: '/', icon: 'home' },
    { label: '搜索', to: '/search', icon: 'search' },
  ]
  if (user.value) {
    items.splice(1, 0, { label: '关注', to: '/following', icon: 'users' })
    items.push(
      { label: '发布', to: '/compose', icon: 'pencil' },
      { label: '通知', to: '/notifications', icon: 'bell' },
      { label: '关注申请', to: '/follow-requests', icon: 'user-plus' },
      { label: '草稿', to: '/drafts', icon: 'file-text' },
      { label: '定时发布', to: '/scheduled', icon: 'calendar' },
      { label: '我的主页', to: `/users/${user.value.username}`, icon: 'user' },
      { label: '设置', to: '/settings/profile', icon: 'sliders' },
    )
  } else {
    items.push({ label: '登录', to: '/login', icon: 'user' })
    items.push({ label: '注册', to: '/register', icon: 'user-plus' })
  }
  return items
})

const adminNav = computed<NavEntry[]>(() =>
  isAdmin.value
    ? [
        { label: '管理概览', to: '/admin/overview', icon: 'gauge' },
        { label: '用户管理', to: '/admin/users', icon: 'users' },
        { label: '内容审核', to: '/admin/content/posts', icon: 'file-text' },
        { label: '模型预设', to: '/admin/providers', icon: 'key' },
        { label: 'AI 管理', to: '/admin/ai', icon: 'sparkles' },
        { label: '全局任务', to: '/admin/tasks', icon: 'list' },
        { label: '调用记录', to: '/admin/ai-calls', icon: 'activity' },
        { label: '审计日志', to: '/admin/audit', icon: 'scroll' },
        { label: '系统设置', to: '/admin/settings', icon: 'toggle' },
      ]
    : [],
)

const tabNav = computed<NavEntry[]>(() => {
  if (!user.value) return mainNav.value
  const items: NavEntry[] = [
    { label: '首页', to: '/', icon: 'home' },
    { label: '关注', to: '/following', icon: 'users' },
    { label: '搜索', to: '/search', icon: 'search' },
    { label: '发布', to: '/compose', icon: 'pencil' },
    { label: '通知', to: '/notifications', icon: 'bell' },
  ]
  if (isAdmin.value) items.push({ label: '管理', to: '/admin/overview', icon: 'shield' })
  return items
})

function isActive(to: string): boolean {
  if (to === '/') return route.path === '/'
  return route.path === to || route.path.startsWith(`${to}/`)
}
</script>

<template>
  <div class="app">
    <div class="shell">
      <aside class="sidebar">
        <div class="side-head">
          <RouterLink to="/" class="brand" aria-label="时间线首页">
            <BrandMark class="mark" />
            <span class="wordmark">时间线</span>
          </RouterLink>
          <button class="icon-btn" type="button" :aria-label="themeLabel" @click="toggleTheme">
            <AppIcon :name="theme === 'dark' ? 'sun' : 'moon'" :size="19" />
          </button>
        </div>

        <nav class="side-nav" aria-label="主导航">
          <RouterLink
            v-for="item in mainNav"
            :key="item.to"
            :to="item.to"
            class="nav-item"
            :class="{ 'is-active': isActive(item.to) }"
          >
            <AppIcon :name="item.icon" :size="21" />
            <span class="txt">{{ item.label }}</span>
            <span
              v-if="item.to === '/notifications' && notificationState.unread > 0"
              class="nav-badge"
              >{{ notificationState.unread > 99 ? '99+' : notificationState.unread }}</span
            >
          </RouterLink>

          <template v-if="adminNav.length > 0">
            <p class="nav-label">管理</p>
            <RouterLink
              v-for="item in adminNav"
              :key="item.to"
              :to="item.to"
              class="nav-item"
              :class="{ 'is-active': isActive(item.to) }"
            >
              <AppIcon :name="item.icon" :size="21" />
              <span class="txt">{{ item.label }}</span>
            </RouterLink>
          </template>
        </nav>

        <div class="side-foot">
          <p v-if="!authState.ready" class="side-note">载入中…</p>
          <RouterLink v-else-if="user" :to="`/users/${user.username}`" class="me">
            <UserAvatar :user="user" size="sm" />
            <span class="me-info">
              <span class="me-name">{{ user.nickname || user.username }}</span>
              <span class="me-handle">@{{ user.username }}</span>
            </span>
            <AppIcon name="chevron-right" :size="17" class="me-arrow" />
          </RouterLink>
          <p v-else class="side-note">
            帖子、评论与 AI 功能尚未开放；现在可以注册账号、管理资料与账号安全。
          </p>
        </div>
      </aside>

      <header class="mtopbar">
        <RouterLink to="/" class="brand" aria-label="时间线首页">
          <BrandMark class="mark" />
          <span class="wordmark">时间线</span>
        </RouterLink>
        <RouterLink
          v-if="user"
          :to="`/users/${user.username}`"
          class="mtop-me"
          :aria-label="`我的主页：${user.nickname || user.username}`"
        >
          <UserAvatar :user="user" size="sm" />
        </RouterLink>
        <button class="icon-btn" type="button" :aria-label="themeLabel" @click="toggleTheme">
          <AppIcon :name="theme === 'dark' ? 'sun' : 'moon'" :size="19" />
        </button>
      </header>

      <main class="main">
        <RouterView />
      </main>
    </div>

    <nav class="tabbar" aria-label="底部导航">
      <RouterLink
        v-for="item in tabNav"
        :key="item.to"
        :to="item.to"
        class="tab"
        :class="{ 'is-active': isActive(item.to) }"
      >
        <AppIcon :name="item.icon" :size="22" />
        <span>{{ item.label }}</span>
      </RouterLink>
    </nav>
  </div>
</template>

<style scoped>
.shell {
  display: grid;
  grid-template-columns: 236px minmax(0, 1fr);
  gap: 20px;
  align-items: start;
  max-width: 940px;
  margin: 0 auto;
  padding: 0 16px;
}

/* ---------- 桌面侧栏 ---------- */
.sidebar {
  position: sticky;
  top: 0;
  display: flex;
  flex-direction: column;
  height: 100vh;
  padding: 14px 0 12px;
}
.side-head {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 10px;
}
.brand {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  color: var(--ink);
}
.brand:hover {
  text-decoration: none;
}
.mark {
  width: 30px;
  height: 30px;
}
.wordmark {
  font-size: 17px;
  font-weight: 800;
  letter-spacing: 0.04em;
}
.icon-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 34px;
  height: 34px;
  margin-left: auto;
  border-radius: 8px;
  color: var(--ink-3);
  transition: background 0.15s ease, color 0.15s ease;
}
.icon-btn:hover {
  background: var(--hover);
  color: var(--ink);
}
.side-nav {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin-top: 14px;
  /* 导航项较多或窗口较矮时允许侧栏内部滚动，避免被 100vh 裁掉 */
  min-height: 0;
  overflow-y: auto;
}
.nav-item {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 9px 12px;
  border-radius: 10px;
  font-size: 15px;
  font-weight: 600;
  color: var(--ink-2);
  transition: background 0.15s ease, color 0.15s ease;
}
.nav-item:hover {
  background: var(--hover);
  color: var(--ink);
  text-decoration: none;
}
.nav-item.is-active {
  background: var(--soft);
  color: var(--ink);
  font-weight: 700;
}
.nav-badge {
  margin-left: auto;
  min-width: 20px;
  padding: 0 6px;
  border-radius: 999px;
  background: var(--primary);
  color: var(--on-media);
  font-size: 11.5px;
  font-weight: 700;
  line-height: 20px;
  text-align: center;
}
.nav-label {
  margin: 16px 12px 6px;
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.06em;
  color: var(--ink-3);
}
.side-foot {
  margin-top: auto;
  padding-top: 12px;
}
.side-note {
  margin: 0 10px;
  font-size: 12.5px;
  line-height: 1.7;
  color: var(--ink-3);
}
.me {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 10px;
  border-radius: 12px;
  color: var(--ink);
}
.me:hover {
  background: var(--hover);
  text-decoration: none;
}
.me-info {
  display: flex;
  flex-direction: column;
  min-width: 0;
  line-height: 1.35;
}
.me-name {
  font-size: 14.5px;
  font-weight: 700;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.me-handle {
  font-size: 12.5px;
  color: var(--ink-3);
}
.me-arrow {
  margin-left: auto;
  color: var(--ink-3);
}

/* ---------- 内容列 ---------- */
.main {
  min-width: 0;
  padding: 16px 0 72px;
}
.mtopbar {
  display: none;
}
.tabbar {
  display: none;
}

/* ---------- 移动端（断点 880px） ---------- */
@media (max-width: 880px) {
  .shell {
    display: block;
    max-width: none;
    padding: 0;
  }
  .sidebar {
    display: none;
  }
  .mtopbar {
    position: sticky;
    top: 0;
    z-index: 30;
    display: flex;
    align-items: center;
    gap: 10px;
    height: 50px;
    padding: 0 14px;
    background: var(--glass);
    backdrop-filter: blur(6px);
    border-bottom: 1px solid var(--border);
  }
  .mtopbar .brand {
    margin-right: auto;
  }
  .mtopbar .mark {
    width: 26px;
    height: 26px;
  }
  .mtopbar .wordmark {
    font-size: 16px;
  }
  .mtopbar .icon-btn {
    margin-left: 0;
  }
  .mtop-me {
    display: flex;
  }
  .main {
    padding: 12px 12px 84px;
  }
  .tabbar {
    position: fixed;
    right: 0;
    bottom: 0;
    left: 0;
    z-index: 40;
    display: flex;
    padding: 6px 4px calc(6px + env(safe-area-inset-bottom, 0px));
    background: var(--glass);
    backdrop-filter: blur(8px);
    border-top: 1px solid var(--border);
  }
  .tab {
    display: flex;
    flex: 1;
    flex-direction: column;
    align-items: center;
    gap: 2px;
    padding: 4px 0;
    font-size: 10.5px;
    color: var(--ink-3);
  }
  .tab:hover {
    text-decoration: none;
  }
  .tab.is-active {
    color: var(--primary);
    font-weight: 700;
  }
}
</style>
