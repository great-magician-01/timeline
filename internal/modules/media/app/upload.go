package app

import (
	"bufio"
	"context"
	"errors"
	"io"

	"github.com/great-magician-01/timeline/internal/modules/media/domain"
	"github.com/great-magician-01/timeline/internal/modules/media/repo"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/storage"
	"github.com/great-magician-01/timeline/internal/shared/access"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/format"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// sniffBytes 与 storage 内部嗅探长度一致：类型判定只看实际内容的前 512 字节。
const sniffBytes = 512

// UploadInput 是一次附件上传的用例输入。Content 由 http 层在限制请求体大小后提供。
type UploadInput struct {
	Viewer   model.Viewer
	Filename string
	Purpose  string
	// SizeHint 是客户端 multipart 头部声明的大小，仅用于提前拒绝明显超配额的请求；
	// 真实大小以 storage.Save 实际写入字节为准。
	SizeHint int64
	// Precheck 是 http 层 PrecheckUpload 返回的只读快照（设置上限 + 已用字节）。
	// 非 nil 表示写权限、上传限频与「配额已满」预检已完成：Upload 不再重复读取设置、
	// 不再重复消费限频、也不再二次统计已用空间；nil 时 Upload 在落盘前自行补做一次
	// （保留给直接调用 Upload 的调用方）。
	Precheck *UploadPrecheck
	Content  io.Reader
}

// UploadPrecheck 是一次上传预检得到的只读快照，用于在 http 层与 Upload 之间传递设置
// 上限与已用配额，避免同一请求内重复读设置（每次 12 行主键点查）与重复聚合。
type UploadPrecheck struct {
	MaxRequestBytes   int64 // multipart 请求体上限（http 层用于 MaxBytesReader）
	MaxUploadBytes    int64 // 单文件上限（storage.Save 使用）
	StorageQuotaBytes int64 // 用户存储配额
	UsedBytes         int64 // 预检时的已用字节（含已逻辑删除附件，只增不减）
}

// UploadResult 是上传结果：附件行 + 衍生文件初始状态。
type UploadResult struct {
	Attachment  model.Attachment
	Derivatives map[string]string
}

// PrecheckUpload 在 http 层解析 multipart 之前完成鉴权、上传限频与「存储配额已满」预检，
// 并返回设置/已用字节快照供 Upload 复用；未登录/停用/AI 会话、超频与配额耗尽的请求因此
// 不必先被读取请求体（旧实现顺序）。解析完成后 Upload 仍会按 SizeHint 做配额校验。
func (s *Service) PrecheckUpload(ctx context.Context, viewer model.Viewer) (UploadPrecheck, error) {
	if err := access.RequireWritable(viewer); err != nil {
		return UploadPrecheck{}, err
	}
	return s.precheckUpload(ctx, viewer)
}

// precheckUpload 读取设置快照、消耗一次上传限频并读取已用配额（attachment_usage 点查）。
// 快照在所有后继校验与落盘中共用，http 层与 Upload 各调用至多一次。
func (s *Service) precheckUpload(ctx context.Context, viewer model.Viewer) (UploadPrecheck, error) {
	values := s.settingsOrDefaults(ctx)
	if allowed, retryAfter := s.uploadLimiter.ConsumeLimit(
		"user:"+format.FormatID(viewer.ID), values.UploadRateLimit, values.UploadRateWindow,
	); !allowed {
		return UploadPrecheck{}, apperr.RateLimited(retryAfter)
	}
	used, err := repo.Usage(ctx, s.exec(ctx), viewer.ID)
	if err != nil {
		return UploadPrecheck{}, s.internal("统计已用存储空间失败", err)
	}
	if domain.ExceedsStorageQuota(used, 0, values.StorageQuotaBytes) {
		return UploadPrecheck{}, domain.ErrStorageQuotaExhausted
	}
	return UploadPrecheck{
		MaxRequestBytes:   values.MaxRequestBytes,
		MaxUploadBytes:    values.MaxUploadBytes,
		StorageQuotaBytes: values.StorageQuotaBytes,
		UsedBytes:         used,
	}, nil
}

// Upload 处理附件上传：鉴权 → 复用预检快照（或补做一次预检）→ 内容嗅探与落盘 →
// 事务内写附件行、累加已用配额、衍生文件记录、入队衍生作业、头像/横幅回写。
// 只信任实际内容，文件名仅用于展示。
func (s *Service) Upload(ctx context.Context, in UploadInput) (UploadResult, error) {
	if err := access.RequireWritable(in.Viewer); err != nil {
		return UploadResult{}, err
	}
	if s.jobs == nil {
		// 衍生文件依赖后台作业队列；未装配属启动配置错误，直接失败而不是静默跳过。
		return UploadResult{}, s.internal("后台作业队列未初始化", errors.New("jobs queue is nil"))
	}

	// http 层已预检时直接复用其快照（设置与已用空间各只读一次）；否则补做一次预检。
	precheck := in.Precheck
	if precheck == nil {
		snapshot, err := s.precheckUpload(ctx, in.Viewer)
		if err != nil {
			return UploadResult{}, err
		}
		precheck = &snapshot
	}

	purpose, err := domain.NormalizePurpose(in.Purpose)
	if err != nil {
		return UploadResult{}, err
	}
	if in.SizeHint > 0 && domain.ExceedsStorageQuota(precheck.UsedBytes, in.SizeHint, precheck.StorageQuotaBytes) {
		return UploadResult{}, domain.ErrStorageQuotaExceeded
	}

	// 先窥探前 512 字节用于种类判定，再把同一个 reader 交给 storage.Save，
	// 保证 kind 与 media_type 都来自实际内容而不是客户端声明。
	buffered := bufio.NewReaderSize(in.Content, sniffBytes)
	header, _ := buffered.Peek(sniffBytes) // 不足 512 字节时返回可用部分，不影响判定
	kind, _, _ := storage.Detect(header)
	// 横幅是主页背景装饰，必须为图片；头像沿用既有行为不做种类限制。
	if purpose == domain.PurposeBanner && kind != domain.KindImage {
		return UploadResult{}, apperr.Invalid("主页横幅必须是图片文件")
	}

	at := s.clk.Now()
	key, size, mediaType, err := s.store.Save(buffered, precheck.MaxUploadBytes, at)
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrTooLarge):
			return UploadResult{}, domain.ErrContentTooLarge
		case errors.Is(err, storage.ErrUnsupportedType):
			return UploadResult{}, domain.ErrUnsupportedMediaType
		default:
			return UploadResult{}, s.internal("保存上传内容失败", err)
		}
	}
	attachmentID, err := s.ids.Next()
	if err != nil {
		return UploadResult{}, s.internal("生成附件 ID 失败", err)
	}
	filename := domain.SanitizeFilename(in.Filename)
	width, height := s.imageDimensions(key)
	derivatives := map[string]string{}

	err = s.tx.WithTx(ctx, func(txCtx context.Context) error {
		txExec := s.exec(txCtx)
		if err := repo.Insert(txCtx, txExec, repo.InsertInput{
			ID:               attachmentID,
			UploaderID:       in.Viewer.ID,
			Kind:             kind,
			Purpose:          purpose,
			OriginalFilename: filename,
			MediaType:        mediaType,
			SizeBytes:        size,
			StorageKey:       key,
			Status:           model.AttachmentAvailable,
			Width:            width,
			Height:           height,
			At:               at,
		}); err != nil {
			return s.internal("写入附件记录失败", err)
		}

		// 同一事务内累加 attachment_usage 冗余计数：配额校验只做一次 PK 点查，
		// 不再随历史附件数实时 SUM（附件逻辑删除不释放磁盘，计数只增不减）。
		if err := repo.AddUsage(txCtx, txExec, in.Viewer.ID, size, at); err != nil {
			return s.internal("更新已用存储空间失败", err)
		}

		// 无法解码的图片类型直接记为 canceled（不回退为失败，也不投递作业）。
		if kind == domain.KindImage && !domain.DecodableImageType(mediaType) {
			derivativeID, err := s.ids.Next()
			if err != nil {
				return s.internal("生成衍生文件 ID 失败", err)
			}
			if err := repo.InsertDerivative(txCtx, txExec, repo.InsertDerivativeInput{
				ID:             derivativeID,
				AttachmentID:   attachmentID,
				DerivativeType: model.DerivativeThumbnail,
				Status:         model.DerivativeCanceled,
				LastError:      "该图片格式不支持生成缩略图，已回退为原图",
				At:             at,
			}); err != nil {
				return s.internal("写入衍生文件记录失败", err)
			}
			derivatives[model.DerivativeThumbnail] = model.DerivativeCanceled
		}
		if derivativeType := domain.DerivativeTypeForKind(kind); derivativeType != "" && derivatives[derivativeType] == "" {
			derivativeID, err := s.ids.Next()
			if err != nil {
				return s.internal("生成衍生文件 ID 失败", err)
			}
			if err := repo.InsertDerivative(txCtx, txExec, repo.InsertDerivativeInput{
				ID:             derivativeID,
				AttachmentID:   attachmentID,
				DerivativeType: derivativeType,
				Status:         model.DerivativePending,
				At:             at,
			}); err != nil {
				return s.internal("写入衍生文件记录失败", err)
			}
			refID := attachmentID
			if err := s.jobs.Enqueue(txCtx, contracts.JobEnqueue{
				Type:           contracts.JobTypeMediaDerivative,
				RefType:        domain.RefTypeAttachment,
				RefID:          &refID,
				IdempotencyKey: domain.DerivativeJobKey(attachmentID, derivativeType),
				Payload:        map[string]any{"attachment_id": attachmentID, "type": derivativeType},
				At:             at,
			}); err != nil {
				return s.internal("投递衍生文件作业失败", err)
			}
			derivatives[derivativeType] = model.DerivativePending
		}

		if purpose == domain.PurposeAvatar {
			if err := s.auth.SetAvatar(txCtx, contracts.AvatarUpdateInput{
				UserID:       in.Viewer.ID,
				AttachmentID: attachmentID,
				AvatarURL:    format.AttachmentContentURL(attachmentID),
				At:           at,
			}); err != nil {
				return s.internal("更新头像地址失败", err)
			}
		}
		if purpose == domain.PurposeBanner {
			if err := s.auth.SetBanner(txCtx, contracts.BannerUpdateInput{
				UserID:       in.Viewer.ID,
				AttachmentID: &attachmentID,
				BannerURL:    format.AttachmentContentURL(attachmentID),
				At:           at,
			}); err != nil {
				return s.internal("更新主页横幅地址失败", err)
			}
		}
		return nil
	})
	if err != nil {
		return UploadResult{}, err
	}

	return UploadResult{
		Attachment: model.Attachment{
			ID:               attachmentID,
			UploaderID:       in.Viewer.ID,
			Kind:             kind,
			OriginalFilename: filename,
			MediaType:        mediaType,
			SizeBytes:        size,
			StorageKey:       key,
			Status:           model.AttachmentAvailable,
			Width:            width,
			Height:           height,
			CreatedAt:        at,
			UpdatedAt:        at,
		},
		Derivatives: derivatives,
	}, nil
}

// imageDimensions 解析图片宽高；只依赖标准库解码器（jpeg/png/gif），
// 其它类型或解析失败时返回空值，不影响上传。
func (s *Service) imageDimensions(key string) (*int, *int) {
	file, err := s.store.Open(key)
	if err != nil {
		return nil, nil
	}
	defer file.Close()
	config, _, err := imageDecodeConfig(file)
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		return nil, nil
	}
	width, height := config.Width, config.Height
	return &width, &height
}
