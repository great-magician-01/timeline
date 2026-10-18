# Timeline 项目文档索引

> **文档状态**：持续更新  
> **文档版本**：0.1.8  
> **最近更新**：2026-10-18  
> **需求基线**：[`REQUIREMENTS.md`](REQUIREMENTS.md) 0.24.1  
> **用途**：文档入口、版本索引与当前实现状态

所有需求与设计决策以本目录文档为准，聊天记录不作为最终依据。各文档版本与更新日期见各自文件头部，变更历史见各自文末。

| 文档 | 内容 | 何时需要更新 |
| --- | --- | --- |
| [REQUIREMENTS.md](REQUIREMENTS.md) | 需求规格说明书：功能范围、核心决策、部署约束、验收口径 | 任何需求新增、修改、暂缓或删除 |
| [PAGES_AND_PERMISSIONS.md](PAGES_AND_PERMISSIONS.md) | 页面清单、导航结构、角色权限矩阵、后端授权核对点 | 页面增删、权限规则变化 |
| [WORKFLOWS.md](WORKFLOWS.md) | 核心状态机与流程：草稿、定时发布、关注审批、评论删除、媒体处理、AI 决策、记忆、通知、账号停用 | 业务流程或状态转换变化 |
| [ARCHITECTURE.md](ARCHITECTURE.md) | 后端目标架构：模块化单体、modules 四层、platform/shared/transport、依赖方向、数据所有权、端口/contracts、事务/错误/时间规范、路由策略组、架构守卫规则、迁移状态 | 分层、依赖边界、模块划分或架构规范变化 |
| [DATA_MODEL.md](DATA_MODEL.md) | 数据模型：实体、字段方向、关系、索引、主键与 schema 约定、时区约定 | 表结构或数据约定变化 |
| [API.md](API.md) | 接口设计：通用约定、错误码、认证、全部分组接口清单 | 接口路径、约定或权限变化 |
| [DEPLOYMENT.md](DEPLOYMENT.md) | Docker 单机部署：镜像构建、Compose 结构、环境变量、初始化（首个账号）、备份恢复、升级回滚 | 部署形态、运维流程或环境约定变化 |
| [ACCEPTANCE.md](ACCEPTANCE.md) | 验收用例：部署、认证、内容、媒体、权限、社交、AI、记忆、安全、运维恢复的执行清单（P0/P1） | 需求变化后增删对应用例 |
| [DESIGN_SYSTEM.md](DESIGN_SYSTEM.md) | 前端设计令牌：颜色、字阶、间距、GitHub 式按钮规范、图标规范、主题机制、动态元素审计 | UI 规范或设计方向变化 |

## 维护规则

1. 需求变化先更新 `REQUIREMENTS.md`，再同步受影响的设计文档。
2. 每次更新修改对应文档的版本号、日期和文末变更记录。
3. 文档间约定冲突时，以 `REQUIREMENTS.md` 的需求基线为准，并立即修正设计文档。
4. 尚未确定的事项记录在文档的“待细化”章节，不在开发时默认已确认。

## 当前状态

（版本号以各文档头部为准；更新任何文档时同步此列表。）

- 需求基线版本：`REQUIREMENTS.md` 0.24.1（全局约束：数据库无外键、全量逻辑删除、业务接口仅 GET/POST；默认保护阈值已确定；AI 联网搜索使用全局独立加密密钥；账号角色仍为 `user`/`admin`，新增 `is_super_admin` 标记：空库首个注册用户自动成为唯一超级管理员，超管可授权/撤销管理员，普通管理员不能操作超管账号；业务时间由应用生成显式传参、数据库不提供业务时间默认值）
- 页面与权限：0.1.18（§8 明确授权实现位置为「路由 Policy + shared/access 纯策略 + app 端口」，权限矩阵与行为不变）
- 状态机与流程：0.7.1（补充「一次事务一个 now」与「时间比较在应用侧、事务边界归 app」）
- 后端架构：0.3.0（目标架构重构完成并清理旧包：`cmd/server` → `internal/app` 组合根 → `transport/router` 声明式路由 → `modules/*` 八域四层；旧 `internal/*` 业务包已删除、集成测试迁移到 `internal/testkit`；架构守卫覆盖新树、`cmd/`、`internal/server` 与 `internal/testkit` 例外，并新增 DB 原语/遗留符号规则；迁移 0011–0020 与本次审查行为修复已同步；SQL 最简原则改造新增 `shared/search` 纯函数辅助包）
- 数据模型：0.9.0（`users.is_super_admin`、单超管与超管保护；§4.4 时间权威在应用侧、数据库不再提供业务时间默认值；迁移 0011–0016 含 43 个时间默认值移除、`users.avatar_attachment_id` 与头像回填、时间线/任务/附件索引、`pg_trgm` 搜索索引、删除未使用与不匹配索引；迁移 0017–0020 新增查询索引批次、删除冗余索引、`posts/reposts.author_is_ai` 快照列、`attachment_usage` 配额冗余表；个人主页统计收敛为 3 项单表计数）
- 接口设计：0.7.4（修复 §2.6 错误码表被实现说明段落截断的格式缺陷；补充就绪前 `/api/*` 503 边界；接口契约不变）
- Docker 部署：0.4.2（新增 `CREATE INDEX CONCURRENTLY` 失败后 INVALID 索引处置、`pg_trgm` 需安装到 `search_path` 内 schema、`0016_review_fixes` 与 `0017`–`0020` 升级说明、`deploy/scripts/check-time-defaults.sql` 只读核查用法；保留原有 NTP/时钟同步要求与 `0011` 升级前置条件）
- 验收用例：0.2.6（ENV-03 改为核对 API `+08:00` 与服务器时钟；新增 §2.5 结构改造后回归项：路由/CSRF/事务/时间/架构守卫）
- 设计令牌：0.1.9（需求基线 0.22.1，令牌与组件规范无实质变更）
- 已产出实现文件：`.env.example`（部署配置模板）、`Dockerfile`、`docker-compose.yml`、`deploy/scripts/`、`.github/workflows/ci.yml`、`demo/`（主页样式演示五版 + 总览 + 图标候选 + `logo.svg`，E 版为选定方向）
- 编码实现：V1 全部功能范围已落地（账号、内容与附件、社交互动、发现、AI 配置与自主社交、AI 联网搜索、管理后台、后台作业、前端全部页面），本轮新增超级管理员：空库首注册自动成为唯一超管、超管授权/撤销管理员、普通管理员不能操作超管账号。`migrations/` 按文件名顺序追加执行，开发库已应用现有迁移。默认 Go 测试、`go vet`、`npm run build` 与隔离 schema 集成测试（账号、内容、CSRF 路由覆盖、全局 AI 开关、AI 管理）已通过；本轮新增的 `TestBootstrapIntegration`（空库首注册/并发唯一超管/空库忽略注册开关）、`TestAdminGrantIntegration`（授权/撤销/超管保护/审计/幂等）与 `TestRequireSuperAdminRoleMatrix` 单测已在开发库隔离 schema 实际运行通过。
- 后端结构改造（`refactor/target-architecture` 分支）：已按 `docs/ARCHITECTURE.md` 完成——`cmd/server` 只保留进程生命周期，业务装配在 `internal/app` 组合根，路由由 `transport/router` 声明式注册，八个模块按 `domain/app/repo/http` 四层实现；`platform/*`、`shared/*`（model/access/contracts/page/richtext/format/view/search）、迁移器 v2、架构守卫 `internal/architecture`（覆盖新树、`cmd/`、`internal/server`，`internal/testkit` 为显式测试基础设施例外）与 `ARCHITECTURE.md` 已建立，`migrations/0011–0020` 已追加；旧 `internal/*` 业务包已从仓库删除，集成测试迁移到 `internal/testkit` + `internal/server`/模块 `http` harness。本次后端审查修复已落地并补默认测试，覆盖 AI 候选范围/目标作者二次授权、任务自动重试与 reconcile、记忆沉淀与来源失效、联网搜索有限工具轮次、发帖限频兜底、作业重激活与人工重试、审计单写、资料统计口径、自评论排除、附件清理两步流程、登录标识索引写法、架构守卫扩展与 `0016`–`0020` 迁移等。
- 未完成：`docs/ACCEPTANCE.md` 中 P0 清单的完整人工验收（含本轮新增的初始化与角色授权用例）。开发机已用临时 PostgreSQL 实例完成 Docker 镜像构建、容器启动、健康检查/前端托管/基础 API 冒烟，以及一次备份/恢复演练（含附件属主修正）；部署机仍应按 `docs/DEPLOYMENT.md` 第 11 章复演。

## 2026-10-17 部署与文档修复范围

本轮修复针对上一轮审计发现的部署/文档漂移，范围如下：

1. `Dockerfile`：运行镜像预创建 `/data/uploads` 与 `/data/logs` 并 `chown 10001:10001`；新增 `ARG APP_VERSION=dev`，通过 `-ldflags "-X main.appVersion=..."` 注入版本；保留 ffmpeg/tzdata/CA、非 root 与 `EXPOSE 5070`。
2. `docker-compose.yml`：`build.args.APP_VERSION` 取 `IMAGE_TAG`；标准输出增加 `json-file` 日志轮转（`max-size: 10m`、`max-file: 3`）；仍只管理 `app` 服务，外部 PostgreSQL 与 healthcheck 不变。
3. `deploy/scripts/prepare.sh`：创建并授权宿主机数据目录，可重复执行；首次部署清单已引用。
4. `deploy/scripts/backup.sh`：新增 `PG_CLIENT_IMAGE`（默认 `postgres:17-alpine`）、独立备份 `ENCRYPTION_KEY`（`encryption-key.txt`，权限 600、不回显）、产物非空与压缩包校验、保留份数与 cron 说明。
5. `deploy/scripts/restore.sh`：使用 `PG_CLIENT_IMAGE`，增加目标库版本检查、恢复确认、附件属主/权限恢复；旧附件目录先移走而不是直接 `rm -rf`。
6. `.env.example`：补充 `PG_CLIENT_IMAGE`、备份密钥、目录准备与系统设置默认值说明。
7. `.github/workflows/ci.yml`：Go 1.25 测试/`vet`、Node 22 前端构建、`docker build`（不启动容器），启用 Go/npm 缓存。
8. 文档：同步 `README.md`、`AGENTS.md`、`docs/README.md`、`PAGES_AND_PERMISSIONS.md`、`DEPLOYMENT.md`、`REQUIREMENTS.md`、`ACCEPTANCE.md` 的版本、Compose 示例、外部数据库操作、默认阈值与未验证状态。

## 变更记录

| 日期 | 版本 | 变更内容 |
| --- | --- | --- |
| 2026-10-18 | 0.1.9 | SQL 最简原则全仓审查与改造落地（批次 1–3）：后端架构 0.3.0（迁移 0017–0020、`shared/search` 辅助包、数据所有权说明）；数据模型 0.9.0（`0017`/`0018` 索引增删、`0019` `author_is_ai` 快照列与 `attachment_usage` 配额冗余表及回填、`0020` 作者类型索引、个人主页统计收敛为 3 项单表计数、§8/§10 同步）；Docker 部署 0.4.2（`0017`–`0020` 升级运维说明）；`migrations/README.md` 与 `AGENTS.md` 同步。未执行项：真实数据库 `EXPLAIN` 基线与对账、完整 P0 人工验收与部署机复演。 |
| 2026-10-18 | 0.1.8 | 文档同步（重构完成 + 本次行为修复）：后端架构 0.2.0（旧包已删除、集成测试迁移 `internal/testkit`、守卫覆盖 cmd/testkit 与新增规则、迁移 0016、本次行为修复清单）；数据模型 0.8.1（`0016` 头像回填与 `ai_tasks_daily` 索引删除）；接口 0.7.4（修复 §2.6 错误码表截断，契约不变）；Docker 部署 0.4.1（INVALID 索引处置、`pg_trgm` schema 要求、`0016`、`check-time-defaults.sql` 用法）；`docs/README.md` 状态行同步为“旧包已删除、架构守卫全绿、默认与集成测试通过”，删除“待 G5 删除/守卫失败项未清零”过期描述；`AGENTS.md` 0.2.0 与 `migrations/README.md` 同步。完整 P0 人工验收仍未执行。 |
| 2026-10-18 | 0.1.7 | 文档版本索引同步：需求基线 0.24.1、页面权限 0.1.18、流程 0.7.1、后端架构 0.1.1、接口 0.7.3、部署 0.4.0、验收 0.2.6（数据模型 0.8.0 不变）；`ARCHITECTURE.md` 需求基线同步 0.24.1；更新后端结构改造进度（组合根 `internal/app` 与 `transport/router` 已接管运行路径，旧包退出运行路径待删除）；`AGENTS.md` 同步 0.1.1（legacy 标注与目标架构表述修正）。 |
| 2026-10-18 | 0.1.6 | 新增 `ARCHITECTURE.md` 0.1.0（后端目标架构权威：模块化单体、模块四层、依赖方向、数据所有权、端口/contracts、事务/错误/时间规范、路由策略组、架构守卫、迁移状态），文档索引加入该文档；同步数据模型 0.8.0（§4.4 应用侧时间权威、迁移 0011-0015）与后端结构改造进度（新树/旧包 cutover、迁移器 v2）。 |
| 2026-10-18 | 0.1.5 | 需求基线更新至 0.24.0；同步超级管理员改造：页面权限 0.1.17、流程 0.7.0、数据模型 0.7.2、接口 0.7.2、部署 0.3.9、验收 0.2.5；索引状态说明补充首注册超管、授权/撤销管理员、超管保护与存量不迁移；设计令牌版本按实际文件修正为 0.1.9。 |
| 2026-10-16 | 0.1.0 | 首次建立文档索引：维护规则、文档清单与当前状态；明确需求基线与各设计文档的更新顺序。 |
| 2026-10-17 | 0.1.2 | 同步开发机 Docker/备份恢复验证结果、验收 0.2.2 与部署 0.3.6；更新状态机/接口/数据模型版本索引。 |
| 2026-10-18 | 0.1.4 | 同步代码审查修复：搜索密钥可用状态、Base URL 明文说明、候选范围二次授权、搜索失败重试分类与 mock 搜索模式；更新各文档版本索引。 |
| 2026-10-17 | 0.1.3 | 同步 AI 联网搜索：需求基线 0.23.0，页面权限 0.1.15、流程 0.6.0、数据模型 0.7.0、接口 0.7.0、部署 0.3.7、验收 0.2.3；全局搜索密钥独立于模型预设并加密保存，按 AI 默认关闭。 |
| 2026-10-17 | 0.1.1 | 同步本轮部署与文档修复：更新需求基线 0.22.1、页面权限 0.1.14、部署 0.3.6、验收 0.2.1；开发机完成 Docker 构建/容器运行与备份恢复基础验证，未完成清单收敛为完整 P0 人工验收；新增本轮修复范围说明。 |
