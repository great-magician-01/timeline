package domain

import (
	"context"
	"testing"
)

// idItem 是 CollectIDPage 测试用的候选：ID 倒序，keep 决定是否保留。
type idItem struct {
	id   int64
	keep bool
}

// TestCollectIDPage 校验 ID 游标收集器的基本分页行为。
func TestCollectIDPage(t *testing.T) {
	fetch := func(_ context.Context, afterID int64, limit int) ([]idItem, error) {
		items := make([]idItem, 0, limit)
		start := int64(100)
		if afterID > 0 {
			start = afterID
		}
		for i := 0; i < limit; i++ {
			items = append(items, idItem{id: start - int64(i+1), keep: true})
		}
		return items, nil
	}
	collected, nextID, hasMore, err := CollectIDPage(context.Background(), 0, 3, fetch,
		func(i idItem) int64 { return i.id }, nil)
	if err != nil {
		t.Fatalf("CollectIDPage: %v", err)
	}
	if len(collected) != 3 || !hasMore {
		t.Fatalf("基本分页: len=%d hasMore=%v", len(collected), hasMore)
	}
	if nextID != 97 {
		t.Fatalf("游标 = %d，期望本页最后一条候选的 ID 97", nextID)
	}
}

// TestCollectIDPageFilteredRefill 校验过滤后不足一页时继续补批，
// 且游标取「本页最后一条候选（含被过滤者）」而不是最后一条保留项。
func TestCollectIDPageFilteredRefill(t *testing.T) {
	calls := 0
	fetch := func(_ context.Context, _ int64, limit int) ([]idItem, error) {
		calls++
		switch calls {
		case 1:
			// limit+1 条候选：前 limit 条全部被过滤，触发继续拉取。
			return []idItem{
				{id: 103, keep: false},
				{id: 102, keep: false},
				{id: 101, keep: false},
			}, nil
		case 2:
			return []idItem{
				{id: 106, keep: true},
				{id: 105, keep: true},
				{id: 104, keep: false},
			}, nil
		default:
			return nil, nil
		}
	}
	items, nextID, hasMore, err := CollectIDPage(context.Background(), 0, 2, fetch,
		func(i idItem) int64 { return i.id }, func(i idItem) bool { return i.keep })
	if err != nil {
		t.Fatalf("CollectIDPage: %v", err)
	}
	if calls != 2 {
		t.Fatalf("fetch 调用次数 = %d，期望 2", calls)
	}
	if len(items) != 2 || items[0].id != 106 || items[1].id != 105 {
		t.Fatalf("返回条目 = %+v，期望 [106 105]", items)
	}
	// 游标取截断后批次（[106 105]）的最后一条候选，与 CollectPage 语义一致。
	if nextID != 105 {
		t.Fatalf("游标 = %d，期望本页最后一条候选 105", nextID)
	}
	if !hasMore {
		t.Fatal("取尽时 hasMore 应为 false")
	}
}

// TestCollectIDPageBatchCap 校验被过滤候选持续存在时最多扫描 MaxPageBatches 轮。
func TestCollectIDPageBatchCap(t *testing.T) {
	calls := 0
	fetch := func(_ context.Context, afterID int64, limit int) ([]idItem, error) {
		calls++
		items := make([]idItem, 0, limit)
		start := int64(1000)
		if afterID > 0 {
			start = afterID
		}
		for i := 0; i < limit; i++ {
			items = append(items, idItem{id: start - int64(i+1), keep: false})
		}
		return items, nil
	}
	items, _, hasMore, err := CollectIDPage(context.Background(), 0, 3, fetch,
		func(i idItem) int64 { return i.id }, func(i idItem) bool { return i.keep })
	if err != nil {
		t.Fatalf("CollectIDPage: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("全部被过滤应返回空列表，实际 %d 条", len(items))
	}
	if calls != MaxPageBatches {
		t.Fatalf("fetch 调用次数 = %d，期望上限 %d", calls, MaxPageBatches)
	}
	if !hasMore {
		t.Fatal("达到轮次上限仍有候选时 hasMore 应为 true")
	}
}
