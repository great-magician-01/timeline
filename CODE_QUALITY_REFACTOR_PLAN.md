# Timeline 后端目标架构与改造方案（v2 · 结构优先）

> 状态：**已完成（refactor/target-architecture 分支）**。G1–G8 已全部落地：新架构接管运行路径、旧包已删除；默认测试、集成测试（12/12）、`go vet`、架构守卫与前端构建均通过。
> 基线：主工作区 HEAD `b6fed97`（super-admin 合并后）。当前代码已按本方案重构，文档以 `docs/ARCHITECTURE.md` 为权威。

---

## 0. 目标与设计原则

改造后的目标是：**一个模块化单体（modular monolith）**——领域边界清晰、依赖方向可静态强制、HTTP/业务/SQL/事务/时间各自归位，同时保持现有 110 条 API 契约与权限行为不变。

九条设计原则（后续所有结构决策以它们为准）：

1. **按限界上下文划分模块**，模块内部四层：`domain`（纯规则）→ `app`（用例/事务）→ `repo`（pgx/SQL）与 `http`（Gin）。模块之间不允许互相 import。
2. **依赖方向单向**：`cmd → app（组合根）→ server/router/modules → transport/platform/shared`；`transport` 不依赖 `cmd`，`modules` 不依赖 `transport`，`shared` 不依赖任何上层。
3. **编译期强制边界**：模块内 `domain/app/repo/http` 是独立 Go 包；再加 `internal/arch` 静态守卫（import 规则 + SQL/时间函数位置白名单），让越界在 CI 失败。
4. **HTTP 只属于 transport**：业务核心（domain/app）不出现 `gin`、`net/http`、`pgx`、`pool.Begin`、`c.JSON`；Gin 只出现在 `*/http` 与 `transport`。
5. **SQL 只属于 repo**：所有 SQL 字符串、Scan、分页查询、`FOR UPDATE`、计数更新只出现在 `*/repo`；跨模块只读的 admin read-model 作为显式例外。
6. **事务由 app 编排**：`platform/db.TxManager.WithTx` 是唯一事务入口，支持 ctx 传播/嵌套合并；一次写操作只取一次 `clock.Now()`。
7. **时间是应用输入**：所有写库时间、日界、租约、退避截止时间由应用生成并作为参数传入；数据库不提供业务时间默认值、不在 SQL 里做时间运算（`schema_migrations.applied_at` 是唯一白名单）。
8. **跨模块只走端口**：消费者在 `app/port.go` 定义窄接口，生产者 app 类型结构化实现，组合根装配；跨模块命令/查询结构体放 `shared/contracts`；禁止跨模块直接查表。
9. **错误领域化、响应统一化**：`platform/apperr` 表达领域错误；`transport/httpx.Respond` 是唯一 HTTP 映射入口；DTO 在 `*/http/view.go`，不把 JSON 形状带进领域。

与 v1 方案的关键差别：

| v1（折中） | v2（最优结构） |
| --- | --- |
| 每包内文件拆分 `handler_*/app_*/repo_*`，边界靠纪律 | **每模块内 4 个子包**，import 不可能越界 |
| 策略组 + 显式注册 | **声明式路由表 + Policy 枚举**，路由矩阵可单测 |
| 共享 `access.Querier` 过渡 | **删除 Querier**，repo 从 ctx 取 Executor；`access` 变纯策略 |
| 时间改造分阶段塞进旧结构 | 新 repo 从第一天就接收显式 `now`，旧写路径迁完后再删 DB 默认值 |
| 迁移器原样 | **升级迁移器支持 `no-transaction` 指令**，支持 `CREATE INDEX CONCURRENTLY` |
| 未考虑 super-admin | super-admin 不变式进入 auth 域 + 路由策略 + 测试矩阵 |

---

## 1. 结构总览

```
timeline/
├── cmd/server/main.go                  # 进程生命周期：TZ、配置、日志、信号、启动/优雅关闭、migrate 子命令
├── internal/
│   ├── app/                            # 组合根（唯一允许 import 全部模块的地方）
│   │   ├── app.go                      # App：engine、runner、readiness、关闭
│   │   ├── wire.go                     # 构造 platform → repo → app → http，注入端口
│   │   ├── jobs.go                     # 作业处理器与周期任务注册
│   │   └── bootstrap.go                # 迁移后初始化状态检查与日志
│   ├── platform/                       # 技术基座（无业务语义；任何层可依赖）
│   │   ├── clock/{clock.go,fake.go}    # Clock/Fake/DayStart，唯一 time.Now 来源
│   │   ├── config/                     # 环境变量加载与校验（只给 app/main 用）
│   │   ├── db/{pool.go,readiness.go,tx.go,executor.go}
│   │   ├── id/                         # 雪花 ID（保留独立时钟与回拨保护）
│   │   ├── logging/
│   │   ├── migrate/                    # 迁移器 v2（no-transaction 指令）
│   │   ├── storage/                    # 本地附件卷
│   │   ├── ratelimit/
│   │   ├── apperr/                     # 领域错误 Kind/Error/构造器
│   │   ├── jobs/{runner.go,queue.go,repo.go}   # background_jobs 所有权 + 通用 Runner
│   │   ├── audit/{writer.go,repo.go}   # admin_audit_logs 所有权
│   │   ├── settings/{values.go,keys.go,store.go,repo.go}  # system_settings 所有权
│   │   ├── websearch/                  # 受控联网搜索 HTTP 客户端（出站适配器）
│   │   └── aiclient/                   # OpenAI 兼容 HTTP 客户端（出站适配器）
│   ├── shared/                         # 共享内核（无 IO；可以是纯类型与纯函数）
│   │   ├── model/                      # 跨上下文数据结构与枚举（User/Session/Viewer/Post/Comment/Attachment/Notification…）
│   │   ├── access/                     # 纯授权策略（可见性/互动/角色/超管保护）
│   │   ├── contracts/                  # 跨模块命令/查询/结果/端口数据类型
│   │   ├── page/                       # 游标分页类型与编码
│   │   └── richtext/                   # 富文本/Markdown 清洗与链接化（纯函数）
│   ├── transport/
│   │   ├── httpx/                      # JSON 错误/解码/ID/时间/分页/上下文读取（原 internal/api 的 HTTP 部分）
│   │   ├── middleware/                 # ready、security headers、access log、request id
│   │   └── router/                     # 唯一注册路由的地方：Policy 枚举 + 路由表 + 领域路由文件
│   ├── server/                         # Gin 引擎装配、健康检查、前端静态托管（不含业务路由）
│   └── modules/
│       ├── auth/{domain,app,repo,http}      # 账号、会话、角色、超管、注册引导、CSRF
│       ├── content/{domain,app,repo,http}   # 帖子、草稿、版本、发布、时间线、标签、搜索
│       ├── social/{domain,app,repo,http}    # 关注/申请、点赞、评论、转发
│       ├── notice/{domain,app,repo,http}    # 通知、提及
│       ├── media/{domain,app,repo,http}     # 附件、衍生文件、清理
│       ├── ai/{domain,app,repo,http}        # 预设、AI 身份/行为、记忆、运行时
│       ├── agent/{domain,app,repo,http}     # 调度、决策、动作、调用记录、AI 管理接口
│       └── admin/{app,repo,http}            # 审核/概览/任务/审计查询（编排型上下文，无自有实体）
├── migrations/                         # 只追加 SQL
├── web/                                # Vue 前端（不参与后端结构改造）
└── docs/                               # 新增 docs/ARCHITECTURE.md 作为权威架构文档
```

**职责边界一句话版**：

| 位置 | 只做 | 绝不做 |
| --- | --- | --- |
| `platform/*` | 通用技术能力（时钟、DB、事务、ID、日志、存储、限流、配置、迁移、外部客户端） | 业务规则、HTTP 路由 |
| `shared/*` | 共享数据类型、纯策略、跨模块 contracts、游标、富文本纯函数 | SQL、IO、Gin、依赖 modules |
| `modules/*/domain` | 实体不变式、状态机、纯校验与策略 | SQL、IO、Gin、依赖其它模块 |
| `modules/*/app` | 用例编排、事务边界、时钟取时、端口调用、审计/通知/入队协作 | SQL、Gin、`net/http`、`pgx` |
| `modules/*/repo` | 唯一 SQL 层：查询、Scan、FOR UPDATE、批量载入 | 业务规则、HTTP、时钟（`now` 由 app 传入） |
| `modules/*/http` | Gin handler、参数解析、错误映射、DTO 组装、本域中间件 | SQL、`pgx`、`pool.Begin` |
| `transport/router` | 路由表、策略组、路径/方法注册 | 业务逻辑、SQL |
| `server` | Gin 引擎、全局中间件、健康检查、前端托管 | 业务路由、服务构造 |
| `app` | 构造全部依赖、装配端口、注册作业 | 业务规则、SQL |

---

## 2. 分层规则与依赖矩阵（含静态守卫）

### 2.1 允许的 import

| 包 | 允许 import | 禁止 import |
| --- | --- | --- |
| `shared/model`、`shared/page`、`richtext` | 标准库 | 任何 `internal/` 上层 |
| `shared/access` | `shared/model`、`platform/apperr` | SQL、Gin、其它 |
| `shared/contracts` | `shared/model` | 业务包、平台实现 |
| `platform/*` | 标准库 + 第三方驱动；`platform/audit/jobs/settings` 可用 `shared/contracts`、`shared/model` | `modules/*`、`transport/*`、`server`、`app` |
| `modules/x/domain` | `shared/model`、`shared/access`、`platform/apperr` | `repo`、`http`、其它模块、Gin、pgx |
| `modules/x/app` | 本模块 `domain`、`shared/*`、`platform/{clock,db,apperr,jobs,audit,settings,storage,ratelimit,id,logging,websearch,aiclient}` | Gin、`net/http`、pgx、**其它 `modules/*`** |
| `modules/x/repo` | `platform/db`、`platform/apperr`、`shared/model`、本模块 `domain`、pgx | Gin、`modules/x/app`、其它模块 |
| `modules/x/http` | 本模块 `app`、`shared/model`、`shared/page`、`transport/httpx`、Gin、`platform/apperr` | pgx、`platform/db`、其它模块 |
| `transport/router` | 各模块 `http`、`shared`、`transport/httpx/middleware`、`platform/db`（Readiness） | 模块 `app/repo/domain`、`server` |
| `server` | Gin、`platform/db/logging`、`web` | 任何 `modules/*`、`router` |
| `app` | 全部 | — |
| `cmd/server` | `app`、`platform/config/logging/migrate` | 业务模块 |

### 2.2 组合根避免循环依赖

- `server.New(cfg, log, readiness)` 只构造 Gin 引擎与全局中间件，暴露 `Engine() *gin.Engine`、`Handler()`。
- `router.Register(engine, deps, readiness)` 由 **app 组合根**调用，`server` 不 import `router`，`router` 不 import `server`。
- `main` 只 import `app`；`app` 负责构造 `db.Readiness`、全部 repo/app/http、注册作业、创建 server 并注册路由。

```
cmd/server ──▶ internal/app
                 ├─▶ internal/server ──▶ transport/middleware
                 ├─▶ internal/transport/router ──▶ modules/*/http ──▶ modules/*/app ──▶ modules/*/repo
                 ├─▶ modules/*/app ──▶ modules/*/repo
                 ├─▶ platform/*
                 └─▶ shared/*
```

### 2.3 架构守卫（`internal/arch/arch_test.go`）

用 `go/parser` 扫描 `internal/`，断言语义规则；CI 直接跑 `go test ./internal/arch`：

1. **模块互斥**：`modules/x/**` 的 import 不得出现 `internal/modules/y`（x≠y）。
2. **层规则**：`domain` 不含 gin/net/http/pgx；`app` 不含 gin/net/http/pgx 与 SQL 关键字；`repo` 不含 gin/net/http 与 `time.Now`；`http` 不含 pgx/database/sql。
3. **SQL 位置**：所有 Go 源码字符串中的 `SELECT/INSERT/UPDATE/DELETE FROM` 只允许出现在 `modules/*/repo/**`、`platform/{jobs,audit,settings}/**`（各自的 repo 文件）、`internal/migrate`；测试文件与 `migrations/*.sql` 豁免。
4. **时间位置**：`time.Now()` 只允许 `platform/clock`、`platform/id`、`platform/logging`、`transport/middleware/accesslog.go`；SQL `now()/CURRENT_DATE/::date/interval` 只允许 `internal/migrate` 白名单。
5. **DB 原语**：`pool.Begin/Commit/Rollback` 只允许 `platform/db` 与 `platform/jobs`（Runner 内部）；`*pgxpool.Pool` 只允许 `platform/db` + 各 repo 构造器 + `app/wire.go`。
6. **Gin 位置**：`*gin.Context` 只允许 `modules/*/http/**`、`transport/**`、`server`。
7. **遗留符号**：`access.Querier`、`PoolProvider`、`internalError(c`、`c.JSON` 在 repo/app/domain 中出现即失败。

这些断言是「结构最优」能否长期保持的关键：没有它们，重构完成后仍会回潮。

---

## 3. 共享内核（shared）

### 3.1 `shared/model`：跨上下文数据形状

只放**数据结构与枚举**，不放 SQL/DTO/JSON tag 之外的行为；JSON 响应形状在 `*/http/view.go`。示例：

```go
package model

type Role string       // "user" | "admin"
type UserStatus string // "active" | "suspended"

type User struct {
    ID           int64
    Username     string
    Email        string
    PasswordHash string
    Nickname     string
    AvatarURL    string
    Bio          string
    Role         Role
    IsSuperAdmin bool
    IsAI         bool
    Status       UserStatus
    RequireFollowApproval bool
    TokenVersion int64
    SuspendedAt  *time.Time
    CreatedAt    time.Time
    UpdatedAt    time.Time
}

type Viewer struct {
    ID            int64
    Authenticated bool
    IsAdmin       bool
    IsSuperAdmin  bool
    IsAI          bool
    IsSuspended   bool
}

type Session struct {
    ID           int64
    UserID       int64
    CSRFToken    string
    TokenVersion int64
    ExpiresAt    time.Time
    RevokedAt    *time.Time
}

type Post struct { ID, AuthorID int64; Status, Visibility string; PublishedAt *time.Time; ... }
type Comment struct { ... }
type Attachment struct { ... }
type Notification struct { ... }
```

### 3.2 `shared/access`：纯授权策略（无 SQL）

```go
package access

func CanViewPost(v model.Viewer, p model.Post, following map[int64]bool) bool
func CanInteract(v model.Viewer, p model.Post) error          // *apperr.Error
func CanManageAdmins(v model.Viewer) bool                     // 超管判定唯一实现
func CanModerateUser(actor model.Viewer, target model.User) error // 超管保护
func CanChangeAdminRole(actor, target model.User) error       // 不能操作自己/超管/AI
```

`access` 从「带 SQL 的授权包」变为纯函数：关注集合、作者状态等由 app 通过端口加载后传入；同一套策略函数同时被 HTTP 中间件（角色部分）与 app 用例复用。

### 3.3 `shared/contracts`：跨模块发布语言

跨模块调用的命令/查询结构放这里，避免生产者与消费者互相 import：

```go
package contracts

type ActorPostInput struct { AuthorID int64; Action string; Format, Source string; ScheduledAt *time.Time; ... }
type CommentInput struct { AuthorID, PostID int64; ... }
type NotificationInput struct { RecipientID int64; Kind string; IdempotencyKey string; ... }
type AuditEntry struct { AdminID int64; Action, TargetType string; TargetID *int64; Detail any; At time.Time }
type JobEnqueue struct { Type, RefType string; RefID *int64; IdempotencyKey string; RunAt time.Time; ... }
type ModelClient interface { Create(ctx context.Context, req ChatRequest) (ChatResponse, error); ... }
type SystemSettings struct { RegistrationEnabled bool; UploadMaxFileBytes int64; PostRateLimit int; ... }
```

### 3.4 `shared/page` 与 `shared/richtext`

- `page.Cursor`：时间+ID 游标与 ID 游标的编码/解码、limit 校验（替代现在 content/social/admin 各写一套）。
- `richtext`：直接迁移现在的纯函数包，无改动。

---

## 4. HTTP 层

### 4.1 中间件分层

`transport/middleware`：通用中间件，不依赖任何业务模块。

| 中间件 | 作用 |
| --- | --- |
| `Recovery`（Gin 内置） | panic 恢复 |
| `AccessLog(log)` | method/path/status/latency/ip/request_id，可附 actor id |
| `SecurityHeaders` | nosniff/frame/referrer/no-store |
| `RequestID` | 生成并回写 request id，进入日志与响应头 |
| `RequireReady(readiness)` | pool 未就绪或迁移未完成 → 503 |

`modules/auth/http/middleware.go`：会话与授权策略（依赖 auth app）。

| 中间件 | 语义 |
| --- | --- |
| `ResolveSession` | 每个请求最多查一次会话：有效则把 `model.Viewer`+`model.Session` 写入 `httpx` 上下文；失效/缺失记录 `SessionState`（原因），不直接拒绝 |
| `RequireLogin` | 上下文无有效身份：缺/过期/吊销/AI → 401 并清 Cookie；停用 → GET 401、写请求 403 `account_suspended` |
| `RequireAdmin` | 已登录且 role=admin、非 AI → 否则 403 |
| `RequireSuperAdmin` | 在 admin 基础上要求 `IsSuperAdmin`，否则 403「需要超级管理员权限」 |
| `RequireCSRF` | 方法感知：GET/HEAD/OPTIONS 放行；其余校验 `X-CSRF-Token` 与当前会话 CSRF 一致（失败 403 `csrf_failed`） |
| `GuestCSRF` | 登录/注册双提交 Cookie 校验 |

**关键改动**：`OptionalAuth + RequireAuth` 双查询合并为 Base 组一次 `ResolveSession`；`RequireLogin/Admin/SuperAdmin` 只读上下文，不查库。`ResolveSession` 对失效会话主动清 Cookie，避免游客路径反复查询。

### 4.2 路由策略组（Policy）

不再逐条路由传 `csrf`/`RequireAuth`，而是把路由声明为一个 `Policy`，由 router 统一组装：

| Policy | 链（外→内） | 适用 |
| --- | --- | --- |
| `Public` | `Ready` | `/healthz`、`/readyz`、`GET /auth/config` |
| `Optional` | `Ready → ResolveSession` | 18 条公开 GET + `GET /auth/csrf`（游客读/公开列表拿 viewer_state） |
| `Login` | `Ready → ResolveSession → RequireLogin` | 已登录只读：`/auth/me`、`/me/*`、通知、关注申请、关注时间线 |
| `HumanWrite` | `… → RequireLogin → RequireCSRF` | 29 条 secured POST |
| `AdminRead` | `… → RequireLogin → RequireAdmin` | 19 条 admin GET |
| `AdminWrite` | `AdminRead → RequireCSRF` | 32 条普通 admin POST |
| `SuperAdminWrite` | `AdminWrite → RequireSuperAdmin` | `POST /admin/users/:id/grant-admin`、`POST /admin/users/:id/revoke-admin` |
| `GuestWrite` | `Ready → GuestCSRF` | `POST /auth/register`、`POST /auth/login` |

`SuperAdminWrite` 的链顺序刻意保持 `RequireCSRF` 在 `RequireSuperAdmin` 之前，与当前行为一致（缺 CSRF 时统一返回 `csrf_failed`）。

路由表是纯数据，便于无数据库单测：

```go
package router

type Policy int
const (Public Policy = iota; Optional; Login; HumanWrite; AdminRead; AdminWrite; SuperAdminWrite; GuestWrite)

type Route struct {
    Method, Path string
    Policy       Policy
    Handler      func(*Deps) gin.HandlerFunc
}

var routes = []Route{
    {GET, "/auth/config", Public, func(d *Deps) gin.HandlerFunc { return d.Auth.Config }},
    {GET, "/timeline", Optional, func(d *Deps) gin.HandlerFunc { return d.Content.Timeline }},
    {POST, "/posts", HumanWrite, func(d *Deps) gin.HandlerFunc { return d.Content.CreatePost }},
    {GET, "/admin/users", AdminRead, func(d *Deps) gin.HandlerFunc { return d.Admin.ListUsers }},
    {POST, "/admin/users/:id/grant-admin", SuperAdminWrite, func(d *Deps) gin.HandlerFunc { return d.Auth.GrantAdmin }},
    // …共 110 条，按领域拆 routes_auth.go / routes_content.go / …
}

func Register(engine *gin.Engine, d Deps, ready Readiness) {
    base := engine.Group("/api/v1", middleware.RequireReady(ready))
    public := base.Group("")
    optional := public.Group("", d.Auth.ResolveSession)
    login := optional.Group("", d.Auth.RequireLogin)
    humanWrite := login.Group("", d.Auth.RequireCSRF)
    adminRead := login.Group("/admin", d.Auth.RequireAdmin)
    adminWrite := adminRead.Group("", d.Auth.RequireCSRF)
    superAdminWrite := adminWrite.Group("", d.Auth.RequireSuperAdmin)
    guestWrite := base.Group("", d.Auth.GuestCSRF)
    groups := map[Policy]*gin.RouterGroup{
        Public: public, Optional: optional, Login: login, HumanWrite: humanWrite,
        AdminRead: adminRead, AdminWrite: adminWrite, SuperAdminWrite: superAdminWrite,
        GuestWrite: guestWrite,
    }
    for _, r := range routes { groups[r.Policy].Handle(r.Method, r.Path, r.Handler(&d)) }
}
```

`routes_test.go`（无 DB、CI 默认执行）断言：

1. 路由总数为基线 110，`Method+Path` 无重复，关键路径清单齐全（防迁移丢失）；
2. 非 GET 路由必须是 `GuestWrite/HumanWrite/AdminWrite/SuperAdminWrite`；
3. `/admin` 前缀必须是 `AdminRead/AdminWrite/SuperAdminWrite`；
4. grant/revoke 两条必须是 `SuperAdminWrite`；
5. 策略实现包含预期中间件（注册表 + 函数名断言）；
6. 生成的路由表可导出为文档/快照。

### 4.3 错误与响应

- `platform/apperr`：`Kind`（NotFound/Forbidden/Unauthenticated/Invalid/Validation/Conflict/Suspended/RateLimited/Unavailable）+ `Code/Message/Details/RetryAfter`；service/app 只返回 `*apperr.Error` 或包装的底层错误。
- `transport/httpx.Response`：唯一 HTTP 映射：`err → status/code/message/details`；中间件与 handler 共用；未知错误 → 500 + access log。
- 移除 8 份 `internalError(c)`；日志在 handler/transport 统一记录一次（含 request_id/actor）。
- API 契约不变：`docs/API.md` 的错误码/状态码/JSON 字段保持，`is_super_admin` 只在私有用户 DTO 出现、公开 DTO 不出现。

### 4.4 DTO / view

- 每个模块 `http/view.go` 定义响应结构 + 纯映射函数（如 `auth/http/view.go: PublicUser/PrivateUser`、`content/http/view.go: PostDetail`）。
- `shared/model` 不带 JSON tag；`gin.H` 只允许出现在 handler 最终响应处；列表响应统一用 `httpx.ListResponse`/`page`。
- 不在 handler 里拼业务字段；权限相关字段（`can_edit/can_delete/follow_state`）由 app 计算后交给 view。

---

## 5. 各模块设计

### 5.1 auth（账号/会话/角色/超管）

```
modules/auth/
├── domain/
│   ├── user.go            # Role/Status 常量、密码/用户名/邮箱校验、私有/公开字段规则
│   ├── session.go         # 会话有效期、token_version 失效规则
│   ├── bootstrap.go       # 空库初始化规则（首个注册 → admin+super）
│   └── role.go            # CanGrantAdmin/CanRevokeAdmin/CanModerate 等纯策略（委托 shared/access）
├── app/
│   ├── service.go         # App 构造：UserRepo/SessionRepo/Settings/Audit/Clock/IDs/logger
│   ├── register.go        # Register 用例：限频→校验→bcrypt（事务外）→WithTx{advisory lock→count→insert}
│   ├── login.go           # Login/Logout/ChangePassword
│   ├── session.go         # ResolveSession（鉴权唯一查询）、CSRF token 刷新
│   ├── profile.go         # Me/UpdateProfile/FollowSettings/GetProfile/SearchUsers
│   ├── admin_roles.go     # GrantAdmin/RevokeAdmin 用例（超级管理员权限、目标校验、审计、幂等）
│   ├── bootstrap.go       # BootstrapRequired/BootstrapStatus（只读用例，供 config 与启动日志）
│   └── port.go            # UserRepo/SessionRepo/SettingsReader/AuditWriter/Clock 等接口
├── repo/
│   ├── user_repo.go       # users SQL：GetByID(ForUpdate)/GetByUsername/Search/Insert/UpdateProfile/UpdateStatus/UpdateRole/CountHuman/CountSuperAdmins
│   ├── session_repo.go    # auth_sessions SQL：Insert/FindByTokenHash/Revoke/RevokeAllForUser/CountActive
│   └── bootstrap_repo.go  # pg_advisory_xact_lock、NOT EXISTS 人类用户查询
└── http/
    ├── handler.go         # Handler 构造与 Service 接口（http/port.go）
    ├── auth_handler.go    # Register/Login/Logout/Me/ChangePassword/CSRF
    ├── profile_handler.go # UpdateProfile/GetProfile/SearchUsers/FollowSettings
    ├── admin_role_handler.go # GrantAdmin/RevokeAdmin（路由在 /admin 下，但用例属 auth 域）
    ├── middleware.go      # ResolveSession/RequireLogin/RequireAdmin/RequireSuperAdmin/RequireCSRF/GuestCSRF
    └── view.go            # UserResponse/PrivateUser/PublicUser（超管标记仅私有）
```

要点：

- `users`/`auth_sessions` 的 SQL **只在这里**；media 改头像、admin 停用/重置密码都通过 auth app 端口完成。
- 注册初始化：bcrypt 在事务外；事务内 `SELECT pg_advisory_xact_lock` → `NOT EXISTS (human users)` 复核 → INSERT（显式 `created_at/updated_at`、`role`、`is_super_admin`、`token_version`）→ 提交；并发首注册只产生一个超管。
- `ResolveSession` 一次 JOIN 查询返回 User+Session（含 is_super_admin），替代现在的 OptionalAuth+RequireAuth 双查询。

### 5.2 content

```
modules/content/
├── domain/post.go          # 状态机（draft/scheduled/published/deleted）、可见性、定时规则、版本规则
├── app/
│   ├── service.go / post.go / draft.go / publish.go / version.go / timeline.go / tag.go / search.go
│   ├── jobs.go             # HandlePublishJob（作业用例仍走 app，不碰 Gin）
│   └── port.go             # PostRepo/VersionRepo/TimelineRepo/RelationshipReader(post visibility 所需)/Notifier/Queue/Auditor/Clock
├── repo/
│   ├── post_repo.go / draft_repo.go / version_repo.go / tag_repo.go / search_repo.go
│   ├── timeline_repo.go    # 唯一动态 SQL builder；可见性 SQL 谓词集中在这里
│   └── visibility.go       # SQL 可见性片段构造（与 shared/access 使用同一组测试向量）
└── http/
    ├── handler.go / post_handler.go / timeline_handler.go / version_handler.go
    └── view.go             # PostBrief/PostDetail/Version/Attachment DTO
```

要点：

- 帖子/草稿/版本/标签/搜索投影表 SQL 全部收在 repo；handler 只解析请求、调用 app、映射 view。
- 时间线 SQL 仍是「分页正确性需要的可见性过滤留在 SQL」，但收敛为单一 builder + SQL 单测；同时按访问者类型生成 SQL 变体（游客/AI 仅 public；登录人类 public+followers；作者页/标签页/搜索页专用查询），减少 OR/EXISTS 组合。
- 标签页从 `post_tags` 反驱动 JOIN posts；搜索走 `post_search` 的 trigram/FTS 方案（见 §7.4）。
- AI/管理员代发入口是 app 的 `CreateForActor/PublishForActor` 用例，不是另一套 handler。

### 5.3 social / notice / media

- **social**：`FollowRepo/LikeRepo/CommentRepo/RepostRepo/FollowRequestRepo` 分开；评论树与计数增量规则放 domain（纯函数）；通知/审计通过端口调用；计数更新保留单语句原子 `UPDATE ... RETURNING`，但去掉 `GREATEST` 夹取（见 §7.3）。
- **notice**：`NotificationRepo/MentionRepo`；`Notifier` 端口供 content/social/agent 调用，幂等键由调用方生成；通知列表预览通过 `content/app.PostReader`、`social/app.CommentReader` 端口批量读取，**不再直接查 posts/comments**。
- **media**：`AttachmentRepo/DerivativeRepo`；文件 IO 走 `platform/storage`；头像更新调用 `auth/app.UserWriter.SetAvatar`（auth 拥有 users SQL）；清理任务需要的「头像引用」通过 `users.avatar_attachment_id` 列（见 §7.4），不再在 SQL 拼 URL。

### 5.4 ai / agent

- **ai**：`ProviderRepo/ProfileRepo/ModelSettingsRepo/BehaviorRepo/MemoryRepo`；模型调用客户端在 `platform/aiclient`，运行时装配在 ai app；记忆检索 SQL 收在 repo，关键词/重要性排序等运算按 §7 规范简化。
- **agent**：`TaskRepo/CallLogRepo/ActionCounterRepo/MemorySourceRepo`；scheduler/decision/actions 都是 app 用例；对 content/social/ai 的调用全部通过 app 端口（`contracts` 类型），事务用 `db.WithTx` 在 agent app 内编排并由 ctx 传播；联网搜索走 `platform/websearch`。

### 5.5 admin（编排型上下文）

- 无自有实体；`app` 编排：用户处置（auth 端口）、内容审核删除（content/social 端口）、附件审核（media 端口）、预设/AI/记忆管理（ai/agent 端口）、后台任务（platform/jobs）、系统设置（platform/settings）、审计写入（platform/audit）。
- `repo/read_model.go`：**显式只读 read-model**，允许跨表 JOIN/聚合（审核列表、概览统计、审计列表）；只读、不写，且任何可见性/权限过滤由 app 传入条件，不在 read-model 里写业务规则。
- 超管授权/撤销的 handler 不放在这里（属于 auth 域），但路由仍位于 `/admin` 前缀下。

---

## 6. 超级管理员（已合并能力）的架构落位

超级管理员不是「多加一个 if」，而是 auth 域的一组不变式，需在 DB、domain、app、HTTP 策略、测试五处一致：

### 6.1 领域不变式

1. `role` 仍只有 `user/admin`；超管 = `role='admin' && is_super_admin=true`，不新增第三角色。
2. 全库最多一个超管（DB 部分唯一索引 `users_single_super_admin`，`migrations/0010_super_admin.sql:11-13`）；超管必须是管理员（`users_super_admin_check`，`:7-9`）。
3. 超管只能由空库首个注册用户在注册事务内产生；不提供授予/转让超管的 API。
4. AI 账号永远不是管理员/超管（`is_ai=true` 时 `is_super_admin=false`，且授权用例拒绝）。
5. 只有超管能授权/撤销管理员；操作者不能是自己，不能操作超管账号；目标已停用时不能授权（撤销可幂等）。
6. 角色/超管标记变更不吊销会话（`token_version` 不变）；由于每次请求 `ResolveSession` 都重新读 `users`，权限即时生效。

### 6.2 分层落位

| 层 | 内容 |
| --- | --- |
| DB | `0010_super_admin` 已落地：列、CHECK、单超管部分唯一索引；不新增超管接口/迁移 |
| `auth/domain` | `CanManageAdmins`/`CanChangeAdminRole`/`CanModerateUser`（纯函数，超管保护唯一实现）；`Bootstrap` 规则 |
| `auth/app` | `Register`（首注册事务+advisory lock）、`BootstrapRequired/BootstrapStatus`、`GrantAdmin/RevokeAdmin`（FOR UPDATE 加载 actor+target、校验、更新 role、audit 同事务、幂等不写审计） |
| `auth/repo` | `users` 查询包含 `is_super_admin`；`UpdateRole`；`CountHumanUsers/CountSuperAdmins`；`AcquireBootstrapLock` |
| `auth/http` | `RequireSuperAdmin` 中间件；`GrantAdmin/RevokeAdmin` handler；`view.PrivateUser` 含 `is_super_admin`、`PublicUser` 不含 |
| `router` | grant/revoke 两条路由使用 `SuperAdminWrite` 策略（CSRF 在超管校验之前，保持现有错误语义） |
| `admin` | 用户处置相关用例通过 `auth/app.UserAdmin` 端口，不能直接 UPDATE users；普通管理员不能处置超管账号由 auth 端口在事务内复核 |
| 启动/运维 | 迁移后由 `app/bootstrap.go` 调用 `auth/app.BootstrapStatus` 输出告警（空库/无超管），与现在 `cmd/server/main.go:131-144` 行为一致 |
| 测试 | 中间件角色矩阵（保留并扩展 `internal/auth/middleware_test.go`）；`TestBootstrapIntegration`/`TestAdminGrantIntegration` 迁到新的集成测试 harness，断言 API 行为不变 |

### 6.3 审查点

- 任何新管理接口都必须先问：普通管理员 / 超管 / 目标保护 三层是否都在 `shared/access` 与 app 用例中有唯一实现？
- `users` 表的写路径只有 auth repo；admin 域不得再出现 `UPDATE users`。
- 超管标记不得进入公开 DTO、日志、审计之外的地方（审计只记录 `previous_role/new_role`，不记录超管标记本身以外的敏感字段）。
- 升级已有部署没有超管时，授权接口不可用；系统通过启动日志告警，不允许「自动提升第一个管理员」的隐式迁移。

---

## 7. 数据访问与 SQL 规范

### 7.1 repo 规则

1. repo 是唯一含 SQL 的层；每个聚合一个 repo 文件，查询语句以 const/函数构建；动态 SQL 只拼静态片段与 `$n` 占位符。
2. repo 方法签名不出现 `pgx.Tx`/`access.Querier`；通过 `db.ExecutorFrom(ctx, pool)` 自动使用当前事务或连接池。
3. repo 不取时钟：写方法接收显式 `at time.Time`（或命令结构里的 `Now`），由 app 传入。
4. 返回 `shared/model` 结构；列清单与 Scan 只在 repo；不再向业务包导出 `PostColumns` 之类的 SQL 片段。
5. 批量载入使用 `id = ANY($1)` + app 内 map 合并；禁止列表页内的相关子查询含字符串函数/聚合。
6. 原子操作保留：`FOR UPDATE SKIP LOCKED` 领取、`ON CONFLICT` 幂等、条件状态迁移 `UPDATE ... WHERE status=... RETURNING`、原子计数自增；其余运算搬到 app。

### 7.2 事务模型

```go
// platform/db/tx.go
type Executor interface {
    Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
    Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
    QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type TxManager struct{ pool *pgxpool.Pool }

func (m *TxManager) WithTx(ctx context.Context, fn func(context.Context) error) error {
    if tx := txFrom(ctx); tx != nil { return fn(ctx) } // 嵌套合并：跨模块协作复用外层事务
    tx, err := m.pool.Begin(ctx)
    if err != nil { return err }
    ctx = withTx(ctx, tx)
    defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
    if err := fn(ctx); err != nil { return err }
    return tx.Commit(ctx)
}

func ExecutorFrom(ctx context.Context, pool *pgxpool.Pool) Executor
```

- app 用例是事务边界：注册初始化、发帖（帖子+草稿+版本+附件+标签+搜索+通知+作业）、评论/点赞（事件+计数+通知）、用户停用（auth+会话+jobs+agent 任务）、AI 动作、管理员授权（用户+审计）。
- 一个事务只取一次 `now := clock.Now()`，作为参数显式传给所有 repo 写方法；禁止 repo 内部再取时间。
- handler 不再出现 `pool.Begin/Commit/Rollback`；跨模块用例在 admin/agent app 用 `WithTx` 包裹并调用各模块 app 端口，ctx 传播保证同一事务。

### 7.3 时间与 SQL 函数

- 新增 `platform/clock`：`Clock.Now()`、`System()`、`Fake`、`DayStart`（固定 Asia/Shanghai）；所有 app 构造注入；`main.go` 构造 `clock.System()`。
- 所有写库时间显式传参：`created_at/updated_at/deleted_at/published_at/started_at/finished_at/run_at/resolved_at/last_used_at/suspended_at` 等。
- 时间比较全部参数化：会话过期、作业租约、AI 任务 reconcile、清理 cutoff、日统计窗口；禁止 `now()`、`CURRENT_DATE`、`created_at::date`、`interval` 运算。
- 首次发布语义：`published_at = COALESCE(published_at, $n)`（同一事务同一 now）。
- 日期列 `ai_action_counters.counter_date` 与日界范围由 `clock.DayStart` 生成；`TZ=UTC` 下测试也应通过。
- 2026 年 10 月合并后的现状：43 个 `DEFAULT now()`、Go SQL 中 `now()` 123 次（写 115 / 查询条件 7 / 迁移器 DDL 1，含 super-admin 新代码 `admin/admins.go` 等），改造完成后运行期 Go 代码中应为 0。
- DB 默认值处理：全部写路径显式后，新增迁移 `0011_drop_time_defaults.sql` 做 43 条 `ALTER COLUMN ... DROP DEFAULT`；`timeline.schema_migrations.applied_at` 保留为唯一白名单；`0010` 新增的 `is_super_admin DEFAULT FALSE` 是布尔默认值，保留。
- 多实例时钟：当前单实例部署；DEPLOYMENT 文档补充 NTP/时钟同步要求，未来多实例再评估 DB 授时。

### 7.4 SQL 简化与索引

**运算下沉应用侧的清单（与 v1 findings 一致，按目标结构重排）**：

| 现状 | 目标 |
| --- | --- |
| `GREATEST(count ± delta, 0)` 5 处 | repo 原子 `UPDATE ... SET count = count ± $1 ... RETURNING`，去掉夹取；app 保证 delta 只在状态翻转时产生；新增计数对账周期任务；脏数据以 CHECK 约束暴露 |
| FollowState 四态 CASE SQL 两份 | 删除 SQL CASE；查 `follows`/`follow_requests` 三个布尔后在 `shared/access` 纯函数分类，或复用同一 `FollowState` app 用例 |
| `created_at::date = CURRENT_DATE` | app 计算 `[dayStart, dayEnd)` 参数化；同时统一「今日动作数」口径（建议 `ai_action_counters`） |
| `suspended_at = CASE WHEN ... THEN now() ELSE NULL` | app 传 `*time.Time` |
| 嵌套 `COALESCE(NULLIF(a,''), COALESCE(b,''))` 业务回退 | repo 返回原始列，app/view 选择值 |
| `COALESCE(p.is_enabled, FALSE)` 过滤 | `p.is_enabled IS TRUE` 或 app 过滤 |
| 头像清理 SQL 拼 `/api/v1/attachments/...` 字符串 | 新增 `users.avatar_attachment_id` 列（结构迁移，不回填；app 双写 + 背景回填任务），清理改等值条件 |
| 相关子查询含 `STRPOS/LOWER`、每行 `COUNT/array_agg` | app 批量 `id = ANY($1)` + Go map；admin read-model 的统计例外保留 |
| 标签页相关 EXISTS | 从 `post_tags(tag_id, post_id)` 反驱动 JOIN posts |
| `($n = 0 OR id < $n)` 参数化 OR | 首页/续页两条固定 SQL |
| `ORDER BY ... NULLS LAST` 与索引不符 | 去掉 NULLS LAST（`published_at` 受 CHECK 约束非空） |
| `COUNT(DISTINCT p.id)` | `COUNT(*)`（`post_tags` 主键保证唯一） |
| `to_jsonb($2::boolean)` | repo 接收 `[]byte`/`json.RawMessage`，app 侧 `json.Marshal` 后 `$n::jsonb` |
| 动态 SQL 20+ 构造器 | 保留必要 builder（时间线/列表），全部在 repo 且有纯函数单测；管理后台过滤 ≤5 个条件的查询可保留通用 builder，超出拆专用查询 |

**保留在 SQL 的原子/分页操作**：`FOR UPDATE SKIP LOCKED`、`ON CONFLICT` 幂等、条件状态迁移、原子计数自增、逻辑删除条件 UPDATE、列表分页的可见性过滤（分页正确性）、事务内唯一性/引用检查。

**搜索方案（结构层决策）**：

- 迁移 `0014_search_trgm.sql`：`CREATE EXTENSION IF NOT EXISTS pg_trgm` + `post_search` 上 `GIN (LOWER(plain_text) gin_trgm_ops)`；查询改为 `LOWER(plain_text) LIKE '%' || $1 || '%'`（`STRPOS` 不吃 trigram 索引）。
- 用户/昵称/评论/附件/预设/记忆等后台搜索：统一在 app 侧小写参数 + repo 专用查询；无索引的后台搜索必须在接口注释和文档中声明数据量上限，禁止无上限全表 `STRPOS(LOWER())`。
- 中文全文检索不引入 `zhparser` 等外部扩展；trigram 是本项目最现实的子串方案。

**索引/迁移计划（0010 之后编号；迁移器升级见 §8）**：

| 迁移 | 内容 | 说明 |
| --- | --- | --- |
| `0011_drop_time_defaults.sql` | 43 条 `ALTER COLUMN ... DROP DEFAULT` | 时间改造收尾；单语句兼容现有分句器 |
| `0012_add_avatar_attachment_id.sql` | `users.avatar_attachment_id BIGINT` + 索引 | **只结构不回填**（遵守 DATA_MODEL §4.5）；app 双写 + 背景回填任务；回填完成后清理 SQL 才依赖新列 |
| `0013_timeline_and_task_indexes.sql` | `reposts(created_at DESC,id DESC) WHERE is_active`、`ai_tasks(ai_id,created_at) WHERE status='succeeded'`、`ai_call_logs(created_at,id) WHERE NOT is_deleted`、`attachments(status) WHERE status='deleted'` | 大表用 `no-transaction` 迁移 + `CREATE INDEX CONCURRENTLY` |
| `0014_search_trgm.sql` | `pg_trgm` 扩展 + `post_search` trigram GIN | 需要 DB 扩展权限；扩展创建可在普通事务迁移，索引可用 CONCURRENTLY |
| `0015_drop_unused_indexes.sql` | 删 `post_search_updated` | 写放大；删除前确认无查询依赖 |

### 7.5 迁移器 v2（结构改造的前置基础设施）

现状 `internal/migrate/migrate.go:105-121` 把每个迁移文件包在事务里执行，因此 `CREATE INDEX CONCURRENTLY` 无法使用。目标：

- 迁移文件首行支持指令注释：`-- timeline:no-transaction`；该文件的所有语句在连接上直接执行（不包事务），成功后单独记录版本；要求此类迁移幂等、单语句、失败可手工恢复。
- 普通迁移保持事务语义；迁移器继续按 `;` 分句（不引入复合语句），并在文档中说明 no-transaction 迁移的评审规则。
- 该升级在时间/索引迁移之前完成，属于 `platform/migrate` 的单独 PR。

---

## 8. 组合根与启动流程

```go
// internal/app/app.go
type App struct {
    server   *server.Server
    runner   *jobs.Runner
    readiness *db.Readiness
    close    func()
}

func New(cfg config.Config, log *slog.Logger) (*App, error) {
    clk := clock.System()
    ids := id.New(...)
    pool := db.Wait(...)                 // 或由 main 后台连接后注入 Readiness
    tx := db.NewTxManager(pool)
    auditW := audit.NewWriter(pool, ids)
    jobsR := jobs.NewRunner(pool, ids, clk, log)
    settingsR := settings.NewStore(pool)

    authApp := authapp.New(authrepo.NewUserRepo(pool), authrepo.NewSessionRepo(pool),
        settingsR, auditW, clk, ids, log, cfg.CookieSecure)
    contentApp := contentapp.New(contentrepo.New(...), tx, clk, ids, log, jobsR, ...)
    // … social / notice / media / ai / agent / admin，全部通过接口注入

    handlers := router.Deps{Auth: authhttp.New(authApp), Content: contenthttp.New(contentApp), ...}
    srv := server.New(cfg, log, readiness)
    router.Register(srv.Engine(), handlers, readiness)
    registerJobs(jobsR, contentApp, mediaApp, agentApp)
    return &App{server: srv, runner: jobsR, ...}, nil
}
```

`cmd/server/main.go` 最终只保留：`time.Local` 设定 → `config.Load` → `logging.Init` → 信号 ctx → `app.New` → 启动 HTTP → 后台 `db.Wait`/`migrate`/`MarkMigrationsDone` → `app.Bootstrap()`（初始化状态日志）→ `runner.Start` → 优雅关闭；`migrate` 子命令保留，`init-admin` 已移除。

---

## 9. 测试体系

| 层 | 测试方式 |
| --- | --- |
| `shared/access`、`domain`、纯函数 | 表驱动单测，不依赖 DB/Gin |
| `app` | 内存 fake repo（实现 `app/port.go` 接口）+ `clock.Fake` + fake `TxManager`（记录 Begin/Commit/Rollback）+ fake audit/jobs/notifier 端口 |
| `repo` | 真库集成测试（沿用 `TIMELINE_INTEGRATION=1` 隔离 schema）：SQL 结果、Scan、分页、`FOR UPDATE`、`ON CONFLICT`、明确时间参数 |
| `http` | handler + fake app 接口 + `httptest`：状态码/错误码/JSON 形状/权限上下文 |
| `transport/router` | 无 DB 路由矩阵测试：110 条、Policy 约束、CSRF/Admin/SuperAdmin 覆盖 |
| 全链路 | 现有 `internal/server/*_integration_test.go` 改为基于新 `app` 的测试 harness（`internal/testkit`），覆盖账号/内容/管理/AI/引导/授权；行为断言保持 |
| 架构 | `internal/arch` 静态守卫（§2.3）；CI 默认执行 |
| 时间 | `clock.Fake` 断言 `expires_at/published_at/updated_at/run_at`；`TZ=UTC` 跑日界测试；迁移后 `information_schema` 默认值检查 |
| 手工 | `docs/ACCEPTANCE.md` P0/SEC 全量，新增超管授权矩阵与初始化窗口用例 |

测试数据与 schema 清理仍遵守「无物理 DELETE」约束（集成测试清理方式保持现状）。

---

## 10. 迁移序列（结构门禁，不按工作量排序）

目标结构一次性成型，但落地按门禁推进；每道门禁都必须可编译、可回滚：

| Gate | 内容 | 完成条件 |
| --- | --- | --- |
| G0 基线冻结 | 新建分支；记录 110 条路由快照、API 错误矩阵、DB schema 快照；跑通现有 `go test ./...`、`go vet ./...`、`npm run build`、5 组集成测试；新增 `internal/arch` 守卫框架与路由快照测试 | 基线全绿；arch 规则以「现状白名单」形式先通过，随后逐步收紧 |
| G1 平台与共享内核 | 新建 `platform/*`（clock/db/apperr/jobs/audit/settings/migrate v2…）与 `shared/*`；旧代码暂不动 | 新包单测通过；旧代码仍可编译运行 |
| G2 auth 垂直切片 | 按目标结构实现 auth 域（含超级管理员引导/授权），router 策略 skeleton 先接管 `/auth/*`、`/users/me*`；旧 auth handler 暂留作回退 | auth 路由行为/错误码与基线一致；中间件角色矩阵、引导/授权集成测试通过 |
| G3 content → social → notice | content app/repo/http + 时间线 builder；social；notice 端口化；router 逐域切换 | 每域集成测试通过；SQL 全部在 repo；时间显式传参 |
| G4 media → ai → agent → admin | 剩余模块迁移；admin 编排 + read-model；超管保护在 auth 端口复核；jobs/audit/settings 端口化 | 全部域集成测试通过；旧 service handler 方法逐批删除 |
| G5 路由与 server 收口 | 110 条路由全部由声明式路由表注册；删除 `internal/server` 中的业务路由、`apiServices`、旧 service 构造；`server.New` 只做引擎；`app.New` 成为唯一装配点 | 路由快照/策略测试通过；`*gin.Context`/SQL/`pool.Begin` 结构断言通过 |
| G6 时间收尾 | 全量写路径显式传时间（新代码天然如此）；删除旧 `now()`/`::date`/`interval`；新增 `0011_drop_time_defaults.sql`；测试 INSERT 补参；静态 grep 归零 | `information_schema` 除白名单外无 `now()` 默认值；集成测试全绿 |
| G7 SQL 简化与索引 | 计数去夹取 + 对账任务；时间线 SQL 变体；`avatar_attachment_id` 双写/回填/切换；trigram 搜索；`0012-0015` 迁移 | `EXPLAIN` 对比达标；搜索/时间线/清理延迟不劣化；对账差异 0 |
| G8 收尾 | `internal/arch` 全量收紧；删除临时 legacy 适配；文档同步；P0/SEC 手工回归 | 架构黑名单清零；docs/ARCHITECTURE.md 成为权威；验收记录归档 |

**过渡期约束**：G1–G4 允许「新模块 + 旧 handler 并存」，但旧代码只能被删除、不能被扩展；router 中旧路由集中放在临时 `routes_legacy.go`，G5 必须删掉。任何新功能（包括后续超级管理员相关接口）只允许写在目标结构里。

---

## 11. 文档同步清单

| 文档/文件 | 变更 |
| --- | --- |
| **新增 `docs/ARCHITECTURE.md`** | 目标架构权威文档：分层图、模块职责、数据所有权、端口清单、事务/错误/时间规范、架构守卫规则、迁移状态 |
| `docs/README.md` | 索引加入 ARCHITECTURE.md，更新版本/日期 |
| `AGENTS.md` §3/§5 | 目录清单加入 `internal/app`、`platform/`、`shared/`、`transport/`、`modules/*`；代码约定改为「handler 不写 SQL、app/domain 不 import gin、repo 唯一 SQL、模块间只走端口、时间由应用传入、schema_migrations.applied_at 白名单」；同步超级管理员约定 |
| `docs/DATA_MODEL.md` §4.4（`:78` 当前写「保证 `now()` 等数据库函数与应用时间一致」） | 改为「时区仍 Asia/Shanghai；写库时间由应用生成传参，DB 不提供业务时间默认值、不承担业务时间比较；DATE/日界在应用侧按北京时间计算」；§6.1 超管规则保持不变 |
| `docs/REQUIREMENTS.md` 时间基准/数据库会话时区 | 保留时区约定，补充「业务时间权威在应用侧」；超级管理员需求不回归 |
| `docs/WORKFLOWS.md` 时间/事务章节 | 补充「一次事务一个 now」「作业租约/日界/退避均由应用时钟计算」；事务边界归 app |
| `docs/API.md` | **API 契约不变**；仅更新实现说明（错误映射位置、CB 策略）；超管接口已存在，路径/字段/错误码不变 |
| `docs/PAGES_AND_PERMISSIONS.md` | 授权实现位置改为「路由 Policy + shared/access + app 端口」；超管权限矩阵行为不变 |
| `docs/DEPLOYMENT.md`（`:23`、`:137`） | 新增 NTP/时钟同步要求（应用时钟是业务时间权威）；补充 `no-transaction` 迁移与 `CREATE INDEX CONCURRENTLY` 的执行方式、扩展权限申请、迁移回滚策略 |
| `docs/ACCEPTANCE.md` ENV-03（`:57`） | 时间验修改为核对 API 返回时间与服务器时钟（+08:00）；`SELECT now()` 不再作为业务时间权威；新增/保留超管 AUTH-07/08、ADMIN-11～14、SEC-09 用例 |
| `.github/workflows/ci.yml` | 增加 `go test ./internal/arch`（或随 `go test ./...` 自动执行）；可选增加关键集成测试任务（CSRF/引导/授权） |
| `internal/content/service.go` 顶部注释（现写「查询与时间比较全部在 SQL 内完成」） | 改为「写时间由应用传入；日期/租约等比较在应用侧完成；分页可见性过滤保留在 SQL」 |

---

## 12. 验收 DoD（结构不变量）

改造完成的判定不是「编译通过」，而是下列不变量同时成立：

1. `*gin.Context` 只出现在 `modules/*/http/**`、`transport/**`、`server`；业务包为 0。
2. SQL 关键字只出现在 `modules/*/repo/**`、平台 repo（jobs/audit/settings）、`internal/migrate`；其余 Go 源码为 0。
3. `access.Querier`、`PoolProvider`、`internalError(c)`、`c.JSON` 在 app/domain/repo 中为 0。
4. `pool.Begin/Commit/Rollback` 只在 `platform/db`（jobs Runner 内部例外）；handler 为 0。
5. `time.Now()` 只在 `platform/clock/id/logging` 与 access log；SQL 中 `now()/CURRENT_DATE/::date/interval` 除迁移器白名单为 0。
6. `modules/x` 不 import `modules/y`（arch 测试断言）。
7. 路由快照 == 110 条（GET 45 / POST 65），`docs/API.md` 的 method/path/状态码/错误码/JSON 字段与改造前一致。
8. 策略矩阵断言：非 GET 必属写策略；`/admin` 必属 Admin/SuperAdmin 策略；grant/revoke 必属 `SuperAdminWrite`；缺 CSRF/缺登录/普通管理员访问超管接口的错误码与基线一致。
9. 时间不变量：会话过期/作业租约/AI reconcile/日统计全部使用应用时钟；`information_schema` 无业务 `now()` 默认值；`TZ=UTC` 测试通过。
10. 计数对账差异为 0；`EXPLAIN` 对比：时间线首页/搜索/通知未读/概览/作业领取不劣化，深分页不再线性变慢。
11. `go test ./...`、`go vet ./...`、`go test ./internal/arch`、`npm run build`、5 组集成测试、mock-llm 冒烟全绿；`docs/ACCEPTANCE.md` P0/SEC 全量通过。
12. `docs/ARCHITECTURE.md`、`AGENTS.md`、DATA_MODEL/WORKFLOWS/DEPLOYMENT/ACCEPTANCE 已同步，版本与变更记录更新。

---

## 附录 A：现有代码 → 目标位置映射

| 现状 | 目标 |
| --- | --- |
| `internal/api` | `transport/httpx`（JSON/错误响应/ID/时间/分页）+ `platform/apperr`（Failure 类型与构造） |
| `internal/model` | `shared/model`（行/枚举/纯判断）；JSON DTO 移到各 `modules/*/http/view.go` |
| `internal/access` | `shared/access`（纯策略）；关系/状态数据通过 app 端口加载 |
| `internal/auth/*` | `modules/auth/{domain,app,repo,http}`；`bootstrap.go` 归 app/repo；`init_admin.go` 已删除 |
| `internal/admin/admins.go`（超管授权） | `modules/auth/app/admin_roles.go` + `modules/auth/http/admin_role_handler.go`；`users` 写入归 auth repo |
| `internal/admin/*` | `modules/admin/{app,repo,http}`；跨模块写改端口；`repo/read_model.go` 只读 |
| `internal/audit` | `platform/audit`（Writer/Repo，写 `admin_audit_logs`） |
| `internal/jobs` | `platform/jobs`（Runner/Queue/Repo，写 `background_jobs`） |
| `internal/settings` | `platform/settings`（Values/Keys/Reader/Writer + PG repo） |
| `internal/jobs` 周期任务注册 | `app/jobs.go` |
| `internal/server/server.go` 路由 | `transport/router/*`（按领域拆文件，声明式） |
| `internal/server` 服务构造/作业注册 | `app/wire.go` / `app/jobs.go` |
| `internal/server` 全局中间件/健康/前端 | `transport/middleware` + `server`（engine/health/frontend） |
| `cmd/server/main.go` 业务装配 | `internal/app`；main 只保留进程生命周期与 `migrate` |
| `internal/migrate` | `platform/migrate`（增加 `no-transaction` 指令） |
| `internal/websearch` | `platform/websearch` |
| `internal/storage`、`internal/id`、`internal/config`、`internal/logging`、`internal/ratelimit`、`internal/richtext` | `platform/*` 或 `shared/richtext` |
| `internal/content`、`internal/social`、`internal/notice`、`internal/media`、`internal/ai`、`internal/agent` | `modules/<同名>/{domain,app,repo,http}` |

## 附录 B：基线数据（b6fed97）

| 指标 | 数值 |
| --- | --- |
| `/api/v1` 路由 | 110（GET 45 / POST 65；public 20 / secured 37 / admin 53） |
| 处理函数 | 166 个 `func (s *Service) Xxx(c *gin.Context)` |
| 业务包非测试代码 | 68 文件 / 22,400 行 |
| `*gin.Context`（非测试 Go） | 195 处 |
| `pool.Begin` | 25 处（含 super-admin 新代码） |
| `access.Querier` 形参 | 163 处 |
| 迁移时间列 / `DEFAULT now()` | 55 列 / 43 列（`0010_super_admin` 只加布尔列，不影响时间列） |
| Go SQL `now()` | 123 处（含 `admin/admins.go` 新代码），`time.Now()` 30 处 |
| 既有超管实现 | `migrations/0010_super_admin.sql`、`auth/bootstrap.go`、`auth/middleware.go:156 RequireSuperAdmin`、`admin/admins.go`、`server.go` grant/revoke 路由、`middleware_test.go`、`bootstrap_integration_test.go`、`admin_grant_integration_test.go` |
| 现有测试文件 | 35 个（含 5 组集成测试） |

## 附录 C：本设计明确不做的事

- 不拆微服务、不引入消息队列/事件总线（模块内端口 + 事务已足够；后台作业仍用 `background_jobs`）。
- 不引入 ORM；repo 继续手写 pgx，保持 SQL 可控。
- 不改变 API 契约、错误码、权限语义、时区基准、逻辑删除与无外键约束等红线。
- 不把前端纳入本次结构改造（仅 `npm run build` 回归）。
- 不为「跨模块只读列表」引入 CQRS 读库；admin read-model 作为显式例外，保持单库。
- `init-admin` 已删除，不恢复；初始化仍通过空库首个注册完成。
