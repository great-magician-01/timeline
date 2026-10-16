# Timeline 项目指南

本文件面向首次接触本仓库的 AI 编码代理。项目文档、代码注释和界面文案主要使用简体中文；修改代码和文档时沿用这一语言习惯。本文区分**当前已实现的行为**与**文档中的目标设计**，不要把设计清单误当成已经完成的功能。

## 1. 项目概览

Timeline 是一个计划支持长文、多附件、分级可见性、完整社交互动，以及具有独立身份和分层记忆的 AI 用户的时间线社区。目标界面为简体中文响应式 Web，目标部署形态是单个 Go 应用托管 Vue 前端并访问独立 PostgreSQL 实例。

当前仓库处于**工程骨架阶段**，已经实现：

- Go/Gin HTTP 服务、`/healthz` 存活检查和 `/readyz` 就绪检查。
- Vue 3 单页骨架、深浅色主题切换，以及前端构建产物的 Go 内嵌托管和 SPA 回退。
- PostgreSQL 连接、失败退避重试、最小迁移器，以及创建 `"user"` 与 `timeline` 两个 schema 的首个迁移。
- 环境变量配置、双通道日志、雪花 ID 生成器及其单元测试。

尚未实现认证、用户、帖子、附件、社交、AI、管理后台和持久化后台任务等业务功能。当前唯一 SQL 迁移只创建 schema；前端目前只是后端状态自检页。仓库也尚未包含实际的 `Dockerfile`、`docker-compose.yml` 或 `deploy/` 脚本。

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

## 3. 目录与模块划分

- `cmd/server/main.go`：唯一服务入口。负责固定 `Asia/Shanghai` 时区、加载配置、初始化日志和 ID 生成器、启动 HTTP、后台连接数据库/迁移，以及优雅关闭。
- `internal/config/`：环境变量加载、校验和 PostgreSQL DSN 构造。
- `internal/db/`：连接池、Ping 和数据库退避重试。
- `internal/id/`：应用侧雪花 ID；worker 范围为 0–1023，带时钟回拨处理。
- `internal/logging/`：`slog` 文本/JSON 日志，文件路径为 `<LOG_FILE 目录>/<yyyyMMdd>/<文件名>`。
- `internal/migrate/`：按文件名顺序执行内嵌 SQL，使用 PostgreSQL advisory lock 和 `timeline.schema_migrations` 记录版本。
- `internal/server/`：Gin 路由、访问日志、运行时就绪状态、健康检查和前端托管。
- `migrations/`：追加式 SQL 迁移及其 embed 包。
- `web/src/`：Vue 入口、单文件组件和全局设计令牌；当前没有路由、状态管理或 API 模块。
- `web/public/`：直接复制到构建产物的静态文件，包括 favicon。
- `web/dist/`：Vite 生成物，由 Go 嵌入；不要手工编辑。
- `docs/`：需求、页面权限、状态机、数据模型、API、部署、验收和设计系统的权威设计文档。
- `demo/`：未接后端的静态视觉方案；`home-e-social.html` 是选定方向，`logo.svg` 是品牌图源。实现样式以 `docs/DESIGN_SYSTEM.md` 为准，而不是以 demo 为准。
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
```

`go run ./cmd/server` 从当前工作目录查找 `.env`，因此应从仓库根目录启动。当前监听地址固定为 `:5070`；`.env.example` 中的 `APP_PORT` 是部署时的宿主机端口映射配置，不会改变 Go 代码的监听端口。

启动需要 `DB_NAME`、`DB_USER`、`DB_PASSWORD`、`DB_HOST`。缺少任意一项会拒绝启动。默认 `RUN_MIGRATIONS=true`，因此运行服务会连接并修改所配置的数据库；只能指向开发/测试数据库。本地运行还应把 `LOG_FILE` 和 `UPLOAD_DIR` 设置为可写的本地路径，不要直接照搬容器中的 `/data/...` 路径。

运行期行为：

- HTTP 先启动，数据库在后台按指数退避重试，退避上限 30 秒。
- `/healthz` 只表示进程可响应，始终不依赖数据库。
- `/readyz` 在数据库可 Ping 且迁移步骤完成（或按配置跳过）后返回 200，否则返回 503。
- 已构建前端存在时，未知 `/api` 路径返回 JSON 404，其他前端路径回退到 `index.html`；`web/dist/index.html` 不存在时服务端显示内置占位页。

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
- `user` 是 PostgreSQL 保留字，所有 SQL 引用该 schema 时必须写成 `"user"`；DDL 应显式带 schema 前缀。
- 内容、任务、媒体和记忆状态相互分离；任务执行时重新授权，并通过幂等键防止重试或重启造成重复结果。
- 帖子、评论、附件等优先逻辑删除；已发布帖子的编辑和历史恢复采用追加版本，不覆盖旧历史。

迁移文件命名为 `NNNN_描述.sql`，按文件名排序，只允许追加，不能修改已应用迁移。当前迁移器只是按分号简单分句，不支持函数体等内部含分号的复合语句；需要复杂迁移时应先改进迁移工具，而不是绕过解析限制。

## 6. 测试与验收

当前自动化测试很少：只有 `internal/id/snowflake_test.go`，覆盖 ID 严格递增/唯一以及 worker 边界。其余 Go 包目前没有测试文件，前端没有测试运行器，数据库集成测试也尚未建立。

新增 Go 逻辑时，在对应包添加 `_test.go`，至少执行：

```bash
go test ./...
go vet ./...
cd web && npm run build
```

`docs/ACCEPTANCE.md` 是完整手工验收和开发自测清单：

- P0 用例为上线必过项，P1 失败必须记录原因和计划；SEC 安全组不允许失败。
- 验收需要游客、普通用户、管理员和至少两个 AI 的隔离场景，并覆盖权限、XSS、CSRF、上传、ID 精度和备份恢复。
- AI 回归使用本地 OpenAI 兼容 mock 服务，覆盖正常、noop、非法 JSON、超时和接口错误；真实模型只做冒烟抽样。
- 当前大量验收项对应尚未实现的功能，不能仅因现有骨架能启动就宣称通过整体验收。

## 7. 文档维护约定

`docs/README.md` 是文档索引。需求或行为变化时：

1. 先更新 `docs/REQUIREMENTS.md`，再同步受影响的页面权限、流程、数据模型、API、部署、验收或设计文档。
2. 每次更新设计文档时同步其版本号、日期和文末变更记录。
3. 文档冲突时以需求基线为准，并修正其他文档；未决事项保留在“待细化”中，不要自行当成已确认需求。
4. 代码、配置、端口或部署约定变化时，同时更新对应文档和本文件。

注意：`docs/README.md` 的“尚未开始编码”状态已经落后于当前骨架代码；`docs/DEPLOYMENT.md` 仍是设计稿，其中示例端口和构建镜像不能替代 `go.mod` 与当前代码的实际配置。修改相关文件时应顺手消除这类漂移。

## 8. 部署设计与当前边界

目标部署流程记录在 `docs/DEPLOYMENT.md`，但相关 Docker 文件和备份脚本尚未落地，因此目前不能假设 `docker compose up` 可用。目标架构为：

- 多阶段构建前端和 Go 二进制，前端静态资源嵌入二进制，由 Go 同源托管。
- Compose 只管理一个应用容器，不设置独立前端、Nginx 或 worker 服务。
- PostgreSQL 是外部独立实例，数据库和账号需预先创建；应用迁移只管理 schema/表结构。
- 上传文件和日志使用本地持久化挂载；数据库与上传目录必须配套备份和恢复。
- 运行镜像计划包含 ffmpeg、时区数据和 CA 证书，并以非 root 用户运行。
- `/healthz` 用于应用存活，`/readyz` 用于数据库和迁移就绪检查。

实现部署文件时，以当前 Go 版本、实际 5070 监听端口和配置加载行为为准，并同步修订部署文档中的设计示例。

## 9. 安全与核心业务红线

这些规则多数对应待实现功能，但从一开始就必须进入设计、代码和测试：

- 不提交 `.env`、数据库凭据、会话秘密或模型 API Key；不要在前端构建产物、接口响应或日志中暴露它们。
- 所有认证、授权、账号状态和可见性检查都在后端执行；隐藏前端按钮不是权限控制。
- 非公开内容在时间线、详情、历史、评论、通知、标签、搜索、附件和 AI 上下文中都不得泄露；不应暴露资源存在性时返回 404。
- 人类富文本和 AI Markdown 都必须在后端转换/清洗为安全 HTML，防止脚本、危险事件属性和任意 iframe。
- 会话设计采用 HttpOnly Cookie、服务端 `auth_sessions` 和 `X-CSRF-Token`；所有非 GET 请求需要 CSRF 防护，账号停用和密码重置要使会话失效。
- 上传必须校验请求/文件大小、声明类型和实际内容，使用安全存储文件名。上传目录不得作为免鉴权或可执行静态目录暴露，非公开附件必须走授权接口。
- AI 只能使用公开内容和自身数据，只能执行后端白名单动作；模型输出不能直接执行 SQL、任意请求或修改权限。AI 调用记录不得包含 API Key 或请求头。
- 登录、发帖、评论、上传和 AI 行为需要限频；后台任务必须有限重试、可恢复且幂等，不能让 AI/媒体/记忆任务失败回滚已成功的普通业务操作。
- 数据库迁移和验收只能针对明确的开发/测试实例，避免对真实数据环境进行破坏性试验。
