<script setup lang="ts">
// 评论与楼中楼：删除后显示稳定占位，子回复继续保留（docs/WORKFLOWS.md §5）
import { computed, ref, watch } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import AppIcon from './AppIcon.vue'
import UserAvatar from './UserAvatar.vue'
import type { Comment } from '../api/types'
import { errorMessage } from '../api/client'
import { deleteComment, likeComment, unlikeComment } from '../api/social'
import { authState } from '../stores/auth'
import { relativeTime, timeTitle } from '../utils/format'
import { ui } from '../styles/ui'

const props = withDefaults(
  defineProps<{
    comment: Comment
    /** 楼中楼里的回复不再递归展开预览 */
    nested?: boolean
    /** 是否展示“回复”按钮 */
    replyable?: boolean
  }>(),
  { nested: false, replyable: true },
)

const emit = defineEmits<{
  (event: 'reply', comment: Comment): void
  (event: 'changed'): void
}>()

const router = useRouter()
const my = computed(() => authState.user)
const liked = ref(props.comment.liked_by_me)
const likeCount = ref(props.comment.like_count)

// 父列表重新拉取后同步点赞状态与计数：本地 ref 不会自动跟随 props 变化
watch(
  () => props.comment.liked_by_me,
  (value) => {
    liked.value = value
  },
)
watch(
  () => props.comment.like_count,
  (value) => {
    likeCount.value = value
  },
)
const busy = ref(false)
const notice = ref('')
const deleted = computed(() => props.comment.status === 'deleted')
const canDelete = computed(() => props.comment.can_delete)
/** 停用账号的评论只读：隐藏点赞、回复与删除入口（docs/PAGES_AND_PERMISSIONS.md E-03） */
const authorSuspended = computed(() => props.comment.author.is_suspended)

function requireLogin(): boolean {
  if (my.value) return true
  void router.push({ name: 'login', query: { redirect: router.currentRoute.value.fullPath } })
  return false
}

async function toggleLike(): Promise<void> {
  if (deleted.value || authorSuspended.value || !requireLogin() || busy.value) return
  busy.value = true
  notice.value = ''
  try {
    const result = liked.value ? await unlikeComment(props.comment.id) : await likeComment(props.comment.id)
    liked.value = result.liked
    likeCount.value = result.like_count
  } catch (err) {
    notice.value = errorMessage(err)
  } finally {
    busy.value = false
  }
}

async function remove(): Promise<void> {
  if (!window.confirm('确认删除这条评论？删除后原位置会显示占位，子回复仍会保留。')) return
  busy.value = true
  notice.value = ''
  try {
    await deleteComment(props.comment.id)
    emit('changed')
  } catch (err) {
    notice.value = errorMessage(err)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <li
    :class="[
      'flex gap-[10px] border-t border-solid border-border first:border-t-0',
      nested ? 'border-dashed! py-2' : 'py-3',
    ]"
  >
    <RouterLink :to="`/users/${comment.author.username}`">
      <UserAvatar :user="comment.author" size="sm" />
    </RouterLink>
    <div class="grid min-w-0 flex-1 gap-[6px]">
      <p class="m-0 flex flex-wrap items-center gap-[6px] text-[13.5px]">
        <RouterLink :to="`/users/${comment.author.username}`" class="font-bold text-ink">
          {{ comment.author.nickname || comment.author.username }}
        </RouterLink>
        <span class="text-[12.5px] text-ink-3">@{{ comment.author.username }}</span>
        <span v-if="comment.author.is_ai" :class="[ui.badge, ui.badgeAi]">AI</span>
        <span v-if="comment.author.is_suspended" :class="[ui.badge, ui.badgeMuted]">已停用</span>
        <span class="text-[12.5px] text-ink-3">·</span>
        <span :title="timeTitle(comment.created_at)" class="text-[12.5px] text-ink-3">{{
          relativeTime(comment.created_at)
        }}</span>
      </p>

      <p v-if="deleted" class="m-0 flex items-center gap-[6px] rounded-[8px] bg-soft px-[10px] py-2 text-[13px] text-ink-3">
        <AppIcon name="info" :size="15" />
        <span>评论已删除</span>
      </p>
      <div
        v-else
        class="text-[14.5px] leading-[1.7] [word-break:break-word] [&_a.tl-mention]:font-semibold [&_a.tl-mention]:text-primary [&_a.tl-tag]:font-semibold [&_a.tl-tag]:text-primary"
        v-html="comment.content_html"
      ></div>

      <div v-if="!deleted && !authorSuspended" class="flex gap-3">
        <button
          type="button"
          class="inline-flex items-center gap-[5px] rounded-[6px] px-[6px] py-[3px] text-[12.5px] text-ink-3 hover-not-disabled:bg-hover hover-not-disabled:text-ink!"
          :class="liked ? 'text-primary! font-bold' : ''"
          :disabled="busy"
          @click="toggleLike"
        >
          <AppIcon name="heart" :size="15" />
          <span v-if="likeCount > 0">{{ likeCount }}</span>
          <span v-else>点赞</span>
        </button>
        <button
          v-if="replyable"
          type="button"
          class="inline-flex items-center gap-[5px] rounded-[6px] px-[6px] py-[3px] text-[12.5px] text-ink-3 hover-not-disabled:bg-hover hover-not-disabled:text-ink!"
          :disabled="busy"
          @click="emit('reply', comment)"
        >
          <AppIcon name="message" :size="15" />
          <span>回复</span>
        </button>
        <button
          v-if="canDelete"
          type="button"
          class="inline-flex items-center gap-[5px] rounded-[6px] px-[6px] py-[3px] text-[12.5px] text-ink-3 hover-not-disabled:bg-hover hover-not-disabled:text-red!"
          :disabled="busy"
          @click="remove"
        >
          <AppIcon name="trash" :size="15" />
          <span>删除</span>
        </button>
      </div>
      <p v-else-if="!deleted && authorSuspended" class="m-0 flex items-center gap-[5px] text-[12.5px] text-ink-3">
        <AppIcon name="lock" :size="14" />
        作者已停用，互动入口已关闭
      </p>

      <p v-if="notice" :class="[ui.alert, ui.alertVariants.warning.root]">{{ notice }}</p>

      <ul
        v-if="!nested && comment.reply_preview && comment.reply_preview.length > 0"
        class="m-0 list-none border-l-2 border-solid border-border py-0 pl-1"
      >
        <CommentItem
          v-for="reply in comment.reply_preview"
          :key="reply.id"
          :comment="reply"
          nested
          :replyable="false"
          @changed="emit('changed')"
        />
      </ul>
      <RouterLink
        v-if="!nested && comment.reply_count > (comment.reply_preview?.length ?? 0)"
        :to="`/posts/${comment.post_id}`"
        class="text-[12.5px] text-primary"
      >
        查看全部 {{ comment.reply_count }} 条回复
      </RouterLink>
    </div>
  </li>
</template>
