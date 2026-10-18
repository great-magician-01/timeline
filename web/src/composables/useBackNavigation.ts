// 返回上一页：优先回退浏览器历史，让 router.scrollBehavior 恢复离开时的滚动位置；
// 直接打开链接或新标签页没有站内上一页时，回到兜底路由。
import { useRouter } from 'vue-router'

export function useBackNavigation(fallbackPath = '/'): { goBack: () => void } {
  const router = useRouter()

  function goBack(): void {
    // vue-router 在 history.state.back 记录站内上一页路径；直接进入站点时为 null
    const back = router.options.history.state.back
    if (typeof back === 'string' && back.length > 0) {
      router.back()
      return
    }
    void router.push(fallbackPath)
  }

  return { goBack }
}
