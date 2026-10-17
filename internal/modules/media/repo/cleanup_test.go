package repo

import (
	"strings"
	"testing"
)

// TestCleanupSQLGuards 验证清理 SQL 的引用保护与“只逻辑失效”约束：
// 只清理帖子用途、未被草稿关联、未被头像引用的临时附件，且时间由参数传入。
func TestCleanupSQLGuards(t *testing.T) {
	guards := []string{
		"post_id IS NULL",
		"status = 'available'",
		"purpose = 'post'",
		"post_draft_attachments",
		"da.is_active",
		"a.id <> ALL($3)",
	}
	for _, guard := range guards {
		if !strings.Contains(markExpiredTempDeletedSQL, guard) {
			t.Errorf("清理 SQL 缺少保护条件 %q", guard)
		}
	}
	lower := strings.ToLower(markExpiredTempDeletedSQL)
	if strings.Contains(lower, "delete from") {
		t.Fatal("清理任务不得物理删除记录")
	}
	if !strings.Contains(markExpiredTempDeletedSQL, "SET status = 'deleted'") {
		t.Fatal("清理任务应只做逻辑失效")
	}
	for _, word := range []string{"now()", "current_timestamp", "current_date", "interval"} {
		if strings.Contains(lower, word) {
			t.Fatalf("清理 SQL 含数据库时间函数/运算 %q，应改为应用时间参数", word)
		}
	}
	if !strings.Contains(markExpiredTempDeletedSQL, "created_at < $1") {
		t.Fatal("截止时间必须作为参数传入")
	}
	if !strings.Contains(markExpiredTempDeletedSQL, "updated_at = $2") {
		t.Fatal("updated_at 必须使用应用时间参数")
	}
}
