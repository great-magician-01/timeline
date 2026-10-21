-- 0023_bookmarks：收藏帖子与收藏夹两张新表（只结构变更，不回填业务数据）。
--
-- 背景：用户可把帖子收藏为私有阅读清单，并按收藏夹分组。收藏完全私有：
-- 仅本人可见、不展示总数、不发通知、管理后台不可见，因此不引入 posts 冗余计数列。
--
-- 约定（DATA_MODEL：迁移只做结构变更，不回填业务数据）：
--   1. bookmarks.folder_id 为 NULL 表示默认收藏（未分组）；取消收藏只置
--      is_active = FALSE，保留 folder_id；删除收藏夹由应用在同一事务内把夹内
--      生效收藏的 folder_id 置回 NULL（归还默认），不使用触发器。
--   2. (user_id, post_id) 唯一索引必须非部分：upsert 的 ON CONFLICT 需要命中
--      is_active = FALSE 的旧行以支持"取消后重新收藏"。
--   3. 列表查询按 id DESC（雪花 ID 即时间序）；两张表各只建列表所需的部分索引，
--      遵循 SQL 最简原则。
--   4. 无外键约束（项目约定）；is_active 的 DEFAULT TRUE 是状态默认值，
--      不违反 0011 的业务时间默认值禁令（参照 0021 dm_messages.status 先例）。
CREATE TABLE timeline.bookmark_folders (
    id         BIGINT PRIMARY KEY,
    user_id    BIGINT NOT NULL,
    name       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    deleted_at TIMESTAMPTZ
);

CREATE INDEX bookmark_folders_user_active ON timeline.bookmark_folders (user_id) WHERE deleted_at IS NULL;

CREATE TABLE timeline.bookmarks (
    id         BIGINT PRIMARY KEY,
    user_id    BIGINT NOT NULL,
    post_id    BIGINT NOT NULL,
    folder_id  BIGINT,
    is_active  BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE UNIQUE INDEX bookmarks_user_post_unique ON timeline.bookmarks (user_id, post_id);

CREATE INDEX bookmarks_user_active ON timeline.bookmarks (user_id, id DESC) WHERE is_active;

CREATE INDEX bookmarks_user_folder_active ON timeline.bookmarks (user_id, folder_id, id DESC) WHERE is_active AND folder_id IS NOT NULL;
