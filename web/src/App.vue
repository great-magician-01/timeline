<script setup lang="ts">
import { computed, onUnmounted, watch } from 'vue'
import { RouterLink, RouterView, useRoute } from 'vue-router'
import AppIcon from './components/AppIcon.vue'
import BrandMark from './components/BrandMark.vue'
import UserAvatar from './components/UserAvatar.vue'
import type { IconName } from './components/icons'
import ThemePicker from './components/ThemePicker.vue'
import { authState } from './stores/auth'
import { startRealtime, stopRealtime } from './composables/realtime'
import { messageState, startMessagePolling, stopMessagePolling } from './stores/messages'
import { groupState, startGroupPolling, stopGroupPolling } from './stores/groups'
import { notificationState, startNotificationPolling, stopNotificationPolling } from './stores/notifications'
import { registrationOpen } from './stores/site'

interface NavEntry {
  label: string
  to: string
  icon: IconName
}

const route = useRoute()

// 通知、私信与群聊未读轮询只对登录用户开启（三个独立接口、独立徽章）；
// SSE 实时信号与轮询并存：信号降低延迟，轮询作为断线兜底
watch(
  () => authState.user?.id,
  (id) => {
    if (id) {
      startNotificationPolling()
      startMessagePolling()
      startGroupPolling()
      startRealtime()
    } else {
      stopNotificationPolling()
      stopMessagePolling()
      stopGroupPolling()
      stopRealtime()
    }
  },
  { immediate: true },
)
onUnmounted(() => {
  stopNotificationPolling()
  stopMessagePolling()
  stopGroupPolling()
  stopRealtime()
})

/** 主导航未读徽章：通知、私信与群聊各自独立 */
function navBadge(to: string): number {
  if (to === '/notifications') return notificationState.unread
  if (to === '/messages') return messageState.unread
  if (to === '/groups') return groupState.unread
  return 0
}

const user = computed(() => authState.user)
const isAdmin = computed(() => user.value?.role === 'admin')

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
      { label: '私信', to: '/messages', icon: 'message' },
      { label: '群聊', to: '/groups', icon: 'users' },
      { label: '关注申请', to: '/follow-requests', icon: 'user-plus' },
      { label: '我的收藏', to: '/bookmarks', icon: 'bookmark' },
      { label: '草稿', to: '/drafts', icon: 'file-text' },
      { label: '定时发布', to: '/scheduled', icon: 'calendar' },
      { label: '我的主页', to: `/users/${user.value.username}`, icon: 'user' },
      { label: '设置', to: '/settings/profile', icon: 'sliders' },
    )
  } else {
    items.push({ label: '登录', to: '/login', icon: 'user' })
    // 注册关闭时隐藏入口；仍保留登录（docs/PAGES_AND_PERMISSIONS.md §3.1）
    // 空库尚未初始化时，即使关闭注册也要保留入口，保证首个超级管理员能完成初始化
    if (registrationOpen.value) {
      items.push({ label: '注册', to: '/register', icon: 'user-plus' })
    }
  }
  return items
})

const adminNav = computed<NavEntry[]>(() =>
  isAdmin.value
    ? [
        { label: '管理概览', to: '/admin/overview', icon: 'gauge' },
        { label: '用户管理', to: '/admin/users', icon: 'users' },
        { label: '内容审核', to: '/admin/content/posts', icon: 'file-text' },
        { label: '附件管理', to: '/admin/content/attachments', icon: 'image' },
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
    { label: '私信', to: '/messages', icon: 'message' },
  ]
  // 底部标签栏最多 6 项：群聊入口并入私信页顶部的次级导航，移动端从侧栏进入
  if (isAdmin.value) items.push({ label: '管理', to: '/admin/overview', icon: 'shield' })
  return items
})

function isActive(to: string): boolean {
  if (to === '/') return route.path === '/'
  return route.path === to || route.path.startsWith(`${to}/`)
}
</script>

<template>
  <div>
    <!-- 壳宽度：宽屏约 3/5 视口（上限 1760px），中等屏保持 940px；移动端由 shell-down 改为单列全宽 -->
    <div class="mx-auto grid w-full max-w-[clamp(940px,60vw,1760px)] grid-cols-[236px_minmax(0,1fr)] items-start gap-5 px-4 shell-down:block shell-down:max-w-none shell-down:p-0">
      <aside class="sticky top-0 flex h-screen flex-col pb-3 pt-[14px] shell-down:hidden">
        <div class="flex items-center gap-2 px-[10px] py-[6px]">
          <RouterLink to="/" class="flex min-w-0 items-center gap-[10px] text-ink hover:no-underline" aria-label="时间线首页">
            <BrandMark class="size-[30px]" />
            <span class="text-[17px] font-extrabold tracking-[0.04em]">时间线</span>
          </RouterLink>
          <ThemePicker class="ml-auto" />
        </div>

        <nav class="mt-[14px] flex min-h-0 flex-col gap-[2px] overflow-y-auto" aria-label="主导航">
          <RouterLink
            v-for="item in mainNav"
            :key="item.to"
            :to="item.to"
            :class="[
              'flex items-center gap-3 rounded-[10px] px-3 py-[9px] text-[15px] font-semibold text-ink-2 transition-[background,color] duration-150 ease-[ease] hover:bg-hover hover:text-ink hover:no-underline',
              isActive(item.to) ? 'bg-soft! text-ink! font-bold!' : '',
            ]"
          >
            <AppIcon :name="item.icon" :size="21" />
            <span>{{ item.label }}</span>
            <span
              v-if="navBadge(item.to) > 0"
              class="ml-auto min-w-[20px] rounded-[999px] bg-primary px-[6px] text-center text-[11.5px] font-bold leading-[20px] text-on-media"
              >{{ navBadge(item.to) > 99 ? '99+' : navBadge(item.to) }}</span
            >
          </RouterLink>

          <template v-if="adminNav.length > 0">
            <p class="mx-3 mb-[6px] mt-4 text-[12px] font-bold tracking-[0.06em] text-ink-3">管理</p>
            <RouterLink
              v-for="item in adminNav"
              :key="item.to"
              :to="item.to"
              :class="[
                'flex items-center gap-3 rounded-[10px] px-3 py-[9px] text-[15px] font-semibold text-ink-2 transition-[background,color] duration-150 ease-[ease] hover:bg-hover hover:text-ink hover:no-underline',
                isActive(item.to) ? 'bg-soft! text-ink! font-bold!' : '',
              ]"
            >
              <AppIcon :name="item.icon" :size="21" />
              <span>{{ item.label }}</span>
            </RouterLink>
          </template>
        </nav>

        <div class="mt-auto pt-3">
          <p v-if="!authState.ready" class="mx-[10px] my-0 text-[12.5px] leading-[1.7] text-ink-3">载入中…</p>
          <RouterLink
            v-else-if="user"
            :to="`/users/${user.username}`"
            class="flex items-center gap-[10px] rounded-[12px] px-[10px] py-2 text-ink hover:bg-hover hover:no-underline"
          >
            <UserAvatar :user="user" size="sm" />
            <span class="flex min-w-0 flex-col leading-[1.35]">
              <span class="overflow-hidden text-ellipsis whitespace-nowrap text-[14.5px] font-bold">{{ user.nickname || user.username }}</span>
              <span class="text-[12.5px] text-ink-3">@{{ user.username }}</span>
            </span>
            <AppIcon name="chevron-right" :size="17" class="ml-auto text-ink-3" />
          </RouterLink>
          <p v-else class="mx-[10px] my-0 text-[12.5px] leading-[1.7] text-ink-3">
            {{
              registrationOpen
                ? '帖子、评论与 AI 功能需要登录后使用；现在可以注册账号、管理资料与账号安全。'
                : '注册暂时关闭；已有账号可以直接登录，浏览公开内容无需登录。'
            }}
          </p>
        </div>
      </aside>

      <header
        class="hidden shell-down:sticky shell-down:top-0 shell-down:z-30 shell-down:flex shell-down:h-[50px] shell-down:items-center shell-down:gap-[10px] shell-down:border-b shell-down:border-solid shell-down:border-border shell-down:bg-glass shell-down:px-[14px] shell-down:backdrop-blur-[6px]"
      >
        <RouterLink
          to="/"
          class="flex min-w-0 items-center gap-[10px] text-ink hover:no-underline shell-down:mr-auto"
          aria-label="时间线首页"
        >
          <BrandMark class="size-[30px] shell-down:size-[26px]" />
          <span class="text-[17px] font-extrabold tracking-[0.04em] shell-down:text-[16px]">时间线</span>
        </RouterLink>
        <RouterLink
          v-if="user"
          :to="`/users/${user.username}`"
          class="shell-down:flex"
          :aria-label="`我的主页：${user.nickname || user.username}`"
        >
          <UserAvatar :user="user" size="sm" />
        </RouterLink>
        <ThemePicker class="ml-auto shell-down:ml-0" />
      </header>

      <main class="min-w-0 pb-[72px] pt-4 shell-down:px-3 shell-down:pb-[84px] shell-down:pt-3">
        <RouterView />
      </main>
    </div>

    <nav
      class="hidden shell-down:fixed shell-down:inset-x-0 shell-down:bottom-0 shell-down:z-40 shell-down:flex shell-down:border-t shell-down:border-solid shell-down:border-border shell-down:bg-glass shell-down:px-1 shell-down:pb-[calc(6px_+_env(safe-area-inset-bottom,0px))] shell-down:pt-[6px] shell-down:backdrop-blur-[8px]"
      aria-label="底部导航"
    >
      <RouterLink
        v-for="item in tabNav"
        :key="item.to"
        :to="item.to"
        :class="[
          'flex flex-1 flex-col items-center gap-[2px] py-1 text-[10.5px] text-ink-3 hover:no-underline',
          isActive(item.to) ? 'text-primary! font-bold' : '',
        ]"
      >
        <span class="relative">
          <AppIcon :name="item.icon" :size="22" />
          <span
            v-if="item.to === '/messages' && messageState.unread > 0"
            class="absolute -right-1 -top-0.5 size-2 rounded-full bg-primary"
            aria-hidden="true"
          ></span>
        </span>
        <span>{{ item.label }}</span>
      </RouterLink>
    </nav>
  </div>
</template>
