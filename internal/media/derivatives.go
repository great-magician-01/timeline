package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/great-magician-01/timeline/internal/access"
	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/jobs"
	"github.com/great-magician-01/timeline/internal/model"
	"github.com/great-magician-01/timeline/internal/storage"
)

const (
	// refTypeAttachment 是 background_jobs.ref_type 的附件引用类型。
	refTypeAttachment = "attachment"

	// thumbnailMaxEdge 是缩略图最长边（不放大）。
	thumbnailMaxEdge = 512
	// jpegQuality 是缩略图与视频封面的编码质量。
	jpegQuality = 82

	// videoCoverMaxWidth 是视频封面最大宽度，高度按比例取偶数。
	videoCoverMaxWidth = 640
	// ffmpegTimeout 限制单次封面抽取时间，避免异常视频占满作业时间。
	ffmpegTimeout = 60 * time.Second

	// derivativeErrorMax 是 last_error 的截断长度（字符）。
	derivativeErrorMax = 500

	// videoCoverFailedMessage 是 ffmpeg 不可用或抽取失败时记录的对外可见原因。
	videoCoverFailedMessage = "未安装 ffmpeg 或封面提取失败"
)

// derivativePayload 是衍生文件作业的负载结构。
type derivativePayload struct {
	AttachmentID int64  `json:"attachment_id"`
	Type         string `json:"type"`
}

// derivativeJobKey 是衍生文件作业的幂等键，保证重试与并发不会重复生成。
func derivativeJobKey(attachmentID int64, derivativeType string) string {
	return "media_derivative:" + api.FormatID(attachmentID) + ":" + derivativeType
}

// derivativeTypeForKind 把附件种类映射为需要生成的衍生文件类型；无需衍生文件时返回空串。
func derivativeTypeForKind(kind string) string {
	switch kind {
	case storage.KindImage:
		return model.DerivativeThumbnail
	case storage.KindVideo:
		return model.DerivativeVideoCover
	default:
		return ""
	}
}

// decodableImageType 判断图片是否由本进程的解码器支持（标准库仅 jpeg/png/gif）。
// storage 还允许 webp/bmp/ico/tiff，这些类型无法生成缩略图：直接标记为 canceled，
// 避免每次都投递一个注定失败、还要重试 3 次的作业；前端按 7.1.6 回退原图。
func decodableImageType(mediaType string) bool {
	switch mediaType {
	case "image/jpeg", "image/png", "image/gif":
		return true
	default:
		return false
	}
}

// HandleDerivativeJob 生成图片缩略图或视频封面（jobs.TypeMediaDerivative）。
//
// 幂等与状态机（docs/WORKFLOWS.md 6.1-6.2）：
//   - 衍生行已 ready 且物理文件存在时直接返回；
//   - 附件已失效或衍生行已 canceled 时直接返回，不重试；
//   - 处理前把行置为 processing（原子认领），失败写 last_error 与 attempts 并返回 error 交给作业层重试。
func (s *Service) HandleDerivativeJob(ctx context.Context, job jobs.Job) error {
	var payload derivativePayload
	if err := job.PayloadJSON(&payload); err != nil {
		return fmt.Errorf("解析衍生文件作业负载失败: %w", err)
	}
	if payload.AttachmentID <= 0 ||
		(payload.Type != model.DerivativeThumbnail && payload.Type != model.DerivativeVideoCover) {
		return errors.New("衍生文件作业负载无效")
	}

	pool := s.pools.Pool()
	if pool == nil {
		return errors.New("数据库尚未就绪")
	}

	att, err := s.AttachmentByID(ctx, pool, payload.AttachmentID)
	if err != nil {
		var failure *api.Failure
		if errors.As(err, &failure) && failure.Code == "not_found" {
			s.log.Warn("衍生文件作业对应的附件不存在，跳过", "attachment_id", payload.AttachmentID)
			return nil
		}
		return err
	}
	if !att.Available() {
		// 附件已逻辑删除：不再生成，也不重试。
		return nil
	}

	var status, existingKey string
	err = pool.QueryRow(ctx, `
		SELECT status, storage_key FROM attachment_derivatives
		WHERE attachment_id = $1 AND derivative_type = $2`,
		att.ID, payload.Type,
	).Scan(&status, &existingKey)
	if errors.Is(err, pgx.ErrNoRows) {
		s.log.Warn("衍生文件记录不存在，跳过", "attachment_id", att.ID, "type", payload.Type)
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取衍生文件记录失败: %w", err)
	}
	if status == model.DerivativeCanceled {
		// 附件或任务已失效，不再处理。
		return nil
	}
	if status == model.DerivativeReady && existingKey != "" && s.store.Exists(existingKey) {
		// 幂等：已经生成且物理文件存在。
		return nil
	}

	// 原子认领：只有仍处于 pending/failed/processing 的行可以进入处理。
	tag, err := pool.Exec(ctx, `
		UPDATE attachment_derivatives
		SET status = 'processing', updated_at = now()
		WHERE attachment_id = $1 AND derivative_type = $2
		  AND status IN ('pending', 'failed', 'processing')`,
		att.ID, payload.Type,
	)
	if err != nil {
		return fmt.Errorf("更新衍生文件状态失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// 行已被其他执行置为 ready/canceled（例如清理任务）；不再重复处理。
		if status == model.DerivativeReady {
			s.log.Warn("衍生文件记录为 ready 但物理文件缺失，等待人工处理",
				"attachment_id", att.ID, "type", payload.Type)
		}
		return nil
	}

	var key string
	switch payload.Type {
	case model.DerivativeThumbnail:
		key, err = s.generateThumbnail(att)
	case model.DerivativeVideoCover:
		key, err = s.generateVideoCover(ctx, att)
	}
	if err != nil {
		s.markDerivativeFailed(ctx, pool, att.ID, payload.Type, err)
		return err
	}

	// 只更新仍处于 processing 的行：若期间附件被失效清理置为 canceled，不复活该行。
	if _, err := pool.Exec(ctx, `
		UPDATE attachment_derivatives
		SET storage_key = $3, status = 'ready', last_error = '', updated_at = now()
		WHERE attachment_id = $1 AND derivative_type = $2 AND status = 'processing'`,
		att.ID, payload.Type, key,
	); err != nil {
		return fmt.Errorf("更新衍生文件记录失败: %w", err)
	}
	return nil
}

// generateThumbnail 生成图片缩略图：最长边不超过 512px，编码为 JPEG 质量 82。
// 原文件保持不动，衍生文件另存新键。
func (s *Service) generateThumbnail(att model.Attachment) (string, error) {
	if att.Kind != storage.KindImage {
		return "", fmt.Errorf("附件类型 %s 不支持生成缩略图", att.Kind)
	}
	file, err := s.store.Open(att.StorageKey)
	if err != nil {
		return "", fmt.Errorf("读取原图失败: %w", err)
	}
	defer file.Close()

	source, _, err := image.Decode(file)
	if err != nil {
		return "", fmt.Errorf("解析图片失败: %w", err)
	}
	scaled, err := scaleBilinear(source, thumbnailMaxEdge)
	if err != nil {
		return "", fmt.Errorf("缩放图片失败: %w", err)
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, scaled, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return "", fmt.Errorf("编码缩略图失败: %w", err)
	}
	key, _, _, err := s.store.Save(bytes.NewReader(encoded.Bytes()), storage.DefaultMaxUploadBytes, time.Now())
	if err != nil {
		return "", fmt.Errorf("保存缩略图失败: %w", err)
	}
	return key, nil
}

// generateVideoCover 用 ffmpeg 抽取视频首帧作为封面。
// 未安装 ffmpeg 或抽取失败时返回错误，由作业层重试并在超过上限后置为 failed（前端显示占位）。
func (s *Service) generateVideoCover(ctx context.Context, att model.Attachment) (string, error) {
	if att.Kind != storage.KindVideo {
		return "", fmt.Errorf("附件类型 %s 不支持抽取视频封面", att.Kind)
	}
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return "", errors.New(videoCoverFailedMessage)
	}
	if !s.store.Exists(att.StorageKey) {
		return "", errors.New(videoCoverFailedMessage)
	}
	source := filepath.Join(s.store.Root(), filepath.FromSlash(att.StorageKey))

	temp, err := os.CreateTemp("", "timeline-cover-*.jpg")
	if err != nil {
		return "", fmt.Errorf("创建封面临时文件失败: %w", err)
	}
	tempPath := temp.Name()
	_ = temp.Close()
	// 本模块坚持“不删除任何附件与衍生文件”，唯一例外是这个由本进程刚生成、
	// 不属于持久化存储卷的 ffmpeg 临时输出文件：读取完成后必须清理，避免累积垃圾。
	defer func() { _ = os.Remove(tempPath) }()

	runCtx, cancel := context.WithTimeout(ctx, ffmpegTimeout)
	defer cancel()
	// 直接使用 exec（不经 shell），因此缩放表达式不带外层引号。
	// scale=min(640,iw):-2 保证封面宽度不超过 640 且高度为偶数。
	command := exec.CommandContext(runCtx, ffmpegPath,
		"-y", "-ss", "0",
		"-i", source,
		"-frames:v", "1",
		"-vf", fmt.Sprintf("scale=min(%d,iw):-2", videoCoverMaxWidth),
		"-f", "image2", tempPath,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		s.log.Warn("视频封面抽取失败",
			"attachment_id", att.ID, "err", err,
			"output", truncateRunes(string(output), derivativeErrorMax))
		return "", errors.New(videoCoverFailedMessage)
	}
	data, err := os.ReadFile(tempPath)
	if err != nil || len(data) == 0 {
		s.log.Warn("读取视频封面临时文件失败", "attachment_id", att.ID, "err", err)
		return "", errors.New(videoCoverFailedMessage)
	}
	key, _, _, err := s.store.Save(bytes.NewReader(data), storage.DefaultMaxUploadBytes, time.Now())
	if err != nil {
		return "", fmt.Errorf("保存视频封面失败: %w", err)
	}
	return key, nil
}

// markDerivativeFailed 记录失败原因与尝试次数；只影响仍处于 processing 的行。
// 使用脱离取消的上下文，保证即使作业超时也能写入错误信息。
func (s *Service) markDerivativeFailed(ctx context.Context, q access.Querier, attachmentID int64, derivativeType string, cause error) {
	message := truncateRunes(cause.Error(), derivativeErrorMax)
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := q.Exec(writeCtx, `
		UPDATE attachment_derivatives
		SET status = 'failed', last_error = $3, attempts = attempts + 1, updated_at = now()
		WHERE attachment_id = $1 AND derivative_type = $2 AND status = 'processing'`,
		attachmentID, derivativeType, message,
	); err != nil {
		s.log.Error("记录衍生文件失败状态出错",
			"attachment_id", attachmentID, "type", derivativeType, "err", err)
		return
	}
	s.log.Warn("衍生文件生成失败", "attachment_id", attachmentID, "type", derivativeType, "err", message)
}

// truncateRunes 按字符（而非字节）截断文本，避免截断 UTF-8 字符。
func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
