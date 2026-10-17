<script setup lang="ts">
// A-17 审计日志：管理处置、AI 动作、记忆管理与关键权限操作
import { computed, reactive } from 'vue'
import { listAuditLogs } from '../../api/adminContent'
import type { AuditLog } from '../../api/adminContent'
import AlertNote from '../../components/AlertNote.vue'
import AppIcon from '../../components/AppIcon.vue'
import { useCursorList } from '../../composables/useCursorList'
import { formatDateTime } from '../../utils/datetime'
import { ui } from '../../styles/ui'

type AuditFilters = {
  action_type?: string
  admin?: string
}

interface ActionOption {
  value: string
  label: string
}

// 与后端 internal/audit/audit.go 的常量保持一致
const ACTION_OPTIONS: ActionOption[] = [
  { value: 'user_suspend', label: '停用账号' },
  { value: 'user_unsuspend', label: '恢复账号' },
  { value: 'user_reset_password', label: '重置密码' },
  { value: 'post_delete', label: '删除帖子' },
  { value: 'comment_delete', label: '删除评论' },
  { value: 'attachment_delete', label: '删除附件' },
  { value: 'provider_create', label: '创建模型预设' },
  { value: 'provider_update', label: '修改模型预设' },
  { value: 'provider_delete', label: '删除模型预设' },
  { value: 'ai_create', label: '创建 AI' },
  { value: 'ai_update', label: '更新 AI 资料' },
  { value: 'ai_model_update', label: '更新 AI 模型' },
  { value: 'ai_behavior_update', label: '更新 AI 行为策略' },
  { value: 'ai_enable', label: '启用 AI' },
  { value: 'ai_disable', label: '停用 AI' },
  { value: 'ai_generate', label: 'AI 生成内容' },
  { value: 'ai_publish', label: 'AI 发布内容' },
  { value: 'memory_create', label: '添加记忆' },
  { value: 'memory_update', label: '编辑记忆' },
  { value: 'memory_delete', label: '删除记忆' },
  { value: 'memory_clear', label: '清空记忆' },
  { value: 'task_retry', label: '重试任务' },
  { value: 'task_cancel', label: '取消任务' },
  { value: 'ai_global_start', label: '恢复全局 AI' },
  { value: 'ai_global_stop', label: '全局停止 AI' },
  { value: 'settings_update', label: '更新系统设置' },
  { value: 'settings_registration_update', label: '更新注册开关' },
]

const filters = reactive({ action_type: '', admin: '' })

const {
  items,
  loading,
  loadingMore,
  hasMore,
  error: listError,
  loaded,
  reload,
  loadMore,
} = useCursorList<AuditLog, AuditFilters>({
  fetch: (params) => listAuditLogs(params),
  params: () => ({
    action_type: filters.action_type || undefined,
    admin: filters.admin.trim() || undefined,
  }),
})

const initialLoading = computed(() => !loaded.value && !listError.value)

function actionLabel(value: string): string {
  return ACTION_OPTIONS.find((item) => item.value === value)?.label ?? value
}

function onFilter(): void {
  void reload()
}

function resetFilters(): void {
  filters.action_type = ''
  filters.admin = ''
  void reload()
}

function detailJson(detail: Record<string, unknown>): string {
  try {
    return JSON.stringify(detail ?? {}, null, 2)
  } catch {
    return '（详情无法序列化）'
  }
}
</script>

<template>
  <div :class="ui.stack">
    <section :class="[ui.card, ui.cardPad]">
      <h2 :class="ui.pageTitle">审计日志</h2>
      <p :class="ui.pageDesc">管理处置、AI 配置变更与全局开关都会记录在这里，用于追溯操作来源。</p>

      <form class="mt-[14px] grid grid-cols-[repeat(auto-fit,minmax(200px,1fr))] gap-x-3 gap-y-[10px]" @submit.prevent="onFilter">
        <label class="flex flex-col gap-1 text-[12.5px]">
          <span :class="ui.muted">动作类型</span>
          <select v-model="filters.action_type" :class="ui.input" name="action_type" aria-label="动作类型筛选">
            <option value="">全部动作</option>
            <option v-for="option in ACTION_OPTIONS" :key="option.value" :value="option.value">
              {{ option.label }}（{{ option.value }}）
            </option>
          </select>
        </label>

        <label class="flex flex-col gap-1 text-[12.5px]">
          <span :class="ui.muted">管理员用户名</span>
          <input
            v-model="filters.admin"
            :class="ui.input"
            type="search"
            name="admin"
            placeholder="例如 admin"
            aria-label="按管理员用户名筛选"
          />
        </label>

        <div class="flex items-end gap-2">
          <button :class="[ui.button, ui.buttonPrimary, ui.buttonSmall]" type="submit" :disabled="loading">
            <span v-if="loading" :class="[ui.spinner, ui.buttonSpinner]" aria-hidden="true"></span>
            {{ loading ? '查询中…' : '查询' }}
          </button>
          <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]" type="button" @click="resetFilters">重置</button>
        </div>
      </form>
    </section>

    <p v-if="initialLoading" :class="[ui.card, ui.cardPad, ui.loadingRow]"><span :class="ui.spinner" aria-hidden="true"></span> 加载中…</p>

    <template v-else-if="listError && items.length === 0">
      <AlertNote kind="error">{{ listError }}</AlertNote>
      <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'self-start']" type="button" @click="reload">重试</button>
    </template>

    <section v-else-if="items.length === 0" :class="[ui.card, ui.cardPad]">
      <div :class="ui.empty">
        <AppIcon name="scroll" :size="26" :class="ui.emptyIcon" />
        <strong :class="ui.emptyStrong">没有匹配的审计记录</strong>
        <p :class="ui.emptyText">调整动作类型或管理员筛选条件。</p>
      </div>
    </section>

    <template v-else>
      <section :class="[ui.card, 'overflow-hidden']">
        <div class="overflow-x-auto">
          <table class="w-full border-collapse text-[13px] [&_th]:whitespace-nowrap [&_th]:border-b [&_th]:border-solid [&_th]:border-border [&_th]:px-3 [&_th]:py-[10px] [&_th]:text-left [&_th]:text-[12.5px] [&_th]:font-semibold [&_th]:text-ink-3 [&_td]:border-b [&_td]:border-solid [&_td]:border-border [&_td]:px-3 [&_td]:py-[10px] [&_td]:align-top [&_tbody_tr:last-child_td]:border-b-0">
            <thead>
              <tr>
                <th>时间</th>
                <th>管理员</th>
                <th>动作类型</th>
                <th>目标</th>
                <th>详情</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="log in items" :key="log.id">
                <td class="whitespace-nowrap text-[12.5px] text-ink-2">{{ formatDateTime(log.created_at) }}</td>
                <td class="whitespace-nowrap">
                  <span class="font-bold">{{ log.admin.nickname || log.admin.username }}</span>
                  <span :class="[ui.mono, 'mt-[2px] block text-[11.5px] text-ink-3']">@{{ log.admin.username }}</span>
                </td>
                <td class="whitespace-nowrap">
                  {{ actionLabel(log.action_type) }}
                  <span :class="[ui.mono, 'mt-[2px] block text-[11.5px] text-ink-3']">{{ log.action_type }}</span>
                </td>
                <td :class="[ui.mono, 'max-w-[200px] [word-break:break-all]']">
                  <template v-if="log.target_type">
                    {{ log.target_type }}<template v-if="log.target_id"> · {{ log.target_id }}</template>
                  </template>
                  <template v-else>—</template>
                </td>
                <td class="min-w-[260px]">
                  <pre :class="[ui.mono, 'm-0 max-h-[220px] overflow-auto rounded-[10px] border border-solid border-border bg-surface-subtle px-3 py-[10px] text-[12px] leading-[1.6] whitespace-pre-wrap [word-break:break-word]']">{{ detailJson(log.detail) }}</pre>
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <div class="flex justify-center px-4 py-[14px]">
          <button v-if="hasMore && !loadingMore" :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]" type="button" @click="loadMore">
            加载更多
          </button>
          <p v-else-if="loadingMore" :class="[ui.loadingRow, 'p-0!']">
            <span :class="ui.spinner" aria-hidden="true"></span> 正在加载…
          </p>
          <p v-else :class="[ui.muted, 'm-0']">已加载全部 {{ items.length }} 条记录</p>
        </div>
      </section>

      <AlertNote v-if="listError" kind="error">{{ listError }}</AlertNote>
    </template>
  </div>
</template>
