package repo

import (
	"context"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/content/domain"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// 置顶是个人主页展示层状态（0022）：本文件只提供状态写入与读取，
// 作者身份、已发布状态与 3 条上限等业务校验在 app 层完成。
// 所有置顶写入都不修改 posts.updated_at（置顶不是内容编辑，沿用计数器先例）。

// SetPinned 置顶帖子：写入置顶时间与展示顺序（1 = 最前）。调用方必须在事务内
// 先锁定帖子行并完成校验（作者本人、已发布、未超上限）。
func (r *Repo) SetPinned(ctx context.Context, postID int64, at time.Time, sort int) error {
	_, err := r.ex(ctx).Exec(ctx, `
		UPDATE posts SET pinned_at = $2, pinned_sort = $3 WHERE id = $1`,
		postID, at, sort)
	return err
}

// ClearPinned 取消置顶：两列同时置空（CHECK 约束要求同空同非空）。
func (r *Repo) ClearPinned(ctx context.Context, postID int64) error {
	_, err := r.ex(ctx).Exec(ctx, `
		UPDATE posts SET pinned_at = NULL, pinned_sort = NULL WHERE id = $1`, postID)
	return err
}

// RenumberPinned 按给定顺序把置顶帖的 pinned_sort 重排为 1..n：列表首项为 1。
// 调用方保证 ID 均属于同一作者且当前处于置顶状态；pinned_sort 无唯一约束，
// 逐行更新不会触发冲突。置顶时间保持不变（重排不改变置顶先后语义）。
func (r *Repo) RenumberPinned(ctx context.Context, orderedIDs []int64) error {
	for i, id := range orderedIDs {
		if _, err := r.ex(ctx).Exec(ctx,
			`UPDATE posts SET pinned_sort = $2 WHERE id = $1`, id, i+1); err != nil {
			return err
		}
	}
	return nil
}

// PinnedPostsByAuthor 读取作者当前置顶帖（最多 3 条），按展示顺序升序返回。
// 已删除帖子因 SoftDelete 同步清空置顶列不会出现；可见性复核仍由 app 层完成。
func (r *Repo) PinnedPostsByAuthor(ctx context.Context, authorID int64) ([]model.Post, error) {
	return r.pinnedPostsByAuthor(ctx, authorID, false)
}

// PinnedPostsByAuthorForUpdate 锁定作者当前置顶帖行；调用方必须在事务内使用。
// 置顶/取消/排序三条写路径统一经它串行化，避免并发写乱展示顺序。
func (r *Repo) PinnedPostsByAuthorForUpdate(ctx context.Context, authorID int64) ([]model.Post, error) {
	return r.pinnedPostsByAuthor(ctx, authorID, true)
}

func (r *Repo) pinnedPostsByAuthor(ctx context.Context, authorID int64, forUpdate bool) ([]model.Post, error) {
	sql := `SELECT ` + postColumns + ` FROM posts
		WHERE author_id = $1 AND status = 'published' AND pinned_at IS NOT NULL`
	if forUpdate {
		sql += ` ORDER BY pinned_sort ASC, id DESC FOR UPDATE`
	} else {
		sql += ` ORDER BY pinned_sort ASC, id DESC`
	}
	rows, err := r.ex(ctx).Query(ctx, sql, authorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.Post, 0, domain.MaxPinnedPerAuthor)
	for rows.Next() {
		post, err := scanPost(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, post)
	}
	return items, rows.Err()
}
