<script setup lang="ts">
// 已发布帖子编辑入口（docs/PAGES_AND_PERMISSIONS.md U-05）
// 已发布帖的保存会生成历史版本，编辑逻辑由 PostComposer 提供；草稿与定时帖统一引导到草稿编辑页。
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import AlertNote from '../components/AlertNote.vue'
import AppIcon from '../components/AppIcon.vue'
import PostComposer from '../components/PostComposer.vue'
import { errorMessage } from '../api/client'
import { getPost } from '../api/posts'
import type { PostDetail } from '../api/types'

const route = useRoute()
const postId = computed(() => String(route.params.postId ?? ''))

const loading = ref(true)
const errorText = ref('')
const post = ref<PostDetail | null>(null)

/** 请求序号：快速切换 :postId 时作废旧响应，避免旧帖子覆盖新帖子 */
let loadSequence = 0

async function load(): Promise<void> {
  const current = ++loadSequence
  loading.value = true
  errorText.value = ''
  try {
    const result = await getPost(postId.value)
    if (current !== loadSequence) return
    post.value = result.post
  } catch (err) {
    if (current !== loadSequence) return
    post.value = null
    errorText.value = errorMessage(err)
  } finally {
    if (current === loadSequence) loading.value = false
  }
}

watch(postId, () => void load(), { immediate: true })

/** 草稿 / 定时 / 删除状态的说明文案：这些状态不在本页直接编辑 */
const redirectHint = computed(() => {
  const current = post.value
  if (!current) return ''
  if (current.status === 'scheduled') {
    return '该帖子已设为定时发布，请到草稿编辑页继续编辑、修改发布时间或立即发布。'
  }
  if (current.status === 'draft') {
    return '该帖子仍是草稿，尚未发布，请到草稿编辑页继续编辑或发布。'
  }
  if (current.status === 'deleted') {
    return '该帖子已被删除，无法继续编辑。'
  }
  return ''
})
</script>

<template>
  <div class="constrain">
    <div class="stack">
      <header class="page-head">
        <RouterLink class="back-link" :to="{ name: 'post-detail', params: { postId } }">
          <AppIcon name="arrow-left" :size="16" /> 返回帖子
        </RouterLink>
        <h1 class="page-title">编辑帖子</h1>
        <p class="page-desc">已发布帖子的每次保存都会生成新的历史版本。</p>
      </header>

      <div v-if="loading" class="card card--pad">
        <p class="loading-row"><span class="spinner" aria-hidden="true"></span> 正在载入帖子…</p>
      </div>

      <template v-else-if="errorText">
        <AlertNote kind="error">{{ errorText }}</AlertNote>
        <div class="actions">
          <button class="btn btn--secondary btn--sm" type="button" @click="load()">重试</button>
          <RouterLink class="btn btn--secondary btn--sm" :to="{ name: 'post-detail', params: { postId } }">
            返回帖子
          </RouterLink>
        </div>
      </template>

      <template v-else-if="post && !post.can_edit">
        <AlertNote kind="error">无权编辑该帖子</AlertNote>
        <div class="actions">
          <RouterLink class="btn btn--secondary btn--sm" :to="{ name: 'post-detail', params: { postId } }">
            返回帖子
          </RouterLink>
        </div>
      </template>

      <template v-else-if="post && post.status === 'published'">
        <AlertNote kind="info">
          保存修改会生成新版本；历史版本与其中的附件对所有有权查看者可见，从当前正文删除的内容不会从历史中消失。
        </AlertNote>
        <section class="card card--pad">
          <PostComposer :post-id="postId" />
        </section>
      </template>

      <template v-else-if="post">
        <AlertNote kind="info">{{ redirectHint }}</AlertNote>
        <div class="actions">
          <RouterLink
            v-if="post.status !== 'deleted'"
            class="btn btn--primary"
            :to="{ name: 'draft-edit', params: { draftId: post.id } }"
          >
            <AppIcon name="pencil" :size="16" /> 继续编辑
          </RouterLink>
          <RouterLink class="btn btn--secondary" :to="{ name: 'post-detail', params: { postId } }">
            返回帖子
          </RouterLink>
        </div>
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
.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
</style>
