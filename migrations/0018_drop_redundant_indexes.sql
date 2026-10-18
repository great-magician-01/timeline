-- timeline:no-transaction
-- 0018_drop_redundant_indexes：SQL 最简原则审查（批次 2）删除冗余/无效索引。
--
-- 说明：
--   1. 首行 no-transaction 指令让迁移器不包事务、逐条在连接上执行，以支持
--      DROP INDEX CONCURRENTLY；失败时不记录版本、不自动回滚，按顺序人工核对重跑（语句均幂等）。
--   2. 只删除索引，不删除表/列/业务数据；被替换索引的新索引已在 0017 创建：
--      - ai_action_counters_unique → 0017 的 ai_action_counters_daily_unique（列集合相同，列序不同）；
--      - follows_follower_active/follows_followee_active → 0017 的 follows_following_cursor /
--        follows_follower_cursor（同前缀、同部分索引谓词，并覆盖关注集合投影）；
--      - attachments_unlinked → 0017 的 attachments_unlinked_post（清理候选查询已限定 purpose='post'）。
--   3. 保留 comments_author：管理端评论审核列表按作者过滤时不过滤 status，0017 的
--      comments_author_created 是 WHERE status='published' 部分索引，不能替代该查询；
--      其余按作者读取评论的运行期查询（个人主页回复页签、用户统计评论数）均带 published 条件，
--      由 comments_author_created 覆盖。
--   4. 其余索引在仓库内无运行期查询使用或与主键等价，仅产生写放大：
--      auth_sessions_expiry、admin_audit_logs_created、users_admin_list、post_versions_post、
--      posts_quoted、ai_tasks_action。
DROP INDEX CONCURRENTLY IF EXISTS timeline.ai_action_counters_unique;

DROP INDEX CONCURRENTLY IF EXISTS timeline.follows_follower_active;

DROP INDEX CONCURRENTLY IF EXISTS timeline.follows_followee_active;

DROP INDEX CONCURRENTLY IF EXISTS timeline.attachments_unlinked;

DROP INDEX CONCURRENTLY IF EXISTS "user".auth_sessions_expiry;

DROP INDEX CONCURRENTLY IF EXISTS timeline.admin_audit_logs_created;

DROP INDEX CONCURRENTLY IF EXISTS "user".users_admin_list;

DROP INDEX CONCURRENTLY IF EXISTS timeline.post_versions_post;

DROP INDEX CONCURRENTLY IF EXISTS timeline.posts_quoted;

DROP INDEX CONCURRENTLY IF EXISTS timeline.ai_tasks_action;
