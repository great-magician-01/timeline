# Timeline 后端目标架构

> **文档状态**：设计基线，持续更新  
> **文档版本**：0.4.2  
> **最近更新**：2026-10-21  
> **需求基线**：[`REQUIREMENTS.md`](REQUIREMENTS.md) 0.27.0  
> **关联文档**：[`DATA_MODEL.md`](DATA_MODEL.md)、[`WORKFLOWS.md`](WORKFLOWS.md)、[`API.md`](API.md)、[`DEPLOYMENT.md`](DEPLOYMENT.md)  
> **用途**：后端分层、依赖边界、数据所有权与架构守卫的权威说明；改造过程细节见仓库根目录 `CODE_QUALITY_REFACTOR_PLAN.md`（过程文档，非长期权威）

## 1. 目标与原则

目标形态是**模块化单体（modular monolith）**：领域边界清晰、依赖方向可静态强制、HTTP/业务/SQL/事务/时间各自归位，同时保持现有 `/api/v1` 契约与权限行为不变。不拆微服务，不引入消息队列/事件总线。

九条原则：

1. **按限界上下文划分模块**，模块内四层：`domain`（纯规则）→ `app`（用例/事务）→ `repo`（SQL）与 `http`（Gin）。
2. **依赖方向单向**：`cmd → app（组合根）→ server/router/modules → transport/platform/shared`；`transport` 不依赖 `cmd`，`modules` 不依赖 `transport`，`shared` 不依赖任何上层。
3. **编译期强制边界**：模块四层是独立 Go 包；另有 `internal/architecture` 静态守卫（import/SQL/时间函数位置、事务原语、遗留符号与 `cmd/server` import 白名单），越界在 CI 失败。
4. **HTTP 只属于 transport**：业务核心（domain/app）不出现 `gin`、`net/http`、`pgx`、`pool.Begin`、`c.JSON`；Gin 只出现在 `*/http` 与 `transport`。
5. **SQL 只属于 repo**：所有 SQL 字符串、Scan、分页查询、`FOR UPDATE`、计数更新只出现在 `*/repo`；跨模块只读的 admin read-model 是显式例外。
6. **事务由 app 编排**：`platform/db.TxManager.WithTx` 是唯一事务入口；跨模块协作复用外层事务；一个事务只取一次 `clock.Now()`。
7. **时间是应用输入**：写库时间、日界、租约、退避截止时间全部由应用生成并作为参数传入；数据库不提供业务时间默认值、不在 SQL 里做时间运算（`schema_migrations.applied_at` 是唯一白名单）。
8. **跨模块只走端口**：消费者在 `app/port.go` 定义窄接口，生产者 app 类型结构化实现，组合根装配；跨模块命令/查询结构体放 `shared/contracts`；禁止跨模块直接查表。
9. **错误领域化、响应统一化**：`platform/apperr` 表达领域错误；`transport/httpx.Respond` 是唯一 HTTP 映射入口；DTO 在 `*/http/view.go`，不把 JSON 形状带进领域。

## 2. 目录结构

```
cmd/server/main.go                  # 进程生命周期：TZ、配置、日志、信号、启动/优雅关闭、migrate 子命令
internal/
├── app/                            # 组合根：唯一允许 import 全部模块的地方
│   ├── app.go / wire.go            # App、依赖装配与迁移后初始化状态日志（Bootstrap）
│   ├── readers.go                  # 跨模块只读查询适配（概览/统计）
│   ├── adapters.go                 # 平台端口适配器（jobs/audit 等）
│   └── jobs.go                     # 作业处理器与周期任务注册
├── platform/                       # 技术基座（无业务语义；任何层可依赖）
│   ├── clock/ db/ id/ logging/     # 时钟（唯一 time.Now 来源）、连接池/事务/就绪状态、雪花 ID、日志
│   ├── migrate/                    # 迁移器 v2（支持 no-transaction 指令）
│   ├── storage/ ratelimit/         # 本地附件卷、单实例固定窗口限频
│   ├── apperr/                     # 领域错误 Kind/Error/构造器
│   ├── jobs/ audit/ settings/      # 分别拥有 background_jobs / admin_audit_logs / system_settings
│   ├── websearch/ aiclient/        # 受控联网搜索、OpenAI 兼容模型客户端（出站适配器）
│   └── config/
├── shared/                         # 共享内核（无 IO）
│   ├── model/ access/ contracts/   # 跨上下文数据结构、纯授权策略、跨模块发布语言
│   ├── page/ richtext/             # 游标分页、富文本/Markdown 清洗与链接化
│   └── format/ view/               # 雪花 ID/北京时间/附件 URL 纯格式化、跨模块响应 DTO
├── transport/
│   ├── httpx/                      # JSON 错误/解码/ID/时间/分页/上下文读取
│   ├── middleware/                 # Ready、Recovery、Security、AccessLog、RequestID
│   └── router/                     # 唯一注册路由的地方：Policy + 声明式路由表
├── server/                         # Gin 引擎、健康检查、前端静态托管（不含业务路由）
├── architecture/                   # 静态架构守卫测试（go/parser 扫描）
├── testkit/                        # 集成测试脚手架：隔离 schema + 组合根服务 + Cookie/CSRF 客户端
└── modules/
    ├── auth/     # 账号、会话、角色/超管、注册引导、CSRF
    ├── content/  # 帖子、草稿、版本、发布、时间线、标签、搜索
    ├── social/   # 关注/申请、点赞、评论、转发
    ├── notice/   # 通知、提及
    ├── dm/       # 1:1 私信会话、消息、已读、撤回（管理后台不可见）
    ├── media/    # 附件、衍生文件、清理
    ├── ai/       # 预设、AI 身份/行为、记忆、运行时
    ├── agent/    # 调度、决策、动作、调用记录、AI 管理接口
    └── admin/    # 审核/概览/任务/审计查询（编排型上下文，无自有实体）
```

各层只做/绝不做：

| 位置 | 只做 | 绝不做 |
| --- | --- | --- |
| `platform/*` | 通用技术能力（时钟、DB、事务、ID、日志、存储、限流、配置、迁移、外部客户端） | 业务规则、HTTP 路由 |
| `shared/*` | 共享类型、纯策略、contracts、游标、富文本纯函数 | SQL、IO、Gin、依赖 modules |
| `modules/*/domain` | 实体不变式、状态机、纯校验与策略 | SQL、IO、Gin、依赖其它模块 |
| `modules/*/app` | 用例编排、事务边界、时钟取时、端口调用、审计/通知/入队协作 | SQL、Gin、`net/http`、`pgx` |
| `modules/*/repo` | 唯一 SQL 层：查询、Scan、`FOR UPDATE`、批量载入 | 业务规则、HTTP、取时钟（`now` 由 app 传入） |
| `modules/*/http` | Gin handler、参数解析、错误映射、DTO 组装、本域中间件 | SQL、`pgx`、`pool.Begin` |
| `transport/router` | 路由表、策略组、路径/方法注册 | 业务逻辑、SQL |
| `server` | Gin 引擎、全局中间件、健康检查、前端托管 | 业务路由、服务构造 |
| `app` | 构造全部依赖、装配端口、注册作业 | 业务规则、SQL |

## 3. 依赖方向与允许 import

```
cmd/server ──▶ internal/app
                 ├─▶ internal/server ──▶ transport/middleware
                 ├─▶ internal/transport/router ──▶ modules/*/http ──▶ modules/*/app ──▶ modules/*/repo
                 ├─▶ modules/*/app ──▶ modules/*/repo
                 ├─▶ platform/*
                 └─▶ shared/*
```

- `server` 不 import `modules/*` 与 `transport/router`；`router` 不 import `modules/*` 的 app/repo/domain（只依赖各模块 `http` 的 Handler）。
- `shared`、`platform` 不 import `modules/*`、`transport/*`、`server`。
- 模块之间互不 import；跨模块协作只能通过 `app/port.go` 端口 + `shared/contracts` 类型。
- `cmd/server` 只 import `internal/app`、`internal/platform/{config,db,id,logging,migrate,storage}`、`internal/server` 与标准库/第三方（含 `time/tzdata`）；不 import `modules/*`、`transport/router` 或其它平台业务包（组合根负责其余装配），该白名单由架构守卫规则 9 断言。

## 4. 数据所有权

| 所有者 | 表 |
| --- | --- |
| `modules/auth` | `"user".users`、`"user".auth_sessions` |
| `modules/content` | `posts`、`post_drafts`、`post_versions`、`post_version_attachments`、`post_draft_attachments`、`tags`、`post_tags`、`post_search` |
| `modules/social` | `follows`、`follow_requests`、`likes`、`comments`、`reposts`、`bookmarks`、`bookmark_folders` |
| `modules/notice` | `notifications`、`mentions` |
| `modules/dm` | `dm_conversations`、`dm_messages` |
| `modules/media` | `attachments`、`attachment_derivatives` |
| `modules/ai` | `model_providers`、`ai_profiles`、`ai_model_settings`、`ai_behavior_settings`、`ai_memories`、`ai_memory_sources` |
| `modules/agent` | `ai_tasks`、`ai_task_memory_refs`、`ai_call_logs`、`ai_action_counters` |
| `modules/admin` | 无自有实体；只读 read-model 跨表 JOIN/聚合，不写任何表 |
| `platform/jobs` | `background_jobs` |
| `platform/audit` | `admin_audit_logs` |
| `platform/settings` | `system_settings` |
| `platform/migrate` | `schema_migrations` |

规则：一张表只有一个写所有者；其它模块需要读写时通过所有者 app 的端口。`media` 更新头像、`admin` 停用/重置密码等 `users` 写操作都经 `auth/app` 端口完成。`users.avatar_attachment_id` 由 auth 拥有、media 通过端口双写；历史头像的旧数据由迁移 `0016_review_fixes` 一次性回填，用于替代附件清理里拼 URL 的跨表条件。`users.dm_policy`（`0021`）由 auth 拥有并读写；`dm` 模块通过 `auth` 的 `UserReader` 读取策略与账号状态、通过 `social` 的 `RelationReader` 读取关注关系，`dm` 不向其它模块暴露能力（私信不进通知中心、管理后台不可见）。

## 5. 端口与 contracts

- 消费者在 `modules/<x>/app/port.go` 定义窄接口（如 `content/app` 的 `PostReader`、`social/app` 的 `Notifier`、`auth/app` 的 `UserWriter`）；生产者由组合根传入，不反向 import。
- 跨模块命令/查询/结果结构放 `shared/contracts`（如 `ActorPostInput`、`CommentInput`、`NotificationInput`、`AuditEntry`、`JobEnqueue`、`SystemSettings`）；跨模块读取端口同样内嵌在发布接口里（如 `SocialService` 内嵌 `RelationReader` 与 `BookmarkReader`，content 经后者读取收藏 refs、social 持有收藏表）。
- 平台端口同样以接口注入：`platform/jobs.Runner`（入队/领取）、`platform/audit.Writer`、`platform/settings.Reader/Writer`、`platform/clock.Clock`。
- 模块间**禁止**直接 `SELECT/UPDATE` 对方表；`admin/repo/read_model.go` 是只读例外，且可见性/权限条件由 app 传入。

## 6. 事务、错误与时间规范

**事务**

- 唯一入口：`platform/db.TxManager.WithTx(ctx, fn)`；ctx 内传播事务，嵌套调用合并为外层事务。
- app 用例是事务边界：注册初始化、发帖（帖子+草稿+版本+附件+标签+搜索+通知+作业）、评论/点赞（事件+计数+通知）、用户停用、AI 动作、管理员授权等。
- handler/http 不出现 `pool.Begin/Commit/Rollback`；repo 通过 `db.ExecutorFrom(ctx, pool)` 自动复用当前事务或连接池。

**错误**

- `platform/apperr`：`Kind`（NotFound/Forbidden/Unauthenticated/Invalid/Validation/Conflict/Suspended/RateLimited/Unavailable）+ `Code/Message/Details/RetryAfter`。
- `transport/httpx.Respond` 是唯一 HTTP 映射入口；handler/中间件共用；未知错误 → 500 并记录日志。
- ERROR 级别日志自动附加完整调用栈：`platform/logging` 在最外层 handler 统一包装（跳过 slog/logging 内部帧，首帧为业务调用点），text 格式在记录行后缩进多行输出、json 格式为带 `\n` 转义的 `stack` 字符串字段；WARN/INFO 与访问日志不附加，调用方显式传入 stack（panic 恢复、作业 panic）时原样保留、不重复采集。HTTP 请求处理 panic 由 `transport/middleware.Recovery`（全局中间件最外层，替换 `gin.Recovery`）用 slog 记录完整栈（`debug.Stack()`，含 goroutine 头）并返回统一 500 `internal_error`，响应已写出不覆盖，客户端断开按 WARN 记录摘要且不打印栈；后台作业/周期任务 panic 同样记录完整栈。
- 错误码、状态码、JSON 字段保持 `docs/API.md` 契约不变；公开用户 DTO 不含 `is_super_admin`。

**时间**

- `platform/clock`：`Clock.Now()`、`System()`、`Fake`、`DayStart`（固定 `Asia/Shanghai`），是 `time.Now()` 的唯一来源（例外：平台 ID 雪花时钟、日志按日目录、access log 耗时）。
- 写库时间由 app 取一次 `now` 显式传参；同一事务所有 `created_at/updated_at/published_at/deleted_at/resolved_at/run_at/started_at/finished_at/last_used_at/suspended_at` 使用同一值。
- SQL 不出现 `now()`、`CURRENT_DATE`、`::date`、`interval` 运算；会话过期、作业租约、AI reconcile、清理 cutoff、日统计窗口全部参数化。
- 首次发布：`published_at = COALESCE(published_at, $n)`，保持“不覆盖首次发布时间”。
- 日期列（如 `ai_action_counters.counter_date`）与日界统计用 `clock.DayStart` 生成 `[dayStart, dayEnd)`；`TZ=UTC` 下测试也应通过。
- `timeline.schema_migrations.applied_at` 保留 `DEFAULT now()`，是数据库时间函数唯一白名单。

## 7. 路由策略组

路由由 `transport/router` 的**声明式路由表**统一注册，每条路由只声明 `Method/Path/Policy/Handler`，中间件链由 Policy 决定：

| Policy | 链（外→内） | 适用 |
| --- | --- | --- |
| `Public` | `Ready` | `/healthz`、`/readyz`、`GET /auth/config` 等无需身份的入口 |
| `Optional` | `Ready → ResolveSession` | 公开 GET 与 `GET /auth/csrf`（游客可读，登录用户带 viewer_state） |
| `Login` | `… → RequireLogin` | 已登录只读：`/auth/me`、`/me/*`、通知、关注申请、关注时间线 |
| `HumanWrite` | `… → RequireLogin → RequireCSRF` | 业务写 POST |
| `AdminRead` | `… → RequireLogin → RequireAdmin` | 管理后台 GET |
| `AdminWrite` | `AdminRead → RequireCSRF` | 普通管理 POST |
| `SuperAdminWrite` | `AdminWrite → RequireSuperAdmin` | `grant-admin` / `revoke-admin`（CSRF 在超管校验之前，保持现有错误语义） |
| `GuestWrite` | `Ready → GuestCSRF` | `POST /auth/register`、`POST /auth/login` |

`ResolveSession` 每请求最多查一次会话并把 `model.Viewer`/`model.Session` 放入上下文；`RequireLogin/Admin/SuperAdmin` 只读上下文不查库。路由表是纯数据，`routes_test.go` 无数据库断言总数、Method+Path 唯一、非 GET 必须写策略、`/admin` 必须是 Admin/SuperAdmin 策略等。

## 8. 架构守卫

`internal/architecture/architecture_test.go` 用 `go/parser` 静态扫描目标结构源码（新树、`cmd/`、`internal/server`、`internal/transport/router` 与显式例外的 `internal/testkit`），CI 通过 `go test ./internal/architecture` 执行（`go test ./...` 也会运行）：

| # | 规则 |
| --- | --- |
| 1 | 模块四层 import 限制：`modules/*/domain` 不 import gin/net/http/pgx/database/sql；`app` 不 import gin/net/http/pgx 且字符串字面量不含 SQL 关键字；`repo` 不 import gin/net/http；`http` 不 import pgx/database/sql |
| 2 | 模块互斥：`internal/modules/x/**` 不 import `internal/modules/y`（x≠y） |
| 3 | 依赖方向：`server` 不 import modules/transport-router；`transport/router` 不 import modules；`shared`/`platform` 不 import modules/transport/server |
| 4 | SQL 位置：SQL 关键字只允许 `modules/*/repo/**`、`platform/{jobs,audit,settings,migrate}/**` 与 `internal/testkit/**`（测试基础设施显式例外；新增例外必须在此登记并说明理由） |
| 5 | Gin 位置：`gin.Context` 只允许 `modules/*/http/**`、`transport/**`、`server` |
| 6 | 时间位置：`time.Now()` 只允许 `platform/{clock,id,logging}/**` 与 `transport/middleware/accesslog.go`；SQL 时间函数只允许 `platform/migrate/**` |
| 7 | 事务原语：`Begin/Commit/Rollback` 只允许 `platform/{db,migrate,jobs}`（迁移器与作业运行器需要自管理事务；业务层必须走 `db.TxManager.WithTx`） |
| 8 | 遗留符号：`app`/`domain`/`repo` 中 `c.JSON`、`internalError(c)`、`access.Querier` 为 0（`http` handler 的 `c.JSON` 是合法的 DTO 输出方式，不在规则范围） |
| 9 | `cmd/server` import 白名单：只允许 `internal/app`、`internal/server`、`platform/{config,db,id,logging,migrate,storage}` 与标准库/第三方；业务服务构造必须在组合根 |

守卫自带覆盖性自检（`TestGuardCoversNewTree` 断言新树、`cmd/` 与 `internal/testkit` 的文件数均非零，避免扫描不到文件时“假通过”）。旧 `internal/` 顶层业务包已全部从仓库删除，守卫不再维护旧包白名单；需要对照历史实现时用 `git show b6fed97:<path>`。

## 9. 迁移状态

- 迁移文件按文件名追加执行，记录于 `timeline.schema_migrations`；仓库当前包含 `0001`–`0023` 迁移文件（是否已应用以部署记录为准）。
- 迁移器 v2（`platform/migrate`）支持首行 `-- timeline:no-transaction`：该文件语句不包事务，用于 `CREATE INDEX CONCURRENTLY`；此类迁移必须幂等（`IF NOT EXISTS`），失败后按顺序人工核对重跑，迁移器不自动回滚。`CONCURRENTLY` 失败留下的 INVALID 索引处置见 `DEPLOYMENT.md` 第 6 章。
- `0011_drop_time_defaults`：移除 43 个业务时间列的 `DEFAULT now()`，`schema_migrations.applied_at` 为唯一白名单；必须在应用侧“所有写路径显式传时间”上线后执行。
- `0012_add_avatar_attachment_id`：只加 `users.avatar_attachment_id` 与索引，不回填；回填完成前清理 SQL 不得依赖该列非空。
- `0013_timeline_and_task_indexes`：`reposts_timeline`、`ai_tasks_daily`、`ai_call_logs_retention_id`、`attachments_status_deleted`，no-transaction + `CONCURRENTLY`。
- `0014_search_trgm`：`pg_trgm` 扩展 + `post_search_text_trgm` GIN(`LOWER(plain_text) gin_trgm_ops`)，no-transaction + `CONCURRENTLY`；查询需改写为 `LOWER(plain_text) LIKE '%' || $n || '%'` 才能命中索引，且扩展必须位于连接 `search_path` 内的 schema（生产为 `timeline`）。
- `0015_drop_unused_indexes`：删除未使用索引 `post_search_updated`。
- `0016_review_fixes`（普通事务）：当 `avatar_url` 恰为 `/api/v1/attachments/<id>/content` 时用正则提取 ID，回填 `"user".users.avatar_attachment_id`（仅当列为 `NULL`、可重复执行、不改 `updated_at`/业务时间列），并删除与运行期查询形态不匹配的 `timeline.ai_tasks_daily` 索引；保留 `ai_call_logs_retention_id`。这是“迁移默认不回填业务数据”的评审例外，详见 `DATA_MODEL.md` §4.5。
- `0017_sql_review_indexes`（no-transaction + `CONCURRENTLY`）：SQL 最简原则改造的查询索引批次——关注游标覆盖索引、评论作者/顶层/trigram、附件文件名 trigram/有效附件/清理候选、提及时间窗、`background_jobs` running、`ai_tasks` running/last_success、用户与标签搜索表达式索引、引用帖部分索引、`ai_action_counters` 新列序唯一索引。
- `0018_drop_redundant_indexes`（no-transaction + `CONCURRENTLY`）：删除被 `0017` 覆盖或运行期无查询/与主键等价的索引（`auth_sessions_expiry`、`admin_audit_logs_created`、`users_admin_list`、`post_versions_post`、`posts_quoted`、`ai_tasks_action`、旧 `ai_action_counters_unique`、旧 follows/attachments 被替换索引）；保留 `comments_author`（管理端评论审核按作者过滤时不带 status，新部分索引不能替代）。
- `0019_author_is_ai_and_attachment_usage`（普通事务）：新增 `posts/reposts.author_is_ai` 快照列并回填 `users.is_ai`；新增 `attachment_usage` 配额冗余计数表并从 `attachments` 明细回填。这是“迁移默认不回填业务数据”的评审例外，依据与幂等条件见文件头注释与 `DATA_MODEL.md` §4.5。
- `0020_author_is_ai_indexes`（no-transaction + `CONCURRENTLY`）：作者类型时间线索引（`posts`、`reposts`）。
- `0021_direct_messages`（普通事务）：新增 `"user".users.dm_policy`（默认 `all`，CHECK 兜底）与 `timeline.dm_conversations`、`timeline.dm_messages` 及配套索引；无回填（新列默认值生效，AI 限制由 dm 入口校验）、无 `CONCURRENTLY`。
- `0022_post_pinned`（普通事务）：`posts` 新增 `pinned_at`/`pinned_sort` 与 CHECK 约束；无回填、无新索引（复用 `posts_author_published`）。
- `0023_bookmarks`（普通事务）：新增 `timeline.bookmark_folders`（收藏夹，`deleted_at` 逻辑删除）与 `timeline.bookmarks`（`is_active` 失效 + `folder_id NULL` 默认收藏）及配套部分索引；`(user_id, post_id)` 唯一索引非部分以支撑 upsert 复活；无回填、无 `CONCURRENTLY`。
- **执行顺序**：先切换迁移器 v2（支持 no-transaction），再应用 `0013`/`0014`；旧的事务式迁移器执行 `CREATE INDEX CONCURRENTLY` 会失败。单个升级窗口内按文件名顺序执行到 `0023`，失败时按 `DEPLOYMENT.md` 第 6、9 章人工核对。

## 10. 当前改造进度（refactor 分支）

- 目标架构已全部落地并接管运行路径：`cmd/server`（进程生命周期）→ `internal/app`（组合根）→ `transport/router`（声明式路由）→ `modules/*` 九域四层；`platform/*`、`shared/*`（model/access/contracts/page/richtext/format/view）、`transport/{httpx,middleware}`、slim `internal/server`、`internal/testkit` 集成测试脚手架、迁移器 v2 与架构守卫均已完成。旧 `internal/*` 业务包已从仓库删除，集成测试迁移到 testkit + `internal/server`/模块 `http` harness。
- 迁移：`0001`–`0021` 已追加（`0016` 头像回填与不匹配索引清理；`0017`–`0020` 为 SQL 最简原则改造的查询索引、冗余计数表与作者类型快照列；`0021` 为私信会话/消息与 `users.dm_policy`）；新树 SQL 不再使用 `now()`/`CURRENT_DATE`/`::date`/`interval` 等数据库时间权威。
- 架构守卫：`go test ./internal/architecture` 全绿；扫描范围含新树、`cmd/`、`internal/server` 与 `internal/testkit` 显式例外，新增 `cmd/server` import 白名单、事务原语与遗留符号规则（详见第 8 章）。
- 本次行为修复（审查问题）：AI 候选范围/目标作者二次授权、任务失败自动重试与 reconcile、记忆沉淀合法值与来源失效复核、`model_override`、联网搜索有限工具轮次、`global_public` 候选与评论/回复开关语义、停机二次校验与暂停范围、发帖限频兜底、搜索 trigram 查询形态、附件/引帖校验事务化、附件清理两步端口流程与头像兜底、作业 Reactivate/人工重试 500/GetJob 404、bootstrap `/api` 503、路由注册竞态、审计单写、资料统计口径、登录标识索引写法、架构守卫扩展与 `0016` 迁移。
- 待完成：SQL/索引性能验证（`EXPLAIN`、对账任务）；完整 P0/SEC 人工验收与部署机复演（`docs/ACCEPTANCE.md`、`docs/DEPLOYMENT.md`）。

## 11. 架构不变式（DoD 摘录）

1. `*gin.Context` 只出现在 `modules/*/http/**`、`transport/**`、`server`；业务包为 0。
2. SQL 关键字只出现在 `modules/*/repo/**`、`platform/{jobs,audit,settings,migrate}/**` 与显式登记的测试基础设施 `internal/testkit/**`；其余 Go 源码为 0。
3. `c.JSON`、`internalError(c)`、`access.Querier` 在 app/domain/repo 中为 0（`http` handler 的 `c.JSON` 是合法 DTO 输出方式）。
4. `pool.Begin/Commit/Rollback` 只在 `platform/{db,migrate,jobs}`（迁移器与作业运行器自管理事务）；handler 与业务层为 0。
5. `time.Now()` 只在 `platform/{clock,id,logging}` 与 access log；SQL 中 `now()/CURRENT_DATE/::date/interval` 除迁移器白名单为 0。
6. `modules/x` 不 import `modules/y`；`shared`/`platform` 不 import 上层。
7. 路由快照与 `docs/API.md` 的 method/path/状态码/错误码/JSON 字段保持契约不变；策略矩阵由无数据库单测断言。
8. 时间不变量：会话过期、作业租约、AI reconcile、日统计全部使用应用时钟；`information_schema` 无业务 `now()` 默认值；`TZ=UTC` 测试通过。

## 12. 变更记录

| 日期 | 版本 | 变更内容 |
| --- | --- | --- |
| 2026-10-18 | 0.3.1 | 同步 ERROR 日志调用栈与 panic 恢复：§2 目录结构 `transport/middleware` 补 `Recovery`；§6 错误规范补充 ERROR 日志自动带完整调用栈（text 记录后多行缩进 / json `stack` 字段、首帧为业务调用点、WARN/INFO 与访问日志不附加）、HTTP 请求 panic 由 `transport/middleware.Recovery` 记录完整栈并返回统一 500 `internal_error`（已写出不覆盖、客户端断开按 WARN 无栈）、后台作业/周期任务 panic 记录完整栈；分层、依赖边界与数据所有权不变。 |
| 2026-10-18 | 0.3.0 | SQL 最简原则改造同步：§9 迁移状态更新到 `0001`–`0020` 并说明 `0017`–`0020`（查询索引批次、冗余/无效索引删除、`author_is_ai` 快照列 + 回填、`attachment_usage` 配额冗余表）；§10 进度更新迁移范围；数据所有权与依赖边界不变（新增 `shared/search` 纯函数辅助包；`attachment_usage` 归 media、`author_is_ai` 随所属内容表由 content/social 写）。 |
| 2026-10-18 | 0.2.0 | 重构完成 + 本次行为修复同步：§2 目录按实际结构修正（`app` 无独立 `bootstrap.go`，Bootstrap 在 `app.go`；补 `readers.go`/`adapters.go`、`shared/format`、`shared/view`、`internal/architecture`、`internal/testkit`）；§3 修正 `cmd/server` 实际 import 白名单（`app`、`platform/{config,db,id,logging,migrate,storage}`、`server`）；§8 守卫改为覆盖新树/`cmd/`/`internal/server` 与 `internal/testkit` 例外，新增 import 白名单、事务原语与遗留符号规则，删除“旧包待删除”表述；§9 迁移补 `0016_review_fixes` 并明确 INVALID 索引处置与 `pg_trgm` schema 要求；§10 进度更新为“目标架构完成 + 本次行为修复清单 + 待完成项”。 |
| 2026-10-18 | 0.1.1 | 版本/基线同步（不改变架构结构）：需求基线更新至 `REQUIREMENTS.md` 0.24.1；§10 改造进度更新为当前状态（`internal/app` 组合根与 `transport/router` 已接管运行路径、八域模块四层实现完成、旧包退出运行路径仅剩删除与测试迁移、G6–G8 收尾项）。 |
| 2026-10-18 | 0.1.0 | 首次建立后端目标架构文档：模块化单体与九条原则、目录结构、模块四层职责、依赖方向与允许 import、数据所有权、端口与 contracts、事务/错误/时间规范、路由策略组、架构守卫（`internal/architecture`）、迁移状态（0001–0015，含 no-transaction 与时间默认值移除）与当前改造进度。 |
| 2026-10-19 | 0.4.0 | 需求基线更新至 0.25.0，新增私信模块：§2 目录结构新增 `modules/dm`（会话/消息/已读/撤回，管理后台不可见）；§4 数据所有权新增 `dm_conversations`、`dm_messages` 归 dm，`users.dm_policy` 归 auth（dm 只经 UserReader/RelationReader 读取，不向其它模块暴露能力）；§9 迁移状态补 `0021_direct_messages`（普通事务、无回填）；§10 八域改九域、迁移范围到 0021。 |
| 2026-10-21 | 0.4.2 | 需求基线更新至 0.27.0，新增收藏：§4 数据所有权补 `bookmarks`/`bookmark_folders` 归 social（content 仅经 `BookmarkReader` 读 refs 组装列表，不触收藏表）；§5 端口说明补 `SocialService` 内嵌 `BookmarkReader` 先例；§9 迁移范围 0001–0023 并补 `0022_post_pinned`（上轮置顶未记录）与 `0023_bookmarks` 两条说明。 |
