-- 0012_add_avatar_attachment_id：users 增加头像附件引用列（只结构变更，不回填业务数据）。
--
-- 背景：附件清理原先用 users.avatar_url 与 /api/v1/attachments/<id>/content 字符串拼接做
-- 关联判断，无法命中索引且与 Go 侧 URL 构造规则重复；改为 users.avatar_attachment_id
-- BIGINT 等值关联。
--
-- 约定（DATA_MODEL §4.5：迁移只做结构变更，不回填业务数据）：
--   1. 本次迁移只加列与普通索引，不 UPDATE 存量 users、不回填数据。
--   2. 新列由应用侧在设置头像时同一事务内双写；既有数据由后续背景回填任务补齐。
--   3. 回填完成前清理任务不得依赖该列非空做删除判断，避免误清理头像附件。
--   4. 不使用外键约束（项目约定），引用完整性由应用层维护；索引用于清理侧等值查询。
ALTER TABLE "user".users ADD COLUMN avatar_attachment_id BIGINT;

CREATE INDEX users_avatar_attachment ON "user".users (avatar_attachment_id);
