// 游标分页 composable：首屏/追加/过期响应作废/失败保留/头部插入
// onMounted 依赖组件实例，纯逻辑测试中 mock 为 no-op；挂载即加载的路径由页面集成覆盖。
import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('vue', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue')>()
  return { ...actual, onMounted: () => {} }
})

import { useCursorList } from './useCursorList'
import { ApiError } from '../api/client'
import type { CursorPage } from '../api/types'

interface Item {
  id: number
}

// FetchFn 是 fetch 选项的最小签名：具名签名让 vi.fn 的调用断言与返回值都有类型
type FetchFn = (params: { cursor?: string }) => Promise<CursorPage<Item>>

function page(items: Item[], nextCursor: string | null): CursorPage<Item> {
  return { items, next_cursor: nextCursor, has_more: nextCursor !== null }
}

function makeList(fetch: FetchFn) {
  return useCursorList<Item, Record<string, unknown>>({ fetch, params: () => ({}) })
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('useCursorList 加载与追加', () => {
  it('首屏加载写入列表与游标，加载更多拼接并透传游标', async () => {
    const fetch = vi
      .fn<FetchFn>()
      .mockResolvedValueOnce(page([{ id: 1 }], 'c1'))
      .mockResolvedValueOnce(page([{ id: 2 }, { id: 3 }], null))
    const list = makeList(fetch)

    await list.reload()
    expect(fetch).toHaveBeenNthCalledWith(1, { cursor: undefined })
    expect(list.items.value).toEqual([{ id: 1 }])
    expect(list.hasMore.value).toBe(true)
    expect(list.loaded.value).toBe(true)

    await list.loadMore()
    expect(fetch).toHaveBeenNthCalledWith(2, { cursor: 'c1' })
    expect(list.items.value).toEqual([{ id: 1 }, { id: 2 }, { id: 3 }])
    expect(list.hasMore.value).toBe(false)

    await list.loadMore()
    expect(fetch).toHaveBeenCalledTimes(2)
  })

  it('加载参数包含额外查询条件', async () => {
    const fetch = vi.fn<FetchFn>().mockResolvedValue(page([], null))
    const list = useCursorList<Item, { tag: string }>({ fetch, params: () => ({ tag: 'dev' }) })
    await list.reload()
    expect(fetch).toHaveBeenCalledWith({ tag: 'dev', cursor: undefined })
  })
})

describe('useCursorList 并发与过期响应', () => {
  it('后发请求作废先发请求：过期响应不写入任何状态', async () => {
    let resolveFirst!: (value: CursorPage<Item>) => void
    let resolveSecond!: (value: CursorPage<Item>) => void
    const fetch = vi
      .fn<FetchFn>()
      .mockImplementationOnce(() => new Promise<CursorPage<Item>>((resolve) => { resolveFirst = resolve }))
      .mockImplementationOnce(() => new Promise<CursorPage<Item>>((resolve) => { resolveSecond = resolve }))
    const list = makeList(fetch)

    const first = list.reload()
    const second = list.reload()
    expect(list.loading.value).toBe(true)

    resolveSecond(page([{ id: 9 }], null))
    await second
    expect(list.items.value).toEqual([{ id: 9 }])
    expect(list.loading.value).toBe(false)

    resolveFirst(page([{ id: 1 }], 'stale'))
    await first
    expect(list.items.value).toEqual([{ id: 9 }])
    expect(list.hasMore.value).toBe(false)
    expect(list.error.value).toBe('')
  })

  it('加载更多在途时不重复发起', async () => {
    let resolveMore!: (value: CursorPage<Item>) => void
    const fetch = vi
      .fn<FetchFn>()
      .mockResolvedValueOnce(page([{ id: 1 }], 'c1'))
      .mockImplementationOnce(() => new Promise<CursorPage<Item>>((resolve) => { resolveMore = resolve }))
    const list = makeList(fetch)

    await list.reload()
    const more = list.loadMore()
    await list.loadMore()
    expect(fetch).toHaveBeenCalledTimes(2)

    resolveMore(page([{ id: 2 }], null))
    await more
    expect(list.loadingMore.value).toBe(false)
    expect(list.items.value).toEqual([{ id: 1 }, { id: 2 }])
  })
})

describe('useCursorList 错误处理', () => {
  it('首屏失败：写入错误文案，保持空列表', async () => {
    const fetch = vi.fn<FetchFn>().mockRejectedValueOnce(new ApiError(500, 'internal_error', '服务暂时不可用'))
    const list = makeList(fetch)

    await list.reload()
    expect(list.error.value).toBe('服务暂时不可用')
    expect(list.items.value).toEqual([])
    expect(list.loading.value).toBe(false)
  })

  it('加载更多失败：保留既有内容，允许重试', async () => {
    const fetch = vi
      .fn<FetchFn>()
      .mockResolvedValueOnce(page([{ id: 1 }], 'c1'))
      .mockRejectedValueOnce(new ApiError(0, 'network_error', '网络连接失败，请确认服务可用后重试'))
      .mockResolvedValueOnce(page([{ id: 2 }], null))
    const list = makeList(fetch)

    await list.reload()
    await list.loadMore()
    expect(list.items.value).toEqual([{ id: 1 }])
    expect(list.error.value).toBe('网络连接失败，请确认服务可用后重试')

    await list.loadMore()
    expect(list.items.value).toEqual([{ id: 1 }, { id: 2 }])
    expect(list.error.value).toBe('')
  })
})

describe('useCursorList 头部插入', () => {
  it('prepend 插入头部且不改变分页状态', async () => {
    const fetch = vi.fn<FetchFn>().mockResolvedValueOnce(page([{ id: 1 }], 'c1'))
    const list = makeList(fetch)

    await list.reload()
    list.prepend({ id: 0 })
    expect(list.items.value).toEqual([{ id: 0 }, { id: 1 }])
    expect(list.hasMore.value).toBe(true)

    await list.loadMore()
    expect(fetch).toHaveBeenLastCalledWith({ cursor: 'c1' })
  })
})
