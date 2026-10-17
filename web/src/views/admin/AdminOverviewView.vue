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
import { ui } from '../../styles/ui'

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
  <div :class="ui.stack">
    <p v-if="loading" :class="[ui.card, ui.cardPad, ui.loadingRow]"><span :class="ui.spinner" aria-hidden="true"></span> 加载中…</p>

    <template v-else-if="loadError">
      <AlertNote kind="error">{{ loadError }}</AlertNote>
      <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'self-start']" type="button" @click="load">重试</button>
    </template>

    <template v-else>
      <AlertNote v-if="!overview" kind="info">暂无统计数据，稍后刷新重试。</AlertNote>

      <section v-for="group in groups" :key="group.title" :class="[ui.card, ui.cardPad]">
        <div class="flex items-center gap-[10px]">
          <AppIcon :name="group.icon" :size="18" class="text-ink-2" />
          <div class="min-w-0">
            <h2 :class="ui.pageTitle">{{ group.title }}</h2>
            <p :class="ui.pageDesc">{{ group.description }}</p>
          </div>
          <RouterLink :to="group.to" class="ml-auto inline-flex items-center gap-[2px] text-[13px] text-ink-2 hover:text-ink hover:no-underline">
            {{ group.linkLabel }}
            <AppIcon name="chevron-right" :size="15" />
          </RouterLink>
        </div>

        <div class="mt-4 grid grid-cols-[repeat(auto-fit,minmax(130px,1fr))] gap-[10px]">
          <div v-for="tile in group.tiles" :key="tile.label" class="rounded-[10px] border border-solid border-border bg-surface-subtle px-[14px] py-3">
            <p class="m-0 text-[12.5px] text-ink-3">{{ tile.label }}</p>
            <p :class="[ui.mono, 'm-0 mt-[2px] text-[20px] font-extrabold tracking-[0.01em]']">{{ tile.value }}</p>
          </div>
        </div>
      </section>

      <section :class="[ui.card, ui.cardPad]">
        <h2 :class="ui.pageTitle">快捷入口</h2>
        <p :class="ui.pageDesc">常用管理页面与排查入口。</p>
        <div class="mt-[14px] grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-2">
          <RouterLink v-for="link in quickLinks" :key="link.to" :to="link.to" class="flex items-start gap-[10px] rounded-[10px] border border-solid border-border px-3 py-[10px] text-ink hover:bg-hover hover:no-underline">
            <AppIcon :name="link.icon" :size="18" class="mt-[2px] text-ink-2" />
            <span class="flex min-w-0 flex-col">
              <span class="text-[14px] font-bold">{{ link.label }}</span>
              <span class="text-[12.5px] leading-[1.6] text-ink-3">{{ link.description }}</span>
            </span>
          </RouterLink>
        </div>
      </section>
    </template>
  </div>
</template>
