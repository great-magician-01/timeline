-- 0016_review_fixes：后端审查收尾迁移（头像引用回填 + 删除与查询不匹配的索引）。
--
-- 本文件是普通事务迁移（无 no-transaction 指令），由迁移器整体包在一个事务内执行：
-- 任一语句失败整体回滚，成功后在同一事务内记录版本。
--
-- 约束与说明：
--   1. 只追加、不回填历史时间列、不修改任何时间默认值；数据库仍不提供业务时间默认值。
--   2. 不更新 updated_at：本迁移只修正引用列，避免把存量账号的"最近更新时间"
--      错误地推后；因此文件内不出现任何数据库时间函数（now()/CURRENT_TIMESTAMP）。
--   3. 迁移器先剔除行注释再按 ASCII 分号分句；本文件只有两条单语句，不含复合语句。
--   4. 执行顺序：0012 已加列/索引、0013/0014 已建索引，本文件追加在最后。

-- ① 头像附件引用回填：覆盖所有 avatar_attachment_id 为空、且 avatar_url 恰为附件内容
--    路径的账号（含历史上 purpose 标记错误但被用作头像的数据），用正则提取雪花 ID。
--    回填完成后，附件清理/衍生文件判断只需依赖 users.avatar_attachment_id，
--    不再需要 media 侧跨模块拼接 users.avatar_url 字符串兜底（见 media 清理实现）。
UPDATE "user".users
SET avatar_attachment_id = (regexp_match(avatar_url, '^/api/v1/attachments/([0-9]+)/content$'))[1]::bigint
WHERE avatar_attachment_id IS NULL
  AND avatar_url ~ '^/api/v1/attachments/[0-9]+/content$';

-- ② 删除与实现查询不匹配的索引：0013 的 ai_tasks_daily(ai_id, created_at) 假设存在
--    "按 AI + created_at 区间统计成功任务"的查询，但实际今日动作统计读取
--    ai_action_counters，ai_tasks 的读取都走 (ai_id, id DESC) 等其它索引。
--    该索引只有写入放大，无查询收益，按追加迁移删除（只删索引，不删表/列/数据）。
DROP INDEX IF EXISTS timeline.ai_tasks_daily;

-- 保留说明：0013 的 ai_call_logs_retention_id(created_at, id) WHERE NOT is_deleted
-- 保留不删除。调用记录保留期清理已按 (created_at, id) 排序扫描未删除行
-- （见 modules/agent/repo 的 MarkDeletedBefore），与本索引形态匹配。
