-- 0025_group_chat：群聊（多人会话）表结构与 AI 群聊发言开关。
--
-- 本文件是普通事务迁移（无 no-transaction 指令），由迁移器整体包在一个事务内执行。
--
-- 背景与依据（docs/REQUIREMENTS.md §10.9、docs/DATA_MODEL.md §6.35–§6.37）：
--   1. 群聊与 1:1 私信并存的独立域：group_conversations 按群主（owner_id）管理，
--      status 仅 active/dissolved 两个状态；解散只置状态，不物理删除（红线）。
--   2. 成员资格是逻辑删除模型：group_members.left_at 为空表示有效成员；
--      UNIQUE (group_id, user_id) 兜底重复拉人，被踢/退出后重新拉人复用同一行。
--   3. 未读采用成员级游标（last_read_message_id）而不是逐消息标记：一条群消息有
--      N 个接收者，per-message read 模型会成倍放大写入；未读数按
--      id > 游标 AND sender_id != 自己 AND status='published' 实时 COUNT，
--      与 dm 的 recipient_id + read_at 模型（1:1 场景）互不影响。
--   4. 隐藏采用 hidden_message_id 指针而不是清位式 hidden_at：群发消息时不能
--      全量 UPDATE 成员行（最简原则）；列表谓词为
--      hidden_message_id IS NULL OR last_message_id > hidden_message_id，
--      群有更新的新消息时会话自动重新出现。
--   5. AI 可被拉入群聊（与私信不同）；AI 是否在群内发言由 ai_behavior_settings
--      的 allow_group_message（自主发言，默认关）与既有 allow_passive_reply（被 @
--      回复）控制，本迁移只加开关列。schedule 间隔键存 JSONB，无需 DDL。
--   6. 新表在空表上建索引，无需 CONCURRENTLY；不建外键、不物理删除、不引入业务
--      时间默认值（0011 红线），全部无回填，可安全在升级窗口内按文件名顺序执行。
CREATE TABLE timeline.group_conversations (
    id              BIGINT PRIMARY KEY,
    name            TEXT NOT NULL,
    owner_id        BIGINT NOT NULL,
    last_message_id BIGINT,
    status          TEXT NOT NULL DEFAULT 'active',
    created_at      TIMESTAMPTZ NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL,
    CONSTRAINT group_conversations_status_check CHECK (status IN ('active', 'dissolved'))
);

CREATE INDEX group_conversations_owner ON timeline.group_conversations (owner_id);

CREATE TABLE timeline.group_members (
    id                   BIGINT PRIMARY KEY,
    group_id             BIGINT NOT NULL,
    user_id              BIGINT NOT NULL,
    role                 TEXT NOT NULL DEFAULT 'member',
    last_read_message_id BIGINT,
    hidden_message_id    BIGINT,
    joined_at            TIMESTAMPTZ NOT NULL,
    left_at              TIMESTAMPTZ,
    CONSTRAINT group_members_role_check CHECK (role IN ('owner', 'member'))
);

CREATE UNIQUE INDEX group_members_pair_unique ON timeline.group_members (group_id, user_id);

CREATE INDEX group_members_user_active ON timeline.group_members (user_id) WHERE left_at IS NULL;

CREATE INDEX group_members_group_active ON timeline.group_members (group_id, joined_at) WHERE left_at IS NULL;

CREATE TABLE timeline.group_messages (
    id             BIGINT PRIMARY KEY,
    group_id       BIGINT NOT NULL,
    sender_id      BIGINT NOT NULL,
    content_format TEXT NOT NULL,
    content_source TEXT NOT NULL,
    content_html   TEXT NOT NULL,
    plain_text     TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL DEFAULT 'published',
    created_at     TIMESTAMPTZ NOT NULL,
    updated_at     TIMESTAMPTZ NOT NULL,
    CONSTRAINT group_messages_format_check CHECK (content_format = 'richtext'),
    CONSTRAINT group_messages_status_check CHECK (status IN ('published', 'recalled'))
);

CREATE INDEX group_messages_group ON timeline.group_messages (group_id, id DESC);

ALTER TABLE timeline.ai_behavior_settings ADD COLUMN allow_group_message BOOLEAN NOT NULL DEFAULT FALSE;

-- 群聊引入新的任务触发类型（group_mention：群消息 @提及；group_schedule：群聊
-- 自主发言定时桶）与动作类型（group_message，被 @ 回复与自主群发言共用），
-- 扩展 ai_tasks 的取值约束。0025 与约束变更同属群聊一个特性批次（0025 尚未在
-- 任何环境先行应用）。
ALTER TABLE timeline.ai_tasks DROP CONSTRAINT ai_tasks_trigger_check;

ALTER TABLE timeline.ai_tasks ADD CONSTRAINT ai_tasks_trigger_check CHECK (
    trigger_type IN ('manual', 'schedule', 'mention', 'comment', 'proactive', 'retry', 'group_mention', 'group_schedule')
);

ALTER TABLE timeline.ai_tasks DROP CONSTRAINT ai_tasks_action_check;

ALTER TABLE timeline.ai_tasks ADD CONSTRAINT ai_tasks_action_check CHECK (
    action_type IN (
        'original_post', 'comment', 'reply', 'like', 'unlike', 'follow', 'unfollow', 'repost', 'quote', 'noop',
        'group_message'
    )
)
