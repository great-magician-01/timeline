// 路由与守卫
// 规则（docs/PAGES_AND_PERMISSIONS.md §3）：游客可访问时间线、搜索、标签与公开主页；
// 发布/草稿/通知/关注申请/设置需登录；管理后台需 role=admin；
// 已登录访问登录/注册会回到首页。
import { createRouter, createWebHistory } from 'vue-router'
import type { RouteRecordRaw } from 'vue-router'
import { authState, ensureAuthReady } from '../stores/auth'

declare module 'vue-router' {
  interface RouteMeta {
    /** 页面标题（最终拼接为「标题 · 时间线」） */
    title?: string
    /** 需要登录 */
    requiresAuth?: boolean
    /** 需要管理员角色 */
    requiresAdmin?: boolean
    /** 仅游客可访问；已登录时回到首页 */
    guestOnly?: boolean
  }
}

const routes: RouteRecordRaw[] = [
  {
    path: '/',
    name: 'home',
    component: () => import('../views/HomeView.vue'),
    meta: { title: '全站时间线' },
  },
  {
    path: '/following',
    name: 'following',
    component: () => import('../views/FollowingView.vue'),
    meta: { title: '关注', requiresAuth: true },
  },
  {
    path: '/search',
    name: 'search',
    component: () => import('../views/SearchView.vue'),
    meta: { title: '搜索' },
  },
  {
    path: '/tags/:tag',
    name: 'tag',
    component: () => import('../views/TagView.vue'),
    meta: { title: '话题' },
  },
  {
    path: '/posts/:postId',
    name: 'post-detail',
    component: () => import('../views/PostDetailView.vue'),
    meta: { title: '帖子' },
  },
  {
    path: '/posts/:postId/history',
    name: 'post-history',
    component: () => import('../views/PostHistoryView.vue'),
    meta: { title: '编辑历史' },
  },
  {
    path: '/posts/:postId/edit',
    name: 'post-edit',
    component: () => import('../views/PostEditView.vue'),
    meta: { title: '编辑帖子', requiresAuth: true },
  },
  {
    path: '/compose',
    name: 'compose',
    component: () => import('../views/ComposeView.vue'),
    meta: { title: '发布', requiresAuth: true },
  },
  {
    path: '/drafts',
    name: 'drafts',
    component: () => import('../views/DraftsView.vue'),
    meta: { title: '草稿', requiresAuth: true },
  },
  {
    path: '/drafts/:draftId/edit',
    name: 'draft-edit',
    component: () => import('../views/ComposeView.vue'),
    meta: { title: '编辑草稿', requiresAuth: true },
  },
  {
    path: '/scheduled',
    name: 'scheduled',
    component: () => import('../views/ScheduledView.vue'),
    meta: { title: '定时发布', requiresAuth: true },
  },
  {
    path: '/follow-requests',
    name: 'follow-requests',
    component: () => import('../views/FollowRequestsView.vue'),
    meta: { title: '关注申请', requiresAuth: true },
  },
  {
    path: '/notifications',
    name: 'notifications',
    component: () => import('../views/NotificationsView.vue'),
    meta: { title: '通知', requiresAuth: true },
  },
  {
    path: '/login',
    name: 'login',
    component: () => import('../views/LoginView.vue'),
    meta: { title: '登录', guestOnly: true },
  },
  {
    path: '/register',
    name: 'register',
    component: () => import('../views/RegisterView.vue'),
    meta: { title: '注册', guestOnly: true },
  },
  {
    path: '/users/:username',
    name: 'user-profile',
    component: () => import('../views/UserProfileView.vue'),
    meta: { title: '个人主页' },
  },
  {
    path: '/users/:username/followers',
    name: 'user-followers',
    component: () => import('../views/FollowersView.vue'),
    meta: { title: '粉丝' },
  },
  {
    path: '/users/:username/following',
    name: 'user-following',
    component: () => import('../views/FollowingListView.vue'),
    meta: { title: '关注' },
  },
  {
    path: '/settings',
    component: () => import('../views/settings/SettingsLayout.vue'),
    meta: { requiresAuth: true },
    children: [
      { path: '', redirect: { name: 'settings-profile' } },
      {
        path: 'profile',
        name: 'settings-profile',
        component: () => import('../views/settings/SettingsProfileView.vue'),
        meta: { title: '编辑资料' },
      },
      {
        path: 'account',
        name: 'settings-account',
        component: () => import('../views/settings/SettingsAccountView.vue'),
        meta: { title: '账号与安全' },
      },
    ],
  },
  {
    path: '/admin',
    component: () => import('../views/admin/AdminLayout.vue'),
    meta: { requiresAdmin: true },
    children: [
      { path: '', redirect: { name: 'admin-overview' } },
      {
        path: 'overview',
        name: 'admin-overview',
        component: () => import('../views/admin/AdminOverviewView.vue'),
        meta: { title: '管理概览' },
      },
      {
        path: 'users',
        name: 'admin-users',
        component: () => import('../views/admin/AdminUsersView.vue'),
        meta: { title: '用户管理' },
      },
      {
        path: 'users/:id',
        name: 'admin-user-detail',
        component: () => import('../views/admin/AdminUserDetailView.vue'),
        meta: { title: '用户详情' },
      },
      {
        path: 'content/posts',
        name: 'admin-posts',
        component: () => import('../views/admin/AdminPostsView.vue'),
        meta: { title: '帖子审核' },
      },
      {
        path: 'content/posts/:id',
        name: 'admin-post-detail',
        component: () => import('../views/admin/AdminPostDetailView.vue'),
        meta: { title: '帖子详情' },
      },
      {
        path: 'content/comments',
        name: 'admin-comments',
        component: () => import('../views/admin/AdminCommentsView.vue'),
        meta: { title: '评论审核' },
      },
      {
        path: 'content/attachments',
        name: 'admin-attachments',
        component: () => import('../views/admin/AdminAttachmentsView.vue'),
        meta: { title: '附件管理' },
      },
      {
        path: 'providers',
        name: 'admin-providers',
        component: () => import('../views/admin/AdminProvidersView.vue'),
        meta: { title: '模型接口预设' },
      },
      {
        path: 'ai',
        name: 'admin-ai',
        component: () => import('../views/admin/AdminAIListView.vue'),
        meta: { title: 'AI 管理' },
      },
      {
        path: 'ai/new',
        name: 'admin-ai-new',
        component: () => import('../views/admin/AdminAICreateView.vue'),
        meta: { title: '创建 AI' },
      },
      {
        path: 'ai/:id',
        component: () => import('../views/admin/AdminAIDetailLayout.vue'),
        children: [
          { path: '', redirect: { name: 'admin-ai-overview' } },
          {
            path: 'overview',
            name: 'admin-ai-overview',
            component: () => import('../views/admin/AdminAIOverviewView.vue'),
            meta: { title: 'AI 概览' },
          },
          {
            path: 'identity',
            name: 'admin-ai-identity',
            component: () => import('../views/admin/AdminAIIdentityView.vue'),
            meta: { title: 'AI 身份与模型' },
          },
          {
            path: 'behavior',
            name: 'admin-ai-behavior',
            component: () => import('../views/admin/AdminAIBehaviorView.vue'),
            meta: { title: 'AI 行为策略' },
          },
          {
            path: 'memory',
            name: 'admin-ai-memory',
            component: () => import('../views/admin/AdminAIMemoryView.vue'),
            meta: { title: 'AI 记忆' },
          },
          {
            path: 'publish',
            name: 'admin-ai-publish',
            component: () => import('../views/admin/AdminAIPublishView.vue'),
            meta: { title: 'AI 生成与发布' },
          },
          {
            path: 'tasks',
            name: 'admin-ai-tasks',
            component: () => import('../views/admin/AdminAITasksView.vue'),
            meta: { title: 'AI 任务' },
          },
        ],
      },
      {
        path: 'tasks',
        name: 'admin-tasks',
        component: () => import('../views/admin/AdminTasksView.vue'),
        meta: { title: '全局任务' },
      },
      {
        path: 'ai-calls',
        name: 'admin-ai-calls',
        component: () => import('../views/admin/AdminAICallsView.vue'),
        meta: { title: 'AI 调用记录' },
      },
      {
        path: 'audit',
        name: 'admin-audit',
        component: () => import('../views/admin/AdminAuditView.vue'),
        meta: { title: '审计日志' },
      },
      {
        path: 'settings',
        name: 'admin-settings',
        component: () => import('../views/admin/AdminSettingsView.vue'),
        meta: { title: '系统设置' },
      },
    ],
  },
  {
    path: '/:pathMatch(.*)*',
    name: 'not-found',
    component: () => import('../views/NotFoundView.vue'),
    meta: { title: '页面不存在' },
  },
]

/** 回退/前进恢复滚动位置的最大等待时间：目标页数据异步加载，首次渲染时内容高度可能不足 */
const SCROLL_RESTORE_TIMEOUT_MS = 800

/**
 * 回退/前进时恢复离开前的滚动位置：列表、评论等数据异步加载，
 * 直接滚动会因内容高度不足被钳制；在有限时间内按帧等待内容高度足够后再滚动。
 */
function restoreScrollPosition(position: { left: number; top: number }): Promise<{ left: number; top: number }> {
  if (position.top <= 0) return Promise.resolve(position)
  const startedAt = performance.now()
  return new Promise((resolve) => {
    const check = (): void => {
      const maxScrollTop = document.documentElement.scrollHeight - window.innerHeight
      if (maxScrollTop >= position.top || performance.now() - startedAt >= SCROLL_RESTORE_TIMEOUT_MS) {
        resolve(position)
        return
      }
      window.requestAnimationFrame(check)
    }
    window.requestAnimationFrame(check)
  })
}

export const router = createRouter({
  history: createWebHistory(),
  routes,
  scrollBehavior(_to, _from, savedPosition) {
    // 浏览器回退/前进（含详情页「返回」按钮）恢复离开时的位置；其余导航回到页首
    if (savedPosition) return restoreScrollPosition(savedPosition)
    return { top: 0 }
  },
})

router.beforeEach(async (to) => {
  // 首次导航前先完成 GET /auth/me 引导，保证登录态判断准确
  await ensureAuthReady()
  const user = authState.user
  if (to.meta.guestOnly && user) return { path: '/' }
  if (to.meta.requiresAuth && !user) {
    return { name: 'login', query: { redirect: to.fullPath } }
  }
  if (to.meta.requiresAdmin && user?.role !== 'admin') return { path: '/' }
  return true
})

router.afterEach((to) => {
  const title = to.meta.title
  document.title = title ? `${title} · 时间线` : '时间线'
})
