<script setup lang="ts">
// A-11 AI 身份与模型：资料身份字段、system prompt、接口预设与生成参数
// 子页面自行读取路由参数 aiId（由 /admin/ai/:id 提供）
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { getAI, listProviders, updateAI, updateAIModel } from '../../api/aiAdmin'
import { errorMessage, fieldErrors } from '../../api/client'
import type { GenerationParams, ModelProvider } from '../../api/types'
import AlertNote from '../../components/AlertNote.vue'
import FormField from '../../components/FormField.vue'

const route = useRoute()
const aiId = computed(() => String(route.params.id ?? ''))

const loading = ref(true)
const loadError = ref('')

const profileForm = reactive({
  nickname: '',
  bio: '',
  background: '',
  personality: '',
  core_values: '',
  language_style: '',
  topics: '',
  target_language: '',
  taboos: '',
  system_prompt: '',
})

const modelForm = reactive({
  provider_id: '',
  model_override: '',
  max_tokens: null as number | null,
  temperature: null as number | null,
  top_p: null as number | null,
  request_timeout_seconds: null as number | null,
})

const providers = ref<ModelProvider[]>([])
const providersError = ref('')

/** 请求序号：快速切换 :id 时作废旧响应，避免旧 AI 覆盖新 AI */
let loadSequence = 0

async function load(): Promise<void> {
  const current = ++loadSequence
  loading.value = true
  loadError.value = ''
  try {
    const data = await getAI(aiId.value)
    if (current !== loadSequence) return
    const { user, profile, model } = data.ai
    profileForm.nickname = user.nickname
    profileForm.bio = data.ai.bio
    profileForm.background = profile.background
    profileForm.personality = profile.personality
    profileForm.core_values = profile.core_values
    profileForm.language_style = profile.language_style
    profileForm.topics = profile.topics
    profileForm.target_language = profile.target_language
    profileForm.taboos = profile.taboos
    profileForm.system_prompt = profile.system_prompt

    modelForm.provider_id = model.provider_id ?? ''
    modelForm.model_override = model.model_override
    modelForm.max_tokens = model.generation_params.max_tokens ?? null
    modelForm.temperature = model.generation_params.temperature ?? null
    modelForm.top_p = model.generation_params.top_p ?? null
    modelForm.request_timeout_seconds = model.request_timeout_seconds
  } catch (error) {
    if (current !== loadSequence) return
    loadError.value = errorMessage(error)
  } finally {
    if (current === loadSequence) loading.value = false
  }
}

async function loadProviders(): Promise<void> {
  providersError.value = ''
  try {
    // 不带 status 过滤以包含当前已选但已停用的预设（选项文案会标注「已停用」）
    const page = await listProviders({ limit: 50 })
    providers.value = page.items
  } catch (error) {
    providersError.value = errorMessage(error)
  }
}

onMounted(() => {
  void load()
  void loadProviders()
})

watch(aiId, () => void load())

// ---------- 资料与身份 ----------
const profileErrors = ref<Record<string, string>>({})
const profileError = ref('')
const profileNotice = ref('')
const savingProfile = ref(false)

async function onSaveProfile(): Promise<void> {
  const next: Record<string, string> = {}
  if (!profileForm.nickname.trim()) next.nickname = '请输入昵称'
  profileErrors.value = next
  profileError.value = ''
  profileNotice.value = ''
  if (Object.keys(next).length > 0) return

  savingProfile.value = true
  try {
    await updateAI(aiId.value, {
      nickname: profileForm.nickname.trim(),
      bio: profileForm.bio.trim(),
      background: profileForm.background.trim(),
      personality: profileForm.personality.trim(),
      core_values: profileForm.core_values.trim(),
      language_style: profileForm.language_style.trim(),
      topics: profileForm.topics.trim(),
      target_language: profileForm.target_language.trim(),
      taboos: profileForm.taboos.trim(),
      system_prompt: profileForm.system_prompt.trim(),
    })
    profileNotice.value = '资料与身份已保存。'
  } catch (error) {
    const fields = fieldErrors(error)
    if (Object.keys(fields).length > 0) profileErrors.value = fields
    else profileError.value = errorMessage(error)
  } finally {
    savingProfile.value = false
  }
}

// ---------- 模型设置 ----------
const modelErrors = ref<Record<string, string>>({})
const modelError = ref('')
const modelNotice = ref('')
const savingModel = ref(false)

function optionalNumber(value: number | null): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

async function onSaveModel(): Promise<void> {
  const next: Record<string, string> = {}
  if (!modelForm.provider_id) next.provider_id = '请选择接口预设'
  const temperature = optionalNumber(modelForm.temperature)
  if (temperature !== undefined && (temperature < 0 || temperature > 2)) {
    next.temperature = 'temperature 需在 0 到 2 之间'
  }
  const topP = optionalNumber(modelForm.top_p)
  if (topP !== undefined && (topP < 0 || topP > 1)) {
    next.top_p = 'top_p 需在 0 到 1 之间'
  }
  const timeout = optionalNumber(modelForm.request_timeout_seconds)
  if (timeout !== undefined && timeout <= 0) {
    next.request_timeout_seconds = '超时时间需大于 0 秒'
  }
  modelErrors.value = next
  modelError.value = ''
  modelNotice.value = ''
  if (Object.keys(next).length > 0) return

  const generationParams: GenerationParams = {}
  const maxTokens = optionalNumber(modelForm.max_tokens)
  if (maxTokens !== undefined) generationParams.max_tokens = maxTokens
  if (temperature !== undefined) generationParams.temperature = temperature
  if (topP !== undefined) generationParams.top_p = topP

  savingModel.value = true
  try {
    await updateAIModel(aiId.value, {
      provider_id: modelForm.provider_id,
      model_override: modelForm.model_override.trim(),
      generation_params: generationParams,
      request_timeout_seconds: timeout,
    })
    modelNotice.value = '模型设置已保存。'
  } catch (error) {
    const fields = fieldErrors(error)
    if (Object.keys(fields).length > 0) modelErrors.value = fields
    else modelError.value = errorMessage(error)
  } finally {
    savingModel.value = false
  }
}
</script>

<template>
  <div class="stack">
    <p v-if="loading" class="card card--pad loading-row"><span class="spinner" aria-hidden="true"></span> 加载中…</p>

    <template v-else-if="loadError">
      <AlertNote kind="error">{{ loadError }}</AlertNote>
      <button class="btn btn--secondary btn--sm self-start" type="button" @click="load">重试</button>
    </template>

    <template v-else>
      <section class="card card--pad">
        <h2 class="page-title">资料与身份</h2>
        <p class="page-desc">昵称与简介对外展示；其余字段只用于生成内容，不会直接出现在界面上。</p>

        <AlertNote v-if="profileNotice" kind="success">{{ profileNotice }}</AlertNote>
        <AlertNote v-if="profileError" kind="error">{{ profileError }}</AlertNote>

        <form class="form" novalidate @submit.prevent="onSaveProfile">
          <div class="grid-two">
            <FormField label="昵称" input-id="identity-nickname" :error="profileErrors.nickname">
              <input
                id="identity-nickname"
                v-model="profileForm.nickname"
                class="input"
                type="text"
                name="nickname"
                :disabled="savingProfile"
              />
            </FormField>

            <FormField label="目标语言" input-id="identity-target-language" :error="profileErrors.target_language">
              <input
                id="identity-target-language"
                v-model="profileForm.target_language"
                class="input"
                type="text"
                name="target_language"
                :disabled="savingProfile"
              />
            </FormField>
          </div>

          <FormField label="简介" input-id="identity-bio" :error="profileErrors.bio">
            <textarea
              id="identity-bio"
              v-model="profileForm.bio"
              class="textarea"
              name="bio"
              :disabled="savingProfile"
            ></textarea>
          </FormField>

          <FormField label="身份背景" input-id="identity-background" :error="profileErrors.background">
            <textarea
              id="identity-background"
              v-model="profileForm.background"
              class="textarea"
              name="background"
              :disabled="savingProfile"
            ></textarea>
          </FormField>

          <FormField label="性格" input-id="identity-personality" :error="profileErrors.personality">
            <textarea
              id="identity-personality"
              v-model="profileForm.personality"
              class="textarea"
              name="personality"
              :disabled="savingProfile"
            ></textarea>
          </FormField>

          <FormField label="价值观" input-id="identity-core-values" :error="profileErrors.core_values">
            <textarea
              id="identity-core-values"
              v-model="profileForm.core_values"
              class="textarea"
              name="core_values"
              :disabled="savingProfile"
            ></textarea>
          </FormField>

          <FormField label="语言风格" input-id="identity-language-style" :error="profileErrors.language_style">
            <textarea
              id="identity-language-style"
              v-model="profileForm.language_style"
              class="textarea"
              name="language_style"
              :disabled="savingProfile"
            ></textarea>
          </FormField>

          <FormField label="关注领域" input-id="identity-topics" :error="profileErrors.topics">
            <input
              id="identity-topics"
              v-model="profileForm.topics"
              class="input"
              type="text"
              name="topics"
              :disabled="savingProfile"
            />
          </FormField>

          <FormField label="禁忌" input-id="identity-taboos" :error="profileErrors.taboos">
            <textarea
              id="identity-taboos"
              v-model="profileForm.taboos"
              class="textarea"
              name="taboos"
              :disabled="savingProfile"
            ></textarea>
          </FormField>

          <FormField
            label="System Prompt"
            input-id="identity-system-prompt"
            :error="profileErrors.system_prompt"
            hint="不要写入密钥或敏感信息；模型输出不会直接执行任何操作"
          >
            <textarea
              id="identity-system-prompt"
              v-model="profileForm.system_prompt"
              class="textarea"
              name="system_prompt"
              :disabled="savingProfile"
            ></textarea>
          </FormField>

          <div class="actions">
            <button class="btn btn--primary" type="submit" :disabled="savingProfile">
              <span v-if="savingProfile" class="spinner" aria-hidden="true"></span>
              {{ savingProfile ? '保存中…' : '保存资料与身份' }}
            </button>
          </div>
        </form>
      </section>

      <section class="card card--pad">
        <h2 class="page-title">模型设置</h2>
        <p class="page-desc">切换预设或调整生成参数后，仅影响之后的调用。</p>

        <AlertNote v-if="modelNotice" kind="success">{{ modelNotice }}</AlertNote>
        <AlertNote v-if="modelError" kind="error">{{ modelError }}</AlertNote>
        <AlertNote v-if="providersError" kind="error">{{ providersError }}</AlertNote>

        <form class="form" novalidate @submit.prevent="onSaveModel">
          <FormField label="接口预设" input-id="identity-provider" :error="modelErrors.provider_id">
            <select
              id="identity-provider"
              v-model="modelForm.provider_id"
              class="input"
              name="provider_id"
              :disabled="savingModel"
            >
              <option value="">请选择预设</option>
              <option v-for="provider in providers" :key="provider.id" :value="provider.id">
                {{ provider.name }}（{{ provider.default_model }}）{{ provider.is_enabled ? '' : ' · 已停用' }}
              </option>
            </select>
          </FormField>

          <FormField
            label="模型覆盖"
            input-id="identity-model-override"
            :error="modelErrors.model_override"
            hint="留空使用预设的默认模型"
          >
            <input
              id="identity-model-override"
              v-model="modelForm.model_override"
              class="input"
              type="text"
              name="model_override"
              :disabled="savingModel"
            />
          </FormField>

          <div class="grid-three">
            <FormField label="max_tokens" input-id="identity-max-tokens" :error="modelErrors.max_tokens">
              <input
                id="identity-max-tokens"
                v-model.number="modelForm.max_tokens"
                class="input"
                type="number"
                min="1"
                step="1"
                name="max_tokens"
                :disabled="savingModel"
              />
            </FormField>

            <FormField label="temperature" input-id="identity-temperature" :error="modelErrors.temperature">
              <input
                id="identity-temperature"
                v-model.number="modelForm.temperature"
                class="input"
                type="number"
                min="0"
                max="2"
                step="0.1"
                name="temperature"
                :disabled="savingModel"
              />
            </FormField>

            <FormField label="top_p" input-id="identity-top-p" :error="modelErrors.top_p">
              <input
                id="identity-top-p"
                v-model.number="modelForm.top_p"
                class="input"
                type="number"
                min="0"
                max="1"
                step="0.05"
                name="top_p"
                :disabled="savingModel"
              />
            </FormField>
          </div>

          <FormField
            label="请求超时（秒）"
            input-id="identity-timeout"
            :error="modelErrors.request_timeout_seconds"
            hint="超时后任务按失败处理，可在任务页重试"
          >
            <input
              id="identity-timeout"
              v-model.number="modelForm.request_timeout_seconds"
              class="input"
              type="number"
              min="1"
              step="1"
              name="request_timeout_seconds"
              :disabled="savingModel"
            />
          </FormField>

          <div class="actions">
            <button class="btn btn--primary" type="submit" :disabled="savingModel">
              <span v-if="savingModel" class="spinner" aria-hidden="true"></span>
              {{ savingModel ? '保存中…' : '保存模型设置' }}
            </button>
          </div>
        </form>
      </section>
    </template>
  </div>
</template>

<style scoped>
.self-start {
  align-self: flex-start;
}
.grid-two {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 0 14px;
}
.grid-three {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(160px, 1fr));
  gap: 0 14px;
}
.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
</style>
