package repo

import (
	"strings"
	"testing"
)

// TestUserStatsPostCountScope 守护两套用户统计的帖子计数口径（无需数据库）：
// 管理后台用户详情保持「未删除」，公开个人主页只计「已发布」（P1-11 回归用例）。
func TestUserStatsPostCountScope(t *testing.T) {
	admin := strings.ToLower(sqlUserStats)
	profile := strings.ToLower(sqlUserProfileStats)

	if !strings.Contains(admin, "status <> 'deleted'") {
		t.Fatalf("管理后台用户统计的帖子口径应保持「未删除」: %s", admin)
	}
	if !strings.Contains(profile, "status = 'published'") ||
		!strings.Contains(profile, "posts where author_id = $1") {
		t.Fatalf("个人主页统计的帖子口径应只计「已发布」: %s", profile)
	}
	// 除帖子口径外两套查询共用同一段计数 SQL，避免口径漂移。
	if !strings.HasSuffix(admin, strings.ToLower(userStatsRestSelect)) ||
		!strings.HasSuffix(profile, strings.ToLower(userStatsRestSelect)) {
		t.Fatal("两套用户统计应共用相同的评论/关注/点赞/转发计数 SQL")
	}
	if strings.Count(admin, "select count(*)") != 7 || strings.Count(profile, "select count(*)") != 7 {
		t.Fatalf("用户统计应输出 7 项计数（帖子/评论/粉丝/关注/点赞送出/点赞收到/转发）")
	}
}
