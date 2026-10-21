package domain

import (
	"context"
	"time"

	"github.com/great-magician-01/timeline/internal/shared/page"
)

// FetchFunc 从游标之后按时间倒序拉取一批候选。
type FetchFunc[T any] func(ctx context.Context, after page.TimeCursor, limit int) ([]T, error)

// CollectPage 按「多取 limit+1 条 → 过滤 → 判断是否还有更多」的策略组装一页。
// 过滤后不足 limit 且仍有候选时继续分批获取，最多 MaxPageBatches 轮，避免死循环。
// 返回的游标是本页最后一条候选的 (时间, ID)，供调用方生成下一页 next_cursor。
func CollectPage[T any](
	ctx context.Context,
	after page.TimeCursor,
	limit int,
	fetch FetchFunc[T],
	key func(T) (time.Time, int64),
	keep func(T) bool,
) ([]T, page.TimeCursor, bool, error) {
	collected := make([]T, 0, limit)
	exhausted := false
	cursor := after
	for round := 0; round < MaxPageBatches; round++ {
		batch, err := fetch(ctx, cursor, limit+1)
		if err != nil {
			return nil, page.TimeCursor{}, false, err
		}
		if len(batch) == 0 {
			exhausted = true
			break
		}
		hasCandidate := len(batch) > limit
		if hasCandidate {
			batch = batch[:limit]
		}
		lastTime, lastID := key(batch[len(batch)-1])
		cursor = page.TimeCursor{Time: lastTime, ID: lastID}
		for _, item := range batch {
			if keep == nil || keep(item) {
				collected = append(collected, item)
			}
		}
		// 本轮候选不足 limit+1 说明后端已取尽，即使正好凑满一页也不再有下一页。
		if !hasCandidate {
			exhausted = true
			break
		}
		if len(collected) >= limit {
			break
		}
	}
	hasMore := !exhausted || len(collected) > limit
	if len(collected) > limit {
		collected = collected[:limit]
	}
	return collected, cursor, hasMore, nil
}

// IDFetchFunc 从行 ID 之后按 ID 倒序拉取一批候选（雪花 ID 即时间序）。
type IDFetchFunc[T any] func(ctx context.Context, afterID int64, limit int) ([]T, error)

// CollectIDPage 是 CollectPage 的纯 ID 游标变体：过滤后不足 limit 且仍有候选时
// 继续分批获取，最多 MaxPageBatches 轮。返回的 nextID 是本页最后一条候选的 ID
// （含被过滤者，与 CollectPage 的游标语义一致），供调用方生成下一页 next_cursor。
func CollectIDPage[T any](
	ctx context.Context,
	afterID int64,
	limit int,
	fetch IDFetchFunc[T],
	key func(T) int64,
	keep func(T) bool,
) ([]T, int64, bool, error) {
	collected := make([]T, 0, limit)
	exhausted := false
	cursor := afterID
	for round := 0; round < MaxPageBatches; round++ {
		batch, err := fetch(ctx, cursor, limit+1)
		if err != nil {
			return nil, 0, false, err
		}
		if len(batch) == 0 {
			exhausted = true
			break
		}
		hasCandidate := len(batch) > limit
		if hasCandidate {
			batch = batch[:limit]
		}
		cursor = key(batch[len(batch)-1])
		for _, item := range batch {
			if keep == nil || keep(item) {
				collected = append(collected, item)
			}
		}
		// 本轮候选不足 limit+1 说明后端已取尽，即使正好凑满一页也不再有下一页。
		if !hasCandidate {
			exhausted = true
			break
		}
		if len(collected) >= limit {
			break
		}
	}
	hasMore := !exhausted || len(collected) > limit
	if len(collected) > limit {
		collected = collected[:limit]
	}
	return collected, cursor, hasMore, nil
}
