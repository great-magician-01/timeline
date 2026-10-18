-- 0019_author_is_ai_and_attachment_usage：SQL 最简原则审查（批次 3）结构变更 + 经评审核准的回填。
--
-- 本文件是普通事务迁移（无 no-transaction 指令），由迁移器整体包在一个事务内执行。
--
-- 背景与依据：
--   1. 时间线「全部/人类/AI」作者类型过滤此前先在应用层 SELECT 全量账号 ID，再以 bigint[]
--      参数下传（human 分支为全部人类账号，随用户数线性劣化）。本迁移把 is_ai 快照到内容行，
--      查询直接按布尔列过滤；is_ai 在账号创建后不可变（无修改入口），快照无需后续维护。
--   2. 存储配额此前每次上传实时 SUM(size_bytes)（含已逻辑删除行、从不下降），随历史附件线性
--      增长。本迁移新增 media 自有的 attachment_usage 只增计数器，上传事务内 O(1) 递增。
--
-- 回填评审（显式例外，参照 0016 做法）：
--   - posts/reposts.author_is_ai：来源为 "user".users.is_ai，只处理新增列（默认 FALSE）后与账号
--     不一致的行；可重复执行；不修改 updated_at 或其它业务时间列。
--   - attachment_usage：来源为 timeline.attachments 全量明细（业务口径包含已逻辑删除行，
--     与旧实时 SUM 一致）；updated_at 使用固定字面量时间（不是数据库时间函数），表示该行为
--     迁移初始值；可重复执行（首次回填后 uploader_id 均已存在，重复执行不产生新行）。
--   - 不改动任何已存在的业务时间列，不引入数据库时间默认值，不建外键。
--
-- 约束与说明：
--   1. 迁移器先剔除行注释再按 ASCII 分号分句；本文件只有单语句，不含复合语句。
--   2. 0020 为新增列配套的并发索引（no-transaction）。
--   3. 代码侧：content/social 写入时显式传入 author_is_ai；media 上传事务内递增 used_bytes。
ALTER TABLE timeline.posts ADD COLUMN author_is_ai BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE timeline.posts AS p
SET author_is_ai = u.is_ai
FROM "user".users AS u
WHERE u.id = p.author_id
  AND p.author_is_ai <> u.is_ai;

ALTER TABLE timeline.reposts ADD COLUMN author_is_ai BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE timeline.reposts AS r
SET author_is_ai = u.is_ai
FROM "user".users AS u
WHERE u.id = r.user_id
  AND r.author_is_ai <> u.is_ai;

CREATE TABLE timeline.attachment_usage (
    uploader_id BIGINT PRIMARY KEY,
    used_bytes  BIGINT NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL,
    CONSTRAINT attachment_usage_used_bytes_check CHECK (used_bytes >= 0)
);

INSERT INTO timeline.attachment_usage (uploader_id, used_bytes, updated_at)
SELECT uploader_id, SUM(size_bytes), '2026-10-18T00:00:00+08:00'::timestamptz
FROM timeline.attachments
GROUP BY uploader_id;
