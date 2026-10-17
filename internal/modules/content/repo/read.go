package repo

import (
	"context"
	"strings"

	"github.com/great-magician-01/timeline/internal/shared/model"
)

// CandidatePostsQuery 是 AI 主动互动的公开候选内容过滤条件。
// AuthorIDs 与 UseAuthorFilter 由 app 通过 auth/social 端口解析后传入（repo 不读 users 表）。
type CandidatePostsQuery struct {
	ExcludeAuthorID int64
	UseAuthorFilter bool
	AuthorIDs       []int64
	Tags            []string
	Limit           int
}

// CandidatePostRow 是候选公开内容投影：帖子行 + 纯文本摘要 + 标签名。
type CandidatePostRow struct {
	Post    model.Post
	Summary string
	Tags    []string
}

// maxCandidateSummaryRunes 是候选摘要的字符上限（与旧 agent 上下文预算一致）。
const maxCandidateSummaryRunes = 400

// ListCandidatePosts 读取公开已发布候选内容：排除自己，可选按作者集合或标签过滤。
func (r *Repo) ListCandidatePosts(ctx context.Context, query CandidatePostsQuery) ([]CandidatePostRow, error) {
	if query.Limit <= 0 {
		return nil, nil
	}
	args := []any{query.ExcludeAuthorID}
	addArg := func(value any) string {
		args = append(args, value)
		return "$" + itoa(len(args))
	}
	where := []string{
		"p.status = 'published'",
		"p.visibility = 'public'",
		"p.author_id <> $1",
	}
	if query.UseAuthorFilter {
		if len(query.AuthorIDs) == 0 {
			return nil, nil
		}
		where = append(where, "p.author_id = ANY("+addArg(query.AuthorIDs)+"::bigint[])")
	}
	if len(query.Tags) > 0 {
		where = append(where, `p.id IN (
			SELECT pt.post_id FROM post_tags pt
			JOIN tags t ON t.id = pt.tag_id
			WHERE pt.is_active AND t.name = ANY(`+addArg(query.Tags)+`))`)
	}
	args = append(args, query.Limit)
	rows, err := r.ex(ctx).Query(ctx, `
		SELECT `+postColumnsP+`,
		       COALESCE(NULLIF(v.plain_text, ''), s.plain_text, ''),
		       COALESCE((SELECT array_agg(t.name ORDER BY t.name)
		                 FROM post_tags pt JOIN tags t ON t.id = pt.tag_id
		                 WHERE pt.post_id = p.id AND pt.is_active), '{}')
		FROM posts p
		LEFT JOIN post_versions v ON v.id = p.current_version_id
		LEFT JOIN post_search s ON s.post_id = p.id
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY p.published_at DESC NULLS LAST, p.id DESC
		LIMIT $`+itoa(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]CandidatePostRow, 0, query.Limit)
	for rows.Next() {
		var item CandidatePostRow
		post, err := scanPostWith(rows, &item.Summary, &item.Tags)
		if err != nil {
			return nil, err
		}
		item.Post = post
		item.Summary = truncateRunes(strings.TrimSpace(item.Summary), maxCandidateSummaryRunes)
		items = append(items, item)
	}
	return items, rows.Err()
}

// truncateRunes 按字符截断，避免破坏多字节字符。
func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
