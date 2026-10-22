package app

// 上传用例：参数校验（登录/停用/AI、用途、大小、配额、限频）、真实存储写入与嗅探、
// 事务内附件/配额计数/衍生文件/头像回写、作业入队的参数断言。数据库分支由内存协议桩驱动。
//
// 查询顺序约定（协议桩按顺序消费，多出/缺少结果都会失败）：
//   - PrecheckUpload 或 Upload 自补预检：attachment_usage 点查 1 条；
//   - 事务内：INSERT 附件行 → INSERT/UPDATE attachment_usage → 衍生文件行（如有）；
//   - 带 Precheck 快照的 Upload 不再读取设置与用量，只排队事务内写入。

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

// testUploadPrecheck 返回一份不限制配额的上传预检快照（供直接调用 Upload 的用例复用）。
func testUploadPrecheck(usedBytes int64) *UploadPrecheck {
	values := testSystemSettings()
	return &UploadPrecheck{
		MaxRequestBytes:   values.MaxRequestBytes,
		MaxUploadBytes:    values.MaxUploadBytes,
		StorageQuotaBytes: values.StorageQuotaBytes,
		UsedBytes:         usedBytes,
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
	env.expect(usageResult(0))
	if _, err := env.svc.PrecheckUpload(context.Background(), humanViewer(7)); err != nil {
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
		env.expect(usageResult(2048))
		_, err := env.svc.PrecheckUpload(context.Background(), humanViewer(7))
		if !errors.Is(err, domain.ErrStorageQuotaExhausted) {
			t.Fatalf("期望 ErrStorageQuotaExhausted，实际 %v", err)
		}
	})
	t.Run("读取已用空间失败", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(dbErrorResult("usage 查询失败"))
		_, err := env.svc.PrecheckUpload(context.Background(), humanViewer(7))
		requireKind(t, err, apperr.KindInternal)
	})
	t.Run("设置读取失败回退默认值", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.settings.err = errors.New("settings 不可用")
		env.expect(usageResult(0))
		if _, err := env.svc.PrecheckUpload(context.Background(), humanViewer(7)); err != nil {
			t.Fatalf("设置读取失败应回退默认值并通过预检: %v", err)
		}
	})
}

// TestPrecheckUploadReturnsSnapshot 预检把设置上限与已用字节作为快照返回，
// 供 http 层设置 MaxBytesReader 并原样传给 Upload，避免同一请求重复读取。
func TestPrecheckUploadReturnsSnapshot(t *testing.T) {
	env := newMediaTestEnv(t)
	env.settings.values.MaxRequestBytes = 12345
	env.settings.values.MaxUploadBytes = 6789
	env.settings.values.StorageQuotaBytes = 1 << 20
	env.expect(usageResult(321))

	snapshot, err := env.svc.PrecheckUpload(context.Background(), humanViewer(7))
	if err != nil {
		t.Fatalf("PrecheckUpload: %v", err)
	}
	if snapshot.MaxRequestBytes != 12345 || snapshot.MaxUploadBytes != 6789 ||
		snapshot.StorageQuotaBytes != 1<<20 || snapshot.UsedBytes != 321 {
		t.Fatalf("预检快照不符: %+v", snapshot)
	}
}

// TestUploadValidationBeforeStorage 覆盖用途、配额与大小校验在落盘前失败的分支。
func TestUploadValidationBeforeStorage(t *testing.T) {
	t.Run("非法用途", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(usageResult(0))
		_, err := env.svc.Upload(context.Background(), uploadInput(humanViewer(7), "a.png", "video", testPNG(t, 4, 4)))
		requireKind(t, err, apperr.KindInvalid)
	})
	t.Run("已用空间超过配额", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.settings.values.StorageQuotaBytes = 100
		env.expect(usageResult(101))
		_, err := env.svc.Upload(context.Background(), uploadInput(humanViewer(7), "a.png", domain.PurposePost, testPNG(t, 4, 4)))
		if !errors.Is(err, domain.ErrStorageQuotaExhausted) {
			t.Fatalf("期望 ErrStorageQuotaExhausted，实际 %v", err)
		}
	})
	t.Run("本次上传将超过配额", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.settings.values.StorageQuotaBytes = 1000
		env.expect(usageResult(900))
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
		env.expect(usageResult(0))
		_, err := env.svc.Upload(context.Background(), uploadInput(humanViewer(7), "a.png", domain.PurposePost, testPNG(t, 4, 4)))
		if !errors.Is(err, domain.ErrContentTooLarge) {
			t.Fatalf("期望 ErrContentTooLarge，实际 %v", err)
		}
	})
	t.Run("不支持的文件类型", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(usageResult(0))
		_, err := env.svc.Upload(context.Background(), uploadInput(humanViewer(7), "evil.html", domain.PurposePost, testHTMLBytes()))
		if !errors.Is(err, domain.ErrUnsupportedMediaType) {
			t.Fatalf("期望 ErrUnsupportedMediaType，实际 %v", err)
		}
	})
	t.Run("空文件", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(usageResult(0))
		_, err := env.svc.Upload(context.Background(), uploadInput(humanViewer(7), "empty.bin", domain.PurposePost, nil))
		if !errors.Is(err, domain.ErrUnsupportedMediaType) {
			t.Fatalf("期望 ErrUnsupportedMediaType，实际 %v", err)
		}
	})
}

// TestUploadImageSuccess 覆盖图片上传全链路：真实嗅探/落盘/尺寸解析、
// 附件与配额计数及衍生记录写入、衍生作业入队参数。
func TestUploadImageSuccess(t *testing.T) {
	env := newMediaTestEnv(t)
	env.expect(usageResult(0), commandResult("INSERT 0 1"), commandResult("INSERT 0 1"), commandResult("INSERT 0 1"))
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
	env.expect(usageResult(0), commandResult("INSERT 0 1"), commandResult("INSERT 0 1"), commandResult("INSERT 0 1"))
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

// TestUploadBannerSuccess 覆盖横幅用途上传：附件落盘后经 auth 端口回写横幅地址。
func TestUploadBannerSuccess(t *testing.T) {
	env := newMediaTestEnv(t)
	env.expect(usageResult(0), commandResult("INSERT 0 1"), commandResult("INSERT 0 1"), commandResult("INSERT 0 1"))
	result, err := env.svc.Upload(context.Background(), uploadInput(humanViewer(7), "banner.png", domain.PurposeBanner, testPNG(t, 32, 8)))
	if err != nil {
		t.Fatalf("Upload 返回错误: %v", err)
	}
	if len(env.auth.bannerInputs) != 1 {
		t.Fatalf("横幅回写次数 = %d，期望 1", len(env.auth.bannerInputs))
	}
	input := env.auth.bannerInputs[0]
	if input.UserID != 7 || input.AttachmentID == nil || *input.AttachmentID != result.Attachment.ID {
		t.Fatalf("横幅回写参数 = %+v", input)
	}
	if input.BannerURL != format.AttachmentContentURL(result.Attachment.ID) {
		t.Fatalf("横幅地址 = %q，期望 %q", input.BannerURL, format.AttachmentContentURL(result.Attachment.ID))
	}
	if input.At != testNow {
		t.Fatalf("横幅回写时间 = %v，期望应用时钟 %v", input.At, testNow)
	}
}

// TestUploadBannerRejectsNonImage 横幅必须为图片：非图片种类在落盘前直接拒绝，不触库。
func TestUploadBannerRejectsNonImage(t *testing.T) {
	env := newMediaTestEnv(t)
	env.expect(usageResult(0))
	_, err := env.svc.Upload(context.Background(), uploadInput(humanViewer(7), "clip.mp4", domain.PurposeBanner, testMP4Bytes()))
	if !apperr.IsKind(err, apperr.KindInvalid) {
		t.Fatalf("非图片横幅应返回 Invalid，实际: %v", err)
	}
	if got := env.stub.pending(); got != 0 {
		t.Fatalf("拒绝后不应继续触库，剩余 %d", got)
	}
	if len(env.auth.bannerInputs) != 0 {
		t.Fatal("被拒绝的上传不应回写横幅")
	}
}

// TestUploadNonDecodableImage 可存储但标准库无法解码的图片直接记为 canceled，不投递注定失败的作业。
func TestUploadNonDecodableImage(t *testing.T) {
	env := newMediaTestEnv(t)
	env.expect(usageResult(0), commandResult("INSERT 0 1"), commandResult("INSERT 0 1"), commandResult("INSERT 0 1"))
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
		env.expect(usageResult(0), commandResult("INSERT 0 1"), commandResult("INSERT 0 1"), commandResult("INSERT 0 1"))
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
		env.expect(usageResult(0), commandResult("INSERT 0 1"), commandResult("INSERT 0 1"))
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

// TestUploadPrecheckedSkipsLimiter 已预检（http 层）时不再重复消耗上传限频额度，
// 也不再重复读取设置与已用空间（快照透传）。
func TestUploadPrecheckedSkipsLimiter(t *testing.T) {
	env := newMediaTestEnv(t)
	env.settings.values.UploadRateLimit = 1
	env.settings.values.UploadRateWindow = time.Hour
	// 手动消耗唯一额度，模拟 http 层已经完成 PrecheckUpload。
	allowed, _ := env.svc.uploadLimiter.ConsumeLimit("user:7", 1, time.Hour)
	if !allowed {
		t.Fatal("首个额度应消耗成功")
	}
	env.expect(commandResult("INSERT 0 1"), commandResult("INSERT 0 1"))
	in := uploadInput(humanViewer(7), "notes.txt", domain.PurposePost, testTextBytes())
	in.Precheck = testUploadPrecheck(0)
	if _, err := env.svc.Upload(context.Background(), in); err != nil {
		t.Fatalf("预检后上传不应再次限频，实际 %v", err)
	}
	if got := env.stub.pending(); got != 0 {
		t.Fatalf("预检后上传不应再有预检查询，剩余 %d 条脚本结果", got)
	}
}

// TestUploadUsesPrecheckSnapshot 确认 Upload 完全复用快照：不读设置、不读用量，
// 配额校验直接使用快照里的 UsedBytes。
func TestUploadUsesPrecheckSnapshot(t *testing.T) {
	env := newMediaTestEnv(t)
	in := uploadInput(humanViewer(7), "a.png", domain.PurposePost, testPNG(t, 4, 4))
	in.SizeHint = 200
	in.Precheck = &UploadPrecheck{
		MaxRequestBytes: 12 << 20, MaxUploadBytes: 10 << 20,
		StorageQuotaBytes: 1000, UsedBytes: 900,
	}
	_, err := env.svc.Upload(context.Background(), in)
	if !errors.Is(err, domain.ErrStorageQuotaExceeded) {
		t.Fatalf("期望 ErrStorageQuotaExceeded，实际 %v", err)
	}
	if got := len(env.stub.recorded()); got != 0 {
		t.Fatalf("带快照的上传不应触库，实际查询 %d 次: %v", got, env.stub.recorded())
	}
}

// TestUploadDatabaseAndPortFailures 覆盖事务内各写入/入队/回写失败时的错误映射。
func TestUploadDatabaseAndPortFailures(t *testing.T) {
	t.Run("统计已用空间失败", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(dbErrorResult("usage 失败"))
		_, err := env.svc.Upload(context.Background(), uploadInput(humanViewer(7), "notes.txt", domain.PurposePost, testTextBytes()))
		requireKind(t, err, apperr.KindInternal)
	})
	t.Run("附件记录写入失败", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(usageResult(0), dbErrorResult("insert 失败"))
		_, err := env.svc.Upload(context.Background(), uploadInput(humanViewer(7), "notes.txt", domain.PurposePost, testTextBytes()))
		requireKind(t, err, apperr.KindInternal)
	})
	t.Run("配额计数写入失败", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(usageResult(0), commandResult("INSERT 0 1"), dbErrorResult("usage 写入失败"))
		_, err := env.svc.Upload(context.Background(), uploadInput(humanViewer(7), "notes.txt", domain.PurposePost, testTextBytes()))
		requireKind(t, err, apperr.KindInternal)
	})
	t.Run("衍生记录写入失败", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(usageResult(0), commandResult("INSERT 0 1"), commandResult("INSERT 0 1"), dbErrorResult("derivative 失败"))
		_, err := env.svc.Upload(context.Background(), uploadInput(humanViewer(7), "a.png", domain.PurposePost, testPNG(t, 4, 4)))
		requireKind(t, err, apperr.KindInternal)
	})
	t.Run("作业入队失败", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.jobs.enqueueErr = errors.New("jobs 不可用")
		env.expect(usageResult(0), commandResult("INSERT 0 1"), commandResult("INSERT 0 1"), commandResult("INSERT 0 1"))
		_, err := env.svc.Upload(context.Background(), uploadInput(humanViewer(7), "a.png", domain.PurposePost, testPNG(t, 4, 4)))
		requireKind(t, err, apperr.KindInternal)
	})
	t.Run("头像回写失败", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.auth.setAvatarErr = errors.New("auth 不可用")
		env.expect(usageResult(0), commandResult("INSERT 0 1"), commandResult("INSERT 0 1"), commandResult("INSERT 0 1"))
		_, err := env.svc.Upload(context.Background(), uploadInput(humanViewer(7), "a.png", domain.PurposeAvatar, testPNG(t, 4, 4)))
		requireKind(t, err, apperr.KindInternal)
	})
	t.Run("ID 生成失败", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.ids.err = errors.New("id 耗尽")
		env.expect(usageResult(0))
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
