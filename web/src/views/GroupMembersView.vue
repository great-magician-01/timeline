<script setup lang="ts">
// 群成员管理：成员分页（按加入时间正序）、拉人、踢人、转让、改名、退出与解散。
// 群主操作按角色显隐；解散/退出后返回群聊列表。
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { ApiError, errorMessage } from '../api/client'
import {
  dissolveGroup,
  getGroup,
  inviteMembers,
  leaveGroup,
  listGroupMembers,
  removeMember,
  renameGroup,
  transferOwner,
} from '../api/groups'
import type { GroupConversation, GroupMember } from '../api/types'
import AlertNote from '../components/AlertNote.vue'
import AppIcon from '../components/AppIcon.vue'
import EmptyState from '../components/EmptyState.vue'
import UserAvatar from '../components/UserAvatar.vue'
import { useBackNavigation } from '../composables/useBackNavigation'
import { authState } from '../stores/auth'
import { ui } from '../styles/ui'

const route = useRoute()
const router = useRouter()
const groupId = computed(() => String(route.params.groupId ?? ''))
const me = computed(() => authState.user)
const { goBack } = useBackNavigation('/groups')

const group = ref<GroupConversation | null>(null)
const members = ref<GroupMember[]>([])
const loading = ref(true)
const loadError = ref('')
const notFound = ref(false)

const hasMore = ref(false)
const memberCursor = ref<string | null>(null)
const loadingMore = ref(false)

const actionError = ref('')
const actionNotice = ref('')
const busy = ref(false)

const inviteInput = ref('')
const renameInput = ref('')

const isOwner = computed(() => group.value?.role === 'owner')
const active = computed(() => group.value?.status === 'active')

async function load(seq: number): Promise<void> {
  loading.value = true
  loadError.value = ''
  notFound.value = false
  group.value = null
  members.value = []
  try {
    const detail = await getGroup(groupId.value)
    if (seq !== loadSeq) return
    group.value = detail.group
    renameInput.value = detail.group.name
    const page = await listGroupMembers(groupId.value, { limit: 50 })
    if (seq !== loadSeq) return
    members.value = page.items
    memberCursor.value = page.next_cursor
    hasMore.value = page.has_more
  } catch (err) {
    if (seq !== loadSeq) return
    if (err instanceof ApiError && err.status === 404) notFound.value = true
    else loadError.value = errorMessage(err)
  } finally {
    if (seq === loadSeq) loading.value = false
  }
}

// 请求序号：切换群后作废在途请求的响应。
let loadSeq = 0

function reload(): void {
  void load(++loadSeq)
}

async function loadMore(): Promise<void> {
  if (!hasMore.value || loadingMore.value || !memberCursor.value) return
  loadingMore.value = true
  try {
    const page = await listGroupMembers(groupId.value, { cursor: memberCursor.value, limit: 50 })
    members.value = [...members.value, ...page.items]
    memberCursor.value = page.next_cursor
    hasMore.value = page.has_more
  } catch (err) {
    actionError.value = errorMessage(err)
  } finally {
    loadingMore.value = false
  }
}

async function run(action: () => Promise<void>, notice: string): Promise<void> {
  if (busy.value) return
  busy.value = true
  actionError.value = ''
  actionNotice.value = ''
  try {
    await action()
    if (notice) actionNotice.value = notice
  } catch (err) {
    actionError.value = errorMessage(err)
  } finally {
    busy.value = false
  }
}

function invite(): void {
  const usernames = inviteInput.value
    .split(/[\s,，]+/)
    .map((entry) => entry.trim())
    .filter(Boolean)
  if (usernames.length === 0) return
  void run(async () => {
    const result = await inviteMembers(groupId.value, usernames)
    inviteInput.value = ''
    // 只把新加入的成员追加进列表（在群成员幂等跳过时不重复展示）。
    const known = new Set(members.value.map((member) => member.user.id))
    for (const member of result.members) {
      if (!known.has(member.user.id)) members.value.push(member)
    }
    if (result.members.length > 0) {
      const detail = await getGroup(groupId.value)
      group.value = detail.group
    }
    actionNotice.value = result.members.length > 0 ? '成员已加入群聊。' : '所选用户都已在群里。'
  }, '')
}

function kick(member: GroupMember): void {
  if (!window.confirm(`把 ${member.user.nickname || member.user.username} 移出群聊？`)) return
  void run(async () => {
    await removeMember(groupId.value, member.user.id)
    members.value = members.value.filter((entry) => entry.user.id !== member.user.id)
    const detail = await getGroup(groupId.value)
    group.value = detail.group
  }, '')
}

function transfer(member: GroupMember): void {
  if (!window.confirm(`把群主转让给 ${member.user.nickname || member.user.username}？转让后你将变为普通成员。`)) return
  void run(async () => {
    await transferOwner(groupId.value, member.user.id)
    reload()
  }, '')
}

function submitRename(): void {
  const name = renameInput.value.trim()
  if (!name || name === group.value?.name) return
  void run(async () => {
    const result = await renameGroup(groupId.value, name)
    group.value = result.group
  }, '群名称已更新。')
}

function leave(): void {
  if (!window.confirm('退出这个群？退出后不再接收新消息。')) return
  void run(async () => {
    await leaveGroup(groupId.value)
    router.push('/groups')
  }, '')
}

function dissolve(): void {
  if (!window.confirm('解散这个群？群将变为只读，全部成员都会看到「已解散」。')) return
  void run(async () => {
    await dissolveGroup(groupId.value)
    router.push('/groups')
  }, '')
}

// 路由参数变化（同组件复用导航到另一群）时重新加载。
watch(groupId, () => void load(++loadSeq), { immediate: true })
</script>

<template>
  <div :class="[ui.constrain]">
    <div :class="[ui.stack]">
      <p v-if="loading" :class="[ui.card, ui.loadingRow]">
        <span :class="[ui.spinner]" aria-hidden="true"></span> 加载中…
      </p>

      <section v-else-if="notFound" :class="[ui.card, ui.cardPad]">
        <EmptyState icon="users" title="群聊不存在或不可见" description="该群可能已被解散或移除，或你不是群成员。">
          <RouterLink :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'mt-3']" to="/groups">返回群聊列表</RouterLink>
        </EmptyState>
      </section>

      <template v-else-if="loadError">
        <AlertNote kind="error">{{ loadError }}</AlertNote>
        <button :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'self-start']" type="button" @click="reload">重试</button>
      </template>

      <template v-else-if="group">
        <section :class="[ui.card, ui.cardPad]">
          <div class="flex items-center gap-2.5">
            <button
              :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]"
              type="button"
              aria-label="返回"
              @click="goBack"
            >
              <AppIcon name="arrow-left" :size="16" />
            </button>
            <span class="flex min-w-0 flex-1 flex-col leading-[1.35]">
              <span class="flex flex-wrap items-center gap-1.5 text-[15px] font-bold">
                {{ group.name }}
                <span v-if="group.status === 'dissolved'" :class="[ui.badge, ui.badgeMuted]">已解散</span>
              </span>
              <span class="text-[12.5px] text-ink-3">群成员（{{ group.member_count }}）</span>
            </span>
            <RouterLink :to="`/groups/${group.id}`" :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]">
              返回对话
            </RouterLink>
          </div>
        </section>

        <AlertNote v-if="actionError" kind="error">{{ actionError }}</AlertNote>
        <AlertNote v-if="actionNotice" kind="info">{{ actionNotice }}</AlertNote>

        <!-- 群主管理：仅 active 群展示 -->
        <section v-if="isOwner && active" :class="[ui.card, ui.cardPad]">
          <h2 class="m-0 mb-2 text-[14.5px] font-bold">群管理</h2>
          <div class="grid gap-3">
            <form class="grid gap-2" @submit.prevent="submitRename">
              <label class="text-[13px] font-semibold text-ink-2" for="group-rename">群名称</label>
              <div class="flex gap-2">
                <input
                  id="group-rename"
                  v-model="renameInput"
                  :class="[ui.input]"
                  type="text"
                  maxlength="30"
                  :disabled="busy"
                />
                <button
                  :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'shrink-0']"
                  type="submit"
                  :disabled="busy || renameInput.trim() === group.name"
                >
                  保存
                </button>
              </div>
            </form>
            <form class="grid gap-2" @submit.prevent="invite">
              <label class="text-[13px] font-semibold text-ink-2" for="group-invite">拉人（用户名，空格或逗号分隔，一次最多 20 人）</label>
              <div class="flex gap-2">
                <input
                  id="group-invite"
                  v-model="inviteInput"
                  :class="[ui.input]"
                  type="text"
                  placeholder="用户名"
                  :disabled="busy"
                />
                <button
                  :class="[ui.button, ui.buttonPrimary, ui.buttonSmall, 'shrink-0']"
                  type="submit"
                  :disabled="busy || inviteInput.trim().length === 0"
                >
                  拉入群
                </button>
              </div>
            </form>
            <div class="flex flex-wrap justify-end gap-2 border-t border-solid border-border pt-3">
              <button
                :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]"
                type="button"
                :disabled="busy"
                @click="leave"
              >
                转让后退出 / 直接退出需先转让
              </button>
              <button
                :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]"
                type="button"
                :disabled="busy"
                @click="dissolve"
              >
                解散群聊
              </button>
            </div>
            <p class="m-0 text-[12px] text-ink-3">
              群主不能直接退出；先在成员列表转让群主，或解散群聊。关闭私信的用户与停用账号不能被拉入。
            </p>
          </div>
        </section>

        <!-- 普通成员：退出 -->
        <section v-else-if="active" :class="[ui.card, ui.cardPad]">
          <div class="flex items-center justify-between gap-2">
            <p class="m-0 text-[13px] text-ink-2">你可以随时退出这个群。</p>
            <button
              :class="[ui.button, ui.buttonSecondary, ui.buttonSmall]"
              type="button"
              :disabled="busy"
              @click="leave"
            >
              退出群聊
            </button>
          </div>
        </section>

        <section :class="[ui.card, 'overflow-hidden']">
          <article
            v-for="member in members"
            :key="member.user.id"
            class="flex items-center gap-3 border-b border-solid border-border px-4 py-3 [&:last-of-type]:border-b-0"
          >
            <RouterLink
              :to="`/users/${member.user.username}`"
              class="flex min-w-0 flex-1 items-center gap-2.5 text-ink hover:no-underline"
            >
              <UserAvatar :user="member.user" size="sm" />
              <span class="flex min-w-0 flex-1 flex-col leading-[1.35]">
                <span class="flex flex-wrap items-center gap-1.5 text-[14px]">
                  <strong>{{ member.user.nickname || member.user.username }}</strong>
                  <span v-if="member.user.is_ai" :class="[ui.badge, ui.badgeAi]">AI</span>
                  <span v-if="member.user.is_suspended" :class="[ui.badge, ui.badgeMuted]">已停用</span>
                  <span v-if="member.role === 'owner'" :class="[ui.badge, ui.badgeMuted]">群主</span>
                </span>
                <span class="text-[12.5px] text-ink-3">@{{ member.user.username }}</span>
              </span>
            </RouterLink>

            <template v-if="isOwner && active && member.role !== 'owner' && member.user.id !== me?.id">
              <button
                :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'shrink-0']"
                type="button"
                :disabled="busy"
                @click="transfer(member)"
              >
                转让
              </button>
              <button
                :class="[ui.button, ui.buttonSecondary, ui.buttonSmall, 'shrink-0']"
                type="button"
                :disabled="busy"
                aria-label="移出群聊"
                @click="kick(member)"
              >
                <AppIcon name="trash" :size="16" />
              </button>
            </template>
          </article>

          <div class="flex justify-center py-3">
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
        </section>
      </template>
    </div>
  </div>
</template>
