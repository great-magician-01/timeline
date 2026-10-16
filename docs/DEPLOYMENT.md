# Timeline Docker 单机部署设计

> **文档状态**：设计基线，持续更新  
> **文档版本**：0.3.2  
> **最近更新**：2026-10-16  
> **需求基线**：[`REQUIREMENTS.md`](REQUIREMENTS.md) 0.21.0  
> **关联文档**：[接口设计](API.md)、[数据模型设计](DATA_MODEL.md)  
> **用途**：部署脚本与 Compose 实现、运维操作和验收用例的依据

## 1. 维护说明

1. 本文档描述部署结构与运维约定；具体文件（`Dockerfile`、`docker-compose.yml`、`.env.example`、备份脚本）在实现阶段按本文档落地。
2. 部署形态变化（新增容器、拆分服务、改用对象存储等）必须先更新 `REQUIREMENTS.md` 第 17 章，再更新本文档。
3. 每次更新同步版本、日期和文末变更记录。

## 2. 部署目标与约束（来自需求基线）

1. 单机 Docker 部署；**本部署只包含应用容器**（Go 后端 + 前端静态资源托管 + AI 后台任务能力）。
2. 前端在构建时编译，由 Go 后端直接托管，同源访问，不使用独立前端或 Nginx 容器。
3. **PostgreSQL 为独立实例**（另行部署的容器或独立主机），不属于本部署的 compose、镜像和生命周期；应用只通过连接配置访问。
4. 附件存本部署的本地持久化卷；PostgreSQL 数据由数据库实例自行管理；两边必须配套备份。
5. 数据库名从配置读取（`DB_NAME`），schema 固定为 `user` 与 `timeline`。
6. 全系统时区 `Asia/Shanghai`，数据库会话与应用一致。
7. 单应用实例，不拆分独立 worker；任务状态持久化在数据库，重启可恢复。
8. 数据库迁移由应用发布流程管理，迁移对象是外部数据库实例。
9. 非公开附件不能通过免鉴权静态路径访问；上传目录不作为静态站点目录暴露。
10. 提供健康检查、日志、备份说明；容器重建后数据不丢失。

## 3. 镜像设计

### 3.1 应用镜像（多阶段构建）

| 阶段 | 基础镜像 | 任务 |
| --- | --- | --- |
| 前端构建 | `node:22-alpine` | 安装依赖、执行 Vue 前端构建，产出静态产物 |
| 后端构建 | `golang:1.25-alpine` | 编译 Go 二进制；通过 `go:embed` 将前端产物嵌入二进制 |
| 运行镜像 | `alpine`（锁定版本） | 仅含二进制、ffmpeg、tzdata、CA 证书 |

运行镜像要求：

1. 安装 `ffmpeg`：视频封面提取的后台任务依赖它；同时需要 `tzdata` 保证 `Asia/Shanghai` 时区可用。
2. `CGO_ENABLED=0` 静态编译；可执行文件包含前端静态资源，镜像内不需要单独的 `dist` 目录。
3. 以非 root 用户运行（固定 UID/GID，如 10001），`/data/uploads` 由卷挂载且在部署清单中赋权。
4. 运行镜像不使用 `latest` 版本基础镜像，基础镜像版本在实现时锁定并记录。
5. 备选方案：如 ffmpeg 的 codec 兼容性在 alpine 上有问题，改用 `debian:*-slim` 基础镜像，其余不变。

### 3.2 数据库（外部独立实例，不属于本镜像与 compose）

1. 数据库**不包含在本部署的 compose 中**，不由本部署创建、停止、升级或迁移数据目录。
2. 建议使用官方 `postgres` 镜像并锁定主版本（如 16 或 17），但该选择由数据库实例的管理方决定，不影响应用代码。
3. 数据库本身与数据库账号需要**预先创建并授权**（`DB_NAME`、`DB_USER`、`DB_PASSWORD`）；应用迁移只创建两个 schema 和表结构，不创建数据库实例。
4. 数据库的数据目录、参数调优与实例级备份策略由该实例的管理方负责；本系统在第 8 章给出配套的 `pg_dump` 备份与恢复建议。
5. 应用镜像不包含 PostgreSQL 服务；数据库实例不包含应用业务代码或迁移产物。

## 4. Compose 结构（设计稿）

目录布局（仓库根目录）：

```
├── Dockerfile
├── docker-compose.yml
├── .env.example
├── deploy/
│   ├── data/
│   │   ├── uploads/         # 附件与衍生文件，首次部署前创建并赋权
│   │   └── logs/            # 应用日志，按天目录（yyyyMMdd）自动创建
│   └── scripts/
│       ├── backup.sh
│       └── restore.sh
└── ...
```

Compose 设计稿（实现时以此为基础）；compose 只管理应用容器：

```yaml
services:
  app:
    build:
      context: .
      dockerfile: Dockerfile
    image: timeline-app:${IMAGE_TAG:-latest}
    restart: unless-stopped
    environment:
      TZ: Asia/Shanghai
      DB_HOST: ${DB_HOST}
      DB_PORT: ${DB_PORT:-5432}
      DB_NAME: ${DB_NAME}
      DB_USER: ${DB_USER}
      DB_PASSWORD: ${DB_PASSWORD}
      DB_SSLMODE: ${DB_SSLMODE:-disable}
      UPLOAD_DIR: /data/uploads
      LOG_FILE: ${LOG_FILE:-/data/logs/app.log}
      COOKIE_SECURE: ${COOKIE_SECURE:-true}
      RUN_MIGRATIONS: ${RUN_MIGRATIONS:-true}
      SNOWFLAKE_WORKER_ID: ${SNOWFLAKE_WORKER_ID:-1}
      LOG_LEVEL: ${LOG_LEVEL:-info}
      LOG_FORMAT: ${LOG_FORMAT:-text}
    ports:
      - "${APP_PORT:-5070}:5070"
    volumes:
      - ./deploy/data/uploads:/data/uploads
      - ./deploy/data/logs:/data/logs
    healthcheck:
      test: ["CMD-SHELL", "wget -qO- http://127.0.0.1:5070/healthz || exit 1"]
      interval: 15s
      timeout: 5s
      retries: 5
      start_period: 30s
    stop_grace_period: 30s
    # 若数据库是同一宿主机上的另一个容器，按需添加：
    # extra_hosts: ["host.docker.internal:host-gateway"]
```

要点：

1. 使用绑定挂载（bind mount）而不是命名卷：单机场景便于直接查看、打包和备份附件目录。
2. 数据库生命周期完全由外部实例管理；compose 中不存在 `db` 服务，也不通过 `depends_on` 等待数据库。
3. 应用启动时对外部数据库按退避策略重试连接：连接失败输出明确日志；服务保持运行且 `/readyz` 持续返回 503 反映真实状态，数据库恢复后自动转为就绪（与验收 ENV-07 一致），避免反复重启的半可用状态。
4. `app` 的健康检查使用 `GET /healthz`（应用存活）；数据库连通性通过 `GET /readyz` 体现（详见 `API.md` 系统接口章节）。
5. 日志双通道：应用同时写文件日志（`LOG_FILE` 基础路径，按北京时间每天新建 `yyyyMMdd` 目录，跨天自动切换）和标准输出；标准输出通道继续使用默认 `json-file` 驱动并配置轮转上限（如 `max-size: 10m`、`max-file: 3`），文件通道由应用按天目录管理、部署方负责清理。
6. 应用容器时区为 `Asia/Shanghai`；应用连接数据库时同时显式设置会话时区，与数据库实例时区设置互相独立、双保险。
7. 数据库与本应用的网络可达性（同宿主机另一容器、局域网实例或云数据库）由部署方保证，通过 `DB_HOST` 配置；跨主机连接建议启用 SSL 并按实际调整 `DB_SSLMODE`。

## 5. 环境变量清单（`.env`）

`.env.example` 至少包含以下变量；实际值不进版本库：

| 变量 | 用途 | 默认 | 必填 |
| --- | --- | --- | --- |
| `TZ` | 容器时区 | `Asia/Shanghai` | 否 |
| `APP_PORT` | 宿主机映射端口（容器内监听同为 5070） | `5070` | 否 |
| `IMAGE_TAG` | 应用镜像标签，用于升级回滚 | `latest` | 否 |
| `DB_NAME` | 数据库名（不固定，从配置读取） | 无 | **是** |
| `DB_USER` | 数据库用户 | 无 | **是** |
| `DB_PASSWORD` | 数据库密码 | 无 | **是** |
| `DB_HOST` | 数据库实例地址（外部独立实例） | 无 | **是** |
| `DB_PORT` | 数据库端口 | `5432` | 否 |
| `DB_SSLMODE` | 数据库 SSL 模式，内网可用 `disable` | `disable` | 否 |
| `COOKIE_SECURE` | 会话 Cookie 是否带 Secure；HTTP 内网部署时须设为 `false` | `true` | 否 |
| `RUN_MIGRATIONS` | 启动时自动执行迁移 | `true` | 否 |
| `SNOWFLAKE_WORKER_ID` | 雪花 ID 工作节点号，单机固定值 | `1` | 否 |
| `LOG_LEVEL` | 日志级别 | `info` | 否 |
| `LOG_FORMAT` | 日志格式：`text` 或 `json` | `text` | 否 |
| `LOG_FILE` | 日志文件基础路径；实际写入 `<目录>/<yyyyMMdd>/<文件名>`（北京时间按天新建目录、跨天自动切换） | `/data/logs/app.log` | 否 |
| `UPLOAD_DIR` | 附件目录（容器内路径） | `/data/uploads` | 否 |

说明：

1. `DB_NAME`、`DB_USER`、`DB_PASSWORD`、`DB_HOST` 无默认值，未配置时应用拒绝启动并输出明确错误。
2. AI 模型接口的 base_url、API Key 和模型配置**不通过环境变量管理**，全部在管理后台的接口预设中维护，存数据库并由服务端加密保护。
3. 单文件大小、存储配额等业务限制首版以管理后台设置为准；服务器层级的请求体上限通过应用默认值控制，具体数值在实现时按“待细化”确定。
4. 数据库 schema 固定为 `timeline` 与 `user`，作为应用常量管理，不通过环境变量配置。

## 6. 启动、迁移与初始化

1. **启动顺序**：`app` 启动 → 连接外部数据库（失败则退避重试）→ 执行迁移（`RUN_MIGRATIONS=true` 时）→ 启动 HTTP 服务与后台任务。
2. **迁移并发保护**：迁移使用 PostgreSQL advisory lock，保证重复启动或异常重启不会并发执行迁移；单实例部署下天然安全。
3. **手动迁移**：`RUN_MIGRATIONS=false` 时，通过 `docker compose exec app /app/timeline migrate` 手动执行。
4. **管理员初始化**：提供引导命令 `docker compose exec app /app/timeline init-admin`，交互式输入用户名和密码创建首个管理员；不通过环境变量传密码，避免残留明文。
5. **注册开关**：默认开放注册（需求基线），上线后可在管理后台设置中关闭。

## 7. HTTPS 与 Cookie 安全

1. 首版 compose 默认 HTTP 直出，适用于内网或自带安全网关的环境。
2. HTTPS 处理方式（三选一，不属于当前 compose 范围）：
   - 宿主机或上游反向代理终止 TLS，转发到 `APP_PORT`；
   - 云厂商负载均衡终止 TLS；
   - 后续为应用增加证书直出能力（需要另行更新需求）。
3. `COOKIE_SECURE` 必须与实际访问协议一致：HTTPS 环境保持 `true`；纯 HTTP 内网部署必须设为 `false`，否则浏览器不会保存会话 Cookie。
4. 无论哪种方式，生产环境不允许通过公网明文 HTTP 暴露应用。

## 8. 数据持久化与备份

### 8.1 持久化范围

| 数据 | 位置 | 由谁管理 |
| --- | --- | --- |
| 附件与衍生文件 | 本部署 `deploy/data/uploads`（容器内 `/data/uploads`） | 本部署 |
| 应用日志 | 本部署 `deploy/data/logs`（容器内 `/data/logs`，按天 `yyyyMMdd` 目录） | 本部署，不随业务备份；由部署方按需清理 |
| PostgreSQL 全部业务数据 | 外部数据库实例的数据目录 | 数据库实例管理方 |

1. 应用镜像本身无状态，重建容器不影响任何数据。
2. 附件与数据库两份数据必须**配套备份、配套恢复**，单独恢复任意一份都会造成引用不一致。
3. 日志不参与业务一致性，不属于备份范围；按天目录便于按日期整目录归档或删除（如保留最近 30 天）。

### 8.2 备份（`deploy/scripts/backup.sh` 设计）

```sh
#!/bin/sh
set -e
DATE=$(date +%Y%m%d-%H%M%S)
DIR="backups/$DATE"
mkdir -p "$DIR"

# 1) 数据库：用一次性 postgres 容器执行 pg_dump，宿主机无需安装客户端
#    若 DB_HOST 是宿主机地址，视环境在 docker run 中增加 --network host
docker run --rm postgres:16-alpine \
  pg_dump -h "$DB_HOST" -p "${DB_PORT:-5432}" -U "$DB_USER" -d "$DB_NAME" \
  | gzip > "$DIR/db.sql.gz"

# 2) 附件：打包本部署的上传目录
tar -czf "$DIR/uploads.tar.gz" -C deploy/data uploads
```

1. 使用 `pg_dump` 逻辑备份外部数据库，运行中执行即可，无需停机；附件在数据库导出后打包，允许秒级轻微偏差（首版接受）。
2. 若数据库实例管理方已有实例级备份机制，本脚本的数据库部分可作为应用视角的补充；两者至少保留其一。
3. 提供 cron 示例：每日凌晨低峰期执行，保留最近 N 份（建议 7 份，具体在实现时写入脚本参数）。
4. 备份产物建议同步到另一台机器或外部存储；备份文件包含用户数据，注意保护。

### 8.3 恢复（`deploy/scripts/restore.sh` 设计）

1. 停止应用容器：`docker compose stop app`（避免恢复期间写入）。
2. 按数据库实例管理方的流程重建或清空目标数据库。
3. 恢复数据库：`gunzip -c db.sql.gz | docker run --rm -i postgres:16-alpine psql -h "$DB_HOST" -p "${DB_PORT:-5432}" -U "$DB_USER" -d "$DB_NAME"`。
4. 恢复附件：清空 `deploy/data/uploads` 后解包 `uploads.tar.gz`。
5. 启动应用：`docker compose start app`；检查 `/readyz` 与后台任务页面。
6. 恢复演练应在部署验收时至少完整执行一次。

## 9. 升级与回滚

1. 升级：`docker compose build app && docker compose up -d app`；启动时应用自动对外部数据库执行迁移，期间服务短暂不可用（单机部署可接受）。
2. 镜像通过 `IMAGE_TAG` 标记版本；回滚时指定上一版本标签重新 `up -d`。
3. 迁移约定：每个版本的迁移脚本应尽量与上一版本兼容（先加列后删列、分版本迁移），保证回滚旧镜像后旧代码可以运行；破坏性迁移必须单独记录并在升级说明中标注。
4. 升级前先做一次完整备份（第 8 章脚本）。

## 10. 日志、健康与排障

1. 日志有两个通道：标准输出用 `docker compose logs` 查看（配置轮转上限避免写满）；文件日志在 `deploy/data/logs/<yyyyMMdd>/` 下按天留档，可直接按日期目录排查与清理。
2. 健康检查：本部署只对 `app` 容器做健康检查（`/healthz`）；数据库连通性通过 `/readyz` 体现，数据库实例自身的可用性监控由实例管理方负责。
3. 排障入口：管理后台的“全局任务”页面查看失败任务（定时发布、媒体处理、记忆整理、AI 任务）及错误原因。
4. 附件目录的磁盘占用需要监控；数据库实例侧的磁盘与连接数由该实例管理方监控。

## 11. 首次部署检查清单

1. 确认外部数据库实例已就绪：数据库、账号已创建并授权，网络可达。应用账号需要对两个 schema 具备 USAGE 与 CREATE：`GRANT USAGE, CREATE ON SCHEMA timeline TO <应用账号>;` 与 `GRANT USAGE, CREATE ON SCHEMA "user" TO <应用账号>;`（若 schema 尚未创建，迁移器会在具备数据库级 CREATE 时自动创建）。
2. 复制 `.env.example` 为 `.env`，填写 `DB_NAME`、`DB_USER`、`DB_PASSWORD`、`DB_HOST` 及安全配置。
3. 创建 `deploy/data/uploads` 目录并赋予应用容器用户（UID 10001）写权限。
4. `docker compose build` 构建应用镜像。
5. `docker compose up -d` 启动，等待 app 容器健康；访问 `http://<host>:<APP_PORT>/` 确认前端加载，`GET /readyz` 返回正常。
6. 执行 `init-admin` 创建管理员并登录。
7. 配置模型接口预设和首个 AI（如需）。
8. 执行一次 `backup.sh`，确认数据库 dump 与附件包完整。
9. 按实际协议设置 `COOKIE_SECURE`，验证登录会话正常。

## 12. 待细化

- 应用镜像与 ffmpeg 的具体版本锁定值，及 alpine codec 验证结论。
- 应用容器资源限制（内存、CPU）的具体建议值。
- 日志轮转参数与备份保留份数。
- 服务器层级请求体上限的默认值。
- 数据库跨主机访问时的 SSL 参数与证书约定。
- 迁移工具选型（与数据模型文档的待细化一致）。

## 13. 变更记录

| 日期 | 版本 | 变更内容 |
| --- | --- | --- |
| 2026-10-16 | 0.1.0 | 基于需求 0.13.0 首次建立部署设计；确定多阶段应用镜像（前端嵌入 Go 二进制、含 ffmpeg 与非 root 运行）、独立 PostgreSQL 容器、绑定挂载数据目录、Compose 结构稿、环境变量清单、迁移与初始化流程、HTTPS 与 Cookie 约定、备份恢复与升级回滚、首次部署检查清单。 |
| 2026-10-16 | 0.1.1 | 需求基线更新至 0.14.0（验收用例文档建立，部署设计无实质变更）。 |
| 2026-10-16 | 0.2.0 | 明确 PostgreSQL 为外部独立实例：compose 只管理应用容器，移除 db 服务、depends_on 与数据库端口相关内容；改为应用启动重试连接；备份恢复改用一次性 postgres 容器对独立实例执行 pg_dump/psql；数据库库与账号需预先创建。 |
| 2026-10-16 | 0.2.1 | 需求基线更新至 0.16.0（界面图标约束为前端实现约定，本部署设计无实质变更）。 |
| 2026-10-16 | 0.2.2 | 需求基线更新至 0.17.0（快速发布框与主题切换为前端约定，本部署设计无实质变更）。 |
| 2026-10-16 | 0.2.3 | 需求基线更新至 0.18.0（callable 为纯 Go、最小依赖库，静态编译无影响；集成需在 go.mod 锁定版本，私有源场景使用 replace）。 |
| 2026-10-16 | 0.3.0 | 新增日志文件配置：`LOG_FILE` 基础路径，按北京时间每天 `yyyyMMdd` 目录写入、跨天自动切换；compose 增加 `deploy/data/logs` 挂载与环境变量；日志不参与业务备份，由部署方按日期清理。 |
| 2026-10-16 | 0.3.1 | 需求基线更新至 0.20.0（AI 调用记录落库，随数据库备份自动覆盖，无部署变更）。 |
| 2026-10-16 | 0.3.2 | 端口统一为 5070（宿主机映射与容器内监听一致）；构建镜像锁定 `golang:1.25-alpine`；compose 与环境变量表新增 `LOG_FORMAT`；数据库不可达时保持运行并以 `/readyz` 反映状态；检查清单补充 schema 授权命令。 |
