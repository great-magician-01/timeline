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
  <li class="comment" :class="{ 'is-nested': nested }">
    <RouterLink :to="`/users/${comment.author.username}`" class="avatar-link">
      <UserAvatar :user="comment.author" size="sm" />
    </RouterLink>
    <div class="main">
      <p class="head">
        <RouterLink :to="`/users/${comment.author.username}`" class="name">
          {{ comment.author.nickname || comment.author.username }}
        </RouterLink>
        <span class="handle">@{{ comment.author.username }}</span>
        <span v-if="comment.author.is_ai" class="badge badge--ai">AI</span>
        <span v-if="comment.author.is_suspended" class="badge badge--muted">已停用</span>
        <span class="dot">·</span>
        <span :title="timeTitle(comment.created_at)" class="muted">{{ relativeTime(comment.created_at) }}</span>
      </p>

      <p v-if="deleted" class="deleted">
        <AppIcon name="info" :size="15" />
        <span>评论已删除</span>
      </p>
      <div v-else class="content" v-html="comment.content_html"></div>

      <div v-if="!deleted && !authorSuspended" class="acts">
        <button
          type="button"
          class="act"
          :class="{ 'is-on': liked }"
          :disabled="busy"
          @click="toggleLike"
        >
          <AppIcon name="heart" :size="15" />
          <span v-if="likeCount > 0">{{ likeCount }}</span>
          <span v-else>点赞</span>
        </button>
        <button v-if="replyable" type="button" class="act" :disabled="busy" @click="emit('reply', comment)">
          <AppIcon name="message" :size="15" />
          <span>回复</span>
        </button>
        <button v-if="canDelete" type="button" class="act danger" :disabled="busy" @click="remove">
          <AppIcon name="trash" :size="15" />
          <span>删除</span>
        </button>
      </div>
      <p v-else-if="!deleted && authorSuspended" class="readonly-note">
        <AppIcon name="lock" :size="14" />
        作者已停用，互动入口已关闭
      </p>

      <p v-if="notice" class="alert alert--warning">{{ notice }}</p>

      <ul v-if="!nested && comment.reply_preview && comment.reply_preview.length > 0" class="replies">
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
        class="more-replies"
      >
        查看全部 {{ comment.reply_count }} 条回复
      </RouterLink>
    </div>
  </li>
</template>

<style scoped>
.comment {
  display: flex;
  gap: 10px;
  padding: 12px 0;
  border-top: 1px solid var(--border);
}
.comment:first-child {
  border-top: none;
}
.is-nested {
  padding: 8px 0;
  border-top: 1px dashed var(--border);
}
.main {
  min-width: 0;
  flex: 1;
  display: grid;
  gap: 6px;
}
.head {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 0;
  font-size: 13.5px;
  flex-wrap: wrap;
}
.name {
  font-weight: 700;
  color: var(--ink);
}
.handle,
.dot,
.muted {
  color: var(--ink-3);
  font-size: 12.5px;
}
.content {
  font-size: 14.5px;
  line-height: 1.7;
  word-break: break-word;
}
.content :deep(a.tl-tag),
.content :deep(a.tl-mention) {
  color: var(--primary);
  font-weight: 600;
}
.deleted {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 0;
  padding: 8px 10px;
  border-radius: 8px;
  background: var(--soft);
  color: var(--ink-3);
  font-size: 13px;
}
.acts {
  display: flex;
  gap: 12px;
}
.act {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 3px 6px;
  border-radius: 6px;
  font-size: 12.5px;
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
.act.danger:hover:not(:disabled) {
  color: var(--red);
}
.readonly-note {
  display: flex;
  align-items: center;
  gap: 5px;
  margin: 0;
  font-size: 12.5px;
  color: var(--ink-3);
}
.replies {
  margin: 0;
  padding: 0 0 0 4px;
  list-style: none;
  border-left: 2px solid var(--border);
}
.more-replies {
  font-size: 12.5px;
  color: var(--primary);
}
</style>
