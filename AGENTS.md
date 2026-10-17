# Timeline 项目指南

本文件面向首次接触本仓库的 AI 编码代理。项目文档、代码注释和界面文案主要使用简体中文；修改代码和文档时沿用这一语言习惯。本文区分**当前已实现的行为**与**文档中的目标设计**，不要把设计清单误当成已经完成的功能。

## 1. 项目概览

Timeline 是一个支持长文、多附件、分级可见性、完整社交互动，以及具有独立身份和分层记忆的 AI 用户的时间线社区。目标界面为简体中文响应式 Web，部署形态是单个 Go 应用托管 Vue 前端并访问独立 PostgreSQL 实例。

当前仓库处于 **V1 已实现、待人工验证** 阶段，功能范围已全部落地：

- Go/Gin HTTP 服务、`/healthz` 存活检查、`/readyz` 就绪检查，以及前端构建产物的 Go 内嵌托管和 SPA 回退。
- PostgreSQL 连接、失败退避重试、最小迁移器；`migrations/` 内按文件名顺序追加的迁移（`0001` 起，含账号、内容、社交、AI、后台作业与 AI 记忆来源等表结构）。
- 账号：用户名/邮箱注册与登录、bcrypt 哈希、HttpOnly 服务端会话、游客与会话 CSRF、三维登录限频、资料与密码修改、关注审批设置、头像上传；公开 `GET /auth/config` 提供注册开关状态。
- 内容：富文本/Markdown 统一安全渲染、帖子、草稿与自动保存冲突、定时发布、编辑历史与版本恢复、附件上传与图片缩略图/视频封面（ffmpeg 可选）、可见性（公开/仅关注者/仅自己）、时间线（全站/关注/个人主页）。
- 社交：关注与关注审批、移除粉丝、点赞、评论与楼中楼、纯转发与引用、提及、通知中心（轮询未读数、按类型筛选）。
- 发现：`#话题#` 链接化与标签聚合、用户搜索、帖子搜索，全部按访问者权限过滤。
- AI：OpenAI 兼容接口预设（API Key 加密存储）、AI 身份与行为策略（含发帖/主动互动间隔）、分层记忆（近期/长期、关键词检索、沉淀与失效、来源关联）、自主动作（发帖/评论/点赞/关注/转发/引用，默认全部关闭）、任务与决策审计、模型调用记录（可按任务筛选）、管理员手动生成支持立即发布或定时发布、任务详情展示注入记忆。
- 管理后台：概览、用户管理、内容审核（帖子/评论/附件）、模型预设、AI 配置与记忆、生成与发布、全局任务、调用记录、审计日志、系统设置（注册、上传/请求大小、存储配额、发帖/上传限频、临时附件保留期、调用记录截断/保留期）与全局 AI 开关。
- 后台作业：持久化在 `background_jobs`，重启恢复，有限次数与退避重试；附件失效清理与 AI 调度为周期任务。
- 环境变量配置、双通道日志、雪花 ID 生成器、Docker/Compose、目录准备与备份恢复脚本，以及 GitHub Actions CI。

验证状态：默认 Go 测试、`go vet`、`npm run build` 与隔离 schema 集成测试已通过；开发机已使用临时 PostgreSQL 实例完成 Docker 镜像构建、容器启动、健康检查、前端托管与基础 API 冒烟，并完成一次备份/恢复演练（含附件属主修正）。尚未执行：`docs/ACCEPTANCE.md` 中的完整 P0 人工验收，以及部署机上的真实环境复演。

## 2. 技术栈与关键配置

- 后端：Go 1.25（以 `go.mod` 为准），模块名 `github.com/great-magician-01/timeline`。
- HTTP：Gin；数据库：PostgreSQL，通过 `pgx/v5/pgxpool` 连接。
- 日志：标准库 `log/slog`，同时写标准输出和按日期分目录的文件。
- 前端：Vue 3、TypeScript、Vite，使用 Node.js 22+、npm 和 `web/package-lock.json`（版本要求见根目录 `README.md`）。
- 前端构建：`vue-tsc -b && vite build`，类型检查和生产构建由同一个 `npm run build` 完成。
- 静态资源：`web/embed.go` 使用 `go:embed all:dist` 嵌入 `web/dist`。
- 数据库迁移：`migrations/embed.go` 嵌入 `migrations/*.sql`。
- 本地配置：`.env.example` 是模板；`internal/config/config.go` 会读取根目录 `.env`，但已有进程环境变量优先。

关键文件：

- `go.mod` / `go.sum`：Go 版本与依赖。
- `web/package.json`：前端脚本与依赖；目前没有 `test`、`lint` 或 `format` 脚本。
- `web/vite.config.ts`：开发代理，将 `/api`、`/healthz`、`/readyz` 转发到 `http://localhost:5070`。
- `web/tsconfig*.json`：TypeScript 项目引用和检查规则，包括未使用变量/参数检查。
- `.env.example`：部署配置模板，真实凭据不得写入或提交。
- `Dockerfile` / `docker-compose.yml`：多阶段镜像与单应用 Compose（外部 PostgreSQL）。
- `.github/workflows/ci.yml`：CI（Go 测试/静态检查、前端构建、镜像构建）；不启动容器。

## 3. 目录与模块划分

- `cmd/server/main.go`：唯一服务入口。负责固定 `Asia/Shanghai` 时区、加载配置、初始化日志/ID 生成器/附件存储/后台作业运行器、启动 HTTP、后台连接数据库/迁移、优雅关闭，以及 `migrate` / `init-admin` 子命令。
- `internal/api/`：统一 JSON 错误与 `*api.Failure` 业务错误、请求体限制、雪花 ID 字符串、游标与列表响应。
- `internal/access/`：跨模块复用的可见性、只读与互动授权判断（`CanViewPost`、`RequireInteractivePost` 等）。
- `internal/admin/`：管理员账号处置、内容审核、后台作业处置、概览、注册与全局 AI 开关、审计查询。
- `internal/agent/`：AI 任务调度、决策引擎、动作执行、记忆形成与沉淀、调用记录与 AI 管理接口。
- `internal/ai/`：模型接口预设（密钥加解密）、AI 身份/模型/行为配置、分层记忆存储与检索、callable 客户端构造。
- `internal/audit/`：管理审计日志写入（同一事务内）。
- `internal/auth/`：账号模型、注册登录、会话与 CSRF（含 `OptionalAuth`）、中间件、资料和密码、首个管理员初始化。
- `internal/config/`：环境变量加载、校验、PostgreSQL DSN 构造与密钥派生。
- `internal/content/`：帖子、草稿与自动保存、定时发布、编辑历史、标签、搜索与时间线。
- `internal/db/`：连接池、Ping 和数据库退避重试。
- `internal/id/`：应用侧雪花 ID；worker 范围为 0–1023，带时钟回拨处理。
- `internal/jobs/`：后台作业运行器（持久化、幂等入队、有限重试、周期任务）。
- `internal/logging/`：`slog` 文本/JSON 日志，文件路径为 `<LOG_FILE 目录>/<yyyyMMdd>/<文件名>`。
- `internal/media/`：附件上传与鉴权读取、图片缩略图与视频封面、失效清理。
- `internal/migrate/`：按文件名顺序执行内嵌 SQL，使用 PostgreSQL advisory lock 和 `timeline.schema_migrations` 记录版本。
- `internal/model/`：跨模块复用的数据行结构与对外 DTO。
- `internal/notice/`：提及与通知写入、幂等去重、通知列表与已读。
- `internal/ratelimit/`：单实例内存固定窗口限频器。
- `internal/richtext/`：富文本清洗、Markdown 渲染、纯文本提取、话题与提及链接化。
- `internal/server/`：Gin 路由与中间件装配、访问日志、运行时就绪状态、健康检查和前端托管。
- `internal/social/`：关注与申请、点赞、评论与回复、纯转发、主页列表。
- `internal/storage/`：本地卷附件的安全读写与类型嗅探（不提供删除能力）。
- `migrations/`：追加式 SQL 迁移及其 embed 包。
- `web/src/`：Vue 入口、路由、认证与通知状态、API 模块、页面组件和全局设计令牌。
- `web/public/`：直接复制到构建产物的静态文件，包括 favicon。
- `web/dist/`：Vite 生成物，由 Go 嵌入；不要手工编辑。
- `docs/`：需求、页面权限、状态机、数据模型、API、部署、验收和设计系统的权威设计文档。
- `demo/`：未接后端的静态视觉方案；`home-e-social.html` 是选定方向，`logo.svg` 是品牌图源。实现样式以 `docs/DESIGN_SYSTEM.md` 为准，而不是以 demo 为准。
- `deploy/scripts/`：`prepare.sh`（创建并授权数据目录）、`backup.sh`（数据库 + 附件 + 加密密钥）、`restore.sh`（数据库 + 附件配套恢复）。
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
go run ./cmd/server migrate       # 显式执行迁移
go run ./cmd/server init-admin    # 交互创建首个管理员，不从环境变量读密码
go run ./cmd/mock-llm             # 验收用本地 OpenAI 兼容 mock（默认 127.0.0.1:8099，非生产）
```

集成测试使用现有开发实例中的 `timeline_test` 与 `user_test` 隔离 schema，不创建数据库，也不写正式业务表；需要测试账号具有这两个 schema 的创建/使用权限。默认全部跳过，需显式开启：

```bash
TIMELINE_INTEGRATION=1 go test ./internal/server -run TestAccountIntegration -count=1 -v
TIMELINE_INTEGRATION=1 go test ./internal/server -run TestContentIntegration -count=1 -v
TIMELINE_INTEGRATION=1 go test ./internal/server -run TestAdminSettingsAndAttachmentIntegration -count=1 -v
TIMELINE_INTEGRATION=1 go test ./internal/ai -run TestAIAdminIntegration -count=1 -v
```

`go run ./cmd/server` 从当前工作目录查找 `.env`，因此应从仓库根目录启动。当前监听地址固定为 `:5070`；`.env.example` 中的 `APP_PORT` 是部署时的宿主机端口映射配置，不会改变 Go 代码的监听端口。

启动需要 `DB_NAME`、`DB_USER`、`DB_PASSWORD`、`DB_HOST`。缺少任意一项会拒绝启动。默认 `RUN_MIGRATIONS=true`，因此运行服务会连接并修改所配置的数据库；只能指向开发/测试数据库。本地运行还应把 `LOG_FILE` 和 `UPLOAD_DIR` 设置为可写的本地路径，不要直接照搬容器中的 `/data/...` 路径。经过反向代理时，只在 `TRUSTED_PROXIES` 中配置实际代理 IP/CIDR，避免伪造客户端 IP 或全站共享限频桶。

运行期行为：

- HTTP 先启动，数据库在后台按指数退避重试，退避上限 30 秒。
- `/healthz` 只表示进程可响应，始终不依赖数据库。
- `/readyz` 在数据库可 Ping 且迁移步骤完成（或按配置跳过）后返回 200，否则返回 503。
- 已构建前端存在时，未知 `/api` 路径返回 JSON 404，其他前端路径回退到 `index.html`；`web/dist/index.html` 不存在时服务端显示内置占位页。

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
- 日志使用项目初始化的 `slog.Logger`，不要另起一套日志框架；密钥和敏感请求头不得进入日志。

### 前端

- Vue 组件使用 `<script setup lang="ts">`，保持现有单引号、无分号和 scoped style 风格。
- 颜色统一来自 `web/src/styles/tokens.css` 的 CSS 自定义属性；组件不得随意写入字面量颜色。品牌 SVG 按现有品牌图源处理。
- 主题通过 `<html data-theme="light|dark">` 切换，手动偏好存储在 `localStorage` 的 `tl-theme`；保留 `web/index.html` 中防止首帧闪白的内联脚本。
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

迁移文件命名为 `NNNN_描述.sql`，按文件名排序，只允许追加，不能修改已应用迁移。当前迁移器只是按分号简单分句，不支持函数体等内部含分号的复合语句；需要复杂迁移时应先改进迁移工具，而不是绕过解析限制。

## 6. 测试与验收

当前自动化测试覆盖：

- 单元测试：雪花 ID、密码与令牌、注册/资料校验、固定窗口限频、游标与 JSON 约束、代理配置、迁移安全约束（无外键/无物理删除）、路由方法与健康检查、富文本清洗与 Markdown 渲染（含 XSS 用例）、附件类型嗅探与图片缩放、帖子 DTO 与时间游标、评论状态机与幂等键、AI 密钥加解密与生成参数解析、AI 决策解析与频率判断。
- 集成测试（默认跳过，需要测试 schema）：`TestAccountIntegration`（账号全链路）、`TestContentIntegration`（帖子/草稿/定时/版本/附件/可见性/时间线/关注/点赞/评论/转发/通知/标签/搜索/管理审核）、`TestAdminSettingsAndAttachmentIntegration`（系统设置即时生效、存储配额、附件审核/删除、用户统计、定时失败状态）、`TestAIAdminIntegration`（模型预设、AI 配置、记忆、生成任务）。
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
- 验收需要游客、普通用户、管理员和至少两个 AI 的隔离场景，并覆盖权限、XSS、CSRF、上传、ID 精度和备份恢复。
- AI 回归使用本地 OpenAI 兼容 mock 服务，覆盖正常、noop、非法 JSON、超时和接口错误；真实模型只做冒烟抽样。
- 完整 P0 人工验收尚未执行，不能因为 `go test`、`npm run build`、CI 或开发机 Docker 冒烟通过就宣称整体验收通过；部署机仍应按清单复演 Docker 与备份恢复流程。

## 7. 文档维护约定

`docs/README.md` 是文档索引。需求或行为变化时：

1. 先更新 `docs/REQUIREMENTS.md`，再同步受影响的页面权限、流程、数据模型、API、部署、验收或设计文档。
2. 每次更新设计文档时同步其版本号、日期和文末变更记录。
3. 文档冲突时以需求基线为准，并修正其他文档；未决事项保留在“待细化”中，不要自行当成已确认需求。
4. 代码、配置、端口或部署约定变化时，同时更新对应文档和本文件。

注意：`docs/README.md`、`docs/DEPLOYMENT.md`、`docs/ACCEPTANCE.md` 已于 2026-10-17 与当前实现同步（版本、Compose、目录准备、备份恢复与 CI）；如再修改部署文件、端口、镜像构建参数或配置变量，必须同时更新 `docs/DEPLOYMENT.md`、`.env.example`、`README.md` 与本文件，避免重新产生漂移。

## 8. 部署实现与当前边界

部署文件已落地并与 `docs/DEPLOYMENT.md` 保持同步。当前实现：

- `Dockerfile` 三阶段构建：Node 22 构建前端 → Go 1.25 编译二进制（`go:embed` 嵌入前端）→ `alpine:3.20` 运行镜像，含 ffmpeg、tzdata、CA 证书。
- 构建参数 `APP_VERSION`（默认 `dev`）通过 `-ldflags "-X main.appVersion=..."` 注入；Compose 传入 `IMAGE_TAG`。
- 运行镜像预创建 `/data/uploads` 与 `/data/logs` 并 `chown 10001:10001`，以非 root 用户运行，`EXPOSE 5070`。
- `docker-compose.yml` 只管理 `app` 一个服务，PostgreSQL 为外部独立实例；标准输出使用 `json-file` 驱动并限制 `max-size: 10m` / `max-file: 3`，文件日志由应用按天目录管理。
- `deploy/scripts/prepare.sh` 创建并授权宿主机数据目录；`backup.sh` 备份数据库、附件与 `ENCRYPTION_KEY`（独立 `encryption-key.txt`，权限 600）；`restore.sh` 校验目标库版本、恢复附件属主与权限。
- 上传文件和日志使用绑定挂载；数据库与上传目录必须配套备份和恢复。
- `/healthz` 用于应用存活，`/readyz` 用于数据库和迁移就绪检查。

边界：开发机已完成 Docker 构建/容器启动与备份恢复演练的基础验证（临时 PostgreSQL 实例），但真实部署环境、外部数据库网络与浏览器 P0 人工验收仍需按 `docs/DEPLOYMENT.md` 第 11 章和 `docs/ACCEPTANCE.md` 执行。

## 9. 安全与核心业务红线

这些规则已在 V1 实现中执行，并继续作为后续修改不可突破的红线。架构与实现约定见 `docs/REQUIREMENTS.md`、`docs/API.md`、`docs/DATA_MODEL.md` 和 `docs/DEPLOYMENT.md`。

- 不提交 `.env`、数据库凭据、会话秘密或模型 API Key；不要在前端构建产物、接口响应或日志中暴露它们。
- 所有认证、授权、账号状态和可见性检查都在后端执行；隐藏前端按钮不是权限控制。
- 非公开内容在时间线、详情、历史、评论、通知、标签、搜索、附件和 AI 上下文中都不得泄露；不应暴露资源存在性时返回 404。
- 人类富文本和 AI Markdown 都必须在后端转换/清洗为安全 HTML，防止脚本、危险事件属性和任意 iframe。
- 会话设计采用 HttpOnly Cookie、服务端 `auth_sessions` 和 `X-CSRF-Token`；所有非 GET 请求需要 CSRF 防护，账号停用和密码重置要使会话失效。
- 上传必须校验请求/文件大小、声明类型和实际内容，使用安全存储文件名。上传目录不得作为免鉴权或可执行静态目录暴露，非公开附件必须走授权接口。
- AI 只能使用公开内容和自身数据，只能执行后端白名单动作；模型输出不能直接执行 SQL、任意请求或修改权限。AI 调用记录不得包含 API Key 或请求头。
- 登录、发帖、评论、上传和 AI 行为需要限频；后台任务必须有限重试、可恢复且幂等，不能让 AI/媒体/记忆任务失败回滚已成功的普通业务操作。
- 数据库不使用外键约束，所有删除（含后台清理任务）一律逻辑删除：任何代码不得对业务数据执行物理 `DELETE`，也不得删除附件、衍生文件等持久化文件；引用完整性由应用层在事务内保证。
- 业务接口只使用 GET 与 POST；删除、取消类操作通过语义化 POST action 路径实现，与 `docs/API.md` 保持一致。
- 数据库迁移和验收只能针对明确的开发/测试实例，避免对真实数据环境进行破坏性试验。
