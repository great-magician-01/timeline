package repo

import (
	"context"
	"time"
)

// TagRecord 是一次标签写入：ID 由 app 预生成（雪花 ID），name 为小写规范化键。
type TagRecord struct {
	ID          int64
	Name        string
	DisplayName string
}

// ReplacePostTags 重建当前标签投影：旧关联先失效，再 upsert 当前标签（不物理删除）。
func (r *Repo) ReplacePostTags(ctx context.Context, postID int64, tags []TagRecord, at time.Time) error {
	exec := r.ex(ctx)
	if _, err := exec.Exec(ctx, `
		UPDATE post_tags SET is_active = FALSE WHERE post_id = $1 AND is_active`, postID); err != nil {
		return err
	}
	for _, tag := range tags {
		var storedID int64
		if err := exec.QueryRow(ctx, `
			INSERT INTO tags (id, name, display_name, created_at)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (name) DO UPDATE SET display_name = EXCLUDED.display_name
			RETURNING id`,
			tag.ID, tag.Name, tag.DisplayName, at).Scan(&storedID); err != nil {
			return err
		}
		if _, err := exec.Exec(ctx, `
			INSERT INTO post_tags (post_id, tag_id, is_active) VALUES ($1, $2, TRUE)
			ON CONFLICT (post_id, tag_id) DO UPDATE SET is_active = TRUE`,
			postID, storedID); err != nil {
			return err
		}
	}
	return nil
}

// UpsertSearchText 写入/更新帖子搜索投影；updated_at 由调用方显式传入。
func (r *Repo) UpsertSearchText(ctx context.Context, postID int64, plain string, at time.Time) error {
	_, err := r.ex(ctx).Exec(ctx, `
		INSERT INTO post_search (post_id, plain_text, updated_at) VALUES ($1, $2, $3)
		ON CONFLICT (post_id) DO UPDATE SET plain_text = EXCLUDED.plain_text, updated_at = EXCLUDED.updated_at`,
		postID, plain, at)
	return err
}

// ClearSearchText 清空帖子搜索投影（逻辑删除/转非公开，不物理删除行）。
func (r *Repo) ClearSearchText(ctx context.Context, postID int64, at time.Time) error {
	_, err := r.ex(ctx).Exec(ctx, `
		UPDATE post_search SET plain_text = '', updated_at = $2 WHERE post_id = $1`, postID, at)
	return err
}

// TagExists 判断标签是否已被任何历史内容使用过。
func (r *Repo) TagExists(ctx context.Context, name string) (bool, error) {
	var exists bool
	err := r.ex(ctx).QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM tags WHERE name = $1)`, name).Scan(&exists)
	return exists, err
}

// TagSuggestionRow 是标签自动补全项。
type TagSuggestionRow struct {
	Name        string
	DisplayName string
	PostCount   int
}

// SuggestTags 按前缀匹配标签，且只统计当前访问者可见的已发布帖子。
func (r *Repo) SuggestTags(
	ctx context.Context,
	viewerID int64,
	followingIDs []int64,
	viewerIsAI bool,
	keyword string,
	limit int,
) ([]TagSuggestionRow, error) {
	rows, err := r.ex(ctx).Query(ctx, `
		SELECT tg.name, tg.display_name, COUNT(DISTINCT p.id) AS post_count
		FROM post_tags pt
		JOIN tags tg ON tg.id = pt.tag_id
		JOIN posts p ON p.id = pt.post_id
		WHERE pt.is_active AND p.status = 'published'
		  AND (p.visibility = 'public' OR p.author_id = $1
		       OR (p.visibility = 'followers' AND NOT $3::boolean AND p.author_id = ANY($2::bigint[])))
		  AND STRPOS(tg.name, $4) = 1
		GROUP BY tg.name, tg.display_name
		ORDER BY post_count DESC, tg.name
		LIMIT $5`, viewerID, followingIDs, viewerIsAI, keyword, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]TagSuggestionRow, 0, limit)
	for rows.Next() {
		var item TagSuggestionRow
		if err := rows.Scan(&item.Name, &item.DisplayName, &item.PostCount); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
