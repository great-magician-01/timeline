<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { me } from '../../api/auth'
import { errorMessage, fieldErrors } from '../../api/client'
import { updateAvatar } from '../../api/media'
import { updateProfile } from '../../api/users'
import AlertNote from '../../components/AlertNote.vue'
import FormField from '../../components/FormField.vue'
import UserAvatar from '../../components/UserAvatar.vue'
import { authState, setCurrentUser } from '../../stores/auth'

const user = computed(() => authState.user)

const form = reactive({
  nickname: authState.user?.nickname ?? '',
  bio: authState.user?.bio ?? '',
})
const saving = ref(false)
const saved = ref(false)
const formError = ref('')
const errors = ref<Record<string, string>>({})
const suspended = computed(() => authState.user?.status === 'suspended')

// ---------- 头像上传：purpose=avatar，成功后重新拉取当前用户 ----------
const avatarInput = ref<HTMLInputElement | null>(null)
const avatarUploading = ref(false)
const avatarError = ref('')
const avatarNotice = ref('')

function pickAvatar(): void {
  if (avatarUploading.value || suspended.value) return
  avatarInput.value?.click()
}

async function onAvatarSelected(event: Event): Promise<void> {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  avatarError.value = ''
  avatarNotice.value = ''
  if (!file.type.startsWith('image/')) {
    avatarError.value = '请选择图片文件作为头像。'
    return
  }
  avatarUploading.value = true
  try {
    await updateAvatar(file)
    // 后端在头像附件落库后写回 avatar_url；重新读取当前用户以同步全站头像
    const data = await me()
    setCurrentUser(data.user)
    avatarNotice.value = '头像已更新。'
  } catch (error) {
    avatarError.value = errorMessage(error)
  } finally {
    avatarUploading.value = false
  }
}

async function onSubmit(): Promise<void> {
  errors.value = {}
  formError.value = ''
  saved.value = false

  const nickname = form.nickname.trim()
  if (!nickname) {
    errors.value = { nickname: '昵称不能为空' }
    return
  }

  saving.value = true
  try {
    const data = await updateProfile({ nickname, bio: form.bio })
    setCurrentUser(data.user)
    form.nickname = data.user.nickname
    form.bio = data.user.bio ?? ''
    saved.value = true
  } catch (error) {
    const fields = fieldErrors(error)
    if (Object.keys(fields).length > 0) {
      errors.value = fields
    } else {
      formError.value = errorMessage(error)
    }
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <section class="card card--pad">
    <h2 class="page-title">头像</h2>
    <div class="avatar-row">
      <UserAvatar v-if="user" :user="user" size="lg" />
      <div class="avatar-actions">
        <input
          ref="avatarInput"
          class="visually-hidden"
          type="file"
          accept="image/*"
          aria-label="选择头像图片"
          :disabled="avatarUploading || suspended"
          @change="onAvatarSelected"
        />
        <button
          class="btn btn--secondary btn--sm"
          type="button"
          :disabled="avatarUploading || suspended"
          @click="pickAvatar"
        >
          <span v-if="avatarUploading" class="spinner" aria-hidden="true"></span>
          {{ avatarUploading ? '上传中…' : '更换头像' }}
        </button>
        <p class="muted">支持常见图片格式；未设置头像时使用昵称首字母生成的默认头像。</p>
        <AlertNote v-if="avatarNotice" kind="success">{{ avatarNotice }}</AlertNote>
        <AlertNote v-if="avatarError" kind="error">{{ avatarError }}</AlertNote>
        <p v-if="suspended" class="muted">账号已被停用，暂时无法更换头像。</p>
      </div>
    </div>
  </section>

  <section class="card card--pad">
    <h2 class="page-title">个人资料</h2>
    <p class="page-desc">这些信息会展示在你的个人主页上。</p>

    <AlertNote v-if="suspended" kind="warning">账号已被停用，暂时无法保存资料，请联系管理员。</AlertNote>
    <AlertNote v-if="saved" kind="success">资料已保存。</AlertNote>
    <AlertNote v-if="formError" kind="error">{{ formError }}</AlertNote>

    <form class="form" novalidate @submit.prevent="onSubmit">
      <FormField label="昵称" input-id="profile-nickname" :error="errors.nickname" hint="展示在主页与其他用户可见的位置">
        <input
          id="profile-nickname"
          v-model="form.nickname"
          class="input"
          type="text"
          name="nickname"
          autocomplete="nickname"
          placeholder="你的昵称"
          :disabled="saving || suspended"
        />
      </FormField>

      <FormField label="简介" input-id="profile-bio" :error="errors.bio" :hint="`当前 ${form.bio.length} 字`">
        <textarea
          id="profile-bio"
          v-model="form.bio"
          class="textarea"
          name="bio"
          placeholder="简单介绍一下自己"
          :disabled="saving || suspended"
        ></textarea>
      </FormField>

      <button class="btn btn--primary" type="submit" :disabled="saving || suspended">
        <span v-if="saving" class="spinner" aria-hidden="true"></span>
        {{ saving ? '保存中…' : '保存资料' }}
      </button>
    </form>
  </section>
</template>

<style scoped>
.avatar-row {
  display: flex;
  align-items: center;
  gap: 14px;
  margin-top: 12px;
}
.avatar-row .muted {
  margin: 0;
}
.avatar-actions {
  display: grid;
  gap: 8px;
  min-width: 0;
}
.visually-hidden {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
}
</style>
