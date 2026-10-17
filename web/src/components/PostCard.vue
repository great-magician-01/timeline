<script setup lang="ts">
// 帖子卡片：时间线、个人主页、标签与搜索结果统一复用（docs/DESIGN_SYSTEM.md）。
// 互动按钮在后端再次鉴权，前端只负责交互与状态提示。
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import AppIcon from './AppIcon.vue'
import AttachmentGallery from './AttachmentGallery.vue'
import UserAvatar from './UserAvatar.vue'
import type { PostBrief } from '../api/types'
import { apiRequest, errorMessage } from '../api/client'
import { likePost, repostPost, unlikePost, unrepostPost } from '../api/social'
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

const emit = defineEmits<{ (event: 'removed', id: string): void }>()

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
const notice = ref('')

/** 转发项：作者是转发者，正文与互动计数属于被转发的原帖 */
const target = computed<PostBrief>(() => (props.post.kind === 'repost' && props.post.repost_of ? props.post.repost_of : props.post))
const isOwner = computed(() => Boolean(my.value && my.value.id === target.value.author.id))
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
  notice.value = ''
  try {
    const result = liked.value ? await unlikePost(target.value.id) : await likePost(target.value.id)
    liked.value = result.liked
    likeCount.value = result.like_count
  } catch (err) {
    notice.value = errorMessage(err)
  } finally {
    busy.value = false
  }
}

async function toggleRepost(): Promise<void> {
  if (!requireLogin() || busy.value) return
  busy.value = true
  notice.value = ''
  try {
    const result = reposted.value ? await unrepostPost(target.value.id) : await repostPost(target.value.id)
    reposted.value = result.reposted
    repostCount.value = result.repost_count
  } catch (err) {
    notice.value = errorMessage(err)
  } finally {
    busy.value = false
  }
}

async function removePost(): Promise<void> {
  if (!window.confirm('删除后内容不再通过普通页面展示，历史版本与附件记录仍会保留。确认删除？')) return
  busy.value = true
  try {
    await apiRequest(`/posts/${target.value.id}/delete`, { method: 'POST' })
    emit('removed', props.post.id)
  } catch (err) {
    notice.value = errorMessage(err)
  } finally {
    busy.value = false
  }
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

const menuOpen = ref(false)

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
        <RouterLink :to="`/posts/${target.id}`" :title="timeTitle(target.published_at)" class="text-ink-3">
          {{ relativeTime(post.kind === 'repost' ? post.reposted_at : target.published_at) }}
        </RouterLink>
        <span v-if="target.edited" :class="ui.muted">已编辑</span>
        <span v-if="target.visibility === 'followers'" :class="[ui.badge, ui.badgeMuted]">仅关注者</span>
        <span v-if="target.visibility === 'private'" :class="[ui.badge, ui.badgeMuted]">仅自己</span>
      </span>

      <div v-if="isOwner && actions" class="relative">
        <button type="button" aria-label="更多操作" @click="menuOpen = !menuOpen">
          <AppIcon name="more" :size="18" />
        </button>
        <div
          v-if="menuOpen"
          class="absolute right-0 top-8 z-20 grid min-w-[150px] rounded-[10px] border border-solid border-border bg-surface p-[6px] shadow-[var(--shadow-pop)]"
          @click="menuOpen = false"
        >
          <RouterLink
            :to="{ name: 'post-edit', params: { postId: target.id } }"
            class="flex items-center gap-2 rounded-[8px] px-[10px] py-[7px] text-left text-[13.5px] text-ink hover:bg-hover hover:no-underline"
          >
            <AppIcon name="pencil" :size="15" /> 编辑
          </RouterLink>
          <RouterLink
            :to="{ name: 'post-history', params: { postId: target.id } }"
            class="flex items-center gap-2 rounded-[8px] px-[10px] py-[7px] text-left text-[13.5px] text-ink hover:bg-hover hover:no-underline"
          >
            <AppIcon name="clock" :size="15" /> 历史版本
          </RouterLink>
          <button
            type="button"
            class="flex items-center gap-2 rounded-[8px] px-[10px] py-[7px] text-left text-[13.5px] text-red hover:bg-hover"
            @click="removePost"
          >
            <AppIcon name="trash" :size="15" /> 删除
          </button>
        </div>
      </div>
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

    <p v-if="notice" :class="[ui.alert, ui.alertVariants.warning.root]">{{ notice }}</p>

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
        type="button"
        class="inline-flex items-center gap-[5px] rounded-[8px] px-2 py-[5px] text-[13px] text-ink-3 hover-not-disabled:bg-hover hover-not-disabled:text-ink! disabled:cursor-not-allowed disabled:opacity-60"
        :class="reposted ? 'text-primary! font-bold' : ''"
        :disabled="busy || !canInteract"
        :title="canInteract ? '转发' : '登录后可转发'"
        @click="toggleRepost"
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
  </article>
</template>
