<script setup lang="ts">
// 定时发布列表（docs/PAGES_AND_PERMISSIONS.md U-04）
// 定时帖在到达发布时间前不对其他人公开，作者可以编辑、取消或删除（docs/REQUIREMENTS.md 6.4）。
import { ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import AlertNote from '../components/AlertNote.vue'
import AppIcon from '../components/AppIcon.vue'
import EmptyState from '../components/EmptyState.vue'
import { errorMessage } from '../api/client'
import { cancelSchedule, deletePost, listMyScheduled, publishPost } from '../api/posts'
import type { DraftSummary, Visibility } from '../api/types'
import { useCursorList } from '../composables/useCursorList'
import { absoluteTime } from '../utils/format'

const router = useRouter()

const { items, loading, loadingMore, hasMore, error, loadMore, reload } = useCursorList<
  DraftSummary,
  Record<string, never>
>({
  fetch: (params) => listMyScheduled(params),
  params: () => ({}),
})

/** 正在处理的帖子 ID，避免同一时间触发多个操作 */
const busyId = ref('')
const notice = ref('')
const actionError = ref('')

function visibilityLabel(visibility: Visibility): string {
  if (visibility === 'public') return '公开'
  if (visibility === 'followers') return '仅关注者'
  return '仅自己'
}

/** 定时列表项状态：定时中 / 发布失败 / 已暂停（作业暂停或取消后回到草稿）/ 已发布 */
function statusLabel(item: DraftSummary): string {
  if (item.status === 'failed' || item.publish_failed) return '发布失败'
  if (item.status === 'paused') return '已暂停'
  if (item.status === 'scheduled') return '定时中'
  if (item.status === 'published') return '已发布'
  return '已暂停'
}

function statusClass(item: DraftSummary): string {
  if (item.status === 'failed' || item.publish_failed) return 'badge--danger'
  if (item.status === 'draft' || item.status === 'paused') return 'badge--muted'
  return ''
}

async function cancel(item: DraftSummary): Promise<void> {
  if (busyId.value) return
  busyId.value = item.id
  notice.value = ''
  actionError.value = ''
  try {
    await cancelSchedule(item.id)
    await reload()
    notice.value = '已取消定时，帖子回到草稿，可在草稿列表中继续编辑。'
  } catch (err) {
    actionError.value = errorMessage(err)
  } finally {
    busyId.value = ''
  }
}

async function publishNow(item: DraftSummary): Promise<void> {
  if (busyId.value) return
  busyId.value = item.id
  notice.value = ''
  actionError.value = ''
  try {
    await publishPost(item.id)
    await router.push({ name: 'post-detail', params: { postId: item.id } })
  } catch (err) {
    actionError.value = errorMessage(err)
  } finally {
    busyId.value = ''
  }
}

async function removeItem(item: DraftSummary): Promise<void> {
  if (busyId.value) return
  if (!window.confirm('删除后帖子不再出现在列表中，未执行的定时发布任务会同时取消。确认删除？')) return
  busyId.value = item.id
  notice.value = ''
  actionError.value = ''
  try {
    await deletePost(item.id)
    items.value = items.value.filter((row) => row.id !== item.id)
    notice.value = '已删除该定时帖'
  } catch (err) {
    actionError.value = errorMessage(err)
  } finally {
    busyId.value = ''
  }
}
</script>

<template>
  <div class="constrain">
    <div class="stack">
      <header class="page-head">
        <h1 class="page-title">定时发布</h1>
        <p class="page-desc">到点后由服务端按帖子当前的可见性自动发布。</p>
      </header>

      <AlertNote kind="info">
        定时帖在到达发布时间前不对其他用户公开；应用重启不会丢失定时任务。
      </AlertNote>
      <AlertNote v-if="notice" kind="success">{{ notice }}</AlertNote>
      <AlertNote v-if="actionError" kind="error">{{ actionError }}</AlertNote>

      <div v-if="loading" class="card card--pad">
        <p class="loading-row"><span class="spinner" aria-hidden="true"></span> 正在加载定时帖…</p>
      </div>

      <template v-else-if="error && items.length === 0">
        <AlertNote kind="error">{{ error }}</AlertNote>
        <button class="btn btn--secondary btn--sm retry" type="button" @click="reload()">重试</button>
      </template>

      <section v-else-if="items.length === 0" class="card">
        <EmptyState
          icon="calendar"
          title="没有待发布的定时帖"
          description="在发布页面选择「定时发布」后，帖子会出现在这里。"
        />
      </section>

      <template v-else>
        <section class="card scheduled-list">
          <article v-for="item in items" :key="item.id" class="scheduled">
            <div class="scheduled-main">
              <p class="scheduled-time">
                <AppIcon name="calendar" :size="15" />
                发布时间 {{ absoluteTime(item.scheduled_at) || '未设置' }}
              </p>
              <p class="scheduled-summary">{{ item.plain_text || '（暂无正文）' }}</p>
              <p class="scheduled-meta">
                <span class="badge" :class="statusClass(item)">
                  {{ statusLabel(item) }}
                </span>
                <span class="badge" :class="{ 'badge--muted': item.visibility !== 'public' }">
                  {{ visibilityLabel(item.visibility) }}
                </span>
                <span v-if="item.attachment_count > 0" class="muted">{{ item.attachment_count }} 个附件</span>
                <span class="muted">更新于 {{ absoluteTime(item.updated_at) }}</span>
              </p>
              <AlertNote v-if="item.publish_failed || item.publish_error" kind="error" class="publish-error">
                定时发布失败：{{ item.publish_error || '原因未知，可重试发布或取消定时。' }}
              </AlertNote>
              <AlertNote v-else-if="item.status === 'paused'" kind="warning" class="publish-error">
                定时任务已暂停（可能因服务重启或重试次数耗尽），可立即发布或取消定时。
              </AlertNote>
            </div>

            <div class="scheduled-actions">
              <RouterLink
                class="btn btn--secondary btn--sm"
                :to="{ name: 'draft-edit', params: { draftId: item.id } }"
              >
                <AppIcon name="pencil" :size="15" /> 编辑
              </RouterLink>
              <button
                v-if="item.status === 'scheduled' || item.status === 'failed' || item.status === 'paused'"
                class="btn btn--secondary btn--sm"
                type="button"
                :disabled="busyId !== ''"
                @click="cancel(item)"
              >
                <AppIcon name="clock" :size="15" />
                {{ busyId === item.id ? '处理中…' : '取消定时' }}
              </button>
              <button
                v-if="item.publish_failed || item.status === 'failed'"
                class="btn btn--primary btn--sm"
                type="button"
                :disabled="busyId !== ''"
                @click="publishNow(item)"
              >
                <AppIcon name="refresh" :size="15" />
                {{ busyId === item.id ? '发布中…' : '重试发布' }}
              </button>
              <button
                v-else-if="item.status === 'scheduled' || item.status === 'paused'"
                class="btn btn--primary btn--sm"
                type="button"
                :disabled="busyId !== ''"
                @click="publishNow(item)"
              >
                <AppIcon name="send" :size="15" /> 立即发布
              </button>
              <button
                class="btn btn--danger btn--sm"
                type="button"
                :disabled="busyId !== ''"
                @click="removeItem(item)"
              >
                <AppIcon name="trash" :size="15" /> 删除
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
            <p v-else class="muted">已加载全部 {{ items.length }} 条定时帖</p>
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
.scheduled-list {
  overflow: hidden;
}
.scheduled {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 14px 16px;
  border-bottom: 1px solid var(--border);
}
.scheduled:last-of-type {
  border-bottom: 0;
}
.scheduled-main {
  min-width: 0;
  flex: 1;
}
.scheduled-time {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 0;
  font-size: 14px;
  font-weight: 700;
  color: var(--ink);
}
.scheduled-time .icon {
  color: var(--ink-3);
}
.scheduled-summary {
  margin: 6px 0 0;
  font-size: 14px;
  line-height: 1.6;
  color: var(--ink-2);
  word-break: break-word;
}
.scheduled-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin: 8px 0 0;
}
.scheduled-meta .muted {
  font-size: 12.5px;
}
.publish-error {
  margin-top: 8px;
}
.scheduled-actions {
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
  .scheduled {
    flex-direction: column;
    align-items: stretch;
  }
  .scheduled-actions .btn {
    flex: 1;
  }
}
</style>
