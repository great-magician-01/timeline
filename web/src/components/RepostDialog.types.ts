// 转发弹窗完成结果（RepostDialog.vue 与 PostCard/PostDetailView 共享的事件载荷）
import type { PostBrief, RepostState } from '../api/types'

/**
 * 判别联合：
 * - pure：留空为纯转发，返回最新转发状态与计数；
 * - unrepost：取消纯转发；
 * - quote：填写内容后发布引用转发，post 为新建的帖子。
 */
export type RepostDialogResult =
  | { kind: 'pure'; result: RepostState }
  | { kind: 'unrepost'; result: RepostState }
  | { kind: 'quote'; post: PostBrief }
