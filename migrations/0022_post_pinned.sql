-- 0022_post_pinned：posts 增加个人主页置顶列（只结构变更，不回填业务数据）。
--
-- 背景：作者可把自己的已发布帖子置顶到个人主页顶部（posts 页签第一页），并支持
-- 调整置顶顺序。置顶状态由两列共同表达：
--   pinned_at    置顶时间（应用时钟写入，非空表示已置顶）；
--   pinned_sort  展示顺序（1 = 最前，1..3）。
--
-- 约定（DATA_MODEL：迁移只做结构变更，不回填业务数据）：
--   1. 两位作者各自最多 3 条置顶；CHECK 约束保证两列同空同非空、顺序在 1..3 内，
--      且仅已发布（status='published'）帖子允许置顶；删除帖子由应用在同一 UPDATE
--      中清空两列（SoftDelete），无需触发器。
--   2. 置顶/取消/排序不修改 updated_at（置顶不是内容编辑，沿用计数器先例）。
--   3. 不加新索引：置顶查询是「单作者 + 已发布」小集合（最多 3 行），走现有
--      posts_author_published 部分索引已足够，遵循 SQL 最简原则。
--   4. 无外键约束（项目约定）；存量数据两列均为 NULL，CHECK 约束可直接生效。
ALTER TABLE timeline.posts ADD COLUMN pinned_at TIMESTAMPTZ;

ALTER TABLE timeline.posts ADD COLUMN pinned_sort INTEGER;

ALTER TABLE timeline.posts ADD CONSTRAINT posts_pinned_check CHECK (
    (pinned_at IS NULL AND pinned_sort IS NULL)
    OR (
        status = 'published'
        AND pinned_at IS NOT NULL
        AND pinned_sort IS NOT NULL
        AND pinned_sort BETWEEN 1 AND 3
    )
);
