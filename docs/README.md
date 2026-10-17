# Timeline 项目文档索引

> **文档状态**：持续更新  
> **文档版本**：0.1.2  
> **最近更新**：2026-10-17  
> **需求基线**：[`REQUIREMENTS.md`](REQUIREMENTS.md) 0.22.1  
> **用途**：文档入口、版本索引与当前实现状态

所有需求与设计决策以本目录文档为准，聊天记录不作为最终依据。各文档版本与更新日期见各自文件头部，变更历史见各自文末。

| 文档 | 内容 | 何时需要更新 |
| --- | --- | --- |
| [REQUIREMENTS.md](REQUIREMENTS.md) | 需求规格说明书：功能范围、核心决策、部署约束、验收口径 | 任何需求新增、修改、暂缓或删除 |
| [PAGES_AND_PERMISSIONS.md](PAGES_AND_PERMISSIONS.md) | 页面清单、导航结构、角色权限矩阵、后端授权核对点 | 页面增删、权限规则变化 |
| [WORKFLOWS.md](WORKFLOWS.md) | 核心状态机与流程：草稿、定时发布、关注审批、评论删除、媒体处理、AI 决策、记忆、通知、账号停用 | 业务流程或状态转换变化 |
| [DATA_MODEL.md](DATA_MODEL.md) | 数据模型：实体、字段方向、关系、索引、主键与 schema 约定、时区约定 | 表结构或数据约定变化 |
| [API.md](API.md) | 接口设计：通用约定、错误码、认证、全部分组接口清单 | 接口路径、约定或权限变化 |
| [DEPLOYMENT.md](DEPLOYMENT.md) | Docker 单机部署：镜像构建、Compose 结构、环境变量、迁移初始化、备份恢复、升级回滚 | 部署形态、运维流程或环境约定变化 |
| [ACCEPTANCE.md](ACCEPTANCE.md) | 验收用例：部署、认证、内容、媒体、权限、社交、AI、记忆、安全、运维恢复的执行清单（P0/P1） | 需求变化后增删对应用例 |
| [DESIGN_SYSTEM.md](DESIGN_SYSTEM.md) | 前端设计令牌：颜色、字阶、间距、GitHub 式按钮规范、图标规范、主题机制、动态元素审计 | UI 规范或设计方向变化 |

## 维护规则

1. 需求变化先更新 `REQUIREMENTS.md`，再同步受影响的设计文档。
2. 每次更新修改对应文档的版本号、日期和文末变更记录。
3. 文档间约定冲突时，以 `REQUIREMENTS.md` 的需求基线为准，并立即修正设计文档。
4. 尚未确定的事项记录在文档的“待细化”章节，不在开发时默认已确认。

## 当前状态

（版本号以各文档头部为准；更新任何文档时同步此列表。）

- 需求基线版本：`REQUIREMENTS.md` 0.22.1（全局约束：数据库无外键、全量逻辑删除、业务接口仅 GET/POST；默认保护阈值已确定）
- 页面与权限：0.1.14（新增 A-20 管理附件页；A-18 补充上传/存储/限频/调用记录设置；U-04 失败状态；A-12/A-14 定时策略与定时发布）
- 状态机与流程：0.5.1（动作前二次开关校验、调用记录逐次成条、记忆来源关联、AI 手动定时发布）
- 数据模型：0.6.4（新增 `ai_memory_sources`、`ai_tasks.scheduled_at`；调用记录按 HTTP 尝试成条）
- 接口设计：0.6.0（公开认证配置、系统设置扩展、管理附件、任务记忆与调用记录 task_id、AI 定时发布）
- Docker 部署：0.3.6（Compose 与真实文件一致、日志轮转、目录准备脚本、备份密钥与恢复权限、GOPROXY 构建参数）
- 验收用例：0.2.2（外部数据库操作、系统设置与限频配额、管理附件页用例、开发机验证记录）
- 设计令牌：0.1.5（需求基线 0.22.1，令牌与组件规范无实质变更）
- 已产出实现文件：`.env.example`（部署配置模板）、`Dockerfile`、`docker-compose.yml`、`deploy/scripts/`、`.github/workflows/ci.yml`、`demo/`（主页样式演示五版 + 总览 + 图标候选 + `logo.svg`，E 版为选定方向）
- 编码实现：V1 全部功能范围已落地（账号、内容与附件、社交互动、发现、AI 配置与自主社交、管理后台、后台作业、前端全部页面）。`migrations/` 按文件名顺序追加执行，开发库已应用现有迁移。默认 Go 测试、`go vet`、`npm run build` 与隔离 schema 集成测试（账号、内容、CSRF 路由覆盖、全局 AI 开关、AI 管理）已通过。
- 未完成：`docs/ACCEPTANCE.md` 中 P0 清单的完整人工验收。开发机已用临时 PostgreSQL 实例完成 Docker 镜像构建、容器启动、健康检查/前端托管/基础 API 冒烟，以及一次备份/恢复演练（含附件属主修正）；部署机仍应按 `docs/DEPLOYMENT.md` 第 11 章复演。

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
| 2026-10-16 | 0.1.0 | 首次建立文档索引：维护规则、文档清单与当前状态；明确需求基线与各设计文档的更新顺序。 |
| 2026-10-17 | 0.1.2 | 同步开发机 Docker/备份恢复验证结果、验收 0.2.2 与部署 0.3.6；更新状态机/接口/数据模型版本索引。 |
| 2026-10-17 | 0.1.1 | 同步本轮部署与文档修复：更新需求基线 0.22.1、页面权限 0.1.14、部署 0.3.6、验收 0.2.1；开发机完成 Docker 构建/容器运行与备份恢复基础验证，未完成清单收敛为完整 P0 人工验收；新增本轮修复范围说明。 |
