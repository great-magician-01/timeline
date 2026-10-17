-- timeline:no-transaction
-- 0013_timeline_and_task_indexes：时间线、AI 任务与附件清理的查询索引。
--
-- 说明：
--   1. 首行 no-transaction 指令让迁移器不包事务、逐条在连接上执行，以支持
--      CREATE INDEX CONCURRENTLY；普通事务迁移无法执行 CONCURRENTLY。
--   2. 本文件全部语句幂等（IF NOT EXISTS），失败后按顺序人工核对重跑即可；
--      no-transaction 迁移失败时迁移器不记录版本，也不会自动回滚已成功语句。
--   3. 仅结构变更，不回填业务数据；schema 前缀写 timeline.，测试 schema 由迁移器统一重写。
--   4. 依赖迁移器 v2 的 no-transaction 支持（本仓库 internal/platform/migrate）；旧的事务式
--      迁移器会把文件包在事务里执行，导致 CREATE INDEX CONCURRENTLY 失败，必须在迁移器
--      切换完成后再应用本迁移。
--   5. 索引用途：
--      - reposts_timeline：转发时间线分支按 (created_at DESC, id DESC) 排序且只看生效转发；
--      - ai_tasks_daily：按 AI 统计“今日成功动作数”/日界区间扫描；
--      - ai_call_logs_retention_id：调用记录保留期清理按 (created_at, id) 排序扫描未删除行；
--      - attachments_status_deleted：附件管理/清理按已逻辑删除状态过滤。
CREATE INDEX CONCURRENTLY IF NOT EXISTS reposts_timeline
    ON timeline.reposts (created_at DESC, id DESC)
    WHERE is_active;

CREATE INDEX CONCURRENTLY IF NOT EXISTS ai_tasks_daily
    ON timeline.ai_tasks (ai_id, created_at)
    WHERE status = 'succeeded';

CREATE INDEX CONCURRENTLY IF NOT EXISTS ai_call_logs_retention_id
    ON timeline.ai_call_logs (created_at, id)
    WHERE NOT is_deleted;

CREATE INDEX CONCURRENTLY IF NOT EXISTS attachments_status_deleted
    ON timeline.attachments (status)
    WHERE status = 'deleted';
