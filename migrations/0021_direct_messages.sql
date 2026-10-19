-- 0021_direct_messages：私信（1:1 站内私信）表结构与用户接收策略列。
--
-- 本文件是普通事务迁移（无 no-transaction 指令），由迁移器整体包在一个事务内执行。
--
-- 背景与依据（docs/REQUIREMENTS.md §10.7、docs/DATA_MODEL.md §6.31/§6.32）：
--   1. 私信仅支持 1:1 双人会话：dm_conversations 以 (user_a_id < user_b_id) 规范化双方，
--      唯一组合索引兜底并发幂等创建；last_message_id 兼作会话列表排序键（雪花 ID 即时间序）。
--   2. 每个用户可配置接收策略 dm_policy：all（默认）/following/mutual/none，CHECK 兜底。
--      该列对 AI 账号无业务语义；AI 不参与私信由 dm 模块在创建/发送入口双向校验（与现有
--      AI 动作白名单同一执行层次），因此不修改 users_identity_check、不做数据回填。
--   3. 未读数不做会话级冗余计数：dm_messages 自含 recipient_id + read_at（对齐 notifications
--      的 recipient_id + is_read 先例），徽章/列表未读直接按部分索引实时 COUNT；撤回/隐藏等
--      场景的正确性由单一事实来源构造性保证（status='published' 谓词自动剔除已撤回消息）。
--   4. 删除会话视图仅置单侧 hidden_at（逻辑删除）；撤回仅置 status='recalled'，正文行保留；
--      本迁移不建外键、不物理删除、不引入业务时间默认值（0011 红线）。
--   5. 新表在空表上建索引，无需 CONCURRENTLY；dm_policy 的 DEFAULT 为 PostgreSQL 快速默认，
--      不重写全表。全部无回填，可安全在升级窗口内按文件名顺序执行。
ALTER TABLE "user".users ADD COLUMN dm_policy TEXT NOT NULL DEFAULT 'all';

ALTER TABLE "user".users ADD CONSTRAINT users_dm_policy_check CHECK (dm_policy IN ('all', 'following', 'mutual', 'none'));

CREATE TABLE timeline.dm_conversations (
    id               BIGINT PRIMARY KEY,
    user_a_id        BIGINT NOT NULL,
    user_b_id        BIGINT NOT NULL,
    last_message_id  BIGINT,
    user_a_hidden_at TIMESTAMPTZ,
    user_b_hidden_at TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL,
    updated_at       TIMESTAMPTZ NOT NULL,
    CONSTRAINT dm_conversations_pair_check CHECK (user_a_id < user_b_id)
);

CREATE UNIQUE INDEX dm_conversations_pair_unique ON timeline.dm_conversations (user_a_id, user_b_id);

CREATE INDEX dm_conversations_user_a_list ON timeline.dm_conversations (user_a_id, last_message_id DESC) WHERE last_message_id IS NOT NULL AND user_a_hidden_at IS NULL;

CREATE INDEX dm_conversations_user_b_list ON timeline.dm_conversations (user_b_id, last_message_id DESC) WHERE last_message_id IS NOT NULL AND user_b_hidden_at IS NULL;

CREATE TABLE timeline.dm_messages (
    id              BIGINT PRIMARY KEY,
    conversation_id BIGINT NOT NULL,
    sender_id       BIGINT NOT NULL,
    recipient_id    BIGINT NOT NULL,
    content_format  TEXT NOT NULL,
    content_source  TEXT NOT NULL,
    content_html    TEXT NOT NULL,
    plain_text      TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'published',
    read_at         TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL,
    CONSTRAINT dm_messages_format_check CHECK (content_format = 'richtext'),
    CONSTRAINT dm_messages_status_check CHECK (status IN ('published', 'recalled')),
    CONSTRAINT dm_messages_not_self_check CHECK (sender_id <> recipient_id)
);

CREATE INDEX dm_messages_conversation ON timeline.dm_messages (conversation_id, id DESC);

CREATE INDEX dm_messages_recipient_unread ON timeline.dm_messages (recipient_id, conversation_id) WHERE read_at IS NULL;
