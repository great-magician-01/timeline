-- 0005_ai_and_jobs：模型接口预设、AI 配置、分层记忆、任务与调用记录、后台作业。
-- API Key 只保存密文（应用侧对称加密）；调用记录按保留期逻辑删除，不执行物理删除。

CREATE TABLE timeline.model_providers (
    id              BIGINT PRIMARY KEY,
    name            TEXT NOT NULL,
    base_url        TEXT NOT NULL,
    api_key_cipher  TEXT NOT NULL DEFAULT '',
    default_model   TEXT NOT NULL,
    is_enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    is_deleted      BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX model_providers_name_unique ON timeline.model_providers (LOWER(name)) WHERE NOT is_deleted;
CREATE INDEX model_providers_enabled ON timeline.model_providers (is_enabled) WHERE NOT is_deleted;

CREATE TABLE timeline.ai_profiles (
    user_id         BIGINT PRIMARY KEY,
    background      TEXT NOT NULL DEFAULT '',
    personality     TEXT NOT NULL DEFAULT '',
    core_values     TEXT NOT NULL DEFAULT '',
    language_style  TEXT NOT NULL DEFAULT '',
    topics          TEXT NOT NULL DEFAULT '',
    target_language TEXT NOT NULL DEFAULT 'zh-CN',
    taboos          TEXT NOT NULL DEFAULT '',
    system_prompt   TEXT NOT NULL DEFAULT '',
    is_enabled      BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE timeline.ai_model_settings (
    ai_id                 BIGINT PRIMARY KEY,
    provider_id           BIGINT,
    model_override        TEXT NOT NULL DEFAULT '',
    generation_params     JSONB NOT NULL DEFAULT '{}'::jsonb,
    request_timeout_seconds INTEGER NOT NULL DEFAULT 60,
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ai_model_settings_timeout_check CHECK (request_timeout_seconds BETWEEN 5 AND 600)
);

CREATE INDEX ai_model_settings_provider ON timeline.ai_model_settings (provider_id);

CREATE TABLE timeline.ai_behavior_settings (
    ai_id                BIGINT PRIMARY KEY,
    allow_original_post  BOOLEAN NOT NULL DEFAULT FALSE,
    allow_active_comment BOOLEAN NOT NULL DEFAULT FALSE,
    allow_passive_reply  BOOLEAN NOT NULL DEFAULT TRUE,
    allow_like           BOOLEAN NOT NULL DEFAULT FALSE,
    allow_follow         BOOLEAN NOT NULL DEFAULT FALSE,
    allow_unfollow       BOOLEAN NOT NULL DEFAULT FALSE,
    allow_repost         BOOLEAN NOT NULL DEFAULT FALSE,
    allow_quote          BOOLEAN NOT NULL DEFAULT FALSE,
    candidate_scope_type TEXT NOT NULL DEFAULT 'none',
    candidate_scope_params JSONB NOT NULL DEFAULT '{}'::jsonb,
    allow_ai_to_ai       BOOLEAN NOT NULL DEFAULT FALSE,
    rate_limits          JSONB NOT NULL DEFAULT '{}'::jsonb,
    cooldown_seconds     INTEGER NOT NULL DEFAULT 900,
    active_hours_start   INTEGER NOT NULL DEFAULT 8,
    active_hours_end     INTEGER NOT NULL DEFAULT 23,
    schedule             JSONB NOT NULL DEFAULT '{}'::jsonb,
    max_reply_depth      INTEGER NOT NULL DEFAULT 2,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ai_behavior_scope_check CHECK (candidate_scope_type IN ('none', 'global_public', 'followed', 'tags', 'authors')),
    CONSTRAINT ai_behavior_cooldown_check CHECK (cooldown_seconds >= 0),
    CONSTRAINT ai_behavior_hours_check CHECK (
        active_hours_start BETWEEN 0 AND 23 AND active_hours_end BETWEEN 0 AND 23
    ),
    CONSTRAINT ai_behavior_depth_check CHECK (max_reply_depth BETWEEN 0 AND 5)
);

CREATE TABLE timeline.ai_tasks (
    id                BIGINT PRIMARY KEY,
    ai_id             BIGINT NOT NULL,
    trigger_type      TEXT NOT NULL,
    trigger_ref_type  TEXT,
    trigger_ref_id    BIGINT,
    action_type       TEXT NOT NULL DEFAULT 'noop',
    status            TEXT NOT NULL DEFAULT 'pending',
    decision_summary  TEXT NOT NULL DEFAULT '',
    target_type       TEXT,
    target_id         BIGINT,
    result_ref_type   TEXT,
    result_ref_id     BIGINT,
    provider_id       BIGINT,
    model             TEXT NOT NULL DEFAULT '',
    error             TEXT NOT NULL DEFAULT '',
    attempts          INTEGER NOT NULL DEFAULT 0,
    idempotency_key   TEXT NOT NULL,
    started_at        TIMESTAMPTZ,
    finished_at       TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ai_tasks_trigger_check CHECK (trigger_type IN ('manual', 'schedule', 'mention', 'comment', 'proactive', 'retry')),
    CONSTRAINT ai_tasks_action_check CHECK (action_type IN (
        'original_post', 'comment', 'reply', 'like', 'unlike', 'follow', 'unfollow', 'repost', 'quote', 'noop'
    )),
    CONSTRAINT ai_tasks_status_check CHECK (status IN ('pending', 'running', 'succeeded', 'noop', 'rejected', 'failed', 'canceled')),
    CONSTRAINT ai_tasks_attempts_check CHECK (attempts >= 0)
);

CREATE UNIQUE INDEX ai_tasks_idempotency_unique ON timeline.ai_tasks (idempotency_key);
CREATE INDEX ai_tasks_ai ON timeline.ai_tasks (ai_id, id DESC);
CREATE INDEX ai_tasks_pending ON timeline.ai_tasks (id) WHERE status = 'pending';
CREATE INDEX ai_tasks_action ON timeline.ai_tasks (ai_id, action_type, finished_at DESC);

CREATE TABLE timeline.ai_task_memory_refs (
    id          BIGINT PRIMARY KEY,
    task_id     BIGINT NOT NULL,
    memory_id   BIGINT NOT NULL,
    memory_type TEXT NOT NULL,
    CONSTRAINT ai_task_memory_refs_type_check CHECK (memory_type IN ('recent', 'long_term'))
);

CREATE UNIQUE INDEX ai_task_memory_refs_unique ON timeline.ai_task_memory_refs (task_id, memory_id, memory_type);

CREATE TABLE timeline.ai_call_logs (
    id                     BIGINT PRIMARY KEY,
    task_id                BIGINT,
    ai_id                  BIGINT,
    provider_id            BIGINT,
    model                  TEXT NOT NULL DEFAULT '',
    params                 JSONB NOT NULL DEFAULT '{}'::jsonb,
    request_content        TEXT NOT NULL DEFAULT '',
    response_content       TEXT NOT NULL DEFAULT '',
    status                 TEXT NOT NULL DEFAULT 'success',
    http_status            INTEGER,
    error_type             TEXT NOT NULL DEFAULT '',
    error_message          TEXT NOT NULL DEFAULT '',
    usage_input_tokens     INTEGER,
    usage_output_tokens    INTEGER,
    usage_reasoning_tokens INTEGER,
    attempt                INTEGER NOT NULL DEFAULT 1,
    latency_ms             BIGINT NOT NULL DEFAULT 0,
    truncated              BOOLEAN NOT NULL DEFAULT FALSE,
    is_deleted             BOOLEAN NOT NULL DEFAULT FALSE,
    started_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ai_call_logs_status_check CHECK (status IN ('success', 'error')),
    CONSTRAINT ai_call_logs_attempt_check CHECK (attempt >= 1),
    CONSTRAINT ai_call_logs_latency_check CHECK (latency_ms >= 0)
);

CREATE INDEX ai_call_logs_task ON timeline.ai_call_logs (task_id, id);
CREATE INDEX ai_call_logs_ai ON timeline.ai_call_logs (ai_id, id DESC);
CREATE INDEX ai_call_logs_status ON timeline.ai_call_logs (status, id DESC);
CREATE INDEX ai_call_logs_provider ON timeline.ai_call_logs (provider_id, id DESC);
CREATE INDEX ai_call_logs_retention ON timeline.ai_call_logs (created_at) WHERE NOT is_deleted;

CREATE TABLE timeline.ai_action_counters (
    id           BIGINT PRIMARY KEY,
    ai_id        BIGINT NOT NULL,
    action_type  TEXT NOT NULL,
    counter_date DATE NOT NULL,
    count        INTEGER NOT NULL DEFAULT 0,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ai_action_counters_count_check CHECK (count >= 0)
);

CREATE UNIQUE INDEX ai_action_counters_unique ON timeline.ai_action_counters (ai_id, action_type, counter_date);

CREATE TABLE timeline.ai_memories (
    id               BIGINT PRIMARY KEY,
    ai_id            BIGINT NOT NULL,
    memory_type      TEXT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'active',
    content          TEXT NOT NULL,
    category         TEXT NOT NULL DEFAULT 'event',
    keywords         TEXT[] NOT NULL DEFAULT '{}',
    importance       INTEGER NOT NULL DEFAULT 3,
    source_type      TEXT NOT NULL DEFAULT 'manual',
    source_ref_type  TEXT,
    source_ref_id    BIGINT,
    inference_level  TEXT NOT NULL DEFAULT 'fact',
    superseded_by_id BIGINT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at     TIMESTAMPTZ,
    CONSTRAINT ai_memories_type_check CHECK (memory_type IN ('recent', 'long_term')),
    CONSTRAINT ai_memories_status_check CHECK (status IN ('active', 'superseded', 'invalidated', 'deleted')),
    CONSTRAINT ai_memories_category_check CHECK (category IN ('fact', 'preference', 'relationship', 'event', 'topic')),
    CONSTRAINT ai_memories_inference_check CHECK (inference_level IN ('fact', 'summary', 'inference')),
    CONSTRAINT ai_memories_importance_check CHECK (importance BETWEEN 1 AND 5)
);

CREATE INDEX ai_memories_ai ON timeline.ai_memories (ai_id, memory_type, status, id DESC);
CREATE INDEX ai_memories_keywords ON timeline.ai_memories USING GIN (keywords);
CREATE INDEX ai_memories_source ON timeline.ai_memories (source_ref_type, source_ref_id);

CREATE TABLE timeline.background_jobs (
    id              BIGINT PRIMARY KEY,
    job_type        TEXT NOT NULL,
    ref_type        TEXT,
    ref_id          BIGINT,
    status          TEXT NOT NULL DEFAULT 'pending',
    run_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    attempts        INTEGER NOT NULL DEFAULT 0,
    max_attempts    INTEGER NOT NULL DEFAULT 3,
    last_error      TEXT NOT NULL DEFAULT '',
    idempotency_key TEXT NOT NULL,
    payload         JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT background_jobs_status_check CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'paused', 'canceled')),
    CONSTRAINT background_jobs_attempts_check CHECK (attempts >= 0 AND max_attempts > 0)
);

CREATE UNIQUE INDEX background_jobs_idempotency_unique ON timeline.background_jobs (idempotency_key);
CREATE INDEX background_jobs_due ON timeline.background_jobs (run_at, id) WHERE status = 'pending';
CREATE INDEX background_jobs_ref ON timeline.background_jobs (ref_type, ref_id);
CREATE INDEX background_jobs_type_status ON timeline.background_jobs (job_type, status, id DESC);
