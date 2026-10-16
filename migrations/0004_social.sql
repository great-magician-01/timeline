-- 0004_social：关注关系与申请、点赞、评论、纯转发、提及与通知。
-- 取消类操作（取关、取消点赞、取消转发）只置失效标记，不物理删除记录。

CREATE TABLE timeline.follows (
    id          BIGINT PRIMARY KEY,
    follower_id BIGINT NOT NULL,
    followee_id BIGINT NOT NULL,
    is_active   BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT follows_not_self_check CHECK (follower_id <> followee_id)
);

CREATE UNIQUE INDEX follows_pair_unique ON timeline.follows (follower_id, followee_id);
CREATE INDEX follows_follower_active ON timeline.follows (follower_id) WHERE is_active;
CREATE INDEX follows_followee_active ON timeline.follows (followee_id) WHERE is_active;

CREATE TABLE timeline.follow_requests (
    id           BIGINT PRIMARY KEY,
    requester_id BIGINT NOT NULL,
    target_id    BIGINT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at  TIMESTAMPTZ,
    CONSTRAINT follow_requests_status_check CHECK (status IN ('pending', 'approved', 'rejected', 'canceled')),
    CONSTRAINT follow_requests_resolved_check CHECK ((status = 'pending') = (resolved_at IS NULL)),
    CONSTRAINT follow_requests_not_self_check CHECK (requester_id <> target_id)
);

CREATE UNIQUE INDEX follow_requests_pending_unique ON timeline.follow_requests (requester_id, target_id) WHERE status = 'pending';
CREATE INDEX follow_requests_target ON timeline.follow_requests (target_id, status, id DESC);
CREATE INDEX follow_requests_requester ON timeline.follow_requests (requester_id, status, id DESC);

CREATE TABLE timeline.likes (
    id          BIGINT PRIMARY KEY,
    user_id     BIGINT NOT NULL,
    target_type TEXT NOT NULL,
    target_id   BIGINT NOT NULL,
    is_active   BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT likes_target_type_check CHECK (target_type IN ('post', 'comment'))
);

CREATE UNIQUE INDEX likes_target_unique ON timeline.likes (user_id, target_type, target_id);
CREATE INDEX likes_target_active ON timeline.likes (target_type, target_id) WHERE is_active;
CREATE INDEX likes_user_active ON timeline.likes (user_id, id DESC) WHERE is_active;

CREATE TABLE timeline.comments (
    id                BIGINT PRIMARY KEY,
    post_id           BIGINT NOT NULL,
    author_id         BIGINT NOT NULL,
    parent_comment_id BIGINT,
    root_comment_id   BIGINT,
    content_format    TEXT NOT NULL,
    content_source    TEXT NOT NULL,
    content_html      TEXT NOT NULL,
    plain_text        TEXT NOT NULL DEFAULT '',
    status            TEXT NOT NULL DEFAULT 'published',
    deleted_by_admin  BOOLEAN NOT NULL DEFAULT FALSE,
    deleted_by_author BOOLEAN NOT NULL DEFAULT FALSE,
    like_count        INTEGER NOT NULL DEFAULT 0,
    reply_count       INTEGER NOT NULL DEFAULT 0,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT comments_status_check CHECK (status IN ('published', 'deleted')),
    CONSTRAINT comments_format_check CHECK (content_format IN ('richtext', 'markdown')),
    CONSTRAINT comments_counts_check CHECK (like_count >= 0 AND reply_count >= 0),
    CONSTRAINT comments_thread_check CHECK (
        (parent_comment_id IS NULL AND root_comment_id IS NULL)
        OR (parent_comment_id IS NOT NULL AND root_comment_id IS NOT NULL)
    ),
    CONSTRAINT comments_deleted_flags_check CHECK (status = 'deleted' OR (NOT deleted_by_admin AND NOT deleted_by_author))
);

CREATE INDEX comments_post ON timeline.comments (post_id, id DESC);
CREATE INDEX comments_root ON timeline.comments (root_comment_id, id);
CREATE INDEX comments_author ON timeline.comments (author_id, id DESC);

CREATE TABLE timeline.reposts (
    id         BIGINT PRIMARY KEY,
    user_id    BIGINT NOT NULL,
    post_id    BIGINT NOT NULL,
    is_active  BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX reposts_unique ON timeline.reposts (user_id, post_id);
CREATE INDEX reposts_post_active ON timeline.reposts (post_id) WHERE is_active;
CREATE INDEX reposts_user_active ON timeline.reposts (user_id, id DESC) WHERE is_active;

CREATE TABLE timeline.mentions (
    id                 BIGINT PRIMARY KEY,
    source_type        TEXT NOT NULL,
    source_id          BIGINT NOT NULL,
    mentioned_user_id  BIGINT NOT NULL,
    is_active          BOOLEAN NOT NULL DEFAULT TRUE,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT mentions_source_type_check CHECK (source_type IN ('post', 'comment'))
);

CREATE UNIQUE INDEX mentions_unique ON timeline.mentions (source_type, source_id, mentioned_user_id);
CREATE INDEX mentions_user ON timeline.mentions (mentioned_user_id, id DESC);

CREATE TABLE timeline.notifications (
    id              BIGINT PRIMARY KEY,
    recipient_id    BIGINT NOT NULL,
    actor_id        BIGINT NOT NULL,
    type            TEXT NOT NULL,
    target_type     TEXT NOT NULL,
    target_id       BIGINT NOT NULL,
    post_id         BIGINT,
    comment_id      BIGINT,
    is_read         BOOLEAN NOT NULL DEFAULT FALSE,
    idempotency_key TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT notifications_type_check CHECK (type IN (
        'post_like', 'comment_like', 'comment', 'reply', 'repost', 'quote',
        'follow', 'follow_request', 'follow_approved', 'mention'
    )),
    CONSTRAINT notifications_target_type_check CHECK (target_type IN ('post', 'comment', 'user')),
    CONSTRAINT notifications_not_self_check CHECK (recipient_id <> actor_id)
);

CREATE UNIQUE INDEX notifications_idempotency_unique ON timeline.notifications (idempotency_key);
CREATE INDEX notifications_recipient ON timeline.notifications (recipient_id, id DESC);
CREATE INDEX notifications_recipient_unread ON timeline.notifications (recipient_id, id DESC) WHERE NOT is_read;
