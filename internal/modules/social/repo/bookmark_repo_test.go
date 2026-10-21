package repo

import (
	"strings"
	"testing"

	"github.com/great-magician-01/timeline/internal/modules/social/domain"
)

// TestSqlBookmarkUpsertMoveSemantics 防回归：收藏 upsert 必须无条件覆盖归属，
// 同时支持「新收藏 / 失效复活 / 移动到指定收藏夹」；且不得出现数据库时间函数。
func TestSqlBookmarkUpsertMoveSemantics(t *testing.T) {
	for _, want := range []string{
		"ON CONFLICT (user_id, post_id)",
		"DO UPDATE SET is_active = TRUE, folder_id = $4, updated_at = $5",
		"RETURNING id",
	} {
		if !strings.Contains(sqlBookmarkUpsert, want) {
			t.Fatalf("sqlBookmarkUpsert 缺少片段 %q:\n%s", want, sqlBookmarkUpsert)
		}
	}
	// 无计数无通知，不需要 like 那样的 WHERE is_active = FALSE 翻转守卫。
	if strings.Contains(sqlBookmarkUpsert, "WHERE likes") || strings.Contains(sqlBookmarkUpsert, "WHERE bookmarks.is_active = FALSE") {
		t.Fatalf("收藏 upsert 应无条件覆盖（幂等 + 移动共用一条路径）:\n%s", sqlBookmarkUpsert)
	}
	if strings.Contains(sqlBookmarkUpsert, "now()") || strings.Contains(sqlBookmarkUpsert, "CURRENT_DATE") {
		t.Fatalf("SQL 不得使用数据库时间函数（时间由应用 Clock 显式传入）:\n%s", sqlBookmarkUpsert)
	}
}

// TestSqlBookmarkDeactivateKeepsFolder 防回归：取消收藏只做逻辑失效，
// 不得清空 folder_id（重新收藏时由 upsert 指定归属，删除夹另有归还路径）。
func TestSqlBookmarkDeactivateKeepsFolder(t *testing.T) {
	if strings.Contains(sqlBookmarkDeactivate, "folder_id") {
		t.Fatalf("取消收藏不得修改 folder_id:\n%s", sqlBookmarkDeactivate)
	}
	for _, want := range []string{"AND is_active", "RETURNING id"} {
		if !strings.Contains(sqlBookmarkDeactivate, want) {
			t.Fatalf("sqlBookmarkDeactivate 缺少片段 %q:\n%s", want, sqlBookmarkDeactivate)
		}
	}
}

// TestBuildBookmarkListSQLScope 表驱动断言夹过滤三态与游标参数顺序。
func TestBuildBookmarkListSQLScope(t *testing.T) {
	cases := []struct {
		name  string
		query domain.BookmarkListQuery
		want  []string
		limit string
		args  int
	}{
		{
			name:  "全部收藏首页",
			query: domain.BookmarkListQuery{UserID: 1, All: true, Limit: 20},
			want:  []string{"user_id = $1", "AND is_active"},
			limit: "LIMIT $2",
			args:  2,
		},
		{
			name:  "未分组过滤",
			query: domain.BookmarkListQuery{UserID: 1, DefaultOnly: true, Limit: 20},
			want:  []string{"user_id = $1", "AND is_active", "AND folder_id IS NULL"},
			limit: "LIMIT $2",
			args:  2,
		},
		{
			name:  "指定收藏夹过滤",
			query: domain.BookmarkListQuery{UserID: 1, FolderID: 42, Limit: 20},
			want:  []string{"user_id = $1", "AND is_active", "AND folder_id = $2"},
			limit: "LIMIT $3",
			args:  3,
		},
		{
			name:  "指定收藏夹 + 游标",
			query: domain.BookmarkListQuery{UserID: 1, FolderID: 42, AfterID: 100, Limit: 20},
			want:  []string{"AND folder_id = $2", "AND id < $3", "ORDER BY id DESC"},
			limit: "LIMIT $4",
			args:  4,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sql, args := buildBookmarkListSQL(tc.query)
			for _, want := range tc.want {
				if !strings.Contains(sql, want) {
					t.Fatalf("SQL 缺少片段 %q:\n%s", want, sql)
				}
			}
			if !strings.Contains(sql, tc.limit) {
				t.Fatalf("SQL 缺少 %q:\n%s", tc.limit, sql)
			}
			if len(args) != tc.args {
				t.Fatalf("参数数量 = %d，期望 %d: %#v", len(args), tc.args, args)
			}
			if !strings.Contains(sql, "ORDER BY id DESC") {
				t.Fatalf("收藏列表必须按行 ID 倒序:\n%s", sql)
			}
		})
	}
}

// TestSqlFolderDeleteReassignsDefault 防回归：删除收藏夹是逻辑删除，
// 且必须与「生效收藏归还默认」配对使用（两条 SQL 由 app 在同一事务内先后执行）。
func TestSqlFolderDeleteReassignsDefault(t *testing.T) {
	for _, want := range []string{"SET deleted_at = $3", "AND user_id = $2", "AND deleted_at IS NULL", "RETURNING id"} {
		if !strings.Contains(sqlFolderDelete, want) {
			t.Fatalf("sqlFolderDelete 缺少片段 %q:\n%s", want, sqlFolderDelete)
		}
	}
	for _, want := range []string{"SET folder_id = NULL", "AND is_active"} {
		if !strings.Contains(sqlFolderReassignToDefault, want) {
			t.Fatalf("sqlFolderReassignToDefault 缺少片段 %q:\n%s", want, sqlFolderReassignToDefault)
		}
	}
}
