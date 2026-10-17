package media

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/access"
	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/audit"
	"github.com/great-magician-01/timeline/internal/auth"
	"github.com/great-magician-01/timeline/internal/model"
	"github.com/great-magician-01/timeline/internal/settings"
)

// cleanupRetention 返回临时附件保留期。settings 已保证有效值大于零，
// 这里的兜底只防御调用方手工构造的 Values。
func cleanupRetention(values settings.Values) time.Duration {
	if values.TempAttachmentRetention <= 0 {
		return settings.DefaultTempRetention
	}
	return values.TempAttachmentRetention
}

// cleanupCutoff 返回临时附件的失效时间点：早于该时间且未关联帖子的附件应被标记失效
// （纯函数，便于单元测试；SQL 条件与之保持一致）。
func cleanupCutoff(now time.Time, retention time.Duration) time.Time {
	return now.Add(-retention)
}

// expiredUnattached 判断一个未关联帖子的附件是否已超过保留期。
func expiredUnattached(createdAt, now time.Time, retention time.Duration) bool {
	return createdAt.Before(cleanupCutoff(now, retention))
}

// cleanupAttachmentsSQL 标记过期临时附件为失效。只处理同时满足以下条件的记录：
//   - 仍可用且用途为帖子（头像附件的用途是 avatar，不在此列）；
//   - 尚未挂到任何帖子（post_id IS NULL）；
//   - 不存在生效中的草稿关联（post_draft_attachments.is_active = TRUE），
//     避免草稿已引用但尚未发布的附件被误清理；
//   - users.avatar_url 未引用该附件（防御历史上的错误用途数据）。
//
// UPDATE 在写入时再次检查引用，避免与草稿保存、发布竞争导致误标；
// 全流程只更新状态，不删除数据库记录与物理文件。
const cleanupAttachmentsSQL = `
	UPDATE attachments AS a
	SET status = 'deleted', updated_at = now()
	WHERE a.post_id IS NULL
	  AND a.status = 'available'
	  AND a.purpose = 'post'
	  AND a.created_at < $1
	  AND NOT EXISTS (
		SELECT 1 FROM post_draft_attachments AS da
		WHERE da.attachment_id = a.id AND da.is_active
	  )
	  AND NOT EXISTS (
		SELECT 1 FROM users AS u
		WHERE u.avatar_url = '/api/v1/attachments/' || a.id::text || '/content'
	  )`

// RunCleanup 是周期清理任务（由作业运行器每 6 小时调用一次，docs/WORKFLOWS.md 6.3）：
//  1. 超过保留期且未被草稿或帖子引用的临时帖子附件标记为失效；
//  2. 已失效附件上未完成的衍生文件标记为取消。
//
// 保留期来自系统设置（media.temp_attachment_retention_hours），每次执行即时读取。
// 全程只更新状态，绝不删除数据库记录与物理文件。
func (s *Service) RunCleanup(ctx context.Context) {
	pool := s.pools.Pool()
	if pool == nil {
		return
	}
	values := settings.LoadOrDefaults(ctx, pool)
	retention := cleanupRetention(values)
	expired, err := pool.Exec(ctx, cleanupAttachmentsSQL, cleanupCutoff(time.Now(), retention))
	if err != nil {
		s.log.Error("清理过期临时附件失败", "err", err)
		return
	}
	canceled, err := pool.Exec(ctx, `
		UPDATE attachment_derivatives
		SET status = 'canceled', updated_at = now()
		WHERE status NOT IN ('ready', 'canceled')
		  AND attachment_id IN (SELECT id FROM attachments WHERE status = 'deleted')`)
	if err != nil {
		s.log.Error("取消失效附件的衍生文件失败", "err", err)
		return
	}
	s.log.Info("附件清理完成",
		"retention_hours", int(retention.Hours()),
		"expired_attachments", expired.RowsAffected(),
		"canceled_derivatives", canceled.RowsAffected())
}

// Delete 处理 POST /api/v1/attachments/:id/delete（仅上传者本人）。
// 已被历史版本引用时只解除当前草稿关联，否则把附件整体置为失效；
// 数据库记录与物理文件始终保留。
func (s *Service) Delete(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	u, ok := auth.CurrentUser(c)
	if !ok {
		api.Error(c, http.StatusUnauthorized, "unauthenticated", "请先登录")
		return
	}
	if err := access.RequireWritable(model.ViewerFrom(u)); err != nil {
		if !api.Respond(c, err) {
			s.internalError(c, "校验删除权限失败", err)
		}
		return
	}
	att, ok := s.attachmentFromPath(c, pool)
	if !ok {
		return
	}
	if att.UploaderID != u.ID {
		// 不向非上传者暴露附件是否存在。
		api.Error(c, http.StatusNotFound, "not_found", "附件不存在或无权操作")
		return
	}
	detached, deleted, err := s.invalidateAttachment(c.Request.Context(), pool, att.ID)
	if err != nil {
		s.internalError(c, "删除附件失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"detached": detached, "deleted": deleted})
}

// DeleteAsAdmin 供管理后台处置附件使用（internal/admin 调用）：跳过上传者校验。
// 与用户删除一致的引用规则：仍被历史版本引用时只解除草稿关联，否则整体置为失效。
// adminID 用于审计；调用方应传入当前事务，使审计与业务变更同生共死。
func (s *Service) DeleteAsAdmin(ctx context.Context, q access.Querier, adminID, attachmentID int64) error {
	att, err := s.AttachmentByID(ctx, q, attachmentID)
	if err != nil {
		return err
	}
	if !att.Available() {
		return api.ErrNotFound("附件不存在或已失效")
	}
	detached, deleted, err := s.invalidateAttachment(ctx, q, att.ID)
	if err != nil {
		return err
	}
	if err := audit.Write(ctx, q, s.ids, adminID, audit.ActionAttachmentDelete, refTypeAttachment,
		&attachmentID, map[string]any{"detached": detached, "deleted": deleted}); err != nil {
		return err
	}
	s.log.Info("管理员处置附件",
		"admin_id", adminID, "attachment_id", attachmentID,
		"detached", detached, "deleted", deleted)
	return nil
}

// invalidateAttachment 执行附件失效，返回 (是否解除了草稿关联, 是否整体失效)。
// 规则（docs/WORKFLOWS.md 6.3）：
//   - 先解除当前草稿关联（post_draft_attachments.is_active = FALSE）；
//   - 只要仍被任何历史版本引用，就保留附件可用状态，仅解除关联；
//   - 否则把附件置为 deleted，并取消尚未就绪的衍生文件；物理文件不删除。
func (s *Service) invalidateAttachment(ctx context.Context, q access.Querier, attachmentID int64) (detached bool, deleted bool, err error) {
	tag, err := q.Exec(ctx, `
		UPDATE post_draft_attachments SET is_active = FALSE
		WHERE attachment_id = $1 AND is_active`, attachmentID)
	if err != nil {
		return false, false, err
	}
	detached = tag.RowsAffected() > 0

	var referenced bool
	if err := q.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM post_version_attachments WHERE attachment_id = $1)`,
		attachmentID,
	).Scan(&referenced); err != nil {
		return detached, false, err
	}
	if referenced {
		return detached, false, nil
	}

	tag, err = q.Exec(ctx, `
		UPDATE attachments SET status = 'deleted', updated_at = now()
		WHERE id = $1 AND status <> 'deleted'`, attachmentID)
	if err != nil {
		return detached, false, err
	}
	if tag.RowsAffected() == 0 {
		// 附件已失效（例如并发删除），无需重复处理。
		return detached, true, nil
	}
	if _, err := q.Exec(ctx, `
		UPDATE attachment_derivatives
		SET status = 'canceled', updated_at = now()
		WHERE attachment_id = $1 AND status NOT IN ('ready', 'canceled')`, attachmentID); err != nil {
		return detached, true, err
	}
	return detached, true, nil
}
