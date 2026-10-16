<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { getUserDetail, resetUserPassword, suspendUser, unsuspendUser } from '../../api/admin'
import { ApiError, errorMessage, fieldErrors } from '../../api/client'
import type { CurrentUser } from '../../api/types'
import AlertNote from '../../components/AlertNote.vue'
import AppIcon from '../../components/AppIcon.vue'
import FormField from '../../components/FormField.vue'
import UserAvatar from '../../components/UserAvatar.vue'
import { authState, setCurrentUser } from '../../stores/auth'
import { formatDateTime } from '../../utils/datetime'

const route = useRoute()
const userId = computed(() => String(route.params.id ?? ''))

const loading = ref(true)
const notFound = ref(false)
const loadError = ref('')
const user = ref<CurrentUser | null>(null)
const activeSessions = ref(0)

const isSuspended = computed(() => user.value?.status === 'suspended' || user.value?.is_suspended === true)

/** 请求序号：快速切换 :id 时作废旧响应，避免旧用户覆盖新用户 */
let loadSequence = 0

async function load(): Promise<void> {
  const current = ++loadSequence
  loading.value = true
  notFound.value = false
  loadError.value = ''
  user.value = null
  try {
    const data = await getUserDetail(userId.value)
    if (current !== loadSequence) return
    user.value = data.user
    activeSessions.value = data.active_sessions
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

watch(userId, () => void load(), { immediate: true })

/** 管理员操作的对象可能就是自己：若是本人，同步更新本地登录态 */
function applyResult(updated: CurrentUser): void {
  user.value = updated
  if (authState.user && authState.user.id === updated.id) setCurrentUser(updated)
}

// ---------- 停用 / 启用 ----------
const suspendConfirming = ref(false)
const actionSaving = ref(false)
const actionError = ref('')
const actionNotice = ref('')

async function onSuspend(): Promise<void> {
  if (!user.value) return
  actionSaving.value = true
  actionError.value = ''
  actionNotice.value = ''
  try {
    const data = await suspendUser(user.value.id)
    applyResult(data.user)
    activeSessions.value = 0
    suspendConfirming.value = false
    actionNotice.value = '账号已停用，该用户的既有会话已全部失效。'
  } catch (error) {
    actionError.value = errorMessage(error)
  } finally {
    actionSaving.value = false
  }
}

async function onUnsuspend(): Promise<void> {
  if (!user.value) return
  actionSaving.value = true
  actionError.value = ''
  actionNotice.value = ''
  try {
    const data = await unsuspendUser(user.value.id)
    applyResult(data.user)
    actionNotice.value = '账号已恢复，该用户可以重新登录。'
  } catch (error) {
    actionError.value = errorMessage(error)
  } finally {
    actionSaving.value = false
  }
}

// ---------- 重置密码 ----------
const passwordForm = reactive({ new_password: '' })
const resetConfirming = ref(false)
const passwordSaving = ref(false)
const passwordError = ref('')
const passwordErrors = ref<Record<string, string>>({})
const passwordNotice = ref('')

function askReset(): void {
  passwordNotice.value = ''
  passwordError.value = ''
  const next: Record<string, string> = {}
  if (!passwordForm.new_password) {
    next.new_password = '请输入新密码'
  } else if (passwordForm.new_password.length < 8) {
    next.new_password = '新密码至少需要 8 个字符'
  }
  passwordErrors.value = next
  if (Object.keys(next).length === 0) resetConfirming.value = true
}

async function confirmReset(): Promise<void> {
  if (!user.value) return
  passwordSaving.value = true
  passwordError.value = ''
  passwordNotice.value = ''
  try {
    await resetUserPassword(user.value.id, passwordForm.new_password)
    passwordForm.new_password = ''
    resetConfirming.value = false
    activeSessions.value = 0
    passwordNotice.value = '密码已重置，该用户的既有会话已全部失效。新密码不会在此页面回显。'
  } catch (error) {
    const fields = fieldErrors(error)
    if (Object.keys(fields).length > 0) {
      passwordErrors.value = fields
    } else {
      passwordError.value = errorMessage(error)
    }
  } finally {
    passwordSaving.value = false
  }
}
</script>

<template>
  <div class="stack">
    <RouterLink to="/admin/users" class="back-link">
      <AppIcon name="arrow-left" :size="16" />
      返回用户列表
    </RouterLink>

    <p v-if="loading" class="card card--pad loading-row"><span class="spinner" aria-hidden="true"></span> 加载中…</p>

    <template v-else-if="notFound">
      <section class="card card--pad">
        <div class="empty">
          <AppIcon name="users" :size="26" />
          <strong>用户不存在或不可见</strong>
          <p>该 ID 可能无效，或用户已被移除。</p>
        </div>
      </section>
    </template>

    <template v-else-if="loadError">
      <AlertNote kind="error">{{ loadError }}</AlertNote>
      <button class="btn btn--secondary btn--sm self-start" type="button" @click="load">重试</button>
    </template>

    <template v-else-if="user">
      <section class="card card--pad">
        <div class="user-head">
          <UserAvatar :user="user" size="lg" />
          <div class="user-info">
            <p class="user-name">
              <span class="name-text">{{ user.nickname || user.username }}</span>
              <span v-if="user.role === 'admin'" class="badge">管理员</span>
              <span v-if="user.is_ai" class="badge badge--ai">AI</span>
              <span v-if="isSuspended" class="badge badge--danger">已停用</span>
            </p>
            <p class="muted">@{{ user.username }}</p>
          </div>
        </div>

        <p v-if="user.bio" class="user-bio">{{ user.bio }}</p>

        <dl class="info-list user-list-info">
          <dt>用户 ID</dt>
          <dd class="mono">{{ user.id }}</dd>
          <dt>邮箱</dt>
          <dd>{{ user.email }}</dd>
          <dt>角色</dt>
          <dd>{{ user.role === 'admin' ? '管理员' : '普通用户' }}</dd>
          <dt>状态</dt>
          <dd>{{ isSuspended ? '已停用' : '正常' }}</dd>
          <dt>类型</dt>
          <dd>{{ user.is_ai ? 'AI 用户' : '人类用户' }}</dd>
          <dt>活跃会话</dt>
          <dd class="mono">{{ activeSessions }}</dd>
          <dt>注册时间</dt>
          <dd>{{ formatDateTime(user.created_at) }}</dd>
          <dt>更新时间</dt>
          <dd>{{ formatDateTime(user.updated_at) }}</dd>
        </dl>
      </section>

      <section class="card card--pad">
        <h2 class="page-title">账号状态</h2>
        <p class="page-desc">停用后该用户的所有会话立即失效；恢复后可以重新登录。</p>

        <AlertNote v-if="actionNotice" kind="success">{{ actionNotice }}</AlertNote>
        <AlertNote v-if="actionError" kind="error">{{ actionError }}</AlertNote>

        <div v-if="!suspendConfirming" class="actions">
          <button v-if="!isSuspended" class="btn btn--danger" type="button" :disabled="actionSaving" @click="suspendConfirming = true">
            停用账号
          </button>
          <button v-else class="btn btn--primary" type="button" :disabled="actionSaving" @click="onUnsuspend">
            <span v-if="actionSaving" class="spinner" aria-hidden="true"></span>
            {{ actionSaving ? '处理中…' : '恢复账号' }}
          </button>
        </div>

        <div v-else class="confirm-panel">
          <AlertNote kind="warning">
            确认停用 @{{ user.username }}？停用后该用户的会话会立即失效，且无法执行写操作。
          </AlertNote>
          <div class="actions">
            <button class="btn btn--danger btn--sm" type="button" :disabled="actionSaving" @click="onSuspend">
              <span v-if="actionSaving" class="spinner" aria-hidden="true"></span>
              {{ actionSaving ? '处理中…' : '确认停用' }}
            </button>
            <button class="btn btn--secondary btn--sm" type="button" :disabled="actionSaving" @click="suspendConfirming = false">
              取消
            </button>
          </div>
        </div>
      </section>

      <section class="card card--pad">
        <h2 class="page-title">重置密码</h2>
        <p class="page-desc">管理员直接设置新密码；重置后该用户的所有会话立即失效，旧密码不会回显。</p>

        <AlertNote v-if="passwordNotice" kind="success">{{ passwordNotice }}</AlertNote>
        <AlertNote v-if="passwordError" kind="error">{{ passwordError }}</AlertNote>

        <form class="form" novalidate @submit.prevent="askReset">
          <FormField
            label="新密码"
            input-id="admin-reset-password"
            :error="passwordErrors.new_password"
            hint="至少 8 个字符；请通过安全渠道告知用户"
          >
            <input
              id="admin-reset-password"
              v-model="passwordForm.new_password"
              class="input"
              type="password"
              name="new_password"
              autocomplete="new-password"
              placeholder="输入新的临时密码"
              :disabled="passwordSaving"
            />
          </FormField>

          <div v-if="!resetConfirming" class="actions">
            <button class="btn btn--secondary" type="submit" :disabled="passwordSaving">重置密码</button>
          </div>
        </form>

        <div v-if="resetConfirming" class="confirm-panel">
          <AlertNote kind="warning">
            确认重置 @{{ user.username }} 的密码？该用户的所有会话将立即失效，新密码只会提交给后端、不在页面显示。
          </AlertNote>
          <div class="actions">
            <button class="btn btn--danger btn--sm" type="button" :disabled="passwordSaving" @click="confirmReset">
              <span v-if="passwordSaving" class="spinner" aria-hidden="true"></span>
              {{ passwordSaving ? '提交中…' : '确认重置' }}
            </button>
            <button class="btn btn--secondary btn--sm" type="button" :disabled="passwordSaving" @click="resetConfirming = false">
              取消
            </button>
          </div>
        </div>
      </section>
    </template>
  </div>
</template>

<style scoped>
.back-link {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  align-self: flex-start;
  font-size: 13.5px;
  color: var(--ink-2);
}
.back-link:hover {
  color: var(--ink);
  text-decoration: none;
}
.user-head {
  display: flex;
  align-items: center;
  gap: 14px;
}
.user-info {
  min-width: 0;
}
.user-name {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  margin: 0;
  font-size: 17px;
  font-weight: 800;
}
.user-info .muted {
  margin: 2px 0 0;
}
.user-bio {
  margin: 14px 0 0;
  font-size: 14px;
  line-height: 1.8;
  white-space: pre-wrap;
}
.user-list-info {
  margin-top: 16px;
}
.self-start {
  align-self: flex-start;
}
.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.confirm-panel {
  margin-top: 4px;
}
</style>
