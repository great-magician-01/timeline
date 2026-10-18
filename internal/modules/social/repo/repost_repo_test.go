package repo

import (
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/social/domain"
)

// TestSqlRepostActivateRefreshesCreatedAt 防回归：逻辑失效后再次转发必须刷新 created_at，
// 否则时间线仍按首次转发时间排序/展示（本次行为修复的核心缺陷之一）。
func TestSqlRepostActivateRefreshesCreatedAt(t *testing.T) {
	for _, want := range []string{
		"DO UPDATE SET is_active = TRUE, author_is_ai = $4, created_at = $5, updated_at = $5",
		"WHERE reposts.is_active = FALSE",
		"RETURNING id, created_at",
	} {
		if !strings.Contains(sqlRepostActivate, want) {
			t.Fatalf("sqlRepostActivate 缺少片段 %q:\n%s", want, sqlRepostActivate)
		}
	}
	if strings.Contains(sqlRepostActivate, "now()") || strings.Contains(sqlRepostActivate, "CURRENT_DATE") {
		t.Fatalf("SQL 不得使用数据库时间函数（时间由应用 Clock 显式传入）:\n%s", sqlRepostActivate)
	}
	// 已生效的重复转发需要回查既有行补齐 id/created_at，保证响应里的 repost 对象可用。
	if !strings.Contains(sqlRepostActivateExisting, "SELECT id, created_at FROM reposts") {
		t.Fatalf("sqlRepostActivateExisting 应回查既有转发行:\n%s", sqlRepostActivateExisting)
	}
}

// TestBuildRepostEventsSQLFollowingOnly 防回归：关注时间线必须显式按关注集合过滤，
// 空关注集合也要下传空数组条件（ANY(空数组) 匹配不到任何行），不能退化为全量纯转发。
func TestBuildRepostEventsSQLFollowingOnly(t *testing.T) {
	t.Run("空关注集合仍追加过滤条件", func(t *testing.T) {
		sql, args := buildRepostEventsSQL(domain.RepostEventQuery{
			FollowingOnly: true,
			FollowingIDs:  nil,
			Limit:         20,
		})
		if !strings.Contains(sql, "user_id = ANY($1)") {
			t.Fatalf("空关注集合也必须追加 ANY 过滤:\n%s", sql)
		}
		if len(args) != 2 {
			t.Fatalf("参数数量 = %d，期望 2（空数组 + limit）: %#v", len(args), args)
		}
		ids, ok := args[0].([]int64)
		if !ok || ids == nil || len(ids) != 0 {
			t.Fatalf("第一个参数应为非 nil 空 []int64，实际 %#v", args[0])
		}
	})

	t.Run("非空关注集合透传", func(t *testing.T) {
		sql, args := buildRepostEventsSQL(domain.RepostEventQuery{
			FollowingOnly: true,
			FollowingIDs:  []int64{7, 9},
			Limit:         20,
		})
		if !strings.Contains(sql, "user_id = ANY($1)") {
			t.Fatalf("关注集合应追加 ANY 过滤:\n%s", sql)
		}
		ids, ok := args[0].([]int64)
		if !ok || len(ids) != 2 || ids[0] != 7 || ids[1] != 9 {
			t.Fatalf("关注集合参数 = %#v，期望 [7 9]", args[0])
		}
	})

	t.Run("非关注时间线不过滤", func(t *testing.T) {
		sql, _ := buildRepostEventsSQL(domain.RepostEventQuery{
			FollowingIDs: []int64{7, 9},
			Limit:        20,
		})
		if strings.Contains(sql, "ANY(") {
			t.Fatalf("FollowingOnly=false 时不得追加关注集合过滤:\n%s", sql)
		}
	})

	t.Run("作者过滤与游标参数顺序", func(t *testing.T) {
		ai := true
		at := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
		sql, args := buildRepostEventsSQL(domain.RepostEventQuery{
			AuthorID:      3,
			FollowingOnly: true,
			FollowingIDs:  []int64{5},
			AuthorIsAI:    &ai,
			AfterAt:       at,
			AfterID:       100,
			Limit:         21,
		})
		for _, want := range []string{
			"user_id = $1",
			"user_id = ANY($2)",
			"author_is_ai = $3",
			"(created_at, id) < ($4, $5)",
			"LIMIT $6",
		} {
			if !strings.Contains(sql, want) {
				t.Fatalf("SQL 缺少片段 %q:\n%s", want, sql)
			}
		}
		if len(args) != 6 {
			t.Fatalf("参数数量 = %d，期望 6: %#v", len(args), args)
		}
		if args[0] != int64(3) || args[3] != at || args[4] != int64(100) || args[5] != 21 {
			t.Fatalf("参数顺序错误: %#v", args)
		}
	})
}
