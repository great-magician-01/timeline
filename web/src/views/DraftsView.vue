<script setup lang="ts">
// 草稿列表（docs/PAGES_AND_PERMISSIONS.md U-02）
// 草稿只有作者可见，不进入任何公共或关注时间线（docs/REQUIREMENTS.md 6.4）。
import { ref } from 'vue'
import { RouterLink } from 'vue-router'
import AlertNote from '../components/AlertNote.vue'
import AppIcon from '../components/AppIcon.vue'
import EmptyState from '../components/EmptyState.vue'
import { errorMessage } from '../api/client'
import { deletePost, listMyDrafts } from '../api/posts'
import type { DraftSummary, Visibility } from '../api/types'
import { useCursorList } from '../composables/useCursorList'
import { ui } from '../styles/ui'
import { relativeTime } from '../utils/format'

const { items, loading, loadingMore, hasMore, error, loadMore, reload } = useCursorList<
  DraftSummary,
  Record<string, never>
>({
  fetch: (params) => listMyDrafts(params),
  params: () => ({}),
})

const deletingId = ref('')
const notice = ref('')
const actionError = ref('')

function visibilityLabel(visibility: Visibility): string {
  if (visibility === 'public') return '公开'
  if (visibility === 'followers') return '仅关注者'
  return '仅自己'
}

/** 逻辑删除；同时取消未执行的定时任务由后端保证 */
async function removeDraft(draft: DraftSummary): Promise<void> {
  if (!window.confirm('删除后草稿不再出现在列表中，其中附件的历史记录仍会保留。确认删除？')) return
  deletingId.value = draft.id
  notice.value = ''
  actionError.value = ''
  try {
    await deletePost(draft.id)
    items.value = items.value.filter((item) => item.id !== draft.id)
    notice.value = '草稿已删除'
  } catch (err) {
    actionError.value = errorMessage(err)
  } finally {
    deletingId.value = ''
  }
}
</script>

<template>
  <div :class="[ui.constrain]">
    <div :class="[ui.stack]">
      <header :class="[ui.pageHead]">
        <h1 :class="[ui.pageTitle]">草稿</h1>
        <p :class="[ui.pageDesc]">草稿只有你自己可见；发布前可以随时继续编辑或删除。</p>
      </header>

      <AlertNote v-if="notice" kind="success">{{ notice }}</AlertNote>
      <AlertNote v-if="actionError" kind="error">{{ actionError }}</AlertNote>

      <div v-if="loading" :class="[ui.card, ui.cardPad]">
        <p :class="[ui.loadingRow]"><span :class="[ui.spinner]" aria-hidden="true"></span> 正在加载草稿…</p>
      </div>

      <template v-else-if="error && items.length === 0">
        <AlertNote kind="error">{{ error }}</AlertNote>
        <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'self-start']" type="button" @click="reload()">重试</button>
      </template>

      <section v-else-if="items.length === 0" :class="[ui.card]">
        <EmptyState
          icon="file-text"
          title="还没有草稿"
          description="在发布页面保存草稿后会出现在这里。"
        />
      </section>

      <template v-else>
        <section :class="[ui.card, 'overflow-hidden']">
          <article
            v-for="draft in items"
            :key="draft.id"
            class="flex items-start gap-3 border-b border-solid border-border px-4 py-[14px] [&:last-of-type]:border-b-0 shell-down:flex-col shell-down:items-stretch"
          >
            <div class="min-w-0 flex-1">
              <p class="m-0 text-[14.5px] leading-[1.6] [word-break:break-word]">{{ draft.plain_text || '（暂无正文）' }}</p>
              <p class="mt-1.5 mb-0 flex flex-wrap items-center gap-2">
                <span :class="[ui.badge, ui.badgeMuted]">草稿</span>
                <span :class="[ui.badge, draft.visibility !== 'public' && ui.badgeMuted]">
                  {{ visibilityLabel(draft.visibility) }}
                </span>
                <span v-if="draft.attachment_count > 0" :class="[ui.muted, 'text-[12.5px]!']">{{ draft.attachment_count }} 个附件</span>
                <span :class="[ui.muted, 'text-[12.5px]!']">更新于 {{ relativeTime(draft.updated_at) }}</span>
              </p>
            </div>

            <div class="flex flex-wrap gap-2">
              <RouterLink
                :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'shell-down:flex-1']"
                :to="{ name: 'draft-edit', params: { draftId: draft.id } }"
              >
                <AppIcon name="pencil" :size="15" /> 继续编辑
              </RouterLink>
              <button
                :class="[ui.button, ui.buttonDanger, ui.buttonSmall, 'shell-down:flex-1']"
                type="button"
                :disabled="deletingId === draft.id"
                @click="removeDraft(draft)"
              >
                <AppIcon v-if="deletingId !== draft.id" name="trash" :size="15" />
                {{ deletingId === draft.id ? '删除中…' : '删除' }}
              </button>
            </div>
          </article>

          <div class="flex justify-center px-4 py-[14px]">
            <button
              v-if="hasMore"
              :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]"
              type="button"
              :disabled="loadingMore"
              @click="loadMore()"
            >
              <span v-if="loadingMore" :class="[ui.spinner, 'size-[14px]! border-border! border-t-primary! opacity-80']" aria-hidden="true"></span>
              {{ loadingMore ? '正在加载…' : '加载更多' }}
            </button>
            <p v-else :class="[ui.muted, 'm-0']">已加载全部 {{ items.length }} 份草稿</p>
          </div>
        </section>

        <AlertNote v-if="error" kind="error">{{ error }}</AlertNote>
      </template>
    </div>
  </div>
</template>
