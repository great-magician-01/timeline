package app

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

	"github.com/great-magician-01/timeline/internal/modules/media/domain"
	"github.com/great-magician-01/timeline/internal/modules/media/repo"
	"github.com/great-magician-01/timeline/internal/platform/storage"
)

const (
	// videoCoverMaxWidth 是视频封面最大宽度，高度按比例取偶数。
	videoCoverMaxWidth = domain.VideoCoverMaxWidth
	// ffmpegTimeout 限制单次封面抽取时间，避免异常视频占满作业时间。
	ffmpegTimeout = 60 * time.Second
	// videoCoverFailedMessage 是 ffmpeg 不可用或抽取失败时记录的对外可见原因。
	videoCoverFailedMessage = "未安装 ffmpeg 或封面提取失败"
)

// generateThumbnail 生成图片缩略图：最长边不超过 512px，编码为 JPEG 质量 82。
// 原文件保持不动，衍生文件另存新键。
func (s *Service) generateThumbnail(attachment repo.AttachmentRow) (string, error) {
	if attachment.Kind != domain.KindImage {
		return "", fmt.Errorf("附件类型 %s 不支持生成缩略图", attachment.Kind)
	}
	file, err := s.store.Open(attachment.StorageKey)
	if err != nil {
		return "", fmt.Errorf("读取原图失败: %w", err)
	}
	defer file.Close()

	source, _, err := image.Decode(file)
	if err != nil {
		return "", fmt.Errorf("解析图片失败: %w", err)
	}
	scaled, err := scaleBilinear(source, domain.ThumbnailMaxEdge)
	if err != nil {
		return "", fmt.Errorf("缩放图片失败: %w", err)
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, scaled, &jpeg.Options{Quality: domain.JPEGQuality}); err != nil {
		return "", fmt.Errorf("编码缩略图失败: %w", err)
	}
	key, _, _, err := s.store.Save(bytes.NewReader(encoded.Bytes()), storage.DefaultMaxUploadBytes, s.clk.Now())
	if err != nil {
		return "", fmt.Errorf("保存缩略图失败: %w", err)
	}
	return key, nil
}

// generateVideoCover 用 ffmpeg 抽取视频首帧作为封面。
// 未安装 ffmpeg 或抽取失败时返回错误，由作业层重试并在超过上限后置为 failed（前端显示占位）。
func (s *Service) generateVideoCover(ctx context.Context, attachment repo.AttachmentRow) (string, error) {
	if attachment.Kind != domain.KindVideo {
		return "", fmt.Errorf("附件类型 %s 不支持抽取视频封面", attachment.Kind)
	}
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return "", errors.New(videoCoverFailedMessage)
	}
	if !s.store.Exists(attachment.StorageKey) {
		return "", errors.New(videoCoverFailedMessage)
	}
	source := filepath.Join(s.store.Root(), filepath.FromSlash(attachment.StorageKey))

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
			"attachment_id", attachment.ID, "err", err,
			"output", domain.TruncateRunes(string(output), domain.DerivativeErrorMaxRunes))
		return "", errors.New(videoCoverFailedMessage)
	}
	data, err := os.ReadFile(tempPath)
	if err != nil || len(data) == 0 {
		s.log.Warn("读取视频封面临时文件失败", "attachment_id", attachment.ID, "err", err)
		return "", errors.New(videoCoverFailedMessage)
	}
	key, _, _, err := s.store.Save(bytes.NewReader(data), storage.DefaultMaxUploadBytes, s.clk.Now())
	if err != nil {
		return "", fmt.Errorf("保存视频封面失败: %w", err)
	}
	return key, nil
}
