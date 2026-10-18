package app

// 上传用例：参数校验（登录/停用/AI、用途、大小、配额、限频）、真实存储写入与嗅探、
// 事务内附件/衍生文件/头像回写、作业入队的参数断言。数据库分支由内存协议桩驱动。

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/media/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/format"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// uploadInput 组装一次上传用例输入。
func uploadInput(viewer model.Viewer, filename, purpose string, content []byte) UploadInput {
	return UploadInput{
		Viewer:   viewer,
		Filename: filename,
		Purpose:  purpose,
		Content:  bytes.NewReader(content),
	}
}

// TestUploadRejectsNonWritable 未登录/停用/AI 会话在上传用例层被拒绝，且不触库。
func TestUploadRejectsNonWritable(t *testing.T) {
	env := newMediaTestEnv(t)
	cases := []struct {
		name   string
		viewer model.Viewer
		want   apperr.Kind
	}{
		{"未登录", guestViewer(), apperr.KindUnauthenticated},
		{"已停用", model.Viewer{ID: 7, Authenticated: true, IsSuspended: true}, apperr.KindSuspended},
		{"AI 会话", aiViewer(9), apperr.KindForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := env.svc.Upload(context.Background(), uploadInput(tc.viewer, "a.png", domain.PurposePost, testPNG(t, 4, 4)))
			requireKind(t, err, tc.want)
		})
	}
	if got := len(env.stub.recorded()); got != 0 {
		t.Fatalf("写权限校验失败时不应触库，实际查询 %d 次", got)
	}
}

// TestUploadRequiresJobsQueue 未装配后台作业队列时上传直接失败（启动配置错误）。
func TestUploadRequiresJobsQueue(t *testing.T) {
	env := newMediaTestEnv(t)
	env.svc.jobs = nil
	_, err := env.svc.Upload(context.Background(), uploadInput(humanViewer(7), "a.png", domain.PurposePost, testPNG(t, 4, 4)))
	requireKind(t, err, apperr.KindInternal)
}

// TestUploadRateLimited 限频额度用尽后返回 rate_limited，且不进入存储与事务。
func TestUploadRateLimited(t *testing.T) {
	env := newMediaTestEnv(t)
	env.settings.values.UploadRateLimit = 1
	env.settings.values.UploadRateWindow = time.Hour
	env.expect(sumSizeResult(0))
	if err := env.svc.PrecheckUpload(context.Background(), humanViewer(7)); err != nil {
		t.Fatalf("首次预检应成功: %v", err)
	}
	_, err := env.svc.Upload(context.Background(), uploadInput(humanViewer(7), "a.png", domain.PurposePost, testPNG(t, 4, 4)))
	requireKind(t, err, apperr.KindRateLimited)
	if got := env.ids.next; got != 0 {
		t.Fatalf("限频后不应生成任何 ID，实际 %d", got)
	}
}

// TestPrecheckUploadQuotaAndSettings 覆盖预检阶段的配额已满、数据库错误与设置读取失败回退。
func TestPrecheckUploadQuotaAndSettings(t *testing.T) {
	t.Run("存储配额已满", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.settings.values.StorageQuotaBytes = 1024
		env.expect(sumSizeResult(2048))
		err := env.svc.PrecheckUpload(context.Background(), humanViewer(7))
		if !errors.Is(err, domain.ErrStorageQuotaExhausted) {
			t.Fatalf("期望 ErrStorageQuotaExhausted，实际 %v", err)
		}
	})
	t.Run("统计已用空间失败", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(dbErrorResult("统计失败"))
		err := env.svc.PrecheckUpload(context.Background(), humanViewer(7))
		requireKind(t, err, apperr.KindInternal)
	})
	t.Run("设置读取失败回退默认值", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.settings.err = errors.New("settings 不可用")
		env.expect(sumSizeResult(0))
		if err := env.svc.PrecheckUpload(context.Background(), humanViewer(7)); err != nil {
			t.Fatalf("设置读取失败应回退默认值并通过预检: %v", err)
		}
	})
}

// TestUploadValidationBeforeStorage 覆盖用途、配额与大小校验在落盘前失败的分支。
func TestUploadValidationBeforeStorage(t *testing.T) {
	t.Run("非法用途", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(sumSizeResult(0))
		_, err := env.svc.Upload(context.Background(), uploadInput(humanViewer(7), "a.png", "video", testPNG(t, 4, 4)))
		requireKind(t, err, apperr.KindInvalid)
	})
	t.Run("已用空间超过配额", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.settings.values.StorageQuotaBytes = 100
		env.expect(sumSizeResult(101))
		_, err := env.svc.Upload(context.Background(), uploadInput(humanViewer(7), "a.png", domain.PurposePost, testPNG(t, 4, 4)))
		if !errors.Is(err, domain.ErrStorageQuotaExhausted) {
			t.Fatalf("期望 ErrStorageQuotaExhausted，实际 %v", err)
		}
	})
	t.Run("本次上传将超过配额", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.settings.values.StorageQuotaBytes = 1000
		env.expect(sumSizeResult(900))
		in := uploadInput(humanViewer(7), "a.png", domain.PurposePost, testPNG(t, 4, 4))
		in.SizeHint = 200
		_, err := env.svc.Upload(context.Background(), in)
		if !errors.Is(err, domain.ErrStorageQuotaExceeded) {
			t.Fatalf("期望 ErrStorageQuotaExceeded，实际 %v", err)
		}
	})
	t.Run("文件超过大小限制", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.settings.values.MaxUploadBytes = 8
		env.expect(sumSizeResult(0))
		_, err := env.svc.Upload(context.Background(), uploadInput(humanViewer(7), "a.png", domain.PurposePost, testPNG(t, 4, 4)))
		if !errors.Is(err, domain.ErrContentTooLarge) {
			t.Fatalf("期望 ErrContentTooLarge，实际 %v", err)
		}
	})
	t.Run("不支持的文件类型", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(sumSizeResult(0))
		_, err := env.svc.Upload(context.Background(), uploadInput(humanViewer(7), "evil.html", domain.PurposePost, testHTMLBytes()))
		if !errors.Is(err, domain.ErrUnsupportedMediaType) {
			t.Fatalf("期望 ErrUnsupportedMediaType，实际 %v", err)
		}
	})
	t.Run("空文件", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(sumSizeResult(0))
		_, err := env.svc.Upload(context.Background(), uploadInput(humanViewer(7), "empty.bin", domain.PurposePost, nil))
		if !errors.Is(err, domain.ErrUnsupportedMediaType) {
			t.Fatalf("期望 ErrUnsupportedMediaType，实际 %v", err)
		}
	})
}

// TestUploadImageSuccess 覆盖图片上传全链路：真实嗅探/落盘/尺寸解析、
// 附件与衍生记录写入、衍生作业入队参数。
func TestUploadImageSuccess(t *testing.T) {
	env := newMediaTestEnv(t)
	env.expect(sumSizeResult(0), commandResult("INSERT 0 1"), commandResult("INSERT 0 1"))
	payload := testPNG(t, 32, 16)

	result, err := env.svc.Upload(context.Background(), uploadInput(humanViewer(7), "目录/../照片.png", domain.PurposePost, payload))
	if err != nil {
		t.Fatalf("Upload 返回错误: %v", err)
	}

	if result.Attachment.ID != 101 {
		t.Fatalf("附件 ID = %d，期望 101", result.Attachment.ID)
	}
	if result.Attachment.UploaderID != 7 || result.Attachment.Kind != domain.KindImage {
		t.Fatalf("附件归属/种类 = %+v", result.Attachment)
	}
	if result.Attachment.MediaType != "image/png" {
		t.Fatalf("媒体类型 = %q，期望 image/png", result.Attachment.MediaType)
	}
	if result.Attachment.OriginalFilename != "照片.png" {
		t.Fatalf("原始文件名 = %q，期望去掉路径后的 照片.png", result.Attachment.OriginalFilename)
	}
	if result.Attachment.Width == nil || result.Attachment.Height == nil ||
		*result.Attachment.Width != 32 || *result.Attachment.Height != 16 {
		t.Fatalf("图片尺寸 = %v,%v，期望 32x16", result.Attachment.Width, result.Attachment.Height)
	}
	if !env.store.Exists(result.Attachment.StorageKey) {
		t.Fatalf("附件物理文件未写入存储: %d", result.Attachment.ID)
	}
	if result.Attachment.CreatedAt != testNow || result.Attachment.UpdatedAt != testNow {
		t.Fatalf("附件时间为 %v，期望应用时钟 %v", result.Attachment.CreatedAt, testNow)
	}
	if got := result.Derivatives[model.DerivativeThumbnail]; got != model.DerivativePending {
		t.Fatalf("缩略图初始状态 = %q，期望 pending", got)
	}

	if len(env.jobs.enqueued) != 1 {
		t.Fatalf("衍生作业入队数量 = %d，期望 1", len(env.jobs.enqueued))
	}
	job := env.jobs.enqueued[0]
	if job.Type != contracts.JobTypeMediaDerivative || job.RefType != domain.RefTypeAttachment {
		t.Fatalf("作业类型/引用 = %q/%q", job.Type, job.RefType)
	}
	if job.RefID == nil || *job.RefID != result.Attachment.ID {
		t.Fatalf("作业引用 ID = %v，期望 %d", job.RefID, result.Attachment.ID)
	}
	wantKey := domain.DerivativeJobKey(result.Attachment.ID, model.DerivativeThumbnail)
	if job.IdempotencyKey != wantKey {
		t.Fatalf("幂等键 = %q，期望 %q", job.IdempotencyKey, wantKey)
	}
	if job.At != testNow {
		t.Fatalf("作业入队时间 = %v，期望应用时钟 %v", job.At, testNow)
	}
	payloadMap, ok := job.Payload.(map[string]any)
	if !ok {
		t.Fatalf("作业负载类型 = %T，期望 map[string]any", job.Payload)
	}
	if payloadMap["attachment_id"] != result.Attachment.ID || payloadMap["type"] != model.DerivativeThumbnail {
		t.Fatalf("作业负载 = %#v", payloadMap)
	}
	if len(env.auth.avatarInputs) != 0 {
		t.Fatal("帖子用途上传不应回写头像")
	}
}

// TestUploadAvatarSuccess 覆盖头像用途上传：附件落盘后经 auth 端口回写头像地址。
func TestUploadAvatarSuccess(t *testing.T) {
	env := newMediaTestEnv(t)
	env.expect(sumSizeResult(0), commandResult("INSERT 0 1"), commandResult("INSERT 0 1"))
	result, err := env.svc.Upload(context.Background(), uploadInput(humanViewer(7), "avatar.png", domain.PurposeAvatar, testPNG(t, 8, 8)))
	if err != nil {
		t.Fatalf("Upload 返回错误: %v", err)
	}
	if len(env.auth.avatarInputs) != 1 {
		t.Fatalf("头像回写次数 = %d，期望 1", len(env.auth.avatarInputs))
	}
	input := env.auth.avatarInputs[0]
	if input.UserID != 7 || input.AttachmentID != result.Attachment.ID {
		t.Fatalf("头像回写参数 = %+v", input)
	}
	if input.AvatarURL != format.AttachmentContentURL(result.Attachment.ID) {
		t.Fatalf("头像地址 = %q，期望 %q", input.AvatarURL, format.AttachmentContentURL(result.Attachment.ID))
	}
	if input.At != testNow {
		t.Fatalf("头像回写时间 = %v，期望应用时钟 %v", input.At, testNow)
	}
}

// TestUploadNonDecodableImage 可存储但标准库无法解码的图片直接记为 canceled，不投递注定失败的作业。
func TestUploadNonDecodableImage(t *testing.T) {
	env := newMediaTestEnv(t)
	env.expect(sumSizeResult(0), commandResult("INSERT 0 1"), commandResult("INSERT 0 1"))
	result, err := env.svc.Upload(context.Background(), uploadInput(humanViewer(7), "image.webp", domain.PurposePost, testWebPBytes()))
	if err != nil {
		t.Fatalf("Upload 返回错误: %v", err)
	}
	if result.Attachment.MediaType != "image/webp" || result.Attachment.Kind != domain.KindImage {
		t.Fatalf("嗅探结果 = %q/%q，期望 image/image/webp", result.Attachment.Kind, result.Attachment.MediaType)
	}
	if result.Attachment.Width != nil || result.Attachment.Height != nil {
		t.Fatalf("无法解码的图片不应有尺寸，实际 %v,%v", result.Attachment.Width, result.Attachment.Height)
	}
	if got := result.Derivatives[model.DerivativeThumbnail]; got != model.DerivativeCanceled {
		t.Fatalf("缩略图状态 = %q，期望 canceled", got)
	}
	if len(env.jobs.enqueued) != 0 {
		t.Fatalf("canceled 的缩略图不应入队，实际 %d 条", len(env.jobs.enqueued))
	}
}

// TestUploadVideoAndText 覆盖视频（生成封面作业）与普通文件（无衍生文件）两条路径。
func TestUploadVideoAndText(t *testing.T) {
	t.Run("视频生成封面作业", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(sumSizeResult(0), commandResult("INSERT 0 1"), commandResult("INSERT 0 1"))
		result, err := env.svc.Upload(context.Background(), uploadInput(humanViewer(7), "clip.mp4", domain.PurposePost, testMP4Bytes()))
		if err != nil {
			t.Fatalf("Upload 返回错误: %v", err)
		}
		if result.Attachment.Kind != domain.KindVideo || result.Attachment.MediaType != "video/mp4" {
			t.Fatalf("嗅探结果 = %q/%q", result.Attachment.Kind, result.Attachment.MediaType)
		}
		if got := result.Derivatives[model.DerivativeVideoCover]; got != model.DerivativePending {
			t.Fatalf("视频封面状态 = %q，期望 pending", got)
		}
		if len(env.jobs.enqueued) != 1 {
			t.Fatalf("视频上传应入队 1 条封面作业，实际 %d", len(env.jobs.enqueued))
		}
		if env.jobs.enqueued[0].IdempotencyKey != domain.DerivativeJobKey(101, model.DerivativeVideoCover) {
			t.Fatalf("作业幂等键 = %q", env.jobs.enqueued[0].IdempotencyKey)
		}
	})
	t.Run("普通文件无衍生文件", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(sumSizeResult(0), commandResult("INSERT 0 1"))
		result, err := env.svc.Upload(context.Background(), uploadInput(humanViewer(7), "notes.txt", domain.PurposePost, testTextBytes()))
		if err != nil {
			t.Fatalf("Upload 返回错误: %v", err)
		}
		if result.Attachment.Kind != domain.KindFile || result.Attachment.MediaType != "text/plain; charset=utf-8" {
			t.Fatalf("嗅探结果 = %q/%q", result.Attachment.Kind, result.Attachment.MediaType)
		}
		if len(result.Derivatives) != 0 || len(env.jobs.enqueued) != 0 {
			t.Fatalf("普通文件不应产生衍生文件: %+v/%d", result.Derivatives, len(env.jobs.enqueued))
		}
	})
}

// TestUploadPrecheckedSkipsLimiter 已预检（http 层）时不再重复消耗上传限频额度。
func TestUploadPrecheckedSkipsLimiter(t *testing.T) {
	env := newMediaTestEnv(t)
	env.settings.values.UploadRateLimit = 1
	env.settings.values.UploadRateWindow = time.Hour
	// 手动消耗唯一额度，模拟 http 层已经完成 PrecheckUpload。
	allowed, _ := env.svc.uploadLimiter.ConsumeLimit("user:7", 1, time.Hour)
	if !allowed {
		t.Fatal("首个额度应消耗成功")
	}
	env.expect(sumSizeResult(0), commandResult("INSERT 0 1"))
	in := uploadInput(humanViewer(7), "notes.txt", domain.PurposePost, testTextBytes())
	in.Prechecked = true
	if _, err := env.svc.Upload(context.Background(), in); err != nil {
		t.Fatalf("预检后上传不应再次限频，实际 %v", err)
	}
}

// TestUploadDatabaseAndPortFailures 覆盖事务内各写入/入队/回写失败时的错误映射。
func TestUploadDatabaseAndPortFailures(t *testing.T) {
	t.Run("统计已用空间失败", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(dbErrorResult("sum 失败"))
		_, err := env.svc.Upload(context.Background(), uploadInput(humanViewer(7), "notes.txt", domain.PurposePost, testTextBytes()))
		requireKind(t, err, apperr.KindInternal)
	})
	t.Run("附件记录写入失败", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(sumSizeResult(0), dbErrorResult("insert 失败"))
		_, err := env.svc.Upload(context.Background(), uploadInput(humanViewer(7), "notes.txt", domain.PurposePost, testTextBytes()))
		requireKind(t, err, apperr.KindInternal)
	})
	t.Run("衍生记录写入失败", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(sumSizeResult(0), commandResult("INSERT 0 1"), dbErrorResult("derivative 失败"))
		_, err := env.svc.Upload(context.Background(), uploadInput(humanViewer(7), "a.png", domain.PurposePost, testPNG(t, 4, 4)))
		requireKind(t, err, apperr.KindInternal)
	})
	t.Run("作业入队失败", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.jobs.enqueueErr = errors.New("jobs 不可用")
		env.expect(sumSizeResult(0), commandResult("INSERT 0 1"), commandResult("INSERT 0 1"))
		_, err := env.svc.Upload(context.Background(), uploadInput(humanViewer(7), "a.png", domain.PurposePost, testPNG(t, 4, 4)))
		requireKind(t, err, apperr.KindInternal)
	})
	t.Run("头像回写失败", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.auth.setAvatarErr = errors.New("auth 不可用")
		env.expect(sumSizeResult(0), commandResult("INSERT 0 1"), commandResult("INSERT 0 1"))
		_, err := env.svc.Upload(context.Background(), uploadInput(humanViewer(7), "a.png", domain.PurposeAvatar, testPNG(t, 4, 4)))
		requireKind(t, err, apperr.KindInternal)
	})
	t.Run("ID 生成失败", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.ids.err = errors.New("id 耗尽")
		env.expect(sumSizeResult(0))
		_, err := env.svc.Upload(context.Background(), uploadInput(humanViewer(7), "notes.txt", domain.PurposePost, testTextBytes()))
		requireKind(t, err, apperr.KindInternal)
	})
}

// TestImageDimensionsRejectsNonImage 非图片内容返回空尺寸而不是 panic（补 generate_test 边界）。
func TestImageDimensionsRejectsNonImage(t *testing.T) {
	env := newMediaTestEnv(t)
	key := env.saveBytes(testTextBytes())
	if width, height := env.svc.imageDimensions(key); width != nil || height != nil {
		t.Fatalf("非图片内容应返回空尺寸，实际 %v,%v", width, height)
	}
}

// TestUploadLimits 校验 http 层读取请求体前使用的上限来自系统设置。
func TestUploadLimits(t *testing.T) {
	env := newMediaTestEnv(t)
	env.settings.values.MaxRequestBytes = 12345
	env.settings.values.MaxUploadBytes = 6789
	maxRequestBytes, maxUploadBytes := env.svc.UploadLimits(context.Background())
	if maxRequestBytes != 12345 || maxUploadBytes != 6789 {
		t.Fatalf("上传上限 = %d/%d，期望 12345/6789", maxRequestBytes, maxUploadBytes)
	}
}
