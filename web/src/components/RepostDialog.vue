<script setup lang="ts">
// 转发弹窗：留空提交为纯转发；填写内容则发布引用转发；已纯转发时可直接取消。
// 弹层样式对齐 AttachmentGallery：固定遮罩、点遮罩或 Esc 关闭，提交中禁止关闭。
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import AppIcon from './AppIcon.vue'
import EmojiPicker from './EmojiPicker.vue'
import UserAvatar from './UserAvatar.vue'
import type { RepostDialogResult } from './RepostDialog.types'
import { errorMessage } from '../api/client'
import { createPost } from '../api/posts'
import { repostPost, unrepostPost } from '../api/social'
import type { PostBrief } from '../api/types'
import { ui } from '../styles/ui'
import { insertAtSelection } from '../utils/emoji'
import { plainSummary } from '../utils/format'
import { textToParagraphHTML } from '../utils/text'

const props = defineProps<{
  /** 被转发的原帖（非转发包装） */
  post: PostBrief
  /** 当前是否已纯转发 */
  reposted: boolean
}>()

const emit = defineEmits<{
  (event: 'close'): void
  (event: 'completed', payload: RepostDialogResult): void
}>()

const text = ref('')
const error = ref('')
/** 当前提交动作：pure=纯转发，quote=引用转发，unrepost=取消纯转发 */
const submitting = ref<'' | 'pure' | 'quote' | 'unrepost'>('')
const textarea = ref<HTMLTextAreaElement | null>(null)

const trimmedText = computed(() => text.value.trim())
const busy = computed(() => submitting.value !== '')
/** 已纯转发且未填写内容时禁用主按钮，避免重复纯转发；填写内容仍可发布引用转发 */
const pureDisabled = computed(() => props.reposted && trimmedText.value === '')
const canSubmit = computed(() => !busy.value && !pureDisabled.value)
const submitLabel = computed(() => {
  if (submitting.value === 'pure' || submitting.value === 'quote') return '提交中…'
  return '转发'
})
const pureHint = computed(() => (pureDisabled.value ? '已转发，可输入内容后发布引用转发' : ''))

function onEmoji(emoji: string): void {
  if (textarea.value) insertAtSelection(textarea.value, emoji)
}

function requestClose(): void {
  if (busy.value) return
  emit('close')
}

function onKeydown(event: KeyboardEvent): void {
  if (event.key !== 'Escape') return
  event.stopPropagation()
  requestClose()
}

async function submit(): Promise<void> {
  if (!canSubmit.value) return
  const value = trimmedText.value
  const mode = value === '' ? 'pure' : 'quote'
  submitting.value = mode
  error.value = ''
  try {
    if (mode === 'quote') {
      const result = await createPost({
        content_format: 'richtext',
        content_source: textToParagraphHTML(value),
        visibility: 'public',
        attachment_ids: [],
        quoted_post_id: props.post.id,
        action: 'publish',
      })
      emit('completed', { kind: 'quote', post: result.post })
    } else {
      const result = await repostPost(props.post.id)
      emit('completed', { kind: 'pure', result })
    }
  } catch (err) {
    error.value = errorMessage(err)
  } finally {
    submitting.value = ''
  }
}

async function cancelRepost(): Promise<void> {
  if (busy.value) return
  submitting.value = 'unrepost'
  error.value = ''
  try {
    const result = await unrepostPost(props.post.id)
    emit('completed', { kind: 'unrepost', result })
  } catch (err) {
    error.value = errorMessage(err)
  } finally {
    submitting.value = ''
  }
}

onMounted(() => {
  window.addEventListener('keydown', onKeydown)
  void nextTick(() => textarea.value?.focus())
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeydown)
})
</script>

<template>
  <div
    class="fixed inset-0 z-[60] flex items-center justify-center bg-overlay p-6"
    role="dialog"
    aria-modal="true"
    aria-labelledby="repost-dialog-title"
    @click.self="requestClose"
  >
    <section :class="[ui.card, ui.cardPad, 'grid w-full max-w-[520px] gap-3']">
      <header class="flex items-center gap-2">
        <h2 id="repost-dialog-title" :class="ui.pageTitle">转发</h2>
        <button
          type="button"
          class="ml-auto flex size-8 items-center justify-center rounded-[8px] text-ink-3 hover-not-disabled:bg-hover hover-not-disabled:text-ink disabled:cursor-not-allowed disabled:opacity-60"
          aria-label="关闭"
          :disabled="busy"
          @click="requestClose"
        >
          <AppIcon name="x" :size="18" />
        </button>
      </header>

      <blockquote class="m-0 rounded-[10px] border border-solid border-border bg-surface-subtle px-3 py-[10px]">
        <div class="mb-[6px] flex min-w-0 items-center gap-2 text-[13.5px] text-ink">
          <UserAvatar :user="post.author" size="sm" />
          <strong>{{ post.author.nickname || post.author.username }}</strong>
          <span class="text-[12.5px] text-ink-3">@{{ post.author.username }}</span>
        </div>
        <p :class="[ui.muted, 'm-0 [word-break:break-word]']">{{ plainSummary(post.content_html, 80) }}</p>
      </blockquote>

      <div class="grid gap-[6px]">
        <div class="flex items-center justify-between gap-2">
          <label for="repost-content" class="text-[13px] font-semibold text-ink-2">转发内容（可选）</label>
          <EmojiPicker :disabled="busy" align="right" @select="onEmoji">
            <template #trigger="{ toggle, open: pickerOpen }">
              <button
                type="button"
                class="flex size-[34px] items-center justify-center rounded-[8px] text-ink-3 transition-[background,color] duration-150 ease-[ease] hover:bg-hover hover:text-ink disabled:cursor-not-allowed disabled:opacity-[0.55]"
                title="插入表情"
                aria-label="插入表情"
                aria-haspopup="dialog"
                :aria-expanded="pickerOpen"
                :disabled="busy"
                @click="toggle"
              >
                <AppIcon name="smile" :size="18" />
              </button>
            </template>
          </EmojiPicker>
        </div>
        <textarea
          id="repost-content"
          ref="textarea"
          v-model="text"
          :class="[ui.input, ui.textarea]"
          rows="4"
          placeholder="说点什么…（留空则直接转发）"
          aria-label="转发内容"
          :disabled="busy"
        ></textarea>
      </div>
      <p :class="[ui.muted, 'm-0']">留空为纯转发；填写内容将作为引用转发发布。</p>
      <p v-if="pureDisabled" :class="[ui.muted, 'm-0']">已转发；填写内容可发布引用转发，或点击「取消转发」取消。</p>

      <p v-if="error" :class="[ui.alert, ui.alertVariants.error.root]">{{ error }}</p>

      <footer class="flex flex-wrap items-center gap-2">
        <button
          v-if="reposted"
          type="button"
          class="rounded-[8px] px-2 py-[5px] text-[13px] text-ink-3 hover-not-disabled:bg-hover hover-not-disabled:text-ink disabled:cursor-not-allowed disabled:opacity-60"
          :disabled="busy"
          @click="cancelRepost"
        >
          {{ submitting === 'unrepost' ? '取消转发中…' : '取消转发' }}
        </button>
        <div class="ml-auto flex items-center gap-2">
          <button
            :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]"
            type="button"
            :disabled="busy"
            @click="requestClose"
          >
            取消
          </button>
          <button
            :class="[ui.button, ui.buttonPrimary, ui.buttonSmall]"
            type="button"
            :disabled="!canSubmit"
            :title="pureHint"
            @click="submit"
          >
            <span
              v-if="submitting === 'pure' || submitting === 'quote'"
              :class="[ui.spinner, ui.buttonSpinner]"
              aria-hidden="true"
            ></span>
            {{ submitLabel }}
          </button>
        </div>
      </footer>
    </section>
  </div>
</template>
