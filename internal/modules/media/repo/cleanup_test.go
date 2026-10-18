package repo

import (
	"strings"
	"testing"
)

// TestCleanupCandidateSQLGuards 验证清理候选 SQL 的引用保护与“不跨模块查表”约束：
// 只选出帖子用途、未挂帖子、未过期且未被头像引用的临时附件；草稿引用由 content
// 端口二次过滤，因此 SQL 中不得出现 post_draft_attachments / users 等其它模块的表。
func TestCleanupCandidateSQLGuards(t *testing.T) {
	guards := []string{
		"post_id IS NULL",
		"status = 'available'",
		"purpose = 'post'",
		"created_at < $1",
		"a.id <> ALL($2)",
		"LIMIT $3",
	}
	for _, guard := range guards {
		if !strings.Contains(listExpiredTempAttachmentCandidatesSQL, guard) {
			t.Errorf("清理候选 SQL 缺少保护条件 %q", guard)
		}
	}
	lower := strings.ToLower(listExpiredTempAttachmentCandidatesSQL)
	for _, forbidden := range []string{
		"post_draft_attachments",
		"users",
		"avatar_url",
		"delete from",
	} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("清理候选 SQL 不应包含 %q（草稿引用走 content 端口；头像引用走 avatar_attachment_id 列表）", forbidden)
		}
	}
	for _, word := range []string{"now()", "current_timestamp", "current_date", "interval"} {
		if strings.Contains(lower, word) {
			t.Fatalf("清理 SQL 含数据库时间函数/运算 %q，应改为应用时间参数", word)
		}
	}
}

// TestMarkDeletedByIDsSQLGuards 验证批量失效 SQL 只做逻辑失效、使用应用时间参数。
func TestMarkDeletedByIDsSQLGuards(t *testing.T) {
	lower := strings.ToLower(markDeletedByIDsSQL)
	if strings.Contains(lower, "delete from") {
		t.Fatal("附件清理不得物理删除记录")
	}
	if !strings.Contains(markDeletedByIDsSQL, "SET status = 'deleted'") {
		t.Fatal("附件清理应只做逻辑失效")
	}
	if !strings.Contains(markDeletedByIDsSQL, "updated_at = $2") {
		t.Fatal("updated_at 必须使用应用时间参数")
	}
	if !strings.Contains(markDeletedByIDsSQL, "id = ANY($1)") {
		t.Fatal("批量失效应按 ID 集合更新")
	}
	if strings.Contains(markDeletedByIDsSQL, "created_at <") {
		t.Fatal("按 ID 批量失效不应再包含候选筛选条件（候选已由第一步选出）")
	}
}

// TestCleanupCandidateLimit 验证单次清理上限的兜底值。
func TestCleanupCandidateLimit(t *testing.T) {
	if defaultCleanupCandidateLimit <= 0 {
		t.Fatalf("默认清理候选上限必须为正数，当前 %d", defaultCleanupCandidateLimit)
	}
}
