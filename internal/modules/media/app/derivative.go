package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/media/domain"
	"github.com/great-magician-01/timeline/internal/modules/media/repo"
	"github.com/great-magician-01/timeline/internal/platform/jobs"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// derivativePayload 是衍生文件作业的负载结构。
type derivativePayload struct {
	AttachmentID int64  `json:"attachment_id"`
	Type         string `json:"type"`
}

// HandleDerivativeJob 生成图片缩略图或视频封面（jobs.TypeMediaDerivative）。
//
// 幂等与状态机（docs/WORKFLOWS.md 6.1-6.2）：
//   - 衍生行已 ready 且物理文件存在时直接返回；
//   - 附件已失效或衍生行已 canceled 时直接返回，不重试；
//   - 处理前把行置为 processing（原子认领），失败写 last_error 与 attempts 并返回
//     error 交给作业层重试。
func (s *Service) HandleDerivativeJob(ctx context.Context, job jobs.Job) error {
	var payload derivativePayload
	if err := job.PayloadJSON(&payload); err != nil {
		return fmt.Errorf("解析衍生文件作业负载失败: %w", err)
	}
	if payload.AttachmentID <= 0 || !domain.ValidDerivativeType(payload.Type) {
		return errors.New("衍生文件作业负载无效")
	}

	row, err := repo.Get(ctx, s.exec(ctx), payload.AttachmentID)
	if repo.IsNotFound(err) {
		s.log.Warn("衍生文件作业对应的附件不存在，跳过", "attachment_id", payload.AttachmentID)
		return nil
	}
	if err != nil {
		return s.internal("读取附件失败", err)
	}
	if !row.Available() {
		// 附件已逻辑删除：不再生成，也不重试。
		return nil
	}

	derivative, err := repo.GetDerivative(ctx, s.exec(ctx), row.ID, payload.Type)
	if repo.IsNotFound(err) {
		s.log.Warn("衍生文件记录不存在，跳过", "attachment_id", row.ID, "type", payload.Type)
		return nil
	}
	if err != nil {
		return s.internal("读取衍生文件记录失败", err)
	}
	if derivative.Status == model.DerivativeCanceled {
		// 附件或任务已失效，不再处理。
		return nil
	}
	if derivative.Status == model.DerivativeReady && derivative.StorageKey != "" && s.store.Exists(derivative.StorageKey) {
		// 幂等：已经生成且物理文件存在。
		return nil
	}

	claimed, err := repo.ClaimProcessing(ctx, s.exec(ctx), row.ID, payload.Type, s.clk.Now())
	if err != nil {
		return s.internal("更新衍生文件状态失败", err)
	}
	if !claimed {
		// 行已被其他执行置为 ready/canceled（例如清理任务）；不再重复处理。
		if derivative.Status == model.DerivativeReady {
			s.log.Warn("衍生文件记录为 ready 但物理文件缺失，等待人工处理",
				"attachment_id", row.ID, "type", payload.Type)
		}
		return nil
	}

	var key string
	switch payload.Type {
	case model.DerivativeThumbnail:
		key, err = s.generateThumbnail(row)
	case model.DerivativeVideoCover:
		key, err = s.generateVideoCover(ctx, row)
	}
	if err != nil {
		s.markDerivativeFailed(ctx, row.ID, payload.Type, err)
		return err
	}

	// 只更新仍处于 processing 的行：若期间附件被失效清理置为 canceled，不复活该行。
	if _, err := repo.MarkReady(ctx, s.exec(ctx), row.ID, payload.Type, key, s.clk.Now()); err != nil {
		return s.internal("更新衍生文件记录失败", err)
	}
	return nil
}

// markDerivativeFailed 记录失败原因与尝试次数；使用脱离取消的上下文，
// 保证即使作业超时也能写入错误信息。
func (s *Service) markDerivativeFailed(ctx context.Context, attachmentID int64, derivativeType string, cause error) {
	message := domain.TruncateRunes(cause.Error(), domain.DerivativeErrorMaxRunes)
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := repo.MarkFailed(writeCtx, s.exec(writeCtx), attachmentID, derivativeType, message, s.clk.Now()); err != nil {
		s.log.Error("记录衍生文件失败状态出错",
			"attachment_id", attachmentID, "type", derivativeType, "err", err)
		return
	}
	s.log.Warn("衍生文件生成失败", "attachment_id", attachmentID, "type", derivativeType, "err", message)
}
