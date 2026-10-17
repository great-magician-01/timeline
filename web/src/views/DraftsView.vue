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
  <div class="constrain">
    <div class="stack">
      <header class="page-head">
        <h1 class="page-title">草稿</h1>
        <p class="page-desc">草稿只有你自己可见；发布前可以随时继续编辑或删除。</p>
      </header>

      <AlertNote v-if="notice" kind="success">{{ notice }}</AlertNote>
      <AlertNote v-if="actionError" kind="error">{{ actionError }}</AlertNote>

      <div v-if="loading" class="card card--pad">
        <p class="loading-row"><span class="spinner" aria-hidden="true"></span> 正在加载草稿…</p>
      </div>

      <template v-else-if="error && items.length === 0">
        <AlertNote kind="error">{{ error }}</AlertNote>
        <button class="btn btn--secondary btn--sm retry" type="button" @click="reload()">重试</button>
      </template>

      <section v-else-if="items.length === 0" class="card">
        <EmptyState
          icon="file-text"
          title="还没有草稿"
          description="在发布页面保存草稿后会出现在这里。"
        />
      </section>

      <template v-else>
        <section class="card draft-list">
          <article v-for="draft in items" :key="draft.id" class="draft">
            <div class="draft-main">
              <p class="draft-summary">{{ draft.plain_text || '（暂无正文）' }}</p>
              <p class="draft-meta">
                <span class="badge badge--muted">草稿</span>
                <span class="badge" :class="{ 'badge--muted': draft.visibility !== 'public' }">
                  {{ visibilityLabel(draft.visibility) }}
                </span>
                <span v-if="draft.attachment_count > 0" class="muted">{{ draft.attachment_count }} 个附件</span>
                <span class="muted">更新于 {{ relativeTime(draft.updated_at) }}</span>
              </p>
            </div>

            <div class="draft-actions">
              <RouterLink
                class="btn btn--secondary btn--sm"
                :to="{ name: 'draft-edit', params: { draftId: draft.id } }"
              >
                <AppIcon name="pencil" :size="15" /> 继续编辑
              </RouterLink>
              <button
                class="btn btn--danger btn--sm"
                type="button"
                :disabled="deletingId === draft.id"
                @click="removeDraft(draft)"
              >
                <AppIcon v-if="deletingId !== draft.id" name="trash" :size="15" />
                {{ deletingId === draft.id ? '删除中…' : '删除' }}
              </button>
            </div>
          </article>

          <div class="list-foot">
            <button
              v-if="hasMore"
              class="btn btn--secondary btn--sm load-more"
              type="button"
              :disabled="loadingMore"
              @click="loadMore()"
            >
              <span v-if="loadingMore" class="spinner" aria-hidden="true"></span>
              {{ loadingMore ? '正在加载…' : '加载更多' }}
            </button>
            <p v-else class="muted">已加载全部 {{ items.length }} 份草稿</p>
          </div>
        </section>

        <AlertNote v-if="error" kind="error">{{ error }}</AlertNote>
      </template>
    </div>
  </div>
</template>

<style scoped>
.retry {
  align-self: flex-start;
}
.draft-list {
  overflow: hidden;
}
.draft {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 14px 16px;
  border-bottom: 1px solid var(--border);
}
.draft:last-of-type {
  border-bottom: 0;
}
.draft-main {
  min-width: 0;
  flex: 1;
}
.draft-summary {
  margin: 0;
  font-size: 14.5px;
  line-height: 1.6;
  word-break: break-word;
}
.draft-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin: 6px 0 0;
}
.draft-meta .muted {
  font-size: 12.5px;
}
.draft-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.list-foot {
  display: flex;
  justify-content: center;
  padding: 14px 16px;
}
.list-foot .muted {
  margin: 0;
}
.load-more .spinner {
  border-color: var(--border);
  border-top-color: var(--primary);
}
@media (max-width: 880px) {
  .draft {
    flex-direction: column;
    align-items: stretch;
  }
  .draft-actions .btn {
    flex: 1;
  }
}
</style>
