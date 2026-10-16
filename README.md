# 时间线（Timeline）

支持长文、多附件、分级可见性与完整社交互动的时间线社区；作者既可以是真实用户，也可以是由管理员配置的 AI 角色。

**一切需求与设计以 [`docs/`](docs/README.md) 为准**（需求基线、页面权限、状态机、数据模型、接口、部署、验收用例、设计令牌）。

## 工程结构

```
├── cmd/server/           # 后端入口（Gin + pgx）
├── internal/
│   ├── config/           # 环境变量加载与校验（缺必填即拒绝启动）
│   ├── logging/          # 双通道日志：stdout + 按天目录文件（yyyyMMdd）
│   ├── db/               # PostgreSQL 连接（退避重试，/readyz 反映状态）
│   ├── migrate/          # 最小迁移器（SQL 内嵌 + advisory lock + schema 引导）
│   ├── id/               # 雪花 ID
│   └── server/           # HTTP：/healthz、/readyz、前端托管与 SPA 回退
├── migrations/           # 迁移 SQL（NNNN_描述.sql，只允许追加）
├── web/                  # Vue 3 + Vite 前端（构建产物嵌入后端二进制）
├── deploy/
│   ├── data/             # 运行期数据卷（uploads / logs），不提交
│   └── scripts/          # backup.sh / restore.sh
├── demo/                 # 前端样式演示（E 版为选定方向）
├── docs/                 # 全部需求与设计文档
├── Dockerfile            # 多阶段：前端构建 → Go 构建（嵌入前端）→ alpine + ffmpeg
├── docker-compose.yml    # 只管理应用容器；数据库为外部独立实例
└── .env.example          # 部署环境变量模板（复制为 .env 并填写）
```

## 本地开发

后端（读取项目根目录 `.env`；数据库为外部实例，需要可连通）：

```bash
go run ./cmd/server        # 监听 :5070；/healthz 存活、/readyz 就绪
go test ./...
```

前端（开发服务器已把 /api、/healthz、/readyz 代理到 :5070）：

```bash
cd web
npm install
npm run dev
```

生产构建：`cd web && npm run build`（产物在 web/dist，会被 `go build` 嵌入二进制）。

## 部署

1. 确认外部 PostgreSQL 就绪，应用账号对 `timeline` 与 `"user"` 两个 schema 具备 **USAGE 与 CREATE** 权限（schema 缺失时由迁移器创建，需要数据库级 CREATE 或由 DBA 预建并授权）。
2. `cp .env.example .env` 填写数据库连接等信息。
3. `docker compose build && docker compose up -d`，等待 `/readyz` 返回 200。
4. 备份：`deploy/scripts/backup.sh`（pg_dump + 附件打包）。

> Docker 构建与部署在另一台机器上进行验证；本仓库开发机已完成 Go/前端构建与本地冒烟测试。

## 文档索引

见 [`docs/README.md`](docs/README.md)：需求基线 `REQUIREMENTS.md`、页面与权限、状态机、数据模型、接口、部署、验收用例、设计令牌（Vue 实现基线）。
