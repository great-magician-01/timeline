<script setup lang="ts">
// 编辑历史（docs/PAGES_AND_PERMISSIONS.md P-08 / U-05）
// 历史版本权限跟随当前帖子：所有有权查看当前帖子的人都能查看历史版本与历史附件；
// 仅作者可以恢复历史版本，恢复会生成新的当前版本，不覆盖既有历史（docs/REQUIREMENTS.md 6.4）。
import { computed, nextTick, onBeforeUnmount, ref, useTemplateRef, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import AlertNote from '../components/AlertNote.vue'
import AppIcon from '../components/AppIcon.vue'
import AttachmentGallery from '../components/AttachmentGallery.vue'
import EmptyState from '../components/EmptyState.vue'
import { errorMessage } from '../api/client'
import { getPost, getVersion, listVersions, restoreVersion } from '../api/posts'
import type { PostDetail, PostVersion, PostVersionSummary, Visibility } from '../api/types'
import { useCursorList } from '../composables/useCursorList'
import { absoluteTime, plainSummary } from '../utils/format'

const route = useRoute()
const router = useRouter()
const postId = computed(() => String(route.params.postId ?? ''))

const { items, loading, loadingMore, hasMore, error, loadMore, reload } = useCursorList<
  PostVersionSummary,
  Record<string, never>
>({
  fetch: (params) => listVersions(postId.value, params),
  params: () => ({}),
})

const post = ref<PostDetail | null>(null)
const postError = ref('')

const selectedId = ref('')
const version = ref<PostVersion | null>(null)
const versionLoading = ref(false)
const versionError = ref('')
const detailRef = useTemplateRef<HTMLElement>('detailPanel')

const restoring = ref(false)
const actionError = ref('')
const notice = ref('')

/** 只有作者（can_edit）能恢复历史版本；查看列表与详情由后端按当前帖子权限判定 */
const canRestore = computed(() => post.value?.can_edit === true)

let navTimer: number | null = null

function visibilityLabel(visibility: Visibility): string {
  if (visibility === 'public') return '公开'
  if (visibility === 'followers') return '仅关注者'
  return '仅自己'
}

/** 请求序号：快速切换 :postId 时作废帖子详情与版本详情的旧响应 */
let postSequence = 0
let versionSequence = 0

async function loadPost(): Promise<void> {
  const current = ++postSequence
  postError.value = ''
  try {
    const result = await getPost(postId.value)
    if (current !== postSequence) return
    post.value = result.post
  } catch (err) {
    if (current !== postSequence) return
    post.value = null
    postError.value = errorMessage(err)
  }
}

watch(postId, () => void loadPost(), { immediate: true })

// 同一路由切换帖子时组件不会重建；首屏加载由 useCursorList 在挂载时完成，这里只处理参数变化
watch(postId, () => {
  versionSequence += 1 // 作废仍在进行中的版本详情请求
  post.value = null
  selectedId.value = ''
  version.value = null
  versionError.value = ''
  void reload()
})

onBeforeUnmount(() => {
  if (navTimer !== null) window.clearTimeout(navTimer)
})

async function selectVersion(id: string): Promise<void> {
  const current = ++versionSequence
  if (selectedId.value === id) {
    selectedId.value = ''
    version.value = null
    versionError.value = ''
    return
  }
  selectedId.value = id
  version.value = null
  versionError.value = ''
  versionLoading.value = true
  try {
    const result = await getVersion(postId.value, id)
    if (current !== versionSequence) return
    version.value = result.version
    await nextTick()
    if (current !== versionSequence) return
    detailRef.value?.scrollIntoView({ block: 'nearest' })
  } catch (err) {
    if (current !== versionSequence) return
    versionError.value = errorMessage(err)
  } finally {
    if (current === versionSequence) versionLoading.value = false
  }
}

async function restore(): Promise<void> {
  const current = version.value
  if (!current || restoring.value) return
  if (!window.confirm('恢复后会把该版本内容生成为新的当前版本，既有历史版本不会被删除。确认恢复？')) return
  restoring.value = true
  actionError.value = ''
  notice.value = ''
  try {
    await restoreVersion(postId.value, current.id)
    notice.value = '已生成新版本，正在跳转到帖子详情…'
    navTimer = window.setTimeout(() => {
      void router.push({ name: 'post-detail', params: { postId: postId.value } })
    }, 800)
  } catch (err) {
    actionError.value = errorMessage(err)
  } finally {
    restoring.value = false
  }
}
</script>

<template>
  <div class="constrain">
    <div class="stack">
      <header class="page-head">
        <RouterLink class="back-link" :to="{ name: 'post-detail', params: { postId } }">
          <AppIcon name="arrow-left" :size="16" /> 返回帖子
        </RouterLink>
        <h1 class="page-title">编辑历史</h1>
        <p class="page-desc">历史版本与其中的附件对所有有权查看该帖子的人可见。</p>
      </header>

      <AlertNote v-if="postError" kind="error">{{ postError }}</AlertNote>
      <AlertNote v-if="notice" kind="success">{{ notice }}</AlertNote>
      <AlertNote v-if="actionError" kind="error">{{ actionError }}</AlertNote>

      <div v-if="loading" class="card card--pad">
        <p class="loading-row"><span class="spinner" aria-hidden="true"></span> 正在加载历史版本…</p>
      </div>

      <template v-else-if="error && items.length === 0">
        <AlertNote kind="error">{{ error }}</AlertNote>
        <button class="btn btn--secondary btn--sm retry" type="button" @click="reload()">重试</button>
      </template>

      <section v-else-if="items.length === 0" class="card">
        <EmptyState
          icon="scroll"
          title="还没有历史版本"
          description="帖子发布后每次保存都会保留一个版本，恢复历史也会生成新版本。"
        />
      </section>

      <template v-else>
        <section class="card version-list">
          <p class="list-title">共 {{ items.length }} 个版本{{ hasMore ? '（还有更多）' : '' }}</p>
          <button
            v-for="item in items"
            :key="item.id"
            type="button"
            class="version-row"
            :class="{ 'is-active': selectedId === item.id }"
            @click="selectVersion(item.id)"
          >
            <span class="version-head">
              <strong>版本 {{ item.version_number }}</strong>
              <span class="badge" :class="{ 'badge--muted': item.visibility !== 'public' }">
                {{ visibilityLabel(item.visibility) }}
              </span>
              <span v-if="item.restored_from_version_id" class="badge badge--muted">由历史恢复</span>
              <span class="muted version-time">{{ absoluteTime(item.created_at) }}</span>
            </span>
            <span class="version-summary">{{ plainSummary(item.summary || '') || '（无摘要）' }}</span>
          </button>

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
            <p v-else class="muted">已加载全部 {{ items.length }} 个版本</p>
          </div>
        </section>

        <AlertNote v-if="error" kind="error">{{ error }}</AlertNote>

        <section v-if="selectedId" ref="detailPanel" class="card card--pad version-detail">
          <p v-if="versionLoading" class="loading-row">
            <span class="spinner" aria-hidden="true"></span> 正在加载版本内容…
          </p>
          <AlertNote v-else-if="versionError" kind="error">{{ versionError }}</AlertNote>

          <template v-else-if="version">
            <header class="detail-head">
              <div>
                <p class="detail-title">
                  版本 {{ version.version_number }}
                  <span class="badge" :class="{ 'badge--muted': version.visibility !== 'public' }">
                    {{ visibilityLabel(version.visibility) }}
                  </span>
                  <span v-if="version.restored_from_version_id" class="badge badge--muted">由历史恢复</span>
                </p>
                <p class="muted">{{ absoluteTime(version.created_at) }}</p>
              </div>
              <button class="btn btn--secondary btn--sm" type="button" @click="selectVersion(version.id)">
                <AppIcon name="x" :size="15" /> 收起
              </button>
            </header>

            <div class="content" v-html="version.content_html"></div>
            <AttachmentGallery :attachments="version.attachments" />

            <div v-if="canRestore" class="detail-actions">
              <button class="btn btn--primary btn--sm" type="button" :disabled="restoring" @click="restore">
                <span v-if="restoring" class="spinner restore-spinner" aria-hidden="true"></span>
                <AppIcon v-else name="refresh" :size="15" />
                {{ restoring ? '恢复中…' : '恢复此版本' }}
              </button>
              <p class="muted">恢复会生成新的当前版本，不会删除或覆盖任何历史版本。</p>
            </div>
          </template>
        </section>
      </template>
    </div>
  </div>
</template>

<style scoped>
.back-link {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  margin-bottom: 8px;
  font-size: 13.5px;
  color: var(--ink-2);
}
.back-link:hover {
  color: var(--ink);
  text-decoration: none;
}
.retry {
  align-self: flex-start;
}
.version-list {
  overflow: hidden;
}
.list-title {
  margin: 0;
  padding: 12px 16px 8px;
  font-size: 12.5px;
  color: var(--ink-3);
}
.version-row {
  display: flex;
  flex-direction: column;
  gap: 4px;
  width: 100%;
  padding: 12px 16px;
  border-top: 1px solid var(--border);
  text-align: left;
  transition: background 0.15s ease;
}
.version-row:hover {
  background: var(--hover);
}
.version-row.is-active {
  background: var(--primary-soft);
}
.version-head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  font-size: 14px;
}
.version-time {
  font-size: 12.5px;
}
.version-summary {
  font-size: 13px;
  color: var(--ink-2);
  word-break: break-word;
}
.list-foot {
  display: flex;
  justify-content: center;
  padding: 14px 16px;
  border-top: 1px solid var(--border);
}
.list-foot .muted {
  margin: 0;
}
.load-more .spinner,
.restore-spinner {
  border-color: var(--border);
  border-top-color: var(--primary);
}
.detail-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 12px;
}
.detail-title {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin: 0;
  font-size: 15px;
  font-weight: 700;
}
.detail-head .muted {
  margin: 4px 0 0;
}
.content {
  font-size: 15px;
  line-height: 1.75;
  color: var(--ink);
  word-break: break-word;
}
.content :deep(a.tl-tag),
.content :deep(a.tl-mention) {
  color: var(--primary);
  font-weight: 600;
}
.content :deep(img) {
  max-width: 100%;
  border-radius: 8px;
}
.content :deep(pre) {
  overflow: auto;
  margin: 0;
  padding: 12px;
  border-radius: 8px;
  background: var(--soft);
  font-size: 13px;
}
.content :deep(blockquote) {
  margin: 0;
  padding-left: 12px;
  border-left: 3px solid var(--border);
  color: var(--ink-2);
}
.detail-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  margin-top: 16px;
  padding-top: 14px;
  border-top: 1px solid var(--border);
}
.detail-actions .muted {
  margin: 0;
}
@media (max-width: 880px) {
  .detail-head {
    flex-direction: column;
    align-items: stretch;
  }
  .detail-head .btn {
    align-self: flex-start;
  }
  .detail-actions .btn {
    width: 100%;
  }
}
</style>
