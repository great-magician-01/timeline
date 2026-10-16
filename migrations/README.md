# 数据库迁移

- 命名规范：`NNNN_描述.sql`（四位数序号，按文件名排序执行），只允许追加，**不得修改已应用的文件**。
- 执行方式：应用启动时（`RUN_MIGRATIONS=true`）自动执行，advisory lock 防并发；也可手动执行。
- 语句限制：骨架期迁移器按 `;` 简单分句逐条执行——**不要写函数体、DO 块等含分号的复合语句**，需要时先升级迁移器。
- 职责边界：两个 schema（`timeline`、`user`）由迁移器启动引导负责创建（缺失才建）；迁移文件只负责表结构。
- 已应用版本记录在 `timeline.schema_migrations` 表。
- **本目录的 README.md 是 go:embed 的恒存锚点，请勿删除**：embed 模式为 `all:*`，保证目录中没有 `.sql` 文件时也能编译（否则报 "pattern all:*.sql: no matching files found"）。

