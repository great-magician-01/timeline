<script setup lang="ts">
// A-17 审计日志：管理处置、AI 动作、记忆管理与关键权限操作
import { computed, reactive } from 'vue'
import { listAuditLogs } from '../../api/adminContent'
import type { AuditLog } from '../../api/adminContent'
import AlertNote from '../../components/AlertNote.vue'
import AppIcon from '../../components/AppIcon.vue'
import { useCursorList } from '../../composables/useCursorList'
import { formatDateTime } from '../../utils/datetime'

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
  <div class="stack">
    <section class="card card--pad">
      <h2 class="page-title">审计日志</h2>
      <p class="page-desc">管理处置、AI 配置变更与全局开关都会记录在这里，用于追溯操作来源。</p>

      <form class="filter-grid" @submit.prevent="onFilter">
        <label class="filter-field">
          <span class="muted">动作类型</span>
          <select v-model="filters.action_type" class="input" name="action_type" aria-label="动作类型筛选">
            <option value="">全部动作</option>
            <option v-for="option in ACTION_OPTIONS" :key="option.value" :value="option.value">
              {{ option.label }}（{{ option.value }}）
            </option>
          </select>
        </label>

        <label class="filter-field">
          <span class="muted">管理员用户名</span>
          <input
            v-model="filters.admin"
            class="input"
            type="search"
            name="admin"
            placeholder="例如 admin"
            aria-label="按管理员用户名筛选"
          />
        </label>

        <div class="filter-actions">
          <button class="btn btn--primary btn--sm" type="submit" :disabled="loading">
            <span v-if="loading" class="spinner" aria-hidden="true"></span>
            {{ loading ? '查询中…' : '查询' }}
          </button>
          <button class="btn btn--secondary btn--sm" type="button" @click="resetFilters">重置</button>
        </div>
      </form>
    </section>

    <p v-if="initialLoading" class="card card--pad loading-row"><span class="spinner" aria-hidden="true"></span> 加载中…</p>

    <template v-else-if="listError && items.length === 0">
      <AlertNote kind="error">{{ listError }}</AlertNote>
      <button class="btn btn--secondary btn--sm self-start" type="button" @click="reload">重试</button>
    </template>

    <section v-else-if="items.length === 0" class="card card--pad">
      <div class="empty">
        <AppIcon name="scroll" :size="26" />
        <strong>没有匹配的审计记录</strong>
        <p>调整动作类型或管理员筛选条件。</p>
      </div>
    </section>

    <template v-else>
      <section class="card table-card">
        <div class="table-scroll">
          <table class="table">
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
                <td class="nowrap col-time">{{ formatDateTime(log.created_at) }}</td>
                <td class="nowrap">
                  <span class="admin-name">{{ log.admin.nickname || log.admin.username }}</span>
                  <span class="muted mono block-id">@{{ log.admin.username }}</span>
                </td>
                <td class="nowrap">
                  {{ actionLabel(log.action_type) }}
                  <span class="muted mono block-id">{{ log.action_type }}</span>
                </td>
                <td class="col-target mono">
                  <template v-if="log.target_type">
                    {{ log.target_type }}<template v-if="log.target_id"> · {{ log.target_id }}</template>
                  </template>
                  <template v-else>—</template>
                </td>
                <td class="col-detail">
                  <pre class="mono block-body">{{ detailJson(log.detail) }}</pre>
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <div class="list-foot">
          <button v-if="hasMore && !loadingMore" class="btn btn--secondary btn--sm" type="button" @click="loadMore">
            加载更多
          </button>
          <p v-else-if="loadingMore" class="loading-row list-foot-loading">
            <span class="spinner" aria-hidden="true"></span> 正在加载…
          </p>
          <p v-else class="muted">已加载全部 {{ items.length }} 条记录</p>
        </div>
      </section>

      <AlertNote v-if="listError" kind="error">{{ listError }}</AlertNote>
    </template>
  </div>
</template>

<style scoped>
.filter-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
  gap: 10px 12px;
  margin-top: 14px;
}
.filter-field {
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: 12.5px;
}
.filter-actions {
  display: flex;
  align-items: flex-end;
  gap: 8px;
}
.self-start {
  align-self: flex-start;
}
.table-card {
  overflow: hidden;
}
.table-scroll {
  overflow-x: auto;
}
.table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}
.table th {
  padding: 10px 12px;
  border-bottom: 1px solid var(--border);
  color: var(--ink-3);
  font-size: 12.5px;
  font-weight: 600;
  text-align: left;
  white-space: nowrap;
}
.table td {
  padding: 10px 12px;
  border-bottom: 1px solid var(--border);
  vertical-align: top;
}
.table tbody tr:last-child td {
  border-bottom: none;
}
.nowrap {
  white-space: nowrap;
}
.col-time {
  font-size: 12.5px;
  color: var(--ink-2);
}
.admin-name {
  font-weight: 700;
}
.block-id {
  display: block;
  font-size: 11.5px;
  margin-top: 2px;
}
.col-target {
  max-width: 200px;
  word-break: break-all;
}
.col-detail {
  min-width: 260px;
}
.block-body {
  max-height: 220px;
  overflow: auto;
  margin: 0;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--surface-subtle);
  font-size: 12px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-word;
}
.list-foot {
  display: flex;
  justify-content: center;
  padding: 14px 16px;
}
.list-foot .muted {
  margin: 0;
}
.list-foot-loading {
  padding: 0;
}
</style>
