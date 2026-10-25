package repo

import (
	"strings"
	"testing"
)

// normalizeColumns 折叠全部空白后比较两份投影：只关心列集与列序，
// 不关心换行与缩进的排版差异。
func normalizeColumns(columns string) string {
	return strings.Join(strings.Fields(columns), " ")
}

// TestUserColumnsSyncWithSessionJoin 守护同一包内的两份 users 投影保持一致：
// UserColumns（裸列）是权威定义，userColumnsJoined（JOIN 查询带 u. 前缀）
// 必须与其列集和顺序一致，否则 ByTokenHash 的按位 Scan 会错位。
func TestUserColumnsSyncWithSessionJoin(t *testing.T) {
	joined := strings.ReplaceAll(normalizeColumns(userColumnsJoined), "u.", "")
	if normalizeColumns(UserColumns) != joined {
		t.Fatalf("users 裸列投影与 JOIN 投影不一致：\nUserColumns:        %s\nuserColumnsJoined:  %s", UserColumns, userColumnsJoined)
	}
}
