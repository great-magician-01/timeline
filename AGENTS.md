# Timeline 项目指南

> **文档版本**：0.2.7  
> **最近更新**：2026-10-21  
> **关联文档**：[`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)（后端目标架构权威）、[`docs/README.md`](docs/README.md)（文档索引）

本文件面向首次接触本仓库的 AI 编码代理。项目文档、代码注释和界面文案主要使用简体中文；修改代码和文档时沿用这一语言习惯。本文区分**当前已实现的行为**与**文档中的目标设计**，不要把设计清单误当成已经完成的功能。

## 1. 项目概览

Timeline 是一个支持长文、多附件、分级可见性、完整社交互动，以及具有独立身份和分层记忆的 AI 用户的时间线社区。目标界面为简体中文响应式 Web，部署形态是单个 Go 应用托管 Vue 前端并访问独立 PostgreSQL 实例。

当前仓库处于 **V1 已实现、待人工验证** 阶段，功能范围已全部落地：

- Go/Gin HTTP 服务、`/healthz` 存活检查、`/readyz` 就绪检查，以及前端构建产物的 Go 内嵌托管和 SPA 回退。
- PostgreSQL 连接、失败退避重试、最小迁移器 v2（支持 `no-transaction` 指令）；`migrations/` 内按文件名顺序追加的迁移（`0001`–`0023`，含账号、超级管理员标记、内容、社交、AI、后台作业、AI 记忆来源、时间默认值移除、并发索引与 `pg_trgm` 搜索索引，以及 SQL 最简原则改造的查询索引批次、冗余索引删除、`posts/reposts.author_is_ai` 作者类型快照列、`attachment_usage` 配额冗余计数表、私信会话/消息与 `users.dm_policy`、个人主页置顶 `pinned_at`/`pinned_sort`、收藏 `bookmarks`/`bookmark_folders` 两表与配套索引等表结构变更）。
- 账号：用户名/邮箱注册与登录、bcrypt 哈希、HttpOnly 服务端会话、游客与会话 CSRF、三维登录限频、资料与密码修改、关注审批设置、头像上传；公开 `GET /auth/config` 提供注册开关状态与 `bootstrap_required` 初始化状态。空库首个注册用户自动成为唯一超级管理员；超级管理员可授权/撤销管理员，普通管理员不能操作超管账号。
- 内容：富文本/Markdown 统一安全渲染、帖子、草稿与自动保存冲突、定时发布、编辑历史与版本恢复、附件上传与图片缩略图/视频封面（ffmpeg 可选）、可见性（公开/仅关注者/仅自己）、时间线（全站/关注/个人主页）、个人主页置顶（作者置顶自己的已发布帖子到主页顶部，最多 3 条、支持调整顺序，普通流排除置顶帖）。
- 社交：关注与关注审批、移除粉丝、点赞、评论与楼中楼、纯转发与引用、提及、收藏（仅帖子的私有阅读清单，支持收藏夹分组：每人 ≤20 个、删除夹时夹内收藏归还默认收藏；列表按访问者可见性过滤；AI 与停用账号不能收藏；不发通知、不展示计数）、通知中心（轮询未读数、按类型筛选）。
- 私信：1:1 双人会话（纯文本无附件，正文上限 2000 字、来源体积上限 64KiB）、用户级接收策略 `dm_policy`（所有人/仅我关注的/仅互关/关闭，默认所有人）、未读数轮询徽章、会话列表与对话页、2 分钟内撤回、删除会话视图（单侧隐藏）；AI 账号双向不参与，管理后台完全不可见。
- 发现：`#话题#` 链接化与标签聚合、用户搜索、帖子搜索，全部按访问者权限过滤。
- AI：OpenAI 兼容接口预设（API Key 加密存储）、AI 身份与行为策略（含发帖/主动互动间隔）、分层记忆（近期/长期、关键词检索、沉淀与失效、来源关联）、自主动作（发帖/评论/点赞/关注/转发/引用，默认全部关闭）、按 AI 开关的受控联网搜索（全局独立密钥、默认关闭、有限工具轮次）、任务与决策审计、模型调用记录（可按任务筛选）、管理员手动生成支持立即发布或定时发布、任务详情展示注入记忆。
- 管理后台：概览、用户管理（含超级管理员授权/撤销管理员）、内容审核（帖子/评论/附件）、模型预设、AI 配置与记忆、生成与发布、全局任务、调用记录、审计日志、系统设置（注册、全局独立网页搜索 Base URL/API Key、上传/请求大小、存储配额、发帖/上传限频、临时附件保留期、调用记录截断/保留期）与全局 AI 开关。
- 后台作业：持久化在 `background_jobs`，重启恢复，有限次数与退避重试；附件失效清理与 AI 调度为周期任务。
- 环境变量配置、双通道日志、雪花 ID 生成器、Docker/Compose、目录准备与备份恢复脚本，以及 GitHub Actions CI。

验证状态：默认 Go 测试、`go vet`、`npm run build`、架构守卫（`go test ./internal/architecture`）与隔离 schema 集成测试已通过；私信功能补充的 domain/app 测试与 `TestDMIntegration`（发送/未读/已读/撤回/隐藏恢复/策略矩阵/AI 与停用拒绝/非成员 404/设置往返），置顶功能的 domain/app/repo 单测与 `TestPinIntegration`（置顶/取消/幂等/上限冲突/排序/权限/主页去重/可见性/删除联动），以及收藏功能的 domain/app/repo/http 单测与 `TestBookmarkIntegration`（收藏/取消/幂等、`viewer_state.bookmarked`、收藏夹 CRUD 与上限/属主 404、移动、删除夹归还默认、游客/停用拒绝、列表可见性过滤、后台无收藏路由）随 `go test ./...` 与 `TIMELINE_INTEGRATION=1 go test -p 1 ./...` 运行，迁移 `0021_direct_messages`、`0022_post_pinned`、`0023_bookmarks` 已在开发库实际应用。开发机已使用临时 PostgreSQL 实例完成 Docker 镜像构建、容器启动、健康检查、前端托管与基础 API 冒烟，并完成一次备份/恢复演练（含附件属主修正）。尚未执行：`docs/ACCEPTANCE.md` 中的完整 P0 人工验收（含本轮 DM-01～12、SEC-10/11 与 BK-01～07），以及部署机上的真实环境复演。

当前分支 `refactor/target-architecture` 已按 `docs/ARCHITECTURE.md` 完成目标架构接管与旧包清理：`cmd/server` → `internal/app`（组合根）→ `transport/router`（声明式路由）→ `modules/*`；旧 `internal/*` 业务包已从仓库删除，集成测试迁移到 `internal/testkit` + `internal/server`/模块 `http` harness，架构守卫覆盖新树、`internal/server`、`cmd/` 与 `internal/testkit` 例外，并新增 DB 原语/遗留符号规则；新功能只写在目标结构中。改造过程与迁移序列见 `CODE_QUALITY_REFACTOR_PLAN.md`（过程文档，非长期权威）。

## 2. 技术栈与关键配置

- 后端：Go 1.25（以 `go.mod` 为准），模块名 `github.com/great-magician-01/timeline`。
- HTTP：Gin；数据库：PostgreSQL，通过 `pgx/v5/pgxpool` 连接。
- 日志：标准库 `log/slog`，同时写标准输出和按日期分目录的文件；`ERROR` 级别日志自动附加完整调用栈（text 为记录行后的缩进多行块、json 为 `stack` 字段），`WARN`/`INFO` 与访问日志不附加，调用方显式传入的 stack 不重复采集；HTTP 请求 panic 由 `transport/middleware.Recovery` 恢复并记录完整栈、统一返回 500，后台作业/周期任务 panic 同样记录完整栈。
- 前端：Vue 3、TypeScript、Vite、Tailwind CSS 4（`@tailwindcss/vite`），使用 Node.js 22+、npm 和 `web/package-lock.json`（版本要求见根目录 `README.md`）。
- 前端构建：`vue-tsc -b && vite build`，类型检查和生产构建由同一个 `npm run build` 完成。
- 静态资源：`web/embed.go` 使用 `go:embed all:dist` 嵌入 `web/dist`。
- 数据库迁移：`migrations/embed.go` 嵌入 `migrations/*.sql`。
- 本地配置：`.env.example` 是模板；`internal/platform/config` 会读取根目录 `.env`，但已有进程环境变量优先。

关键文件：

- `go.mod` / `go.sum`：Go 版本与依赖。
- `web/package.json`：前端脚本与依赖；目前没有 `test`、`lint` 或 `format` 脚本。
- `web/vite.config.ts`：Vue 与 Tailwind 插件；开发代理将 `/api`、`/healthz`、`/readyz` 转发到 `http://localhost:5070`。
- `web/tsconfig*.json`：TypeScript 项目引用和检查规则，包括未使用变量/参数检查。
- `.env.example`：部署配置模板，真实凭据不得写入或提交。
- `Dockerfile` / `docker-compose.yml`：多阶段镜像与单应用 Compose（外部 PostgreSQL）。
- `.github/workflows/ci.yml`：CI（Go 测试/静态检查、前端构建、镜像构建）；不启动容器。

## 3. 目录与模块划分

目标架构（`refactor/target-architecture` 分支，权威说明见 [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)）：

- `internal/app/`：组合根（`app.go`/`wire.go` 装配，`readers.go` 跨模块只读适配，`adapters.go` 平台端口适配，`jobs.go` 作业处理器与周期任务；迁移后的初始化状态日志 `Bootstrap` 在 `app.go`）。唯一装配全部平台依赖、跨模块端口与作业处理器并调用路由注册的地方。
- `internal/platform/`：技术基座（无业务语义）。`clock`（唯一取时来源）、`db`（连接池与事务 `TxManager.WithTx`）、`id`（雪花 ID）、`logging`、`migrate`（迁移器 v2，支持 `no-transaction` 指令）、`storage`、`ratelimit`、`apperr`（领域错误 Kind/构造器）、`jobs`/`audit`/`settings`（分别拥有 `background_jobs`/`admin_audit_logs`/`system_settings`）、`websearch`、`aiclient`、`config`。
- `internal/shared/`：无 IO 的共享内核。`model`（跨上下文行结构与枚举）、`access`（可见性/互动/角色/超管保护纯策略）、`contracts`（跨模块发布语言：端口接口与输入结构）、`page`（游标分页）、`richtext`（富文本/Markdown 清洗与链接化）、`format`（雪花 ID/北京时间/附件 URL 纯格式化）、`view`（跨模块复用的响应 DTO 与构造辅助）。
- `internal/transport/`：`httpx`（JSON 错误映射、请求解码、ID/时间/分页、viewer/session 上下文）、`middleware`（Ready/Recovery/Security/AccessLog/RequestID）、`router`（唯一注册路由处：Policy 枚举 + 声明式路由表）。
- `internal/server/`：slim server，只做 Gin 引擎与全局中间件装配、健康检查与前端静态托管；业务路由不在这里注册，由组合根在 `Engine()` 上调用 `transport/router` 注册。
- `internal/modules/<域>/{domain,app,repo,http}/`：模块四层。域为 auth、content、social、notice、dm、media、ai、agent、admin。`domain` 放实体不变式/状态机/纯校验；`app` 放用例、事务边界、取时与端口调用；`repo` 是唯一 SQL 层；`http` 放 Gin handler、参数解析、错误映射与 DTO 组装。
- `internal/architecture/`：架构守卫测试（`go test ./internal/architecture`），静态扫描新树、`internal/server`、`internal/transport/router` 与 `cmd/`，并对 `internal/testkit` 做显式例外；越界在 CI 失败。
- `internal/testkit/`：集成测试脚手架，构造隔离测试 schema、真实组合根服务与带 Cookie/CSRF 的 HTTP 客户端，供 `internal/server` 与模块 `http` 集成测试复用。

- `cmd/server/main.go`：唯一服务入口。只负责固定 `Asia/Shanghai` 时区、加载配置、初始化日志/ID/存储、HTTP 先启动、数据库就绪后切换为组合根生成的完整 handler、优雅关闭与 `migrate` 子命令；业务装配全部在 `internal/app`，不在 main 中构造业务服务。

**旧包位置映射**：旧 `internal/*` 业务包已在目标架构接管运行路径后从仓库删除（需要对照历史实现时用 `git show b6fed97:<path>`）；新代码不要再引用这些路径：

| 旧包（已删除） | 新位置 |
| --- | --- |
| `internal/api` | `internal/transport/httpx`（JSON 错误/解码/分页/上下文）+ `internal/platform/apperr`（领域错误）+ `internal/shared/format`（ID/时间/URL） |
| `internal/access` | `internal/shared/access` |
| `internal/admin` | `internal/modules/admin` |
| `internal/agent` | `internal/modules/agent` + `internal/platform/aiclient`/`internal/platform/websearch` |
| `internal/ai` | `internal/modules/ai` + `internal/platform/aiclient` |
| `internal/audit` | `internal/platform/audit` |
| `internal/auth` | `internal/modules/auth` |
| `internal/config` | `internal/platform/config` |
| `internal/content` | `internal/modules/content` |
| `internal/db` | `internal/platform/db` |
| `internal/id` | `internal/platform/id` |
| `internal/jobs` | `internal/platform/jobs` |
| `internal/logging` | `internal/platform/logging` |
| `internal/media` | `internal/modules/media` |
| `internal/migrate` | `internal/platform/migrate` |
| `internal/model` | `internal/shared/model` + `internal/shared/view` |
| `internal/notice` | `internal/modules/notice` |
| `internal/ratelimit` | `internal/platform/ratelimit` |
| `internal/richtext` | `internal/shared/richtext` |
| `internal/settings` | `internal/platform/settings` |
| `internal/social` | `internal/modules/social` |
| `internal/storage` | `internal/platform/storage` |
| `internal/websearch` | `internal/platform/websearch` |
| 旧 `internal/server`（业务服务 + 路由装配 + 集成测试宿主） | slim `internal/server`（Gin 引擎/全局中间件/健康检查/静态托管）+ `internal/app`（组合根）+ `internal/transport/router`（路由表）+ `internal/testkit`（集成测试脚手架） |

其它目录：

- `migrations/`：追加式 SQL 迁移及其 embed 包；执行方式与评审规则见 `migrations/README.md`。
- `web/src/`：Vue 入口、路由、认证与通知状态、API 模块、页面组件和全局设计令牌。
- `web/public/`：直接复制到构建产物的静态文件，包括 favicon。
- `web/dist/`：Vite 生成物，由 Go 嵌入；不要手工编辑。
- `docs/`：需求、页面权限、状态机、数据模型、API、部署、验收和设计系统的权威设计文档。
- `demo/`：未接后端的静态视觉方案；`home-e-social.html` 是选定方向，`logo.svg` 是品牌图源。实现样式以 `docs/DESIGN_SYSTEM.md` 为准，而不是以 demo 为准。
- `deploy/scripts/`：`prepare.sh`（创建并授权数据目录）、`backup.sh`（数据库 + 附件 + 加密密钥）、`restore.sh`（数据库 + 附件配套恢复）、`check-time-defaults.sql`（只读核查业务时间默认值，用法见 `docs/DEPLOYMENT.md`）。
- `.github/workflows/ci.yml`：CI 流水线（Go 测试/`vet`、前端 `npm ci` + 构建、镜像构建）。
- `bin/`：本地编译产物，不是源码。

## 4. 构建、运行与检查

以下 Go 命令均从仓库根目录执行；前端命令在 `web/` 目录执行。

### 前端

```bash
cd web
npm ci
npm run dev       # Vite 开发服务器，默认地址由 Vite 输出
npm run build     # TypeScript 检查 + 生产构建到 web/dist
npm run preview   # 预览已构建前端
```

开发时同时启动 Go 服务，Vite 才能代理健康和 API 请求。要让 Go 服务托管最新前端，必须先执行 `npm run build`，再重新构建并重启 Go 服务（`go run` 会在启动时重新编译）；仅修改 `web/src` 或只重启旧二进制都不会更新其中的嵌入资源。

### 后端

```bash
go test ./...
go vet ./...
go build -o bin/timeline.exe ./cmd/server   # Windows
# Linux/macOS 可输出为：go build -o bin/timeline ./cmd/server
go run ./cmd/server
go run ./cmd/server migrate       # 显式执行迁移（唯一的子命令）
go run ./cmd/mock-llm             # 验收用本地 OpenAI 兼容 mock（默认 127.0.0.1:8099，非生产）
```

`cmd/server` 只保留 `migrate` 子命令：不再提供 `init-admin`，首个管理员通过注册页的首个注册账号自动产生（空库环境）。

集成测试通过 `internal/testkit` 脚手架构造隔离测试 schema（使用现有开发实例中的 `timeline_test` 与 `user_test`，不创建数据库，也不写正式业务表）、真实组合根服务与带 Cookie/CSRF 的 HTTP 客户端；需要测试账号具有这两个 schema 的创建/使用权限，迁移库需预装 `pg_trgm`（否则 `0014` 或一次性 schema 用例会 Skip/失败）。默认全部跳过，需显式开启。集成测试位于 `internal/server`（HTTP 全链路）与模块 `http` 包（如 `internal/modules/ai/http`），已不再依赖旧 `internal/*` 业务包：

```bash
TIMELINE_INTEGRATION=1 go test ./internal/server -run TestAccountIntegration -count=1 -v
TIMELINE_INTEGRATION=1 go test ./internal/server -run TestContentIntegration -count=1 -v
TIMELINE_INTEGRATION=1 go test ./internal/server -run TestAdminSettingsAndAttachmentIntegration -count=1 -v
TIMELINE_INTEGRATION=1 go test ./internal/server -run 'TestBootstrapIntegration|TestAdminGrantIntegration|TestGlobalAISwitchIntegration' -count=1 -v
TIMELINE_INTEGRATION=1 go test ./internal/modules/ai/http -run TestAIAdminIntegration -count=1 -v
# 全部集成测试（多个测试包共享同一对测试 schema，建议串行执行）
TIMELINE_INTEGRATION=1 go test -p 1 ./...
```

`go run ./cmd/server` 从当前工作目录查找 `.env`，因此应从仓库根目录启动。当前监听地址固定为 `:5070`；`.env.example` 中的 `APP_PORT` 是部署时的宿主机端口映射配置，不会改变 Go 代码的监听端口。

启动需要 `DB_NAME`、`DB_USER`、`DB_PASSWORD`、`DB_HOST`。缺少任意一项会拒绝启动。默认 `RUN_MIGRATIONS=true`，因此运行服务会连接并修改所配置的数据库；只能指向开发/测试数据库。本地运行还应把 `LOG_FILE` 和 `UPLOAD_DIR` 设置为可写的本地路径，不要直接照搬容器中的 `/data/...` 路径。经过反向代理时，只在 `TRUSTED_PROXIES` 中配置实际代理 IP/CIDR，避免伪造客户端 IP 或全站共享限频桶。

运行期行为：

- HTTP 先启动，数据库在后台按指数退避重试，退避上限 30 秒。
- `/healthz` 只表示进程可响应，始终不依赖数据库。
- `/readyz` 在数据库可 Ping 且迁移步骤完成（或按配置跳过）后返回 200，否则返回 503。
- 已构建前端存在时，数据库/迁移未就绪期间 `/api/*` 返回 503 `service_unavailable`；就绪后未知 `/api` 路径返回 JSON 404，其他前端路径回退到 `index.html`；`web/dist/index.html` 不存在时服务端显示内置占位页。

### 部署文件与 CI

```bash
bash -n deploy/scripts/*.sh                  # 脚本语法检查
docker compose config                        # 校验 Compose 与环境变量展开（需 .env）
IMAGE_TAG=local docker compose build         # 构建镜像；APP_VERSION 由 IMAGE_TAG 注入
docker compose up -d && docker compose ps    # 需外部 PostgreSQL 可达
```

- 运行镜像内已预创建 `/data/uploads` 与 `/data/logs`，属主 UID/GID 10001；宿主机目录由 `deploy/scripts/prepare.sh` 准备。
- 镜像构建参数 `APP_VERSION` 默认 `dev`，Compose 传入 `IMAGE_TAG`；启动日志会输出 `version` 字段。
- CI 只执行 `go test ./...`、`go vet ./...`、`npm ci && npm run build` 与 `docker build`，**不启动容器**，因此不替代部署机上的容器运行验证。

## 5. 代码与接口约定

### Go 与通用习惯

- 遵循 `gofmt` 和标准 Go 包结构；修改后运行 `go test ./...` 与 `go vet ./...`。
- 保持现有小包划分和依赖方向，业务入口不要堆积到 `cmd/server/main.go`。
- 注释、错误信息和面向用户的文案沿用现有简体中文风格；公开标识符和 API 字段仍使用既定英文命名。
- 全系统固定使用 `Asia/Shanghai`，不增加用户时区换算。DSN 同时固定数据库会话时区和 `search_path=timeline,"user"`。
- 日志使用项目初始化的 `slog.Logger`，不要另起一套日志框架；密钥和敏感请求头不得进入日志。ERROR 级别日志由 `logging.Init` 包装的 handler 自动附加完整调用栈（跳过 slog/logging 内部帧，首帧为业务调用点），显式记录 panic 现场时传 `slog.String(logging.StackAttr, string(debug.Stack()))`，已提供 stack 不会重复采集；WARN/INFO 与访问日志（`msg=http`）不附加。panic 恢复统一走 `transport/middleware.Recovery` 与 `platform/jobs`：记录完整栈与请求/作业标识，HTTP 未写出响应时返回统一 500 `internal_error`、客户端断开（EPIPE/ECONNRESET/`http.ErrAbortHandler`）按 WARN 记录摘要且不打印栈；不要使用只写 stderr 的 `gin.Recovery`，也不要把 panic 细节返回客户端。

### 目标架构分层与依赖约定（refactor 分支）

- **handler 只在 `modules/*/http`**：`*gin.Context`、`c.JSON`、`net/http` 只出现在 `modules/*/http`、`transport/*` 与 `server`；`domain`/`app`/`repo` 不得出现 gin。
- **SQL 只在 `modules/*/repo`**：所有 SQL 字符串、Scan、`FOR UPDATE`、计数更新只写在 repo；平台 `jobs`/`audit`/`settings`/`migrate` 各自拥有对应表的 SQL。禁止 repo 访问其它模块拥有的表，跨模块读写一律走端口。
- **app/domain 不 import pgx**：`app` 不出现 `pgx`、`net/http`、`gin`、`pool.Begin/Commit/Rollback`；repo 通过 `db.ExecutorFrom(ctx, pool)` 自动复用 app 编排的当前事务。
- **模块间只走 `shared/contracts`**：消费者在 `app` 中依赖窄接口（生产者 app 类型结构化实现），组合根注入；跨模块命令/查询/结果结构放 `shared/contracts`；禁止 `modules/x` import `modules/y`。
- **路由策略组在 `transport/router`**：每条路由只声明 `Method/Path/Policy/Handler`，中间件链由 Policy 决定（Public/Optional/Login/HumanWrite/AdminRead/AdminWrite/SuperAdminWrite/GuestWrite）；handler 不自行拼装认证/CSRF 中间件。
- **组合根在 `internal/app`**：`server` 只构造 Gin 引擎与全局中间件，`router` 只注册路由；`cmd/server/main.go` 不再装配业务服务。
- **时间由应用 Clock 显式传入**：`platform/clock` 是 `time.Now()` 唯一来源（ID/日志/访问日志等白名单除外）；app 每个事务取一次 `now` 并作为参数传给 repo。**数据库不提供业务时间默认值**（`0011` 起移除），SQL 不出现 `now()/CURRENT_DATE/::date/interval`；`timeline.schema_migrations.applied_at` 是唯一白名单。
- **测试与守卫**：新增逻辑按层补测（domain 纯函数表驱动、app fake 端口、repo 集成测试、http `httptest`）；`go test ./internal/architecture` 是结构不变量（模块分层/互斥/依赖方向、SQL/时间函数/Gin 位置、事务原语、遗留符号、`cmd/server` import 白名单）的 CI 门禁，扫描范围覆盖新树、`cmd/`、`internal/server`、`internal/transport/router`，`internal/testkit` 是唯一显式 SQL 例外（测试基础设施）。

### 前端

- Vue 组件使用 `<script setup lang="ts">`，保持现有单引号、无分号风格；样式使用 Tailwind utility，不再新增 SFC `scoped` 样式块或手写组件 CSS。
- `web/src/styles/app.css` 是唯一样式入口，负责 Tailwind 主题映射、全局基础行为、`shell-down`（`max-width: 880px`）断点和动画；不要启用 Preflight 或另建 PostCSS/Tailwind 配置文件。
- 跨组件重复的 utility 组合放在 `web/src/styles/ui.ts`，必须使用完整静态类名；禁止运行时拼接 Tailwind 类名片段，动态状态使用静态映射或 Vue 条件 class。
- 颜色统一来自 `web/src/styles/tokens.css` 的 CSS 自定义属性，并通过 `app.css` 的 `@theme inline` 映射给 Tailwind；组件不得随意写入字面量颜色。品牌 SVG 按现有品牌图源处理。
- 主题通过 `<html data-theme>` 切换，注册表在 `web/src/composables/theme.ts`（内置浅色/深色 + EVA 扩展配色主题 `eva-01`/`eva-rei`/`eva-asuka`，每套主题在 `tokens.css` 定义全部令牌），手动偏好存储在 `localStorage` 的 `tl-theme`；保留 `web/index.html` 中防止首帧闪白的内联脚本（按主题白名单校验，扩展主题时同步更新）。
- 图标使用内联 SVG 或本地 SVG sprite，遵循 24×24 viewBox、圆头线框和 `currentColor` 规范；不要引入图标字体、位图图标或外链图标。
- 遵循 `docs/DESIGN_SYSTEM.md` 的 GitHub 式按钮、4px 间距体系、880px 响应式断点和减少动效规则。
- 前端没有独立 lint/format/test 命令，至少用 `npm run build` 验证 TypeScript 与 Vite 构建。

### API 与数据模型

以下是 `docs/API.md` 和 `docs/DATA_MODEL.md` 确定的实现约定，新增业务时必须同步遵守：

- 业务 API 基础路径为 `/api/v1`，JSON 字段使用 `snake_case`，列表使用不透明游标分页。
- 雪花 ID 在数据库中为 `BIGINT`，由后端生成；JSON 中必须以字符串序列化，避免 JavaScript 精度丢失。
- 账号/认证表属于 `"user"` schema，其余业务表属于 `timeline`，不使用 `public` 承载业务表。
- 迁移和 DDL 必须显式带 schema 前缀，`user` 是 PostgreSQL 保留字，显式引用时必须写成 `"user"`；运行期业务查询统一使用未限定表名，并依赖 DSN 固定的 `search_path=timeline,"user"`，不要在同一套运行期查询中混用两种方案。
- 数据库表一律不使用外键约束，表间引用只对引用列建立普通索引；引用完整性由应用层在事务内维护，跨表写入需在同一事务中校验引用对象存在且状态有效。
- 所有持久化数据一律逻辑删除（状态字段或 `deleted_at`），不做物理删除；清理任务只标记失效或删除状态，不执行 `DELETE`，也不删除附件、衍生文件等物理文件。
- 业务 HTTP 接口只使用 GET 与 POST，不使用 PUT/PATCH/DELETE；修改类操作用 POST（可与资源路径同路径），删除、取消类操作使用语义化 POST action 路径（如 `/delete`、`/unlike`、`/unfollow`）。
- 内容、任务、媒体和记忆状态相互分离；任务执行时重新授权，并通过幂等键防止重试或重启造成重复结果。
- 帖子、评论、附件等一律逻辑删除（记录保留，只更新状态/`deleted_at`）；已发布帖子的编辑和历史恢复采用追加版本，不覆盖旧历史。
- 角色模型保持 `users.role` 为 `user`/`admin` 两值，不新增第三个角色；**超级管理员 = `role='admin'` 且 `is_super_admin=true`**，由空库首个注册用户自动获得且全库唯一（单超管部分唯一索引）。只有超级管理员可授权/撤销管理员；普通管理员不能操作超管账号；公开用户对象不返回 `is_super_admin`。

迁移文件命名为 `NNNN_描述.sql`，按文件名排序，只允许追加，不能修改已应用迁移。当前迁移器 v2（`internal/platform/migrate`）仍按分号简单分句，不支持函数体等内部含分号的复合语句；需要复杂迁移时应先改进迁移工具，而不是绕过解析限制。首行为 `-- timeline:no-transaction` 的迁移文件**不包事务**，用于 `CREATE INDEX CONCURRENTLY` 等不能在事务内执行的语句（仓库 `0013`/`0014`），此类文件必须幂等（`IF NOT EXISTS`）且失败后需人工核对重跑，迁移器不自动回滚。业务时间列的 `DEFAULT now()` 已在 `0011` 移除：新写入必须由应用显式传入时间，数据库只保留 `schema_migrations.applied_at` 一个时间默认值；`pg_trgm` 等扩展需要数据库级权限，权限不足时应由 DBA 预先创建扩展（见 `docs/DEPLOYMENT.md`）。

## 6. 测试与验收

当前自动化测试覆盖：

- 单元测试：雪花 ID、密码与令牌、注册/资料校验、固定窗口限频、游标与 JSON 约束、代理配置、迁移安全约束（无外键/无物理删除）、路由方法与健康检查、富文本清洗与 Markdown 渲染（含 XSS 用例）、附件类型嗅探与图片缩放、帖子 DTO 与时间游标、评论状态机与幂等键、私信权限矩阵/撤回窗口/预览截断/规范化与幂等（`internal/modules/dm/{domain,app}`）、AI 密钥加解密与生成参数解析、AI 决策解析与频率判断、网页搜索 URL/重试/结果裁剪/密钥脱敏、有限工具轮次与降级、搜索设置加密与校验、候选范围二次授权、空工具调用兼容与控制字符校验；ERROR 日志调用栈与 panic 恢复：`internal/platform/logging/stack_test.go`（首帧为业务调用点、跳过日志内部帧、深栈扩容、WARN/INFO/访问日志不附加、显式 stack 去重、WithAttrs/WithGroup）、`TestInitAutoStackOnError`（Init 后 text 记录后多行缩进栈与 json `stack` 字段）、`internal/transport/middleware/recovery_test.go`（panic 记录完整栈并返回 500 `internal_error`、已写出不覆盖、EPIPE/`ErrAbortHandler` 按 WARN 无栈、nil logger 回退）、`internal/server` 的 panic 全局接线用例与 `internal/platform/jobs` 的作业/周期任务 panic 栈断言。
- 集成测试（默认跳过，需要测试 schema）：`TestAccountIntegration`（账号全链路）、`TestContentIntegration`（帖子/草稿/定时/版本/附件/可见性/时间线/关注/点赞/评论/转发/通知/标签/搜索/管理审核）、`TestAdminSettingsAndAttachmentIntegration`（系统设置即时生效、全局搜索密钥加密/清除、存储配额、附件审核/删除、用户统计、定时失败状态）、`TestBootstrapIntegration`（空库首注册超管、并发唯一超管、空库忽略注册开关的隔离 schema 用例）、`TestAdminGrantIntegration`（超管授权/撤销管理员、普通管理员越权与超管保护、审计与幂等）、`TestGlobalAISwitchIntegration`（全局 AI 开关即时生效、紧急停止取消待执行任务并暂停后台作业）、`TestAIAdminIntegration`（模型预设、AI 配置、`allow_web_search`、记忆、生成任务）、`TestDMIntegration`（私信创建幂等/发送/未读/已读/撤回/隐藏恢复/策略矩阵/AI 与停用拒绝/非成员 404/设置往返）、`TestPinIntegration`（置顶权限与幂等、3 条上限冲突、完整列表重排、草稿拒绝、主页 pinned_posts 与普通流去重、仅关注者置顶帖可见性、删除置顶帖自动失效）、`TestBookmarkIntegration`（收藏/取消/幂等、时间线与详情 `viewer_state.bookmarked`、收藏夹 CRUD 与第 21 个 409 `folder_limit`、他人夹 404、移动归属与 `folder_id` 三态过滤、删除夹归还默认、游客 401/停用 403、删帖与取关后列表过滤、管理后台无收藏路由）。
- 前端以 `npm run build`（`vue-tsc` + `vite`）作为类型与构建校验，没有独立 lint/test 脚本。

新增 Go 逻辑时，在对应包添加 `_test.go`，至少执行：

```bash
go test ./...
go vet ./...
cd web && npm run build
```

`docs/ACCEPTANCE.md` 是完整手工验收和开发自测清单：

- P0 用例为上线必过项，P1 失败必须记录原因和计划；SEC 安全组不允许失败。
- V1 功能已实现，但 P0 清单尚未人工执行；改动核心流程后需要重新走一遍对应条目。
- 验收需要游客、普通用户、超级管理员、普通管理员和至少两个 AI 的隔离场景，并覆盖权限、XSS、CSRF、上传、ID 精度和备份恢复；本轮新增私信用例（DM-01～12）与 SEC-10/11，覆盖接收策略矩阵、撤回时间窗、隐藏恢复、非成员 404、管理员不可见与 AI 双向拒绝；初始化与角色授权用例（AUTH-07/08、ADMIN-11～14、SEC-09）继续保留。
- AI 回归使用本地 OpenAI 兼容 mock 服务，覆盖正常、noop、非法 JSON、超时和接口错误；真实模型只做冒烟抽样。
- 完整 P0 人工验收尚未执行，不能因为 `go test`、`npm run build`、CI 或开发机 Docker 冒烟通过就宣称整体验收通过；部署机仍应按清单复演 Docker 与备份恢复流程。

## 7. 文档维护约定

`docs/README.md` 是文档索引。需求或行为变化时：

1. 先更新 `docs/REQUIREMENTS.md`，再同步受影响的页面权限、流程、数据模型、API、部署、验收或设计文档。
2. 每次更新设计文档时同步其版本号、日期和文末变更记录。
3. 文档冲突时以需求基线为准，并修正其他文档；未决事项保留在“待细化”中，不要自行当成已确认需求。
4. 代码、配置、端口或部署约定变化时，同时更新对应文档和本文件。

注意：`docs/README.md`、`docs/DEPLOYMENT.md`、`docs/ACCEPTANCE.md` 已于 2026-10-18 与当前实现同步（版本、Compose、目录准备、备份恢复、CI 与超级管理员初始化/授权流程）；如再修改部署文件、端口、镜像构建参数或配置变量，必须同时更新 `docs/DEPLOYMENT.md`、`.env.example`、`README.md` 与本文件，避免重新产生漂移。

## 8. 部署实现与当前边界

部署文件已落地并与 `docs/DEPLOYMENT.md` 保持同步。当前实现：

- `Dockerfile` 三阶段构建：Node 22 构建前端 → Go 1.25 编译二进制（`go:embed` 嵌入前端）→ `alpine:3.20` 运行镜像，含 ffmpeg、tzdata、CA 证书。
- 构建参数 `APP_VERSION`（默认 `dev`）通过 `-ldflags "-X main.appVersion=..."` 注入；Compose 传入 `IMAGE_TAG`。
- 运行镜像预创建 `/data/uploads` 与 `/data/logs` 并 `chown 10001:10001`，以非 root 用户运行，`EXPOSE 5070`。
- `docker-compose.yml` 只管理 `app` 一个服务，PostgreSQL 为外部独立实例；标准输出使用 `json-file` 驱动并限制 `max-size: 10m` / `max-file: 3`，文件日志由应用按天目录管理。
- `deploy/scripts/prepare.sh` 创建并授权宿主机数据目录；`backup.sh` 备份数据库、附件与 `ENCRYPTION_KEY`（独立 `encryption-key.txt`，权限 600）；`restore.sh` 校验目标库版本、恢复附件属主与权限。
- 上传文件和日志使用绑定挂载；数据库与上传目录必须配套备份和恢复。
- `/healthz` 用于应用存活，`/readyz` 用于数据库和迁移就绪检查。
- **服务器时钟必须 NTP 同步**：应用 Clock 是业务时间权威（写库时间、定时发布触发、作业租约/退避、日界统计），服务器时钟漂移会直接改变业务时间与任务触发时间；部署前确认宿主机 `chrony`/`systemd-timesyncd` 等时间同步服务正常（容器使用宿主机内核时钟，无需在容器内单独配置 NTP）。
- 初始化不再使用命令行：`cmd/server` 只保留 `migrate`；首次部署在 `/readyz` 就绪后立即通过注册页创建首个账号（自动成为唯一超级管理员）并登录，之后才对外开放或按需关闭注册；存量部署升级到 `0010_super_admin` 后没有超级管理员，启动日志会告警，需要时另行制定迁移方案。

边界：开发机已完成 Docker 构建/容器启动与备份恢复演练的基础验证（临时 PostgreSQL 实例），但真实部署环境、外部数据库网络与浏览器 P0 人工验收仍需按 `docs/DEPLOYMENT.md` 第 11 章和 `docs/ACCEPTANCE.md` 执行。

## 9. 安全与核心业务红线

这些规则已在 V1 实现中执行，并继续作为后续修改不可突破的红线。架构与实现约定见 `docs/REQUIREMENTS.md`、`docs/API.md`、`docs/DATA_MODEL.md` 和 `docs/DEPLOYMENT.md`。

- 不提交 `.env`、数据库凭据、会话秘密或模型 API Key；不要在前端构建产物、接口响应或日志中暴露它们。
- 所有认证、授权、账号状态和可见性检查都在后端执行；隐藏前端按钮不是权限控制。
- 超级管理员唯一且受保护：只有空库首个注册用户自动成为超管（`role='admin'` + `is_super_admin=true`），不提供授予/转让超管接口；仅超管可授权/撤销管理员，普通管理员不能停用、重置密码或处置超管账号；授权/撤销必须写入审计（`user_grant_admin`/`user_revoke_admin`），角色变更即时生效且不吊销会话。
- 空库初始化是安全窗口：系统尚无人类用户时注册接口忽略注册开关，任何能访问注册页的人都可能成为超管；首次部署必须先完成初始化再对外开放，或临时限制访问来源。
- 非公开内容在时间线、详情、历史、评论、通知、标签、搜索、附件和 AI 上下文中都不得泄露；不应暴露资源存在性时返回 404。
- 人类富文本和 AI Markdown 都必须在后端转换/清洗为安全 HTML，防止脚本、危险事件属性和任意 iframe。
- 会话设计采用 HttpOnly Cookie、服务端 `auth_sessions` 和 `X-CSRF-Token`；所有非 GET 请求需要 CSRF 防护，账号停用和密码重置要使会话失效。
- 上传必须校验请求/文件大小、声明类型和实际内容，使用安全存储文件名。上传目录不得作为免鉴权或可执行静态目录暴露，非公开附件必须走授权接口。
- AI 只能使用公开内容和自身数据，只能执行后端白名单动作；模型输出不能直接执行 SQL、任意请求或修改权限。AI 调用记录不得包含 API Key 或请求头。联网搜索只能使用全局独立密钥和固定只读 Search API，不开放 Fetch、任意 URL 或社区动作；搜索结果必须作为不可信资料，主动互动目标必须再次比对本次候选范围/关系列表，未配置或失败时降级为普通决策。
- 登录、发帖、评论、上传和 AI 行为需要限频；后台任务必须有限重试、可恢复且幂等，不能让 AI/媒体/记忆任务失败回滚已成功的普通业务操作。
- 数据库不使用外键约束，所有删除（含后台清理任务）一律逻辑删除：任何代码不得对业务数据执行物理 `DELETE`，也不得删除附件、衍生文件等持久化文件；引用完整性由应用层在事务内保证。
- 业务接口只使用 GET 与 POST；删除、取消类操作通过语义化 POST action 路径实现，与 `docs/API.md` 保持一致。
- 数据库迁移和验收只能针对明确的开发/测试实例，避免对真实数据环境进行破坏性试验。

## 10. 变更记录

| 日期 | 版本 | 变更内容 |
| --- | --- | --- |
| 2026-10-18 | 0.2.2 | 同步 ERROR 日志自动调用栈与 panic 恢复：§2 日志技术栈补 ERROR 自动完整栈、WARN/INFO 与访问日志不附加、显式 stack 去重、HTTP/后台作业/周期任务 panic 记录恢复；§3 `transport/middleware` 补 `Recovery`；§5 补日志约定（`logging.Init` 自动附加、panic 显式传 `debug.Stack()`、不得用只写 stderr 的 `gin.Recovery`、不向客户端泄露 panic 细节）；§6 测试覆盖补 `stack_test.go`/`TestInitAutoStackOnError`/`recovery_test.go`/server panic 接线/作业与周期任务栈断言；`docs/REQUIREMENTS.md` 0.24.2、`docs/DEPLOYMENT.md` 0.4.4、`docs/ARCHITECTURE.md` 0.3.1、`docs/ACCEPTANCE.md` 0.2.7、`docs/README.md` 0.1.10 已同步。 |
| 2026-10-18 | 0.2.1 | SQL 最简原则全仓审查与改造落地：迁移范围补到 `0001`–`0020`（`0017`/`0018` 查询索引增删、`0019` `author_is_ai` 快照列与 `attachment_usage` 配额冗余表及回填、`0020` 作者类型索引）；`shared/` 新增 `search` 纯函数辅助包；`docs/DATA_MODEL.md` 0.9.0、`docs/ARCHITECTURE.md` 0.3.0、`docs/DEPLOYMENT.md` 0.4.2、`migrations/README.md` 与 `docs/README.md` 已同步。改造范围：个人主页统计收敛为 3 项单表计数、管理端统计拆单表查询、关键词过滤统一 `LIKE + ESCAPE`、同请求重复读取与 N+1 批量化、`author_is_ai` 替代全量账号 ID 数组过滤、上传配额 O(1) 计数、AI 调度/记忆复核降低全表扫描。未执行：真实数据库 `EXPLAIN` 基线、完整 P0 人工验收与部署机复演。 |
| 2026-10-18 | 0.2.0 | 重构完成 + 本次行为修复同步：§1 状态改为“旧包已删除、架构守卫全绿、默认与集成测试通过”，迁移范围补到 `0001`–`0016`，并注明本次修复补充的默认测试；§2 删除“旧 `internal/config` 为 legacy”；§3 旧包列表改为“已从仓库删除的旧→新位置映射表”，补充 `internal/architecture`/`internal/testkit` 与真实目录结构（`app` 无独立 `bootstrap.go`、`shared/format`、`shared/view`）；§4 集成测试说明改为 `internal/testkit` 载体、命令 `go test ./internal/ai` 改为 `go test ./internal/modules/ai/http`，补 `TestGlobalAISwitchIntegration`、`-p 1` 串行建议与 `pg_trgm` 前置，运行期行为补 `/api` 就绪前 503；§5 守卫说明同步 9 条规则与覆盖范围；§6 集成测试清单补全局 AI 开关用例。 |
| 2026-10-18 | 0.1.1 | 收尾同步：§1 改为“目标架构已接管运行路径（`cmd/server`→`internal/app`→`transport/router`→`modules/*`），旧包已退出运行路径待删除”；§2 本地配置指向 `internal/platform/config`；§3 目标架构新增 `internal/server`（slim 引擎/健康检查/前端托管）并把 `cmd/server/main.go` 移出 legacy 列表、legacy 列表标注“已退出运行路径、仅旧测试/迁移参考”，删除已改造的 `internal/server` 旧描述；§4 注明 `internal/server` 集成测试为 legacy（迁移到 `internal/testkit` 前保留）。 |
| 2026-10-18 | 0.1.0 | 按目标架构改造（G8）同步：新增文档版本/日期/关联文档头与本节变更记录；§1 说明 `refactor/target-architecture` 分支新旧包并存策略；§3 新增 `internal/{app,platform,shared,transport,modules}` 目标目录与 legacy 旧包标注；§5 新增「目标架构分层与依赖约定」（handler 只在 `modules/*/http`、SQL 只在 `modules/*/repo`、app/domain 不 import gin/pgx、模块间只走 `shared/contracts`、路由 Policy 在 `transport/router`、组合根在 `internal/app`、时间由应用 Clock 显式传入且 DB 无业务时间默认值、`schema_migrations.applied_at` 唯一白名单）；迁移段落补充迁移器 v2 的 `no-transaction` 指令、`0011` 时间默认值移除与扩展权限；§8 部署边界补充 NTP/时钟同步要求。 |
| 2026-10-19 | 0.2.3 | 新增私信（`modules/dm`）：§1 功能清单补充 1:1 私信（`dm_policy` 四档接收策略、未读轮询、撤回、单侧隐藏，AI 不参与、后台不可见）与迁移范围 `0001`–`0021`；§3 模块域补 `dm`；§6 测试清单补私信单元测试与 `TestDMIntegration`、验收补 DM-01～12 与 SEC-10/11；同步 `REQUIREMENTS.md` 0.25.0、`PAGES_AND_PERMISSIONS.md` 0.1.21、`WORKFLOWS.md` 0.8.0、`DATA_MODEL.md` 0.10.0、`API.md` 0.8.0、`ACCEPTANCE.md` 0.3.0、`ARCHITECTURE.md` 0.4.0、`docs/README.md` 0.1.11 与 `migrations/README.md`。 |
| 2026-10-19 | 0.2.4 | §1 私信条目补充正文约束（上限 2000 字、来源体积上限 64KiB，超限 400）；§3 管理端 read-model 与 auth 的 users 投影对齐说明见代码注释；同步 `REQUIREMENTS.md` 0.25.1、`API.md` 0.8.1、`ACCEPTANCE.md` 0.3.1 与 `docs/README.md` 0.1.12（审查修复：admin 投影漂移、私信正文无长度上限、对话页陈旧响应污染）。 |
| 2026-10-21 | 0.2.5 | 新增个人主页置顶：§1 功能清单补置顶条目（作者置顶已发布帖子到主页顶部、最多 3 条、可调整顺序、普通流排除）与迁移范围 `0001`–`0022`；§6 测试清单补 `TestPinIntegration`；同步 `REQUIREMENTS.md` 0.26.0、`PAGES_AND_PERMISSIONS.md` 0.1.22、`WORKFLOWS.md` 0.9.0、`DATA_MODEL.md` 0.11.0、`API.md` 0.9.0、`ACCEPTANCE.md` 0.4.0、`docs/README.md` 0.1.13 与 `migrations/README.md`（`0022_post_pinned`）。 |
| 2026-10-21 | 0.2.6 | 新增收藏：§1 功能清单社交条目补收藏（仅帖子、私有、收藏夹分组 ≤20、删除夹归还默认、列表可见性过滤、AI 与停用拒绝）与迁移范围 `0001`–`0023`；§6 测试清单补 `TestBookmarkIntegration`；同步 `REQUIREMENTS.md` 0.27.0、`PAGES_AND_PERMISSIONS.md` 0.1.23、`WORKFLOWS.md` 0.10.0（原 11–14 章顺延）、`DATA_MODEL.md` 0.12.0、`API.md` 0.10.0、`ACCEPTANCE.md` 0.5.0（BK 组，原章节顺延）、`DESIGN_SYSTEM.md` 0.2.2、`ARCHITECTURE.md` 0.4.2、`docs/README.md` 0.1.14 与 `migrations/README.md`（`0023_bookmarks`）。 |
| 2026-10-21 | 0.2.7 | 主题机制扩展为多主题：§5 前端 bullet 改为 `<html data-theme>` 注册表切换（`web/src/composables/theme.ts`，内置浅色/深色 + EVA 扩展主题 `eva-01`/`eva-rei`/`eva-asuka`，每套在 `tokens.css` 定义全部令牌；`index.html` 首帧按白名单校验）；纯前端变更，无后端/迁移影响；同步 `REQUIREMENTS.md` 0.28.0、`DESIGN_SYSTEM.md` 0.3.0（§3.4 EVA 配色表 + §8 多主题机制 + `ThemePicker.vue`/`palette` 图标）与 `docs/README.md` 0.1.15。 |
