package repo

import (
	"strings"
	"testing"
)

// TestBuildPostCandidatesSQLExcludePinned 防回归：个人主页普通流必须排除置顶帖，
// 条件并入同一 WHERE（与游标等条件合并），且不影响其它时间线（默认不过滤）。
func TestBuildPostCandidatesSQLExcludePinned(t *testing.T) {
	sql, _ := buildPostCandidatesSQL(TimelineQuery{
		ViewerID:      7,
		FollowingIDs:  []int64{},
		ViewerIsAI:    false,
		IncludePosts:  true,
		AuthorID:      9,
		ExcludePinned: true,
		Limit:         20,
	})
	if got := strings.Count(sql, " WHERE "); got != 1 {
		t.Fatalf("SQL 必须只有一个 WHERE，实际 %d 个:\n%s", got, sql)
	}
	if !strings.Contains(sql, "p.pinned_at IS NULL") {
		t.Fatalf("排除置顶条件缺失:\n%s", sql)
	}

	// 其它时间线（全站/关注/标签/搜索）不应排除置顶帖。
	sql, _ = buildPostCandidatesSQL(TimelineQuery{
		ViewerID:     7,
		FollowingIDs: []int64{},
		ViewerIsAI:   false,
		IncludePosts: true,
		Limit:        20,
	})
	if strings.Contains(sql, "pinned_at") {
		t.Fatalf("未开启 ExcludePinned 时不应出现置顶条件:\n%s", sql)
	}
}
