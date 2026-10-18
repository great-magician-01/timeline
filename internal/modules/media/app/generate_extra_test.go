package app

// 缩略图生成的错误分支（原文件缺失、内容无法解码）。成功流水线与缩放纯函数
// 已在既有 generate_test.go/imaging_test.go 覆盖，这里不重复。

import (
	"strings"
	"testing"

	"github.com/great-magician-01/timeline/internal/modules/media/domain"
	"github.com/great-magician-01/timeline/internal/modules/media/repo"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// TestGenerateThumbnailErrors 原图缺失或内容不是图片时返回可读错误，不写衍生文件。
func TestGenerateThumbnailErrors(t *testing.T) {
	env := newMediaTestEnv(t)

	t.Run("原文件缺失", func(t *testing.T) {
		_, err := env.svc.generateThumbnail(repo.AttachmentRow{Attachment: model.Attachment{
			ID: 1, Kind: domain.KindImage, StorageKey: testStorageKey(1),
		}})
		if err == nil || !strings.Contains(err.Error(), "读取原图失败") {
			t.Fatalf("期望读取原图失败，实际 %v", err)
		}
	})
	t.Run("内容无法解码", func(t *testing.T) {
		key := env.saveBytes(testTextBytes())
		_, err := env.svc.generateThumbnail(repo.AttachmentRow{Attachment: model.Attachment{
			ID: 2, Kind: domain.KindImage, StorageKey: key,
		}})
		if err == nil || !strings.Contains(err.Error(), "解析图片失败") {
			t.Fatalf("期望解析图片失败，实际 %v", err)
		}
	})
}
