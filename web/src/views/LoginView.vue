<script setup lang="ts">
import { reactive, ref } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { errorMessage, fieldErrors } from '../api/client'
import AlertNote from '../components/AlertNote.vue'
import FormField from '../components/FormField.vue'
import { login } from '../stores/auth'

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
  <div class="constrain">
    <section class="card card--pad">
      <h1 class="page-title">登录</h1>
      <p class="page-desc">使用用户名或邮箱登录，继续管理你的资料与账号。</p>

      <AlertNote v-if="registeredNotice" kind="success">注册成功，请使用新账号登录。</AlertNote>

      <form class="form" novalidate @submit.prevent="onSubmit">
        <FormField label="用户名或邮箱" input-id="login-identifier" :error="errors.identifier">
          <input
            id="login-identifier"
            v-model="form.identifier"
            class="input"
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
            class="input"
            type="password"
            name="password"
            autocomplete="current-password"
            placeholder="密码"
            :disabled="submitting"
          />
        </FormField>

        <AlertNote v-if="formError" kind="error">{{ formError }}</AlertNote>

        <button class="btn btn--primary btn--block" type="submit" :disabled="submitting">
          <span v-if="submitting" class="spinner" aria-hidden="true"></span>
          {{ submitting ? '登录中…' : '登录' }}
        </button>
      </form>

      <p class="muted switch-link">还没有账号？<RouterLink to="/register">注册新账号</RouterLink></p>
    </section>
  </div>
</template>

<style scoped>
.switch-link {
  margin: 16px 0 0;
}
</style>
