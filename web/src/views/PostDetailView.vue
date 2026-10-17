<script setup lang="ts">
// 帖子详情（docs/PAGES_AND_PERMISSIONS.md P-07）：
// 正文、附件、引用、作者关注入口、互动栏、评论与楼中楼回复。
// 所有权限提示只做展示，实际授权由后端逐个接口执行。
import { computed, nextTick, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { ApiError, apiRequest, errorMessage } from '../api/client'
import { getPost } from '../api/posts'
import {
  createComment,
  fetchProfile,
  followUser,
  likePost,
  listComments,
  repostPost,
  unfollowUser,
  unlikePost,
  unrepostPost,
} from '../api/social'
import type { Comment, FollowState, PostBrief, PostDetail } from '../api/types'
import AlertNote from '../components/AlertNote.vue'
import AppIcon from '../components/AppIcon.vue'
import AttachmentGallery from '../components/AttachmentGallery.vue'
import CommentItem from '../components/CommentItem.vue'
import EmptyState from '../components/EmptyState.vue'
import UserAvatar from '../components/UserAvatar.vue'
import { useCursorList } from '../composables/useCursorList'
import { authState } from '../stores/auth'
import { ui } from '../styles/ui'
import { relativeTime, timeTitle } from '../utils/format'
import { textToParagraphHTML } from '../utils/text'

const route = useRoute()
const router = useRouter()
const my = computed(() => authState.user)
const postId = computed(() => String(route.params.postId ?? ''))

const post = ref<PostDetail | null>(null)
const loading = ref(true)
const notFound = ref(false)
const loadError = ref('')

/** 转发项兜底：详情理论上不返回转发包装，出现时展示原帖内容 */
const target = computed<PostBrief | null>(() => {
  const current = post.value
  if (!current) return null
  if (current.kind === 'repost' && current.repost_of) return current.repost_of
  return current
})

const liked = ref(false)
const likeCount = ref(0)
const reposted = ref(false)
const repostCount = ref(0)
const busy = ref(false)
const actionNotice = ref('')

const visibilityLabel = computed(() => {
  if (target.value?.visibility === 'followers') return '仅关注者可见'
  if (target.value?.visibility === 'private') return '仅自己可见'
  return '公开'
})

const visibilityClass = computed(() =>
  target.value?.visibility === 'public' ? ui.badgeMuted : '',
)

const isAuthor = computed(() => Boolean(my.value && target.value && my.value.id === target.value.author.id))
/** 停用账号内容只读：隐藏点赞、评论、转发、引用入口（docs/PAGES_AND_PERMISSIONS.md E-03） */
const authorSuspended = computed(() => target.value?.author.is_suspended === true)
const canShare = computed(
  () => Boolean(my.value) && !authorSuspended.value && target.value?.visibility === 'public',
)

const shareHint = computed(() => {
  if (!my.value) return '登录后可以转发或引用'
  if (authorSuspended.value) return '作者已停用，互动入口已关闭'
  if (target.value?.visibility !== 'public') return '仅公开帖子可以转发或引用'
  return ''
})

// ---------- 作者关注状态 ----------

const authorFollow = ref<FollowState | null>(null)
const followBusy = ref(false)

const showFollowButton = computed(
  () =>
    Boolean(my.value) &&
    !isAuthor.value &&
    authorFollow.value !== null &&
    authorFollow.value !== 'incoming_pending' &&
    !target.value?.author.is_suspended,
)

const followLabel = computed(() => {
  if (authorFollow.value === 'following') return '已关注'
  if (authorFollow.value === 'pending') return '已申请'
  return '关注'
})

async function loadFollowState(data: PostDetail, current: number): Promise<void> {
  if (!my.value || my.value.id === data.author.id) {
    authorFollow.value = null
    return
  }
  if (data.author_follow_state) {
    authorFollow.value = data.author_follow_state
    return
  }
  // 详情未返回关注状态时退回资料接口，失败则不展示关注按钮
  try {
    const state = (await fetchProfile(data.author.username)).follow_state
    if (current === loadSequence) authorFollow.value = state
  } catch {
    if (current === loadSequence) authorFollow.value = null
  }
}

async function toggleFollow(): Promise<void> {
  const current = target.value
  if (!current || followBusy.value) return
  if (authorFollow.value === 'following' || authorFollow.value === 'pending') {
    if (!window.confirm(authorFollow.value === 'pending' ? '确认取消关注申请？' : '确认取消关注？')) return
    followBusy.value = true
    actionNotice.value = ''
    try {
      authorFollow.value = (await unfollowUser(current.author.username)).follow_state
    } catch (err) {
      actionNotice.value = errorMessage(err)
    } finally {
      followBusy.value = false
    }
    return
  }
  followBusy.value = true
  actionNotice.value = ''
  try {
    authorFollow.value = (await followUser(current.author.username)).follow_state
  } catch (err) {
    actionNotice.value = errorMessage(err)
  } finally {
    followBusy.value = false
  }
}

// ---------- 详情加载 ----------

/** 请求序号：快速切换 :postId 时作废旧响应，避免旧帖子覆盖新帖子 */
let loadSequence = 0

async function loadPost(): Promise<void> {
  const current = ++loadSequence
  loading.value = true
  notFound.value = false
  loadError.value = ''
  post.value = null
  actionNotice.value = ''
  authorFollow.value = null
  try {
    const data = await getPost(postId.value)
    if (current !== loadSequence) return
    post.value = data.post
    liked.value = data.post.viewer_state.liked
    likeCount.value = data.post.like_count
    reposted.value = data.post.viewer_state.reposted
    repostCount.value = data.post.repost_count
    await loadFollowState(data.post, current)
    // 过期响应不触发评论加载，避免用旧帖子 id 拉评论
    if (current !== loadSequence) return
    void reloadComments()
  } catch (err) {
    if (current !== loadSequence) return
    if (err instanceof ApiError && err.status === 404) notFound.value = true
    else loadError.value = errorMessage(err)
  } finally {
    if (current === loadSequence) loading.value = false
  }
}

// 详情加载放在脚本末尾（loadPost 依赖下面声明的评论列表状态）

// ---------- 互动 ----------

async function toggleLike(): Promise<void> {
  const current = target.value
  if (!current || busy.value || authorSuspended.value) return
  if (!my.value) {
    void router.push({ name: 'login', query: { redirect: route.fullPath } })
    return
  }
  busy.value = true
  actionNotice.value = ''
  try {
    const result = liked.value ? await unlikePost(current.id) : await likePost(current.id)
    liked.value = result.liked
    likeCount.value = result.like_count
  } catch (err) {
    actionNotice.value = errorMessage(err)
  } finally {
    busy.value = false
  }
}

async function toggleRepost(): Promise<void> {
  const current = target.value
  if (!current || busy.value || !canShare.value) return
  busy.value = true
  actionNotice.value = ''
  try {
    const result = reposted.value ? await unrepostPost(current.id) : await repostPost(current.id)
    reposted.value = result.reposted
    repostCount.value = result.repost_count
  } catch (err) {
    actionNotice.value = errorMessage(err)
  } finally {
    busy.value = false
  }
}

function quotePost(): void {
  const current = target.value
  if (!current || !canShare.value) return
  void router.push({ name: 'compose', query: { quote: current.id } })
}

async function removePost(): Promise<void> {
  const current = post.value
  if (!current) return
  if (!window.confirm('删除后内容不再通过普通页面展示，历史版本与附件记录仍会保留。确认删除？')) return
  busy.value = true
  actionNotice.value = ''
  try {
    await apiRequest(`/posts/${current.id}/delete`, { method: 'POST' })
    await router.push('/')
  } catch (err) {
    actionNotice.value = errorMessage(err)
    busy.value = false
  }
}

// ---------- 评论 ----------

const {
  items: commentItems,
  loading: commentsLoading,
  loadingMore: commentsLoadingMore,
  hasMore: commentsHasMore,
  error: commentsError,
  loaded: commentsLoaded,
  reload: reloadComments,
  loadMore: loadMoreComments,
} = useCursorList<Comment, { postId: string }>({
  fetch: (params) => listComments(params.postId, { cursor: params.cursor }),
  params: () => ({ postId: postId.value }),
  immediate: false,
})

const commentText = ref('')
const replyTarget = ref<Comment | null>(null)
const commentSubmitting = ref(false)
const commentError = ref('')
const commentBox = ref<HTMLTextAreaElement | null>(null)

function startReply(comment: Comment): void {
  if (!my.value) {
    void router.push({ name: 'login', query: { redirect: route.fullPath } })
    return
  }
  replyTarget.value = comment
  void nextTick(() => commentBox.value?.focus())
}

async function submitComment(): Promise<void> {
  const value = commentText.value.trim()
  if (!value) {
    commentError.value = '请输入评论内容。'
    return
  }
  if (commentSubmitting.value) return
  commentSubmitting.value = true
  commentError.value = ''
  try {
    const reply = replyTarget.value
    await createComment(postId.value, {
      content_format: 'richtext',
      content_source: textToParagraphHTML(value),
      ...(reply ? { parent_comment_id: reply.id } : {}),
    })
    commentText.value = ''
    replyTarget.value = null
    await reloadComments()
  } catch (err) {
    commentError.value = errorMessage(err)
  } finally {
    commentSubmitting.value = false
  }
}

const canManage = computed(() => {
  const current = post.value
  if (!current) return false
  return Boolean(current.can_edit || current.can_delete || current.can_view_history)
})

// 路由参数变化（含首次进入）时加载详情，成功后按需加载评论首屏
watch(postId, () => void loadPost(), { immediate: true })
</script>

<template>
  <div :class="[ui.constrain]">
    <div :class="[ui.stack]">
      <p v-if="loading" :class="[ui.card, ui.loadingRow]">
        <span :class="[ui.spinner]" aria-hidden="true"></span> 加载中…
      </p>

      <section v-else-if="notFound" :class="[ui.card, ui.cardPad]">
        <EmptyState icon="lock" title="内容不存在或无权访问" description="该内容可能已被删除，或你没有查看权限。">
          <RouterLink :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'mt-3']" to="/">返回首页</RouterLink>
        </EmptyState>
      </section>

      <template v-else-if="loadError">
        <AlertNote kind="error">{{ loadError }}</AlertNote>
        <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'self-start']" type="button" @click="loadPost">重试</button>
      </template>

      <template v-else-if="post && target">
        <article :class="[ui.card, ui.cardPad, 'grid gap-3']">
          <header class="flex items-center gap-2.5">
            <RouterLink :to="`/users/${target.author.username}`" class="flex min-w-0 items-center gap-2.5 text-ink hover:no-underline">
              <UserAvatar :user="target.author" size="md" />
              <span class="flex min-w-0 flex-col leading-[1.35]">
                <span class="flex items-center gap-1.5 text-[15px]">
                  <strong>{{ target.author.nickname || target.author.username }}</strong>
                  <span v-if="target.author.is_ai" :class="[ui.badge, ui.badgeAi]">AI</span>
                  <span v-if="target.author.is_suspended" :class="[ui.badge, ui.badgeMuted]">已停用</span>
                </span>
                <span class="text-[12.5px] text-ink-3">@{{ target.author.username }}</span>
              </span>
            </RouterLink>

            <button
              v-if="showFollowButton"
              :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'ml-auto']"
              type="button"
              :disabled="followBusy"
              @click="toggleFollow"
            >
              {{ followLabel }}
            </button>
            <RouterLink
              v-else-if="my && !isAuthor && authorFollow === 'incoming_pending'"
              :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'ml-auto']"
              to="/follow-requests"
            >
              对方申请关注你
            </RouterLink>
          </header>

          <p class="m-0 flex flex-wrap items-center gap-1.5 text-[12.5px] text-ink-3">
            <span v-if="post.kind === 'repost'" :class="[ui.muted]">转发了以下内容 ·</span>
            <RouterLink :to="`/posts/${target.id}`" :title="timeTitle(target.published_at)" class="text-ink-3">
              {{ relativeTime(target.published_at) }}
            </RouterLink>
            <span :class="[ui.badge, visibilityClass]">{{ visibilityLabel }}</span>
            <span v-if="post.status === 'draft'" :class="[ui.badge]">草稿</span>
            <span v-else-if="post.status === 'scheduled'" :class="[ui.badge]">定时发布</span>
            <span v-if="target.edited" :class="[ui.muted]">已编辑</span>
          </p>

          <AlertNote v-if="authorSuspended" kind="warning">
            该内容作者已停用，正文与评论只读展示，点赞、评论、转发与引用入口已关闭。
          </AlertNote>

          <div v-if="canManage" class="flex flex-wrap gap-2 border-y border-solid border-border py-2.5">
            <RouterLink
              v-if="post.can_edit"
              :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]"
              :to="{ name: 'post-edit', params: { postId: post.id } }"
            >
              <AppIcon name="pencil" :size="16" /> 编辑
            </RouterLink>
            <RouterLink
              v-if="post.can_view_history"
              :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]"
              :to="{ name: 'post-history', params: { postId: post.id } }"
            >
              <AppIcon name="clock" :size="16" /> 历史版本
            </RouterLink>
            <button
              v-if="post.can_delete"
              :class="[ui.button, ui.buttonDanger, ui.buttonSmall]"
              type="button"
              :disabled="busy"
              @click="removePost"
            >
              <AppIcon name="trash" :size="16" /> 删除
            </button>
          </div>

          <div
            class="text-[15.5px] leading-[1.85] [word-break:break-word] [&_a.tl-tag]:font-semibold [&_a.tl-tag]:text-primary [&_a.tl-mention]:font-semibold [&_a.tl-mention]:text-primary [&_img]:max-w-full [&_img]:rounded-lg [&_pre]:m-0 [&_pre]:overflow-auto [&_pre]:rounded-lg [&_pre]:bg-soft [&_pre]:p-3 [&_pre]:text-[13px] [&_blockquote]:m-0 [&_blockquote]:border-l-[3px] [&_blockquote]:border-solid [&_blockquote]:border-border [&_blockquote]:pl-3 [&_blockquote]:text-ink-2"
            v-html="target.content_html"
          ></div>
          <AttachmentGallery :attachments="target.attachments" />

          <blockquote v-if="target.quoted_post" class="m-0 rounded-[10px] border border-solid border-border bg-surface-subtle px-3 py-2.5">
            <template v-if="target.quoted_post.available && target.quoted_post.post">
              <RouterLink :to="`/posts/${target.quoted_post.post.id}`" class="mb-1.5 flex items-center gap-2 text-[13.5px] text-ink hover:no-underline">
                <UserAvatar :user="target.quoted_post.post.author" size="sm" />
                <strong>{{ target.quoted_post.post.author.nickname || target.quoted_post.post.author.username }}</strong>
                <span class="text-[12.5px] text-ink-3">@{{ target.quoted_post.post.author.username }}</span>
              </RouterLink>
              <div
                class="text-[14px] leading-[1.85] [word-break:break-word] [&_a.tl-tag]:font-semibold [&_a.tl-tag]:text-primary [&_a.tl-mention]:font-semibold [&_a.tl-mention]:text-primary [&_img]:max-w-full [&_img]:rounded-lg [&_pre]:m-0 [&_pre]:overflow-auto [&_pre]:rounded-lg [&_pre]:bg-soft [&_pre]:p-3 [&_pre]:text-[13px] [&_blockquote]:m-0 [&_blockquote]:border-l-[3px] [&_blockquote]:border-solid [&_blockquote]:border-border [&_blockquote]:pl-3 [&_blockquote]:text-ink-2"
                v-html="target.quoted_post.post.content_html"
              ></div>
            </template>
            <p v-else :class="[ui.muted]">原内容不可见</p>
          </blockquote>

          <AlertNote v-if="actionNotice" kind="warning">{{ actionNotice }}</AlertNote>

          <footer v-if="!authorSuspended" class="flex gap-4 border-t border-solid border-border pt-2">
            <button
              type="button"
              class="inline-flex items-center gap-1.5 rounded-lg px-2.5 py-[5px] text-[13.5px] text-ink-3 hover-not-disabled:bg-hover hover-not-disabled:text-ink disabled:cursor-not-allowed disabled:opacity-60"
              :class="liked && 'font-bold text-primary'"
              :disabled="busy"
              :title="my ? '点赞' : '登录后可点赞'"
              @click="toggleLike"
            >
              <AppIcon name="heart" :size="18" />
              <span>{{ likeCount }}</span>
            </button>
            <button
              type="button"
              class="inline-flex items-center gap-1.5 rounded-lg px-2.5 py-[5px] text-[13.5px] text-ink-3 hover-not-disabled:bg-hover hover-not-disabled:text-ink disabled:cursor-not-allowed disabled:opacity-60"
              :class="reposted && 'font-bold text-primary'"
              :disabled="busy || !canShare"
              :title="shareHint || '转发'"
              @click="toggleRepost"
            >
              <AppIcon name="repeat" :size="18" />
              <span>{{ repostCount }}</span>
            </button>
            <button
              type="button"
              class="inline-flex items-center gap-1.5 rounded-lg px-2.5 py-[5px] text-[13.5px] text-ink-3 hover-not-disabled:bg-hover hover-not-disabled:text-ink disabled:cursor-not-allowed disabled:opacity-60"
              :disabled="busy || !canShare"
              :title="shareHint || '引用并附加你的看法'"
              @click="quotePost"
            >
              <AppIcon name="message" :size="18" />
              <span>引用</span>
            </button>
          </footer>
          <p v-else class="m-0 flex items-center gap-1.5 rounded-lg border-t border-solid border-border bg-soft px-2.5 py-2 text-[13px] text-ink-3">
            <AppIcon name="lock" :size="15" />
            作者已停用，互动入口已关闭
          </p>
        </article>

        <section :class="[ui.card, ui.cardPad, 'grid gap-3']">
          <h2 :class="[ui.pageHead, ui.pageTitle]">评论（{{ target.comment_count }}）</h2>

          <p v-if="authorSuspended" class="m-0 flex items-center gap-1.5 rounded-lg border-t border-solid border-border bg-soft px-2.5 py-2 text-[13px] text-ink-3">
            <AppIcon name="lock" :size="15" />
            作者已停用，评论区只读展示，不再接受新的评论或回复。
          </p>
          <template v-else-if="my">
            <div v-if="replyTarget" class="mt-3 flex items-center gap-2 rounded-lg bg-primary-soft px-3 py-1.5 text-[13px] text-ink">
              <span>回复 @{{ replyTarget.author.username }}</span>
              <button type="button" class="ml-auto inline-flex items-center gap-[3px] text-[12.5px] text-ink-2" @click="replyTarget = null">
                <AppIcon name="x" :size="14" /> 取消
              </button>
            </div>
            <div class="mt-3 grid gap-2">
              <textarea
                ref="commentBox"
                v-model="commentText"
                :class="[ui.input, ui.textarea]"
                rows="3"
                placeholder="友善地表达你的看法…"
                aria-label="评论内容"
              ></textarea>
              <AlertNote v-if="commentError" kind="error" class="m-0!">{{ commentError }}</AlertNote>
              <div class="flex justify-end">
                <button
                  :class="[ui.button, ui.buttonPrimary, ui.buttonSmall]"
                  type="button"
                  :disabled="commentSubmitting"
                  @click="submitComment"
                >
                  <span v-if="commentSubmitting" :class="[ui.spinner, ui.buttonSpinner]" aria-hidden="true"></span>
                  {{ commentSubmitting ? '发送中…' : '发表评论' }}
                </button>
              </div>
            </div>
          </template>
          <div v-else class="mt-3 flex flex-wrap items-center justify-between gap-2.5 rounded-[10px] border border-solid border-border bg-surface-subtle px-3.5 py-3">
            <p :class="[ui.muted, 'm-0']">登录后可以参与评论与回复。</p>
            <RouterLink
              :class="[ui.button, ui.buttonPrimary, ui.buttonSmall]"
              :to="{ name: 'login', query: { redirect: route.fullPath } }"
            >
              登录
            </RouterLink>
          </div>

          <p v-if="commentsLoading" :class="[ui.loadingRow]">
            <span :class="[ui.spinner]" aria-hidden="true"></span> 加载评论中…
          </p>
          <EmptyState
            v-else-if="commentsLoaded && commentItems.length === 0"
            icon="message"
            title="还没有评论"
            description="成为第一个评论的人。"
          />
          <ul v-else class="m-0 list-none p-0">
            <CommentItem
              v-for="comment in commentItems"
              :key="comment.id"
              :comment="comment"
              @reply="startReply"
              @changed="reloadComments"
            />
          </ul>

          <div class="flex justify-center py-1">
            <button
              v-if="commentsHasMore && !commentsLoadingMore"
              :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]"
              type="button"
              @click="loadMoreComments"
            >
              加载更多评论
            </button>
            <p v-else-if="commentsLoadingMore" :class="[ui.loadingRow, 'p-0!']">
              <span :class="[ui.spinner]" aria-hidden="true"></span> 正在加载…
            </p>
          </div>

          <AlertNote v-if="commentsError" kind="error">{{ commentsError }}</AlertNote>
        </section>
      </template>
    </div>
  </div>
</template>
