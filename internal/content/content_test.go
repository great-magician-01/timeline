package content

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/model"
)

func TestPageCursorRoundTrip(t *testing.T) {
	at := time.Date(2026, 10, 17, 20, 30, 15, 123456789, time.FixedZone("CST", 8*3600))
	raw := encodePageCursor(at, 1234567890123456789)
	if raw == "" {
		t.Fatal("编码游标不应为空")
	}
	if strings.ContainsAny(raw, "+/=") {
		t.Fatalf("游标应为 base64url 无填充，实际为 %q", raw)
	}
	gotTime, gotID, err := decodePageCursor(raw)
	if err != nil {
		t.Fatalf("解析游标失败: %v", err)
	}
	if gotID != 1234567890123456789 {
		t.Fatalf("游标 ID 往返不一致: %d", gotID)
	}
	if !gotTime.Equal(at) {
		t.Fatalf("游标时间往返不一致: %s != %s", gotTime, at)
	}
}

func TestDecodePageCursorInvalid(t *testing.T) {
	if _, id, err := decodePageCursor(""); err != nil || id != 0 {
		t.Fatalf("空游标应表示首页: id=%d err=%v", id, err)
	}
	if _, _, err := decodePageCursor("!!!not-base64!!!"); err == nil {
		t.Fatal("非法 base64 游标应报错")
	}
	unknownField := base64.RawURLEncoding.EncodeToString([]byte(`{"t":"2026-10-17T20:30:00Z","i":1,"x":2}`))
	if _, _, err := decodePageCursor(unknownField); err == nil {
		t.Fatal("包含未知字段的游标应报错")
	}
	badID := base64.RawURLEncoding.EncodeToString([]byte(`{"t":"2026-10-17T20:30:00Z","i":0}`))
	if _, _, err := decodePageCursor(badID); err == nil {
		t.Fatal("ID 非法的游标应报错")
	}
	badTime := base64.RawURLEncoding.EncodeToString([]byte(`{"i":10}`))
	if _, _, err := decodePageCursor(badTime); err == nil {
		t.Fatal("缺少时间的游标应报错")
	}
}

func TestNormalizeParams(t *testing.T) {
	visibilityCases := map[string]string{
		"":          model.VisibilityPublic,
		"PUBLIC":    model.VisibilityPublic,
		"followers": model.VisibilityFollowers,
		" private ": model.VisibilityPrivate,
	}
	for input, want := range visibilityCases {
		got, err := normalizeVisibility(input)
		if err != nil || got != want {
			t.Fatalf("normalizeVisibility(%q) = %q, %v；期望 %q", input, got, err, want)
		}
	}
	if _, err := normalizeVisibility("friends"); err == nil {
		t.Fatal("非法可见性应被拒绝")
	}

	for _, action := range []string{actionDraft, actionPublish, actionSchedule} {
		if got, err := normalizeAction(action); err != nil || got != action {
			t.Fatalf("normalizeAction(%q) 失败: %q %v", action, got, err)
		}
	}
	if _, err := normalizeAction(""); err == nil {
		t.Fatal("空 action 应被拒绝")
	}
	if _, err := normalizeAction("archive"); err == nil {
		t.Fatal("非法 action 应被拒绝")
	}

	if got, err := normalizeFilter(""); err != nil || got != filterAll {
		t.Fatalf("缺省 filter 应为 all: %q %v", got, err)
	}
	if got, err := normalizeFilter("AI"); err != nil || got != filterAI {
		t.Fatalf("filter=AI 应解析为 ai: %q %v", got, err)
	}
	if _, err := normalizeFilter("bot"); err == nil {
		t.Fatal("非法 filter 应被拒绝")
	}

	if got, err := normalizeTab(""); err != nil || got != tabPosts {
		t.Fatalf("缺省 tab 应为 posts: %q %v", got, err)
	}
	if got, err := normalizeTab("REPLIES"); err != nil || got != tabReplies {
		t.Fatalf("tab=REPLIES 应解析为 replies: %q %v", got, err)
	}
	if _, err := normalizeTab("likes"); err == nil {
		t.Fatal("非法 tab 应被拒绝")
	}

	if got, err := normalizeFormat("Markdown"); err != nil || got != "markdown" {
		t.Fatalf("内容格式应允许 markdown: %q %v", got, err)
	}
	if _, err := normalizeFormat("html"); err == nil {
		t.Fatal("非法内容格式应被拒绝")
	}
}

func TestParseScheduledAt(t *testing.T) {
	now := time.Date(2026, 10, 17, 20, 0, 0, 0, time.FixedZone("CST", 8*3600))
	if _, err := parseScheduledAt("", now); err == nil {
		t.Fatal("空时间应被拒绝")
	}
	if _, err := parseScheduledAt("2026-10-17 21:00:00", now); err == nil {
		t.Fatal("非 RFC3339 时间应被拒绝")
	}
	if _, err := parseScheduledAt(now.Add(-time.Hour).Format(time.RFC3339), now); err == nil {
		t.Fatal("过去时间应被拒绝")
	}
	if _, err := parseScheduledAt(now.Add(30*time.Second).Format(time.RFC3339), now); err == nil {
		t.Fatal("不足 1 分钟的时间应被拒绝")
	}
	at, err := parseScheduledAt(now.Add(minScheduleLead).Format(time.RFC3339), now)
	if err != nil {
		t.Fatalf("恰好 1 分钟后应被接受: %v", err)
	}
	if !at.Equal(now.Add(time.Minute)) {
		t.Fatalf("解析结果不正确: %s", at)
	}
}

func TestParseAttachmentAndQuotedIDs(t *testing.T) {
	ids, err := parseAttachmentIDs([]string{" 1 ", "2", "1"})
	if err != nil || len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
		t.Fatalf("附件 ID 解析与去重失败: %v %v", ids, err)
	}
	if _, err := parseAttachmentIDs([]string{"abc"}); err == nil {
		t.Fatal("非法附件 ID 应被拒绝")
	}
	quoted, err := parseQuotedPostID(nil)
	if err != nil || quoted != nil {
		t.Fatalf("未填写引用时应返回 nil: %v %v", quoted, err)
	}
	empty := "  "
	if quoted, err := parseQuotedPostID(&empty); err != nil || quoted != nil {
		t.Fatalf("空白引用应视为未填写: %v %v", quoted, err)
	}
	value := "42"
	if quoted, err := parseQuotedPostID(&value); err != nil || quoted == nil || *quoted != 42 {
		t.Fatalf("引用 ID 解析失败: %v %v", quoted, err)
	}
}

func TestSummaryAndSearchKeyword(t *testing.T) {
	if got := summary("  你好 世界  "); got != "你好 世界" {
		t.Fatalf("摘要应去除首尾空白: %q", got)
	}
	long := strings.Repeat("很", summaryRunes+10)
	got := summary(long)
	if runes := []rune(got); len(runes) != summaryRunes {
		t.Fatalf("摘要应按字符截断到 %d，实际 %d", summaryRunes, len(runes))
	}
	if _, err := normalizeSearchKeyword("   "); err == nil {
		t.Fatal("空关键词应被拒绝")
	}
	if _, err := normalizeSearchKeyword(strings.Repeat("a", maxSearchRunes+1)); err == nil {
		t.Fatal("过长关键词应被拒绝")
	}
	if got, err := normalizeSearchKeyword(" Go 语言 "); err != nil || got != "Go 语言" {
		t.Fatalf("关键词应去除首尾空白: %q %v", got, err)
	}
}

func TestSortedIDs(t *testing.T) {
	got := sortedIDs(map[int64]bool{5: true, 1: true, 3: false, 2: true})
	want := []int64{1, 2, 5}
	if len(got) != len(want) {
		t.Fatalf("关注集合转换结果长度错误: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("关注集合应升序且忽略失效项: %v", got)
		}
	}
}

func TestCollectPageBatchesAndHasMore(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	// 第 1 批全是不可见项，第 2 批才有可见项：应继续补取。
	visible := map[int]bool{5: true, 6: true}
	calls := 0
	fetch := func(_ context.Context, after time.Time, afterID int64, limit int) ([]int, error) {
		calls++
		start := 1
		if afterID > 0 {
			start = int(afterID) + 1
		}
		items := make([]int, 0, limit)
		for i := start; i < start+limit; i++ {
			items = append(items, i)
		}
		return items, nil
	}
	key := func(id int) (time.Time, int64) { return base.Add(-time.Duration(id) * time.Second), int64(id) }
	keep := func(id int) bool { return visible[id] }

	items, hasMore, err := collectPage(ctx, fetch, key, time.Time{}, 0, 2, keep)
	if err != nil {
		t.Fatalf("collectPage 失败: %v", err)
	}
	if len(items) != 2 || items[0] != 5 || items[1] != 6 {
		t.Fatalf("应跨批补足并过滤出可见项: %v", items)
	}
	if !hasMore {
		t.Fatal("仍有候选项时 has_more 应为真")
	}
	if calls < 2 {
		t.Fatalf("过滤后为空时应继续分批取，实际只取 %d 批", calls)
	}
}

func TestCollectPageStopsWhenExhausted(t *testing.T) {
	ctx := context.Background()
	base := time.Now()
	fetch := func(_ context.Context, _ time.Time, afterID int64, limit int) ([]int, error) {
		if afterID > 0 {
			return nil, nil
		}
		return []int{1}, nil
	}
	key := func(id int) (time.Time, int64) { return base, int64(id) }
	items, hasMore, err := collectPage(ctx, fetch, key, time.Time{}, 0, 5, nil)
	if err != nil {
		t.Fatalf("collectPage 失败: %v", err)
	}
	if len(items) != 1 || hasMore {
		t.Fatalf("取尽后 has_more 应为假: %v %v", items, hasMore)
	}
}

func TestCollectPageFullPageIsLastPage(t *testing.T) {
	ctx := context.Background()
	base := time.Now()
	// 候选正好等于 limit：多取一条时取不到，说明已到末页。
	fetch := func(_ context.Context, _ time.Time, afterID int64, limit int) ([]int, error) {
		if afterID > 0 {
			return nil, nil
		}
		return []int{1, 2, 3}, nil
	}
	key := func(id int) (time.Time, int64) { return base, int64(id) }
	items, hasMore, err := collectPage(ctx, fetch, key, time.Time{}, 0, 3, nil)
	if err != nil {
		t.Fatalf("collectPage 失败: %v", err)
	}
	if len(items) != 3 || hasMore {
		t.Fatalf("正好取满且无更多候选时 has_more 应为假: %v %v", items, hasMore)
	}
}

func TestCollectPageBoundedRounds(t *testing.T) {
	ctx := context.Background()
	base := time.Now()
	calls := 0
	fetch := func(_ context.Context, _ time.Time, afterID int64, limit int) ([]int, error) {
		calls++
		items := make([]int, 0, limit)
		for i := 0; i < limit; i++ {
			items = append(items, int(afterID)+i+1)
		}
		return items, nil
	}
	key := func(id int) (time.Time, int64) { return base, int64(id) }
	items, hasMore, err := collectPage(ctx, fetch, key, time.Time{}, 0, 3, func(int) bool { return false })
	if err != nil {
		t.Fatalf("collectPage 失败: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("全部被过滤时不应返回条目: %v", items)
	}
	if calls != maxPageBatches {
		t.Fatalf("分批次数应被限制在 %d，实际 %d", maxPageBatches, calls)
	}
	if !hasMore {
		t.Fatal("取满上限仍有候选时应保持 has_more 为真")
	}
}

func TestBuildTimelineSQL(t *testing.T) {
	cursorTime := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	sql, args := buildTimelineSQL(&timelineParams{
		viewerID:       42,
		followingIDs:   []int64{7, 9},
		includePosts:   true,
		includeReposts: true,
		filter:         filterAI,
		cursorTime:     cursorTime,
		cursorID:       100,
		limit:          20,
	})
	for _, fragment := range []string{
		"UNION ALL",
		"p.status = 'published'",
		"p.visibility = 'public'",
		"p.visibility = 'followers' AND NOT $3::boolean AND p.author_id = ANY($2::bigint[])",
		"r.is_active",
		"p.visibility = 'public' AND", // 转发分支要求原帖公开
		"(t.sort_at, t.item_id) < ($",
		"ORDER BY t.sort_at DESC, t.item_id DESC",
		"u.is_ai =",
	} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("时间线 SQL 缺少片段 %q：\n%s", fragment, sql)
		}
	}
	// 访问者、关注集合、访问者是否 AI、两个分支各自的作者类型过滤、游标时间、游标 ID、limit。
	if len(args) != 8 {
		t.Fatalf("参数数量应为 8，实际 %d：%v", len(args), args)
	}
	if args[0].(int64) != 42 {
		t.Fatalf("第一个参数应为访问者 ID：%v", args[0])
	}
	if got := args[1].([]int64); len(got) != 2 || got[0] != 7 {
		t.Fatalf("第二个参数应为关注集合：%v", args[1])
	}
	if args[2].(bool) {
		t.Fatalf("人类访问者的 AI 标记应为假：%v", args[2])
	}
	if args[3].(bool) != true || args[4].(bool) != true {
		t.Fatalf("filter=ai 时两个分支都应过滤 AI 作者：%v", args[3:5])
	}
	if args[7].(int) != 20 {
		t.Fatalf("最后一个参数应为 limit：%v", args[7])
	}
}

func TestBuildTimelineSQLFilters(t *testing.T) {
	tagSQL, _ := buildTimelineSQL(&timelineParams{
		includePosts: true, tagName: "go语言", filter: filterAll, limit: 10,
	})
	if !strings.Contains(tagSQL, "post_tags") || !strings.Contains(tagSQL, "pt.is_active") {
		t.Fatalf("标签查询应只关联生效标签：\n%s", tagSQL)
	}
	if strings.Contains(tagSQL, "UNION ALL") {
		t.Fatalf("标签聚合不应包含纯转发：\n%s", tagSQL)
	}
	searchSQL, _ := buildTimelineSQL(&timelineParams{
		includePosts: true, searchText: "go", filter: filterAll, limit: 10,
	})
	if !strings.Contains(searchSQL, "post_search") || !strings.Contains(searchSQL, "STRPOS(LOWER(ps.plain_text)") {
		t.Fatalf("搜索应基于 post_search 纯文本：\n%s", searchSQL)
	}
	quoteSQL, _ := buildTimelineSQL(&timelineParams{
		includePosts: true, includeReposts: true, quotedOnly: true, authorID: 5, limit: 10,
	})
	if !strings.Contains(quoteSQL, "p.quoted_post_id IS NOT NULL") {
		t.Fatalf("引用转发页签应只取带引用的帖子：\n%s", quoteSQL)
	}
	if strings.Contains(quoteSQL, "u.is_ai") {
		t.Fatalf("filter=all 不应附加作者类型过滤：\n%s", quoteSQL)
	}
	if sql, _ := buildTimelineSQL(&timelineParams{limit: 10}); sql != "" {
		t.Fatalf("未选择任何数据源时应返回空 SQL：%s", sql)
	}
}

// newBriefData 构造不访问数据库的 DTO 组装输入。
func newBriefData() *briefData {
	return &briefData{
		posts:       map[int64]model.Post{},
		authors:     map[int64]model.UserBrief{},
		versions:    map[int64]model.PostVersion{},
		drafts:      map[int64]draftContent{},
		attachments: map[int64][]model.AttachmentResponse{},
		quoted:      map[int64]*QuotedPost{},
		liked:       map[int64]bool{},
		reposted:    map[int64]bool{},
	}
}

func publishedPost(id, authorID int64) model.Post {
	published := time.Date(2026, 10, 16, 10, 0, 0, 0, time.FixedZone("CST", 8*3600))
	return model.Post{
		ID:           id,
		AuthorID:     authorID,
		Status:       model.PostStatusPublished,
		Visibility:   model.VisibilityPublic,
		PublishedAt:  &published,
		LikeCount:    3,
		CommentCount: 2,
		RepostCount:  1,
		CreatedAt:    published,
		UpdatedAt:    published,
	}
}

func TestBriefForPost(t *testing.T) {
	data := newBriefData()
	post := publishedPost(100, 7)
	data.authors[7] = model.UserBrief{ID: 7, Username: "alice", Nickname: "Alice"}
	data.versions[100] = model.PostVersion{ID: 900, PostID: 100, VersionNumber: 2, ContentHTML: "<p>正文</p>"}
	data.attachments[100] = []model.AttachmentResponse{{ID: "1", Kind: "image", Status: "available"}}
	data.liked[100] = true

	brief := data.brief(post, briefOptions{kind: kindPost, authorID: 7})
	if brief.ID != "100" || brief.Kind != kindPost {
		t.Fatalf("帖子列表项标识错误: %+v", brief)
	}
	if brief.Author.Username != "alice" || brief.Author.ID != "7" {
		t.Fatalf("作者摘要错误: %+v", brief.Author)
	}
	if brief.ContentHTML != "<p>正文</p>" || !brief.Edited {
		t.Fatalf("正文或已编辑标记错误: %q %v", brief.ContentHTML, brief.Edited)
	}
	if brief.PublishedAt == nil || brief.RepostedAt != nil {
		t.Fatalf("发布时间/转发时间错误: %v %v", brief.PublishedAt, brief.RepostedAt)
	}
	if len(brief.Attachments) != 1 || brief.Attachments[0].ID != "1" {
		t.Fatalf("附件摘要错误: %+v", brief.Attachments)
	}
	if !brief.ViewerState.Liked || brief.ViewerState.Reposted {
		t.Fatalf("访问者状态错误: %+v", brief.ViewerState)
	}
	if brief.RepostOf != nil || brief.QuotedPost != nil {
		t.Fatalf("普通帖子不应带转发或引用嵌套: %+v", brief)
	}
}

func TestBriefForRepost(t *testing.T) {
	data := newBriefData()
	post := publishedPost(100, 7)
	data.authors[7] = model.UserBrief{ID: 7, Username: "alice"}
	data.authors[9] = model.UserBrief{ID: 9, Username: "bob"}
	data.versions[100] = model.PostVersion{ID: 900, PostID: 100, VersionNumber: 3, ContentHTML: "<p>原帖</p>"}
	repostedAt := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)

	outer := data.brief(post, briefOptions{
		kind: kindRepost, authorID: 9, blankBody: true, repostedAt: &repostedAt,
	})
	outer.ID = api.FormatID(555)
	inner := data.brief(post, briefOptions{kind: kindPost, authorID: post.AuthorID, nested: true})
	outer.RepostOf = &inner

	if outer.Kind != kindRepost || outer.ID != "555" {
		t.Fatalf("纯转发外层应为转发条目: %+v", outer)
	}
	if outer.Author.Username != "bob" {
		t.Fatalf("纯转发外层作者应为转发者: %+v", outer.Author)
	}
	if outer.ContentHTML != "" {
		t.Fatalf("纯转发外层正文应为空: %q", outer.ContentHTML)
	}
	if outer.RepostedAt == nil || *outer.RepostedAt != api.FormatTime(repostedAt) {
		t.Fatalf("纯转发时间错误: %v", outer.RepostedAt)
	}
	if outer.LikeCount != post.LikeCount || outer.CommentCount != post.CommentCount || outer.RepostCount != post.RepostCount {
		t.Fatalf("纯转发外层应沿用原帖计数: %+v", outer)
	}
	if outer.RepostOf == nil || outer.RepostOf.Kind != kindPost || outer.RepostOf.ContentHTML != "<p>原帖</p>" {
		t.Fatalf("纯转发应指向原帖列表项: %+v", outer.RepostOf)
	}
	if outer.RepostOf.RepostOf != nil || outer.RepostOf.QuotedPost != nil {
		t.Fatalf("嵌套列表项不得再次嵌套: %+v", outer.RepostOf)
	}
	if outer.RepostOf.ID != "100" || outer.RepostOf.Kind != kindPost {
		t.Fatalf("原帖标识错误: %+v", outer.RepostOf)
	}
}

func TestQuotedPostPlaceholder(t *testing.T) {
	data := newBriefData()
	quoted := publishedPost(200, 8)
	data.authors[8] = model.UserBrief{ID: 8, Username: "carol"}
	data.versions[200] = model.PostVersion{ID: 901, PostID: 200, VersionNumber: 1, ContentHTML: "<p>被引用</p>"}

	guest := model.GuestViewer()
	data.posts[200] = quoted
	available := data.buildQuoted(guest, map[int64]bool{}, 200)
	if !available.Available || available.Post == nil || available.Post.ContentHTML != "<p>被引用</p>" {
		t.Fatalf("公开原帖应可引用展示: %+v", available)
	}

	// 原帖改为非公开后，游客只能看到占位。
	private := quoted
	private.Visibility = model.VisibilityPrivate
	data.posts[200] = private
	placeholder := data.buildQuoted(guest, map[int64]bool{}, 200)
	if placeholder.Available || placeholder.Post != nil {
		t.Fatalf("不可见原帖应返回占位: %+v", placeholder)
	}

	// 已删除原帖同样只返回占位。
	deleted := quoted
	deleted.Status = model.PostStatusDeleted
	deletedAt := time.Now()
	deleted.DeletedAt = &deletedAt
	data.posts[200] = deleted
	if got := data.buildQuoted(guest, map[int64]bool{}, 200); got.Available {
		t.Fatalf("已删除原帖应返回占位: %+v", got)
	}

	// 不存在的原帖。
	if got := data.buildQuoted(guest, map[int64]bool{}, 999); got.Available {
		t.Fatalf("不存在的原帖应返回占位: %+v", got)
	}

	// 关注者可见的非公开原帖对关注者仍然可用。
	followers := quoted
	followers.Visibility = model.VisibilityFollowers
	data.posts[200] = followers
	follower := model.Viewer{ID: 5, Authenticated: true}
	if got := data.buildQuoted(follower, map[int64]bool{8: true}, 200); !got.Available {
		t.Fatalf("关注者应能看到关注者可见的原帖: %+v", got)
	}
}

func TestBriefForUnpublishedUsesDraftWorkspace(t *testing.T) {
	data := newBriefData()
	post := model.Post{
		ID:         300,
		AuthorID:   7,
		Status:     model.PostStatusScheduled,
		Visibility: model.VisibilityPrivate,
	}
	scheduled := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	post.ScheduledAt = &scheduled
	data.authors[7] = model.UserBrief{ID: 7, Username: "alice"}
	data.drafts[300] = draftContent{format: "markdown", html: "<p>草稿</p>", plain: "草稿", revision: 4}
	data.attachments[300] = []model.AttachmentResponse{}

	brief := data.brief(post, briefOptions{kind: kindPost, authorID: 7})
	if brief.ContentHTML != "<p>草稿</p>" {
		t.Fatalf("未发布内容应使用草稿工作区正文: %q", brief.ContentHTML)
	}
	if brief.PublishedAt != nil || brief.Edited {
		t.Fatalf("未发布内容不应有发布时间或已编辑标记: %v %v", brief.PublishedAt, brief.Edited)
	}
	if brief.Attachments == nil || len(brief.Attachments) != 0 {
		t.Fatalf("附件列表应为空数组而不是 null: %+v", brief.Attachments)
	}
}

func TestVersionDetailDTO(t *testing.T) {
	createdAt := time.Date(2026, 10, 17, 8, 0, 0, 0, time.UTC)
	restoredFrom := int64(11)
	version := model.PostVersion{
		ID:                    12,
		PostID:                300,
		VersionNumber:         3,
		ContentFormat:         "markdown",
		ContentSource:         "# 标题",
		ContentHTML:           "<h1>标题</h1>",
		Visibility:            model.VisibilityPublic,
		RestoredFromVersionID: &restoredFrom,
		CreatedBy:             7,
		CreatedAt:             createdAt,
	}
	detail := versionDetail(version, nil)
	if detail.ID != "12" || detail.VersionNumber != 3 || detail.CreatedBy != "7" {
		t.Fatalf("版本详情标识错误: %+v", detail)
	}
	if detail.RestoredFromVersionID == nil || *detail.RestoredFromVersionID != "11" {
		t.Fatalf("恢复来源错误: %v", detail.RestoredFromVersionID)
	}
	if detail.Attachments == nil {
		t.Fatal("附件列表应为空数组而不是 null")
	}
	if detail.CreatedAt != api.FormatTime(createdAt) {
		t.Fatalf("创建时间格式错误: %q", detail.CreatedAt)
	}
}

func TestTimeListResponseHidesCursorWithoutItems(t *testing.T) {
	empty := timeListResponse([]PostBrief{}, time.Time{}, 0, true)
	if empty["has_more"] != false || empty["next_cursor"] != nil {
		t.Fatalf("空列表不应返回游标: %+v", empty)
	}
	at := time.Now()
	page := timeListResponse([]PostBrief{{ID: "1"}}, at, 1, true)
	cursor, ok := page["next_cursor"].(string)
	if !ok || cursor == "" {
		t.Fatalf("有下一页时应返回游标: %+v", page)
	}
	if gotTime, gotID, err := decodePageCursor(cursor); err != nil || gotID != 1 || !gotTime.Equal(at) {
		t.Fatalf("游标不可解析: %v %v %v", gotTime, gotID, err)
	}
}

func TestPublishJobKey(t *testing.T) {
	at := time.Unix(1799999999, 0)
	got := publishJobKey(42, at)
	want := "post_publish:42:1799999999"
	if got != want {
		t.Fatalf("定时发布幂等键错误: %q", got)
	}
}

func TestAliasColumns(t *testing.T) {
	got := aliasColumns("p.", model.PostColumns)
	if !strings.HasPrefix(got, "p.id, p.author_id") {
		t.Fatalf("列别名拼接错误: %q", got)
	}
	if strings.Contains(got, "\n") || strings.Contains(got, "  ") {
		t.Fatalf("列别名应去除多余空白: %q", got)
	}
}

func TestTagPostFilter(t *testing.T) {
	post := publishedPost(1, 7)
	guest := model.GuestViewer()
	if !tagPostFilter(guest, post, nil) {
		t.Fatal("公开已发布帖子应通过筛选")
	}
	private := post
	private.Visibility = model.VisibilityPrivate
	if tagPostFilter(guest, private, nil) {
		t.Fatal("游客不应看到非公开帖子")
	}
	viewer := model.Viewer{ID: 7, Authenticated: true}
	if !tagPostFilter(viewer, private, nil) {
		t.Fatal("作者应看到自己的非公开帖子")
	}
	draft := post
	draft.Status = model.PostStatusDraft
	if tagPostFilter(viewer, draft, nil) {
		t.Fatal("草稿不应进入标签聚合")
	}
}

func TestCreatePostForActorRejectsMissingAuthor(t *testing.T) {
	// 作者缺失时应在访问数据库之前直接拒绝。
	service := NewService(nil, nil, nil, nil)
	if _, err := service.CreatePostForActor(context.Background(), nil, ActorPostInput{}); err == nil {
		t.Fatal("缺少作者账号应被拒绝")
	}
	if _, err := service.CreatePostForActor(context.Background(), nil, ActorPostInput{
		AuthorID: -1, ContentFormat: "markdown", ContentSource: "正文",
	}); err == nil {
		t.Fatal("非法作者账号应被拒绝")
	}
}

func TestCollectPagePropagatesError(t *testing.T) {
	want := errors.New("查询失败")
	fetch := func(context.Context, time.Time, int64, int) ([]int, error) { return nil, want }
	key := func(int) (time.Time, int64) { return time.Time{}, 0 }
	if _, _, err := collectPage(context.Background(), fetch, key, time.Time{}, 0, 10, nil); !errors.Is(err, want) {
		t.Fatalf("应原样返回查询错误，实际 %v", err)
	}
}

func TestPublishStateFor(t *testing.T) {
	cases := []struct {
		name         string
		postStatus   string
		jobStatus    string
		jobError     string
		wantStatus   string
		wantFailed   bool
		wantErrorMsg string
	}{
		{name: "草稿没有作业", postStatus: model.PostStatusDraft, wantStatus: model.PostStatusDraft},
		{name: "定时等待中", postStatus: model.PostStatusScheduled, jobStatus: "pending", wantStatus: model.PostStatusScheduled},
		{name: "定时执行中", postStatus: model.PostStatusScheduled, jobStatus: "running", wantStatus: model.PostStatusScheduled},
		{
			name: "发布失败", postStatus: model.PostStatusScheduled, jobStatus: "failed", jobError: "模型调用失败",
			wantStatus: "failed", wantFailed: true, wantErrorMsg: "模型调用失败",
		},
		{
			name: "发布暂停", postStatus: model.PostStatusScheduled, jobStatus: "paused", jobError: "作者账号已停用",
			wantStatus: "paused", wantFailed: false, wantErrorMsg: "",
		},
		{name: "无最新作业时沿用帖子状态", postStatus: model.PostStatusScheduled, jobStatus: "", wantStatus: model.PostStatusScheduled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, failed, message := publishStateFor(tc.postStatus, tc.jobStatus, tc.jobError)
			if status != tc.wantStatus || failed != tc.wantFailed || message != tc.wantErrorMsg {
				t.Fatalf("publishStateFor(%q,%q,%q) = (%q,%v,%q)，期望 (%q,%v,%q)",
					tc.postStatus, tc.jobStatus, tc.jobError,
					status, failed, message, tc.wantStatus, tc.wantFailed, tc.wantErrorMsg)
			}
		})
	}
}

func TestValidateFutureScheduledAt(t *testing.T) {
	now := time.Date(2026, 10, 17, 20, 0, 0, 0, time.FixedZone("CST", 8*3600))
	if err := validateFutureScheduledAt(now.Add(time.Second), now); err != nil {
		t.Fatalf("未来时间应通过: %v", err)
	}
	if err := validateFutureScheduledAt(now, now); err == nil {
		t.Fatal("当前时间应被拒绝")
	}
	if err := validateFutureScheduledAt(now.Add(-time.Minute), now); err == nil {
		t.Fatal("过去时间应被拒绝")
	}
}

func TestValidateScheduledAt(t *testing.T) {
	now := time.Date(2026, 10, 17, 20, 0, 0, 0, time.FixedZone("CST", 8*3600))
	if err := validateScheduledAt(now.Add(2*time.Minute), now); err != nil {
		t.Fatalf("晚于当前时间 2 分钟应通过: %v", err)
	}
	if err := validateScheduledAt(now.Add(time.Minute), now); err != nil {
		t.Fatalf("恰好晚于 1 分钟应通过: %v", err)
	}
	if err := validateScheduledAt(now.Add(30*time.Second), now); err == nil {
		t.Fatal("不足 1 分钟应被拒绝")
	}
	if err := validateScheduledAt(now.Add(-time.Hour), now); err == nil {
		t.Fatal("过去时间应被拒绝")
	}
}
