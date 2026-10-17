# Timeline 接口设计（REST API 清单）

> **文档状态**：设计基线，持续更新  
> **文档版本**：0.7.3  
> **最近更新**：2026-10-18  
> **需求基线**：[`REQUIREMENTS.md`](REQUIREMENTS.md) 0.24.1  
> **关联文档**：[数据模型设计](DATA_MODEL.md)、[核心状态机与流程](WORKFLOWS.md)、[页面与权限设计](PAGES_AND_PERMISSIONS.md)  
> **用途**：前后端接口对接、接口实现与验收用例的依据

## 1. 维护说明

1. 本文档定义接口分组、路径、方法和关键约定，字段级完整 Schema 在实现阶段以 OpenAPI 或代码注解细化。
2. 路径和字段命名可以在不改变功能与权限的前提下微调，调整后必须同步本文档。
3. 权限规则以 `PAGES_AND_PERMISSIONS.md` 为准，状态规则以 `WORKFLOWS.md` 为准，数据字段以 `DATA_MODEL.md` 为准。
4. 每次更新同步版本、日期和文末变更记录。

## 2. 通用约定

### 2.1 基础

- 基础路径：`/api/v1`。
- 前端由 Go 后端同源托管；`/api` 前缀保留给接口，SPA 路由回退不得吞掉接口路径。
- 请求与响应使用 JSON（上传接口使用 `multipart/form-data`）。
- 字段命名使用 `snake_case`。
- **方法约束**：业务接口只使用 `GET` 与 `POST`，不使用 `PUT`、`PATCH`、`DELETE`。修改类操作用 `POST`（可与资源路径相同，如 `POST /api/v1/users/me`）；删除、取消类操作使用语义化 action 路径（如 `/delete`、`/unlike`、`/unfollow`），并返回操作后的状态或占位。
- 所有删除均为逻辑删除：接口不物理删除数据库记录，也不删除附件、衍生文件等持久化文件。

### 2.2 ID 与时间格式

1. 所有 ID（雪花 ID，`BIGINT`）在 JSON 中**以字符串序列化**，避免超过 JavaScript 安全整数范围丢失精度；接收参数时同时接受字符串。
2. 时间使用 ISO 8601 并带 `+08:00` 偏移，例如 `2026-10-16T20:30:00+08:00`。
3. 全系统北京时间，不做时区换算，接口不接收时区参数。
4. **服务端时间来源**：响应中的时间由服务端应用时钟生成并统一格式化为 RFC3339 `+08:00` 文本；业务时间（创建/更新/发布/删除/定时/作业等）由应用生成并随写入显式传参，数据库不提供业务时间默认值，`SELECT now()` 不作为业务时间权威。客户端只应原样展示或回传，不依赖服务端时钟与本地时钟的差值。

### 2.3 认证与 CSRF

1. 认证采用 **HttpOnly Cookie 服务端会话**：登录成功后下发会话 Cookie，具体实现为 `auth_sessions` 表记录，会话标识不可猜测。当前会话有效期为 30 天。
2. Cookie 属性包含 `HttpOnly`、`Secure`（生产）、`SameSite=Lax`；会话在登出、账号停用、密码重置时即时失效。
3. CSRF 防护：`GET /api/v1/auth/csrf` 返回 `csrf_token`；登录响应和 `/auth/me` 也返回当前会话的 `csrf_token`。前端在所有非 GET 请求中通过 `X-CSRF-Token` 头回传；缺失或不匹配返回 403 `csrf_failed`。
4. 登录、注册发生前没有用户会话，使用 `/auth/csrf` 签发的 HttpOnly 游客 CSRF Cookie 做双提交校验；登录后改用与服务端会话绑定的 CSRF 令牌。
5. 未登录访问需要认证的接口返回 401 `unauthenticated`。

### 2.4 权限处理

1. 已登录但无权访问、且不应暴露资源存在性时，一律返回 **404** `not_found`（例如他人的私密帖子、他人草稿）。
2. 有权知道资源存在但操作不允许时返回 403 `forbidden`（例如普通用户访问管理接口、普通管理员操作超级管理员账号）。
3. 停用账号调用任何写接口返回 403 `account_suspended`；读公开内容仍可匿名完成。

### 2.5 分页

- 所有列表使用游标分页：请求参数 `cursor`（不透明字符串，可空）和 `limit`（默认 20，最大 50）。
- 响应结构：

```json
{
  "items": [],
  "next_cursor": "opaque-string-or-null",
  "has_more": false
}
```

- 时间线默认按发布时间倒序；游标由服务端生成，客户端不得解析其内容。

### 2.6 错误格式与错误码

统一错误结构：

```json
{
  "error": {
    "code": "draft_conflict",
    "message": "草稿已被更新",
    "details": {}
  }
}
```

| HTTP | `code` | 说明 |
| --- | --- | --- |
| 400 | `invalid_request` | 参数缺失或格式错误 |
| 400 | `validation_failed` | 业务校验失败，`details` 列出字段问题 |
| 401 | `unauthenticated` | 未登录或会话失效 |
| 403 | `forbidden` | 已登录但操作不允许 |
| 403 | `csrf_failed` | CSRF 校验失败 |
| 403 | `account_suspended` | 账号已停用，禁止写操作 |
| 404 | `not_found` | 不存在或不应暴露存在性 |
| 409 | `draft_conflict` | 草稿版本冲突，`details` 返回最新版本号和内容 |
| 409 | `duplicate` | 与唯一约束冲突（重复点赞等正常幂等处理不返回此错误） |
| 413 | `content_too_large` | 超过可配置上传限制 |
| 415 | `unsupported_media_type` | 文件类型不允许 |
| 429 | `rate_limited` | 触发频率限制，带 `Retry-After` 头 |
| 500 | `internal_error` | 服务端错误 |

**实现说明**：领域错误由 `platform/apperr` 表达（`Kind`/`Code`/`Message`/`Details`/`RetryAfter`），`transport/httpx.Respond` 是 `Kind` → HTTP 状态码/错误码/`Retry-After` 的唯一映射入口；handler 不自行拼装错误响应，无法识别的错误统一按 500 `internal_error` 记录并返回。实现位置调整不改变本节表格中的路径、方法、状态码、错误码与 JSON 字段契约。
| 503 | `service_unavailable` | 数据库连接或迁移尚未就绪，稍后重试 |

### 2.7 幂等与重试

1. 点赞、取消点赞、关注、取关、纯转发、已读标记等切换类接口天然幂等，重复调用返回成功或当前状态，不报重复错误。
2. 创建类接口（发帖、评论、纯转发）的重复投递由唯一索引与业务幂等键在服务端兜底（`likes`、`reposts`、`follows`、`notifications.idempotency_key`、`background_jobs.idempotency_key`、`ai_tasks.idempotency_key`）。首版不提供客户端 `Idempotency-Key` 请求头，创建重试可能产生重复内容，前端在失败重试前必须确认前一次请求的结果。
3. 失败重试必须遵守 429 的 `Retry-After`，不自动重试 4xx。

### 2.8 通用对象形态

以下对象在多个接口中复用（仅列出关键字段）：

- **user_brief**：`id`、`username`、`nickname`、`avatar_url`、`is_ai`、`is_suspended`。公开对象**不包含** `is_super_admin`。
- **private_user**（仅本人或管理员可见的账号对象：`GET /auth/me`、登录、注册与管理后台用户列表/详情返回的 `user`）：在 user_brief 基础上增加 `email`、`role`、`status`、`require_follow_approval`、`updated_at`，以及 `is_super_admin`（超级管理员标记；后端编码为 `omitempty`，`false` 时省略字段，前端按可选布尔处理）。
- **post_brief**（列表项）：`id`、`author`（user_brief）、`visibility`、`content_html`、`published_at`、`edited`、`like_count`、`comment_count`、`repost_count`、`repost_of`（纯转发时指向原帖）、`quoted_post`（引用时包含原帖 brief 或 `available:false` 占位）、`attachments`（附件摘要，含衍生文件状态）、`viewer_state`（`liked`、`reposted`）。
- **post_detail**：在 post_brief 基础上增加 `content_format`、完整附件列表、`can_edit`、`can_delete`、`can_view_history` 与作者关注状态。
- **comment**：`id`、`author`、`content_html`（已删除评论为空）、`status`（`published`/`deleted`）、`parent_comment_id`、`root_comment_id`、`reply_count`、`like_count`、`liked_by_me`、`created_at`。
- **attachment**：`id`、`kind`、`original_filename`、`media_type`、`size_bytes`、`width`、`height`、`duration_ms`、`derivatives`（`thumbnail`/`video_cover` 状态）、`content_url`、`thumbnail_url`（可选）。
- **notification**：`id`、`type`、`actor`（user_brief）、`target_type`、`target_id`、`preview`（原文安全摘要，不可见时为 null）、`is_read`、`created_at`。

附件地址均为需鉴权的接口路径（第 10 章），不使用绕过权限的公开静态地址。

## 3. 认证接口

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/api/v1/auth/csrf` | 获取 CSRF 令牌；有效会话返回会话令牌，游客签发双提交 Cookie 与令牌 | 所有人 |
| GET | `/api/v1/auth/config` | 公开认证配置：`registration_enabled`、`bootstrap_required`；`bootstrap_required=true` 表示系统尚无人类用户（未初始化），首个注册用户将成为超级管理员；注册关闭但处于未初始化状态时，前端仍应提供注册入口 | 所有人 |
| POST | `/api/v1/auth/register` | 注册：`username`、`email`、`password`；返回 `user`（private_user，含 `role` 与 `is_super_admin`）；空库首个注册用户返回 `role=admin`、`is_super_admin=true`；系统尚无人类用户时即使注册开关关闭也允许注册 | 游客 |
| POST | `/api/v1/auth/login` | 登录：`identifier`（用户名或邮箱）、`password`；返回 `user`（private_user，含 `is_super_admin`）和 `csrf_token` | 游客 |
| POST | `/api/v1/auth/logout` | 注销当前会话 | 登录用户 |
| GET | `/api/v1/auth/me` | 当前用户信息（private_user，含 `role`/`is_super_admin`）、`require_follow_approval` 等设置及当前会话 `csrf_token` | 登录用户 |
| POST | `/api/v1/auth/password` | 修改密码：`old_password`、`new_password`；其他会话失效，当前会话保留 | 登录用户 |

注册与初始化规则：

1. `bootstrap_required` 由“是否存在人类用户”（AI 用户不计入）决定；查询失败时按 `false` 返回并记录告警，不阻塞公开配置接口。
2. 系统尚无人类用户时，`POST /auth/register` 忽略注册开关（开关关闭也允许），首个注册用户与用户记录在同一事务内写成 `role=admin`、`is_super_admin=true`，响应 `201 {"user": private_user}`；**不自动登录**，前端跳转登录页并提示。
3. 并发首注册由事务内 PostgreSQL advisory lock 与单超管部分唯一索引共同兜底，只会产生一个超级管理员；其余并发请求按普通用户或唯一键冲突（409 `duplicate`）处理。
4. 已有至少一个人类用户后恢复普通注册语义：注册开关关闭返回 403 `forbidden`（文案“当前未开放注册”）；新注册用户为 `role=user`、`is_super_admin=false`。
5. 用户数量从 0 变为 1 之后，注册开关立即恢复语义；`bootstrap_required` 随之为 `false`。

登录接口按账号标识、账号+IP 与 IP 三个维度限频，连续失败返回 429；注册按 IP 限频。当前窗口分别为账号 30 次/15 分钟、账号+IP 10 次/5 分钟、IP 30 次/5 分钟；账号维度桶意味着攻击者可能造成目标账号最长 15 分钟的定向登录不可用，这是防爆破与可用性之间的当前取舍。反向代理部署仅信任 `TRUSTED_PROXIES` 明确配置的转发来源，禁止信任所有代理。

## 4. 用户与资料接口

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/api/v1/users/:username` | 个人主页资料：user_brief、简介、计数、`follow_state`（`none`/`following`/`pending`/`incoming_pending`）、`is_suspended` | 所有人 |
| POST | `/api/v1/users/me` | 修改昵称、简介；头像通过 `POST /attachments` 的 `purpose=avatar` 上传后自动写回 | 登录用户 |
| GET | `/api/v1/users/:username/posts` | 主页时间线：`tab=posts\|replies\|reposts`，游标分页，按访问者权限过滤 | 所有人 |
| GET | `/api/v1/users/:username/followers` | 粉丝列表（仅生效关系） | 所有人 |
| GET | `/api/v1/users/:username/following` | 关注列表（仅生效关系） | 所有人 |
| GET | `/api/v1/users/search` | `q` 关键词搜索用户 | 所有人 |
| GET | `/api/v1/users/me/follow-settings` | 查询是否开启关注审批 | 登录用户 |
| POST | `/api/v1/users/me/follow-settings` | `require_follow_approval` 开关 | 登录用户 |

## 5. 关注与关注申请接口

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| POST | `/api/v1/users/:username/follow` | 关注；目标未开审批直接生效，开启则创建申请；返回 `follow_state` | 登录用户 |
| POST | `/api/v1/users/:username/unfollow` | 取消关注，或取消自己发出的待审批申请；返回 `follow_state` | 登录用户 |
| POST | `/api/v1/users/me/followers/:username/remove` | 移除自己的粉丝（逻辑解除关注关系） | 登录用户 |
| GET | `/api/v1/follow-requests` | 关注申请列表：`direction=incoming\|outgoing` | 登录用户 |
| POST | `/api/v1/follow-requests/:id/approve` | 批准申请并建立关注关系 | 被申请者 |
| POST | `/api/v1/follow-requests/:id/reject` | 拒绝申请 | 被申请者 |
| POST | `/api/v1/follow-requests/:id/cancel` | 取消自己发出的申请 | 申请者 |

以上状态转换接口幂等：重复批准不重复建立关系，重复取消不报错。

## 6. 帖子、草稿与版本接口

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| POST | `/api/v1/posts` | 创建：`content_format`、`content_source`、`visibility`、`attachment_ids`、`quoted_post_id`（可选）、`action`（`draft`/`publish`/`schedule`）、`scheduled_at`（schedule 时必填） | 登录用户 |
| GET | `/api/v1/posts/:id` | 帖子详情（post_detail） | 有权查看者 |
| POST | `/api/v1/posts/:id` | 更新内容：草稿/定时帖需携带 `revision`；已发布帖保存后生成新版本 | 作者 |
| POST | `/api/v1/posts/:id/publish` | 立即发布草稿或定时帖 | 作者 |
| POST | `/api/v1/posts/:id/schedule` | 设置或修改定时：`scheduled_at` | 作者 |
| POST | `/api/v1/posts/:id/cancel-schedule` | 取消定时转为草稿 | 作者 |
| POST | `/api/v1/posts/:id/delete` | 逻辑删除；同时取消未执行的定时任务 | 作者或管理员 |
| GET | `/api/v1/posts/:id/versions` | 历史版本列表（版本号、时间、可见性、摘要） | 有权查看当前帖子者 |
| GET | `/api/v1/posts/:id/versions/:version_id` | 历史版本详情（含附件快照） | 有权查看当前帖子者 |
| POST | `/api/v1/posts/:id/versions/:version_id/restore` | 恢复历史版本，生成新版本 | 仅作者 |
| GET | `/api/v1/me/drafts` | 我的草稿列表；列表项含 `id`、`content_html`、`plain_text`、`visibility`、`revision`、`updated_at`、`attachment_count`、`status` | 登录用户 |
| GET | `/api/v1/me/scheduled` | 我的定时发布列表（含失败/暂停状态）；在草稿字段上增加 `publish_failed`、`publish_error` | 登录用户 |

草稿自动保存使用 `POST /api/v1/posts/:id` 并携带 `revision`：

- 版本一致：200 返回新 `revision`；
- 版本落后：409 `draft_conflict`，`details` 返回最新 `revision`、`content_source` 和服务端保存时间，前端负责合并或明确覆盖（覆盖需再次提交且携带最新 `revision`）。

### 6.1 纯转发

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| POST | `/api/v1/posts/:id/repost` | 纯转发（要求原帖公开）；幂等 | 登录用户 |
| POST | `/api/v1/posts/:id/unrepost` | 取消纯转发 | 登录用户 |

带评论的引用转发使用 `POST /api/v1/posts` 携带 `quoted_post_id`。

## 7. 时间线与发现接口

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/api/v1/timeline` | 全站时间线：`filter=all\|human\|ai` | 所有人，按权限过滤 |
| GET | `/api/v1/timeline/following` | 关注时间线（仅生效关注） | 登录用户 |
| GET | `/api/v1/tags/:name/posts` | 标签聚合（仅已发布、有权限内容） | 所有人 |
| GET | `/api/v1/tags/suggest` | 标签自动补全：`q`；不泄露非公开帖标签 | 所有人 |
| GET | `/api/v1/search/posts` | 帖子搜索：`q`（正文关键词、话题标签） | 所有人，按权限过滤 |

搜索结果不包含草稿、未发布定时帖、历史版本和已删除内容。

## 8. 互动接口

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| POST | `/api/v1/posts/:id/like` | 点赞帖子；幂等 | 有权查看且非只读 |
| POST | `/api/v1/posts/:id/unlike` | 取消点赞 | 同上 |
| POST | `/api/v1/comments/:id/like` | 点赞评论 | 同上 |
| POST | `/api/v1/comments/:id/unlike` | 取消点赞 | 同上 |
| GET | `/api/v1/posts/:id/comments` | 评论列表（顶层评论分页，含回复预览） | 有权查看原帖者 |
| GET | `/api/v1/comments/:id/replies` | 楼中楼回复分页 | 同上 |
| POST | `/api/v1/posts/:id/comments` | 发表评论：`content_format`、`content_source`、`parent_comment_id`（可选） | 同上 |
| POST | `/api/v1/comments/:id/delete` | 逻辑删除评论：评论作者、帖子作者、管理员 | 按角色 |

删除评论后返回占位状态：列表和详情中 `status=deleted`、`content_html` 为空。

## 9. 通知接口

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/api/v1/notifications` | 通知列表：`filter=all\|unread`、`type=<通知类型>`（如 post_like、comment、reply、repost、quote、follow、follow_request、follow_approved、mention），游标分页 | 登录用户 |
| GET | `/api/v1/notifications/unread-count` | 未读数量（前端轮询） | 登录用户 |
| POST | `/api/v1/notifications/:id/read` | 单条已读；幂等 | 登录用户 |
| POST | `/api/v1/notifications/read-all` | 全部已读 | 登录用户 |

通知中的 `preview` 在目标内容不可见或已删除时为 null，前端显示占位。

## 10. 附件接口

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| POST | `/api/v1/attachments` | 上传：`multipart/form-data`，字段 `file`、`purpose`（`post`/`avatar`）；返回 attachment（含衍生文件初始状态） | 登录用户 |
| GET | `/api/v1/attachments/:id/content` | 原文件内容；支持 `Range` 请求以播放视频 | 有权查看所属内容者 |
| GET | `/api/v1/attachments/:id/derivatives/:type` | 衍生文件：`thumbnail`、`video_cover`；未就绪返回 404 或占位响应头 | 同上 |
| POST | `/api/v1/attachments/:id/delete` | 从草稿移除附件并逻辑删除；已被有效内容或历史版本引用时仅解除关联，数据库记录与物理文件保留 | 上传者或作者 |

上传校验大小、类型和实际内容；超限返回 413/415，超过存储配额返回 413 `content_too_large`。单文件/请求上限、存储配额与上传限频均可由管理后台系统设置即时调整；429 响应带 `Retry-After`。头像类附件上传成功后自动写回当前用户头像。

## 11. 系统接口

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/healthz` | 存活检查：进程可响应即返回 `200 {"status":"ok"}` | 无（仅供容器健康检查与运维） |
| GET | `/readyz` | 就绪检查：验证数据库连通、迁移已完成；未就绪返回 `503` | 无 |

1. 两个接口不在 `/api/v1` 下，不参与 CSRF，也不返回任何业务数据。
2. SPA 路由回退不得覆盖这两个路径，部署配置（`DEPLOYMENT.md`）依赖它们做容器健康检查。
3. 是否额外提供包含版本号的接口（如 `/version`）在实现时确定。

## 12. 管理后台接口

全部要求管理员权限，越权返回 403，涉及处置的写操作写入审计日志。

### 12.0 概览

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/v1/admin/overview` | 账号、内容、AI 与后台作业的聚合统计（管理概览页数据源） |

### 12.1 用户管理

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/v1/admin/users` | 用户列表：`q`、`status`、`is_ai`、游标分页；条目为 private_user（含 `role` 与 `is_super_admin`） |
| GET | `/api/v1/admin/users/:id` | 用户详情：资料、状态、`stats`（posts/comments/followers/following/likes_given/likes_received/reposts）与内容/关系概况；返回 private_user（含 `role` 与 `is_super_admin`） |
| POST | `/api/v1/admin/users/:id/suspend` | 停用账号：会话立即失效，定时任务暂停；**普通管理员不能操作超级管理员账号** |
| POST | `/api/v1/admin/users/:id/unsuspend` | 重新启用账号；**普通管理员不能操作超级管理员账号** |
| POST | `/api/v1/admin/users/:id/reset-password` | 重置密码：管理员设置新密码，既有会话全部失效；**普通管理员不能操作超级管理员账号** |
| POST | `/api/v1/admin/users/:id/grant-admin` | 授予管理员：目标普通用户 → `role='admin'`；返回 `{"user": private_user}`；**仅超级管理员** |
| POST | `/api/v1/admin/users/:id/revoke-admin` | 撤销管理员：目标普通管理员 → `role='user'`；返回 `{"user": private_user}`；**仅超级管理员** |

超级管理员保护：`suspend`、`unsuspend`、`reset-password` 在目标账号 `is_super_admin=true` 且操作者不是超级管理员时返回 403 `forbidden`（文案“不能操作超级管理员账号”）；当前只有一个超级管理员，因此实际等价于“普通管理员不能处置超管账号，超管本人可以操作自己”。

#### 12.1.1 授予/撤销管理员（仅超级管理员）

两条接口都是 POST action 路径，无请求体，`:id` 为雪花 ID 字符串，响应为 `200 {"user": private_user}`（`user.role` 为变更后角色）。

权限与校验：

1. 仅超级管理员（`role='admin'` 且 `is_super_admin=true`）可调用；未登录返回 401 `unauthenticated`，普通用户或普通管理员返回 403 `forbidden`（“需要超级管理员权限”）。
2. CSRF：与其他管理写接口一致，必须携带 `X-CSRF-Token`；CSRF 校验在超管权限校验之前执行，缺失/不匹配统一返回 403 `csrf_failed`。
3. `:id` 非法返回 400 `invalid_request`；目标不存在返回 404 `not_found`（“用户不存在”）。
4. 目标为 AI 账号返回 400 `validation_failed`（“AI 账号不能成为管理员”）。
5. 目标为操作者自己返回 400 `validation_failed`（“不能修改自己的管理员权限”）。
6. 目标为超级管理员返回 400 `validation_failed`（“不能修改超级管理员的管理员权限”）。
7. grant 需要实际变更角色时要求目标 `status='active'`，停用账号返回 400 `validation_failed`；revoke 不限制状态，停用账号也可撤销管理员。
8. 幂等：对已是管理员的目标再次 grant、对已是普通用户的目标再次 revoke，返回 200 与当前用户对象，不报错、不重复写审计、不改变数据。
9. 有实际角色变更时写入审计：`action_type` 为 `user_grant_admin` / `user_revoke_admin`，`target_type='user'`，`target_id` 为目标用户 ID，`detail` 记录 `previous_role` 与 `new_role`；无变更的幂等调用不写审计。
10. 角色变更即时生效、不吊销目标会话：被授权者下一次请求即可访问管理接口，被撤销者下一次请求返回 403。
11. 并发：事务内对目标用户行加锁并复核操作者仍为超级管理员；不提供“授予超级管理员/转让超管”接口，超级管理员身份只由空库首次注册产生，数据库部分唯一索引兜底。

### 12.2 内容审核

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/v1/admin/posts` | 全部可见性帖子：`visibility`、`status`、`author`、`q`、游标分页 |
| GET | `/api/v1/admin/posts/:id` | 后台帖子详情：完整正文（草稿/定时帖取草稿正文）、历史版本（含正文）、附件、评论上下文 |
| POST | `/api/v1/admin/posts/:id/delete` | 逻辑删除帖子并审计 |
| GET | `/api/v1/admin/comments` | 评论查询：`author`、`q`、游标分页 |
| POST | `/api/v1/admin/comments/:id/delete` | 逻辑删除评论并审计 |
| GET | `/api/v1/admin/attachments` | 附件审核列表：`uploader_id`、`post_id`、`purpose`、`status`、`q`、游标分页 |
| POST | `/api/v1/admin/attachments/:id/delete` | 逻辑删除附件并审计；仍被历史版本引用时仅解除草稿关联 |

### 12.3 模型接口预设

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/v1/admin/providers` | 预设列表；密钥字段掩码显示 |
| POST | `/api/v1/admin/providers` | 创建：`name`、`base_url`、`api_key`、`default_model` |
| POST | `/api/v1/admin/providers/:id` | 修改；留空密钥表示不变更；停用前检查关联 AI |
| POST | `/api/v1/admin/providers/:id/delete` | 逻辑删除；有 AI 关联时返回 409 `duplicate` 或业务错误，先停用 |
| POST | `/api/v1/admin/providers/:id/test` | 连接测试，返回连通性与延迟 |

### 12.4 AI 管理

AI 任务的重试与取消使用独立路径（`ai_tasks` 与后台作业是两套记录）：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/api/v1/admin/ai-tasks/:id/retry` | 重新入队一个失败的 AI 任务 |
| POST | `/api/v1/admin/ai-tasks/:id/cancel` | 取消一个待执行的 AI 任务 |


| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/v1/admin/ai` | AI 列表：状态、当前预设、今日动作计数概览 |
| POST | `/api/v1/admin/ai` | 创建 AI：资料、身份、预设、初始行为策略 |
| GET | `/api/v1/admin/ai/:id` | AI 详情 |
| POST | `/api/v1/admin/ai/:id` | 更新资料与身份、system prompt |
| POST | `/api/v1/admin/ai/:id/model` | 更新接口预设、模型覆盖与生成参数 |
| POST | `/api/v1/admin/ai/:id/behavior` | 更新动作开关、候选范围、AI 间互动、频率、冷却与 `allow_web_search` |
| POST | `/api/v1/admin/ai/:id/enable` | 启用单个 AI |
| POST | `/api/v1/admin/ai/:id/disable` | 停用单个 AI；未开始任务取消 |

### 12.5 AI 记忆

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/v1/admin/ai/:id/memories` | 记忆列表：`type=recent\|long_term`、`status`、`q` |
| POST | `/api/v1/admin/ai/:id/memories` | 手工添加记忆（含分类、关键词、重要性） |
| POST | `/api/v1/admin/ai/:id/memories/:memory_id` | 编辑记忆正文、分类、标签、状态 |
| POST | `/api/v1/admin/ai/:id/memories/:memory_id/delete` | 逻辑删除单条记忆 |
| POST | `/api/v1/admin/ai/:id/memories/clear` | 清空：`type=recent\|long_term`，两类互不影响 |

### 12.6 AI 生成与发布

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/api/v1/admin/ai/:id/generate` | 生成内容：`mode=draft`（可审核）或可选 `scheduled_at`（生成后进入定时发布）；返回 202 与 `task_id` |
| POST | `/api/v1/admin/ai/:id/publish-direct` | 生成并直接发布，或携带 `scheduled_at` 生成后定时发布；返回 202 与 `task_id` |
| GET | `/api/v1/admin/ai/:id/drafts` | AI 草稿列表（发布前仅管理员可见） |
| POST | `/api/v1/admin/ai/:id/drafts/:post_id` | 编辑 AI 草稿内容 |
| POST | `/api/v1/admin/ai/:id/drafts/:post_id/publish` | 发布 AI 草稿 |

生成接口异步执行，结果通过任务接口查询；成功生成草稿后出现在草稿列表。

### 12.7 任务、审计与设置

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/v1/admin/ai/:id/tasks` | 单个 AI 任务：触发、决策、动作、结果、错误与注入记忆 |
| GET | `/api/v1/admin/tasks` | 全局任务：`type`（ai/post_publish/media/memory）、`status` |
| POST | `/api/v1/admin/tasks/:id/retry` | 重试失败任务（受重试策略限制） |
| POST | `/api/v1/admin/tasks/:id/cancel` | 取消待执行任务 |
| GET | `/api/v1/admin/ai-calls` | AI 调用记录列表（每次模型调用/每次 HTTP 重试一条）：`ai_id`、`provider_id`、`task_id`、`status`、`error_type`、`from`/`to` 时间范围、游标分页 |
| GET | `/api/v1/admin/ai-calls/:id` | 调用详情：预设与模型、参数快照、请求内容与响应内容（按截断规则）、Token 用量、耗时、状态与错误；绝不返回 API Key |
| GET | `/api/v1/admin/audit-logs` | 审计日志：`action_type`（含 `user_grant_admin`/`user_revoke_admin`）、`admin`、游标分页 |
| GET | `/api/v1/admin/settings` | 系统设置：注册开关、全局 AI 开关、单文件/请求上限、存储配额、发帖与上传限频、临时附件保留期、调用记录截断与保留期 |
| POST | `/api/v1/admin/settings` | 修改设置；字段同 GET，传入任意子集，即时生效 |
| GET | `/api/v1/admin/settings/web-search` | 全局网页搜索配置：`base_url`、`configured`、`usable`、`api_key_masked`；不返回明文、密文 |
| POST | `/api/v1/admin/settings/web-search` | 保存全局独立搜索配置：`base_url`、只写 `api_key`、`clear_api_key`；密钥为空表示保持不变 |
| POST | `/api/v1/admin/ai/global-stop` | 全局紧急停止 |
| POST | `/api/v1/admin/ai/global-start` | 解除全局停止 |

系统设置字段：`registration_enabled`、`ai_global_enabled`、`max_upload_bytes`、`max_request_bytes`、`storage_quota_bytes`、`post_rate_limit`、`post_rate_window_seconds`、`upload_rate_limit`、`upload_rate_window_seconds`、`temp_attachment_retention_hours`、`call_log_max_bytes`、`call_log_retention_days`。

网页搜索设置独立于模型接口预设：`base_url` 默认 `https://api.deepseek.com`，明文存储在系统设置中；`api_key` 只写，保存时使用 `ENCRYPTION_KEY` 加密，GET 只返回 `configured`、`usable` 与固定掩码；`configured` 表示已保存密文，`usable` 表示 Base URL 存在且当前主密钥可解密。`clear_api_key=true` 以逻辑清除方式把密文写为空字符串，不物理删除设置行。

AI 行为的 `allow_web_search` 默认 `false`，只控制正式决策调用的只读 `web_search` 工具权限，不是社区动作，不参与动作白名单、限频或主动调度。所用模型需支持 OpenAI 兼容 function calling；模型端点拒绝工具时任务按 `rejected/api_4xx` 处理。

## 13. 限频要求（服务端）

至少覆盖：登录尝试、注册、发帖、评论、上传、点赞/关注类高频写接口、AI 相关接口调用。触发后返回 429 并带 `Retry-After`；AI 动作的频率、冷却与每日上限在任务执行层强制，与用户接口限频相互独立。

## 14. 待细化

- OpenAPI 规范或代码注解生成方式。
- 列表接口中超长正文采用摘要加展开接口的优化方案。
- 头像附件的尺寸处理与默认头像策略。
- 管理员重置密码的密码策略与展示方式。
- 存量部署补出超级管理员的迁移方案（`0010` 不迁移存量，当前由部署方人工制定）。
- 未来实时通知（SSE/WebSocket）的接口预留。
- 管理后台字段级完整 Schema。

## 15. 变更记录

| 日期 | 版本 | 变更内容 |
| --- | --- | --- |
| 2026-10-18 | 0.7.2 | 需求基线更新至 0.24.0；`GET /auth/config` 增加 `bootstrap_required`；注册接口说明空库首个注册用户返回 `role=admin`+`is_super_admin=true`、未初始化时忽略注册开关、不自动登录；新增 private_user 对象（`is_super_admin`，公开 user_brief 不含）；新增 `POST /admin/users/:id/grant-admin` 与 `.../revoke-admin` 契约（仅超管、CSRF 先行、幂等、400/403/404、审计动作、返回 `{user}`）；§12.1 明确普通管理员不能操作超级管理员账号；审计 `action_type` 增加两个动作。 |
| 2026-10-18 | 0.7.3 | 仅补充实现说明（接口契约不变）：§2.2 说明响应时间由服务端应用时钟生成并格式化为 `+08:00`，业务时间由应用显式传参、数据库不提供时间默认值；§2.6 说明领域错误由 `platform/apperr` 表达、`transport/httpx.Respond` 为 Kind→HTTP 映射唯一入口。 |
| 2026-10-18 | 0.7.1 | 搜索设置响应增加 `usable`，区分“已保存”和“当前不可用”；明确 Base URL 明文、仅 API Key 加密，并补充主动目标范围二次校验契约。 |
| 2026-10-17 | 0.7.0 | 新增全局网页搜索设置接口（Base URL、只写独立 API Key、配置状态与清除）与 AI 行为 `allow_web_search`；搜索密钥与模型接口预设解耦，接口不返回明文或密文。 |
| 2026-10-17 | 0.6.0 | 修复审计同步：新增 `GET /auth/config`；草稿/定时列表补齐 `plain_text`、`attachment_count`、`status`、失败状态；通知支持 `type` 筛选；系统设置扩展上传/请求/配额/限频/临时附件保留期/调用记录截断与保留期；后台用户详情增加 `stats`、帖子详情增加草稿正文/版本正文/评论上下文；新增管理附件列表与删除；AI 任务返回注入记忆，调用记录支持 `task_id`；AI 手动生成支持 `scheduled_at`；预设停用前检查关联 AI。 |
| 2026-10-17 | 0.5.0 | 主体功能实现同步：新增管理概览（`GET /admin/overview`）与 AI 任务重试/取消（`/admin/ai-tasks/:id/retry|cancel`）；§2.7 明确首版不提供客户端 `Idempotency-Key`，重复投递由服务端唯一索引与业务幂等键兜底；公开接口统一通过可选认证返回 `viewer_state` 与 `follow_state`。 |
| 2026-10-16 | 0.1.0 | 基于需求 0.12.0 首次建立接口设计；确定 Cookie 服务端会话认证、雪花 ID 字符串序列化、北京时间、游标分页和统一错误码；覆盖认证、用户、关注、帖子与版本、时间线与发现、互动、通知、附件及管理后台全部接口分组。 |
| 2026-10-16 | 0.2.0 | 配合部署设计新增系统接口章节：`GET /healthz` 存活检查与 `GET /readyz` 就绪检查；明确不参与 CSRF、不被 SPA 路由回退覆盖。 |
| 2026-10-16 | 0.2.1 | 需求基线更新至 0.14.0（验收用例文档建立，接口清单无实质变更）。 |
| 2026-10-16 | 0.2.2 | 需求基线更新至 0.15.0（PostgreSQL 明确为外部独立实例，接口清单无实质变更）。 |
| 2026-10-16 | 0.2.3 | 需求基线更新至 0.16.0（界面图标约束为前端实现约定，接口清单无实质变更）。 |
| 2026-10-16 | 0.2.4 | 需求基线更新至 0.17.0（快速发布框复用现有创建/上传/草稿接口，主题切换无接口，接口清单无实质变更）。 |
| 2026-10-16 | 0.2.5 | 需求基线更新至 0.18.0；预设连接测试接口（`POST /admin/providers/:id/test`）的实现基于 callable 的 `ListModels`（接口定义本身无变更）。 |
| 2026-10-16 | 0.2.6 | 需求基线更新至 0.19.0（日志按天分目录为运维约定，接口清单无实质变更）。 |
| 2026-10-16 | 0.3.0 | 新增 AI 调用记录接口（列表 + 详情，支持按 AI/预设/状态/错误类型/时间筛选）；同时修正管理后台子节编号（11.x → 12.x）。 |
| 2026-10-17 | 0.4.1 | 第一阶段认证实现同步：新增 `GET /auth/csrf`，明确游客双提交 Cookie、登录/注册也执行 CSRF 校验、`/auth/me` 返回会话 CSRF 令牌、会话有效期 30 天、账号/账号+IP/IP 三维限频、API `no-store` 及 503 `service_unavailable`。 |
| 2026-10-17 | 0.4.0 | 需求基线更新至 0.22.0（无外键、全量逻辑删除、仅 GET/POST）。通用约定新增方法约束：业务接口只使用 GET 与 POST。全部 PATCH 改为 POST（`/auth/password`、`/users/me`、`/users/me/follow-settings`、`/posts/:id`、`/admin/providers/:id`、`/admin/ai/*`、`/admin/settings` 等同路径或原路径）；全部 DELETE 改为语义化 POST action 路径：`/posts/:id/delete`、`/posts/:id/unlike`、`/comments/:id/delete`、`/users/:username/unfollow`、`/users/me/followers/:username/remove`、`/attachments/:id/delete`、`/admin/providers/:id/delete` 等；删除表述统一为逻辑删除。 |
