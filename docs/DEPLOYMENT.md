# Timeline Docker 单机部署

> **文档状态**：部署基线，与当前实现同步  
> **文档版本**：0.3.8  
> **最近更新**：2026-10-18  
> **需求基线**：[`REQUIREMENTS.md`](REQUIREMENTS.md) 0.23.1  
> **关联文档**：[接口设计](API.md)、[数据模型设计](DATA_MODEL.md)、[验收用例](ACCEPTANCE.md)  
> **用途**：部署脚本与 Compose 实现、运维操作和验收用例的依据

## 1. 维护说明

1. 本文档描述部署结构与运维约定；`Dockerfile`、`docker-compose.yml`、`.env.example`、`deploy/scripts/` 已按本文档落地。文档与文件不一致时，先以实际文件为准修正本文档，再检查需求基线。
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
| 运行镜像 | `alpine:3.20`（锁定版本） | 仅含二进制、ffmpeg、tzdata、CA 证书 |

运行镜像要求：

1. 安装 `ffmpeg`：视频封面提取的后台任务依赖它；同时需要 `tzdata` 保证 `Asia/Shanghai` 时区可用，并安装 CA 证书供模型接口 HTTPS 调用。
2. `CGO_ENABLED=0` 静态编译；可执行文件包含前端静态资源，镜像内不需要单独的 `dist` 目录。
3. 以非 root 用户运行（固定 UID/GID `10001`）。镜像内预创建 `/data/uploads` 与 `/data/logs` 并 `chown 10001:10001`，宿主机绑定目录由 `prepare.sh` 准备。
4. 构建参数 `APP_VERSION`（默认 `dev`）通过 `-ldflags "-X main.appVersion=${APP_VERSION}"` 注入二进制；Compose 的 `build.args.APP_VERSION` 取 `IMAGE_TAG`，启动日志输出 `version` 字段，便于确认实际运行版本。
5. 运行镜像基础版本在 Dockerfile 中锁定，不使用 `latest`。
6. 备选方案：如 ffmpeg 的 codec 兼容性在 alpine 上有问题，改用 `debian:*-slim` 基础镜像，其余不变。

### 3.2 数据库（外部独立实例，不属于本镜像与 compose）

1. 数据库**不包含在本部署的 compose 中**，不由本部署创建、停止、升级或迁移数据目录。
2. 建议使用官方 `postgres` 镜像并锁定主版本（如 16 或 17），但该选择由数据库实例的管理方决定，不影响应用代码。
3. 数据库本身与数据库账号需要**预先创建并授权**（`DB_NAME`、`DB_USER`、`DB_PASSWORD`）；应用迁移只创建两个 schema 和表结构，不创建数据库实例。
4. 数据库的数据目录、参数调优与实例级备份策略由该实例的管理方负责；本系统在第 8 章给出配套的 `pg_dump` 备份与恢复建议。
5. 应用镜像不包含 PostgreSQL 服务；数据库实例不包含应用业务代码或迁移产物。

## 4. Compose 结构

目录布局（仓库根目录）：

```
├── Dockerfile
├── docker-compose.yml
├── .env.example
├── .github/
│   └── workflows/
│       └── ci.yml            # Go 测试/vet、前端构建、镜像构建（不启动容器）
├── deploy/
│   ├── data/
│   │   ├── uploads/         # 附件与衍生文件，首次部署前由 prepare.sh 创建并赋权
│   │   └── logs/            # 应用日志，按天目录（yyyyMMdd）自动创建
│   └── scripts/
│       ├── prepare.sh       # 创建并授权数据目录（可重复执行）
│       ├── backup.sh
│       └── restore.sh
└── ...
```

`docker-compose.yml` 当前内容（compose 只管理应用容器）：

```yaml
services:
  app:
    build:
      context: .
      dockerfile: Dockerfile
      args:
        APP_VERSION: ${IMAGE_TAG:-latest}
        GOPROXY: ${GOPROXY:-https://proxy.golang.org,direct}
    image: timeline-app:${IMAGE_TAG:-latest}
    restart: unless-stopped
    env_file:
      - .env
    environment:
      TZ: Asia/Shanghai
      DB_HOST: ${DB_HOST}
      DB_PORT: ${DB_PORT:-5432}
      DB_NAME: ${DB_NAME}
      DB_USER: ${DB_USER}
      DB_PASSWORD: ${DB_PASSWORD}
      DB_SSLMODE: ${DB_SSLMODE:-disable}
      UPLOAD_DIR: ${UPLOAD_DIR:-/data/uploads}
      LOG_FILE: ${LOG_FILE:-/data/logs/app.log}
      LOG_LEVEL: ${LOG_LEVEL:-info}
      LOG_FORMAT: ${LOG_FORMAT:-text}
      COOKIE_SECURE: ${COOKIE_SECURE:-true}
      TRUSTED_PROXIES: ${TRUSTED_PROXIES:-}
      RUN_MIGRATIONS: ${RUN_MIGRATIONS:-true}
      SNOWFLAKE_WORKER_ID: ${SNOWFLAKE_WORKER_ID:-1}
    ports:
      - "${APP_PORT:-5070}:5070"
    volumes:
      - ./deploy/data/uploads:/data/uploads
      - ./deploy/data/logs:/data/logs
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
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

1. 使用绑定挂载（bind mount）而不是命名卷：单机场景便于直接查看、打包和备份附件目录。宿主机目录由 `deploy/scripts/prepare.sh` 创建并 `chown 10001:10001`。
2. 数据库生命周期完全由外部实例管理；compose 中不存在 `db` 服务，也不通过 `depends_on` 等待数据库。
3. 应用启动时对外部数据库按退避策略重试连接：连接失败输出明确日志；服务保持运行且 `/readyz` 持续返回 503 反映真实状态，数据库恢复后自动转为就绪（与验收 ENV-07 一致），避免反复重启的半可用状态。
4. `app` 的健康检查使用 `GET /healthz`（应用存活）；数据库连通性通过 `GET /readyz` 体现（详见 `API.md` 系统接口章节）。
5. 日志双通道：应用同时写文件日志（`LOG_FILE` 基础路径，按北京时间每天新建 `yyyyMMdd` 目录，跨天自动切换）和标准输出；标准输出通道使用 `json-file` 驱动并配置轮转（`max-size: 10m`、`max-file: 3`），文件通道由应用按天目录管理、部署方负责清理。
6. 应用容器时区为 `Asia/Shanghai`；应用连接数据库时同时显式设置会话时区，与数据库实例时区设置互相独立、双保险。
7. 数据库与本应用的网络可达性（同宿主机另一容器、局域网实例或云数据库）由部署方保证，通过 `DB_HOST` 配置；跨主机连接建议启用 SSL 并按实际调整 `DB_SSLMODE`。
8. `env_file: .env` 为容器注入全部配置；`environment` 中列出的变量使用 `.env` 同名值或默认值，二者冲突时 `environment` 优先。`TZ`、`APP_PORT`、`IMAGE_TAG`、`PG_CLIENT_IMAGE` 属于 compose/脚本层变量，应用进程本身不读取它们。
9. 版本注入：`IMAGE_TAG` 同时作为镜像标签与 `APP_VERSION` 构建参数；升级/回滚时指定不同 `IMAGE_TAG` 重新构建或拉取即可。

## 5. 环境变量清单（`.env`）

`.env.example` 至少包含以下变量；实际值不进版本库：

| 变量 | 用途 | 默认 | 必填 |
| --- | --- | --- | --- |
| `TZ` | 容器时区 | `Asia/Shanghai` | 否 |
| `APP_PORT` | 宿主机映射端口（容器内监听固定为 5070） | `5070` | 否 |
| `IMAGE_TAG` | 应用镜像标签，同时作为 `APP_VERSION` 构建参数与升级回滚依据 | `latest` | 否 |
| `GOPROXY` | 构建镜像时的 Go 模块代理；受限网络可改为 `https://goproxy.cn,direct` | `https://proxy.golang.org,direct` | 否 |
| `PG_CLIENT_IMAGE` | `pg_dump`/`psql` 一次性容器使用的 PostgreSQL 客户端镜像 | `postgres:17-alpine` | 否（备份/恢复脚本使用） |
| `DB_NAME` | 数据库名（不固定，从配置读取） | 无 | **是** |
| `DB_USER` | 数据库用户 | 无 | **是** |
| `DB_PASSWORD` | 数据库密码 | 无 | **是** |
| `DB_HOST` | 数据库实例地址（外部独立实例） | 无 | **是** |
| `DB_PORT` | 数据库端口 | `5432` | 否 |
| `DB_SSLMODE` | 数据库 SSL 模式，内网可用 `disable` | `disable` | 否 |
| `COOKIE_SECURE` | 会话 Cookie 是否带 Secure；HTTP 内网部署时须设为 `false` | `true` | 否 |
| `TRUSTED_PROXIES` | 受信反向代理 IP/CIDR，逗号分隔；留空表示不信任转发头 | 空 | 否 |
| `ENCRYPTION_KEY` | 模型接口预设与全局独立网页搜索密钥的加密密钥；留空时由数据库连接信息派生 | 空 | 否 |
| `RUN_MIGRATIONS` | 启动时自动执行迁移 | `true` | 否 |
| `SNOWFLAKE_WORKER_ID` | 雪花 ID 工作节点号，单机固定值 | `1` | 否 |
| `LOG_LEVEL` | 日志级别 | `info` | 否 |
| `LOG_FORMAT` | 日志格式：`text` 或 `json` | `text` | 否 |
| `LOG_FILE` | 日志文件基础路径；实际写入 `<目录>/<yyyyMMdd>/<文件名>`（北京时间按天新建目录、跨天自动切换） | `/data/logs/app.log` | 否 |
| `UPLOAD_DIR` | 附件目录（容器内路径） | `/data/uploads` | 否 |

说明：

1. `DB_NAME`、`DB_USER`、`DB_PASSWORD`、`DB_HOST` 无默认值，未配置时应用拒绝启动并输出明确错误。
2. AI 模型接口的 base_url、API Key 和模型配置**不通过环境变量管理**，全部在管理后台的接口预设中维护；模型 API Key 使用 AES-256-GCM 加密保护（密钥来自 `ENCRYPTION_KEY`；未配置时由数据库连接信息派生），Base URL 与模型配置为明文保存。全局网页搜索的 Base URL 与独立 API Key 同样在管理后台“系统设置”中维护；**Base URL 为明文配置，独立 API Key 才使用同一个 `ENCRYPTION_KEY` 加密**，且不复用任何模型预设密钥，因此任意 OpenAI 兼容且支持工具调用的模型都可以使用。更换 `ENCRYPTION_KEY` 后模型 API Key 与搜索 API Key 都无法解密，需要在管理后台重新填写。备份脚本会把该密钥单独写入 `encryption-key.txt`（权限 600，不回显）。
3. 单文件大小、单请求大小、存储配额、发帖与上传限频、临时附件保留期、调用记录截断上限与保留期、全局网页搜索 Base URL 与独立密钥均以管理后台“系统设置”为准，修改后即时生效。代码内默认值：单文件 100 MiB、单请求 101 MiB（100 MiB + 1 MiB multipart 余量）、每用户存储 5 GiB、发帖 30 次/10 分钟、上传 60 次/10 分钟、临时附件保留 7 天、调用记录截断 64 KiB、保留 90 天。
4. 数据库 schema 固定为 `timeline` 与 `user`，作为应用常量管理，不通过环境变量配置。
5. `TZ`、`APP_PORT`、`IMAGE_TAG`、`PG_CLIENT_IMAGE` 由 Compose 或部署脚本使用，不传入 Go 应用进程；应用监听端口固定为 5070。
6. 开启 AI 联网搜索前需确认应用容器可出站访问配置的搜索 Base URL（默认 `https://api.deepseek.com`，用于 `/v1/tools/search`）。网络不可达时搜索会以工具错误降级，不阻塞 AI 的其他决策；不需要搜索的部署应在每个 AI 的行为策略中保持关闭。

## 6. 启动、迁移与初始化

1. **启动顺序**：`app` 加载配置并启动 HTTP 存活检查 → 后台连接外部数据库（失败则退避重试）→ 执行迁移（`RUN_MIGRATIONS=true` 时）→ 数据库与迁移就绪后 `/readyz` 返回 200。
2. **迁移并发保护**：迁移使用 PostgreSQL advisory lock，保证重复启动或异常重启不会并发执行迁移；单实例部署下天然安全。
3. **手动迁移**：`RUN_MIGRATIONS=false` 时，通过 `docker compose exec app /app/timeline migrate` 手动执行。
4. **管理员初始化**：通过 `docker compose exec app /app/timeline init-admin` 交互式输入用户名、邮箱和密码创建首个管理员；不通过环境变量传密码，避免残留明文。命令会先确保迁移完成，且已有管理员时拒绝重复初始化。
5. **注册开关**：默认开放注册（需求基线），上线后可在管理后台设置中关闭。

## 7. HTTPS 与 Cookie 安全

1. 首版 compose 默认 HTTP 直出，适用于内网或自带安全网关的环境。
2. HTTPS 处理方式（三选一，不属于当前 compose 范围）：
   - 宿主机或上游反向代理终止 TLS，转发到 `APP_PORT`；
   - 云厂商负载均衡终止 TLS；
   - 后续为应用增加证书直出能力（需要另行更新需求）。
3. `COOKIE_SECURE` 必须与实际访问协议一致：HTTPS 环境保持 `true`；纯 HTTP 内网部署必须设为 `false`，否则浏览器不会保存会话 Cookie。
4. 无论哪种方式，生产环境不允许通过公网明文 HTTP 暴露应用。
5. 经过反向代理时，`TRUSTED_PROXIES` 必须只填写实际代理的 IP 或 CIDR，应用才会采用 `X-Forwarded-For` / `X-Real-IP` 中的客户端地址用于日志与限频；留空可防伪造，但代理拓扑下所有访客会被视为同一个代理 IP，可能共同触发限频。禁止配置 `0.0.0.0/0` 或 `::/0`，否则任何访客都能伪造 IP 绕过限频。

## 8. 数据持久化与备份

### 8.1 持久化范围

| 数据 | 位置 | 由谁管理 |
| --- | --- | --- |
| 附件与衍生文件 | 本部署 `deploy/data/uploads`（容器内 `/data/uploads`） | 本部署 |
| 应用日志 | 本部署 `deploy/data/logs`（容器内 `/data/logs`，按天 `yyyyMMdd` 目录） | 本部署，不随业务备份；由部署方按需清理 |
| 模型与搜索密钥加密密钥（`ENCRYPTION_KEY`） | 部署机 `.env`；备份时写入 `backups/<时间>/encryption-key.txt` | 部署方，必须与数据库备份配套保管 |
| PostgreSQL 全部业务数据 | 外部数据库实例的数据目录 | 数据库实例管理方 |

1. 应用镜像本身无状态，重建容器不影响任何数据。
2. 附件与数据库两份数据必须**配套备份、配套恢复**，单独恢复任意一份都会造成引用不一致；`ENCRYPTION_KEY` 必须与数据库备份一并保管，否则已保存的模型接口密钥和全局网页搜索密钥都无法解密。
3. 日志不参与业务一致性，不属于备份范围；按天目录便于按日期整目录归档或删除（如保留最近 30 天）。

### 8.2 备份（`deploy/scripts/backup.sh`）

脚本从项目根目录读取 `.env`（进程环境变量优先），校验 `DB_HOST`、`DB_USER`、`DB_NAME`、`DB_PASSWORD` 后执行：

1. **数据库**：用一次性 `PG_CLIENT_IMAGE` 容器（默认 `postgres:17-alpine`）执行 `pg_dump` 并 gzip 到 `backups/<yyyyMMdd-HHMMSS>/db.sql.gz`，宿主机无需安装 PostgreSQL 客户端。若 `DB_HOST` 是宿主机地址，视环境在 `docker run` 中增加 `--network host`。
2. **附件**：`tar -czf uploads.tar.gz -C deploy/data uploads`。
3. **加密密钥**：把 `ENCRYPTION_KEY` 写入同一备份目录的 `encryption-key.txt`，文件权限 600；未配置时写空文件并输出警告。脚本不回显密钥。
4. **校验**：检查三个产物均存在、`db.sql.gz` 与 `uploads.tar.gz` 非空，并分别执行 `gzip -t` 与 `tar -tzf` 验证可解压。
5. **权限**：备份目录权限 700，数据库 dump 与附件包权限 600。

示例产物：

```text
backups/20261017-030000/
├── db.sql.gz
├── uploads.tar.gz
└── encryption-key.txt   # 权限 600
```

保留份数与调度：

1. 脚本**不自动删除旧备份**，避免误删；建议保留最近 7 份（按 `backups/` 下的日期目录判断），由部署方另行归档或清理。
2. cron 示例（每日 03:00 低峰执行，输出追加到日志文件）：

   ```cron
   0 3 * * * cd /srv/timeline && deploy/scripts/backup.sh >> deploy/data/logs/backup.log 2>&1
   ```

3. `PG_CLIENT_IMAGE` 的 PostgreSQL 主版本必须不低于目标数据库主版本，否则 `pg_dump` 会拒绝导出；数据库升级后同步修改该变量。
4. 备份产物包含用户数据和 `ENCRYPTION_KEY`，应同步到另一台机器或受控的外部存储，并限制访问权限。
5. 若数据库实例管理方已有实例级备份机制，本脚本的数据库部分可作为应用视角的补充；两者至少保留其一。

### 8.3 恢复（`deploy/scripts/restore.sh`）

用法：`deploy/scripts/restore.sh backups/<日期时间>`。脚本流程：

1. 校验备份目录中 `db.sql.gz` 与 `uploads.tar.gz` 存在且非空。
2. 读取 `.env` 与 `PG_CLIENT_IMAGE`（默认 `postgres:17-alpine`），连接目标数据库执行 `SHOW server_version_num`；取 `psql --version` 的主版本比较。**客户端主版本低于目标数据库时中止**并提示改用 `PG_CLIENT_IMAGE=postgres:<主版本>-alpine`。因此目标数据库必须已由实例管理方重建或清空，且网络可达。
3. 若备份中存在非空 `encryption-key.txt`，与当前 `ENCRYPTION_KEY` 比较；不一致时警告（只比较，不回显密钥）。恢复后已保存的模型接口密钥将无法解密，需要重新填写。
4. 打印目标 `数据库@主机:端口/库名` 与附件目录，要求输入 `yes` 确认；无人值守恢复演练可设置 `RESTORE_YES=1` 跳过。
5. `docker compose stop app` 停止应用容器（避免恢复期间写入）。
6. `gunzip -c db.sql.gz | psql ... -v ON_ERROR_STOP=1` 恢复数据库。
7. 恢复附件：先把现有 `deploy/data/uploads` 移动到 `deploy/data/uploads.restore-old-<时间戳>`（**不直接 `rm -rf`**，便于回退），再解包 `uploads.tar.gz` 到 `deploy/data`。
8. 恢复附件属主与权限：以 root 执行时直接 `chown -R 10001:10001 deploy/data/uploads`；非 root 且可访问 Docker 时，用一次性 `PG_CLIENT_IMAGE` 容器修正属主与权限（目录 755、文件 644）；两者都不可用时打印对应 `sudo` 命令，由部署方执行。
9. 启动应用：`docker compose start app`；检查 `/readyz` 与后台任务页面。确认恢复无误后，由部署方自行清理 `uploads.restore-old-*` 临时目录。
10. 恢复演练应在部署验收时至少完整执行一次。

## 9. 升级与回滚

1. 升级：设置新的 `IMAGE_TAG` 后执行 `docker compose build app && docker compose up -d app`；`IMAGE_TAG` 同时作为镜像标签与 `APP_VERSION` 注入应用，启动日志的 `version` 字段可用于确认版本。启动时应用自动对外部数据库执行迁移，期间服务短暂不可用（单机部署可接受）。
2. 镜像通过 `IMAGE_TAG` 标记版本；回滚时指定上一版本标签重新构建/启动。
3. 迁移约定：每个版本的迁移脚本应尽量与上一版本兼容（先加列后删列、分版本迁移），保证回滚旧镜像后旧代码可以运行；破坏性迁移必须单独记录并在升级说明中标注。
4. 升级前先做一次完整备份（第 8 章脚本），确认 `db.sql.gz`、`uploads.tar.gz` 与 `encryption-key.txt` 均已生成且非空。

## 10. 日志、健康与排障

1. 日志有两个通道：标准输出用 `docker compose logs` 查看（配置轮转上限避免写满）；文件日志在 `deploy/data/logs/<yyyyMMdd>/` 下按天留档，可直接按日期目录排查与清理。
2. 健康检查：本部署只对 `app` 容器做健康检查（`/healthz`）；数据库连通性通过 `/readyz` 体现，数据库实例自身的可用性监控由实例管理方负责。
3. 排障入口：管理后台的“全局任务”页面查看失败任务（定时发布、媒体处理、记忆整理、AI 任务）及错误原因。
4. 附件目录的磁盘占用需要监控；数据库实例侧的磁盘与连接数由该实例管理方监控。

## 11. 首次部署检查清单

1. 确认外部数据库实例已就绪：数据库、账号已创建并授权，网络可达。应用账号需要对两个 schema 具备 USAGE 与 CREATE：`GRANT USAGE, CREATE ON SCHEMA timeline TO <应用账号>;` 与 `GRANT USAGE, CREATE ON SCHEMA "user" TO <应用账号>;`（若 schema 尚未创建，迁移器会在具备数据库级 CREATE 时自动创建）。
2. 复制 `.env.example` 为 `.env`，填写 `DB_NAME`、`DB_USER`、`DB_PASSWORD`、`DB_HOST` 及安全配置；按需设置 `IMAGE_TAG` 与 `PG_CLIENT_IMAGE`。
3. 执行 `deploy/scripts/prepare.sh` 创建并授权 `deploy/data/uploads` 与 `deploy/data/logs`（属主 UID/GID 10001）；非 root 时按提示执行 `sudo chown -R 10001:10001 ...`。
4. `docker compose config` 校验 Compose 展开，然后 `docker compose build` 构建应用镜像。
5. `docker compose up -d` 启动，等待 app 容器健康；访问 `http://<host>:<APP_PORT>/` 确认前端加载，`GET /readyz` 返回正常。
6. 执行 `docker compose exec app /app/timeline init-admin` 创建管理员并登录。
7. 配置模型接口预设和首个 AI（如需），在系统设置中确认上传/存储/限频/保留期等默认值。
8. 执行一次 `backup.sh`，确认 `db.sql.gz`、`uploads.tar.gz` 非空，且 `encryption-key.txt` 权限为 600。
9. 按实际协议设置 `COOKIE_SECURE`，验证登录会话正常。
10. 在独立环境执行一次 `restore.sh` 恢复演练（OPS-02），确认附件文件可下载、`/readyz` 正常。

## 12. 待细化

以下事项不影响当前部署可用性，需在部署机上验证或由运维方确定：

- 应用容器资源限制（内存、CPU）的具体建议值。
- 数据库跨主机访问时的 SSL 参数与证书约定。
- 运行镜像在部署机上的 ffmpeg 编解码兼容性验证（使用 `alpine:3.20` 自带版本）；如不满足再切换 `debian:*-slim`。
- 备份产物的异地保留周期与监控告警接入方式。

已确定项（不再作为待细化）：容器监听端口固定 5070；镜像标签与版本注入使用 `IMAGE_TAG`/`APP_VERSION`；标准输出日志轮转 `10m × 3`；备份默认客户端镜像 `postgres:17-alpine`、建议保留 7 份；系统设置默认阈值见第 5 章。

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
| 2026-10-17 | 0.3.3 | 同步已实现的 `migrate` 与 `init-admin` 命令：首个管理员交互输入用户名、邮箱和密码；启动顺序明确为 HTTP 存活检查先启动、数据库与迁移后台就绪；新增 `TRUSTED_PROXIES` 以在反向代理下保留真实客户端 IP；需求基线更新至 0.22.0。 |
| 2026-10-17 | 0.3.4 | 新增 `ENCRYPTION_KEY`：模型接口预设的 API Key 使用 AES-256-GCM 加密存储，未配置时由数据库连接信息派生并输出启动告警；说明更换密钥后需要重新填写预设密钥。 |
| 2026-10-18 | 0.3.8 | 修正文档：搜索与模型 Base URL 均为明文配置，只有 API Key 使用 ENCRYPTION_KEY 加密；补充搜索密钥不可用状态与出站要求。 |
| 2026-10-17 | 0.3.7 | 新增全局独立网页搜索配置：密钥由管理后台维护、复用 `ENCRYPTION_KEY` 加密但不复用模型预设密钥；补充应用容器访问搜索 Base URL 的出站要求与备份恢复说明；需求基线更新至 0.23.0。 |
| 2026-10-17 | 0.3.6 | 构建阶段支持 `GOPROXY` 构建参数（受限网络可用镜像代理），并在 Compose 与 `.env.example` 暴露；恢复脚本在非 root 时可用一次性容器修正附件属主。 |
| 2026-10-17 | 0.3.5 | 与实现同步：Dockerfile 预创建并授权 `/data/uploads`、`/data/logs`，新增 `APP_VERSION` 版本注入；Compose 增加 `build.args`、`env_file`、`TRUSTED_PROXIES` 与 `json-file` 日志轮转，示例与真实文件一致；新增 `prepare.sh`；备份参数化 `PG_CLIENT_IMAGE`、增加 `encryption-key.txt` 与产物校验；恢复增加目标库版本检查、确认提示、附件属主/权限恢复与旧目录保留（避免直接 `rm -rf`）；固定端口 5070、确定默认保护阈值与备份保留建议；需求基线更新至 0.22.1。 |
| 2026-10-16 | 0.3.2 | 端口统一为 5070（宿主机映射与容器内监听一致）；构建镜像锁定 `golang:1.25-alpine`；compose 与环境变量表新增 `LOG_FORMAT`；数据库不可达时保持运行并以 `/readyz` 反映状态；检查清单补充 schema 授权命令。 |
