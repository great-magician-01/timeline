<script setup lang="ts">
import { reactive, ref } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { errorMessage, fieldErrors } from '../api/client'
import AlertNote from '../components/AlertNote.vue'
import FormField from '../components/FormField.vue'
import { login } from '../stores/auth'
import { registrationOpen } from '../stores/site'
import { ui } from '../styles/ui'

const route = useRoute()
const router = useRouter()

const form = reactive({
  identifier: typeof route.query.identifier === 'string' ? route.query.identifier : '',
  password: '',
})
const submitting = ref(false)
const formError = ref('')
const errors = ref<Record<string, string>>({})
const registeredNotice = ref(route.query.registered === '1')
const firstAdminNotice = ref(route.query.first_admin === '1')

/** 只接受同源站内路径，避免 // 与反斜杠变体造成开放重定向 */
function redirectTarget(): string {
  const raw = route.query.redirect
  if (typeof raw !== 'string' || !raw.startsWith('/')) return '/'
  try {
    const target = new URL(raw, window.location.origin)
    if (target.origin !== window.location.origin) return '/'
    return `${target.pathname}${target.search}${target.hash}`
  } catch {
    return '/'
  }
}

async function onSubmit(): Promise<void> {
  errors.value = {}
  formError.value = ''

  const localErrors: Record<string, string> = {}
  if (!form.identifier.trim()) localErrors.identifier = '请输入用户名或邮箱'
  if (!form.password) localErrors.password = '请输入密码'
  if (Object.keys(localErrors).length > 0) {
    errors.value = localErrors
    return
  }

  submitting.value = true
  try {
    await login(form.identifier.trim(), form.password)
    await router.replace(redirectTarget())
  } catch (error) {
    const fields = fieldErrors(error)
    if (Object.keys(fields).length > 0) {
      errors.value = fields
    } else {
      formError.value = errorMessage(error)
    }
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div :class="[ui.narrow]">
    <section :class="[ui.card, ui.cardPad]">
      <h1 :class="[ui.pageTitle]">登录</h1>
      <p :class="[ui.pageDesc]">使用用户名或邮箱登录，继续管理你的资料与账号。</p>

      <AlertNote v-if="firstAdminNotice" kind="success">
        首位注册用户已成为超级管理员，请登录后进入管理后台完成初始化。
      </AlertNote>
      <AlertNote v-else-if="registeredNotice" kind="success">注册成功，请使用新账号登录。</AlertNote>

      <form :class="[ui.form]" novalidate @submit.prevent="onSubmit">
        <FormField label="用户名或邮箱" input-id="login-identifier" :error="errors.identifier">
          <input
            id="login-identifier"
            v-model="form.identifier"
            :class="[ui.input]"
            type="text"
            name="identifier"
            autocomplete="username"
            placeholder="用户名或邮箱"
            :disabled="submitting"
          />
        </FormField>

        <FormField label="密码" input-id="login-password" :error="errors.password">
          <input
            id="login-password"
            v-model="form.password"
            :class="[ui.input]"
            type="password"
            name="password"
            autocomplete="current-password"
            placeholder="密码"
            :disabled="submitting"
          />
        </FormField>

        <AlertNote v-if="formError" kind="error">{{ formError }}</AlertNote>

        <button :class="[ui.button, ui.buttonPrimary, ui.buttonBlock]" type="submit" :disabled="submitting">
          <span v-if="submitting" :class="[ui.spinner, ui.buttonSpinner]" aria-hidden="true"></span>
          {{ submitting ? '登录中…' : '登录' }}
        </button>
      </form>

      <p v-if="registrationOpen" :class="[ui.muted, 'mt-4 mb-0']">
        还没有账号？<RouterLink to="/register">注册新账号</RouterLink>
      </p>
    </section>
  </div>
</template>
