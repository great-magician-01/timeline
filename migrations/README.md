# 数据库迁移

> **最近更新**：2026-10-18（随 `0016_review_fixes` 同步）
> 权威设计见 [`docs/DATA_MODEL.md`](../docs/DATA_MODEL.md) 与 [`docs/ARCHITECTURE.md`](../docs/ARCHITECTURE.md) §9；部署侧执行/回滚见 [`docs/DEPLOYMENT.md`](../docs/DEPLOYMENT.md) 第 6、9 章。

## 约定

- 命名规范：`NNNN_描述.sql`（四位数序号，按文件名排序执行），只允许追加，**不得修改已应用的文件**。
- 执行方式：应用启动时（`RUN_MIGRATIONS=true`）自动执行，使用 PostgreSQL advisory lock 防并发；也可手动执行（`go run ./cmd/server migrate`，容器内为 `/app/timeline migrate`）。
- 语句限制：迁移器 v2 仍按 `;` 简单分句逐条执行——**不要写函数体、DO 块等含分号的复合语句**，需要时先升级迁移器。
- **`no-transaction` 指令**：文件首行写 `-- timeline:no-transaction` 时，该文件不包事务、逐条在连接上执行，用于 `CREATE INDEX CONCURRENTLY` 等不能在事务内执行的语句（本仓库 `0013`、`0014`）。此类文件必须幂等（`CREATE ... IF NOT EXISTS`、`DROP ... IF EXISTS`）；失败时迁移器不记录版本、不自动回滚，需人工核对后重跑；`CONCURRENTLY` 失败留下的 INVALID 索引处置见 `docs/DEPLOYMENT.md` 第 6 章。
- 职责边界：两个 schema（`timeline`、`user`）由迁移器启动引导负责创建（缺失才建）；迁移文件负责表结构，以及经评审的必要数据回填。
- 语句红线：所有 DDL 显式带 schema 前缀；`user` 是保留字，必须写成 `"user"`；不建外键约束；不写业务时间默认值（`0011` 起）；SQL 不出现 `now()`/`CURRENT_DATE`/`::date`/`interval`（迁移基础设施白名单除外）。`constraints_test.go` 会断言迁移不含外键与物理删除（`REFERENCES`/`FOREIGN KEY`/`DELETE FROM`/`DROP TABLE`/`TRUNCATE`），并对 `0016` 的头像回填契约做专项断言（精确正则、只更新 `avatar_attachment_id`、不更新 `updated_at`、不出现数据库时间函数、只删索引）；新增迁移后必须全绿。
- 已应用版本记录在 `timeline.schema_migrations` 表；迁移默认只做结构变更、不回填业务数据，回填属于需单独评审的例外，必须在文件头注释说明依据、幂等条件与回滚影响，并同步 `docs/DATA_MODEL.md`。
- **本目录的 README.md 是 go:embed 的恒存锚点，请勿删除**：embed 模式为 `all:*`，保证目录中没有 `.sql` 文件时也能编译（否则报 "pattern all:*.sql: no matching files found"）。

## 迁移概览

| 版本 | 内容 |
| --- | --- |
| `0001`–`0010` | 基线 schema、账号与管理员、内容、社交、AI 与后台作业、评论删除角色、AI 记忆来源、AI 任务定时、AI 联网搜索、超级管理员标记 |
| `0011_drop_time_defaults` | 移除 43 个业务时间列的 `DEFAULT now()`；`timeline.schema_migrations.applied_at` 是唯一保留的时间默认值 |
| `0012_add_avatar_attachment_id` | 只加 `"user".users.avatar_attachment_id` 与普通索引，不回填（回填由 `0016` 完成） |
| `0013_timeline_and_task_indexes` | no-transaction：并发创建 `reposts_timeline`、`ai_tasks_daily`、`ai_call_logs_retention_id`、`attachments_status_deleted` |
| `0014_search_trgm` | no-transaction：`pg_trgm` 扩展 + `post_search_text_trgm` GIN(`LOWER(plain_text) gin_trgm_ops`)；查询须写成 `LOWER(plain_text) LIKE '%' \|\| $n \|\| '%'` 才命中索引；扩展需安装到连接 `search_path` 内的 schema（生产为 `timeline`） |
| `0015_drop_unused_indexes` | 删除未使用索引 `timeline.post_search_updated` |
| `0016_review_fixes` | 普通事务：当 `avatar_url` 恰为 `/api/v1/attachments/<id>/content` 时回填 `"user".users.avatar_attachment_id`（仅当列为 `NULL`，幂等，不改 `updated_at`）+ 删除与查询形态不匹配的 `timeline.ai_tasks_daily` 索引；保留 `ai_call_logs_retention_id`，不动时间默认值与业务数据 |

## 新增迁移的评审规则

1. 只追加、不改已应用文件；先在开发/测试库完整执行，再随版本上线。
2. 含 `CONCURRENTLY` 的语句必须放在 `no-transaction` 文件并保持幂等；失败后按 `docs/DEPLOYMENT.md` 第 6 章处置 INVALID 索引再重跑。
3. 不做物理删除业务数据；清理类需求只更新状态/失效标记，或仅删除索引等结构对象。
4. 默认不写业务数据；确需回填时先评审，并在文件头注释说明来源、只处理 `NULL`/未处理行、可重复执行、不修改业务时间列。
5. 涉及时间默认值、扩展权限或索引变化时，同步更新 `docs/DEPLOYMENT.md` 与 `docs/ARCHITECTURE.md` §9、`docs/DATA_MODEL.md` 迁移说明。
