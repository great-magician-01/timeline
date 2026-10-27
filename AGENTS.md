# Timeline 项目指南

本文件只记录编码代理需要长期遵守的仓库约定。具体需求、接口和部署细节以 [`docs/README.md`](docs/README.md) 索引的权威文档为准，不要在此复制功能清单或变更历史。项目文档、代码注释和界面文案使用简体中文；公开标识符和 API 字段沿用既定英文命名。

## 1. 项目与技术栈

Timeline 是包含内容、社交、私信、群聊和 AI 用户的时间线社区。部署形态为单个 Go 应用托管 Vue 前端，并连接独立 PostgreSQL；实时推送基于进程内事件总线，当前只支持单实例。

- 后端：Go、Gin、PostgreSQL、`pgx/v5/pgxpool`、`log/slog`。Go 版本以 `go.mod` 为准。
- 前端：Vue 3、TypeScript、Vite、Tailwind CSS 4、Vitest，使用 Node.js 22+ 和 npm。
- 前端产物由 `web/embed.go` 嵌入 Go 二进制；`web/dist/` 是生成目录，不要手改。
- 配置模板为 `.env.example`，真实凭据不得提交。本地从仓库根目录读取 `.env`，已有环境变量优先。
- 当前 V1 功能已实现，但完整 P0 人工验收和真实部署复演尚未完成，不能把自动化测试通过等同于整体验收通过。

## 2. 常用命令

Go 命令从仓库根目录执行，前端命令在 `web/` 下执行。

```bash
# 后端
go test ./...
go vet ./...
go test ./internal/architecture
go build -o bin/timeline.exe ./cmd/server   # Windows
go run ./cmd/server
go run ./cmd/server migrate                 # 唯一子命令
go run ./cmd/mock-llm                       # 本地 AI 验收 mock，非生产

# 前端
npm ci
npm run dev
npm test
npm run build                               # vue-tsc 类型检查 + Vite 构建

# 全量集成测试；多个包共享测试 schema，需串行
TIMELINE_INTEGRATION=1 go test -p 1 -count=1 ./...
```

- 新增 Go 逻辑应在对应包补 `_test.go`，并按改动范围运行上述检查。前端没有独立 lint、format 或浏览器 E2E 脚本。
- 集成测试默认跳过，使用 `internal/testkit` 和隔离的 `timeline_test`/`user_test` schema，不写正式业务表；测试账号需有相应权限，数据库需预装 `pg_trgm`。
- 启动必须配置 `DB_HOST`、`DB_NAME`、`DB_USER`、`DB_PASSWORD`。默认 `RUN_MIGRATIONS=true`，只能指向明确的开发/测试数据库；本地 `LOG_FILE`、`UPLOAD_DIR` 应使用可写本地路径。
- 服务固定监听 `:5070`；`APP_PORT` 只用于部署时的宿主机端口映射。`/health`、`/healthz` 是存活检查，`/readyz` 是数据库与迁移就绪检查。
- 修改 `web/src` 后，必须先 `npm run build`，再重新构建并启动 Go 服务，内嵌前端才会更新。
- CI 包含 gofmt、Go 测试/vet、架构守卫、PostgreSQL 集成测试、前端测试/构建和 Docker 镜像构建，不替代部署机验证。

## 3. 目录与依赖方向

```text
cmd/server             唯一服务入口；不装配业务服务
internal/app           唯一组合根：依赖注入、跨模块端口、作业、路由注册
internal/platform      无业务语义的技术基座
internal/shared        无 IO 的共享模型、策略、契约和纯函数
internal/transport     HTTP 公共处理、全局中间件、声明式路由
internal/server        Gin 引擎、全局中间件、健康检查、静态托管
internal/modules/*     业务模块，通常为 domain/app/repo/http 四层
internal/architecture  架构守卫测试
internal/testkit       隔离 schema 与 HTTP 集成测试脚手架
migrations             追加式 SQL 迁移
web/src                Vue 前端源码
docs                   需求、API、数据模型、部署、验收等权威文档
```

核心调用方向：`cmd/server` → `internal/app` → `transport/router` → `modules/*`。架构细节见 [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)。

## 4. 后端硬性约定

- `domain`：实体不变式、状态机和纯校验；`app`：用例、事务边界、取时和端口调用；`repo`：唯一业务 SQL 层；`http`：Gin handler、参数解析、错误映射和 DTO。
- Gin、`net/http` 只允许出现在 `modules/*/http`、`transport/*` 和 `server`。`domain`/`app`/`repo` 不得依赖 Gin，`app`/`domain` 不得 import pgx。
- SQL、Scan、`FOR UPDATE` 只写在 `modules/*/repo`。平台 `jobs`/`audit`/`settings`/`migrate` 各自管理对应表；`internal/testkit` 是测试基础设施例外。
- repo 不得直接访问其他模块的表，禁止 `modules/x` import `modules/y`。跨模块读写通过 `shared/contracts` 中的窄接口和数据结构，由组合根注入。
- 事务由 app 通过 `platform/db.TxManager.WithTx` 编排，app 不直接调用 `pool.Begin/Commit/Rollback`；repo 使用 `db.ExecutorFrom(ctx, pool)` 复用当前事务。
- 路由只在 `transport/router` 注册，通过 Policy 决定认证、CSRF 等中间件链；handler 不自行拼装这些中间件。
- 全系统固定 `Asia/Shanghai`。业务取时只能使用 `platform/clock`，app 每个事务取一次 `now` 并显式传给 repo；数据库不提供业务时间默认值，业务 SQL 不使用 `now()`、`CURRENT_DATE`、`::date` 或 `interval`。
- SSE 只发送提示信号，不携带内容载荷。实时事件必须通过 `platform/db.AfterCommit` 在事务提交后发布；回滚、幂等跳过、策略拒绝或接收者无效时不得发布。SSE 传输只放在 `modules/realtime`。
- 使用项目初始化的 `slog.Logger`，不得记录密钥或敏感请求头。ERROR 日志自动附加调用栈；panic 统一由 `transport/middleware.Recovery` 和 `platform/jobs` 恢复并记录，不向客户端泄露细节，不使用只写 stderr 的 `gin.Recovery`。
- 后台任务必须有限重试、可恢复且幂等；AI、媒体或记忆任务失败不得回滚已成功的普通业务操作。
- 修改结构后运行 `go test ./internal/architecture`，不要绕过守卫复制旧模式。

## 5. API、数据库与迁移

详细契约见 [`docs/API.md`](docs/API.md)、[`docs/DATA_MODEL.md`](docs/DATA_MODEL.md) 和 [`migrations/README.md`](migrations/README.md)。

- 业务 API 前缀为 `/api/v1`，JSON 使用 `snake_case`，列表使用不透明游标分页。
- 业务接口只用 GET 和 POST；修改用 POST，删除/取消用语义化 POST action 路径，如 `/delete`、`/unlike`。
- 雪花 ID 在数据库为 `BIGINT`，由后端生成，JSON 中必须序列化为字符串。
- 账号/认证表属于 `"user"` schema，其余业务表属于 `timeline`，不使用 `public`。DDL 显式带 schema；运行期查询使用未限定表名，并依赖 DSN 的 `search_path=timeline,"user"`。
- 不使用外键约束；引用完整性由应用层事务维护。所有持久化数据只能逻辑删除，禁止物理 `DELETE`，也不得删除附件或衍生文件。
- 帖子、评论、附件等记录保留；已发布帖子的编辑和恢复采用追加版本，不覆盖历史。
- `users.role` 只有 `user`/`admin`；超级管理员是 `role='admin' AND is_super_admin=true`，全库唯一，不提供授予或转让超管接口，公开用户对象不返回该标记。
- 迁移文件命名为 `NNNN_描述.sql`，按文件名排序，只追加，不修改已应用文件。迁移器按分号简单分句，不支持函数体等复合语句。
- 首行 `-- timeline:no-transaction` 用于并发索引等不能包事务的语句；此类迁移必须幂等，失败后需人工核对重跑。
- 任务执行时重新授权，并通过幂等键防止重试或重启造成重复结果。

## 6. 前端约定

- Vue 组件使用 `<script setup lang="ts">`，保持单引号、无分号风格。样式使用 Tailwind utility，不新增 SFC `scoped` 样式或手写组件 CSS。
- `web/src/styles/app.css` 是唯一样式入口；不要启用 Preflight，不另建 PostCSS/Tailwind 配置。
- 重复 utility 组合放在 `web/src/styles/ui.ts`，使用完整静态类名；禁止运行时拼接 Tailwind 类名片段。
- 颜色只能来自 `web/src/styles/tokens.css` 的 CSS 变量及 `app.css` 的主题映射，不写散落的字面量颜色。
- 主题通过 `<html data-theme>` 和 `web/src/composables/theme.ts` 注册表切换；新增主题时同步完整令牌、本地存储白名单及 `web/index.html` 防首帧闪白脚本。
- 图标使用内联 SVG 或本地 sprite，遵循 24×24 viewBox、圆头线框和 `currentColor`，不引入图标字体、位图或外链图标。
- 遵循 [`docs/DESIGN_SYSTEM.md`](docs/DESIGN_SYSTEM.md)，不要以 `demo/` 为最终实现依据。

## 7. 安全红线

- 认证、授权、账号状态和可见性检查全部在后端执行；隐藏前端按钮不是权限控制。非公开内容不得经任何列表、详情、通知、搜索、附件或 AI 上下文泄露；需要隐藏资源存在性时返回 404。
- 会话使用 HttpOnly Cookie、服务端 `auth_sessions` 和 `X-CSRF-Token`；所有非 GET 请求需要 CSRF 防护，停用账号和重置密码必须使会话失效。
- 空库首个注册用户自动成为唯一超级管理员，这是初始化安全窗口；仅超管可授权/撤销管理员并写审计，普通管理员不能操作超管账号。
- 人类富文本和 AI Markdown 都必须在后端清洗为安全 HTML，防止脚本、危险事件属性和任意 iframe。
- 上传必须校验请求/文件大小、声明类型和实际内容，使用安全存储文件名；上传目录不得作为免鉴权或可执行静态目录暴露，非公开附件必须走授权接口。
- AI 只能使用公开内容和自身数据，只能执行后端白名单动作；模型输出不能执行 SQL、任意请求或权限修改，调用记录不得包含 API Key 或请求头。
- 联网搜索只使用全局独立密钥和固定只读 Search API；搜索结果是不可信资料，互动目标必须重新授权，未配置或失败时降级。
- 登录、发帖、评论、上传和 AI 行为需要限频。反向代理场景只在 `TRUSTED_PROXIES` 配置真实代理 IP/CIDR。
- 不提交 `.env`、数据库凭据、会话秘密或模型 API Key，也不得在前端产物、响应或日志中暴露。
- 数据库迁移和验收只能针对明确的开发/测试实例，避免对真实数据进行破坏性试验。

## 8. 文档与验收

- 需求或行为变化时，先更新 `docs/REQUIREMENTS.md`，再同步受影响的页面权限、流程、数据模型、API、部署、验收或设计文档；文档冲突以需求基线为准。
- 更新设计文档时同步版本号、日期和变更记录。未决事项保留在“待细化”，不要当成已确认需求。
- 修改配置、端口或部署文件时，同步 `docs/DEPLOYMENT.md`、`.env.example`、根目录 `README.md` 和本文件中的相关约定。
- 手工验收以 `docs/ACCEPTANCE.md` 为准：P0 是上线必过项，SEC 不允许失败；核心流程变更后重跑对应条目，AI 回归优先使用本地 mock。
