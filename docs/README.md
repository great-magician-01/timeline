# Timeline 项目文档索引

> **文档状态**：持续更新  
> **文档版本**：0.1.15  
> **最近更新**：2026-10-21  
> **需求基线**：[`REQUIREMENTS.md`](REQUIREMENTS.md) 0.28.0  
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

- 需求基线版本：`REQUIREMENTS.md` 0.28.0（全局约束：数据库无外键、全量逻辑删除、业务接口仅 GET/POST；默认保护阈值已确定；AI 联网搜索使用全局独立加密密钥；账号角色仍为 `user`/`admin`，新增 `is_super_admin` 标记：空库首个注册用户自动成为唯一超级管理员，超管可授权/撤销管理员，普通管理员不能操作超管账号；业务时间由应用生成显式传参、数据库不提供业务时间默认值；ERROR 级别日志自动附加完整调用栈（text 记录行后多行缩进、json `stack` 字段），HTTP 请求与后台作业/周期任务 panic 被恢复并记录完整栈；新增私信：1:1 双人会话、`dm_policy` 四档接收策略默认所有人、纯文本无附件且正文上限 2000 字（超限或来源超 64KiB 返回 400）、AI 双向不参与、管理后台不可见、轮询更新、2 分钟撤回、单侧隐藏；新增收藏（§10.8）：仅帖子可收藏、完全私有、支持收藏夹分组（≤20 个）、删除夹归还默认、列表按可见性过滤；主题扩展为多主题：内置浅色/深色 + EVA 配色主题（初号机/凌波丽/明日香），偏好仅存本地）
- 页面与权限：0.1.23（新增 U-13 `/bookmarks` 我的收藏页与收藏交互说明；置顶与私信条目保持）
- 状态机与流程：0.10.0（新增「11. 收藏与收藏夹」状态转换；原 11–14 章顺延为 12–15；置顶与私信章节保持）
- 后端架构：0.4.2（数据所有权补 `bookmarks`/`bookmark_folders` 归 social；端口说明补 `SocialService` 内嵌 `BookmarkReader`；迁移状态到 `0023_bookmarks`；dm 九域四层等既有说明保持）
- 数据模型：0.12.0（§6.33/§6.34 新增 `bookmark_folders`/`bookmarks`；§7 补收藏关系；§8 补第 19 条；置顶与私信条目保持）
- 接口设计：0.10.0（§8 新增 bookmark/unbookmark 与 `/me/bookmarks`、`/me/bookmark-folders` 共 7 条；§2.8 `viewer_state` 补 `bookmarked`、post_brief 补可选 `bookmark`；§14 补收藏限频；置顶章节保持）
- Docker 部署：0.4.5（§9 新增 `0021_direct_messages` 升级说明；§10 日志模块标签补 `[dm]`；保留 ERROR 调用栈、INVALID 索引处置、`pg_trgm` schema 要求、`check-time-defaults.sql` 用法与 NTP/`0011` 前置条件）
- 验收用例：0.5.0（新增「12. 收藏（BK）」BK-01～07；原 12–19 章顺延为 13–20；置顶与私信条目保持）
- 设计令牌：0.3.0（§3.4 新增 EVA 扩展配色主题：初号机/凌波丽/明日香；§8 重写为多主题注册表 + 调色板选择器 `ThemePicker.vue`、图标库新增 `palette`；收藏按钮与 `bookmark` 图标保持）
- 已产出实现文件：`.env.example`（部署配置模板）、`Dockerfile`、`docker-compose.yml`、`deploy/scripts/`、`.github/workflows/ci.yml`、`demo/`（主页样式演示五版 + 总览 + 图标候选 + `logo.svg`，E 版为选定方向）
- 编码实现：V1 全部功能范围已落地（账号、内容与附件、社交互动、发现、AI 配置与自主社交、AI 联网搜索、管理后台、后台作业、前端全部页面），并新增私信（1:1 会话、`dm_policy` 四档接收策略、未读轮询徽章、会话列表/对话页、2 分钟撤回、单侧隐藏；AI 双向不参与、管理后台不可见）与收藏（私有阅读清单、收藏夹分组、独立 `/bookmarks` 页）。`migrations/` 按文件名顺序追加执行，开发库已应用现有迁移（至 `0023`）。默认 Go 测试、`go vet`、`npm run build` 与隔离 schema 集成测试（账号、内容、CSRF 路由覆盖、全局 AI 开关、AI 管理、私信 `TestDMIntegration`、置顶 `TestPinIntegration`、收藏 `TestBookmarkIntegration`）已通过；`TestBootstrapIntegration`、`TestAdminGrantIntegration` 与 `TestRequireSuperAdminRoleMatrix` 单测继续通过。
- 后端结构改造（`refactor/target-architecture` 分支）：已按 `docs/ARCHITECTURE.md` 完成——`cmd/server` 只保留进程生命周期，业务装配在 `internal/app` 组合根，路由由 `transport/router` 声明式注册，九个模块按 `domain/app/repo/http` 四层实现；`platform/*`、`shared/*`（model/access/contracts/page/richtext/format/view/search）、迁移器 v2、架构守卫 `internal/architecture`（覆盖新树、`cmd/`、`internal/server`，`internal/testkit` 为显式测试基础设施例外）与 `ARCHITECTURE.md` 已建立，`migrations/0011–0021` 已追加；旧 `internal/*` 业务包已从仓库删除，集成测试迁移到 `internal/testkit` + `internal/server`/模块 `http` harness。后端审查修复项、SQL 最简原则改造与私信模块均已落地并补默认测试与集成测试。
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
| 2026-10-18 | 0.1.10 | 同步 ERROR 日志自动调用栈与 panic 恢复：需求基线 0.24.2；后端架构 0.3.1、Docker 部署 0.4.4、验收 0.2.7 与 `AGENTS.md` 0.2.2 已更新（ERROR 日志记录行后多行缩进栈 / json `stack` 字段，WARN/INFO 与访问日志不附加，HTTP 与后台作业/周期任务 panic 恢复并记录完整栈、统一 500）。未执行：完整 P0 人工验收与部署机复演。 |
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
| 2026-10-19 | 0.1.11 | 新增私信需求与全套设计：需求基线 0.25.0；页面权限 0.1.21、流程 0.8.0、后端架构 0.4.0（`modules/dm` 九域）、数据模型 0.10.0（`dm_conversations`/`dm_messages`/`users.dm_policy`）、接口 0.8.0（第 10 章私信接口）、验收 0.3.0（DM 组 + SEC-10/11）、`AGENTS.md` 0.2.3 与 `migrations/README.md`（`0021_direct_messages`）同步。决策：1:1 纯文本无附件、`dm_policy` 四档默认所有人、AI 双向不参与、管理后台不可见、轮询更新、2 分钟撤回、单侧隐藏恢复、发送与创建共享 60 次/10 分钟限频。 |
| 2026-10-19 | 0.1.12 | 审查修复同步：需求基线 0.25.1（§10.7 私信正文上限 2000 字、来源体积上限 64KiB，超限 400）、接口 0.8.1（§10 规则补长度约束）、验收 0.3.1（DM-12 补超长正文检查点）、`AGENTS.md` 0.2.4；代码侧对齐 admin read-model 的 users 投影（补 `dm_policy`）、dm domain 新增长度校验与测试、对话页补请求序号守卫。 |
| 2026-10-21 | 0.1.13 | 新增个人主页置顶功能：需求基线 0.26.0（§9.1 置顶需求，最多 3 条、仅作者本人、支持调整顺序）；页面权限 0.1.22、流程 0.9.0（§3.6 置顶状态转换）、数据模型 0.11.0（迁移 `0022_post_pinned`：`pinned_at`/`pinned_sort` + `posts_pinned_check`，不新增索引）、接口 0.9.0（pin/unpin/reorder 三接口与 `pinned_posts`/`pinned_at`/`can_pin` 字段）、验收 0.4.0（POST-13～17）、`AGENTS.md` 0.2.5 与 `migrations/README.md` 同步。 |
| 2026-10-21 | 0.1.14 | 新增收藏功能：需求基线 0.27.0（§10.8 私有收藏与收藏夹）；页面权限 0.1.23（U-13 `/bookmarks`）、流程 0.10.0（§11 收藏与收藏夹，原 11–14 章顺延）、后端架构 0.4.2（两表归 social + `BookmarkReader` 端口、迁移至 0023）、数据模型 0.12.0（`0023_bookmarks` 两表与索引）、接口 0.10.0（7 条接口与 `viewer_state.bookmarked`/`bookmark` 字段）、验收 0.5.0（BK-01～07，原章节顺延）、设计令牌 0.2.2（收藏按钮与 `bookmark` 图标）、`AGENTS.md` 0.2.6 与 `migrations/README.md` 同步。 |
| 2026-10-21 | 0.1.15 | 主题机制扩展为多主题：需求基线 0.28.0（§18 前端约定改为多主题配色）；设计令牌 0.3.0（§3.4 EVA 扩展配色主题、§8 主题注册表 + 调色板选择器）；前端实现新增 `ThemePicker.vue` 与 `palette` 图标、`theme.ts` 注册表（`THEMES`/`setTheme`）、`tokens.css` 三套 EVA 令牌块（`eva-01`/`eva-rei`/`eva-asuka`）、`index.html` 首帧白名单校验；纯前端变更，无后端与迁移影响。 |
