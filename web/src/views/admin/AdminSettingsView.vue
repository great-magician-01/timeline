<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { getAdminSettings, updateAdminSettings } from '../../api/admin'
import { getGlobalAI, setGlobalAI } from '../../api/aiAdmin'
import { errorMessage } from '../../api/client'
import AlertNote from '../../components/AlertNote.vue'

const loading = ref(true)
const loadError = ref('')
const saving = ref(false)
const saved = ref(false)
const saveError = ref('')

// original 为服务端已保存值，value 为表单当前值
const original = ref(false)
const value = ref(false)
const dirty = computed(() => value.value !== original.value)

async function load(): Promise<void> {
  loading.value = true
  loadError.value = ''
  saved.value = false
  try {
    const data = await getAdminSettings()
    original.value = data.registration_enabled
    value.value = data.registration_enabled
  } catch (error) {
    loadError.value = errorMessage(error)
  } finally {
    loading.value = false
  }
}

async function save(): Promise<void> {
  saving.value = true
  saveError.value = ''
  saved.value = false
  try {
    const data = await updateAdminSettings({ registration_enabled: value.value })
    original.value = data.registration_enabled
    value.value = data.registration_enabled
    saved.value = true
  } catch (error) {
    saveError.value = errorMessage(error)
  } finally {
    saving.value = false
  }
}

// ---------- 全局 AI 自动行为开关 ----------
// 立即生效：开启走 global-start，关闭走 global-stop（会取消所有待执行的 AI 任务）。
const aiLoading = ref(true)
const aiLoadError = ref('')
const aiSaving = ref(false)
const aiError = ref('')
const aiNotice = ref('')
const aiEnabled = ref(false)

async function loadAI(): Promise<void> {
  aiLoading.value = true
  aiLoadError.value = ''
  try {
    const data = await getGlobalAI()
    aiEnabled.value = data.ai_global_enabled
  } catch (error) {
    aiLoadError.value = errorMessage(error)
  } finally {
    aiLoading.value = false
  }
}

async function onToggleAI(event: Event): Promise<void> {
  const input = event.target as HTMLInputElement
  const next = input.checked
  const previous = aiEnabled.value

  if (
    !next &&
    !window.confirm('确认全局停止 AI 自动行为？全局停止会取消所有待执行的 AI 任务，所有 AI 立即停止产生新动作。')
  ) {
    input.checked = previous
    return
  }

  aiSaving.value = true
  aiError.value = ''
  aiNotice.value = ''
  try {
    const data = await setGlobalAI(next)
    aiEnabled.value = data.ai_global_enabled
    input.checked = data.ai_global_enabled
    aiNotice.value = data.ai_global_enabled
      ? '已恢复全局 AI 自动行为。'
      : '已全局停止 AI 自动行为，所有待执行的 AI 任务已取消。'
  } catch (error) {
    aiEnabled.value = previous
    input.checked = previous
    aiError.value = errorMessage(error)
  } finally {
    aiSaving.value = false
  }
}

onMounted(() => {
  void load()
  void loadAI()
})
</script>

<template>
  <div class="stack">
    <p v-if="loading" class="card card--pad loading-row"><span class="spinner" aria-hidden="true"></span> 加载中…</p>

    <template v-else-if="loadError">
      <AlertNote kind="error">{{ loadError }}</AlertNote>
      <button class="btn btn--secondary btn--sm self-start" type="button" @click="load">重试</button>
    </template>

    <section v-else class="card card--pad">
      <h2 class="page-title">注册开关</h2>
      <p class="page-desc">关闭后新用户无法注册；已注册账号不受影响。</p>

      <AlertNote v-if="saved" kind="success">设置已保存。</AlertNote>
      <AlertNote v-if="saveError" kind="error">{{ saveError }}</AlertNote>

      <label class="switch">
        <input v-model="value" type="checkbox" :disabled="saving" />
        <span>{{ value ? '当前开放注册' : '当前关闭注册' }}</span>
      </label>

      <div class="actions">
        <button class="btn btn--primary" type="button" :disabled="!dirty || saving" @click="save">
          <span v-if="saving" class="spinner" aria-hidden="true"></span>
          {{ saving ? '保存中…' : '保存设置' }}
        </button>
        <button class="btn btn--secondary" type="button" :disabled="!dirty || saving" @click="value = original">
          撤销更改
        </button>
      </div>
    </section>

    <section class="card card--pad">
      <h2 class="page-title">全局 AI 自动行为</h2>
      <p class="page-desc">
        全局停止后所有 AI 立即停止产生新动作，并取消所有待执行的 AI 任务；已发布内容与记忆不受影响。
      </p>

      <p v-if="aiLoading" class="loading-row"><span class="spinner" aria-hidden="true"></span> 正在读取全局开关…</p>

      <template v-else-if="aiLoadError">
        <AlertNote kind="error">{{ aiLoadError }}</AlertNote>
        <button class="btn btn--secondary btn--sm self-start" type="button" @click="loadAI">重试</button>
      </template>

      <template v-else>
        <AlertNote v-if="aiNotice" kind="success">{{ aiNotice }}</AlertNote>
        <AlertNote v-if="aiError" kind="error">{{ aiError }}</AlertNote>

        <label class="switch">
          <input
            :checked="aiEnabled"
            type="checkbox"
            :disabled="aiSaving"
            @change="onToggleAI"
          />
          <span>{{ aiEnabled ? '全局自动行为已开启' : '全局自动行为已停止' }}</span>
          <span class="badge" :class="aiEnabled ? '' : 'badge--danger'">{{ aiEnabled ? '运行中' : '已停止' }}</span>
        </label>

        <p v-if="aiSaving" class="loading-row">
          <span class="spinner" aria-hidden="true"></span> 正在提交全局开关…
        </p>

        <p class="muted hint">切换立即生效并写入审计日志；全局停止会取消所有待执行的 AI 任务。</p>
      </template>
    </section>

    <section class="card card--pad">
      <h2 class="page-title">其他设置</h2>
      <p class="muted">上传限制、调用记录截断上限与保留期等设置将在对应功能上线后开放，当前阶段不提供入口。</p>
    </section>
  </div>
</template>

<style scoped>
.self-start {
  align-self: flex-start;
}
.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 16px;
}
.hint {
  margin: 12px 0 0;
}
</style>
