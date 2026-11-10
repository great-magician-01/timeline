-- 0026_drive_files：按用户隔离的一级网盘目录、文件公开级别与文章默认目录。
--
-- 设计约定：
--   1. attachments 继续拥有物理文件与媒体元数据；drive_files 只表达网盘成员关系、
--      目录和公开级别，drive_folders 只表达用户的一级目录。两表均归 media 模块。
--   2. 文件最终权限在 drive_files.visibility；目录“统一设置”是应用层批量更新，不做继承 ACL。
--   3. system_key='post' 是每用户唯一的“文章附件”系统目录；目录 ID 与文件 ID 都由应用生成。
--   4. 全部无外键、无业务时间默认值、无物理删除；folder_id NULL 表示根目录。
--
-- 存量回填评审（显式例外，参照 0016/0019）：
--   - 只处理 purpose='post'、status='available'，且仍被帖子、任一历史版本或生效草稿引用的附件；
--     未关联临时附件、头像、横幅与已失效附件不回填。
--   - 每个相关上传者补一个 system_key='post' 目录。为保证可重复执行且不引入数据库 ID 序列，
--     仅本迁移回填的系统目录使用 owner_id（本身是雪花 ID）作为目录 ID；运行期新目录仍由应用生成。
--   - 文件统一回填为 private，目录关联取实际存在的系统目录 ID，因此重复执行不会误指向其他目录。
--   - 只插入新表，不修改既有行业务时间；新目录时间使用固定迁移字面量，文件保留原附件创建时间。
--   - ON CONFLICT DO NOTHING 保证幂等；无外键、无数据库时间函数、无 CONCURRENTLY。

CREATE TABLE timeline.drive_folders (
    id          BIGINT PRIMARY KEY,
    owner_id    BIGINT NOT NULL,
    name        TEXT NOT NULL,
    system_key  TEXT,
    status      TEXT NOT NULL DEFAULT 'active',
    created_at  TIMESTAMPTZ NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL,
    deleted_at  TIMESTAMPTZ,
    CONSTRAINT drive_folders_status_check CHECK (status IN ('active', 'deleted')),
    CONSTRAINT drive_folders_system_key_check CHECK (system_key IS NULL OR system_key = 'post'),
    CONSTRAINT drive_folders_name_check CHECK (length(name) > 0 AND length(name) <= 150),
    CONSTRAINT drive_folders_deleted_check CHECK ((status = 'deleted') = (deleted_at IS NOT NULL))
);

CREATE UNIQUE INDEX drive_folders_owner_name_active
    ON timeline.drive_folders (owner_id, LOWER(name)) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX drive_folders_owner_system_active
    ON timeline.drive_folders (owner_id, system_key)
    WHERE deleted_at IS NULL AND system_key IS NOT NULL;
CREATE INDEX drive_folders_owner_list
    ON timeline.drive_folders (owner_id, id DESC) WHERE deleted_at IS NULL;

CREATE TABLE timeline.drive_files (
    attachment_id BIGINT PRIMARY KEY,
    owner_id      BIGINT NOT NULL,
    folder_id     BIGINT,
    visibility    TEXT NOT NULL DEFAULT 'private',
    status        TEXT NOT NULL DEFAULT 'active',
    created_at    TIMESTAMPTZ NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL,
    deleted_at    TIMESTAMPTZ,
    CONSTRAINT drive_files_visibility_check CHECK (visibility IN ('private', 'public')),
    CONSTRAINT drive_files_status_check CHECK (status IN ('active', 'deleted')),
    CONSTRAINT drive_files_deleted_check CHECK ((status = 'deleted') = (deleted_at IS NOT NULL))
);

CREATE INDEX drive_files_owner_list
    ON timeline.drive_files (owner_id, attachment_id DESC) WHERE status = 'active';
CREATE INDEX drive_files_owner_folder_list
    ON timeline.drive_files (owner_id, folder_id, attachment_id DESC) WHERE status = 'active';
CREATE INDEX drive_files_owner_public_list
    ON timeline.drive_files (owner_id, attachment_id DESC)
    WHERE status = 'active' AND visibility = 'public';
CREATE INDEX drive_files_folder_active
    ON timeline.drive_files (folder_id, attachment_id) WHERE status = 'active';

INSERT INTO timeline.drive_folders (
    id, owner_id, name, system_key, status, created_at, updated_at, deleted_at
)
SELECT DISTINCT
    a.uploader_id, a.uploader_id, '文章附件', 'post', 'active',
    '2026-11-10T00:00:00+08:00'::timestamptz,
    '2026-11-10T00:00:00+08:00'::timestamptz,
    NULL::timestamptz
FROM timeline.attachments AS a
WHERE a.purpose = 'post'
  AND a.status = 'available'
  AND (
      a.post_id IS NOT NULL
      OR EXISTS (
          SELECT 1 FROM timeline.post_version_attachments AS pva
          WHERE pva.attachment_id = a.id
      )
      OR EXISTS (
          SELECT 1 FROM timeline.post_draft_attachments AS pda
          WHERE pda.attachment_id = a.id AND pda.is_active
      )
  )
ON CONFLICT DO NOTHING;

INSERT INTO timeline.drive_files (
    attachment_id, owner_id, folder_id, visibility, status,
    created_at, updated_at, deleted_at
)
SELECT
    a.id, a.uploader_id, f.id, 'private', 'active',
    a.created_at, a.created_at, NULL::timestamptz
FROM timeline.attachments AS a
JOIN timeline.drive_folders AS f
  ON f.owner_id = a.uploader_id
 AND f.system_key = 'post'
 AND f.deleted_at IS NULL
WHERE a.purpose = 'post'
  AND a.status = 'available'
  AND (
      a.post_id IS NOT NULL
      OR EXISTS (
          SELECT 1 FROM timeline.post_version_attachments AS pva
          WHERE pva.attachment_id = a.id
      )
      OR EXISTS (
          SELECT 1 FROM timeline.post_draft_attachments AS pda
          WHERE pda.attachment_id = a.id AND pda.is_active
      )
  )
ON CONFLICT DO NOTHING;
