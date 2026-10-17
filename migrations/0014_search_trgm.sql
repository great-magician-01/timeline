-- timeline:no-transaction
-- 0014_search_trgm：帖子搜索投影的 trigram 子串索引（结构变更，不回填数据）。
--
-- 说明：
--   1. 首行 no-transaction 指令让迁移器不包事务、逐条在连接上执行，以支持
--      CREATE INDEX CONCURRENTLY；普通事务迁移无法执行 CONCURRENTLY。
--   2. pg_trgm 扩展创建放在本文件并与索引同文件：扩展语句本身可幂等创建
--      （IF NOT EXISTS），先建扩展再建索引，避免扩展未就绪导致索引语句失败。
--   3. 需要数据库账号具备创建扩展的权限（PostgreSQL 13+ 的 pg_trgm 为 trusted
--      extension，通常库 owner 即可创建；托管环境若受限，由 DBA 预装后本语句退化为 no-op）。
--      新建时扩展会装到 search_path 的第一个 schema（生产为 timeline）；若已预装在不在
--      search_path 中的 schema，索引语句会找不到 gin_trgm_ops 而失败，需由 DBA 安装到
--      search_path 内 schema 或调整部署配置。
--   4. 依赖迁移器 v2 的 no-transaction 支持（本仓库 internal/platform/migrate）；旧的事务式
--      迁移器会包事务执行并导致 CREATE INDEX CONCURRENTLY 失败，必须在迁移器切换完成后再应用。
--      该路径失败时不会记录版本、也不会回滚已成功语句，按顺序重跑幂等。
--   5. 查询侧必须改写为 LOWER(plain_text) LIKE '%' || $n || '%' 才能命中该表达式索引；
--      STRPOS 走不了 trigram 索引（应用侧改造另案处理）。
--   6. 仅结构变更，不回填业务数据；schema 前缀写 timeline.，测试 schema 由迁移器统一重写。
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE INDEX CONCURRENTLY IF NOT EXISTS post_search_text_trgm
    ON timeline.post_search
    USING GIN (LOWER(plain_text) gin_trgm_ops);
