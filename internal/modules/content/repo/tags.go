package repo

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// TagRecord 是一次标签写入：ID 由 app 预生成（雪花 ID），name 为小写规范化键。
type TagRecord struct {
	ID          int64
	Name        string
	DisplayName string
}

// ReplacePostTags 重建当前标签投影：旧关联先失效，再批量 upsert 当前标签（不物理删除）。
//
// 写入合并为两条语句（tags 多值 upsert + post_tags 多值 upsert），把原先 2N 次往返
// 压缩为固定 3 条，缩短发布/编辑事务的持锁时间。按 name 去重后再插入，避免同一条
// INSERT ... ON CONFLICT 语句内重复命中同一唯一键（PostgreSQL 会直接报错）。
func (r *Repo) ReplacePostTags(ctx context.Context, postID int64, tags []TagRecord, at time.Time) error {
	exec := r.ex(ctx)
	if _, err := exec.Exec(ctx, `
		UPDATE post_tags SET is_active = FALSE WHERE post_id = $1 AND is_active`, postID); err != nil {
		return err
	}
	records := uniqueTagRecords(tags)
	if len(records) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(records))
	names := make([]string, 0, len(records))
	displays := make([]string, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.ID)
		names = append(names, record.Name)
		displays = append(displays, record.DisplayName)
	}
	// 命中 tags_name_unique 冲突时保留已有标签 ID，只刷新展示名。
	rows, err := exec.Query(ctx, `
		INSERT INTO tags (id, name, display_name, created_at)
		SELECT t.id, t.name, t.display_name, $4
		FROM unnest($1::bigint[], $2::text[], $3::text[]) AS t(id, name, display_name)
		ON CONFLICT (name) DO UPDATE SET display_name = EXCLUDED.display_name
		RETURNING id, name`, ids, names, displays, at)
	if err != nil {
		return err
	}
	stored := make(map[string]int64, len(records))
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()
			return err
		}
		stored[name] = id
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	tagIDs := make([]int64, 0, len(records))
	for _, record := range records {
		if id, ok := stored[record.Name]; ok {
			tagIDs = append(tagIDs, id)
		}
	}
	if len(tagIDs) == 0 {
		return nil
	}
	_, err = exec.Exec(ctx, `
		INSERT INTO post_tags (post_id, tag_id, is_active)
		SELECT $1, t.tag_id, TRUE FROM unnest($2::bigint[]) AS t(tag_id)
		ON CONFLICT (post_id, tag_id) DO UPDATE SET is_active = TRUE`, postID, tagIDs)
	return err
}

// uniqueTagRecords 按 name 去重并保序（同名标签只保留首次出现的记录）。
func uniqueTagRecords(tags []TagRecord) []TagRecord {
	out := make([]TagRecord, 0, len(tags))
	seen := make(map[string]bool, len(tags))
	for _, tag := range tags {
		if tag.Name == "" || seen[tag.Name] {
			continue
		}
		seen[tag.Name] = true
		out = append(out, tag)
	}
	return out
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

// LookupTag 按规范化名称解析标签 ID；不存在返回 (0, false)。
// 标签时间线先用它把 name 解析为 tag_id，候选 SQL 只按 post_tags 主键 (post_id, tag_id) 探测，
// 不再逐候选行 JOIN tags 解析名称。
func (r *Repo) LookupTag(ctx context.Context, name string) (int64, bool, error) {
	var id int64
	err := r.ex(ctx).QueryRow(ctx, `SELECT id FROM tags WHERE name = $1`, name).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return id, true, nil
}

// TagPrefixRow 是标签前缀查询命中的标签行。
type TagPrefixRow struct {
	ID          int64
	Name        string
	DisplayName string
}

// sqlListTagPrefixes 按名称前缀读取标签候选（命中 0017 的 tags_name_prefix 索引）。
// keyword 必须已由 app 用 shared/search.EscapeLikePattern 转义并按小写规范化，
// 这里只拼接通配符并显式 ESCAPE '\'，保证 %/_/\ 按字面量前缀匹配。
const sqlListTagPrefixes = `
	SELECT id, name, display_name FROM tags
	WHERE name LIKE $1 || '%' ESCAPE '\'
	ORDER BY name
	LIMIT $2`

// ListTagPrefixes 按名称前缀读取标签候选（升序、最多 limit 条）。
func (r *Repo) ListTagPrefixes(ctx context.Context, keyword string, limit int) ([]TagPrefixRow, error) {
	if keyword == "" || limit <= 0 {
		return nil, nil
	}
	rows, err := r.ex(ctx).Query(ctx, sqlListTagPrefixes, keyword, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]TagPrefixRow, 0, limit)
	for rows.Next() {
		var item TagPrefixRow
		if err := rows.Scan(&item.ID, &item.Name, &item.DisplayName); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// sqlPostCountsByTagIDs 统计这批标签下「当前访问者可见的已发布帖子」数量。
// post_tags 主键是 (post_id, tag_id)，同一标签内 post_id 唯一，因此 COUNT(*) 不需要 DISTINCT；
// 候选标签数由 ListTagPrefixes 的上限约束，聚合宽度可控。
const sqlPostCountsByTagIDs = `
	SELECT pt.tag_id, COUNT(*)
	FROM post_tags pt
	JOIN posts p ON p.id = pt.post_id
	WHERE pt.tag_id = ANY($1) AND pt.is_active AND p.status = 'published'
	  AND (p.visibility = 'public' OR p.author_id = $2
	       OR (p.visibility = 'followers' AND NOT $4::boolean AND p.author_id = ANY($3::bigint[])))
	GROUP BY pt.tag_id`

// PostCountsByTagIDs 批量统计标签下访问者可见的已发布帖子数，返回 tag_id → count。
func (r *Repo) PostCountsByTagIDs(
	ctx context.Context,
	tagIDs []int64,
	viewerID int64,
	followingIDs []int64,
	viewerIsAI bool,
) (map[int64]int, error) {
	result := make(map[int64]int, len(tagIDs))
	if len(tagIDs) == 0 {
		return result, nil
	}
	rows, err := r.ex(ctx).Query(ctx, sqlPostCountsByTagIDs, tagIDs, viewerID, followingIDs, viewerIsAI)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var tagID int64
		var count int
		if err := rows.Scan(&tagID, &count); err != nil {
			return nil, err
		}
		result[tagID] = count
	}
	return result, rows.Err()
}
