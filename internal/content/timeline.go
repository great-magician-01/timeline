package content

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/great-magician-01/timeline/internal/access"
	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/auth"
	"github.com/great-magician-01/timeline/internal/model"
)

// ViewerState 是访问者对当前内容的互动状态。
type ViewerState struct {
	Liked    bool `json:"liked"`
	Reposted bool `json:"reposted"`
}

// QuotedPost 是引用转发的原帖占位：原帖不可见或已删除时 Available 为 false 且不带 post。
type QuotedPost struct {
	Available bool       `json:"available"`
	Post      *PostBrief `json:"post,omitempty"`
}

// PostBrief 是时间线、搜索、标签聚合与嵌套引用共用的列表项 DTO。
// 嵌套位置（RepostOf 与 QuotedPost.Post）不得再嵌套：其 RepostOf/QuotedPost 为 nil。
type PostBrief struct {
	ID           string                     `json:"id"`
	Kind         string                     `json:"kind"`
	Author       model.UserBriefResponse    `json:"author"`
	Visibility   string                     `json:"visibility"`
	ContentHTML  string                     `json:"content_html"`
	PublishedAt  *string                    `json:"published_at"`
	RepostedAt   *string                    `json:"reposted_at,omitempty"`
	Edited       bool                       `json:"edited"`
	LikeCount    int                        `json:"like_count"`
	CommentCount int                        `json:"comment_count"`
	RepostCount  int                        `json:"repost_count"`
	RepostOf     *PostBrief                 `json:"repost_of,omitempty"`
	QuotedPost   *QuotedPost                `json:"quoted_post,omitempty"`
	Attachments  []model.AttachmentResponse `json:"attachments"`
	ViewerState  ViewerState                `json:"viewer_state"`
}

// PostDetail 是帖子详情 DTO：在列表项基础上补充作者操作与历史访问信息。
type PostDetail struct {
	PostBrief
	Status            string  `json:"status"`
	ContentFormat     string  `json:"content_format"`
	ScheduledAt       *string `json:"scheduled_at,omitempty"`
	Revision          int     `json:"revision,omitempty"`
	VersionNumber     int     `json:"version_number,omitempty"`
	CanEdit           bool    `json:"can_edit"`
	CanDelete         bool    `json:"can_delete"`
	CanViewHistory    bool    `json:"can_view_history"`
	AuthorFollowState string  `json:"author_follow_state,omitempty"`
}

// CommentTimelineItem 是个人主页“回复”页签的列表项。
type CommentTimelineItem struct {
	Kind        string                  `json:"kind"`
	ID          string                  `json:"id"`
	Author      model.UserBriefResponse `json:"author"`
	ContentHTML string                  `json:"content_html"`
	CreatedAt   string                  `json:"created_at"`
	Post        *PostBrief              `json:"post"`
}

// timelineItem 是时间线候选：帖子或纯转发。
type timelineItem struct {
	kind       string
	itemID     int64
	sortAt     time.Time
	reposterID int64
	repostedAt *time.Time
	postID     int64
	post       model.Post
}

func (it timelineItem) key() (time.Time, int64) { return it.sortAt, it.itemID }

// commentItem 是回复时间线候选。
type commentItem struct {
	id          int64
	authorID    int64
	contentHTML string
	createdAt   time.Time
	postID      int64
	post        model.Post
}

func (it commentItem) key() (time.Time, int64) { return it.createdAt, it.id }

// timelineParams 描述一次候选查询。
type timelineParams struct {
	viewerID       int64
	viewerIsAI     bool
	followingIDs   []int64
	includePosts   bool
	includeReposts bool
	quotedOnly     bool
	authorID       int64
	followingOnly  bool
	filter         string
	tagName        string
	searchText     string
	cursorTime     time.Time
	cursorID       int64
	limit          int
}

// postColumnsP 是带 posts 别名的帖子列，避免手写两份列名。
var postColumnsP = aliasColumns("p.", model.PostColumns)
var versionColumnsV = aliasColumns("v.", model.PostVersionColumns)

// aliasColumns 给逗号分隔的列清单统一加上表别名前缀。
func aliasColumns(alias, columns string) string {
	parts := strings.Split(columns, ",")
	for i, part := range parts {
		parts[i] = alias + strings.TrimSpace(part)
	}
	return strings.Join(parts, ", ")
}

// buildTimelineSQL 组装候选查询：帖子与纯转发的并集，按 (sort_at, item_id) 倒序。
// 可见性过滤完全在 SQL 内完成，避免把非公开内容带出数据库。
func buildTimelineSQL(p *timelineParams) (string, []any) {
	args := make([]any, 0, 12)
	placeholder := func(value any) string {
		args = append(args, value)
		return "$" + strconv.Itoa(len(args))
	}

	viewerArg := placeholder(p.viewerID)
	followingArg := placeholder(p.followingIDs)
	// AI 访问者即使关注了作者也不能读取他人的仅关注者内容（需求 8.2.11）。
	aiArg := placeholder(p.viewerIsAI)
	visibility := "(p.visibility = 'public' OR p.author_id = " + viewerArg +
		" OR (p.visibility = 'followers' AND NOT " + aiArg + "::boolean AND p.author_id = ANY(" +
		followingArg + "::bigint[])))"

	branches := make([]string, 0, 2)
	if p.includePosts {
		conds := []string{"p.status = 'published'", visibility}
		if p.authorID > 0 {
			conds = append(conds, "p.author_id = "+placeholder(p.authorID))
		}
		if p.followingOnly {
			conds = append(conds, "p.author_id = ANY("+followingArg+"::bigint[])")
		}
		if p.quotedOnly {
			conds = append(conds, "p.quoted_post_id IS NOT NULL")
		}
		if p.filter == filterHuman || p.filter == filterAI {
			conds = append(conds, "EXISTS (SELECT 1 FROM users u WHERE u.id = p.author_id AND u.is_ai = "+
				placeholder(p.filter == filterAI)+")")
		}
		if p.tagName != "" {
			conds = append(conds, "EXISTS (SELECT 1 FROM post_tags pt JOIN tags tg ON tg.id = pt.tag_id "+
				"WHERE pt.post_id = p.id AND pt.is_active AND tg.name = "+placeholder(p.tagName)+")")
		}
		if p.searchText != "" {
			conds = append(conds, "EXISTS (SELECT 1 FROM post_search ps WHERE ps.post_id = p.id AND "+
				"STRPOS(LOWER(ps.plain_text), "+placeholder(p.searchText)+") > 0)")
		}
		branches = append(branches, `SELECT 'post'::text AS kind, p.id AS item_id, p.published_at AS sort_at,
				NULL::bigint AS reposter_id, NULL::timestamptz AS reposted_at, p.id AS post_id
			FROM posts p WHERE `+strings.Join(conds, " AND "))
	}
	if p.includeReposts {
		// 纯转发只允许公开原帖；原帖后续改为非公开时不再进入时间线。
		conds := []string{"r.is_active", "p.status = 'published'", "p.visibility = 'public'"}
		if p.authorID > 0 {
			conds = append(conds, "r.user_id = "+placeholder(p.authorID))
		}
		if p.followingOnly {
			conds = append(conds, "r.user_id = ANY("+followingArg+"::bigint[])")
		}
		if p.filter == filterHuman || p.filter == filterAI {
			conds = append(conds, "EXISTS (SELECT 1 FROM users u WHERE u.id = r.user_id AND u.is_ai = "+
				placeholder(p.filter == filterAI)+")")
		}
		branches = append(branches, `SELECT 'repost'::text AS kind, r.id AS item_id, r.created_at AS sort_at,
				r.user_id AS reposter_id, r.created_at AS reposted_at, r.post_id AS post_id
			FROM reposts r JOIN posts p ON p.id = r.post_id WHERE `+strings.Join(conds, " AND "))
	}
	if len(branches) == 0 {
		return "", nil
	}

	where := ""
	if !p.cursorTime.IsZero() && p.cursorID > 0 {
		where = " WHERE (t.sort_at, t.item_id) < (" + placeholder(p.cursorTime) + "::timestamptz, " +
			placeholder(p.cursorID) + "::bigint)"
	}
	limitArg := placeholder(p.limit)
	sql := "SELECT t.kind, t.item_id, t.sort_at, t.reposter_id, t.reposted_at, t.post_id FROM (" +
		strings.Join(branches, " UNION ALL ") + ") t" + where +
		" ORDER BY t.sort_at DESC, t.item_id DESC LIMIT " + limitArg
	return sql, args
}

// fetchTimelineBatch 拉取一批候选并附带帖子行，供可见性复核与后续组装复用。
func (s *Service) fetchTimelineBatch(ctx context.Context, q access.Querier, p timelineParams) ([]timelineItem, error) {
	sql, args := buildTimelineSQL(&p)
	if sql == "" {
		return nil, nil
	}
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]timelineItem, 0, p.limit)
	postIDs := make([]int64, 0, p.limit)
	for rows.Next() {
		var item timelineItem
		var reposterID *int64
		if err := rows.Scan(&item.kind, &item.itemID, &item.sortAt, &reposterID, &item.repostedAt, &item.postID); err != nil {
			return nil, err
		}
		if reposterID != nil {
			item.reposterID = *reposterID
		}
		items = append(items, item)
		postIDs = append(postIDs, item.postID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	posts, err := loadPosts(ctx, q, uniqueIDs(postIDs))
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i].post = posts[items[i].postID]
	}
	return items, nil
}

// collectTimeline 按时间游标取一页候选，并在内存中复核可见性（防御性二次校验）。
func (s *Service) collectTimeline(
	ctx context.Context,
	q access.Querier,
	viewer model.Viewer,
	following map[int64]bool,
	params timelineParams,
	cursorTime time.Time,
	cursorID int64,
	limit int,
) ([]timelineItem, bool, error) {
	params.viewerID = viewer.ID
	params.followingIDs = sortedIDs(following)

	fetch := func(ctx context.Context, afterTime time.Time, afterID int64, batchLimit int) ([]timelineItem, error) {
		batch := params
		batch.cursorTime, batch.cursorID, batch.limit = afterTime, afterID, batchLimit
		return s.fetchTimelineBatch(ctx, q, batch)
	}
	keep := func(item timelineItem) bool {
		if item.post.ID == 0 {
			return false
		}
		if item.kind == kindRepost {
			// 纯转发只能指向公开的已发布原帖。
			return item.post.Status == model.PostStatusPublished &&
				item.post.Visibility == model.VisibilityPublic
		}
		return tagPostFilter(viewer, item.post, following)
	}
	return collectPage(ctx, fetch, timelineItem.key, cursorTime, cursorID, limit, keep)
}

// collectReplies 取一页回复候选，过滤掉所属帖子不可见的记录。
func (s *Service) collectReplies(
	ctx context.Context,
	q access.Querier,
	viewer model.Viewer,
	following map[int64]bool,
	authorID int64,
	cursorTime time.Time,
	cursorID int64,
	limit int,
) ([]commentItem, bool, error) {
	fetch := func(ctx context.Context, afterTime time.Time, afterID int64, batchLimit int) ([]commentItem, error) {
		return s.fetchRepliesBatch(ctx, q, viewer, following, authorID, afterTime, afterID, batchLimit)
	}
	keep := func(item commentItem) bool {
		return item.post.ID != 0 && item.post.Status == model.PostStatusPublished &&
			access.CanViewPostWithFollowing(viewer, item.post, following)
	}
	return collectPage(ctx, fetch, commentItem.key, cursorTime, cursorID, limit, keep)
}

func (s *Service) fetchRepliesBatch(
	ctx context.Context,
	q access.Querier,
	viewer model.Viewer,
	following map[int64]bool,
	authorID int64,
	afterTime time.Time,
	afterID int64,
	limit int,
) ([]commentItem, error) {
	sql := `SELECT c.id, c.author_id, c.content_html, c.created_at, c.post_id
		FROM comments c JOIN posts p ON p.id = c.post_id
		WHERE c.author_id = $4 AND c.status = 'published' AND p.status = 'published'
		  AND (p.visibility = 'public' OR p.author_id = $1
		       OR (p.visibility = 'followers' AND NOT $3::boolean AND p.author_id = ANY($2::bigint[])))`
	args := []any{viewer.ID, sortedIDs(following), viewer.IsAI, authorID}
	if !afterTime.IsZero() && afterID > 0 {
		sql += ` AND (c.created_at, c.id) < ($5::timestamptz, $6::bigint)`
		args = append(args, afterTime, afterID)
	}
	sql += ` ORDER BY c.created_at DESC, c.id DESC LIMIT $` + strconv.Itoa(len(args)+1)
	args = append(args, limit)

	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]commentItem, 0, limit)
	postIDs := make([]int64, 0, limit)
	for rows.Next() {
		var item commentItem
		if err := rows.Scan(&item.id, &item.authorID, &item.contentHTML, &item.createdAt, &item.postID); err != nil {
			return nil, err
		}
		items = append(items, item)
		postIDs = append(postIDs, item.postID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	posts, err := loadPosts(ctx, q, uniqueIDs(postIDs))
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i].post = posts[items[i].postID]
	}
	return items, nil
}

// sortedIDs 把集合转为升序切片，保证 SQL 参数稳定。
func sortedIDs(set map[int64]bool) []int64 {
	out := make([]int64, 0, len(set))
	for id, active := range set {
		if active {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func loadPosts(ctx context.Context, q access.Querier, ids []int64) (map[int64]model.Post, error) {
	result := make(map[int64]model.Post, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := q.Query(ctx, `SELECT `+model.PostColumns+` FROM posts WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		post, err := model.ScanPost(rows)
		if err != nil {
			return nil, err
		}
		result[post.ID] = post
	}
	return result, rows.Err()
}

func loadPost(ctx context.Context, q access.Querier, id int64) (model.Post, error) {
	return model.ScanPost(q.QueryRow(ctx, `SELECT `+model.PostColumns+` FROM posts WHERE id = $1`, id))
}

// loadPostForUpdate 锁定帖子行，保证并发编辑与重复发布不会产生重复版本。
// 调用方应在事务内使用，行锁才有意义。
func loadPostForUpdate(ctx context.Context, q access.Querier, id int64) (model.Post, error) {
	return model.ScanPost(q.QueryRow(ctx, `SELECT `+model.PostColumns+` FROM posts WHERE id = $1 FOR UPDATE`, id))
}

// draftContent 是草稿工作区的最新内容。
type draftContent struct {
	format   string
	html     string
	plain    string
	revision int
}

// briefData 汇总组装 DTO 所需的批量查询结果，避免逐条查询。
type briefData struct {
	posts       map[int64]model.Post
	authors     map[int64]model.UserBrief
	versions    map[int64]model.PostVersion
	drafts      map[int64]draftContent
	attachments map[int64][]model.AttachmentResponse
	quoted      map[int64]*QuotedPost
	liked       map[int64]bool
	reposted    map[int64]bool
}

// loadBriefData 批量载入帖子和被引用原帖、作者摘要、当前版本、附件与访问者互动状态。
// preloaded 允许复用调用方已加载的帖子行，避免重复查询。
func (s *Service) loadBriefData(
	ctx context.Context,
	q access.Querier,
	viewer model.Viewer,
	following map[int64]bool,
	postIDs []int64,
	extraUserIDs []int64,
	preloaded map[int64]model.Post,
) (*briefData, error) {
	data := &briefData{
		posts:       make(map[int64]model.Post),
		authors:     make(map[int64]model.UserBrief),
		versions:    make(map[int64]model.PostVersion),
		drafts:      make(map[int64]draftContent),
		attachments: make(map[int64][]model.AttachmentResponse),
		quoted:      make(map[int64]*QuotedPost),
		liked:       make(map[int64]bool),
		reposted:    make(map[int64]bool),
	}

	wanted := uniqueIDs(postIDs)
	for id, post := range preloaded {
		if _, ok := data.posts[id]; !ok {
			data.posts[id] = post
		}
	}
	missing := make([]int64, 0, len(wanted))
	for _, id := range wanted {
		if _, ok := data.posts[id]; !ok {
			missing = append(missing, id)
		}
	}
	loaded, err := loadPosts(ctx, q, missing)
	if err != nil {
		return nil, err
	}
	for id, post := range loaded {
		data.posts[id] = post
	}

	// 引用转发的原帖只展开一层，避免递归拉取。
	quotedIDs := make([]int64, 0, len(wanted))
	for _, id := range wanted {
		if post, ok := data.posts[id]; ok && post.QuotedPostID != nil {
			quotedIDs = append(quotedIDs, *post.QuotedPostID)
		}
	}
	quotedMissing := make([]int64, 0, len(quotedIDs))
	for _, id := range uniqueIDs(quotedIDs) {
		if _, ok := data.posts[id]; !ok {
			quotedMissing = append(quotedMissing, id)
		}
	}
	quotedPosts, err := loadPosts(ctx, q, quotedMissing)
	if err != nil {
		return nil, err
	}
	for id, post := range quotedPosts {
		data.posts[id] = post
	}

	allIDs := make([]int64, 0, len(data.posts))
	for id := range data.posts {
		allIDs = append(allIDs, id)
	}

	// 作者摘要：展示作者（含转发者）、被引用原帖作者与评论作者。
	userIDs := append([]int64{}, extraUserIDs...)
	for _, id := range allIDs {
		userIDs = append(userIDs, data.posts[id].AuthorID)
	}
	authors, err := access.UserBriefsByID(ctx, q, uniqueIDs(userIDs))
	if err != nil {
		return nil, err
	}
	data.authors = authors

	// 已发布内容的当前版本正文；未发布内容读取草稿工作区。
	unpublished := make([]int64, 0, len(allIDs))
	for _, id := range allIDs {
		if data.posts[id].Status != model.PostStatusPublished {
			unpublished = append(unpublished, id)
		}
	}
	versions, err := loadCurrentVersions(ctx, q, allIDs)
	if err != nil {
		return nil, err
	}
	data.versions = versions
	drafts, err := loadDrafts(ctx, q, unpublished)
	if err != nil {
		return nil, err
	}
	data.drafts = drafts

	// 附件：已发布内容取当前版本快照，未发布内容取草稿工作区。
	versionIDs := make([]int64, 0, len(versions))
	for _, version := range versions {
		versionIDs = append(versionIDs, version.ID)
	}
	byVersion, err := loadVersionAttachments(ctx, q, versionIDs, true)
	if err != nil {
		return nil, err
	}
	byDraft, err := loadDraftAttachments(ctx, q, unpublished)
	if err != nil {
		return nil, err
	}
	attachmentIDs := make([]int64, 0, len(byVersion)+len(byDraft))
	raw := make(map[int64][]model.Attachment, len(allIDs))
	for _, id := range allIDs {
		if version, ok := versions[id]; ok {
			raw[id] = byVersion[version.ID]
			continue
		}
		raw[id] = byDraft[id]
	}
	for _, list := range raw {
		for _, attachment := range list {
			attachmentIDs = append(attachmentIDs, attachment.ID)
		}
	}
	derivatives, err := loadDerivatives(ctx, q, uniqueIDs(attachmentIDs))
	if err != nil {
		return nil, err
	}
	for id, list := range raw {
		responses := make([]model.AttachmentResponse, 0, len(list))
		for _, attachment := range list {
			responses = append(responses, model.AttachmentResponseFor(attachment, derivatives[attachment.ID]))
		}
		data.attachments[id] = responses
	}

	// 访问者互动状态。
	liked, reposted, err := loadViewerStates(ctx, q, viewer.ID, allIDs)
	if err != nil {
		return nil, err
	}
	data.liked = liked
	data.reposted = reposted

	// 引用转发的原帖占位。
	for _, id := range allIDs {
		post, ok := data.posts[id]
		if !ok || post.QuotedPostID == nil {
			continue
		}
		data.quoted[id] = data.buildQuoted(viewer, following, *post.QuotedPostID)
	}
	return data, nil
}

// buildQuoted 组装引用占位：原帖不可见、非公开或已删除时只返回 Available=false。
func (d *briefData) buildQuoted(viewer model.Viewer, following map[int64]bool, quotedID int64) *QuotedPost {
	quoted, ok := d.posts[quotedID]
	if !ok || quoted.Status != model.PostStatusPublished ||
		!access.CanViewPostWithFollowing(viewer, quoted, following) {
		return &QuotedPost{Available: false}
	}
	brief := d.brief(quoted, briefOptions{kind: kindPost, authorID: quoted.AuthorID, nested: true})
	return &QuotedPost{Available: true, Post: &brief}
}

// briefOptions 描述一次 brief 组装的角色差异。
type briefOptions struct {
	kind       string
	authorID   int64
	blankBody  bool
	repostedAt *time.Time
	nested     bool
}

// brief 组装列表项；纯转发外层以转发者作为作者、正文留空，并通过 RepostOf 指向原帖。
func (d *briefData) brief(post model.Post, opts briefOptions) PostBrief {
	kind := opts.kind
	if kind == "" {
		kind = kindPost
	}
	author := model.UserBrief{ID: opts.authorID}
	if found, ok := d.authors[opts.authorID]; ok {
		author = found
	}
	out := PostBrief{
		ID:           api.FormatID(post.ID),
		Kind:         kind,
		Author:       author.Response(),
		Visibility:   post.Visibility,
		LikeCount:    post.LikeCount,
		CommentCount: post.CommentCount,
		RepostCount:  post.RepostCount,
		Attachments:  attachmentResponses(d.attachments[post.ID]),
		ViewerState: ViewerState{
			Liked:    d.liked[post.ID],
			Reposted: d.reposted[post.ID],
		},
	}
	if post.PublishedAt != nil {
		published := api.FormatTime(*post.PublishedAt)
		out.PublishedAt = &published
	}
	if version, ok := d.versions[post.ID]; ok {
		if !opts.blankBody {
			out.ContentHTML = version.ContentHTML
		}
		out.Edited = version.VersionNumber > 1
	} else if draft, ok := d.drafts[post.ID]; ok {
		if !opts.blankBody {
			out.ContentHTML = draft.html
		}
	}
	if opts.repostedAt != nil {
		reposted := api.FormatTime(*opts.repostedAt)
		out.RepostedAt = &reposted
	}
	if !opts.nested {
		out.QuotedPost = d.quoted[post.ID]
	}
	return out
}

func attachmentResponses(list []model.AttachmentResponse) []model.AttachmentResponse {
	if list == nil {
		return []model.AttachmentResponse{}
	}
	return list
}

// assembleTimeline 把候选批量组装成时间线列表项。
func (s *Service) assembleTimeline(
	ctx context.Context,
	q access.Querier,
	viewer model.Viewer,
	following map[int64]bool,
	items []timelineItem,
) ([]PostBrief, error) {
	postIDs := make([]int64, 0, len(items))
	userIDs := make([]int64, 0, len(items))
	preloaded := make(map[int64]model.Post, len(items))
	for _, item := range items {
		postIDs = append(postIDs, item.postID)
		if item.post.ID != 0 {
			preloaded[item.post.ID] = item.post
		}
		if item.kind == kindRepost && item.reposterID > 0 {
			userIDs = append(userIDs, item.reposterID)
		}
	}
	data, err := s.loadBriefData(ctx, q, viewer, following, postIDs, userIDs, preloaded)
	if err != nil {
		return nil, err
	}
	out := make([]PostBrief, 0, len(items))
	for _, item := range items {
		post, ok := data.posts[item.postID]
		if !ok {
			continue
		}
		if item.kind == kindRepost {
			outer := data.brief(post, briefOptions{
				kind: kindRepost, authorID: item.reposterID, blankBody: true, repostedAt: item.repostedAt,
			})
			outer.ID = api.FormatID(item.itemID)
			inner := data.brief(post, briefOptions{kind: kindPost, authorID: post.AuthorID, nested: true})
			outer.RepostOf = &inner
			out = append(out, outer)
			continue
		}
		out = append(out, data.brief(post, briefOptions{kind: kindPost, authorID: post.AuthorID}))
	}
	return out, nil
}

// assembleReplies 把回复候选组装成列表项，并附带所属帖子的列表项。
func (s *Service) assembleReplies(
	ctx context.Context,
	q access.Querier,
	viewer model.Viewer,
	following map[int64]bool,
	items []commentItem,
) ([]CommentTimelineItem, error) {
	postIDs := make([]int64, 0, len(items))
	userIDs := make([]int64, 0, len(items))
	preloaded := make(map[int64]model.Post, len(items))
	for _, item := range items {
		postIDs = append(postIDs, item.postID)
		userIDs = append(userIDs, item.authorID)
		if item.post.ID != 0 {
			preloaded[item.post.ID] = item.post
		}
	}
	data, err := s.loadBriefData(ctx, q, viewer, following, postIDs, userIDs, preloaded)
	if err != nil {
		return nil, err
	}
	out := make([]CommentTimelineItem, 0, len(items))
	for _, item := range items {
		post, ok := data.posts[item.postID]
		if !ok {
			continue
		}
		brief := data.brief(post, briefOptions{kind: kindPost, authorID: post.AuthorID})
		author := model.UserBrief{ID: item.authorID}
		if found, ok := data.authors[item.authorID]; ok {
			author = found
		}
		out = append(out, CommentTimelineItem{
			Kind:        "comment",
			ID:          api.FormatID(item.id),
			Author:      author.Response(),
			ContentHTML: item.contentHTML,
			CreatedAt:   api.FormatTime(item.createdAt),
			Post:        &brief,
		})
	}
	return out, nil
}

// loadCurrentVersions 读取每个帖子的当前发布版本。
func loadCurrentVersions(ctx context.Context, q access.Querier, postIDs []int64) (map[int64]model.PostVersion, error) {
	result := make(map[int64]model.PostVersion, len(postIDs))
	if len(postIDs) == 0 {
		return result, nil
	}
	rows, err := q.Query(ctx, `
		SELECT `+versionColumnsV+`
		FROM post_versions v JOIN posts p ON p.current_version_id = v.id
		WHERE p.id = ANY($1)`, postIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		version, err := model.ScanPostVersion(rows)
		if err != nil {
			return nil, err
		}
		result[version.PostID] = version
	}
	return result, rows.Err()
}

// loadDrafts 读取未发布帖子的草稿工作区。
func loadDrafts(ctx context.Context, q access.Querier, postIDs []int64) (map[int64]draftContent, error) {
	result := make(map[int64]draftContent, len(postIDs))
	if len(postIDs) == 0 {
		return result, nil
	}
	rows, err := q.Query(ctx, `
		SELECT post_id, content_format, content_html, plain_text, draft_revision
		FROM post_drafts WHERE post_id = ANY($1)`, postIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var postID int64
		var draft draftContent
		if err := rows.Scan(&postID, &draft.format, &draft.html, &draft.plain, &draft.revision); err != nil {
			return nil, err
		}
		result[postID] = draft
	}
	return result, rows.Err()
}

// loadVersionAttachments 读取版本附件快照。onlyAvailable 为真时只保留仍然有效的附件。
func loadVersionAttachments(ctx context.Context, q access.Querier, versionIDs []int64, onlyAvailable bool) (map[int64][]model.Attachment, error) {
	result := make(map[int64][]model.Attachment, len(versionIDs))
	if len(versionIDs) == 0 {
		return result, nil
	}
	query := `SELECT va.version_id, va.attachment_id
		FROM post_version_attachments va
		JOIN attachments a ON a.id = va.attachment_id`
	if onlyAvailable {
		query += ` AND a.status = 'available'`
	}
	query += ` WHERE va.version_id = ANY($1) ORDER BY va.version_id, va.sort_order, va.attachment_id`

	links, err := queryAttachmentLinks(ctx, q, query, versionIDs)
	if err != nil {
		return nil, err
	}
	return resolveAttachmentLinks(ctx, q, links)
}

// loadDraftAttachments 读取草稿工作区中生效的附件。
func loadDraftAttachments(ctx context.Context, q access.Querier, postIDs []int64) (map[int64][]model.Attachment, error) {
	result := make(map[int64][]model.Attachment, len(postIDs))
	if len(postIDs) == 0 {
		return result, nil
	}
	links, err := queryAttachmentLinks(ctx, q, `SELECT da.post_id, da.attachment_id
		FROM post_draft_attachments da
		JOIN attachments a ON a.id = da.attachment_id AND a.status = 'available'
		WHERE da.post_id = ANY($1) AND da.is_active
		ORDER BY da.post_id, da.sort_order, da.attachment_id`, postIDs)
	if err != nil {
		return nil, err
	}
	return resolveAttachmentLinks(ctx, q, links)
}

// queryAttachmentLinks 读取 (作用域 ID, 附件 ID) 关联，保持 SQL 的排序。
func queryAttachmentLinks(ctx context.Context, q access.Querier, query string, scopeIDs []int64) (map[int64][]int64, error) {
	result := make(map[int64][]int64, len(scopeIDs))
	rows, err := q.Query(ctx, query, scopeIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var scopeID, attachmentID int64
		if err := rows.Scan(&scopeID, &attachmentID); err != nil {
			return nil, err
		}
		result[scopeID] = append(result[scopeID], attachmentID)
	}
	return result, rows.Err()
}

// resolveAttachmentLinks 批量加载附件行并按原顺序返回。
func resolveAttachmentLinks(ctx context.Context, q access.Querier, links map[int64][]int64) (map[int64][]model.Attachment, error) {
	ids := make([]int64, 0)
	for _, list := range links {
		ids = append(ids, list...)
	}
	found, err := loadAttachments(ctx, q, uniqueIDs(ids))
	if err != nil {
		return nil, err
	}
	result := make(map[int64][]model.Attachment, len(links))
	for scopeID, list := range links {
		attachments := make([]model.Attachment, 0, len(list))
		for _, id := range list {
			if attachment, ok := found[id]; ok {
				attachments = append(attachments, attachment)
			}
		}
		result[scopeID] = attachments
	}
	return result, nil
}

func loadAttachments(ctx context.Context, q access.Querier, ids []int64) (map[int64]model.Attachment, error) {
	result := make(map[int64]model.Attachment, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := q.Query(ctx, `SELECT `+model.AttachmentColumns+` FROM attachments WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		attachment, err := model.ScanAttachment(rows)
		if err != nil {
			return nil, err
		}
		result[attachment.ID] = attachment
	}
	return result, rows.Err()
}

// loadDerivatives 读取附件衍生文件状态，供 DTO 决定缩略图地址。
func loadDerivatives(ctx context.Context, q access.Querier, ids []int64) (map[int64]map[string]string, error) {
	result := make(map[int64]map[string]string, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := q.Query(ctx, `
		SELECT attachment_id, derivative_type, status FROM attachment_derivatives
		WHERE attachment_id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var attachmentID int64
		var derivativeType, status string
		if err := rows.Scan(&attachmentID, &derivativeType, &status); err != nil {
			return nil, err
		}
		if result[attachmentID] == nil {
			result[attachmentID] = map[string]string{}
		}
		result[attachmentID][derivativeType] = status
	}
	return result, rows.Err()
}

// loadViewerStates 批量读取访问者对一批帖子的点赞与转发状态。
func loadViewerStates(ctx context.Context, q access.Querier, userID int64, postIDs []int64) (map[int64]bool, map[int64]bool, error) {
	liked := make(map[int64]bool, len(postIDs))
	reposted := make(map[int64]bool, len(postIDs))
	if userID <= 0 || len(postIDs) == 0 {
		return liked, reposted, nil
	}
	rows, err := q.Query(ctx, `
		SELECT p.id,
			EXISTS (SELECT 1 FROM likes l
				WHERE l.user_id = $1 AND l.target_type = 'post' AND l.target_id = p.id AND l.is_active),
			EXISTS (SELECT 1 FROM reposts r
				WHERE r.user_id = $1 AND r.post_id = p.id AND r.is_active)
		FROM posts p WHERE p.id = ANY($2)`, userID, postIDs)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var postID int64
		var isLiked, isReposted bool
		if err := rows.Scan(&postID, &isLiked, &isReposted); err != nil {
			return nil, nil, err
		}
		liked[postID] = isLiked
		reposted[postID] = isReposted
	}
	return liked, reposted, rows.Err()
}

// timelineList 是时间线类接口的公共流程：取候选、组装、按时间游标返回。
func (s *Service) timelineList(
	c *gin.Context,
	q access.Querier,
	viewer model.Viewer,
	following map[int64]bool,
	params timelineParams,
) {
	limit, err := api.PageLimit(c)
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	cursorTime, cursorID, err := pageQueryCursor(c)
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", "无效的游标")
		return
	}
	ctx := c.Request.Context()
	items, hasMore, err := s.collectTimeline(ctx, q, viewer, following, params, cursorTime, cursorID, limit)
	if err != nil {
		s.internalError(c, "读取时间线失败", err)
		return
	}
	briefs, err := s.assembleTimeline(ctx, q, viewer, following, items)
	if err != nil {
		s.internalError(c, "组装时间线失败", err)
		return
	}
	var lastTime time.Time
	var lastID int64
	if len(items) > 0 {
		lastTime, lastID = items[len(items)-1].key()
	}
	c.JSON(http.StatusOK, timeListResponse(briefs, lastTime, lastID, hasMore))
}

// Timeline 返回全站时间线，按访问者权限过滤。
func (s *Service) Timeline(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	viewer := viewerOf(c)
	filter, err := normalizeFilter(c.Query("filter"))
	if err != nil {
		s.respond(c, "解析时间线筛选失败", err)
		return
	}
	ctx := c.Request.Context()
	following, err := access.FollowingSet(ctx, pool, viewer.ID)
	if err != nil {
		s.internalError(c, "读取关注集合失败", err)
		return
	}
	s.timelineList(c, pool, viewer, following, timelineParams{
		includePosts:   true,
		includeReposts: true,
		filter:         filter,
	})
}

// FollowingTimeline 返回关注时间线：作者必须是我当前生效关注的账号。
func (s *Service) FollowingTimeline(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	viewer := viewerOf(c)
	if !viewer.Authenticated {
		api.Respond(c, api.ErrUnauthenticated("请先登录"))
		return
	}
	if viewer.IsAI {
		api.Respond(c, api.ErrForbidden("AI 账号不能通过此接口操作"))
		return
	}
	filter, err := normalizeFilter(c.Query("filter"))
	if err != nil {
		s.respond(c, "解析时间线筛选失败", err)
		return
	}
	ctx := c.Request.Context()
	following, err := access.FollowingSet(ctx, pool, viewer.ID)
	if err != nil {
		s.internalError(c, "读取关注集合失败", err)
		return
	}
	s.timelineList(c, pool, viewer, following, timelineParams{
		includePosts:   true,
		includeReposts: true,
		followingOnly:  true,
		filter:         filter,
	})
}

// UserTimeline 返回个人主页时间线，支持 posts/replies/reposts 三种页签。
func (s *Service) UserTimeline(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	tab, err := normalizeTab(c.Query("tab"))
	if err != nil {
		s.respond(c, "解析页签失败", err)
		return
	}
	ctx := c.Request.Context()
	author, err := auth.UserByUsername(ctx, pool, strings.TrimSpace(c.Param("username")))
	if errors.Is(err, pgx.ErrNoRows) {
		api.Respond(c, api.ErrNotFound("用户不存在"))
		return
	}
	if err != nil {
		s.internalError(c, "读取主页作者失败", err)
		return
	}
	viewer := viewerOf(c)
	following, err := access.FollowingSet(ctx, pool, viewer.ID)
	if err != nil {
		s.internalError(c, "读取关注集合失败", err)
		return
	}

	if tab == tabReplies {
		s.userTimelineReplies(c, pool, viewer, following, author.ID)
		return
	}
	params := timelineParams{
		includePosts: true,
		authorID:     author.ID,
		filter:       filterAll,
	}
	if tab == tabReposts {
		// 纯转发与引用转发合并按时间倒序展示。
		params.includeReposts = true
		params.quotedOnly = true
	}
	s.timelineList(c, pool, viewer, following, params)
}

func (s *Service) userTimelineReplies(c *gin.Context, q access.Querier, viewer model.Viewer, following map[int64]bool, authorID int64) {
	limit, err := api.PageLimit(c)
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	cursorTime, cursorID, err := pageQueryCursor(c)
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", "无效的游标")
		return
	}
	ctx := c.Request.Context()
	items, hasMore, err := s.collectReplies(ctx, q, viewer, following, authorID, cursorTime, cursorID, limit)
	if err != nil {
		s.internalError(c, "读取回复时间线失败", err)
		return
	}
	replies, err := s.assembleReplies(ctx, q, viewer, following, items)
	if err != nil {
		s.internalError(c, "组装回复时间线失败", err)
		return
	}
	var lastTime time.Time
	var lastID int64
	if len(items) > 0 {
		lastTime, lastID = items[len(items)-1].key()
	}
	c.JSON(http.StatusOK, timeListResponse(replies, lastTime, lastID, hasMore))
}
