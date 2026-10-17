<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { grantAdmin, getUserDetail, resetUserPassword, revokeAdmin, suspendUser, unsuspendUser } from '../../api/admin'
import { ApiError, errorMessage, fieldErrors } from '../../api/client'
import type { AdminUserStats, CurrentUser } from '../../api/types'
import AlertNote from '../../components/AlertNote.vue'
import AppIcon from '../../components/AppIcon.vue'
import FormField from '../../components/FormField.vue'
import UserAvatar from '../../components/UserAvatar.vue'
import { authState, setCurrentUser } from '../../stores/auth'
import { formatDateTime } from '../../utils/datetime'
import { ui } from '../../styles/ui'

const route = useRoute()
const userId = computed(() => String(route.params.id ?? ''))

const loading = ref(true)
const notFound = ref(false)
const loadError = ref('')
const user = ref<CurrentUser | null>(null)
const activeSessions = ref(0)
const stats = ref<AdminUserStats | null>(null)

const STAT_FIELDS: { key: keyof AdminUserStats; label: string }[] = [
  { key: 'posts', label: '帖子' },
  { key: 'comments', label: '评论' },
  { key: 'followers', label: '粉丝' },
  { key: 'following', label: '关注' },
  { key: 'likes_given', label: '发出点赞' },
  { key: 'likes_received', label: '收到点赞' },
  { key: 'reposts', label: '转发' },
]

const isSuspended = computed(() => user.value?.status === 'suspended' || user.value?.is_suspended === true)

const isSelf = computed(() => !!user.value && !!authState.user && user.value.id === authState.user.id)
const viewerIsSuperAdmin = computed(() => authState.user?.is_super_admin === true)
/** 目标是超级管理员、而当前登录者不是：普通管理员不能处置该账号 */
const superAdminProtected = computed(() => user.value?.is_super_admin === true && !viewerIsSuperAdmin.value)
/** 只有超级管理员能授权/撤销管理员，且目标必须是人类、非本人、非超级管理员 */
const canManageRoles = computed(
  () =>
    viewerIsSuperAdmin.value &&
    !!user.value &&
    user.value.is_ai !== true &&
    !isSelf.value &&
    user.value.is_super_admin !== true,
)

const roleLabel = computed(() => {
  const target = user.value
  if (!target) return ''
  if (target.is_super_admin) return '超级管理员'
  return target.role === 'admin' ? '管理员' : '普通用户'
})

const roleDescription = computed(() => {
  const target = user.value
  if (!target) return ''
  if (target.is_ai) return 'AI 账号不能成为管理员，角色固定为普通用户；其行为策略由 AI 配置单独管理。'
  if (target.is_super_admin) return '超级管理员拥有全部管理权限，可授权或撤销管理员；该账号不能被降级，普通管理员也不能处置。'
  if (target.role === 'admin') return '管理员可访问管理后台；管理员的授权与撤销只能由超级管理员执行。'
  return '普通用户可以使用社区功能；超级管理员可以将其授权为管理员。'
})

/** 当前登录者不能操作角色时的说明 */
const roleActionHint = computed(() => {
  const target = user.value
  if (!target || canManageRoles.value) return ''
  if (target.is_ai) return 'AI 账号不能成为管理员。'
  if (target.is_super_admin) return '该账号为超级管理员，不能修改其管理员权限。'
  if (isSelf.value) return '不能修改自己的管理员权限。'
  return '只有超级管理员可以授权或撤销管理员。'
})

/** 请求序号：快速切换 :id 时作废旧响应，避免旧用户覆盖新用户 */
let loadSequence = 0

async function load(): Promise<void> {
  const current = ++loadSequence
  loading.value = true
  notFound.value = false
  loadError.value = ''
  user.value = null
  stats.value = null
  try {
    const data = await getUserDetail(userId.value)
    if (current !== loadSequence) return
    user.value = data.user
    activeSessions.value = data.active_sessions
    stats.value = data.stats ?? null
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

// ---------- 授权 / 撤销管理员（仅超级管理员） ----------
const roleConfirming = ref(false)
const roleSaving = ref(false)
const roleError = ref('')
const roleNotice = ref('')

const roleActive = computed(() => user.value?.role === 'admin')

const roleActionLabel = computed(() => (roleActive.value ? '撤销管理员' : '授权为管理员'))

const roleConfirmText = computed(() => {
  const target = user.value
  if (!target) return ''
  return roleActive.value
    ? `确认撤销 @${target.username} 的管理员权限？该用户将立即失去管理后台的访问权限，现有会话不会失效。`
    : `确认将 @${target.username} 授权为管理员？该用户将立即获得管理后台的访问权限，现有会话不会失效。`
})

function askRoleChange(): void {
  if (!canManageRoles.value) return
  roleError.value = ''
  roleNotice.value = ''
  roleConfirming.value = true
}

async function confirmRoleChange(): Promise<void> {
  if (!user.value || !canManageRoles.value) return
  const granting = !roleActive.value
  roleSaving.value = true
  roleError.value = ''
  roleNotice.value = ''
  try {
    const data = granting ? await grantAdmin(user.value.id) : await revokeAdmin(user.value.id)
    applyResult(data.user)
    roleConfirming.value = false
    roleNotice.value = granting ? '已授权为管理员，角色权限立即生效。' : '已撤销管理员权限，角色变更立即生效。'
  } catch (error) {
    roleError.value = errorMessage(error)
  } finally {
    roleSaving.value = false
  }
}
</script>

<template>
  <div :class="ui.stack">
    <RouterLink to="/admin/users" class="inline-flex items-center gap-[6px] self-start text-[13.5px] text-ink-2 hover:text-ink hover:no-underline">
      <AppIcon name="arrow-left" :size="16" />
      返回用户列表
    </RouterLink>

    <p v-if="loading" :class="[ui.card, ui.cardPad, ui.loadingRow]"><span :class="ui.spinner" aria-hidden="true"></span> 加载中…</p>

    <template v-else-if="notFound">
      <section :class="[ui.card, ui.cardPad]">
        <div :class="ui.empty">
          <AppIcon name="users" :size="26" :class="ui.emptyIcon" />
          <strong :class="ui.emptyStrong">用户不存在或不可见</strong>
          <p :class="ui.emptyText">该 ID 可能无效，或用户已被移除。</p>
        </div>
      </section>
    </template>

    <template v-else-if="loadError">
      <AlertNote kind="error">{{ loadError }}</AlertNote>
      <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'self-start']" type="button" @click="load">重试</button>
    </template>

    <template v-else-if="user">
      <section :class="[ui.card, ui.cardPad]">
        <div class="flex items-center gap-[14px]">
          <UserAvatar :user="user" size="lg" />
          <div class="min-w-0">
            <p class="m-0 flex flex-wrap items-center gap-[6px] text-[17px] font-extrabold">
              <span class="font-extrabold">{{ user.nickname || user.username }}</span>
              <span v-if="user.is_super_admin" :class="[ui.badge, ui.badgeSuper]">超级管理员</span>
              <span v-else-if="user.role === 'admin'" :class="ui.badge">管理员</span>
              <span v-if="user.is_ai" :class="[ui.badge, ui.badgeAi]">AI</span>
              <span v-if="isSuspended" :class="[ui.badge, ui.badgeDanger]">已停用</span>
            </p>
            <p :class="[ui.muted, 'm-0 mt-[2px]']">@{{ user.username }}</p>
          </div>
        </div>

        <p v-if="user.bio" class="m-0 mt-[14px] whitespace-pre-wrap text-[14px] leading-[1.8]">{{ user.bio }}</p>

        <dl :class="[ui.infoList, 'mt-4']">
          <dt :class="ui.infoTerm">用户 ID</dt>
          <dd :class="[ui.infoDetails, ui.mono]">{{ user.id }}</dd>
          <dt :class="ui.infoTerm">邮箱</dt>
          <dd :class="ui.infoDetails">{{ user.email }}</dd>
          <dt :class="ui.infoTerm">角色</dt>
          <dd :class="ui.infoDetails">{{ roleLabel }}</dd>
          <dt :class="ui.infoTerm">状态</dt>
          <dd :class="ui.infoDetails">{{ isSuspended ? '已停用' : '正常' }}</dd>
          <dt :class="ui.infoTerm">类型</dt>
          <dd :class="ui.infoDetails">{{ user.is_ai ? 'AI 用户' : '人类用户' }}</dd>
          <dt :class="ui.infoTerm">活跃会话</dt>
          <dd :class="[ui.infoDetails, ui.mono]">{{ activeSessions }}</dd>
          <dt :class="ui.infoTerm">注册时间</dt>
          <dd :class="ui.infoDetails">{{ formatDateTime(user.created_at) }}</dd>
          <dt :class="ui.infoTerm">更新时间</dt>
          <dd :class="ui.infoDetails">{{ formatDateTime(user.updated_at) }}</dd>
        </dl>
      </section>

      <section v-if="stats" :class="[ui.card, ui.cardPad]">
        <h2 :class="ui.pageTitle">内容与关系概览</h2>
        <p :class="ui.pageDesc">用于管理审核统计；数据来自当前账号的帖子、评论与关注关系。</p>
        <ul class="m-0 mt-[14px] grid list-none grid-cols-[repeat(auto-fit,minmax(110px,1fr))] gap-[10px] p-0">
          <li v-for="field in STAT_FIELDS" :key="field.key" class="rounded-[10px] border border-solid border-border bg-surface-subtle px-3 py-[10px]">
            <strong class="block text-[18px] font-extrabold tabular-nums">{{ stats[field.key] }}</strong>
            <span class="text-[12.5px] text-ink-3">{{ field.label }}</span>
          </li>
        </ul>
      </section>

      <section :class="[ui.card, ui.cardPad]">
        <h2 :class="ui.pageTitle">角色与权限</h2>
        <p :class="ui.pageDesc">{{ roleDescription }}</p>

        <AlertNote v-if="roleNotice" kind="success">{{ roleNotice }}</AlertNote>
        <AlertNote v-if="roleError" kind="error">{{ roleError }}</AlertNote>

        <template v-if="canManageRoles">
          <div v-if="!roleConfirming" class="flex flex-wrap gap-2">
            <button
              :class="[ui.button, roleActive ? ui.buttonDanger : ui.buttonPrimary]"
              type="button"
              :disabled="roleSaving"
              @click="askRoleChange"
            >
              {{ roleActionLabel }}
            </button>
          </div>

          <div v-else class="mt-1">
            <AlertNote kind="warning">{{ roleConfirmText }}</AlertNote>
            <div class="flex flex-wrap gap-2">
              <button :class="[ui.button, ui.buttonPrimary, ui.buttonSmall]" type="button" :disabled="roleSaving" @click="confirmRoleChange">
                <span v-if="roleSaving" :class="[ui.spinner, ui.buttonSpinner]" aria-hidden="true"></span>
                {{ roleSaving ? '处理中…' : '确认操作' }}
              </button>
              <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]" type="button" :disabled="roleSaving" @click="roleConfirming = false">
                取消
              </button>
            </div>
          </div>
        </template>

        <p v-else-if="roleActionHint" :class="[ui.muted, 'm-0 mt-[14px]']">{{ roleActionHint }}</p>
      </section>

      <section :class="[ui.card, ui.cardPad]">
        <h2 :class="ui.pageTitle">账号状态</h2>
        <p :class="ui.pageDesc">停用后该用户的所有会话立即失效；恢复后可以重新登录。</p>

        <AlertNote v-if="superAdminProtected" kind="warning">该账号为超级管理员，普通管理员不能处置。</AlertNote>

        <template v-else>
          <AlertNote v-if="actionNotice" kind="success">{{ actionNotice }}</AlertNote>
          <AlertNote v-if="actionError" kind="error">{{ actionError }}</AlertNote>

          <div v-if="!suspendConfirming" class="flex flex-wrap gap-2">
            <button v-if="!isSuspended" :class="[ui.button, ui.buttonDanger]" type="button" :disabled="actionSaving" @click="suspendConfirming = true">
              停用账号
            </button>
            <button v-else :class="[ui.button, ui.buttonPrimary]" type="button" :disabled="actionSaving" @click="onUnsuspend">
              <span v-if="actionSaving" :class="[ui.spinner, ui.buttonSpinner]" aria-hidden="true"></span>
              {{ actionSaving ? '处理中…' : '恢复账号' }}
            </button>
          </div>

          <div v-else class="mt-1">
            <AlertNote kind="warning">
              确认停用 @{{ user.username }}？停用后该用户的会话会立即失效，且无法执行写操作。
            </AlertNote>
            <div class="flex flex-wrap gap-2">
              <button :class="[ui.button, ui.buttonDanger, ui.buttonSmall]" type="button" :disabled="actionSaving" @click="onSuspend">
                <span v-if="actionSaving" :class="[ui.spinner, ui.buttonSpinner]" aria-hidden="true"></span>
                {{ actionSaving ? '处理中…' : '确认停用' }}
              </button>
              <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]" type="button" :disabled="actionSaving" @click="suspendConfirming = false">
                取消
              </button>
            </div>
          </div>
        </template>
      </section>

      <section :class="[ui.card, ui.cardPad]">
        <h2 :class="ui.pageTitle">重置密码</h2>
        <p :class="ui.pageDesc">管理员直接设置新密码；重置后该用户的所有会话立即失效，旧密码不会回显。</p>

        <AlertNote v-if="superAdminProtected" kind="warning">该账号为超级管理员，普通管理员不能处置。</AlertNote>

        <template v-else>
          <AlertNote v-if="passwordNotice" kind="success">{{ passwordNotice }}</AlertNote>
          <AlertNote v-if="passwordError" kind="error">{{ passwordError }}</AlertNote>

          <form :class="ui.form" novalidate @submit.prevent="askReset">
            <FormField
              label="新密码"
              input-id="admin-reset-password"
              :error="passwordErrors.new_password"
              hint="至少 8 个字符；请通过安全渠道告知用户"
            >
              <input
                id="admin-reset-password"
                v-model="passwordForm.new_password"
                :class="ui.input"
                type="password"
                name="new_password"
                autocomplete="new-password"
                placeholder="输入新的临时密码"
                :disabled="passwordSaving"
              />
            </FormField>

            <div v-if="!resetConfirming" class="flex flex-wrap gap-2">
              <button :class="[ui.button, ui.buttonSecondary]" type="submit" :disabled="passwordSaving">重置密码</button>
            </div>
          </form>

          <div v-if="resetConfirming" class="mt-1">
            <AlertNote kind="warning">
              确认重置 @{{ user.username }} 的密码？该用户的所有会话将立即失效，新密码只会提交给后端、不在页面显示。
            </AlertNote>
            <div class="flex flex-wrap gap-2">
              <button :class="[ui.button, ui.buttonDanger, ui.buttonSmall]" type="button" :disabled="passwordSaving" @click="confirmReset">
                <span v-if="passwordSaving" :class="[ui.spinner, ui.buttonSpinner]" aria-hidden="true"></span>
                {{ passwordSaving ? '提交中…' : '确认重置' }}
              </button>
              <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]" type="button" :disabled="passwordSaving" @click="resetConfirming = false">
                取消
              </button>
            </div>
          </div>
        </template>
      </section>
    </template>
  </div>
</template>
