package domain

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/shared/page"
)

// TestNormalizeSearchKeyword 校验搜索关键词：去空白、空值/超长返回 validation_failed 并带字段详情。
func TestNormalizeSearchKeyword(t *testing.T) {
	keyword, err := NormalizeSearchKeyword("  Go 并发  ")
	if err != nil {
		t.Fatalf("合法关键词不应报错: %v", err)
	}
	if keyword != "Go 并发" {
		t.Fatalf("关键词应去除首尾空白: %q", keyword)
	}
	if _, err := NormalizeSearchKeyword("   "); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("空关键词应为 validation_failed: %v", err)
	}
	tooLong := strings.Repeat("字", MaxSearchRunes+1)
	if _, err := NormalizeSearchKeyword(tooLong); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("超长关键词应为 validation_failed: %v", err)
	}
	if _, err := NormalizeSearchKeyword(strings.Repeat("字", MaxSearchRunes)); err != nil {
		t.Fatalf("恰好 %d 个字符应允许: %v", MaxSearchRunes, err)
	}
}

// TestNormalizeTagName 校验标签名：小写去空白，空值/超长返回空字符串（表示不查询）。
func TestNormalizeTagName(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"  GoLang  ", "golang"},
		{"#话题#", "#话题#"},
		{"", ""},
		{"   ", ""},
		{strings.Repeat("标", MaxTagRunes+1), ""},
		{strings.Repeat("标", MaxTagRunes), strings.Repeat("标", MaxTagRunes)},
	}
	for _, tc := range cases {
		if got := NormalizeTagName(tc.raw); got != tc.want {
			t.Errorf("NormalizeTagName(%q) = %q，期望 %q", tc.raw, got, tc.want)
		}
	}
}

// TestPostStatusPredicates 校验草稿工作区状态判断与版本号计算：
// 仅 draft/scheduled 可取消定时/编辑；deleted 为已删除；版本号从 1 开始递增。
func TestPostStatusPredicates(t *testing.T) {
	cancellable := []string{model.PostStatusDraft, model.PostStatusScheduled}
	for _, status := range cancellable {
		if !CanCancelSchedule(status) {
			t.Errorf("状态 %q 应允许取消定时", status)
		}
		if !Editable(status) {
			t.Errorf("状态 %q 应可编辑草稿", status)
		}
	}
	for _, status := range []string{model.PostStatusPublished, model.PostStatusDeleted, ""} {
		if CanCancelSchedule(status) {
			t.Errorf("状态 %q 不应允许取消定时", status)
		}
		if Editable(status) {
			t.Errorf("状态 %q 不应可编辑草稿", status)
		}
	}
	if !IsDeleted(model.PostStatusDeleted) {
		t.Fatal("deleted 应判定为已删除")
	}
	if IsDeleted(model.PostStatusDraft) {
		t.Fatal("draft 不应判定为已删除")
	}

	versions := []struct {
		max  int
		want int
	}{
		{-1, 1},
		{0, 1},
		{1, 2},
		{7, 8},
	}
	for _, tc := range versions {
		if got := NextVersionNumber(tc.max); got != tc.want {
			t.Errorf("NextVersionNumber(%d) = %d，期望 %d", tc.max, got, tc.want)
		}
	}
}

// TestCheckPublishState 校验发布状态机：draft/scheduled 允许发布，其余返回 conflict invalid_state。
func TestCheckPublishState(t *testing.T) {
	for _, status := range []string{model.PostStatusDraft, model.PostStatusScheduled} {
		if err := CheckPublishState(status); err != nil {
			t.Errorf("状态 %q 应允许发布: %v", status, err)
		}
	}
	for _, status := range []string{model.PostStatusPublished, model.PostStatusDeleted, ""} {
		err := CheckPublishState(status)
		if !apperr.IsKind(err, apperr.KindConflict) {
			t.Errorf("状态 %q 应返回 conflict，实际 %v", status, err)
		}
	}
}

// TestCounterColumnAndDelta 校验计数器列名映射与增减幅度校验。
func TestCounterColumnAndDelta(t *testing.T) {
	columns := map[string]string{
		"like":    "like_count",
		"comment": "comment_count",
		"repost":  "repost_count",
	}
	for counter, want := range columns {
		got, err := CounterColumn(counter)
		if err != nil || got != want {
			t.Errorf("CounterColumn(%q) = (%q,%v)，期望 %q", counter, got, err, want)
		}
	}
	if _, err := CounterColumn("unknown"); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("未知计数器应为 validation_failed: %v", err)
	}
	if err := ValidateCounterDelta(0); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("0 增减幅度应为 validation_failed: %v", err)
	}
	for _, delta := range []int{1, -1, 5} {
		if err := ValidateCounterDelta(delta); err != nil {
			t.Errorf("幅度 %d 应合法: %v", delta, err)
		}
	}
}

// collectItem 是 CollectPage 测试用的候选：按时间倒序由 key 计算游标。
type collectItem struct {
	id   int64
	at   time.Time
	keep bool
}

// TestCollectPageMultiBatch 校验过滤后不足一页时继续分批获取，最多 MaxPageBatches 轮，
// 并在凑满一页后截断、报告 has_more。
func TestCollectPageMultiBatch(t *testing.T) {
	base := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	key := func(item collectItem) (time.Time, int64) { return item.at, item.id }

	t.Run("过滤后继续下一批", func(t *testing.T) {
		calls := 0
		fetch := func(_ context.Context, _ page.TimeCursor, limit int) ([]collectItem, error) {
			calls++
			switch calls {
			case 1:
				// 3 条候选（limit+1）：前 limit 条都被过滤掉，触发继续拉取。
				return []collectItem{
					{id: 3, at: base, keep: false},
					{id: 2, at: base.Add(-time.Minute), keep: false},
					{id: 1, at: base.Add(-2 * time.Minute), keep: true},
				}, nil
			case 2:
				// 再来 3 条候选，其中 2 条保留：过滤后总数超过 limit，应截断。
				return []collectItem{
					{id: 6, at: base.Add(-3 * time.Minute), keep: true},
					{id: 5, at: base.Add(-4 * time.Minute), keep: true},
					{id: 4, at: base.Add(-5 * time.Minute), keep: true},
				}, nil
			default:
				return nil, nil
			}
		}
		items, cursor, hasMore, err := CollectPage(context.Background(), page.TimeCursor{}, 2, fetch,
			key, func(item collectItem) bool { return item.keep })
		if err != nil {
			t.Fatalf("CollectPage: %v", err)
		}
		if calls != 2 {
			t.Fatalf("fetch 调用次数 = %d，期望 2", calls)
		}
		if len(items) != 2 {
			t.Fatalf("返回条数 = %d，期望截断到 limit=2", len(items))
		}
		// 第一轮前 limit 条被过滤（第 limit+1 条留到下一页重新拉取），第二轮凑满一页。
		if items[0].id != 6 || items[1].id != 5 {
			t.Fatalf("返回条目异常: %+v", items)
		}
		if !hasMore {
			t.Fatal("过滤后仍有剩余候选，hasMore 应为 true")
		}
		if cursor.ID != 5 || !cursor.Time.Equal(base.Add(-4*time.Minute)) {
			t.Fatalf("游标应为本页最后一条候选: %+v", cursor)
		}
	})

	t.Run("取尽后结束", func(t *testing.T) {
		fetch := func(context.Context, page.TimeCursor, int) ([]collectItem, error) {
			return []collectItem{{id: 2, at: base, keep: true}}, nil
		}
		items, _, hasMore, err := CollectPage(context.Background(), page.TimeCursor{}, 2, fetch,
			key, nil)
		if err != nil {
			t.Fatalf("CollectPage: %v", err)
		}
		if len(items) != 1 || hasMore {
			t.Fatalf("候选不足一页应结束: items=%d hasMore=%v", len(items), hasMore)
		}
	})

	t.Run("拉取错误透传", func(t *testing.T) {
		wantErr := errors.New("查询失败")
		fetch := func(context.Context, page.TimeCursor, int) ([]collectItem, error) {
			return nil, wantErr
		}
		if _, _, _, err := CollectPage(context.Background(), page.TimeCursor{}, 2, fetch, key, nil); !errors.Is(err, wantErr) {
			t.Fatalf("应透传拉取错误，实际 %v", err)
		}
	})
}
