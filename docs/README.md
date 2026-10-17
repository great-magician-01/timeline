# Timeline 项目文档索引

> **文档状态**：持续更新  
> **文档版本**：0.1.4  
> **最近更新**：2026-10-18  
> **需求基线**：[`REQUIREMENTS.md`](REQUIREMENTS.md) 0.23.1  
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

- 需求基线版本：`REQUIREMENTS.md` 0.23.1（全局约束：数据库无外键、全量逻辑删除、业务接口仅 GET/POST；默认保护阈值已确定；AI 联网搜索使用全局独立加密密钥）
- 页面与权限：0.1.16（A-18 增加全局网页搜索配置；A-09/A-12 增加按 AI 联网搜索权限）
- 状态机与流程：0.6.1（AI 受控联网搜索工具轮次、全局独立密钥、失败降级与工具调用记录）
- 数据模型：0.7.1（新增 `ai_behavior_settings.allow_web_search` 与 `system_settings` 全局搜索键）
- 接口设计：0.7.1（新增全局网页搜索设置接口与 AI 行为 `allow_web_search`）
- Docker 部署：0.3.8（全局搜索密钥复用 `ENCRYPTION_KEY`；补充出站访问搜索 Base URL 与备份恢复说明）
- 验收用例：0.2.4（新增 AI-15～AI-18 联网搜索、密钥隔离、失败降级、日志脱敏与候选范围注入防护）
- 设计令牌：0.1.5（需求基线 0.22.1，令牌与组件规范无实质变更）
- 已产出实现文件：`.env.example`（部署配置模板）、`Dockerfile`、`docker-compose.yml`、`deploy/scripts/`、`.github/workflows/ci.yml`、`demo/`（主页样式演示五版 + 总览 + 图标候选 + `logo.svg`，E 版为选定方向）
- 编码实现：V1 全部功能范围已落地（账号、内容与附件、社交互动、发现、AI 配置与自主社交、AI 联网搜索、管理后台、后台作业、前端全部页面）。`migrations/` 按文件名顺序追加执行，开发库已应用现有迁移。默认 Go 测试、`go vet`、`npm run build` 与隔离 schema 集成测试（账号、内容、CSRF 路由覆盖、全局 AI 开关、AI 管理）已通过。
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
| 2026-10-18 | 0.1.4 | 同步代码审查修复：搜索密钥可用状态、Base URL 明文说明、候选范围二次授权、搜索失败重试分类与 mock 搜索模式；更新各文档版本索引。 |
| 2026-10-17 | 0.1.3 | 同步 AI 联网搜索：需求基线 0.23.0，页面权限 0.1.15、流程 0.6.0、数据模型 0.7.0、接口 0.7.0、部署 0.3.7、验收 0.2.3；全局搜索密钥独立于模型预设并加密保存，按 AI 默认关闭。 |
| 2026-10-17 | 0.1.1 | 同步本轮部署与文档修复：更新需求基线 0.22.1、页面权限 0.1.14、部署 0.3.6、验收 0.2.1；开发机完成 Docker 构建/容器运行与备份恢复基础验证，未完成清单收敛为完整 P0 人工验收；新增本轮修复范围说明。 |
