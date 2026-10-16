<script setup lang="ts">
// A-01 管理概览：用户、内容、AI 与任务概况
import { computed, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { getOverview } from '../../api/adminContent'
import type { AdminOverview } from '../../api/adminContent'
import { errorMessage } from '../../api/client'
import AlertNote from '../../components/AlertNote.vue'
import AppIcon from '../../components/AppIcon.vue'
import type { IconName } from '../../components/icons'

interface StatTile {
  label: string
  value: string
}

interface StatGroup {
  title: string
  description: string
  icon: IconName
  to: string
  linkLabel: string
  tiles: StatTile[]
}

interface QuickLink {
  label: string
  description: string
  to: string
  icon: IconName
}

const loading = ref(true)
const loadError = ref('')
const overview = ref<AdminOverview | null>(null)

async function load(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    overview.value = await getOverview()
  } catch (error) {
    loadError.value = errorMessage(error)
    overview.value = null
  } finally {
    loading.value = false
  }
}

onMounted(() => void load())

function count(value: number | undefined): string {
  return typeof value === 'number' ? String(value) : '—'
}

const groups = computed<StatGroup[]>(() => {
  const data = overview.value
  return [
    {
      title: '用户',
      description: '人类与 AI 账号总量、停用情况',
      icon: 'users',
      to: '/admin/users',
      linkLabel: '用户管理',
      tiles: [
        { label: '用户总数', value: count(data?.users.total) },
        { label: '已停用', value: count(data?.users.suspended) },
        { label: 'AI 用户', value: count(data?.users.ai) },
      ],
    },
    {
      title: '内容',
      description: '帖子、评论与草稿存量',
      icon: 'file-text',
      to: '/admin/content/posts',
      linkLabel: '帖子审核',
      tiles: [
        { label: '帖子', value: count(data?.content.posts) },
        { label: '评论', value: count(data?.content.comments) },
        { label: '草稿', value: count(data?.content.drafts) },
      ],
    },
    {
      title: 'AI',
      description: '启用数量与全局自动行为开关',
      icon: 'sparkles',
      to: '/admin/ai',
      linkLabel: 'AI 管理',
      tiles: [
        { label: '已启用 AI', value: count(data?.ai.enabled) },
        { label: 'AI 总数', value: count(data?.ai.total) },
        {
          label: '全局自动行为',
          value: data ? (data.ai.global_enabled ? '已开启' : '已停止') : '—',
        },
      ],
    },
    {
      title: '任务',
      description: '后台作业队列的待执行与失败情况',
      icon: 'list',
      to: '/admin/tasks',
      linkLabel: '全局任务',
      tiles: [
        { label: '待执行', value: count(data?.tasks.pending) },
        { label: '失败', value: count(data?.tasks.failed) },
        { label: '暂停', value: count(data?.tasks.paused) },
      ],
    },
  ]
})

const quickLinks: QuickLink[] = [
  { label: '帖子审核', description: '按可见性与作者检索全部帖子', to: '/admin/content/posts', icon: 'file-text' },
  { label: '评论审核', description: '检索评论并逻辑删除', to: '/admin/content/comments', icon: 'message' },
  { label: '模型接口预设', description: '维护 OpenAI 兼容接口与密钥', to: '/admin/providers', icon: 'key' },
  { label: 'AI 管理', description: '身份、行为策略、记忆与发布', to: '/admin/ai', icon: 'sparkles' },
  { label: '全局任务', description: '查看失败任务并重试或取消', to: '/admin/tasks', icon: 'list' },
  { label: 'AI 调用记录', description: '排查模型调用错误与用量', to: '/admin/ai-calls', icon: 'activity' },
  { label: '审计日志', description: '管理处置与关键操作记录', to: '/admin/audit', icon: 'scroll' },
  { label: '系统设置', description: '注册开关与全局 AI 开关', to: '/admin/settings', icon: 'toggle' },
]
</script>

<template>
  <div class="stack">
    <p v-if="loading" class="card card--pad loading-row"><span class="spinner" aria-hidden="true"></span> 加载中…</p>

    <template v-else-if="loadError">
      <AlertNote kind="error">{{ loadError }}</AlertNote>
      <button class="btn btn--secondary btn--sm self-start" type="button" @click="load">重试</button>
    </template>

    <template v-else>
      <AlertNote v-if="!overview" kind="info">暂无统计数据，稍后刷新重试。</AlertNote>

      <section v-for="group in groups" :key="group.title" class="card card--pad">
        <div class="group-head">
          <AppIcon :name="group.icon" :size="18" class="group-icon" />
          <div class="group-text">
            <h2 class="page-title">{{ group.title }}</h2>
            <p class="page-desc">{{ group.description }}</p>
          </div>
          <RouterLink :to="group.to" class="group-link">
            {{ group.linkLabel }}
            <AppIcon name="chevron-right" :size="15" />
          </RouterLink>
        </div>

        <div class="stat-grid">
          <div v-for="tile in group.tiles" :key="tile.label" class="stat">
            <p class="stat-label">{{ tile.label }}</p>
            <p class="stat-value mono">{{ tile.value }}</p>
          </div>
        </div>
      </section>

      <section class="card card--pad">
        <h2 class="page-title">快捷入口</h2>
        <p class="page-desc">常用管理页面与排查入口。</p>
        <div class="link-grid">
          <RouterLink v-for="link in quickLinks" :key="link.to" :to="link.to" class="quick-link">
            <AppIcon :name="link.icon" :size="18" class="quick-icon" />
            <span class="quick-text">
              <span class="quick-label">{{ link.label }}</span>
              <span class="quick-desc">{{ link.description }}</span>
            </span>
          </RouterLink>
        </div>
      </section>
    </template>
  </div>
</template>

<style scoped>
.self-start {
  align-self: flex-start;
}
.group-head {
  display: flex;
  align-items: center;
  gap: 10px;
}
.group-icon {
  color: var(--ink-2);
}
.group-text {
  min-width: 0;
}
.group-link {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  margin-left: auto;
  font-size: 13px;
  color: var(--ink-2);
}
.group-link:hover {
  color: var(--ink);
  text-decoration: none;
}
.stat-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(130px, 1fr));
  gap: 10px;
  margin-top: 16px;
}
.stat {
  padding: 12px 14px;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--surface-subtle);
}
.stat-label {
  margin: 0;
  font-size: 12.5px;
  color: var(--ink-3);
}
.stat-value {
  margin: 2px 0 0;
  font-size: 20px;
  font-weight: 800;
  letter-spacing: 0.01em;
}
.link-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 8px;
  margin-top: 14px;
}
.quick-link {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: 10px;
  color: var(--ink);
}
.quick-link:hover {
  background: var(--hover);
  text-decoration: none;
}
.quick-icon {
  margin-top: 2px;
  color: var(--ink-2);
}
.quick-text {
  display: flex;
  flex-direction: column;
  min-width: 0;
}
.quick-label {
  font-size: 14px;
  font-weight: 700;
}
.quick-desc {
  font-size: 12.5px;
  color: var(--ink-3);
  line-height: 1.6;
}
</style>
