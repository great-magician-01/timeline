<script setup lang="ts">
// 帖子卡片：时间线、个人主页、标签与搜索结果统一复用（docs/DESIGN_SYSTEM.md）。
// 互动按钮在后端再次鉴权，前端只负责交互与状态提示。
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import AppIcon from './AppIcon.vue'
import AttachmentGallery from './AttachmentGallery.vue'
import PostActionMenu from './PostActionMenu.vue'
import RepostDialog from './RepostDialog.vue'
import type { RepostDialogResult } from './RepostDialog.types'
import UserAvatar from './UserAvatar.vue'
import type { PostBrief, RepostState, UserBrief } from '../api/types'
import { errorMessage } from '../api/client'
import { likePost, unlikePost } from '../api/social'
import { authState } from '../stores/auth'
import { relativeTime, timeTitle } from '../utils/format'
import { ui } from '../styles/ui'

const props = withDefaults(
  defineProps<{
    post: PostBrief
    /** 详情场景展示完整正文；列表场景最多 4 行，超出时提供「查看全文」 */
    detail?: boolean
    /** 是否显示互动操作栏 */
    actions?: boolean
  }>(),
  { detail: false, actions: true },
)

const emit = defineEmits<{
  (event: 'removed', id: string): void
  /** 转发状态变化：成功后父列表可用 item 即时插入纯转发卡片；取消后按 postId 移除对应转发项 */
  (event: 'repost-change', payload: { reposted: boolean; postId: string; item?: PostBrief }): void
  /** 引用转发发布成功：父列表可把新帖即时插入顶部 */
  (event: 'post-created', post: PostBrief): void
  /** 置顶状态变化（置顶/取消置顶成功）：父列表整体刷新置顶区块与普通流 */
  (event: 'pin-changed', payload: { id: string; pinned: boolean }): void
}>()

const router = useRouter()
const my = computed(() => authState.user)

const liked = ref(props.post.viewer_state.liked)
const likeCount = ref(props.post.like_count)
const reposted = ref(props.post.viewer_state.reposted)
const repostCount = ref(props.post.repost_count)

// 父列表重新拉取后同步互动状态与计数：本地 ref 不会自动跟随 props 变化
watch(
  () => props.post.viewer_state.liked,
  (value) => {
    liked.value = value
  },
)
watch(
  () => props.post.like_count,
  (value) => {
    likeCount.value = value
  },
)
watch(
  () => props.post.viewer_state.reposted,
  (value) => {
    reposted.value = value
  },
)
watch(
  () => props.post.repost_count,
  (value) => {
    repostCount.value = value
  },
)

const busy = ref(false)

/** 转发弹窗开关：点击转发按钮先打开弹窗，留空为纯转发、填写内容为引用转发 */
const repostOpen = ref(false)

// ---------- 操作提示：成功/取消约 3 秒自动消失，失败保留到下一次操作 ----------

const notice = ref('')
/** 成功/取消操作使用 success 语气，失败提示使用 warning */
const noticeKind = ref<'success' | 'warning'>('warning')
let noticeTimer: number | null = null

function clearNoticeTimer(): void {
  if (noticeTimer === null) return
  window.clearTimeout(noticeTimer)
  noticeTimer = null
}

/** 展示提示；autoHide 仅用于成功/取消这类操作反馈，约 3 秒后自动消失 */
function showNotice(message: string, kind: 'success' | 'warning', autoHide = false): void {
  clearNoticeTimer()
  notice.value = message
  noticeKind.value = kind
  if (!autoHide) return
  noticeTimer = window.setTimeout(() => {
    notice.value = ''
    noticeTimer = null
  }, 3000)
}

function clearNotice(): void {
  clearNoticeTimer()
  notice.value = ''
}

/** 转发项：作者是转发者，正文与互动计数属于被转发的原帖 */
const target = computed<PostBrief>(() => (props.post.kind === 'repost' && props.post.repost_of ? props.post.repost_of : props.post))
const isOwner = computed(() => Boolean(my.value && my.value.id === target.value.author.id))
/** 置顶入口仅顶层帖子卡片（转发卡片指向原帖，不提供置顶） */
const canPin = computed(() => props.actions && props.post.kind === 'post' && Boolean(target.value.can_pin))
const isPinned = computed(() => Boolean(target.value.pinned_at))
/** 停用账号的内容只读：隐藏点赞、评论、转发等互动入口（docs/PAGES_AND_PERMISSIONS.md E-03） */
const authorSuspended = computed(() => target.value.author.is_suspended)
const canInteract = computed(() => Boolean(my.value) && !my.value?.is_ai && !authorSuspended.value)

function requireLogin(): boolean {
  if (my.value) return true
  void router.push({ name: 'login', query: { redirect: router.currentRoute.value.fullPath } })
  return false
}

async function toggleLike(): Promise<void> {
  if (!requireLogin() || busy.value) return
  busy.value = true
  clearNotice()
  try {
    const result = liked.value ? await unlikePost(target.value.id) : await likePost(target.value.id)
    liked.value = result.liked
    likeCount.value = result.like_count
  } catch (err) {
    showNotice(errorMessage(err), 'warning')
  } finally {
    busy.value = false
  }
}

/**
 * 用转发响应构造纯转发列表项：作者是当前登录用户，正文与互动信息属于被转发的原帖。
 * 时间线接口不保证立即返回刚创建的转发，父列表用该对象即时插入。
 */
function buildRepostItem(original: PostBrief, result: RepostState): PostBrief | null {
  const viewer = my.value
  if (!result.repost || !viewer) return null
  const author: UserBrief = {
    id: viewer.id,
    username: viewer.username,
    nickname: viewer.nickname,
    avatar_url: viewer.avatar_url,
    is_ai: viewer.is_ai,
    is_suspended: viewer.status === 'suspended',
  }
  return {
    id: result.repost.id,
    kind: 'repost',
    author,
    visibility: original.visibility,
    content_html: '',
    published_at: original.published_at,
    reposted_at: result.repost.created_at,
    edited: false,
    like_count: original.like_count,
    comment_count: original.comment_count,
    repost_count: result.repost_count,
    attachments: [],
    viewer_state: { liked: liked.value, reposted: true },
    repost_of: original,
  }
}

/** 打开转发弹窗：留空提交纯转发，填写内容发布引用转发 */
function openRepostDialog(): void {
  if (!requireLogin() || busy.value || !canInteract.value) return
  clearNotice()
  repostOpen.value = true
}

/**
 * 转发弹窗完成：纯转发/取消转发更新本地状态并通知父列表；
 * 引用转发把新帖交给父列表插入（详情页等无列表场景只依赖成功提示）。
 */
function onRepostCompleted(payload: RepostDialogResult): void {
  repostOpen.value = false
  if (payload.kind === 'pure') {
    reposted.value = payload.result.reposted
    repostCount.value = payload.result.repost_count
    const item = buildRepostItem(target.value, payload.result)
    if (item) emit('repost-change', { reposted: true, postId: target.value.id, item })
    showNotice('已转发到你的主页，可在个人主页「转发」页签查看。', 'success', true)
    return
  }
  if (payload.kind === 'unrepost') {
    reposted.value = payload.result.reposted
    repostCount.value = payload.result.repost_count
    emit('repost-change', { reposted: false, postId: target.value.id })
    showNotice('已取消转发。', 'success', true)
    return
  }
  emit('post-created', payload.post)
  showNotice('已发布引用转发，可在个人主页查看。', 'success', true)
}

/** 更多菜单删除成功：转发卡片删除的是原帖，父列表按卡片自身 id 移除 */
function handleRemoved(): void {
  emit('removed', props.post.id)
}

/** 置顶状态变化成功：向上通知父列表刷新置顶区块与普通流 */
function handlePinChanged(payload: { id: string; pinned: boolean }): void {
  showNotice(payload.pinned ? '已置顶到个人主页。' : '已取消置顶。', 'success', true)
  emit('pin-changed', payload)
}

function handleMenuError(message: string): void {
  showNotice(message, 'warning')
}

function openDetail(): void {
  void router.push({ name: 'post-detail', params: { postId: target.value.id } })
}

// ---------- 信息流长帖折叠（docs/DESIGN_SYSTEM.md §4.3：最多 4 行 + 查看全文） ----------

const contentEl = ref<HTMLElement | null>(null)
const contentClamped = ref(false)

function updateClamped(): void {
  const el = contentEl.value
  contentClamped.value = !props.detail && el !== null && el.scrollHeight > el.clientHeight + 1
}

onMounted(() => void nextTick(updateClamped))
watch(
  () => [props.post.content_html, props.detail],
  () => void nextTick(updateClamped),
)

// 提示定时器在卸载时清理，避免组件销毁后仍触发状态更新
onUnmounted(clearNoticeTimer)

// 富文本正文的容器级样式（含原有后代选择器的等价 utility）
const richContentClass =
  'text-[15px] leading-[1.75] text-ink [word-break:break-word] [&_a.tl-tag]:font-semibold [&_a.tl-tag]:text-primary [&_a.tl-mention]:font-semibold [&_a.tl-mention]:text-primary [&_img]:max-w-full [&_img]:rounded-[8px] [&_pre]:m-0 [&_pre]:overflow-auto [&_pre]:rounded-[8px] [&_pre]:bg-soft [&_pre]:p-3 [&_pre]:text-[13px] [&_blockquote]:m-0 [&_blockquote]:border-l-[3px] [&_blockquote]:border-solid [&_blockquote]:border-border [&_blockquote]:pl-3 [&_blockquote]:text-ink-2'
</script>

<template>
  <article :class="[ui.card, ui.cardPad, 'grid gap-[10px]']">
    <header class="flex items-start gap-[10px]">
      <RouterLink
        :to="`/users/${post.kind === 'repost' ? post.author.username : target.author.username}`"
        class="flex min-w-0 items-center gap-[10px] text-ink hover:no-underline"
      >
        <UserAvatar :user="post.kind === 'repost' ? post.author : target.author" size="sm" />
        <span class="flex min-w-0 flex-col leading-[1.35]">
          <span class="flex items-center gap-[6px] text-[14.5px]">
            <strong>{{ post.kind === 'repost' ? post.author.nickname || post.author.username : target.author.nickname || target.author.username }}</strong>
            <span v-if="(post.kind === 'repost' ? post.author : target.author).is_ai" :class="[ui.badge, ui.badgeAi]">AI</span>
            <span v-if="(post.kind === 'repost' ? post.author : target.author).is_suspended" :class="[ui.badge, ui.badgeMuted]">已停用</span>
          </span>
          <span class="text-[12.5px] text-ink-3">@{{ post.kind === 'repost' ? post.author.username : target.author.username }}</span>
        </span>
      </RouterLink>

      <span class="ml-auto flex items-center gap-[6px] whitespace-nowrap text-[12.5px] text-ink-3">
        <span v-if="post.kind === 'repost'" class="inline-flex items-center gap-[3px]">
          <AppIcon name="repeat" :size="14" /> 转发了
        </span>
        <span
          v-if="isPinned && post.kind === 'post'"
          :class="[ui.badge, ui.badgeMuted]"
          title="作者已将该帖置顶到个人主页"
        >
          <AppIcon name="pin" :size="12" /> 置顶
        </span>
        <RouterLink :to="`/posts/${target.id}`" :title="timeTitle(target.published_at)" class="text-ink-3">
          {{ relativeTime(post.kind === 'repost' ? post.reposted_at : target.published_at) }}
        </RouterLink>
        <span v-if="target.edited" :class="ui.muted">已编辑</span>
        <span v-if="target.visibility === 'followers'" :class="[ui.badge, ui.badgeMuted]">仅关注者</span>
        <span v-if="target.visibility === 'private'" :class="[ui.badge, ui.badgeMuted]">仅自己</span>
      </span>

      <PostActionMenu
        v-if="isOwner && actions"
        :post-id="target.id"
        :can-edit="isOwner"
        :can-view-history="isOwner"
        :can-delete="isOwner"
        :can-pin="canPin"
        :pinned="isPinned"
        @removed="handleRemoved"
        @error="handleMenuError"
        @pin-changed="handlePinChanged"
      />
    </header>

    <div class="grid gap-2" @click.self="openDetail">
      <div ref="contentEl" :class="[richContentClass, !detail ? 'line-clamp-4' : '']" v-html="target.content_html"></div>
      <button
        v-if="contentClamped"
        type="button"
        class="justify-self-start p-0 text-[13.5px] text-primary hover:underline"
        @click="openDetail"
      >
        查看全文
      </button>
      <AttachmentGallery :attachments="target.attachments" :compact="!detail" />

      <blockquote
        v-if="target.quoted_post"
        class="m-0 rounded-[10px] border border-solid border-border bg-surface-subtle px-3 py-[10px]"
      >
        <template v-if="target.quoted_post.available && target.quoted_post.post">
          <RouterLink
            :to="`/posts/${target.quoted_post.post.id}`"
            class="mb-[6px] flex items-center gap-2 text-[13.5px] text-ink"
          >
            <UserAvatar :user="target.quoted_post.post.author" size="sm" />
            <strong>{{ target.quoted_post.post.author.nickname || target.quoted_post.post.author.username }}</strong>
            <span class="text-[12.5px] text-ink-3">@{{ target.quoted_post.post.author.username }}</span>
          </RouterLink>
          <div :class="[richContentClass, 'text-[14px]!']" v-html="target.quoted_post.post.content_html"></div>
        </template>
        <p v-else :class="ui.muted">原内容不可见</p>
      </blockquote>
    </div>

    <p v-if="notice" :class="[ui.alert, ui.alertVariants[noticeKind].root]">
      <AppIcon
        :name="noticeKind === 'success' ? 'check-circle' : 'alert'"
        :size="17"
        :class="[ui.alertIcon, ui.alertVariants[noticeKind].icon]"
      />
      <span :class="ui.alertText">{{ notice }}</span>
    </p>

    <footer v-if="actions && !authorSuspended" class="flex gap-4 border-t border-solid border-border pt-[6px]">
      <button
        type="button"
        class="inline-flex items-center gap-[5px] rounded-[8px] px-2 py-[5px] text-[13px] text-ink-3 hover-not-disabled:bg-hover hover-not-disabled:text-ink! disabled:cursor-not-allowed disabled:opacity-60"
        :disabled="busy"
        @click="openDetail"
      >
        <AppIcon name="message" :size="17" />
        <span>{{ target.comment_count }}</span>
      </button>
      <button
        v-if="target.visibility === 'public'"
        type="button"
        class="inline-flex items-center gap-[5px] rounded-[8px] px-2 py-[5px] text-[13px] text-ink-3 hover-not-disabled:bg-hover hover-not-disabled:text-ink! disabled:cursor-not-allowed disabled:opacity-60"
        :class="reposted ? 'text-primary! font-bold' : ''"
        :disabled="busy || !canInteract"
        :title="canInteract ? '转发' : '登录后可转发'"
        @click="openRepostDialog"
      >
        <AppIcon name="repeat" :size="17" />
        <span>{{ repostCount }}</span>
      </button>
      <button
        type="button"
        class="inline-flex items-center gap-[5px] rounded-[8px] px-2 py-[5px] text-[13px] text-ink-3 hover-not-disabled:bg-hover hover-not-disabled:text-ink! disabled:cursor-not-allowed disabled:opacity-60"
        :class="liked ? 'text-primary! font-bold' : ''"
        :disabled="busy || !canInteract"
        :title="canInteract ? '点赞' : '登录后可点赞'"
        @click="toggleLike"
      >
        <AppIcon name="heart" :size="17" />
        <span>{{ likeCount }}</span>
      </button>
    </footer>
    <p
      v-else-if="actions && authorSuspended"
      class="m-0 flex items-center gap-[5px] border-t border-solid border-border pt-[6px] text-[12.5px] text-ink-3"
    >
      <AppIcon name="lock" :size="14" />
      作者已停用，互动入口已关闭
    </p>

    <RepostDialog
      v-if="repostOpen"
      :post="target"
      :reposted="reposted"
      @close="repostOpen = false"
      @completed="onRepostCompleted"
    />
  </article>
</template>
