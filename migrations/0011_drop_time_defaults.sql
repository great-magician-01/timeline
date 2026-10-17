-- 0011_drop_time_defaults：移除业务时间列的数据库默认值，时间统一由应用侧显式传入。
--
-- 背景：迁移 0002-0009 中引入的业务时间列使用 DEFAULT now() 兜底（0006/0008/0009
-- 未新增默认值列，0010 只增加布尔列与约束/索引）；应用改造后所有写路径都显式传时间
-- （应用侧 Clock），继续保留 DEFAULT 会在漏传时静默兜底、掩盖缺陷，因此按“阶段二”
-- 计划移除默认值。
--
-- 约束与说明：
--   1. 0001-0010 已应用且不可修改，本文件以追加迁移的方式移除默认值。
--   2. 共 43 条 ALTER TABLE ... ALTER COLUMN ... DROP DEFAULT，每列一条。
--   3. 不包含 timeline.schema_migrations.applied_at：它由迁移器维护，属于迁移基础设施
--      而不是业务时间列，保留 DEFAULT now() 作为唯一白名单。
--   4. 前置条件：应用侧“所有写路径显式传时间”的版本已上线并验证；旧二进制若仍依赖
--      DEFAULT，在本迁移后写入会违反 NOT NULL，必须在新版本全量生效后再执行。
--   5. 本迁移只去掉默认值，不修改既有数据，也不改列类型、可空性、索引与约束。
--   6. 迁移器先剔除行注释再按 ASCII 分号分句；本文件每行仅一条 ALTER 语句，
--      不含函数体、DO 块等复合语句。
--   7. 回滚：本仓库迁移只追加、无 down 脚本；需要回滚时追加反向迁移恢复
--      DEFAULT now()（按本文件对应列），或从备份恢复。

-- 来源：0002_auth_and_admin.sql
ALTER TABLE "user".users ALTER COLUMN created_at DROP DEFAULT;
ALTER TABLE "user".users ALTER COLUMN updated_at DROP DEFAULT;
ALTER TABLE "user".auth_sessions ALTER COLUMN created_at DROP DEFAULT;
ALTER TABLE timeline.system_settings ALTER COLUMN updated_at DROP DEFAULT;
ALTER TABLE timeline.admin_audit_logs ALTER COLUMN created_at DROP DEFAULT;

-- 来源：0003_content.sql
ALTER TABLE timeline.posts ALTER COLUMN created_at DROP DEFAULT;
ALTER TABLE timeline.posts ALTER COLUMN updated_at DROP DEFAULT;
ALTER TABLE timeline.post_drafts ALTER COLUMN updated_at DROP DEFAULT;
ALTER TABLE timeline.post_versions ALTER COLUMN created_at DROP DEFAULT;
ALTER TABLE timeline.tags ALTER COLUMN created_at DROP DEFAULT;
ALTER TABLE timeline.post_search ALTER COLUMN updated_at DROP DEFAULT;
ALTER TABLE timeline.attachments ALTER COLUMN created_at DROP DEFAULT;
ALTER TABLE timeline.attachments ALTER COLUMN updated_at DROP DEFAULT;
ALTER TABLE timeline.attachment_derivatives ALTER COLUMN created_at DROP DEFAULT;
ALTER TABLE timeline.attachment_derivatives ALTER COLUMN updated_at DROP DEFAULT;

-- 来源：0004_social.sql
ALTER TABLE timeline.follows ALTER COLUMN created_at DROP DEFAULT;
ALTER TABLE timeline.follows ALTER COLUMN updated_at DROP DEFAULT;
ALTER TABLE timeline.follow_requests ALTER COLUMN created_at DROP DEFAULT;
ALTER TABLE timeline.likes ALTER COLUMN created_at DROP DEFAULT;
ALTER TABLE timeline.likes ALTER COLUMN updated_at DROP DEFAULT;
ALTER TABLE timeline.comments ALTER COLUMN created_at DROP DEFAULT;
ALTER TABLE timeline.comments ALTER COLUMN updated_at DROP DEFAULT;
ALTER TABLE timeline.reposts ALTER COLUMN created_at DROP DEFAULT;
ALTER TABLE timeline.reposts ALTER COLUMN updated_at DROP DEFAULT;
ALTER TABLE timeline.mentions ALTER COLUMN created_at DROP DEFAULT;
ALTER TABLE timeline.notifications ALTER COLUMN created_at DROP DEFAULT;

-- 来源：0005_ai_and_jobs.sql
ALTER TABLE timeline.model_providers ALTER COLUMN created_at DROP DEFAULT;
ALTER TABLE timeline.model_providers ALTER COLUMN updated_at DROP DEFAULT;
ALTER TABLE timeline.ai_profiles ALTER COLUMN created_at DROP DEFAULT;
ALTER TABLE timeline.ai_profiles ALTER COLUMN updated_at DROP DEFAULT;
ALTER TABLE timeline.ai_model_settings ALTER COLUMN updated_at DROP DEFAULT;
ALTER TABLE timeline.ai_behavior_settings ALTER COLUMN updated_at DROP DEFAULT;
ALTER TABLE timeline.ai_tasks ALTER COLUMN created_at DROP DEFAULT;
ALTER TABLE timeline.ai_call_logs ALTER COLUMN started_at DROP DEFAULT;
ALTER TABLE timeline.ai_call_logs ALTER COLUMN finished_at DROP DEFAULT;
ALTER TABLE timeline.ai_call_logs ALTER COLUMN created_at DROP DEFAULT;
ALTER TABLE timeline.ai_action_counters ALTER COLUMN updated_at DROP DEFAULT;
ALTER TABLE timeline.ai_memories ALTER COLUMN created_at DROP DEFAULT;
ALTER TABLE timeline.ai_memories ALTER COLUMN updated_at DROP DEFAULT;
ALTER TABLE timeline.background_jobs ALTER COLUMN run_at DROP DEFAULT;
ALTER TABLE timeline.background_jobs ALTER COLUMN created_at DROP DEFAULT;
ALTER TABLE timeline.background_jobs ALTER COLUMN updated_at DROP DEFAULT;

-- 来源：0007_ai_memory_sources.sql
ALTER TABLE timeline.ai_memory_sources ALTER COLUMN created_at DROP DEFAULT;
