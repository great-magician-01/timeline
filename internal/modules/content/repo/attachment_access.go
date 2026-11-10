package repo

import (
	"context"

	"github.com/great-magician-01/timeline/internal/shared/model"
)

// AttachmentPosts 返回引用指定附件的全部帖子（版本或生效草稿关联），供 media 统一鉴权。
// 附件可复用于多帖，attachments.post_id 不再是权限事实来源。
func (r *Repo) AttachmentPosts(ctx context.Context, attachmentID int64) ([]model.Post, error) {
	rows, err := r.ex(ctx).Query(ctx, `
		SELECT `+postColumnsP+` FROM posts AS p
		WHERE EXISTS (
			SELECT 1 FROM post_versions AS pv
			JOIN post_version_attachments AS pva ON pva.version_id = pv.id
			WHERE pv.post_id = p.id AND pva.attachment_id = $1
		) OR EXISTS (
			SELECT 1 FROM post_draft_attachments AS pda
			WHERE pda.post_id = p.id AND pda.attachment_id = $1 AND pda.is_active
		)
		ORDER BY p.id`, attachmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.Post, 0)
	for rows.Next() {
		post, err := scanPost(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, post)
	}
	return items, rows.Err()
}

// HasNonPublicAttachmentReference 判断附件是否受非公开或已删除帖子约束。
// 公开草稿/定时帖表达的是目标公开级别，不视为私密引用；删除帖始终构成约束。
func (r *Repo) HasNonPublicAttachmentReference(ctx context.Context, attachmentID int64) (bool, error) {
	var restricted bool
	err := r.ex(ctx).QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM posts AS p
			WHERE (p.status = 'deleted' OR p.visibility <> 'public')
			  AND (
				EXISTS (
					SELECT 1 FROM post_versions AS pv
					JOIN post_version_attachments AS pva ON pva.version_id = pv.id
					WHERE pv.post_id = p.id AND pva.attachment_id = $1
				) OR EXISTS (
					SELECT 1 FROM post_draft_attachments AS pda
					WHERE pda.post_id = p.id AND pda.attachment_id = $1 AND pda.is_active
				)
			  )
		)`, attachmentID).Scan(&restricted)
	return restricted, err
}

// ReferencedAttachmentIDs 批量返回仍被任一历史版本或生效草稿引用的附件 ID。
func (r *Repo) ReferencedAttachmentIDs(ctx context.Context, attachmentIDs []int64) ([]int64, error) {
	if len(attachmentIDs) == 0 {
		return []int64{}, nil
	}
	rows, err := r.ex(ctx).Query(ctx, `
		SELECT attachment_id FROM post_version_attachments WHERE attachment_id = ANY($1)
		UNION
		SELECT attachment_id FROM post_draft_attachments
		WHERE attachment_id = ANY($1) AND is_active
		ORDER BY attachment_id`, attachmentIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// AttachmentIDsForPost 返回某帖所有版本及生效草稿引用的附件，供删除时撤销公开。
func (r *Repo) AttachmentIDsForPost(ctx context.Context, postID int64) ([]int64, error) {
	rows, err := r.ex(ctx).Query(ctx, `
		SELECT pva.attachment_id FROM post_versions AS pv
		JOIN post_version_attachments AS pva ON pva.version_id = pv.id
		WHERE pv.post_id = $1
		UNION
		SELECT attachment_id FROM post_draft_attachments
		WHERE post_id = $1 AND is_active
		ORDER BY attachment_id`, postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// DetachAttachmentFromDraft 精确解除一个草稿工作区中的一条附件关联。
func (r *Repo) DetachAttachmentFromDraft(ctx context.Context, postID, attachmentID int64) (bool, error) {
	tag, err := r.ex(ctx).Exec(ctx, `
		UPDATE post_draft_attachments SET is_active = FALSE
		WHERE post_id = $1 AND attachment_id = $2 AND is_active`, postID, attachmentID)
	return tag.RowsAffected() > 0, err
}
