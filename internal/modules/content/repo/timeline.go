package repo

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/content/domain"
)

// TimelineQuery 描述一次时间线帖子候选查询（纯转发分支由 social 端口提供）。
type TimelineQuery struct {
	ViewerID     int64
	ViewerIsAI   bool
	FollowingIDs []int64
	AuthorIDs    []int64 // 作者类型过滤（human/ai）：由 app 通过 auth 端口解析后传入

	IncludePosts  bool
	QuotedOnly    bool
	FollowingOnly bool
	AuthorID      int64
	Filter        string
	TagName       string
	SearchText    string

	AfterTime time.Time
	AfterID   int64
	Limit     int
}

// CandidateRow 是时间线候选：帖子或（由 app 合并的）纯转发。
type CandidateRow struct {
	Kind       string
	ItemID     int64
	SortAt     time.Time
	ReposterID int64
	RepostedAt *time.Time
	PostID     int64
}

// TimelinePostCandidates 组装帖子分支候选：按 (published_at, id) 倒序，可见性过滤在 SQL 内完成。
func (r *Repo) TimelinePostCandidates(ctx context.Context, query TimelineQuery) ([]CandidateRow, error) {
	if !query.IncludePosts {
		return nil, nil
	}
	sql, args := buildPostCandidatesSQL(query)
	rows, err := r.ex(ctx).Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]CandidateRow, 0, query.Limit)
	for rows.Next() {
		var item CandidateRow
		var reposterID *int64
		if err := rows.Scan(&item.Kind, &item.ItemID, &item.SortAt, &reposterID, &item.RepostedAt); err != nil {
			return nil, err
		}
		if reposterID != nil {
			item.ReposterID = *reposterID
		}
		item.PostID = item.ItemID
		items = append(items, item)
	}
	return items, rows.Err()
}

// buildPostCandidatesSQL 是纯 SQL 组装函数（不访问数据库，便于单测断言）。
// 所有过滤条件（可见性、作者/标签/搜索、游标）都必须作为 AND 条件拼进同一个 WHERE，
// 避免出现两个 WHERE 导致 PostgreSQL 语法错误。
func buildPostCandidatesSQL(query TimelineQuery) (string, []any) {
	args := make([]any, 0, 12)
	placeholder := func(value any) string {
		args = append(args, value)
		return "$" + strconv.Itoa(len(args))
	}

	viewerArg := placeholder(query.ViewerID)
	followingArg := placeholder(query.FollowingIDs)
	// AI 访问者即使关注了作者也不能读取他人的仅关注者内容（需求 8.2.11）。
	aiArg := placeholder(query.ViewerIsAI)
	visibility := "(p.visibility = 'public' OR p.author_id = " + viewerArg +
		" OR (p.visibility = 'followers' AND NOT " + aiArg + "::boolean AND p.author_id = ANY(" +
		followingArg + "::bigint[])))"

	conds := []string{"p.status = 'published'", visibility}
	if query.AuthorID > 0 {
		conds = append(conds, "p.author_id = "+placeholder(query.AuthorID))
	}
	if query.FollowingOnly {
		conds = append(conds, "p.author_id = ANY("+followingArg+"::bigint[])")
	}
	if query.QuotedOnly {
		conds = append(conds, "p.quoted_post_id IS NOT NULL")
	}
	if query.Filter == domain.FilterHuman || query.Filter == domain.FilterAI {
		conds = append(conds, "p.author_id = ANY("+placeholder(query.AuthorIDs)+"::bigint[])")
	}
	if query.TagName != "" {
		conds = append(conds, "EXISTS (SELECT 1 FROM post_tags pt JOIN tags tg ON tg.id = pt.tag_id "+
			"WHERE pt.post_id = p.id AND pt.is_active AND tg.name = "+placeholder(query.TagName)+")")
	}
	if query.SearchText != "" {
		// 必须写成 LOWER(plain_text) LIKE '%' || ... || '%' 才能命中 0014 的
		// GIN(LOWER(plain_text) gin_trgm_ops) 表达式索引；STRPOS 不走 trigram 索引。
		conds = append(conds, "EXISTS (SELECT 1 FROM post_search ps WHERE ps.post_id = p.id AND "+
			"LOWER(ps.plain_text) LIKE '%' || LOWER("+placeholder(query.SearchText)+") || '%')")
	}
	if !query.AfterTime.IsZero() && query.AfterID > 0 {
		conds = append(conds, "(p.published_at, p.id) < ("+placeholder(query.AfterTime)+"::timestamptz, "+
			placeholder(query.AfterID)+"::bigint)")
	}
	limitArg := placeholder(query.Limit)
	sql := `SELECT 'post'::text, p.id, p.published_at, NULL::bigint, NULL::timestamptz
		FROM posts p WHERE ` + strings.Join(conds, " AND ") +
		` ORDER BY p.published_at DESC, p.id DESC LIMIT ` + limitArg
	return sql, args
}
