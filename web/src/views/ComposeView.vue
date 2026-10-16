<script setup lang="ts">
// 发布新帖与草稿编辑共用页面（docs/PAGES_AND_PERMISSIONS.md U-01 / U-03）
// 编辑器本体与自动保存、冲突处理、定时发布逻辑全部复用 PostComposer，本页只负责路由参数与引用原帖加载。
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import AlertNote from '../components/AlertNote.vue'
import AppIcon from '../components/AppIcon.vue'
import PostComposer from '../components/PostComposer.vue'
import { errorMessage } from '../api/client'
import { getPost } from '../api/posts'
import type { PostDetail } from '../api/types'

const route = useRoute()
const router = useRouter()

function firstValue(value: unknown): string {
  if (typeof value === 'string') return value
  if (Array.isArray(value) && typeof value[0] === 'string') return value[0]
  return ''
}

const draftId = computed(() => firstValue(route.params.draftId))
const quoteId = computed(() => firstValue(route.query.quote))

const editingDraft = computed(() => draftId.value !== '')
const title = computed(() => (editingDraft.value ? '编辑草稿' : '发布新帖'))

const quoted = ref<PostDetail | null>(null)
const quoteError = ref('')
const quoteLoading = ref(false)

/** 引用转发：只允许引用公开帖子，非公开原帖按提示忽略（docs/REQUIREMENTS.md 6.4） */
async function loadQuoted(id: string): Promise<void> {
  quoteLoading.value = true
  quoteError.value = ''
  quoted.value = null
  try {
    const result = await getPost(id)
    if (result.post.visibility !== 'public') {
      quoteError.value = '只有公开帖子可以被引用'
      return
    }
    quoted.value = result.post
  } catch (err) {
    quoteError.value = errorMessage(err)
  } finally {
    quoteLoading.value = false
  }
}

// 加载结果保留在页面状态中：首次保存草稿后会切到草稿编辑路由，组件复用时不重复加载、也不丢失引用
watch(
  quoteId,
  (id) => {
    if (id) {
      void loadQuoted(id)
    } else {
      quoted.value = null
      quoteError.value = ''
    }
  },
  { immediate: true },
)

function onPublished(id: string): void {
  void router.push({ name: 'post-detail', params: { postId: id } })
}
</script>

<template>
  <div class="constrain">
    <div class="stack">
      <header class="page-head">
        <RouterLink class="back-link" :to="editingDraft ? { name: 'drafts' } : { name: 'home' }">
          <AppIcon name="arrow-left" :size="16" />
          {{ editingDraft ? '返回草稿列表' : '返回首页' }}
        </RouterLink>
        <h1 class="page-title">{{ title }}</h1>
        <p class="page-desc">
          {{ editingDraft ? '草稿只有你自己可见，发布前不会进入任何时间线。' : '支持富文本与附件；保存草稿后可以稍后继续编辑或定时发布。' }}
        </p>
      </header>

      <p v-if="quoteLoading" class="card card--pad loading-row">
        <span class="spinner" aria-hidden="true"></span> 正在载入被引用的帖子…
      </p>
      <AlertNote v-else-if="quoteError" kind="warning">{{ quoteError }}</AlertNote>

      <section class="card card--pad">
        <PostComposer :post-id="draftId || undefined" :quoted-post="quoted" @published="onPublished" />
      </section>
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
</style>
