package repo

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// TimelineQuery 描述一次时间线帖子候选查询（纯转发分支由 social 端口提供）。
type TimelineQuery struct {
	ViewerID     int64
	ViewerIsAI   bool
	FollowingIDs []int64
	// AuthorIsAI 非 nil 时按作者类型过滤（false=人类 / true=AI）；nil 表示不过滤。
	// 由 app 依据 filter 直接传入，SQL 按 posts.author_is_ai 冗余列过滤，
	// 不再在应用层解析全部账号 ID 集合。
	AuthorIsAI *bool

	IncludePosts  bool
	QuotedOnly    bool
	FollowingOnly bool
	AuthorID      int64
	TagID         int64
	SearchText    string
	// ExcludePinned 排除作者已置顶的帖子：个人主页 posts 页签把置顶帖单独展示在
	// 顶部（按 pinned_sort），普通流不再重复出现；其余时间线不过滤置顶。
	ExcludePinned bool

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
	if query.ExcludePinned {
		// 置顶帖在第一页单独展示（pinned_posts），所有页的普通流都排除以避免重复；
		// 条件与游标无关，分页仍然连续。
		conds = append(conds, "p.pinned_at IS NULL")
	}
	if query.FollowingOnly {
		conds = append(conds, "p.author_id = ANY("+followingArg+"::bigint[])")
	}
	if query.QuotedOnly {
		conds = append(conds, "p.quoted_post_id IS NOT NULL")
	}
	if query.AuthorIsAI != nil {
		// 作者类型过滤按 posts.author_is_ai 快照列直接比较（0020 起有配套部分索引）。
		conds = append(conds, "p.author_is_ai = "+placeholder(*query.AuthorIsAI))
	}
	if query.TagID > 0 {
		// 标签已在 app 层用 tags.name 解析为 tag_id，这里只需 post_tags 主键 (post_id, tag_id)
		// 一次探测，不再逐候选行 JOIN tags 解析名称。
		conds = append(conds, "EXISTS (SELECT 1 FROM post_tags pt "+
			"WHERE pt.post_id = p.id AND pt.tag_id = "+placeholder(query.TagID)+" AND pt.is_active)")
	}
	if query.SearchText != "" {
		// 必须写成 LOWER(plain_text) LIKE '%' || ... || '%' 才能命中 0014 的
		// GIN(LOWER(plain_text) gin_trgm_ops) 表达式索引；STRPOS 不走 trigram 索引。
		// 关键词已由 app 用 shared/search.EscapeLikePattern 转义，这里显式 ESCAPE '\'，
		// 保证 %/_/\ 按字面量匹配。
		conds = append(conds, "EXISTS (SELECT 1 FROM post_search ps WHERE ps.post_id = p.id AND "+
			"LOWER(ps.plain_text) LIKE '%' || LOWER("+placeholder(query.SearchText)+") || '%' ESCAPE '\\')")
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
