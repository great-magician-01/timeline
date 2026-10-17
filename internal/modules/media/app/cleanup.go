package app

import (
	"context"

	"github.com/great-magician-01/timeline/internal/modules/media/domain"
	"github.com/great-magician-01/timeline/internal/modules/media/repo"
)

// RunCleanup 是周期清理任务（由作业运行器每 6 小时调用一次，docs/WORKFLOWS.md 6.3）：
//  1. 超过保留期且未被草稿、帖子或用户头像引用的临时帖子附件标记为失效；
//  2. 已失效附件上未完成的衍生文件标记为取消。
//
// 保留期来自系统设置（media.temp_attachment_retention_hours），每次执行即时读取；
// 截止时间由应用时钟计算后作为参数传给 SQL。全程只更新状态，绝不删除数据库记录与物理文件。
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
	expired, err := repo.MarkExpiredTempDeleted(ctx, exec, cutoff, at, avatarAttachmentIDs)
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
		"expired_attachments", expired,
		"canceled_derivatives", canceled)
}
