-- check-time-defaults.sql：只读检查 0011_drop_time_defaults 迁移后的时间列默认值状态。
-- 用法（开发/测试实例，需具备 information_schema 读取权限）：
--   psql "$DSN" -f deploy/scripts/check-time-defaults.sql
-- 本文件只包含 SELECT 查询，不修改数据库。
--
-- 期望结果：
--   查询一：0 行（业务表不再有 now()/CURRENT_TIMESTAMP 默认值）；
--   查询二：1 行（保留的白名单 timeline.schema_migrations.applied_at）；
--   查询三：仅用于排查，列出仍有时间默认值的全部列（正常应只剩白名单 1 行）。

-- 查询一：业务表中残留的“当前时间”默认值（期望 0 行）。
SELECT table_schema,
       table_name,
       column_name,
       data_type,
       column_default
FROM information_schema.columns
WHERE table_schema IN ('timeline', 'user')
  AND table_name <> 'schema_migrations'
  AND (column_default ILIKE '%now()%' OR column_default ILIKE '%CURRENT_TIMESTAMP%')
ORDER BY table_schema, table_name, column_name;

-- 查询二：迁移基础设施白名单（期望 1 行，DEFAULT now() 仍保留）。
SELECT table_schema,
       table_name,
       column_name,
       column_default
FROM information_schema.columns
WHERE table_schema = 'timeline'
  AND table_name = 'schema_migrations'
  AND column_name = 'applied_at';

-- 查询三：全部 schema 内仍带时间默认值的列（正常应只剩白名单 1 行）。
SELECT table_schema,
       table_name,
       column_name,
       column_default
FROM information_schema.columns
WHERE table_schema IN ('timeline', 'user')
  AND (column_default ILIKE '%now()%' OR column_default ILIKE '%CURRENT_TIMESTAMP%')
ORDER BY table_schema, table_name, column_name;
