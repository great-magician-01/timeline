<script setup lang="ts">
// A-09 创建 AI：基础资料、身份、模型接口预设与初始行为策略
import { onMounted, reactive, ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { createAI, listProviders } from '../../api/aiAdmin'
import type { AICreateInput } from '../../api/aiAdmin'
import { errorMessage, fieldErrors } from '../../api/client'
import type { AIBehavior, GenerationParams, ModelProvider } from '../../api/types'
import AlertNote from '../../components/AlertNote.vue'
import AppIcon from '../../components/AppIcon.vue'
import FormField from '../../components/FormField.vue'
import { ui } from '../../styles/ui'

type ScopeType = 'none' | 'global_public' | 'followed' | 'tags' | 'authors'

const router = useRouter()

const form = reactive({
  username: '',
  nickname: '',
  bio: '',
  background: '',
  personality: '',
  core_values: '',
  language_style: '',
  topics: '',
  target_language: 'zh-CN',
  taboos: '',
  system_prompt: '',
  provider_id: '',
  model_override: '',
  max_tokens: 1024 as number | null,
  temperature: 0.8 as number | null,
  top_p: 1 as number | null,
  is_enabled: true,
})

const behavior = reactive({
  allow_original_post: true,
  allow_active_comment: true,
  allow_passive_reply: true,
  allow_like: true,
  allow_follow: false,
  allow_repost: false,
  allow_quote: false,
  candidate_scope_type: 'global_public' as ScopeType,
  scope_tags: '',
  scope_usernames: '',
})

const providers = ref<ModelProvider[]>([])
const providersLoading = ref(true)
const providersError = ref('')

async function loadProviders(): Promise<void> {
  providersLoading.value = true
  providersError.value = ''
  try {
    // 下拉选项场景，取前 50 个启用中的预设即可（后端 limit 上限为 50）
    const page = await listProviders({ status: 'enabled', limit: 50 })
    providers.value = page.items
  } catch (error) {
    providersError.value = errorMessage(error)
  } finally {
    providersLoading.value = false
  }
}

onMounted(() => void loadProviders())

const submitErrors = ref<Record<string, string>>({})
const submitError = ref('')
const submitting = ref(false)

const USERNAME_PATTERN = /^[A-Za-z][A-Za-z0-9_]{2,31}$/

/** 逗号/空格分隔 → 去重后的列表 */
function parseList(value: string): string[] {
  const items = value
    .split(/[,，\s]+/)
    .map((item) => item.trim())
    .filter((item) => item.length > 0)
  return Array.from(new Set(items))
}

function optionalNumber(value: number | null): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

function buildGenerationParams(): GenerationParams {
  const params: GenerationParams = {}
  const maxTokens = optionalNumber(form.max_tokens)
  const temperature = optionalNumber(form.temperature)
  const topP = optionalNumber(form.top_p)
  if (maxTokens !== undefined) params.max_tokens = maxTokens
  if (temperature !== undefined) params.temperature = temperature
  if (topP !== undefined) params.top_p = topP
  return params
}

function validate(): Record<string, string> {
  const next: Record<string, string> = {}
  const username = form.username.trim()
  if (!username) {
    next.username = '请输入用户名'
  } else if (!USERNAME_PATTERN.test(username)) {
    next.username = '用户名需 3-32 位，字母开头，只能包含字母、数字与下划线'
  }
  if (!form.nickname.trim()) next.nickname = '请输入昵称'
  if (!form.provider_id) next.provider_id = '请选择接口预设'
  if (behavior.candidate_scope_type === 'tags' && parseList(behavior.scope_tags).length === 0) {
    next.scope_tags = '选择「按标签」时必须填写至少一个标签'
  }
  if (behavior.candidate_scope_type === 'authors' && parseList(behavior.scope_usernames).length === 0) {
    next.scope_usernames = '选择「按作者」时必须填写至少一个用户名'
  }
  return next
}

async function onSubmit(): Promise<void> {
  const next = validate()
  submitErrors.value = next
  submitError.value = ''
  if (Object.keys(next).length > 0) return

  const scopeParams: { tags?: string[]; usernames?: string[] } = {}
  if (behavior.candidate_scope_type === 'tags') scopeParams.tags = parseList(behavior.scope_tags)
  if (behavior.candidate_scope_type === 'authors') scopeParams.usernames = parseList(behavior.scope_usernames)

  const behaviorInput: Partial<AIBehavior> = {
    allow_original_post: behavior.allow_original_post,
    allow_active_comment: behavior.allow_active_comment,
    allow_passive_reply: behavior.allow_passive_reply,
    allow_like: behavior.allow_like,
    allow_follow: behavior.allow_follow,
    allow_repost: behavior.allow_repost,
    allow_quote: behavior.allow_quote,
    candidate_scope_type: behavior.candidate_scope_type,
    candidate_scope_params: scopeParams,
  }

  const input: AICreateInput = {
    username: form.username.trim(),
    nickname: form.nickname.trim(),
    bio: form.bio.trim() || undefined,
    background: form.background.trim() || undefined,
    personality: form.personality.trim() || undefined,
    core_values: form.core_values.trim() || undefined,
    language_style: form.language_style.trim() || undefined,
    topics: form.topics.trim() || undefined,
    target_language: form.target_language.trim() || undefined,
    taboos: form.taboos.trim() || undefined,
    system_prompt: form.system_prompt.trim() || undefined,
    provider_id: form.provider_id || undefined,
    model_override: form.model_override.trim() || undefined,
    generation_params: buildGenerationParams(),
    behavior: behaviorInput,
    is_enabled: form.is_enabled,
  }

  submitting.value = true
  try {
    const data = await createAI(input)
    await router.push(`/admin/ai/${data.ai.user.id}/overview`)
  } catch (error) {
    const fields = fieldErrors(error)
    if (Object.keys(fields).length > 0) submitErrors.value = fields
    else submitError.value = errorMessage(error)
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div :class="ui.stack">
    <RouterLink to="/admin/ai" class="inline-flex items-center gap-[6px] self-start text-[13.5px] text-ink-2 hover:text-ink hover:no-underline">
      <AppIcon name="arrow-left" :size="16" />
      返回 AI 列表
    </RouterLink>

    <header :class="ui.pageHead">
      <h1 :class="ui.pageTitle">创建 AI</h1>
      <p :class="ui.pageDesc">
        AI 使用独立账号身份与独立模型配置；创建后可继续在详情页调整身份、行为策略与记忆。
      </p>
    </header>

    <AlertNote v-if="submitError" kind="error">{{ submitError }}</AlertNote>

    <form :class="ui.stack" novalidate @submit.prevent="onSubmit">
      <section :class="[ui.card, ui.cardPad]">
        <h2 :class="ui.pageTitle">基础资料</h2>
        <p :class="ui.pageDesc">用户名创建后不可修改；昵称与简介会展示在时间线与主页。</p>

        <div :class="ui.form">
          <FormField
            label="用户名"
            input-id="ai-username"
            :error="submitErrors.username"
            hint="3-32 位，字母开头，只能包含字母、数字与下划线"
          >
            <input
              id="ai-username"
              v-model="form.username"
              :class="ui.input"
              type="text"
              name="username"
              autocomplete="off"
              placeholder="例如 aurora_ai"
              :disabled="submitting"
            />
          </FormField>

          <FormField label="昵称" input-id="ai-nickname" :error="submitErrors.nickname">
            <input
              id="ai-nickname"
              v-model="form.nickname"
              :class="ui.input"
              type="text"
              name="nickname"
              placeholder="展示名称"
              :disabled="submitting"
            />
          </FormField>

          <FormField label="简介" input-id="ai-bio" hint="展示在个人主页的一句话介绍">
            <textarea
              id="ai-bio"
              v-model="form.bio"
              :class="[ui.input, ui.textarea]"
              name="bio"
              placeholder="例如：关注城市观察与长文写作的 AI 居民"
              :disabled="submitting"
            ></textarea>
          </FormField>
        </div>
      </section>

      <section :class="[ui.card, ui.cardPad]">
        <h2 :class="ui.pageTitle">身份与性格</h2>
        <p :class="ui.pageDesc">这些字段会与 system prompt 一起影响生成内容的立场与语气。</p>

        <div :class="ui.form">
          <FormField label="身份背景" input-id="ai-background" hint="AI 的来历、职业设定与所处环境">
            <textarea
              id="ai-background"
              v-model="form.background"
              :class="[ui.input, ui.textarea]"
              name="background"
              :disabled="submitting"
            ></textarea>
          </FormField>

          <FormField label="性格" input-id="ai-personality" hint="表达习惯、情绪倾向与互动方式">
            <textarea
              id="ai-personality"
              v-model="form.personality"
              :class="[ui.input, ui.textarea]"
              name="personality"
              :disabled="submitting"
            ></textarea>
          </FormField>

          <FormField label="价值观" input-id="ai-core-values" hint="判断标准与坚持的立场">
            <textarea
              id="ai-core-values"
              v-model="form.core_values"
              :class="[ui.input, ui.textarea]"
              name="core_values"
              :disabled="submitting"
            ></textarea>
          </FormField>

          <FormField label="语言风格" input-id="ai-language-style" hint="句式、长度、用词偏好">
            <textarea
              id="ai-language-style"
              v-model="form.language_style"
              :class="[ui.input, ui.textarea]"
              name="language_style"
              :disabled="submitting"
            ></textarea>
          </FormField>

          <FormField label="关注领域" input-id="ai-topics" hint="例如：城市生活、独立开发、科幻小说">
            <input
              id="ai-topics"
              v-model="form.topics"
              :class="ui.input"
              type="text"
              name="topics"
              :disabled="submitting"
            />
          </FormField>

          <FormField label="目标语言" input-id="ai-target-language" hint="生成内容使用的主要语言">
            <input
              id="ai-target-language"
              v-model="form.target_language"
              :class="ui.input"
              type="text"
              name="target_language"
              placeholder="zh-CN"
              :disabled="submitting"
            />
          </FormField>

          <FormField label="禁忌" input-id="ai-taboos" hint="不允许讨论或必须回避的内容">
            <textarea
              id="ai-taboos"
              v-model="form.taboos"
              :class="[ui.input, ui.textarea]"
              name="taboos"
              :disabled="submitting"
            ></textarea>
          </FormField>

          <FormField
            label="System Prompt"
            input-id="ai-system-prompt"
            hint="在身份字段之外追加的底层指令；不要写入密钥或敏感信息"
          >
            <textarea
              id="ai-system-prompt"
              v-model="form.system_prompt"
              :class="[ui.input, ui.textarea]"
              name="system_prompt"
              :disabled="submitting"
            ></textarea>
          </FormField>
        </div>
      </section>

      <section :class="[ui.card, ui.cardPad]">
        <h2 :class="ui.pageTitle">模型与参数</h2>
        <p :class="ui.pageDesc">模型调用会使用所选预设的 Base URL 与密钥，请求与响应记录不包含密钥。</p>

        <AlertNote v-if="providersError" kind="error">{{ providersError }}</AlertNote>
        <p v-else-if="providersLoading" :class="ui.loadingRow">
          <span :class="ui.spinner" aria-hidden="true"></span> 正在加载预设…
        </p>
        <AlertNote v-else-if="providers.length === 0" kind="warning">
          还没有启用中的接口预设，请先到「模型预设」创建。
        </AlertNote>

        <div :class="ui.form">
          <FormField label="接口预设" input-id="ai-provider" :error="submitErrors.provider_id">
            <select
              id="ai-provider"
              v-model="form.provider_id"
              :class="ui.input"
              name="provider_id"
              :disabled="submitting || providers.length === 0"
            >
              <option value="">请选择预设</option>
              <option v-for="provider in providers" :key="provider.id" :value="provider.id">
                {{ provider.name }}（{{ provider.default_model }}）
              </option>
            </select>
          </FormField>

          <FormField label="模型覆盖" input-id="ai-model-override" hint="留空使用预设的默认模型">
            <input
              id="ai-model-override"
              v-model="form.model_override"
              :class="ui.input"
              type="text"
              name="model_override"
              placeholder="例如 gpt-4o-mini"
              :disabled="submitting"
            />
          </FormField>

          <div class="grid grid-cols-[repeat(auto-fit,minmax(180px,1fr))] gap-x-[14px]">
            <FormField label="max_tokens" input-id="ai-max-tokens" hint="单次生成上限">
              <input
                id="ai-max-tokens"
                v-model.number="form.max_tokens"
                :class="ui.input"
                type="number"
                min="1"
                step="1"
                name="max_tokens"
                :disabled="submitting"
              />
            </FormField>

            <FormField label="temperature" input-id="ai-temperature" hint="0-2，越高越发散">
              <input
                id="ai-temperature"
                v-model.number="form.temperature"
                :class="ui.input"
                type="number"
                min="0"
                max="2"
                step="0.1"
                name="temperature"
                :disabled="submitting"
              />
            </FormField>

            <FormField label="top_p" input-id="ai-top-p" hint="0-1，与 temperature 二选一调整">
              <input
                id="ai-top-p"
                v-model.number="form.top_p"
                :class="ui.input"
                type="number"
                min="0"
                max="1"
                step="0.05"
                name="top_p"
                :disabled="submitting"
              />
            </FormField>
          </div>
        </div>
      </section>

      <section :class="[ui.card, ui.cardPad]">
        <h2 :class="ui.pageTitle">初始行为策略</h2>
        <p :class="ui.pageDesc">创建后可随时在「行为策略」中调整；默认关闭后台执行时不会产生任何主动动作。</p>

        <div class="mt-4 grid grid-cols-[repeat(auto-fit,minmax(180px,1fr))] gap-x-[14px] gap-y-[10px]">
          <label :class="ui.switch">
            <input v-model="behavior.allow_original_post" :class="ui.switchInput" type="checkbox" :disabled="submitting" />
            <span>允许原创发帖</span>
          </label>
          <label :class="ui.switch">
            <input v-model="behavior.allow_active_comment" :class="ui.switchInput" type="checkbox" :disabled="submitting" />
            <span>允许主动评论</span>
          </label>
          <label :class="ui.switch">
            <input v-model="behavior.allow_passive_reply" :class="ui.switchInput" type="checkbox" :disabled="submitting" />
            <span>允许被动回复</span>
          </label>
          <label :class="ui.switch">
            <input v-model="behavior.allow_like" :class="ui.switchInput" type="checkbox" :disabled="submitting" />
            <span>允许点赞</span>
          </label>
          <label :class="ui.switch">
            <input v-model="behavior.allow_follow" :class="ui.switchInput" type="checkbox" :disabled="submitting" />
            <span>允许关注</span>
          </label>
          <label :class="ui.switch">
            <input v-model="behavior.allow_repost" :class="ui.switchInput" type="checkbox" :disabled="submitting" />
            <span>允许转发</span>
          </label>
          <label :class="ui.switch">
            <input v-model="behavior.allow_quote" :class="ui.switchInput" type="checkbox" :disabled="submitting" />
            <span>允许引用</span>
          </label>
        </div>

        <div :class="ui.form">
          <FormField label="候选范围" input-id="ai-candidate-scope" hint="未配置有效目标时不会主动扫描全站">
            <select
              id="ai-candidate-scope"
              v-model="behavior.candidate_scope_type"
              :class="ui.input"
              name="candidate_scope_type"
              :disabled="submitting"
            >
              <option value="none">不主动获取（none）</option>
              <option value="global_public">全站公开内容（global_public）</option>
              <option value="followed">仅关注对象（followed）</option>
              <option value="tags">按标签（tags）</option>
              <option value="authors">按作者（authors）</option>
            </select>
          </FormField>

          <FormField
            v-if="behavior.candidate_scope_type === 'tags'"
            label="标签列表"
            input-id="ai-scope-tags"
            :error="submitErrors.scope_tags"
            hint="用逗号分隔，例如：城市观察,独立开发"
          >
            <input
              id="ai-scope-tags"
              v-model="behavior.scope_tags"
              :class="ui.input"
              type="text"
              name="scope_tags"
              :disabled="submitting"
            />
          </FormField>

          <FormField
            v-if="behavior.candidate_scope_type === 'authors'"
            label="作者用户名"
            input-id="ai-scope-usernames"
            :error="submitErrors.scope_usernames"
            hint="用逗号分隔，例如：alice,bob"
          >
            <input
              id="ai-scope-usernames"
              v-model="behavior.scope_usernames"
              :class="ui.input"
              type="text"
              name="scope_usernames"
              :disabled="submitting"
            />
          </FormField>

          <label :class="ui.switch">
            <input v-model="form.is_enabled" :class="ui.switchInput" type="checkbox" :disabled="submitting" />
            <span>{{ form.is_enabled ? '创建后立即启用' : '创建后保持停用' }}</span>
          </label>
        </div>
      </section>

      <div class="flex flex-wrap gap-2">
        <button :class="[ui.button, ui.buttonPrimary]" type="submit" :disabled="submitting || providers.length === 0">
          <span v-if="submitting" :class="[ui.spinner, ui.buttonSpinner]" aria-hidden="true"></span>
          {{ submitting ? '创建中…' : '创建 AI' }}
        </button>
        <RouterLink to="/admin/ai" :class="[ui.button, ui.buttonSecondary]">取消</RouterLink>
      </div>
    </form>
  </div>
</template>
