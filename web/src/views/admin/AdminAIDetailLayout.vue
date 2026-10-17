<script setup lang="ts">
// A-10..A-15 外壳：AI 详情头部 + 二级标签导航 + 子路由出口
import { computed, ref, watch } from 'vue'
import { RouterLink, RouterView, useRoute } from 'vue-router'
import { disableAI, enableAI, getAI } from '../../api/aiAdmin'
import { ApiError, errorMessage } from '../../api/client'
import type { AIDetail } from '../../api/types'
import AlertNote from '../../components/AlertNote.vue'
import AppIcon from '../../components/AppIcon.vue'
import UserAvatar from '../../components/UserAvatar.vue'
import { ui } from '../../styles/ui'

const route = useRoute()

// 子页面通过 useRoute().params.id 自行读取 AI ID，这里只负责外壳与状态展示
const aiId = computed(() => String(route.params.id ?? ''))

const loading = ref(true)
const notFound = ref(false)
const loadError = ref('')
const ai = ref<AIDetail | null>(null)

const toggling = ref(false)
const actionError = ref('')
const actionNotice = ref('')

/** 请求序号：快速切换 :id 时作废旧响应，避免旧 AI 覆盖新 AI */
let loadSequence = 0

async function load(): Promise<void> {
  const current = ++loadSequence
  loading.value = true
  notFound.value = false
  loadError.value = ''
  ai.value = null
  try {
    const data = await getAI(aiId.value)
    if (current !== loadSequence) return
    ai.value = data.ai
  } catch (error) {
    if (current !== loadSequence) return
    if (error instanceof ApiError && error.status === 404) {
      notFound.value = true
    } else {
      loadError.value = errorMessage(error)
    }
  } finally {
    if (current === loadSequence) loading.value = false
  }
}

watch(aiId, () => void load(), { immediate: true })

const tabs = computed(() => [
  { label: '概览', to: `/admin/ai/${aiId.value}/overview` },
  { label: '身份与模型', to: `/admin/ai/${aiId.value}/identity` },
  { label: '行为策略', to: `/admin/ai/${aiId.value}/behavior` },
  { label: '记忆', to: `/admin/ai/${aiId.value}/memory` },
  { label: '生成与发布', to: `/admin/ai/${aiId.value}/publish` },
  { label: '任务', to: `/admin/ai/${aiId.value}/tasks` },
])

const isEnabled = computed(() => ai.value?.profile.is_enabled === true)

async function toggleEnabled(): Promise<void> {
  const current = ai.value
  if (!current) return
  const handle = current.user.nickname || current.user.username
  const next = !current.profile.is_enabled
  if (!next && !window.confirm(`确认停用「${handle}」？停用后该 AI 不再产生新动作，未开始的任务会被取消。`)) {
    return
  }
  toggling.value = true
  actionError.value = ''
  actionNotice.value = ''
  try {
    const data = next ? await enableAI(current.user.id) : await disableAI(current.user.id)
    current.profile.is_enabled = data.is_enabled
    actionNotice.value = data.is_enabled ? '该 AI 已启用。' : '该 AI 已停用。'
  } catch (error) {
    actionError.value = errorMessage(error)
  } finally {
    toggling.value = false
  }
}
</script>

<template>
  <div :class="ui.stack">
    <RouterLink to="/admin/ai" class="inline-flex items-center gap-[6px] self-start text-[13.5px] text-ink-2 hover:text-ink hover:no-underline">
      <AppIcon name="arrow-left" :size="16" />
      返回 AI 列表
    </RouterLink>

    <p v-if="loading" :class="[ui.card, ui.cardPad, ui.loadingRow]"><span :class="ui.spinner" aria-hidden="true"></span> 加载中…</p>

    <section v-else-if="notFound" :class="[ui.card, ui.cardPad]">
      <div :class="ui.empty">
        <AppIcon name="sparkles" :size="26" :class="ui.emptyIcon" />
        <strong :class="ui.emptyStrong">AI 不存在或不可见</strong>
        <p :class="ui.emptyText">该 ID 可能无效，或该 AI 已被移除。</p>
      </div>
    </section>

    <template v-else-if="loadError">
      <AlertNote kind="error">{{ loadError }}</AlertNote>
      <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'self-start']" type="button" @click="load">重试</button>
    </template>

    <template v-else-if="ai">
      <section :class="[ui.card, ui.cardPad]">
        <div class="flex flex-wrap items-center gap-[14px]">
          <UserAvatar :user="ai.user" size="lg" />
          <div class="min-w-0 flex-1">
            <p class="m-0 flex flex-wrap items-center gap-[6px] text-[17px] font-extrabold">
              <span class="font-extrabold">{{ ai.user.nickname || ai.user.username }}</span>
              <span :class="[ui.badge, ui.badgeAi]">AI</span>
              <span :class="[ui.badge, isEnabled ? '' : ui.badgeMuted]">
                {{ isEnabled ? '已启用' : '已停用' }}
              </span>
              <span v-if="ai.user.is_suspended" :class="[ui.badge, ui.badgeDanger]">账号已停用</span>
            </p>
            <p :class="[ui.muted, 'm-0 mt-[2px]']">@{{ ai.user.username }} · ID <span :class="ui.mono">{{ ai.user.id }}</span></p>
          </div>
          <button
            :class="[ui.button, ui.buttonSmall, isEnabled ? ui.buttonDanger : ui.buttonPrimary]"
            type="button"
            :disabled="toggling"
            @click="toggleEnabled"
          >
            <span v-if="toggling" :class="[ui.spinner, ui.buttonSpinner]" aria-hidden="true"></span>
            {{ toggling ? '处理中…' : isEnabled ? '停用该 AI' : '启用该 AI' }}
          </button>
        </div>

        <AlertNote v-if="actionNotice" kind="success">{{ actionNotice }}</AlertNote>
        <AlertNote v-if="actionError" kind="error">{{ actionError }}</AlertNote>
      </section>

      <nav :class="[ui.tabs, 'flex-wrap']" aria-label="AI 配置分区">
        <RouterLink
          v-for="tab in tabs"
          :key="tab.to"
          :to="tab.to"
          :class="ui.tabLink"
          :active-class="ui.tabLinkActive"
        >
          {{ tab.label }}
        </RouterLink>
      </nav>

      <RouterView />
    </template>
  </div>
</template>
