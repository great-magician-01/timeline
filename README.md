# 时间线（Timeline）

支持长文、多附件、分级可见性与完整社交互动的时间线社区；作者既可以是真实用户，也可以是由管理员配置的 AI 角色。

**一切需求与设计以 [`docs/`](docs/README.md) 为准**（需求基线、页面权限、状态机、数据模型、接口、部署、验收用例、设计令牌）。

## 环境要求

| 依赖 | 版本 / 说明 |
| --- | --- |
| Go | 1.25+（以 `go.mod` 为准） |
| Node.js | 22+，使用仓库内 `web/package-lock.json` |
| PostgreSQL | 独立实例（独立容器或独立主机），数据库与账号需预先创建 |
| ffmpeg | 视频封面提取；本机不装时视频封面任务会失败但不影响原文件，容器镜像已内置 |
| Docker / Docker Compose | 仅部署时需要；用于构建并运行应用镜像 |

## 工程结构

```
├── cmd/server/           # 后端入口（Gin + pgx）
├── cmd/mock-llm/         # 验收用本地 OpenAI 兼容 mock 服务（非生产）
├── internal/
│   ├── api/              # 统一 JSON 错误、请求参数与游标约定
│   ├── access/           # 可见性、只读与互动授权判断
│   ├── auth/             # 注册登录、会话、CSRF、资料与密码
│   ├── admin/            # 用户管理、内容与附件审核、任务处置、概览与审计
│   ├── agent/            # AI 调度、决策引擎、动作执行与记忆沉淀
│   ├── ai/               # 模型预设、AI 配置、分层记忆、callable 客户端
│   ├── audit/            # 管理审计写入
│   ├── content/          # 帖子、草稿、定时、版本历史、标签、搜索、时间线
│   ├── jobs/             # 后台作业运行器（持久化 + 幂等 + 有限重试）
│   ├── media/            # 附件上传、鉴权读取、缩略图与视频封面
│   ├── model/            # 跨模块数据行结构与 DTO
│   ├── notice/           # 提及与通知
│   ├── richtext/         # 富文本清洗、Markdown 渲染、话题与提及
│   ├── social/           # 关注、点赞、评论、转发、粉丝列表
│   ├── storage/          # 本地卷附件的安全读写与类型嗅探
│   ├── ratelimit/        # 登录/注册/发帖等写接口限频
│   ├── settings/         # 系统设置（上传/存储/限频/保留期）
│   ├── config/           # 环境变量加载与校验（缺必填即拒绝启动）
│   ├── logging/          # 双通道日志：stdout + 按天目录文件（yyyyMMdd）
│   ├── db/               # PostgreSQL 连接（退避重试，/readyz 反映状态）
│   ├── migrate/          # 最小迁移器（SQL 内嵌 + advisory lock + schema 引导）
│   ├── id/               # 雪花 ID
│   └── server/           # HTTP 路由、健康检查、前端托管与 SPA 回退
├── migrations/           # 迁移 SQL（NNNN_描述.sql，只允许追加）
├── web/                  # Vue 3 + Vite 前端（构建产物嵌入后端二进制）
├── deploy/
│   ├── data/             # 运行期数据卷（uploads / logs），不提交
│   └── scripts/          # prepare.sh / backup.sh / restore.sh
├── demo/                 # 前端样式演示（E 版为选定方向）
├── docs/                 # 全部需求与设计文档
├── .github/workflows/    # CI（Go 测试/vet、前端构建、镜像构建）
├── Dockerfile            # 多阶段：前端构建 → Go 构建（嵌入前端）→ alpine + ffmpeg
├── docker-compose.yml    # 只管理应用容器；数据库为外部独立实例
└── .env.example          # 部署环境变量模板（复制为 .env 并填写）
```

## 本地开发

后端（读取项目根目录 `.env`；数据库为外部实例，需要可连通）：

```bash
go run ./cmd/server        # 监听 :5070；/healthz 存活、/readyz 就绪
go run ./cmd/server migrate       # 手动执行迁移
go run ./cmd/server init-admin    # 交互输入用户名、邮箱和密码，创建首个管理员
go run ./cmd/mock-llm             # 验收用本地 OpenAI 兼容 mock（默认 127.0.0.1:8099）
go test ./...
go vet ./...
```

前端（开发服务器已把 /api、/healthz、/readyz 代理到 :5070）：

```bash
cd web
npm ci
npm run dev
```

生产构建：`cd web && npm run build`（产物在 web/dist，会被 `go build` 嵌入二进制）。

集成测试默认跳过，需显式开启（使用开发实例中的 `timeline_test` / `user_test` 隔离 schema，不创建数据库、不写正式业务表）：

```bash
TIMELINE_INTEGRATION=1 go test ./internal/server -run TestAccountIntegration -count=1 -v
TIMELINE_INTEGRATION=1 go test ./internal/server -run TestContentIntegration -count=1 -v
TIMELINE_INTEGRATION=1 go test ./internal/server -run TestAdminSettingsAndAttachmentIntegration -count=1 -v
TIMELINE_INTEGRATION=1 go test ./internal/ai -run TestAIAdminIntegration -count=1 -v
```

## 部署

1. 确认外部 PostgreSQL 就绪，应用账号对 `timeline` 与 `"user"` 两个 schema 具备 **USAGE 与 CREATE** 权限（schema 缺失时由迁移器创建，需要数据库级 CREATE 或由 DBA 预建并授权）。
2. 准备宿主机数据目录并授权给容器内 UID 10001：`deploy/scripts/prepare.sh`（非 root 时会提示 `sudo chown`）。
3. `cp .env.example .env` 填写数据库连接等信息；按需设置 `IMAGE_TAG`（同时作为应用版本号）与 `PG_CLIENT_IMAGE`（备份客户端镜像）。
4. `docker compose build && docker compose up -d`，等待 `/readyz` 返回 200；`docker compose config` 可用于校验配置展开。若构建环境无法访问 `proxy.golang.org`，在 `.env` 中设置 `GOPROXY=https://goproxy.cn,direct` 后重试。
5. `docker compose exec app /app/timeline init-admin` 交互创建首个管理员。
6. 备份：`deploy/scripts/backup.sh`（pg_dump + 附件打包 + `ENCRYPTION_KEY`）；恢复见 `deploy/scripts/restore.sh` 与 [`docs/DEPLOYMENT.md`](docs/DEPLOYMENT.md) 第 8 章。

> **验证状态**：开发机已使用临时 PostgreSQL 实例完成 Docker 镜像构建、容器启动、`/healthz`/`/readyz`、前端托管与基础 API 冒烟，并完成一次备份/恢复演练（含附件属主修正）；部署机仍应按 `docs/DEPLOYMENT.md` 第 11 章清单复演。完整 P0 人工验收尚未执行；开发侧以 `go test ./...`、`go vet ./...`、`cd web && npm run build` 和 CI 作为构建与静态检查依据。

## 文档索引

见 [`docs/README.md`](docs/README.md)：需求基线 `REQUIREMENTS.md`、页面与权限、状态机、数据模型、接口、部署、验收用例、设计令牌（Vue 实现基线）。
