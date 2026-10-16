-- 0006_comment_delete_actor：评论删除需要区分三种处置角色
-- （评论作者、帖子作者、管理员，docs/REQUIREMENTS.md 5.1、docs/WORKFLOWS.md 5.1）。
-- 原有的 deleted_by_admin / deleted_by_author 两个标记无法区分“帖子作者删除”，
-- 这里补一列并把 CHECK 收紧到同样的“逻辑删除才允许带标记”口径。

ALTER TABLE timeline.comments ADD COLUMN deleted_by_post_author BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE timeline.comments DROP CONSTRAINT comments_deleted_flags_check;

ALTER TABLE timeline.comments ADD CONSTRAINT comments_deleted_flags_check CHECK (
    status = 'deleted'
    OR (
        NOT deleted_by_admin
        AND NOT deleted_by_author
        AND NOT deleted_by_post_author
    )
);
