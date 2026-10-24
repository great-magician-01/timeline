// 游标分页列表：首屏加载、加载更多、刷新与错误状态
// 所有列表接口统一返回 { items, next_cursor, has_more }（docs/API.md §2.5）
import { onMounted, ref, shallowRef } from 'vue'
import type { CursorPage } from '../api/types'
import { errorMessage } from '../api/client'

export interface CursorListOptions<T, P> {
  /** 拉取一页；params 为额外查询条件 */
  fetch: (params: { cursor?: string } & P) => Promise<CursorPage<T>>
  /** 额外查询条件（变化时需手动调用 reload） */
  params: () => P
  /** 是否在挂载时立即加载，默认 true */
  immediate?: boolean
}

export function useCursorList<T, P extends Record<string, unknown>>(options: CursorListOptions<T, P>) {
  const items = shallowRef<T[]>([])
  const loading = ref(false)
  const loadingMore = ref(false)
  const hasMore = ref(false)
  const cursor = ref<string | null>(null)
  const error = ref('')
  const loaded = ref(false)

  /**
   * 请求序号：后发请求作废先发请求。
   * 重新加载（如切换筛选）不再因请求进行中被丢弃，而是丢弃先发请求的响应，
   * 避免界面停留在旧筛选条件的结果上。
   */
  let sequence = 0

  async function load(reset: boolean): Promise<void> {
    if (reset) {
      loading.value = true
      // 先发请求的响应会被序号作废，其 finally 不再复位标志，这里直接清理
      loadingMore.value = false
    } else {
      // 加载更多：首屏加载中、已有加载更多在途或没有下一页时不重复发起
      if (loading.value || loadingMore.value || !hasMore.value) return
      loadingMore.value = true
    }
    const current = ++sequence
    error.value = ''
    try {
      const page = await options.fetch({
        ...options.params(),
        cursor: reset ? undefined : cursor.value ?? undefined,
      })
      // 过期响应直接丢弃，不写入 items/cursor/hasMore/error/loaded
      if (current !== sequence) return
      items.value = reset ? page.items : [...items.value, ...page.items]
      cursor.value = page.next_cursor
      hasMore.value = page.has_more
      loaded.value = true
    } catch (err) {
      if (current !== sequence) return
      error.value = errorMessage(err)
      // 首屏失败时保持空列表；加载更多失败保留既有内容，允许重试
    } finally {
      // 只有最新请求负责复位加载状态，避免旧请求清掉新请求的 loading
      if (current === sequence) {
        loading.value = false
        loadingMore.value = false
      }
    }
  }

  function reload(): Promise<void> {
    cursor.value = null
    hasMore.value = true
    return load(true)
  }

  function loadMore(): Promise<void> {
    return load(false)
  }

  /** 把新条目插入列表头部（本地创建等场景，不改变分页游标）。 */
  function prepend(item: T): void {
    items.value = [item, ...items.value]
    loaded.value = true
  }

  onMounted(() => {
    if (options.immediate !== false) void load(true)
  })

  return { items, loading, loadingMore, hasMore, error, loaded, reload, loadMore, prepend }
}
