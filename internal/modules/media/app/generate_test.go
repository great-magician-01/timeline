package app

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/media/domain"
	"github.com/great-magician-01/timeline/internal/modules/media/repo"
	"github.com/great-magician-01/timeline/internal/platform/storage"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// fixedClock 是仅用于生成的假时钟。
type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at }

// TestGenerateThumbnail 验证缩略图流水线（解码 PNG、缩放、编码 JPEG、另存衍生文件），
// 不需要数据库，只使用临时存储目录。
func TestGenerateThumbnail(t *testing.T) {
	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatalf("创建存储失败: %v", err)
	}
	at := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	source := image.NewNRGBA(image.Rect(0, 0, 800, 400))
	for y := 0; y < 400; y++ {
		for x := 0; x < 800; x++ {
			source.SetNRGBA(x, y, color.NRGBA{R: 200, G: 100, B: 50, A: 200})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatalf("编码测试图片失败: %v", err)
	}
	key, _, mediaType, err := store.Save(bytes.NewReader(encoded.Bytes()), storage.DefaultMaxUploadBytes, at)
	if err != nil {
		t.Fatalf("保存测试图片失败: %v", err)
	}
	if mediaType != "image/png" {
		t.Fatalf("媒体类型 = %q，期望 image/png", mediaType)
	}

	service := &Service{store: store, log: slog.New(slog.NewTextHandler(io.Discard, nil)), clk: fixedClock{at: at}}
	thumbnailKey, err := service.generateThumbnail(repo.AttachmentRow{Attachment: model.Attachment{
		ID:         1,
		Kind:       domain.KindImage,
		StorageKey: key,
	}})
	if err != nil {
		t.Fatalf("生成缩略图失败: %v", err)
	}
	if thumbnailKey == key {
		t.Fatal("缩略图必须是新的存储键，不能覆盖原文件")
	}
	if !store.Exists(key) {
		t.Fatal("原文件必须保留")
	}

	file, err := store.Open(thumbnailKey)
	if err != nil {
		t.Fatalf("打开缩略图失败: %v", err)
	}
	defer file.Close()
	config, format, err := image.DecodeConfig(file)
	if err != nil {
		t.Fatalf("解析缩略图失败: %v", err)
	}
	if format != "jpeg" {
		t.Fatalf("缩略图格式 = %q，期望 jpeg", format)
	}
	if config.Width != 512 || config.Height != 256 {
		t.Fatalf("缩略图尺寸 = %dx%d，期望 512x256", config.Width, config.Height)
	}

	// 非图片附件无法生成缩略图，应当返回错误而不 panic。
	if _, err := service.generateThumbnail(repo.AttachmentRow{Attachment: model.Attachment{
		ID:         2,
		Kind:       domain.KindFile,
		StorageKey: key,
	}}); err == nil {
		t.Fatal("文件类附件不应生成缩略图")
	}
}

// TestImageDimensions 解析附件宽高：必须返回真实尺寸并正常返回（防回归：
// 历史 bug 曾让 imageDimensions 递归调用自身，导致上传请求栈溢出挂起）。
func TestImageDimensions(t *testing.T) {
	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatalf("创建存储失败: %v", err)
	}
	at := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	source := image.NewNRGBA(image.Rect(0, 0, 20, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 20; x++ {
			source.SetNRGBA(x, y, color.NRGBA{R: 10, G: 20, B: 30, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatalf("编码测试图片失败: %v", err)
	}
	key, _, _, err := store.Save(bytes.NewReader(encoded.Bytes()), storage.DefaultMaxUploadBytes, at)
	if err != nil {
		t.Fatalf("保存测试图片失败: %v", err)
	}
	service := &Service{store: store, log: slog.New(slog.NewTextHandler(io.Discard, nil)), clk: fixedClock{at: at}}

	width, height := service.imageDimensions(key)
	if width == nil || height == nil {
		t.Fatalf("图片尺寸不应为空: %v,%v", width, height)
	}
	if *width != 20 || *height != 10 {
		t.Fatalf("图片尺寸 = %dx%d，期望 20x10", *width, *height)
	}

	// 不存在/非图片内容返回空值而不是 panic。
	if w, h := service.imageDimensions("2026/10/18/00000000000000000000000000000000.bin"); w != nil || h != nil {
		t.Fatalf("非法存储键应返回空尺寸，实际 %v,%v", w, h)
	}
}
