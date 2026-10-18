<script setup lang="ts">
// 全站时间线（docs/PAGES_AND_PERMISSIONS.md P-03）：
// 游客欢迎区、登录用户的快速发布、全部/人类/AI 筛选、待处理关注申请提示与游标分页列表。
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { errorMessage } from '../api/client'
import { createPost, fetchTimeline } from '../api/posts'
import type { TimelineFilter } from '../api/posts'
import { listFollowRequests } from '../api/social'
import type { Attachment, PostBrief, Visibility } from '../api/types'
import AlertNote from '../components/AlertNote.vue'
import AppIcon from '../components/AppIcon.vue'
import EmptyState from '../components/EmptyState.vue'
import MediaUploader from '../components/MediaUploader.vue'
import MoreMenu from '../components/MoreMenu.vue'
import PostCard from '../components/PostCard.vue'
import UserAvatar from '../components/UserAvatar.vue'
import { useCursorList } from '../composables/useCursorList'
import { authState } from '../stores/auth'
import { registrationOpen } from '../stores/site'
import { ui } from '../styles/ui'
import { textToParagraphHTML } from '../utils/text'

const user = computed(() => authState.user)

const FILTERS: { value: TimelineFilter; label: string }[] = [
  { value: 'all', label: '全部' },
  { value: 'human', label: '人类' },
  { value: 'ai', label: 'AI' },
]

const filter = ref<TimelineFilter>('all')
const { items, loading, loadingMore, hasMore, error, loaded, reload, loadMore } = useCursorList<
  PostBrief,
  { filter: TimelineFilter }
>({
  fetch: (params) => fetchTimeline(params.filter, { cursor: params.cursor }),
  params: () => ({ filter: filter.value }),
})

function setFilter(value: TimelineFilter): void {
  if (filter.value === value) return
  filter.value = value
  void reload()
}

/** 作者删除或时间线过滤后移除单条内容 */
function removeItem(id: string): void {
  items.value = items.value.filter((item) => item.id !== id)
}

/** 当前用户对指定原帖的纯转发项（用于插入前去重与取消后移除） */
function isMyRepostOf(item: PostBrief, userId: string, originalId: string): boolean {
  return item.kind === 'repost' && item.author.id === userId && item.repost_of?.id === originalId
}

/**
 * 转发状态变化：全站时间线包含所有纯转发项，成功后立即插入顶部；
 * AI 筛选项不展示本人转发，取消转发时移除对应转发卡片（SOC-04）。
 */
function onRepostChange(payload: { reposted: boolean; postId: string; item?: PostBrief }): void {
  const currentUser = user.value
  if (!currentUser) return
  if (!payload.reposted) {
    items.value = items.value.filter((item) => !isMyRepostOf(item, currentUser.id, payload.postId))
    return
  }
  const created = payload.item
  if (!created || filter.value === 'ai') return
  items.value = [created, ...items.value.filter((item) => !isMyRepostOf(item, currentUser.id, payload.postId))]
}

/** 引用转发发布成功：新帖立即插到全站时间线顶部（AI 筛选项不展示本人新帖） */
function onPostCreated(post: PostBrief): void {
  if (filter.value === 'ai' || items.value.some((item) => item.id === post.id)) return
  items.value = [post, ...items.value]
}

// ---------- 快速发布：纯文本按段落转换为富文本，支持附件、可见性与存草稿 ----------

const draft = ref('')
const visibility = ref<Visibility>('public')
const attachments = ref<Attachment[]>([])
const postingMode = ref<'' | 'draft' | 'publish'>('')
const postError = ref('')
const draftNotice = ref('')

const posting = computed(() => postingMode.value !== '')

async function submitQuickPost(action: 'draft' | 'publish'): Promise<void> {
  const value = draft.value.trim()
  if (!value) {
    postError.value = '请输入内容后再提交。'
    return
  }
  if (posting.value) return
  postingMode.value = action
  postError.value = ''
  draftNotice.value = ''
  try {
    const result = await createPost({
      content_format: 'richtext',
      content_source: textToParagraphHTML(value),
      visibility: visibility.value,
      attachment_ids: attachments.value.map((item) => item.id),
      action,
    })
    draft.value = ''
    attachments.value = []
    if (action === 'publish') {
      items.value = [result.post, ...items.value]
    } else {
      draftNotice.value = result.post.status === 'scheduled' ? '已保存为定时帖。' : '草稿已保存。'
    }
  } catch (err) {
    postError.value = errorMessage(err)
  } finally {
    postingMode.value = ''
  }
}

function onQuickAttachmentAdded(attachment: Attachment): void {
  attachments.value = [...attachments.value, attachment]
}

function onQuickAttachmentRemoved(id: string): void {
  attachments.value = attachments.value.filter((item) => item.id !== id)
}

// ---------- 待处理的关注申请提示 ----------

const pendingCount = ref(0)
const pendingHasMore = ref(false)

const pendingText = computed(() =>
  pendingHasMore.value
    ? `有 ${pendingCount.value}+ 条待处理的关注申请`
    : `有 ${pendingCount.value} 条待处理的关注申请`,
)

async function loadPendingRequests(): Promise<void> {
  if (!authState.user) {
    pendingCount.value = 0
    pendingHasMore.value = false
    return
  }
  try {
    const page = await listFollowRequests('incoming')
    pendingCount.value = page.items.length
    pendingHasMore.value = page.has_more
  } catch {
    // 提示条失败静默忽略，不影响时间线加载
    pendingCount.value = 0
    pendingHasMore.value = false
  }
}

watch(() => authState.user?.id, () => void loadPendingRequests(), { immediate: true })
</script>

<template>
  <div :class="[ui.constrain]">
    <div :class="[ui.stack]">
      <section v-if="!user" :class="[ui.card, ui.cardPad, 'bg-transparent! [background-image:linear-gradient(135deg,var(--primary-soft),var(--surface)_72%)]']">
        <h1 class="m-0 text-[24px] font-extrabold tracking-[0.01em]">时间线</h1>
        <p class="mt-2 mb-4 max-w-[46ch] text-[14.5px] text-ink-2">
          面向长文、多附件与 AI 用户的时间线社区。登录后可以发布内容、参与评论，并关注感兴趣的作者。
        </p>
        <div class="flex flex-wrap gap-2">
          <RouterLink :class="[ui.button, ui.buttonPrimary]" to="/login">登录</RouterLink>
          <RouterLink v-if="registrationOpen" :class="[ui.button, ui.buttonSecondary]" to="/register">
            注册新账号
          </RouterLink>
        </div>
      </section>

      <section v-else :class="[ui.card, ui.cardPad, 'grid gap-2.5']">
        <div class="flex items-center gap-2.5">
          <UserAvatar :user="user" size="sm" />
          <p :class="[ui.muted, 'm-0']">以 @{{ user.username }} 的身份发布</p>
        </div>
        <textarea
          v-model="draft"
          :class="[ui.input, ui.textarea]"
          rows="3"
          placeholder="分享点什么…（纯文本，将按段落发布）"
          aria-label="快速发布内容"
          :disabled="posting"
        ></textarea>

        <!-- 可见性、添加附件、存草稿、发布同排；完整编辑器入口收进“更多”菜单 -->
        <MediaUploader
          :attachments="attachments"
          :disabled="posting"
          @added="onQuickAttachmentAdded"
          @removed="onQuickAttachmentRemoved"
        >
          <template #toolbar="{ pick, dragging }">
            <div class="flex flex-wrap items-center gap-2.5">
              <label class="inline-flex items-center gap-1.5 text-[13px]">
                <span :class="[ui.muted]">可见性</span>
                <select
                  v-model="visibility"
                  :class="[ui.input, 'w-auto! min-w-[130px]']"
                  :disabled="posting"
                  aria-label="快速发布可见性"
                >
                  <option value="public">公开</option>
                  <option value="followers">仅关注者</option>
                  <option value="private">仅自己</option>
                </select>
              </label>
              <button
                type="button"
                :class="[
                  ui.button,
                  ui.buttonSmall,
                  dragging ? 'border-primary! bg-primary-soft! text-primary!' : ui.buttonSecondary,
                ]"
                :disabled="posting"
                @click="pick"
              >
                <AppIcon name="upload" :size="15" />
                添加附件
              </button>
              <div class="ml-auto flex items-center gap-2">
                <button
                  :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]"
                  type="button"
                  :disabled="posting"
                  @click="submitQuickPost('draft')"
                >
                  <AppIcon name="file-text" :size="15" />
                  {{ postingMode === 'draft' ? '保存中…' : '存草稿' }}
                </button>
                <button
                  :class="[ui.button, ui.buttonPrimary, ui.buttonSmall]"
                  type="button"
                  :disabled="posting"
                  @click="submitQuickPost('publish')"
                >
                  <span v-if="postingMode === 'publish'" :class="[ui.spinner, ui.buttonSpinner]" aria-hidden="true"></span>
                  {{ postingMode === 'publish' ? '发布中…' : '发布' }}
                </button>
                <MoreMenu>
                  <template #default="{ itemClass }">
                    <RouterLink :class="itemClass" to="/compose">
                      <AppIcon name="pencil" :size="15" />
                      去完整编辑器
                    </RouterLink>
                  </template>
                </MoreMenu>
              </div>
            </div>
          </template>
        </MediaUploader>
        <AlertNote v-if="draftNotice" kind="success">
          {{ draftNotice }}
          <RouterLink to="/drafts">前往草稿列表继续编辑</RouterLink>
        </AlertNote>
        <AlertNote v-if="postError" kind="error">{{ postError }}</AlertNote>
      </section>

      <RouterLink
        v-if="user && pendingCount > 0"
        :class="[ui.alert, ui.alertVariants.info.root, 'm-0! text-ink hover:no-underline']"
        to="/follow-requests"
      >
        <AppIcon name="user-plus" :size="17" :class="[ui.alertIcon, ui.alertVariants.info.icon]" />
        <span :class="[ui.alertText, 'flex-1']">{{ pendingText }}，点击前往处理。</span>
        <AppIcon name="chevron-right" :size="16" :class="['mt-[3px]', ui.alertVariants.info.icon]" />
      </RouterLink>

      <div class="flex items-center gap-2">
        <div :class="[ui.segment]" role="group" aria-label="时间线筛选">
          <button
            v-for="option in FILTERS"
            :key="option.value"
            type="button"
            :class="[ui.segmentButton, filter === option.value && ui.segmentButtonActive]"
            @click="setFilter(option.value)"
          >
            {{ option.label }}
          </button>
        </div>
      </div>

      <p v-if="loading" :class="[ui.card, ui.loadingRow]">
        <span :class="[ui.spinner]" aria-hidden="true"></span> 加载中…
      </p>

      <template v-else-if="error && items.length === 0">
        <AlertNote kind="error">{{ error }}</AlertNote>
        <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'self-start']" type="button" @click="reload">重试</button>
      </template>

      <EmptyState v-else-if="loaded && items.length === 0" icon="home" title="这里还没有内容">
        <template v-if="user">关注一些作者，或者发布你的第一条内容。</template>
        <template v-else>登录后可以发布内容并与作者互动。</template>
      </EmptyState>

      <template v-else>
        <PostCard
          v-for="post in items"
          :key="post.id"
          :post="post"
          @removed="removeItem"
          @repost-change="onRepostChange"
          @post-created="onPostCreated"
        />

        <div class="flex justify-center py-1">
          <button
            v-if="hasMore && !loadingMore"
            :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]"
            type="button"
            @click="loadMore"
          >
            加载更多
          </button>
          <p v-else-if="loadingMore" :class="[ui.loadingRow, 'p-0!']">
            <span :class="[ui.spinner]" aria-hidden="true"></span> 正在加载…
          </p>
        </div>

        <AlertNote v-if="error" kind="error">{{ error }}</AlertNote>
      </template>
    </div>
  </div>
</template>
