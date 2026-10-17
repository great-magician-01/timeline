-- 0010_super_admin：超级管理员标记（role 保持 user/admin，超管 = admin + 该标记）。
-- 存量数据不迁移：升级后的既有部署没有超级管理员，由启动日志提示；
-- 空库首次注册的用户在注册事务内成为唯一超级管理员。
ALTER TABLE "user".users
    ADD COLUMN is_super_admin BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE "user".users
    ADD CONSTRAINT users_super_admin_check
    CHECK (NOT is_super_admin OR role = 'admin');

CREATE UNIQUE INDEX users_single_super_admin
    ON "user".users (is_super_admin)
    WHERE is_super_admin;
