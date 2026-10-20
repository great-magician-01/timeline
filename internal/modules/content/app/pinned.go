package app

import (
	"context"

	"github.com/great-magician-01/timeline/internal/modules/content/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// 置顶是作者对个人主页展示的运营操作（0022）：仅作者本人可操作，仅已发布帖子，
// 每位作者最多 domain.MaxPinnedPerAuthor 条。三条写路径都在事务内锁定置顶行
// （FOR UPDATE）串行化，避免并发操作打乱展示顺序；置顶状态变更不修改 updated_at
// （置顶不是内容编辑）。详情/列表组装在事务提交后执行，不占用行锁。

// pinGuards 置顶操作的公共前置校验：登录、停用、AI 拒绝（与删除用例保持一致）。
func pinGuards(viewer model.Viewer) error {
	if !viewer.Authenticated {
		return apperr.Unauthenticated("请先登录")
	}
	if viewer.IsSuspended {
		return apperr.Suspended("账号已停用，不能执行此操作")
	}
	if viewer.IsAI {
		return apperr.Forbidden("AI 账号不能通过此接口操作")
	}
	return nil
}

// PinPost 作者把自己的已发布帖子置顶到个人主页：新帖追加到置顶列表末尾。
// 已置顶的帖子重复置顶按幂等处理；置顶已满返回冲突。
// 非作者请求按内容不存在处理，避免泄露他人草稿/定时帖的存在性。
func (s *Service) PinPost(ctx context.Context, viewer model.Viewer, postID int64) (PostDetail, error) {
	if err := pinGuards(viewer); err != nil {
		return PostDetail{}, err
	}
	err := s.withTx(ctx, func(txCtx context.Context) error {
		post, err := s.repo.GetPostForUpdate(txCtx, postID)
		if err != nil {
			return err
		}
		if post.AuthorID != viewer.ID {
			return apperr.NotFound("内容不存在")
		}
		if post.Status == model.PostStatusDeleted {
			return apperr.NotFound("内容不存在")
		}
		if !domain.CanPin(post.Status) {
			return apperr.Conflict("invalid_state", "只有已发布的帖子可以置顶")
		}
		if post.IsPinned() {
			return nil
		}
		pinned, err := s.repo.PinnedPostsByAuthorForUpdate(txCtx, viewer.ID)
		if err != nil {
			return s.wrap(err, "读取置顶帖失败")
		}
		if len(pinned) >= domain.MaxPinnedPerAuthor {
			return apperr.Conflict("pin_limit", "最多置顶 3 条帖子，请先取消已有置顶").
				WithDetails(map[string]any{"pinned": "置顶帖数量已达上限"})
		}
		// 删除置顶帖（SoftDelete 清列）会在剩余顺序中留下空隙，这里先归一化
		// 再追加到末尾，保证顺序始终连续（同序兜底由 id DESC 承担）。
		if len(pinned) > 0 {
			ids := make([]int64, 0, len(pinned))
			for _, item := range pinned {
				ids = append(ids, item.ID)
			}
			if err := s.repo.RenumberPinned(txCtx, ids); err != nil {
				return s.wrap(err, "重排置顶顺序失败")
			}
		}
		// 并发置顶在作者无置顶帖时可能同时读到空列表而写入相同顺序值：
		// 顺序仅用于排序展示（同序按 id 兜底），不破坏数据一致性，可接受。
		return s.repo.SetPinned(txCtx, postID, s.Now(), len(pinned)+1)
	})
	if err != nil {
		return PostDetail{}, err
	}
	return s.pinnedPostDetail(ctx, viewer, postID)
}

// UnpinPost 作者取消置顶；剩余置顶帖顺序归一化为 1..n（保持原相对顺序）。
// 未置顶的帖子重复取消按幂等处理。
func (s *Service) UnpinPost(ctx context.Context, viewer model.Viewer, postID int64) (PostDetail, error) {
	if err := pinGuards(viewer); err != nil {
		return PostDetail{}, err
	}
	err := s.withTx(ctx, func(txCtx context.Context) error {
		post, err := s.repo.GetPostForUpdate(txCtx, postID)
		if err != nil {
			return err
		}
		if post.AuthorID != viewer.ID {
			return apperr.NotFound("内容不存在")
		}
		if post.Status == model.PostStatusDeleted {
			return apperr.NotFound("内容不存在")
		}
		if !post.IsPinned() {
			return nil
		}
		if err := s.repo.ClearPinned(txCtx, postID); err != nil {
			return s.wrap(err, "取消置顶失败")
		}
		return s.normalizePinnedOrder(txCtx, viewer.ID)
	})
	if err != nil {
		return PostDetail{}, err
	}
	return s.pinnedPostDetail(ctx, viewer, postID)
}

// ReorderPinnedPosts 作者调整个人主页置顶帖展示顺序：请求必须给出当前全部置顶帖
// 的完整有序列表（首项最前），与当前置顶集合不一致时拒绝，避免并发改动被静默覆盖。
// 返回重排后的置顶帖列表项。
func (s *Service) ReorderPinnedPosts(ctx context.Context, viewer model.Viewer, postIDs []int64) ([]PostBrief, error) {
	if err := pinGuards(viewer); err != nil {
		return nil, err
	}
	if err := domain.ValidatePinnedReorder(postIDs); err != nil {
		return nil, err
	}
	err := s.withTx(ctx, func(txCtx context.Context) error {
		pinned, err := s.repo.PinnedPostsByAuthorForUpdate(txCtx, viewer.ID)
		if err != nil {
			return s.wrap(err, "读取置顶帖失败")
		}
		current := make([]int64, 0, len(pinned))
		for _, post := range pinned {
			current = append(current, post.ID)
		}
		if !domain.SameIDSet(postIDs, current) {
			return apperr.Validation("置顶帖列表与当前置顶不一致，请刷新后重试").
				WithDetails(map[string]any{"post_ids": "排序请求必须包含当前全部置顶帖"})
		}
		return s.repo.RenumberPinned(txCtx, postIDs)
	})
	if err != nil {
		return nil, err
	}
	return s.pinnedBriefs(ctx, viewer, viewer.ID)
}

// pinnedPostDetail 组装置顶操作后的帖子详情（作者必然可见自己的帖子）。
func (s *Service) pinnedPostDetail(ctx context.Context, viewer model.Viewer, postID int64) (PostDetail, error) {
	post, err := s.repo.GetPost(ctx, postID)
	if err != nil {
		return PostDetail{}, s.wrap(err, "读取帖子失败")
	}
	following, err := s.followingSet(ctx, viewer)
	if err != nil {
		return PostDetail{}, err
	}
	return s.loadDetail(ctx, viewer, following, post)
}

// normalizePinnedOrder 把作者剩余置顶帖的展示顺序重排为 1..n（保持原相对顺序）。
// 在取消置顶后调用；置顶与排序路径写入的顺序本身就是连续的，无需归一化。
func (s *Service) normalizePinnedOrder(ctx context.Context, authorID int64) error {
	remaining, err := s.repo.PinnedPostsByAuthorForUpdate(ctx, authorID)
	if err != nil {
		return s.wrap(err, "读取置顶帖失败")
	}
	if len(remaining) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(remaining))
	for _, post := range remaining {
		ids = append(ids, post.ID)
	}
	if err := s.repo.RenumberPinned(ctx, ids); err != nil {
		return s.wrap(err, "重排置顶顺序失败")
	}
	return nil
}
