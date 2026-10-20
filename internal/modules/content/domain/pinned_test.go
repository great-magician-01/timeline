package domain

import (
	"strings"
	"testing"

	"github.com/great-magician-01/timeline/internal/shared/model"
)

// TestCanPin 校验置顶状态机：仅已发布帖子允许置顶/取消置顶。
func TestCanPin(t *testing.T) {
	cases := []struct {
		status string
		want   bool
	}{
		{model.PostStatusPublished, true},
		{model.PostStatusDraft, false},
		{model.PostStatusScheduled, false},
		{model.PostStatusDeleted, false},
		{"unknown", false},
	}
	for _, tc := range cases {
		if got := CanPin(tc.status); got != tc.want {
			t.Fatalf("CanPin(%q) = %v，期望 %v", tc.status, got, tc.want)
		}
	}
}

// TestValidatePinnedReorder 校验置顶排序请求体校验。
func TestValidatePinnedReorder(t *testing.T) {
	valid := [][]int64{{1}, {3, 1, 2}, {9, 8, 7}}
	for _, ids := range valid {
		if err := ValidatePinnedReorder(ids); err != nil {
			t.Fatalf("ValidatePinnedReorder(%v) 应通过，实际: %v", ids, err)
		}
	}

	invalid := []struct {
		ids     []int64
		message string
	}{
		{nil, "请提供置顶帖列表"},
		{[]int64{}, "请提供置顶帖列表"},
		{[]int64{1, 2, 3, 4}, "置顶帖数量超出上限"},
		{[]int64{1, 1}, "存在重复"},
		{[]int64{0, 1}, "帖子 ID 无效"},
		{[]int64{-1}, "帖子 ID 无效"},
	}
	for _, tc := range invalid {
		err := ValidatePinnedReorder(tc.ids)
		if err == nil {
			t.Fatalf("ValidatePinnedReorder(%v) 应拒绝", tc.ids)
		}
		if !strings.Contains(err.Error(), tc.message) {
			t.Fatalf("ValidatePinnedReorder(%v) 文案应包含 %q，实际: %v", tc.ids, tc.message, err)
		}
	}
}

// TestSameIDSet 校验置顶集合一致性比较：元素与次数相同、顺序无关。
func TestSameIDSet(t *testing.T) {
	cases := []struct {
		a, b []int64
		want bool
	}{
		{[]int64{1, 2, 3}, []int64{3, 2, 1}, true},
		{[]int64{1}, []int64{1}, true},
		{nil, nil, true},
		{[]int64{1, 2}, []int64{1}, false},
		{[]int64{1}, []int64{1, 2}, false},
		{[]int64{1, 2}, []int64{1, 3}, false},
		{[]int64{1, 1, 2}, []int64{1, 2, 2}, false},
	}
	for _, tc := range cases {
		if got := SameIDSet(tc.a, tc.b); got != tc.want {
			t.Fatalf("SameIDSet(%v, %v) = %v，期望 %v", tc.a, tc.b, got, tc.want)
		}
	}
}
