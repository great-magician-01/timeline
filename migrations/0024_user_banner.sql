-- 0024_user_banner：users 增加个人主页横幅（banner）引用列，attachments 放宽 purpose 约束。
--
-- 背景：横幅完全复用头像链路——作为 purpose='banner' 的图片附件上传，media 在同一事务内
-- 经 auth 端口把展示地址写回 users.banner_url（users 表由 auth 模块独有）。
--
-- 约定（DATA_MODEL：迁移只做结构变更，不回填业务数据）：
--   1. banner_url TEXT NOT NULL DEFAULT ''：空串表示未设置（与 avatar_url 同口径）；
--      banner_attachment_id BIGINT 可空，由应用在设置横幅时同一事务内双写，存量数据无需回填。
--   2. 重建 attachments_purpose_check： purpose 枚举加入 'banner'（先 DROP 再 ADD，
--      约束名不变）；重建会全表校验既有行，附件表当前规模下可接受，不做 NOT VALID 分步。
--   3. 不建 banner_attachment_id 索引：0012 头像引用索引是专供媒体清理侧等值查询的，
--      而清理候选 SQL 固定 purpose='post'（media/repo），banner 附件天然不在候选之列，
--      无任何按该列查询的路径，遵循 SQL 最简原则。
--   4. 不使用外键约束（项目约定）；横幅附件公开可读由应用层 requireAttachmentAccess 判定。
ALTER TABLE "user".users ADD COLUMN banner_url TEXT NOT NULL DEFAULT '';

ALTER TABLE "user".users ADD COLUMN banner_attachment_id BIGINT;

ALTER TABLE timeline.attachments DROP CONSTRAINT attachments_purpose_check;

ALTER TABLE timeline.attachments
    ADD CONSTRAINT attachments_purpose_check CHECK (purpose IN ('post', 'avatar', 'banner'));
