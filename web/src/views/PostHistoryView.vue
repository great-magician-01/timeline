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
import { ui } from '../styles/ui'
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
  <div :class="[ui.constrain]">
    <div :class="[ui.stack]">
      <header :class="[ui.pageHead]">
        <RouterLink class="mb-2 inline-flex items-center gap-1.5 text-[13.5px] text-ink-2 hover:text-ink hover:no-underline" :to="{ name: 'post-detail', params: { postId } }">
          <AppIcon name="arrow-left" :size="16" /> 返回帖子
        </RouterLink>
        <h1 :class="[ui.pageTitle]">编辑历史</h1>
        <p :class="[ui.pageDesc]">历史版本与其中的附件对所有有权查看该帖子的人可见。</p>
      </header>

      <AlertNote v-if="postError" kind="error">{{ postError }}</AlertNote>
      <AlertNote v-if="notice" kind="success">{{ notice }}</AlertNote>
      <AlertNote v-if="actionError" kind="error">{{ actionError }}</AlertNote>

      <div v-if="loading" :class="[ui.card, ui.cardPad]">
        <p :class="[ui.loadingRow]"><span :class="[ui.spinner]" aria-hidden="true"></span> 正在加载历史版本…</p>
      </div>

      <template v-else-if="error && items.length === 0">
        <AlertNote kind="error">{{ error }}</AlertNote>
        <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'self-start']" type="button" @click="reload()">重试</button>
      </template>

      <section v-else-if="items.length === 0" :class="[ui.card]">
        <EmptyState
          icon="scroll"
          title="还没有历史版本"
          description="帖子发布后每次保存都会保留一个版本，恢复历史也会生成新版本。"
        />
      </section>

      <template v-else>
        <section :class="[ui.card, 'overflow-hidden']">
          <p class="m-0 px-4 pt-3 pb-2 text-[12.5px] text-ink-3">共 {{ items.length }} 个版本{{ hasMore ? '（还有更多）' : '' }}</p>
          <button
            v-for="item in items"
            :key="item.id"
            type="button"
            class="flex w-full flex-col gap-1 border-t border-solid border-border px-4 py-3 text-left transition-[background] duration-150 ease-[ease]"
            :class="selectedId === item.id ? 'bg-primary-soft' : 'hover:bg-hover'"
            @click="selectVersion(item.id)"
          >
            <span class="flex flex-wrap items-center gap-2 text-[14px]">
              <strong>版本 {{ item.version_number }}</strong>
              <span :class="[ui.badge, item.visibility !== 'public' && ui.badgeMuted]">
                {{ visibilityLabel(item.visibility) }}
              </span>
              <span v-if="item.restored_from_version_id" :class="[ui.badge, ui.badgeMuted]">由历史恢复</span>
              <span :class="[ui.muted, 'text-[12.5px]!']">{{ absoluteTime(item.created_at) }}</span>
            </span>
            <span class="text-[13px] text-ink-2 [word-break:break-word]">{{ plainSummary(item.summary || '') || '（无摘要）' }}</span>
          </button>

          <div class="flex justify-center border-t border-solid border-border px-4 py-[14px]">
            <button
              v-if="hasMore"
              :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]"
              type="button"
              :disabled="loadingMore"
              @click="loadMore()"
            >
              <span v-if="loadingMore" :class="[ui.spinner, 'size-[14px]! border-border! border-t-primary! opacity-80']" aria-hidden="true"></span>
              {{ loadingMore ? '正在加载…' : '加载更多' }}
            </button>
            <p v-else :class="[ui.muted, 'm-0']">已加载全部 {{ items.length }} 个版本</p>
          </div>
        </section>

        <AlertNote v-if="error" kind="error">{{ error }}</AlertNote>

        <section v-if="selectedId" ref="detailPanel" :class="[ui.card, ui.cardPad]">
          <p v-if="versionLoading" :class="[ui.loadingRow]">
            <span :class="[ui.spinner]" aria-hidden="true"></span> 正在加载版本内容…
          </p>
          <AlertNote v-else-if="versionError" kind="error">{{ versionError }}</AlertNote>

          <template v-else-if="version">
            <header class="mb-3 flex items-start justify-between gap-3 shell-down:flex-col shell-down:items-stretch">
              <div>
                <p class="m-0 flex flex-wrap items-center gap-2 text-[15px] font-bold">
                  版本 {{ version.version_number }}
                  <span :class="[ui.badge, version.visibility !== 'public' && ui.badgeMuted]">
                    {{ visibilityLabel(version.visibility) }}
                  </span>
                  <span v-if="version.restored_from_version_id" :class="[ui.badge, ui.badgeMuted]">由历史恢复</span>
                </p>
                <p :class="[ui.muted, 'm-0 mt-1']">{{ absoluteTime(version.created_at) }}</p>
              </div>
              <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'shell-down:self-start']" type="button" @click="selectVersion(version.id)">
                <AppIcon name="x" :size="15" /> 收起
              </button>
            </header>

            <div
              class="text-[15px] leading-[1.75] text-ink [word-break:break-word] [&_a.tl-tag]:font-semibold [&_a.tl-tag]:text-primary [&_a.tl-mention]:font-semibold [&_a.tl-mention]:text-primary [&_img]:max-w-full [&_img]:rounded-lg [&_pre]:m-0 [&_pre]:overflow-auto [&_pre]:rounded-lg [&_pre]:bg-soft [&_pre]:p-3 [&_pre]:text-[13px] [&_blockquote]:m-0 [&_blockquote]:border-l-[3px] [&_blockquote]:border-solid [&_blockquote]:border-border [&_blockquote]:pl-3 [&_blockquote]:text-ink-2"
              v-html="version.content_html"
            ></div>
            <AttachmentGallery :attachments="version.attachments" />

            <div v-if="canRestore" class="mt-4 flex flex-wrap items-center gap-2.5 border-t border-solid border-border pt-[14px]">
              <button :class="[ui.button, ui.buttonPrimary, ui.buttonSmall, 'shell-down:w-full']" type="button" :disabled="restoring" @click="restore">
                <span v-if="restoring" :class="[ui.spinner, 'size-[14px]! border-border! border-t-primary! opacity-80']" aria-hidden="true"></span>
                <AppIcon v-else name="refresh" :size="15" />
                {{ restoring ? '恢复中…' : '恢复此版本' }}
              </button>
              <p :class="[ui.muted, 'm-0']">恢复会生成新的当前版本，不会删除或覆盖任何历史版本。</p>
            </div>
          </template>
        </section>
      </template>
    </div>
  </div>
</template>
