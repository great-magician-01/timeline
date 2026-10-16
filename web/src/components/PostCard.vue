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
const canInteract = computed(() => Boolean(my.value) && !my.value?.is_ai)

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
</script>

<template>
  <article class="post card card--pad">
    <header class="head">
      <RouterLink :to="`/users/${post.kind === 'repost' ? post.author.username : target.author.username}`" class="who">
        <UserAvatar :user="post.kind === 'repost' ? post.author : target.author" size="sm" />
        <span class="names">
          <span class="row">
            <strong>{{ post.kind === 'repost' ? post.author.nickname || post.author.username : target.author.nickname || target.author.username }}</strong>
            <span v-if="(post.kind === 'repost' ? post.author : target.author).is_ai" class="badge badge--ai">AI</span>
            <span v-if="(post.kind === 'repost' ? post.author : target.author).is_suspended" class="badge badge--muted">已停用</span>
          </span>
          <span class="handle">@{{ post.kind === 'repost' ? post.author.username : target.author.username }}</span>
        </span>
      </RouterLink>

      <span class="meta">
        <span v-if="post.kind === 'repost'" class="repost-note">
          <AppIcon name="repeat" :size="14" /> 转发了
        </span>
        <RouterLink :to="`/posts/${target.id}`" :title="timeTitle(target.published_at)" class="time">
          {{ relativeTime(post.kind === 'repost' ? post.reposted_at : target.published_at) }}
        </RouterLink>
        <span v-if="target.edited" class="muted">已编辑</span>
        <span v-if="target.visibility === 'followers'" class="badge badge--muted">仅关注者</span>
        <span v-if="target.visibility === 'private'" class="badge badge--muted">仅自己</span>
      </span>

      <div v-if="isOwner && actions" class="menu">
        <button type="button" class="icon-btn" aria-label="更多操作" @click="menuOpen = !menuOpen">
          <AppIcon name="more" :size="18" />
        </button>
        <div v-if="menuOpen" class="menu-pop" @click="menuOpen = false">
          <RouterLink :to="{ name: 'post-edit', params: { postId: target.id } }">
            <AppIcon name="pencil" :size="15" /> 编辑
          </RouterLink>
          <RouterLink :to="{ name: 'post-history', params: { postId: target.id } }">
            <AppIcon name="clock" :size="15" /> 历史版本
          </RouterLink>
          <button type="button" class="danger" @click="removePost">
            <AppIcon name="trash" :size="15" /> 删除
          </button>
        </div>
      </div>
    </header>

    <div class="body" :class="{ 'is-clickable': !detail }" @click.self="openDetail">
      <div ref="contentEl" class="content" :class="{ 'is-clamped': !detail }" v-html="target.content_html"></div>
      <button v-if="contentClamped" type="button" class="more-link" @click="openDetail">查看全文</button>
      <AttachmentGallery :attachments="target.attachments" :compact="!detail" />

      <blockquote v-if="target.quoted_post" class="quote">
        <template v-if="target.quoted_post.available && target.quoted_post.post">
          <RouterLink :to="`/posts/${target.quoted_post.post.id}`" class="quote-head">
            <UserAvatar :user="target.quoted_post.post.author" size="sm" />
            <strong>{{ target.quoted_post.post.author.nickname || target.quoted_post.post.author.username }}</strong>
            <span class="handle">@{{ target.quoted_post.post.author.username }}</span>
          </RouterLink>
          <div class="content" v-html="target.quoted_post.post.content_html"></div>
        </template>
        <p v-else class="muted">原内容不可见</p>
      </blockquote>
    </div>

    <p v-if="notice" class="alert alert--warning">{{ notice }}</p>

    <footer v-if="actions" class="actions">
      <button type="button" class="act" :disabled="busy" @click="openDetail">
        <AppIcon name="message" :size="17" />
        <span>{{ target.comment_count }}</span>
      </button>
      <button
        type="button"
        class="act"
        :class="{ 'is-on': reposted }"
        :disabled="busy || !canInteract"
        :title="canInteract ? '转发' : '登录后可转发'"
        @click="toggleRepost"
      >
        <AppIcon name="repeat" :size="17" />
        <span>{{ repostCount }}</span>
      </button>
      <button
        type="button"
        class="act"
        :class="{ 'is-on': liked }"
        :disabled="busy || !canInteract"
        :title="canInteract ? '点赞' : '登录后可点赞'"
        @click="toggleLike"
      >
        <AppIcon name="heart" :size="17" />
        <span>{{ likeCount }}</span>
      </button>
    </footer>
  </article>
</template>

<style scoped>
.post {
  display: grid;
  gap: 10px;
}
.head {
  display: flex;
  align-items: flex-start;
  gap: 10px;
}
.who {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  color: var(--ink);
}
.who:hover {
  text-decoration: none;
}
.names {
  display: flex;
  flex-direction: column;
  min-width: 0;
  line-height: 1.35;
}
.row {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 14.5px;
}
.handle {
  font-size: 12.5px;
  color: var(--ink-3);
}
.meta {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-left: auto;
  font-size: 12.5px;
  color: var(--ink-3);
  white-space: nowrap;
}
.meta .time {
  color: var(--ink-3);
}
.repost-note {
  display: inline-flex;
  align-items: center;
  gap: 3px;
}
.menu {
  position: relative;
}
.menu-pop {
  position: absolute;
  right: 0;
  top: 32px;
  z-index: 20;
  display: grid;
  min-width: 150px;
  padding: 6px;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--surface);
  box-shadow: var(--shadow-pop);
}
.menu-pop a,
.menu-pop button {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 7px 10px;
  border-radius: 8px;
  font-size: 13.5px;
  color: var(--ink);
  text-align: left;
}
.menu-pop a:hover,
.menu-pop button:hover {
  background: var(--hover);
  text-decoration: none;
}
.menu-pop .danger {
  color: var(--red);
}
.body {
  display: grid;
  gap: 8px;
}
.content {
  font-size: 15px;
  line-height: 1.75;
  color: var(--ink);
  word-break: break-word;
}
/* 列表页长帖最多 4 行；详情页不加该类，保持完整展示 */
.content.is-clamped {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 4;
  overflow: hidden;
}
.more-link {
  justify-self: start;
  padding: 0;
  font-size: 13.5px;
  color: var(--primary);
}
.more-link:hover {
  text-decoration: underline;
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
.quote {
  margin: 0;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--surface-subtle);
}
.quote-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
  font-size: 13.5px;
  color: var(--ink);
}
.quote .content {
  font-size: 14px;
}
.actions {
  display: flex;
  gap: 16px;
  padding-top: 6px;
  border-top: 1px solid var(--border);
}
.act {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 5px 8px;
  border-radius: 8px;
  font-size: 13px;
  color: var(--ink-3);
}
.act:hover:not(:disabled) {
  background: var(--hover);
  color: var(--ink);
}
.act.is-on {
  color: var(--primary);
  font-weight: 700;
}
.act:disabled {
  cursor: not-allowed;
  opacity: 0.6;
}
</style>
