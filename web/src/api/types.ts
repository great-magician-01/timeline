// 接口对象类型（对齐 docs/API.md 与后端契约）
// 约定：雪花 ID 在 JSON 中始终是字符串，前端任何位置都不得转换为 Number。

/** 公共用户字段（列表项、个人主页等复用） */
export interface PublicUser {
  id: string
  username: string
  nickname: string
  avatar_url: string | null
  bio: string
  is_ai: boolean
  is_suspended: boolean
  created_at: string
}

/** 当前用户与管理员视角的用户对象：额外包含账号与设置字段 */
export interface CurrentUser extends PublicUser {
  email: string
  role: 'user' | 'admin'
  /** 超级管理员标记（后端只在私有用户对象中返回，false 时省略）；超管 = role='admin' + 该标记 */
  is_super_admin?: boolean
  status: 'active' | 'suspended'
  require_follow_approval: boolean
  updated_at: string
}

/** 游标分页响应（docs/API.md §2.5） */
export interface CursorPage<T> {
  items: T[]
  next_cursor: string | null
  has_more: boolean
}

/** 管理端用户列表筛选参数 */
export interface UserListParams {
  q?: string
  status?: 'active' | 'suspended'
  is_ai?: boolean
  cursor?: string
  limit?: number
}

// ---------- 帖子与内容 ----------

export type Visibility = 'public' | 'followers' | 'private'
export type ContentFormat = 'richtext' | 'markdown'
export type PostKind = 'post' | 'repost'

export interface UserBrief {
  id: string
  username: string
  nickname: string
  avatar_url: string | null
  is_ai: boolean
  is_suspended: boolean
}

export interface ViewerState {
  liked: boolean
  reposted: boolean
}

export interface QuotedPost {
  available: boolean
  post?: PostBrief
}

/** 附件摘要（含衍生文件状态） */
export interface Attachment {
  id: string
  kind: 'image' | 'video' | 'file'
  original_filename: string
  media_type: string
  size_bytes: number
  width: number | null
  height: number | null
  duration_ms: number | null
  content_url: string
  thumbnail_url?: string
  derivatives: Record<string, string>
  /** 衍生文件处理中/失败时保留原文件并展示状态（docs/PAGES_AND_PERMISSIONS.md E-04） */
  status: 'available' | 'processing' | 'failed' | 'deleted'
  created_at: string
}

export interface PostBrief {
  id: string
  kind: PostKind
  author: UserBrief
  visibility: Visibility
  content_html: string
  published_at: string | null
  reposted_at?: string
  edited: boolean
  like_count: number
  comment_count: number
  repost_count: number
  /** 非空表示帖子被作者置顶（个人主页顶部展示） */
  pinned_at?: string
  /** 当前访问者可置顶/取消置顶（作者本人且已发布） */
  can_pin?: boolean
  repost_of?: PostBrief
  quoted_post?: QuotedPost
  attachments: Attachment[]
  viewer_state: ViewerState
}

export type PostStatus = 'draft' | 'scheduled' | 'published' | 'deleted'
export type FollowState = 'none' | 'following' | 'pending' | 'incoming_pending'

export interface PostDetail extends PostBrief {
  status: PostStatus
  content_format: ContentFormat
  scheduled_at?: string
  revision?: number
  version_number?: number
  can_edit: boolean
  can_delete: boolean
  can_view_history: boolean
  author_follow_state?: FollowState
}

export interface PostVersionSummary {
  id: string
  version_number: number
  visibility: Visibility
  created_at: string
  restored_from_version_id?: string
  summary: string
}

export interface PostVersion {
  id: string
  version_number: number
  content_format: ContentFormat
  content_source: string
  content_html: string
  visibility: Visibility
  created_at: string
  created_by: string
  restored_from_version_id?: string
  attachments: Attachment[]
}

/** 列表展示状态：failed/paused 来自最新定时发布作业（后端 drafts.publishStateFor） */
export type DraftStatus = 'draft' | 'scheduled' | 'published' | 'failed' | 'paused'

/** 草稿与定时发布列表项（GET /me/drafts 与 GET /me/scheduled 共用） */
export interface DraftSummary {
  id: string
  content_html: string
  plain_text: string
  visibility: Visibility
  revision: number
  updated_at: string
  scheduled_at?: string
  attachment_count: number
  status: DraftStatus
  /** 定时发布任务失败时由后端置为 true，并在 publish_error 给出原因 */
  publish_failed?: boolean
  publish_error?: string
}

export interface TimelineCommentItem {
  kind: 'comment'
  id: string
  author: UserBrief
  content_html: string
  created_at: string
  post: PostBrief
}

export interface TagSuggestion {
  name: string
  display_name: string
  post_count: number
}

/** 帖子创建/更新请求体（action 只在创建时使用） */
export interface PostInput {
  content_format: ContentFormat
  content_source: string
  visibility: Visibility
  attachment_ids: string[]
  quoted_post_id?: string
  action?: 'draft' | 'publish' | 'schedule'
  scheduled_at?: string
  revision?: number
}

// ---------- 评论与互动 ----------

export interface Comment {
  id: string
  post_id: string
  author: UserBrief
  content_html: string
  status: 'published' | 'deleted'
  parent_comment_id?: string
  root_comment_id?: string
  reply_count: number
  like_count: number
  liked_by_me: boolean
  can_delete: boolean
  created_at: string
  reply_preview?: Comment[]
}

export interface LikeState {
  liked: boolean
  like_count: number
}

export interface RepostState {
  reposted: boolean
  repost_count: number
  /** 本次新创建的纯转发记录（仅 reposted=true 时返回）；ID 保持字符串防止精度丢失 */
  repost?: { id: string; created_at: string }
}

/** 关注申请 */
export interface FollowRequest {
  id: string
  status: 'pending' | 'approved' | 'rejected' | 'canceled'
  requester: UserBrief
  target: UserBrief
  created_at: string
  resolved_at?: string
}

/** 通知 */
export type NotificationType =
  | 'post_like'
  | 'comment_like'
  | 'comment'
  | 'reply'
  | 'repost'
  | 'quote'
  | 'follow'
  | 'follow_request'
  | 'follow_approved'
  | 'mention'

export interface Notification {
  id: string
  type: NotificationType
  actor: UserBrief
  target_type: 'post' | 'comment' | 'user'
  target_id: string
  preview: string | null
  is_read: boolean
  created_at: string
}

// ---------- 私信 ----------

/** 私信接收策略（后端 users.dm_policy） */
export type DMPolicy = 'all' | 'following' | 'mutual' | 'none'

/** 私信会话（GET /dm/conversations、POST /dm/conversations） */
export interface DMConversation {
  id: string
  peer: UserBrief
  /** 空会话为 null（不会出现在列表接口，仅详情接口可能出现） */
  last_message: { id: string; preview: string; created_at: string } | null
  unread_count: number
  created_at: string
  updated_at: string
}

/** 私信消息（GET /dm/conversations/:id/messages） */
export interface DMMessage {
  id: string
  conversation_id: string
  sender: UserBrief
  /** 已撤回消息为空字符串 */
  content_html: string
  status: 'published' | 'recalled'
  read_at: string | null
  /** 发送者是否仍在撤回时间窗内 */
  can_recall: boolean
  created_at: string
}

// ---------- 管理后台 ----------

export interface AdminPost {
  id: string
  author: UserBrief
  status: PostStatus
  visibility: Visibility
  content_html: string
  published_at: string | null
  created_at: string
  like_count: number
  comment_count: number
  repost_count: number
  attachment_count: number
}

export interface AdminComment {
  id: string
  post_id: string
  author: UserBrief
  content_html: string
  status: 'published' | 'deleted'
  created_at: string
}

/** 管理端附件（GET /admin/attachments） */
export interface AdminAttachment {
  id: string
  uploader: UserBrief | null
  post_id: string | null
  kind: 'image' | 'video' | 'file'
  purpose: 'post' | 'avatar' | string
  original_filename: string
  media_type: string
  size_bytes: number
  status: string
  created_at: string
}

/** 管理端用户的内容与关系统计（GET /admin/users/:id → stats） */
export interface AdminUserStats {
  posts: number
  comments: number
  followers: number
  following: number
  likes_given: number
  likes_received: number
  reposts: number
}

/** 模型接口预设 */
export interface ModelProvider {
  id: string
  name: string
  base_url: string
  default_model: string
  is_enabled: boolean
  api_key_masked: string
  ai_count: number
  created_at: string
  updated_at: string
}

export interface ProviderTestResult {
  ok: boolean
  latency_ms: number
  models?: string[]
  error?: string
}

/** AI 用户 */
export interface AIProfile {
  user: UserBrief
  is_enabled: boolean
  provider_id: string | null
  provider_name: string
  model: string
  today_actions: number
  created_at: string
}

export interface GenerationParams {
  max_tokens?: number
  temperature?: number
  top_p?: number
  stop?: string[]
}

export interface AIDetail {
  user: UserBrief
  /** 展示用简介，由后端 aiDetailResponse 在顶层返回（internal/ai/profiles.go） */
  bio: string
  profile: {
    background: string
    personality: string
    core_values: string
    language_style: string
    topics: string
    target_language: string
    taboos: string
    system_prompt: string
    is_enabled: boolean
  }
  model: {
    provider_id: string | null
    model_override: string
    generation_params: GenerationParams
    request_timeout_seconds: number
  }
  behavior: AIBehavior
  recent_tasks: AITask[]
  memory_counts: { recent: number; long_term: number }
}

export interface AIBehavior {
  allow_original_post: boolean
  allow_active_comment: boolean
  allow_passive_reply: boolean
  allow_like: boolean
  allow_follow: boolean
  allow_unfollow: boolean
  allow_repost: boolean
  allow_quote: boolean
  candidate_scope_type: 'none' | 'global_public' | 'followed' | 'tags' | 'authors'
  candidate_scope_params: { tags?: string[]; usernames?: string[] }
  allow_ai_to_ai: boolean
  allow_web_search: boolean
  rate_limits: Record<string, number>
  cooldown_seconds: number
  active_hours_start: number
  active_hours_end: number
  schedule: Record<string, unknown>
  max_reply_depth: number
}

export interface AIMemory {
  id: string
  memory_type: 'recent' | 'long_term'
  status: 'active' | 'superseded' | 'invalidated' | 'deleted'
  content: string
  category: 'fact' | 'preference' | 'relationship' | 'event' | 'topic'
  keywords: string[]
  importance: number
  source_type: string
  source_ref_type: string
  source_ref_id: string | null
  inference_level: 'fact' | 'summary' | 'inference'
  superseded_by_id: string | null
  created_at: string
  updated_at: string
  last_used_at: string | null
}

export type AITaskStatus =
  | 'pending'
  | 'running'
  | 'succeeded'
  | 'noop'
  | 'rejected'
  | 'failed'
  | 'canceled'

/** 任务执行时注入的上下文记忆（GET /admin/ai/:id/tasks） */
export interface AITaskMemoryRef {
  id: string
  type: string
  content: string
  category: string
}

export interface AITask {
  id: string
  ai: UserBrief
  trigger_type: 'manual' | 'schedule' | 'mention' | 'comment' | 'proactive' | 'retry'
  action_type: string
  status: AITaskStatus
  decision_summary: string
  target_type: string | null
  target_id: string | null
  result_ref_type: string | null
  result_ref_id: string | null
  provider_id: string | null
  model: string
  error: string
  attempts: number
  started_at: string | null
  finished_at: string | null
  created_at: string
  memories?: AITaskMemoryRef[]
}

export interface BackgroundJob {
  id: string
  job_type: string
  ref_type: string
  ref_id: string | null
  status: 'pending' | 'running' | 'succeeded' | 'failed' | 'paused' | 'canceled'
  run_at: string
  attempts: number
  max_attempts: number
  last_error: string
  created_at: string
  updated_at: string
}

export interface AICallLog {
  id: string
  task_id: string | null
  ai: UserBrief | null
  provider_id: string | null
  provider_name: string
  model: string
  params: Record<string, unknown>
  status: 'success' | 'error'
  http_status: number | null
  error_type: string
  error_message: string
  usage_input_tokens: number | null
  usage_output_tokens: number | null
  usage_reasoning_tokens: number | null
  attempt: number
  latency_ms: number
  truncated: boolean
  started_at: string
  finished_at: string
}

export interface AICallLogDetail extends AICallLog {
  request_content: string
  response_content: string
}
