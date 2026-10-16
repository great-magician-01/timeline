-- 0002_auth_and_admin：账号、服务端会话、注册设置与管理审计。
-- 所有 ID 由应用侧雪花生成器产生；user 是保留字，必须带双引号。

CREATE TABLE "user".users (
    id                      BIGINT PRIMARY KEY,
    username                TEXT NOT NULL,
    email                   TEXT,
    password_hash           TEXT,
    nickname                TEXT NOT NULL,
    avatar_url              TEXT NOT NULL DEFAULT '',
    bio                     TEXT NOT NULL DEFAULT '',
    role                    TEXT NOT NULL DEFAULT 'user',
    is_ai                   BOOLEAN NOT NULL DEFAULT FALSE,
    status                  TEXT NOT NULL DEFAULT 'active',
    require_follow_approval BOOLEAN NOT NULL DEFAULT FALSE,
    token_version           BIGINT NOT NULL DEFAULT 1,
    suspended_at            TIMESTAMPTZ,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT users_role_check CHECK (role IN ('user', 'admin')),
    CONSTRAINT users_status_check CHECK (status IN ('active', 'suspended')),
    CONSTRAINT users_token_version_check CHECK (token_version > 0),
    CONSTRAINT users_identity_check CHECK (
        (is_ai AND role = 'user' AND email IS NULL AND password_hash IS NULL AND NOT require_follow_approval)
        OR
        (NOT is_ai AND email IS NOT NULL AND password_hash IS NOT NULL)
    ),
    CONSTRAINT users_suspension_check CHECK (
        (status = 'active' AND suspended_at IS NULL)
        OR
        (status = 'suspended' AND suspended_at IS NOT NULL)
    )
);

CREATE UNIQUE INDEX users_username_unique ON "user".users (LOWER(username));
CREATE UNIQUE INDEX users_email_unique ON "user".users (LOWER(email)) WHERE email IS NOT NULL;
CREATE INDEX users_admin_list ON "user".users (id DESC);
CREATE INDEX users_status ON "user".users (status);

CREATE TABLE "user".auth_sessions (
    id            BIGINT PRIMARY KEY,
    user_id       BIGINT NOT NULL,
    token_hash    TEXT NOT NULL UNIQUE,
    csrf_token    TEXT NOT NULL,
    token_version BIGINT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ NOT NULL,
    revoked_at    TIMESTAMPTZ,
    CONSTRAINT auth_sessions_token_hash_check CHECK (LENGTH(token_hash) = 64),
    CONSTRAINT auth_sessions_token_version_check CHECK (token_version > 0),
    CONSTRAINT auth_sessions_expiry_check CHECK (expires_at > created_at)
);

CREATE INDEX auth_sessions_user ON "user".auth_sessions (user_id, revoked_at, expires_at);
CREATE INDEX auth_sessions_expiry ON "user".auth_sessions (expires_at) WHERE revoked_at IS NULL;

CREATE TABLE timeline.system_settings (
    key        TEXT PRIMARY KEY,
    value      JSONB NOT NULL,
    updated_by BIGINT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO timeline.system_settings (key, value)
VALUES ('registration.enabled', 'true'::jsonb);

CREATE TABLE timeline.admin_audit_logs (
    id          BIGINT PRIMARY KEY,
    admin_id    BIGINT NOT NULL,
    action_type TEXT NOT NULL,
    target_type TEXT NOT NULL,
    target_id   BIGINT,
    detail      JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX admin_audit_logs_created ON timeline.admin_audit_logs (id DESC);
CREATE INDEX admin_audit_logs_action ON timeline.admin_audit_logs (action_type, id DESC);
CREATE INDEX admin_audit_logs_admin ON timeline.admin_audit_logs (admin_id, id DESC);
