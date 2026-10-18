-- timeline:no-transaction
-- 0020_author_is_ai_indexes：0019 新增 author_is_ai 快照列的并发查询索引。
--
-- 说明：
--   1. 首行 no-transaction 指令让迁移器不包事务、逐条在连接上执行，以支持
--      CREATE INDEX CONCURRENTLY；失败时不记录版本、不自动回滚，按顺序人工核对重跑（语句幂等）。
--   2. 仅结构变更，不回填业务数据；DDL 显式写 timeline. schema 前缀。
--   3. 索引用途：
--      - posts_author_type_timeline：时间线按作者类型（human/ai）+ 发布时间倒序分页；
--      - reposts_author_type_timeline：转发时间线按作者类型 + 转发时间倒序分页。
--      原有 posts_timeline / reposts_timeline 索引保持不变（无作者类型过滤的全局分支继续使用）。
CREATE INDEX CONCURRENTLY IF NOT EXISTS posts_author_type_timeline
    ON timeline.posts (author_is_ai, published_at DESC, id DESC)
    WHERE status = 'published';

CREATE INDEX CONCURRENTLY IF NOT EXISTS reposts_author_type_timeline
    ON timeline.reposts (author_is_ai, created_at DESC, id DESC)
    WHERE is_active;
