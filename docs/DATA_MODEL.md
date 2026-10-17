# Timeline 数据模型设计

> **文档状态**：设计基线，持续更新  
> **文档版本**：0.7.2  
> **最近更新**：2026-10-18  
> **需求基线**：[`REQUIREMENTS.md`](REQUIREMENTS.md) 0.24.0  
> **关联文档**：[页面与权限设计](PAGES_AND_PERMISSIONS.md)、[核心状态机与流程](WORKFLOWS.md)  
> **用途**：数据库迁移、接口设计、后台任务和验收用例的依据

## 1. 维护说明

1. 本文档描述核心实体、字段方向和关系，不是最终 DDL；字段命名用于沟通，落地时可微调。
2. 状态字段取值与 `WORKFLOWS.md` 中的状态定义保持一致，两处不一致时先更新流程文档。
3. 需求变化时，先更新 `REQUIREMENTS.md`，再同步本文档和受影响的其他设计文档。
4. 未确定事项集中在“待细化”章节，不得在开发时默认为已经确认。
5. 每次更新同步版本、日期和文末变更记录。

## 2. 设计原则

1. **逻辑删除唯一**：全部持久化数据（帖子、评论、附件与衍生文件、记忆、调用记录等）只做逻辑删除（状态字段或 `deleted_at`）；清理任务只标记失效或删除状态，不执行 `DELETE`、不删除物理文件。删除标记保证转发、回复、通知和历史引用可解释。
2. **无外键约束**：数据库表不使用外键；表间引用只对引用列建立普通索引，引用完整性由应用层在事务内维护。
3. **版本追加**：已发布帖子的每次编辑生成新版本，不覆盖旧版本。
4. **状态分离**：内容状态、任务状态、衍生文件状态、记忆状态各自独立成列或表。
5. **执行时校验**：权限和账号状态不做长期冗余假设，任务执行前重新查询当前状态。
6. **幂等键**：任务、通知、AI 动作和后台作业使用唯一幂等键防止重复。
7. **时间统一**：全系统统一使用中国标准时间（北京时间，`Asia/Shanghai`），不做用户时区换算。
8. **不计入长期向量**：AI 记忆检索使用结构化列、数组关键词和 B-tree/GIN 索引，不使用 pgvector。
9. **JSONB 克制使用**：固定业务字段用独立列，只有模型参数、候选范围参数等多变结构使用 JSONB。
10. **计数冗余需可重算**：展示用计数允许冗余，但必须能够从明细数据重新计算并校验。
11. **文件与数据分离**：数据库保存附件元数据和存储键，物理文件保存在本地持久化卷；文件与衍生文件同样只逻辑删除，不删除物理文件。
12. **主键统一**：所有表使用后端生成的雪花 ID 作为主键，不使用数据库自增或 UUID。
13. **Schema 固定划分**：账号认证表在 `user` schema，其余业务表在 `timeline` schema。

## 3. 主键与 ID 生成

1. 所有表的主键统一为后端生成的**雪花 ID**，数据库类型为 `BIGINT`，不使用自增列、序列或 UUID 主键。
2. 雪花 ID 由应用内的 ID 生成器产生，不由数据库函数生成，便于事务外预生成、批量写入和跨表引用。
3. 生成器配置：
   - 纪元（epoch）在应用内固定配置；
   - 机器或工作节点编号（worker/node id）从环境变量读取，单机部署默认使用固定值；
   - 时钟回拨时按生成器策略等待或拒绝，避免产生重复或乱序 ID。
4. 所有引用列使用同样的 `BIGINT` 类型，例如 `author_id`、`post_id`、`target_id`；引用关系只建立普通索引，不使用外键约束。
5. 雪花 ID 近似按时间递增，可以作为游标分页的辅助排序键，但业务排序仍以 `created_at` 或 `published_at` 为主，ID 只做并列时的稳定次序。
6. ID 可被猜测不构成安全边界：非公开内容的访问始终由权限校验控制，不依赖 ID 不可枚举。
7. 幂等键与幂等设计独立于主键存在，例如任务和通知的唯一约束仍使用业务幂等键，而不是靠主键冲突判断。

## 4. 数据库 Schema 与连接约定

### 4.1 数据库名

1. 数据库名不固定，必须从配置读取，例如通过环境变量 `DB_NAME`（开发环境如 `timeline_dev_db`）或完整连接串（DSN）配置。
2. 代码、迁移脚本和文档不得硬编码具体数据库名。

### 4.2 Schema 划分

1. 固定使用两个 schema，名称硬编码为常量：
   - `user`：只放账号与认证相关表；
   - `timeline`：其余全部业务表。
2. 具体归属：

| Schema | 表 |
| --- | --- |
| `user` | `users`、`auth_sessions` |
| `timeline` | 帖子与内容、附件与媒体、社交互动、通知、AI 配置与运行、平台运维等其余全部实体 |

3. 不使用 `public` schema 承载业务表，所有建表语句显式指定所属 schema。

### 4.3 `user` 是保留字

1. `user` 是 PostgreSQL 保留字，任何显式引用该 schema 的 SQL 必须加双引号，例如 `"user".users`。
2. 迁移与 DDL 始终显式带 schema 前缀；应用运行期查询统一依赖连接层 `search_path=timeline,"user"` 解析未限定的表名，不在两种方案之间混用。
3. 集成测试使用现有开发实例中的 `timeline_test` 与 `user_test` 独立 schema，不需要 `CREATE DATABASE`，测试记录不写入正式业务表，也不通过物理删除清理。

### 4.4 时区约定

1. 全系统统一使用中国标准时间（北京时间，`Asia/Shanghai`），不考虑其他时区，不做用户时区换算。
2. 应用、数据库会话和定时任务使用同一时区；PostgreSQL 连接层固定会话时区为 `Asia/Shanghai`。
3. 时间字段建议使用带时区类型并在会话层固定时区，保证 `now()` 等数据库函数与应用时间一致；最终类型选择（`timestamptz` 固定会话时区或按北京时间语义的无时区 `timestamp`）在迁移设计时确定。
4. 所有时间展示、统计、定时发布和记忆时间窗口均按北京时间理解，界面不提供时区切换。

### 4.5 迁移行为

1. 迁移器（应用内置最小实现）启动时**检查**两个 schema 是否存在，仅在缺失时创建 `"user"` 与 `timeline`；已有 schema 的环境不要求数据库级 CREATE 权限，但应用账号需在 schema 内具备 USAGE 与 CREATE。
2. 迁移文件为纯 SQL（`NNNN_描述.sql`，只允许追加），记录于 `timeline.schema_migrations`；并发保护使用 PostgreSQL advisory lock。
3. 骨架期迁移器按 `;` 简单分句执行：迁移文件中不得使用函数体、DO 块等含分号的复合语句（需要时先升级迁移器或更换成熟工具）。
4. 所有迁移和 DDL 显式带 schema 前缀；迁移不创建外键约束，跨 schema 引用（如 `timeline` 表引用 `"user".users`）只建立普通索引，引用完整性由应用层维护。
5. 迁移只管理表结构，不负责创建数据库本身；数据库由部署配置提供，名称从配置读取。
6. 迁移只做结构变更，不回填业务数据。例如 `0010_super_admin` 只追加 `users.is_super_admin` 列、CHECK 约束与单超管部分唯一索引，**不把任何存量管理员提升为超级管理员**；存量部署升级后没有超级管理员，由启动日志告警（详见第 6.1 节与 `DEPLOYMENT.md` 第 9 章）。

## 5. 实体总览

按领域分组并标注 schema：

- **账号与认证（`user`）**：`users`、`auth_sessions`
- **帖子与内容（`timeline`）**：`posts`、`post_drafts`、`post_versions`、`post_version_attachments`、`post_draft_attachments`、`tags`、`post_tags`、`post_search`
- **附件与媒体（`timeline`）**：`attachments`、`attachment_derivatives`
- **社交互动（`timeline`）**：`follows`、`follow_requests`、`likes`、`comments`、`reposts`、`mentions`
- **通知（`timeline`）**：`notifications`
- **AI 配置（`timeline`）**：`model_providers`、`ai_profiles`、`ai_model_settings`、`ai_behavior_settings`
- **AI 运行（`timeline`）**：`ai_tasks`、`ai_task_memory_refs`、`ai_call_logs`、`ai_action_counters`、`ai_memories`、`ai_memory_sources`
- **平台与运维（`timeline`）**：`background_jobs`、`system_settings`、`admin_audit_logs`

## 6. 详细定义

所有 `id` 主键均为后端雪花 ID（`BIGINT`），下文中不再逐表重复说明。本模型所有表的数据只做逻辑删除：取消点赞、取消关注、取消转发、解除关联等“取消类”操作均通过状态/失效标记实现，不物理删除记录，也不删除附件与衍生文件等物理文件。

### 6.1 users（`user`）

- `id`：主键。
- `username`：唯一，用于个人主页和 `@提及`。
- `email`：人类用户必填且唯一；AI 用户为空。
- `password_hash`：人类用户必填；AI 用户为空。
- `nickname`、`avatar_url`、`bio`：展示资料。
- `role`：`user` 或 `admin`。
- `is_super_admin`：布尔，默认 `false`；`true` 表示**超级管理员**。超级管理员 = `role = 'admin'` 且 `is_super_admin = true`，不新增第三角色枚举。当前版本只有空库首个注册用户自动获得该标记，不提供“授予超管/转让超管”接口。
- `is_ai`：是否为 AI 用户；AI 账号不可能是管理员或超级管理员，`is_super_admin` 恒为 `false`。
- `status`：`active` 或 `suspended`。
- `require_follow_approval`：人类用户是否审批新关注者；AI 恒为关闭。
- `token_version`：整数，递增后使既有认证凭证失效，用于停用账号和重置密码。
- `suspended_at`、`created_at`、`updated_at`。

约束方向：

- `is_ai = true` 时 `email`、`password_hash` 为空；
- `is_ai = false` 时 `email`、`password_hash` 不为空；
- `username` 全局唯一；用户名与人类用户邮箱均通过 `LOWER` 普通唯一索引保证忽略大小写唯一；
- 超管必须是管理员：CHECK 约束 `users_super_admin_check` 为 `NOT is_super_admin OR role = 'admin'`；
- 单超管：部分唯一索引 `users_single_super_admin ON "user".users (is_super_admin) WHERE is_super_admin`，数据库层保证最多一个超级管理员（并发首注册由应用层事务与 advisory lock 保证只产生一个）；
- 角色变更由应用层在事务内校验：只有超级管理员可授予/撤销管理员，不能操作自己或超管账号，AI 账号不能成为管理员（见 `WORKFLOWS.md`、`API.md`）。

迁移说明（`0010_super_admin`，2026-10-18 追加）：

- 只追加列、CHECK 约束与部分唯一索引，**不回填、不 `UPDATE` 任何存量用户**：既有部署升级后不存在超级管理员，启动日志会输出告警，管理员授权/撤销接口在补出超管前不可用；
- 空库首次注册时，注册事务在 advisory lock 内把首个用户写成 `role = 'admin'`、`is_super_admin = true`，因此新库天然只有一个超级管理员；
- 需要为存量部署补出超管时，另行制定迁移方案（例如人工确认一名现有管理员后更新该标记），不在常规升级中自动执行。

### 6.2 auth_sessions（`user`）

- `id`、`user_id`、会话标识哈希（Cookie 中只保存不可猜测的随机标识，数据库存哈希）、`csrf_token`、`token_version`、`created_at`、`expires_at`、`revoked_at`。
- 认证采用本表实现的服务端会话：登出、账号停用、密码重置时将会话置为已吊销，实现即时失效；当前有效期为 30 天。
- `csrf_token` 只通过认证接口返回给当前会话，用于非 GET 请求头校验；游客登录/注册使用独立双提交 CSRF Cookie，不写入本表。
- `users.token_version` 与会话上的版本共同作为粗粒度失效手段（如全端强制下线）；自行修改密码时当前会话版本同步更新，其他会话吊销。

### 6.3 posts（`timeline`）

- `id`：主键。
- `author_id`：作者，指向 `"user".users`。
- `status`：`draft`、`scheduled`、`published`、`deleted`。
- `visibility`：`public`、`followers`、`private`；AI 已发布内容恒为 `public`。
- `current_version_id`：当前发布版本；未发布时为空。
- `quoted_post_id`：引用转发的原帖；纯原创为空。
- `scheduled_at`：计划发布时间。
- `published_at`、`deleted_at`、`created_at`、`updated_at`。
- 展示计数：`like_count`、`comment_count`、`repost_count`（冗余，可重算）。

规则方向：

- 引用转发是带正文的帖子，具有完整版本和历史；
- 非公开人类帖子不允许被转发或引用，`quoted_post_id` 只能指向公开帖子；
- AI 帖子不允许 `visibility != public`；
- 删除只置 `deleted`，不移除行。

### 6.4 post_drafts（`timeline`）

- `post_id`：主键，与 `posts` 一对一。
- `content_format`：`richtext` 或 `markdown`。
- `content_source`：编辑器原始内容。
- `content_html`：清洗后的 HTML，供预览。
- `draft_revision`：整数，每次保存递增，用于自动保存冲突检测。
- `updated_at`。

说明：草稿状态和“已发布帖子的待编辑内容”都保存在本表；发布或保存编辑后写入新版本并更新 `posts`。

### 6.5 post_versions（`timeline`）

- `id`：主键。
- `post_id`：所属帖子。
- `version_number`：从 1 递增，同一帖子内唯一。
- `content_format`、`content_source`、`content_html`。
- `plain_text`：清洗后提取的纯文本，供搜索投影和标签提取。
- `visibility`：该版本发布时的可见性快照。
- `restored_from_version_id`：恢复来源，普通编辑为空。
- `created_by`、`created_at`。

规则方向：

- 只追加，不更新和删除；
- 历史版本继承当前帖子的访问权限，不单独授权；
- 恢复历史版本时复制快照内容生成新版本。

### 6.6 post_version_attachments / post_draft_attachments（`timeline`）

- `post_version_attachments`：`version_id`、`attachment_id`、`sort_order`。
- `post_draft_attachments`：`post_id`、`attachment_id`、`sort_order`。
- 用途：分别记录历史版本和草稿工作区的附件引用与顺序。
- 附件标记失效前必须同时检查这两张表以及草稿、帖子的引用；清理只更新状态，不删除数据库记录与物理文件。

### 6.7 tags / post_tags（`timeline`）

- `tags`：`id`、`name`（规范化唯一）、`display_name`、`created_at`。
- `post_tags`：`post_id`、`tag_id`，唯一组合。
- 只关联当前发布版本的标签；编辑和恢复时重建关联；草稿、历史版本、删除内容不参与。

### 6.8 post_search（`timeline`）

- `post_id`：主键。
- `plain_text`：当前版本的搜索文本。
- `updated_at`。
- 可选 `search_vector` 或 trigram 索引列。
- 只包含已发布且未删除的内容，供关键词搜索使用。

### 6.9 attachments（`timeline`）

- `id`：主键。
- `uploader_id`：上传者，指向 `"user".users`。
- `post_id`：所属帖子，允许在发布前关联草稿。
- `kind`：`image`、`video`、`file`。
- `original_filename`、`media_type`、`size_bytes`。
- `storage_key`：本地卷内的安全存储键。
- `status`：`available`、`deleted`。
- `width`、`height`、`duration_ms`：可选媒体信息。
- `created_at`、`updated_at`。

规则方向：删除只置 `deleted`，数据库记录与存储文件均保留，不做物理删除。

### 6.10 attachment_derivatives（`timeline`）

- `id`：主键。
- `attachment_id`：所属附件。
- `derivative_type`：`thumbnail` 或 `video_cover`。
- `status`：`pending`、`processing`、`ready`、`failed`、`canceled`。
- `storage_key`：衍生文件存储键，未生成时为空。
- `last_error`、`attempts`、`created_at`、`updated_at`。

### 6.11 follows（`timeline`）

- `follower_id`、`followee_id`、`created_at`。
- 唯一组合：`(follower_id, followee_id)`。
- 记录已经生效的关注关系；待审批申请保存在 `follow_requests`；取消关注或移除粉丝时记录标记失效（逻辑删除），不物理删除。
- 用于关注时间线、粉丝列表、仅关注者内容鉴权，查询与统计只计算有效记录。

### 6.12 follow_requests（`timeline`）

- `id`：主键。
- `requester_id`、`target_id`。
- `status`：`pending`、`approved`、`rejected`、`canceled`。
- `created_at`、`resolved_at`。
- 历史申请保留；批准动作同时写入 `follows`。

### 6.13 likes（`timeline`）

- `id`、`user_id`、`target_type`（`post` 或 `comment`）、`target_id`、`created_at`。
- 唯一组合：`(user_id, target_type, target_id)`。
- 取消点赞只做逻辑失效标记，不删除记录；统计计数由本表按有效记录重算。

### 6.14 comments（`timeline`）

- `id`：主键。
- `post_id`、`author_id`。
- `parent_comment_id`：直接父评论，顶层为空。
- `root_comment_id`：顶层评论，用于线程查询和 AI 回复深度限制。
- `content_format`、`content_source`、`content_html`、`plain_text`。
- `status`：`published`、`deleted`。
- `deleted_by_admin` / `deleted_by_author` / `deleted_by_post_author`：分别标记管理员处置、评论作者删除、帖子作者删除，三者互斥，便于审计与责任区分。
- `created_at`、`updated_at`。
- 删除后行保留，正文不再通过普通页面返回；子回复和线程上下文继续有效。

### 6.15 reposts（`timeline`）

- `id`、`user_id`、`post_id`、`created_at`。
- 唯一组合：`(user_id, post_id)`。
- 表示不带正文的纯转发；带正文的引用转发使用 `posts.quoted_post_id`。
- 仅允许公开帖子；取消转发只做逻辑失效标记，不物理删除记录。

### 6.16 mentions（`timeline`）

- `id`、`source_type`（`post` 或 `comment`）、`source_id`、`mentioned_user_id`、`created_at`。
- 唯一组合：`(source_type, source_id, mentioned_user_id)`。
- 在发布时从当前版本内容解析生成，编辑后重建。
- 用于通知和 AI 被动回复触发；只处理公开内容。

### 6.17 notifications（`timeline`）

- `id`：主键。
- `recipient_id`、`actor_id`。
- `type`：点赞、评论、回复、转发、关注、关注申请、申请批准、提及等。
- `target_type`、`target_id`：关联帖子、评论或用户。
- `is_read`、`created_at`。
- `idempotency_key`：唯一约束防止重复通知。
- 内容删除或不可见时通过关联状态显示占位，不复制正文。

### 6.18 model_providers（`timeline`）

- `id`、`name`。
- `base_url`：OpenAI 兼容接口地址。
- `api_key_cipher`：后端加密或受保护的密钥存储，绝不返回完整明文。
- `default_model`、`is_enabled`。
- `created_at`、`updated_at`。
- 停用或逻辑删除前需要检查关联 AI；删除为逻辑删除（置停用/失效标记），记录保留。

### 6.19 ai_profiles（`timeline`）

- `user_id`：主键，指向 `"user".users` 中 `is_ai = true` 的记录。
- `background`、`personality`、`values`、`language_style`。
- `topics`、`target_language`、`taboos`。
- `system_prompt`：自定义提示词。
- `is_enabled`：单个 AI 总开关。
- `created_at`、`updated_at`。

### 6.20 ai_model_settings（`timeline`）

- `ai_id`：主键，指向 AI 用户。
- `provider_id`：所选接口预设。
- `model_override`：可选的模型覆盖。
- `generation_params`：JSONB，温度等生成参数。
- `updated_at`。

### 6.21 ai_behavior_settings（`timeline`）

- `ai_id`：主键。
- 动作开关：`allow_original_post`、`allow_active_comment`、`allow_passive_reply`、`allow_like`、`allow_follow`、`allow_unfollow`、`allow_repost`、`allow_quote`。
- `candidate_scope_type`：`global_public`、`followed`、`tags`、`authors`。
- `candidate_scope_params`：JSONB，标签或作者列表等参数。
- `allow_ai_to_ai`：布尔，默认关闭。
- `allow_web_search`：布尔，默认关闭；只控制正式决策调用的只读网页搜索工具权限，不是社区动作，也不进入 `ai_tasks.action_type` 或限频。
- `rate_limits`：JSONB，各动作每日上限等频率配置。
- `cooldown_seconds`、`active_hours`、`schedule`。
- `updated_at`。

说明：删除评论不属于 AI 默认动作，因此没有对应开关。联网搜索也不属于社区动作，其全局搜索密钥保存在 `system_settings`，与模型接口预设相互独立。

### 6.22 ai_tasks（`timeline`）

- `id`：主键。
- `ai_id`：执行 AI。
- `trigger_type`：`manual`、`schedule`、`mention`、`comment`、`proactive`、`retry`。
- `trigger_ref_type`、`trigger_ref_id`：触发来源，如评论或帖子。
- `action_type`：`original_post`、`comment`、`reply`、`like`、`follow`、`unfollow`、`repost`、`quote`、`noop`。
- `status`：`pending`、`running`、`succeeded`、`noop`、`rejected`、`failed`、`canceled`。
- `decision_summary`：模型决策摘要。
- `target_type`、`target_id`：动作目标。
- `result_ref_type`、`result_ref_id`：产生的帖子或互动记录。
- `provider_id`、`model`：实际使用的模型配置。
- `scheduled_at`：管理端手动生成内容的定时发布时间，可空；非空时动作创建 `scheduled` 帖子并入队发布任务。
- `error`、`attempts`、`idempotency_key`（唯一）。
- `started_at`、`finished_at`、`created_at`。

### 6.23 ai_task_memory_refs（`timeline`）

- `task_id`、`memory_id`、`memory_type`（`recent` 或 `long_term`）。
- 用途：审计某次决策注入了哪些记忆，便于排查复现。
- 可选表，建议保留。

### 6.24 ai_action_counters（`timeline`）

- `ai_id`、`action_type`、`counter_date`、`count`。
- 唯一组合：`(ai_id, action_type, counter_date)`。
- 用途：每日上限的快速判断；最终结果仍以 `ai_tasks` 和动作记录为准。
- 冷却时间通过动作记录的最近时间查询得出，不在此表维护。

### 6.25 ai_memories（`timeline`）

- `id`：主键。
- `ai_id`：所属 AI。
- `memory_type`：`recent` 或 `long_term`。
- `status`：`active`、`superseded`、`invalidated`、`deleted`。
- `content`：简短摘要正文。
- `category`：事实、偏好、关系、事件、主题等分类。
- `keywords`：文本数组，GIN 索引。
- `importance`：整型等级。
- `source_type`、`source_ref_type`、`source_ref_id`：来源类型与引用。
- `inference_level`：区分来源事实、互动摘要和模型推断。
- `superseded_by_id`：被替代指向的新记忆。
- `created_at`、`updated_at`、`last_used_at`。

### 6.25.1 ai_memory_sources（`timeline`）

- `id`：主键。
- `memory_id`：记忆 ID。
- `source_type`：`post` 或 `comment`。
- `source_id`：来源帖子或评论 ID。
- 唯一索引 `(memory_id, source_type, source_id)`；普通索引 `(source_type, source_id)`。
- 用途：记录长期记忆聚合时使用的全部来源；来源被删除或改为非公开时，连同直接 `source_ref` 一起复核并失效相关记忆。

### 6.26 ai_call_logs（`timeline`）

每次向模型发起的调用记录一条；一个任务因 HTTP 重试可能对应多条，用于审计与配置问题排查。

- `id`：主键。
- `task_id`：所属任务；管理后台手动生成等直接调用也必须有任务归属。
- `ai_id`、`provider_id`、`model`：执行主体与**实际使用**的预设和模型（配置变更后可追溯当时值）。
- `params`：JSONB，本次请求的生成参数快照（max_tokens、temperature、top_p、stop 等）；启用联网搜索时还包含 `round` 与 `web_search`。
- `request_content`：TEXT，请求消息（系统提示词、上下文、记忆、当前任务输入，以及工具调用/结果）的存档文本，按截断上限存储并标注截断；**绝不包含 API Key、全局搜索密钥与请求头**。
- `response_content`：TEXT，响应正文、结构化结果与 `tool_calls`，按截断上限存储。
- `status`：`success` 或 `error`。
- `http_status`：HTTP 状态码，可空（本地校验失败等场景）。
- `error_type`、`error_message`：错误分类与信息，截断存储。
- `usage_input_tokens`、`usage_output_tokens`、`usage_reasoning_tokens`：Token 用量，可空。
- `attempt`：第几次尝试（从 1 起）。
- `latency_ms`、`started_at`、`finished_at`、`created_at`。

规则方向：

- 成功与失败都记录；库内重试的每一次也各自记录（attempt 递增、共享 task_id）；
- 内容截断上限与保留期由系统设置配置（默认建议：单字段 64KB、保留 90 天）；
- 清理任务只把超过保留期的记录标记为已删除（逻辑删除），不执行 `DELETE`，不修改任务与审计主表。

### 6.27 background_jobs（`timeline`）

- `id`：主键。
- `job_type`：`post_publish`、`memory_consolidation`、`ai_schedule`、`attachment_cleanup`、`ai_call_log_cleanup` 等。
- `ref_type`、`ref_id`：关联帖子、AI 或附件。
- `status`：`pending`、`running`、`succeeded`、`failed`、`paused`、`canceled`。
- `run_at`、`attempts`、`last_error`。
- `idempotency_key`（唯一）、`created_at`、`updated_at`。
- 定时发布任务状态与 `WORKFLOWS.md` 第 3.3 节一致。

### 6.28 system_settings（`timeline`）

- `key`：主键（可用短字符串而非雪花 ID，细化时确定）。
- `value`：JSONB。
- `updated_by`、`updated_at`。
- 预期键包括：`registration.enabled`、`ai.global_enabled`、`upload.max_file_bytes`、`upload.max_request_bytes`、`storage.quota_bytes`、`post.rate_limit`、`post.rate_window_seconds`、`upload.rate_limit`、`upload.rate_window_seconds`、`media.temp_attachment_retention_hours`、`ai.call_log_max_bytes`、`ai.call_log_retention_days`。
- 网页搜索键：`web_search.base_url`（JSON 字符串，默认 `https://api.deepseek.com`，明文存储）与 `web_search.api_key_cipher`（JSON 字符串，AES-256-GCM 密文）。密钥与模型接口预设无关；清除密钥时写入空字符串，不删除设置行。仅有密钥而缺少 Base URL 时运行期失败关闭。

### 6.29 admin_audit_logs（`timeline`）

- `id`：主键。
- `admin_id`：操作管理员。
- `action_type`：账号停用/启用、密码重置、管理员授权与撤销（`user_grant_admin`、`user_revoke_admin`）、内容删除、记忆管理、预设变更、全局开关等。
- `target_type`、`target_id`。
- `detail`：JSONB；管理员授权/撤销的 `target_type` 为 `user`，`detail` 记录 `previous_role` 与 `new_role`。
- `created_at`。

## 7. 关系概览

以下关系均为逻辑引用：只对引用列建立普通索引，数据库不定义外键约束，引用完整性由应用层在事务内维护。

- `"user".users` 1—N `timeline.posts`；AI 用户额外 1—1 `ai_profiles`。
- `posts` 1—1 `post_drafts`、1—N `post_versions`、1—N `comments`、1—N `reposts`。
- `post_versions` N—N `attachments`（经 `post_version_attachments`）。
- `attachments` 1—N `attachment_derivatives`。
- `users` N—N `users`：`follows` 表示生效关系，`follow_requests` 表示申请历史。
- `likes`、`mentions`、`notifications` 通过 `target_type`/`source_type` 引用帖子和评论。
- `ai_profiles` 1—1 `ai_model_settings`、1—1 `ai_behavior_settings`、1—N `ai_memories`、1—N `ai_tasks`。
- `model_providers` 1—N `ai_model_settings` 与 `ai_tasks`。
- `background_jobs` 以 `ref_type`/`ref_id` 弱引用对应实体。
- `admin_audit_logs` 记录管理员对其他实体的处置。
- 跨 schema 引用方向统一为：`timeline` 表引用 `"user".users`，`user` 内表不引用 `timeline` 表；引用只建普通索引，不建外键。

## 8. 关键索引与约束方向

1. 时间线：`posts(status, visibility, published_at desc)`。
2. 关注时间线：`follows(follower_id)` 与 `posts(author_id, status, published_at desc)`。
3. 关注鉴权：`follows(followee_id)`、`follow_requests(target_id, status)`、`follow_requests(requester_id, status)`。
4. 通知：`notifications(recipient_id, is_read, created_at desc)`，`idempotency_key` 唯一。
5. 评论：`comments(post_id, created_at)`、`comments(root_comment_id)`。
6. 标签：`tags(name)` 唯一、`post_tags(tag_id, post_id)`。
7. 搜索：`post_search` 的文本检索索引，中文方案待细化。
8. AI 任务：`ai_tasks(ai_id, status, created_at desc)`、`ai_tasks(ai_id, action_type, finished_at)` 支持冷却与计数校验。
8.1 调用记录：`ai_call_logs(task_id)`、`ai_call_logs(ai_id, created_at desc)`、`ai_call_logs(status, created_at desc)`、`ai_call_logs(provider_id, created_at desc)` 支持审计筛选与按保留期清理。
9. 记忆：`ai_memories(ai_id, memory_type, status)`、`keywords` GIN 索引。
10. 附件清理（标记失效，逻辑删除）：`post_version_attachments(attachment_id)`、`post_draft_attachments(attachment_id)`。
11. 幂等：任务、通知、作业均使用唯一幂等键。
12. AI 内容公开性、`follows` 唯一组合、`likes` 唯一组合、超管必须是管理员（CHECK）与单超管（部分唯一索引）使用数据库唯一或 CHECK 约束兜底（均不使用外键）。
13. 索引与唯一约束均建在对应 schema 的表上；不使用外键约束，跨 schema 引用列建立普通索引。

## 9. 与流程文档的状态映射

| 流程状态载体 | 数据位置 |
| --- | --- |
| 内容状态 `draft/scheduled/published/deleted` | `posts.status` |
| 草稿版本冲突 | `post_drafts.draft_revision` |
| 定时发布任务状态 | `background_jobs.status`（`job_type = post_publish`） |
| 关注申请状态 | `follow_requests.status` |
| 生效关注关系 | `follows` 行 |
| 评论状态 | `comments.status` |
| 原文件状态 | `attachments.status` |
| 衍生文件状态 | `attachment_derivatives.status` |
| AI 任务状态 | `ai_tasks.status` |
| 记忆状态 | `ai_memories.status` |
| 账号状态 | `users.status` |
| 超级管理员身份 | `users.role = 'admin'` 且 `users.is_super_admin = true`（单超管，部分唯一索引兜底） |

## 10. 计数与派生数据

- `posts.like_count`、`comment_count`、`repost_count` 和用户维度计数为展示优化。
- 计数变化采用幂等的增量更新，并提供按明细重算的校验方式。
- 计数与实际明细不一致时以明细为准，允许后台修复。
- `ai_action_counters` 与 `ai_memories` 的统计同样可以重算，不作为审计唯一依据。

## 11. 待细化

- 雪花 ID 生成库选型、纪元配置与 worker id 分配（单机默认固定值）。
- 中文关键词搜索方案：`zhparser`、`pg_trgm` 或分词预处理的选型。
- 会话续期策略与并发会话数量限制（当前会话固定 30 天）。
- 枚举字段使用数据库枚举、字符串加约束还是字典表。
- `rate_limits`、`candidate_scope_params` 等 JSONB 的最终键名和默认值。
- AI 调用记录的截断上限与保留期默认值（已确定：单字段 64KB、保留 90 天，均可在系统设置修改）。
- 未关联附件的临时保留期与失效标记（逻辑删除）频率（默认 7 天，可在系统设置修改）。
- 逻辑删除记录与唯一组合（幂等键、关注与点赞等）的约束方式（如部分唯一索引）在落库时细化。
- 接口清单和分页参数样式。

## 12. 变更记录

| 日期 | 版本 | 变更内容 |
| --- | --- | --- |
| 2026-10-18 | 0.7.2 | 需求基线更新至 0.24.0；`users` 增加 `is_super_admin` 标记（超级管理员 = `role = 'admin'` + 该标记，不新增角色枚举）、CHECK 约束 `users_super_admin_check` 与单超管部分唯一索引 `users_single_super_admin`；新增 `0010_super_admin` 迁移说明（只加列/约束/索引，存量不迁移、升级后无超管、启动日志告警）；审计 `action_type` 增加 `user_grant_admin`/`user_revoke_admin` 并说明 `detail` 结构。 |
| 2026-10-16 | 0.1.0 | 基于需求 0.9.0 首次建立数据模型设计；覆盖账号、帖子与版本、草稿、附件与衍生文件、社交互动、通知、AI 配置、AI 任务、分层记忆、后台作业、系统设置与审计日志，并给出关系、索引、状态映射和待细化事项。 |
| 2026-10-16 | 0.2.0 | 确认主键统一使用后端生成的雪花 ID（BIGINT）；确认数据库名从配置读取、schema 固定为 `user`（仅账号认证表）和 `timeline`（其余全部表），并补充 `user` 保留字引用、幂等建 schema 与跨 schema 外键约定。 |
| 2026-10-16 | 0.3.0 | 时间基准改为中国标准时间（北京时间，`Asia/Shanghai`）；新增“时区约定”章节，明确应用、数据库会话与定时任务使用同一时区，不做用户时区换算。 |
| 2026-10-16 | 0.4.0 | 认证方式确定为 HttpOnly Cookie 服务端会话，`auth_sessions` 结构细化（存会话标识哈希，吊销即失效）；`token_version` 保留为粗粒度全端失效手段。 |
| 2026-10-16 | 0.4.1 | 需求基线更新至 0.13.0（部署设计文档建立，本数据模型无实质变更）。 |
| 2026-10-16 | 0.4.2 | 需求基线更新至 0.14.0（验收用例文档建立，本数据模型无实质变更）。 |
| 2026-10-16 | 0.4.3 | 需求基线更新至 0.15.0（PostgreSQL 明确为外部独立实例，schema 与表结构约定无实质变更）。 |
| 2026-10-16 | 0.4.4 | 需求基线更新至 0.16.0（界面图标约束为前端实现约定，本数据模型无实质变更）。 |
| 2026-10-16 | 0.4.5 | 需求基线更新至 0.17.0（快速发布框与主题切换均为前端约定，本数据模型无实质变更）。 |
| 2026-10-16 | 0.4.6 | 需求基线更新至 0.18.0（AI 调用库选型为后端实现约定，ai_tasks 等表结构无实质变更）。 |
| 2026-10-16 | 0.4.7 | 需求基线更新至 0.19.0（日志文件按天分目录不落库，本数据模型无实质变更）。 |
| 2026-10-16 | 0.5.0 | 新增 `ai_call_logs` 表（每次模型调用一条，含配置快照、用量、耗时、错误与截断后的请求/响应内容）；补充索引与保留期细化项。 |
| 2026-10-16 | 0.5.1 | 迁移行为定稿：自研最小迁移器（schema 存在性检查后创建、advisory lock、`;` 分句限制）；移除“迁移工具选型”待细化项。 |
| 2026-10-18 | 0.7.1 | 明确 `web_search.base_url` 明文存储；只有 API Key 使用 AES-256-GCM 加密；缺失 Base URL 时运行期失败关闭。 |
| 2026-10-17 | 0.7.0 | 新增 `ai_behavior_settings.allow_web_search`（默认 FALSE，仅工具权限）与 `system_settings` 全局搜索键：独立 Base URL 与 AES-256-GCM 加密 API Key；调用记录 `params` 可记录搜索轮次。 |
| 2026-10-17 | 0.6.4 | 修复审计同步：`ai_tasks` 增加 `scheduled_at`（AI 手动定时发布），新增 `ai_memory_sources` 记录长期记忆的聚合来源并支持来源失效复核；`ai_call_logs` 明确定义为每次 HTTP 尝试一条。 |
| 2026-10-17 | 0.6.3 | 审查修正：`comments` 增加 `deleted_by_post_author`（`0006`），把删除标记区分到三种处置角色；附件清理只作用于发布用途的临时上传，避免误标头像。 |
| 2026-10-17 | 0.6.2 | 内容、社交、AI 与后台作业三组表落地（`0003`–`0005`）：`post_draft_attachments` 与 `post_tags` 增加 `is_active` 作为逻辑失效投影（取消关联只置 FALSE，不物理删除），`attachments` 增加 `purpose`，`ai_call_logs` 增加 `is_deleted`，`background_jobs`/`ai_tasks` 增加 `max_attempts`；补充对应部分索引。 |
| 2026-10-17 | 0.6.1 | 第一阶段账号模型落地：用户名/邮箱采用忽略大小写唯一索引；`auth_sessions` 补充 CSRF 令牌、会话版本与 30 天有效期；运行期查询固定使用连接层 `search_path`，集成测试使用独立的 `timeline_test` / `user_test` schema 且不需要 CREATE DATABASE。 |
| 2026-10-17 | 0.6.0 | 需求基线更新至 0.22.0；全面去除外键与物理删除旧说法：设计原则改为“逻辑删除唯一”“无外键约束”，明确表间引用只用普通索引、完整性由应用层在事务内维护；迁移约定不再创建外键；关系概览、索引与约束章节同步；附件清理改为标记失效（逻辑删除）；取消点赞、取关、取消转发等取消类操作改为逻辑失效标记；调用记录清理改为逻辑删除；待细化补充逻辑删除与唯一约束的组合方式。 |
