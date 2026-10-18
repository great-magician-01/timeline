package app

import (
	"context"
	"errors"

	"github.com/great-magician-01/timeline/internal/modules/media/domain"
	"github.com/great-magician-01/timeline/internal/modules/media/repo"
)

// cleanupCandidateLimit 是单次清理最多处理的候选附件数；每 6 小时一次，足以覆盖增量。
const cleanupCandidateLimit = 1000

// RunCleanup 是周期清理任务（由作业运行器每 6 小时调用一次，docs/WORKFLOWS.md 6.3）：
//  1. 选出超过保留期、未被头像引用的临时帖子附件候选；
//  2. 通过 content 端口剔除仍被生效草稿关联的候选（不跨模块查表）；
//  3. 把剩余候选标记为失效；
//  4. 已失效附件上未完成的衍生文件标记为取消。
//
// 保留期来自系统设置（media.temp_attachment_retention_hours），每次执行即时读取；
// 截止时间由应用时钟计算后作为参数传给 SQL。全程只更新状态，绝不删除数据库记录与物理文件。
// 存量头像引用由迁移 0016 回填 users.avatar_attachment_id 保证，清理不依赖 avatar_url。
func (s *Service) RunCleanup(ctx context.Context) {
	values := s.settingsOrDefaults(ctx)
	retention := domain.CleanupRetention(values.TempAttachmentRetention)
	at := s.clk.Now()
	cutoff := domain.CleanupCutoff(at, retention)

	// 头像引用属于 auth 模块，通过端口读取后作为 SQL 排除参数，media 不查 users 表。
	avatarAttachmentIDs, err := s.auth.ListAvatarAttachmentIDs(ctx)
	if err != nil {
		s.log.Error("读取头像引用附件失败，跳过本次附件清理", "err", err)
		return
	}

	exec := s.exec(ctx)
	candidates, err := repo.ListExpiredTempAttachmentCandidates(
		ctx, exec, cutoff, cleanupCandidateLimit, avatarAttachmentIDs,
	)
	if err != nil {
		s.log.Error("查询过期临时附件失败", "err", err)
		return
	}

	// 草稿引用属于 content 模块：通过端口二次过滤后再按 ID 标记失效。
	deletable, err := s.filterDraftReferenced(ctx, candidates)
	if err != nil {
		s.log.Error("过滤草稿引用附件失败，跳过本次附件清理", "err", err)
		return
	}
	expired, err := repo.MarkDeletedByIDs(ctx, exec, deletable, at)
	if err != nil {
		s.log.Error("清理过期临时附件失败", "err", err)
		return
	}

	canceled, err := repo.CancelIncompleteForDeletedAttachments(ctx, exec, at)
	if err != nil {
		s.log.Error("取消失效附件的衍生文件失败", "err", err)
		return
	}
	s.log.Info("附件清理完成",
		"retention_hours", int(retention.Hours()),
		"candidates", len(candidates),
		"expired_attachments", expired,
		"canceled_derivatives", canceled)
}

// filterDraftReferenced 剔除仍被生效草稿关联的附件，返回可安全失效的 ID（保持输入顺序）。
// 这是清理两步流程的第二步：草稿关联由 content 端口回答，media 不直接查询草稿表。
func (s *Service) filterDraftReferenced(ctx context.Context, candidates []int64) ([]int64, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	if s.content == nil {
		return nil, s.internal("内容服务未初始化", errors.New("content port is nil"))
	}
	referenced, err := s.content.ActiveDraftAttachmentIDs(ctx, candidates)
	if err != nil {
		return nil, s.wrap("查询草稿附件引用失败", err)
	}
	skip := make(map[int64]struct{}, len(referenced))
	for _, id := range referenced {
		skip[id] = struct{}{}
	}
	out := make([]int64, 0, len(candidates))
	for _, id := range candidates {
		if _, ok := skip[id]; ok {
			continue
		}
		out = append(out, id)
	}
	return out, nil
}
