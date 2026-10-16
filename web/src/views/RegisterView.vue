<script setup lang="ts">
import { reactive, ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { register } from '../api/auth'
import { errorMessage, fieldErrors } from '../api/client'
import AlertNote from '../components/AlertNote.vue'
import FormField from '../components/FormField.vue'

const router = useRouter()

const form = reactive({ username: '', email: '', password: '' })
const submitting = ref(false)
const formError = ref('')
const errors = ref<Record<string, string>>({})

// 与账号规则一致：用户名 3–32 位、字母开头、仅字母数字下划线；密码至少 8 个字符
const USERNAME_PATTERN = /^[A-Za-z][A-Za-z0-9_]{2,31}$/
const EMAIL_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

function validate(): Record<string, string> {
  const result: Record<string, string> = {}
  const username = form.username.trim()
  if (!username) {
    result.username = '请输入用户名'
  } else if (!USERNAME_PATTERN.test(username)) {
    result.username = '用户名需为 3–32 位，以字母开头，仅可使用字母、数字和下划线'
  }
  if (!form.email.trim()) {
    result.email = '请输入邮箱'
  } else if (!EMAIL_PATTERN.test(form.email.trim())) {
    result.email = '邮箱格式不正确'
  }
  if (!form.password) {
    result.password = '请输入密码'
  } else if (form.password.length < 8) {
    result.password = '密码至少需要 8 个字符'
  }
  return result
}

async function onSubmit(): Promise<void> {
  formError.value = ''
  errors.value = validate()
  if (Object.keys(errors.value).length > 0) return

  submitting.value = true
  try {
    await register(form.username.trim(), form.email.trim(), form.password)
    // 注册不自动登录：跳转登录页并提示
    await router.push({ name: 'login', query: { registered: '1', identifier: form.username.trim() } })
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
      <h1 class="page-title">注册</h1>
      <p class="page-desc">创建时间线账号；注册成功后不会自动登录。</p>

      <AlertNote v-if="formError" kind="error">{{ formError }}</AlertNote>

      <form class="form" novalidate @submit.prevent="onSubmit">
        <FormField
          label="用户名"
          input-id="register-username"
          :error="errors.username"
          hint="3–32 位，以字母开头，仅可使用字母、数字和下划线；注册后不可修改"
        >
          <input
            id="register-username"
            v-model="form.username"
            class="input"
            type="text"
            name="username"
            autocomplete="username"
            placeholder="例如 timeline_user"
            :disabled="submitting"
          />
        </FormField>

        <FormField
          label="邮箱"
          input-id="register-email"
          :error="errors.email"
          hint="用于登录；同一邮箱只能注册一个账号"
        >
          <input
            id="register-email"
            v-model="form.email"
            class="input"
            type="email"
            name="email"
            autocomplete="email"
            placeholder="you@example.com"
            :disabled="submitting"
          />
        </FormField>

        <FormField label="密码" input-id="register-password" :error="errors.password" hint="至少 8 个字符">
          <input
            id="register-password"
            v-model="form.password"
            class="input"
            type="password"
            name="password"
            autocomplete="new-password"
            placeholder="设置登录密码"
            :disabled="submitting"
          />
        </FormField>

        <button class="btn btn--primary btn--block" type="submit" :disabled="submitting">
          <span v-if="submitting" class="spinner" aria-hidden="true"></span>
          {{ submitting ? '注册中…' : '注册' }}
        </button>
      </form>

      <p class="muted switch-link">已有账号？<RouterLink to="/login">直接登录</RouterLink></p>
      <p class="muted">若站点已关闭开放注册，提交后会返回提示且不会创建账号。</p>
    </section>
  </div>
</template>

<style scoped>
.switch-link {
  margin: 16px 0 8px;
}
</style>
