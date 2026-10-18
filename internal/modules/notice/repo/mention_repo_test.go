package repo

import (
	"strings"
	"testing"
)

// TestMentionListActiveUsesTimeOrder 锁定生效提及列表查询的形态：
// 排序键必须与新增的 mentions_user_created 部分索引
// (mentioned_user_id, created_at DESC, id DESC) WHERE is_active 一致，
// 让 created_at 时间窗过滤走索引范围扫描，而不是按 id 倒序扫用户全部历史提及后再过滤。
// 本用例只断言 SQL 常量，不连接数据库（测试环境可能无 PostgreSQL）。
func TestMentionListActiveUsesTimeOrder(t *testing.T) {
	wants := []string{
		"mentioned_user_id = $1",
		"is_active",
		"created_at > $2",
		"ORDER BY created_at DESC, id DESC",
		"LIMIT $3",
	}
	for _, want := range wants {
		if !strings.Contains(sqlMentionListActive, want) {
			t.Fatalf("ListActive SQL 缺少 %q：%s", want, sqlMentionListActive)
		}
	}
	if strings.Contains(sqlMentionListActive, "ORDER BY id DESC") {
		t.Fatalf("ListActive 不应再只按 id 排序（无法命中时间窗索引）：%s", sqlMentionListActive)
	}
}
