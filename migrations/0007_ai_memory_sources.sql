-- 0007_ai_memory_sources：长期记忆的来源关联（docs/DATA_MODEL.md 6.25、docs/WORKFLOWS.md 8.5）。
-- 沉淀后的长期记忆可能综合了多条近期记忆，需要记录“这条长期记忆到底引用过哪些公开来源”；
-- 当任一来源被删除或改为非公开时，按来源复核把关联的长期记忆置为 invalidated。
-- 仅使用普通索引，不建外键；来源失效只更新 ai_memories.status，不物理删除本表记录。

CREATE TABLE timeline.ai_memory_sources (
    id          BIGINT PRIMARY KEY,
    memory_id   BIGINT NOT NULL,
    source_type TEXT NOT NULL,
    source_id   BIGINT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ai_memory_sources_type_check CHECK (source_type IN ('post', 'comment'))
);

CREATE UNIQUE INDEX ai_memory_sources_unique ON timeline.ai_memory_sources (memory_id, source_type, source_id);
CREATE INDEX ai_memory_sources_source ON timeline.ai_memory_sources (source_type, source_id);
