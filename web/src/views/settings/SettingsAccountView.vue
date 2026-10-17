<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { changePassword } from '../../api/auth'
import { errorMessage, fieldErrors } from '../../api/client'
import { getFollowSettings, updateFollowSettings } from '../../api/users'
import AlertNote from '../../components/AlertNote.vue'
import AppIcon from '../../components/AppIcon.vue'
import FormField from '../../components/FormField.vue'
import { authState, logout } from '../../stores/auth'
import { ui } from '../../styles/ui'
import { formatDate } from '../../utils/datetime'

const router = useRouter()
const user = computed(() => authState.user)
const suspended = computed(() => authState.user?.status === 'suspended')

const roleLabel = computed(() => (authState.user?.role === 'admin' ? '管理员' : '普通用户'))

// ---------- 关注审批开关 ----------
const followLoading = ref(true)
const followError = ref('')
const followSaved = ref(false)
const followSaving = ref(false)
const requireApproval = ref(false)

async function loadFollowSettings(): Promise<void> {
  followLoading.value = true
  followError.value = ''
  try {
    const data = await getFollowSettings()
    requireApproval.value = data.require_follow_approval
  } catch (error) {
    followError.value = errorMessage(error)
  } finally {
    followLoading.value = false
  }
}

async function onApprovalChange(event: Event): Promise<void> {
  const input = event.target as HTMLInputElement
  const next = input.checked
  followError.value = ''
  followSaved.value = false
  followSaving.value = true
  try {
    const data = await updateFollowSettings(next)
    requireApproval.value = data.require_follow_approval
    input.checked = requireApproval.value
    followSaved.value = true
  } catch (error) {
    input.checked = requireApproval.value
    followError.value = errorMessage(error)
  } finally {
    followSaving.value = false
  }
}

// ---------- 修改密码 ----------
const passwordForm = reactive({ old_password: '', new_password: '' })
const passwordSaving = ref(false)
const passwordSaved = ref(false)
const passwordError = ref('')
const passwordErrors = ref<Record<string, string>>({})

function validatePassword(): Record<string, string> {
  const result: Record<string, string> = {}
  if (!passwordForm.old_password) result.old_password = '请输入当前密码'
  if (!passwordForm.new_password) {
    result.new_password = '请输入新密码'
  } else if (passwordForm.new_password.length < 8) {
    result.new_password = '新密码至少需要 8 个字符'
  } else if (passwordForm.new_password === passwordForm.old_password) {
    result.new_password = '新密码不能与当前密码相同'
  }
  return result
}

async function onChangePassword(): Promise<void> {
  passwordSaved.value = false
  passwordError.value = ''
  passwordErrors.value = validatePassword()
  if (Object.keys(passwordErrors.value).length > 0) return

  passwordSaving.value = true
  try {
    await changePassword(passwordForm.old_password, passwordForm.new_password)
    passwordForm.old_password = ''
    passwordForm.new_password = ''
    passwordSaved.value = true
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

// ---------- 退出登录 ----------
const loggingOut = ref(false)

async function onLogout(): Promise<void> {
  loggingOut.value = true
  try {
    await logout()
    await router.replace({ name: 'home' })
  } finally {
    loggingOut.value = false
  }
}

onMounted(() => void loadFollowSettings())
</script>

<template>
  <section :class="[ui.card, ui.cardPad]">
    <h2 :class="[ui.pageTitle]">账号信息</h2>
    <p :class="[ui.pageDesc]">用户名与邮箱由系统管理；邮箱变更尚未开放。</p>

    <AlertNote v-if="suspended" kind="warning">账号已被停用：读操作可用，写操作会被拒绝。</AlertNote>

    <dl :class="[ui.infoList, 'mt-[14px]']">
      <dt :class="[ui.infoTerm]">用户名</dt>
      <dd :class="[ui.infoDetails, ui.mono]">@{{ user?.username }}</dd>
      <dt :class="[ui.infoTerm]">邮箱</dt>
      <dd :class="[ui.infoDetails]">{{ user?.email }}</dd>
      <dt :class="[ui.infoTerm]">角色</dt>
      <dd :class="[ui.infoDetails]">{{ roleLabel }}</dd>
      <dt :class="[ui.infoTerm]">状态</dt>
      <dd :class="[ui.infoDetails]">
        <span v-if="suspended" :class="[ui.badge, ui.badgeDanger]">已停用</span>
        <span v-else :class="[ui.badge]">正常</span>
      </dd>
      <dt :class="[ui.infoTerm]">注册时间</dt>
      <dd :class="[ui.infoDetails]">{{ formatDate(user?.created_at) }}</dd>
    </dl>
  </section>

  <section :class="[ui.card, ui.cardPad]">
    <h2 :class="[ui.pageTitle]">修改密码</h2>
    <p :class="[ui.pageDesc]">修改成功后，其他已登录会话会立即失效，当前会话保留。</p>

    <AlertNote v-if="passwordSaved" kind="success">密码已更新，其他会话已失效。</AlertNote>
    <AlertNote v-if="passwordError" kind="error">{{ passwordError }}</AlertNote>

    <form :class="[ui.form]" novalidate @submit.prevent="onChangePassword">
      <FormField label="当前密码" input-id="account-old-password" :error="passwordErrors.old_password">
        <input
          id="account-old-password"
          v-model="passwordForm.old_password"
          :class="[ui.input]"
          type="password"
          name="old_password"
          autocomplete="current-password"
          placeholder="当前密码"
          :disabled="passwordSaving || suspended"
        />
      </FormField>

      <FormField
        label="新密码"
        input-id="account-new-password"
        :error="passwordErrors.new_password"
        hint="至少 8 个字符"
      >
        <input
          id="account-new-password"
          v-model="passwordForm.new_password"
          :class="[ui.input]"
          type="password"
          name="new_password"
          autocomplete="new-password"
          placeholder="新密码"
          :disabled="passwordSaving || suspended"
        />
      </FormField>

      <button :class="[ui.button, ui.buttonPrimary]" type="submit" :disabled="passwordSaving || suspended">
        <span v-if="passwordSaving" :class="[ui.spinner, ui.buttonSpinner]" aria-hidden="true"></span>
        {{ passwordSaving ? '提交中…' : '更新密码' }}
      </button>
    </form>
  </section>

  <section :class="[ui.card, ui.cardPad]">
    <h2 :class="[ui.pageTitle]">关注审批</h2>
    <p :class="[ui.pageDesc]">开启后，他人关注你需要你的批准；关闭后关注会直接生效。</p>

    <p v-if="followLoading" :class="[ui.loadingRow]"><span :class="[ui.spinner]" aria-hidden="true"></span> 载入中…</p>

    <template v-else>
      <label :class="[ui.switch]">
        <input
          type="checkbox"
          :class="[ui.switchInput]"
          :checked="requireApproval"
          :disabled="followSaving || suspended"
          @change="onApprovalChange"
        />
        <span>需要我批准新的关注请求</span>
      </label>

      <AlertNote v-if="followSaved" kind="success" class="mt-3!">关注审批设置已更新。</AlertNote>
      <AlertNote v-if="followError" kind="error" class="mt-3!">{{ followError }}</AlertNote>
    </template>
  </section>

  <section :class="[ui.card, ui.cardPad]">
    <h2 :class="[ui.pageTitle]">退出登录</h2>
    <p :class="[ui.pageDesc]">退出后需要重新输入用户名与密码。</p>
    <button :class="[ui.button, ui.buttonSecondary]" type="button" :disabled="loggingOut" @click="onLogout">
      <AppIcon name="logout" :size="17" />
      {{ loggingOut ? '正在退出…' : '退出登录' }}
    </button>
  </section>
</template>
