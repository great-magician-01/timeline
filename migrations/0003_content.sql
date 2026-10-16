-- 0003_content：帖子、草稿、历史版本、标签、搜索投影、附件与衍生文件。
-- 约定：无外键约束（引用列只用普通索引，完整性由应用层在事务内维护）；
--       全部数据逻辑删除（状态字段或 deleted_at），不做物理删除。
-- 注意：迁移器按 ASCII 分号简单分句（先剔除行注释），本文件不得出现含分号的
-- 复合语句（函数体、DO 块）或含 "--" 的字符串字面量。

CREATE TABLE timeline.posts (
    id                 BIGINT PRIMARY KEY,
    author_id          BIGINT NOT NULL,
    status             TEXT NOT NULL DEFAULT 'draft',
    visibility         TEXT NOT NULL DEFAULT 'public',
    current_version_id BIGINT,
    quoted_post_id     BIGINT,
    scheduled_at       TIMESTAMPTZ,
    published_at       TIMESTAMPTZ,
    deleted_at         TIMESTAMPTZ,
    like_count         INTEGER NOT NULL DEFAULT 0,
    comment_count      INTEGER NOT NULL DEFAULT 0,
    repost_count       INTEGER NOT NULL DEFAULT 0,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT posts_status_check CHECK (status IN ('draft', 'scheduled', 'published', 'deleted')),
    CONSTRAINT posts_visibility_check CHECK (visibility IN ('public', 'followers', 'private')),
    CONSTRAINT posts_counts_check CHECK (like_count >= 0 AND comment_count >= 0 AND repost_count >= 0),
    CONSTRAINT posts_published_check CHECK (status <> 'published' OR published_at IS NOT NULL),
    CONSTRAINT posts_deleted_check CHECK ((status = 'deleted') = (deleted_at IS NOT NULL)),
    CONSTRAINT posts_scheduled_check CHECK (status <> 'scheduled' OR scheduled_at IS NOT NULL),
    CONSTRAINT posts_quoted_check CHECK (quoted_post_id IS NULL OR quoted_post_id <> id)
);

CREATE INDEX posts_timeline ON timeline.posts (published_at DESC, id DESC) WHERE status = 'published';
CREATE INDEX posts_author ON timeline.posts (author_id, status, id DESC);
CREATE INDEX posts_author_published ON timeline.posts (author_id, published_at DESC, id DESC) WHERE status = 'published';
CREATE INDEX posts_scheduled ON timeline.posts (scheduled_at, id) WHERE status = 'scheduled';
CREATE INDEX posts_quoted ON timeline.posts (quoted_post_id);
CREATE INDEX posts_current_version ON timeline.posts (current_version_id);
CREATE INDEX posts_status ON timeline.posts (status, id DESC);

CREATE TABLE timeline.post_drafts (
    post_id        BIGINT PRIMARY KEY,
    content_format TEXT NOT NULL,
    content_source TEXT NOT NULL,
    content_html   TEXT NOT NULL,
    plain_text     TEXT NOT NULL DEFAULT '',
    draft_revision INTEGER NOT NULL DEFAULT 1,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT post_drafts_format_check CHECK (content_format IN ('richtext', 'markdown')),
    CONSTRAINT post_drafts_revision_check CHECK (draft_revision > 0)
);

CREATE TABLE timeline.post_versions (
    id                       BIGINT PRIMARY KEY,
    post_id                  BIGINT NOT NULL,
    version_number           INTEGER NOT NULL,
    content_format           TEXT NOT NULL,
    content_source           TEXT NOT NULL,
    content_html             TEXT NOT NULL,
    plain_text               TEXT NOT NULL DEFAULT '',
    visibility               TEXT NOT NULL,
    restored_from_version_id BIGINT,
    created_by               BIGINT NOT NULL,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT post_versions_format_check CHECK (content_format IN ('richtext', 'markdown')),
    CONSTRAINT post_versions_visibility_check CHECK (visibility IN ('public', 'followers', 'private')),
    CONSTRAINT post_versions_number_check CHECK (version_number > 0)
);

CREATE UNIQUE INDEX post_versions_post_number_unique ON timeline.post_versions (post_id, version_number);
CREATE INDEX post_versions_post ON timeline.post_versions (post_id, version_number DESC);

CREATE TABLE timeline.post_version_attachments (
    version_id    BIGINT NOT NULL,
    attachment_id BIGINT NOT NULL,
    sort_order    INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (version_id, attachment_id)
);

CREATE INDEX post_version_attachments_attachment ON timeline.post_version_attachments (attachment_id);
CREATE INDEX post_version_attachments_version ON timeline.post_version_attachments (version_id, sort_order);

CREATE TABLE timeline.post_draft_attachments (
    post_id       BIGINT NOT NULL,
    attachment_id BIGINT NOT NULL,
    sort_order    INTEGER NOT NULL DEFAULT 0,
    is_active     BOOLEAN NOT NULL DEFAULT TRUE,
    PRIMARY KEY (post_id, attachment_id)
);

CREATE INDEX post_draft_attachments_attachment ON timeline.post_draft_attachments (attachment_id) WHERE is_active;
CREATE INDEX post_draft_attachments_post ON timeline.post_draft_attachments (post_id, sort_order) WHERE is_active;

CREATE TABLE timeline.tags (
    id           BIGINT PRIMARY KEY,
    name         TEXT NOT NULL,
    display_name TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX tags_name_unique ON timeline.tags (name);

CREATE TABLE timeline.post_tags (
    post_id   BIGINT NOT NULL,
    tag_id    BIGINT NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    PRIMARY KEY (post_id, tag_id)
);

CREATE INDEX post_tags_tag ON timeline.post_tags (tag_id, post_id DESC) WHERE is_active;
CREATE INDEX post_tags_post ON timeline.post_tags (post_id) WHERE is_active;

CREATE TABLE timeline.post_search (
    post_id    BIGINT PRIMARY KEY,
    plain_text TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX post_search_updated ON timeline.post_search (updated_at DESC);

CREATE TABLE timeline.attachments (
    id                BIGINT PRIMARY KEY,
    uploader_id       BIGINT NOT NULL,
    post_id           BIGINT,
    kind              TEXT NOT NULL,
    purpose           TEXT NOT NULL DEFAULT 'post',
    original_filename TEXT NOT NULL,
    media_type        TEXT NOT NULL,
    size_bytes        BIGINT NOT NULL,
    storage_key       TEXT NOT NULL,
    status            TEXT NOT NULL DEFAULT 'available',
    width             INTEGER,
    height            INTEGER,
    duration_ms       BIGINT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT attachments_kind_check CHECK (kind IN ('image', 'video', 'file')),
    CONSTRAINT attachments_purpose_check CHECK (purpose IN ('post', 'avatar')),
    CONSTRAINT attachments_status_check CHECK (status IN ('available', 'deleted')),
    CONSTRAINT attachments_size_check CHECK (size_bytes >= 0),
    CONSTRAINT attachments_dimensions_check CHECK (
        (width IS NULL OR width > 0) AND (height IS NULL OR height > 0)
        AND (duration_ms IS NULL OR duration_ms >= 0)
    )
);

CREATE UNIQUE INDEX attachments_storage_key_unique ON timeline.attachments (storage_key);
CREATE INDEX attachments_uploader ON timeline.attachments (uploader_id, id DESC);
CREATE INDEX attachments_post ON timeline.attachments (post_id);
CREATE INDEX attachments_unlinked ON timeline.attachments (created_at) WHERE post_id IS NULL AND status = 'available';

CREATE TABLE timeline.attachment_derivatives (
    id              BIGINT PRIMARY KEY,
    attachment_id   BIGINT NOT NULL,
    derivative_type TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending',
    storage_key     TEXT NOT NULL DEFAULT '',
    last_error      TEXT NOT NULL DEFAULT '',
    attempts        INTEGER NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT attachment_derivatives_type_check CHECK (derivative_type IN ('thumbnail', 'video_cover')),
    CONSTRAINT attachment_derivatives_status_check CHECK (status IN ('pending', 'processing', 'ready', 'failed', 'canceled')),
    CONSTRAINT attachment_derivatives_attempts_check CHECK (attempts >= 0)
);

CREATE UNIQUE INDEX attachment_derivatives_unique ON timeline.attachment_derivatives (attachment_id, derivative_type);
CREATE INDEX attachment_derivatives_status ON timeline.attachment_derivatives (status, id);
