-- timeline:no-transaction
-- 0017_sql_review_indexes：SQL 最简原则审查（批次 2）新增查询索引。
--
-- 说明：
--   1. 首行 no-transaction 指令让迁移器不包事务、逐条在连接上执行，以支持
--      CREATE INDEX CONCURRENTLY；失败时不记录版本、不自动回滚，按顺序人工核对重跑（语句均幂等）。
--   2. 仅结构变更，不回填业务数据；DDL 显式写 timeline. / "user". schema 前缀。
--   3. 索引用途：
--      - follows_*_cursor：粉丝/关注列表按关注行 id 倒序 + 关注集合覆盖投影（替换旧单列部分索引，
--        旧索引在 0018 删除）；
--      - comments_author_created：个人主页「回复」页签按 (created_at,id) 倒序；
--      - comments_post_top：帖子顶层评论（parent_comment_id IS NULL）分页；
--      - comments_plain_text_trgm / attachments_filename_trgm / users_*_trgm / tags_name_prefix：
--        管理端与用户搜索的 LIKE 子串/前缀过滤（查询侧必须用 LOWER(x) LIKE ... ESCAPE '\' 形态）；
--      - attachments_post_available：审核列表/详情按帖统计有效附件；
--      - attachments_unlinked_post：临时附件清理候选（替换旧 attachments_unlinked，旧索引在 0018 删除）；
--      - mentions_user_created：AI 调度按时间窗读取生效提及；
--      - background_jobs_running：每 2 分钟回收失联 running 作业（旧查询全表扫描）；
--      - ai_tasks_running / ai_tasks_last_success：AI 调度 reconcile 与冷却判断；
--      - users_human_exists：公开 /auth/config 初始化判定 EXISTS 短路；
--      - posts_author_quoted_published：个人主页「转发」页签；
--      - ai_action_counters_daily_unique：按 (ai_id,counter_date) 前缀聚合当日动作总数
--        （旧唯一索引列序在 0018 删除，ON CONFLICT 仍可按列集合推断）。
CREATE INDEX CONCURRENTLY IF NOT EXISTS follows_following_cursor
    ON timeline.follows (follower_id, id DESC, followee_id) WHERE is_active;

CREATE INDEX CONCURRENTLY IF NOT EXISTS follows_follower_cursor
    ON timeline.follows (followee_id, id DESC, follower_id) WHERE is_active;

CREATE INDEX CONCURRENTLY IF NOT EXISTS comments_author_created
    ON timeline.comments (author_id, created_at DESC, id DESC) WHERE status = 'published';

CREATE INDEX CONCURRENTLY IF NOT EXISTS comments_post_top
    ON timeline.comments (post_id, id DESC) WHERE parent_comment_id IS NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS comments_plain_text_trgm
    ON timeline.comments USING GIN (LOWER(plain_text) gin_trgm_ops);

CREATE INDEX CONCURRENTLY IF NOT EXISTS attachments_filename_trgm
    ON timeline.attachments USING GIN (LOWER(original_filename) gin_trgm_ops);

CREATE INDEX CONCURRENTLY IF NOT EXISTS attachments_post_available
    ON timeline.attachments (post_id) WHERE status = 'available';

CREATE INDEX CONCURRENTLY IF NOT EXISTS attachments_unlinked_post
    ON timeline.attachments (created_at)
    WHERE post_id IS NULL AND status = 'available' AND purpose = 'post';

CREATE INDEX CONCURRENTLY IF NOT EXISTS mentions_user_created
    ON timeline.mentions (mentioned_user_id, created_at DESC, id DESC) WHERE is_active;

CREATE INDEX CONCURRENTLY IF NOT EXISTS background_jobs_running
    ON timeline.background_jobs (updated_at) WHERE status = 'running';

CREATE INDEX CONCURRENTLY IF NOT EXISTS ai_tasks_running
    ON timeline.ai_tasks (started_at) WHERE status = 'running';

CREATE INDEX CONCURRENTLY IF NOT EXISTS ai_tasks_last_success
    ON timeline.ai_tasks (ai_id, trigger_type, finished_at DESC)
    WHERE status IN ('succeeded', 'noop');

CREATE INDEX CONCURRENTLY IF NOT EXISTS users_human_exists
    ON "user".users (id) WHERE NOT is_ai;

CREATE INDEX CONCURRENTLY IF NOT EXISTS users_username_trgm
    ON "user".users USING GIN (LOWER(username) gin_trgm_ops);

CREATE INDEX CONCURRENTLY IF NOT EXISTS users_nickname_trgm
    ON "user".users USING GIN (LOWER(nickname) gin_trgm_ops);

CREATE INDEX CONCURRENTLY IF NOT EXISTS users_email_trgm
    ON "user".users USING GIN (LOWER(COALESCE(email, '')) gin_trgm_ops);

CREATE INDEX CONCURRENTLY IF NOT EXISTS tags_name_prefix
    ON timeline.tags (name text_pattern_ops);

CREATE INDEX CONCURRENTLY IF NOT EXISTS posts_author_quoted_published
    ON timeline.posts (author_id, published_at DESC, id DESC)
    WHERE status = 'published' AND quoted_post_id IS NOT NULL;

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS ai_action_counters_daily_unique
    ON timeline.ai_action_counters (ai_id, counter_date, action_type);
