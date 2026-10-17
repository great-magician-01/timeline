package media

import (
	"bufio"
	"context"
	"errors"
	"image"
	"math"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/access"
	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/auth"
	"github.com/great-magician-01/timeline/internal/jobs"
	"github.com/great-magician-01/timeline/internal/model"
	"github.com/great-magician-01/timeline/internal/settings"
	"github.com/great-magician-01/timeline/internal/storage"
)

// sniffBytes 与 storage 内部嗅探长度一致：类型判定只看实际内容的前 512 字节。
const sniffBytes = 512

// maxOriginalFilenameRunes 是展示用原始文件名的最大长度。
const maxOriginalFilenameRunes = 200

// defaultFilename 用于无法得到安全文件名的场景。
const defaultFilename = "未命名文件"

// retryAfterSeconds 把限频窗口剩余时间换算为 Retry-After 秒数（至少 1 秒）。
func retryAfterSeconds(retryAfter time.Duration) int {
	seconds := int(math.Ceil(retryAfter.Seconds()))
	if seconds < 1 {
		return 1
	}
	return seconds
}

// usedStorageBytes 统计上传者现有附件的总大小，用于存储配额判断。
// 这里刻意包含已逻辑删除的记录：物理文件仍然保留并占用磁盘，配额必须按实际占用计算。
func usedStorageBytes(ctx context.Context, q access.Querier, uploaderID int64) (int64, error) {
	var used int64
	err := q.QueryRow(ctx,
		`SELECT COALESCE(SUM(size_bytes), 0) FROM attachments WHERE uploader_id = $1`,
		uploaderID).Scan(&used)
	return used, err
}

// exceedsStorageQuota 判断“已用空间 + 本次上传”是否超过配额（纯函数，便于单元测试）。
func exceedsStorageQuota(used, incoming, quota int64) bool {
	return used+incoming > quota
}

// Upload 处理 POST /api/v1/attachments。
// 只信任实际内容：文件名仅保留安全形式用于展示，存储路径完全由服务端生成。
func (s *Service) Upload(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	if s.jobs == nil {
		// 衍生文件依赖后台作业运行器；未装配属启动配置错误，直接失败而不是静默跳过。
		s.internalError(c, "后台作业运行器未初始化", errors.New("jobs runner is nil"))
		return
	}
	u, ok := auth.CurrentUser(c)
	if !ok {
		api.Error(c, http.StatusUnauthorized, "unauthenticated", "请先登录")
		return
	}
	viewer := model.ViewerFrom(u)
	if err := access.RequireWritable(viewer); err != nil {
		if !api.Respond(c, err) {
			s.internalError(c, "校验上传权限失败", err)
		}
		return
	}
	// 上传大小、请求大小、存储配额与限频均为系统设置，每次请求即时读取。
	ctx := c.Request.Context()
	values := settings.LoadOrDefaults(ctx, pool)
	if allowed, retryAfter := s.uploadLimiter.ConsumeLimit(
		"user:"+api.FormatID(u.ID), values.UploadRateLimit, values.UploadRateWindow,
	); !allowed {
		c.Header("Retry-After", strconv.Itoa(retryAfterSeconds(retryAfter)))
		api.Error(c, http.StatusTooManyRequests, "rate_limited", "上传过于频繁，请稍后重试")
		return
	}

	used, err := usedStorageBytes(ctx, pool, u.ID)
	if err != nil {
		s.internalError(c, "统计已用存储空间失败", err)
		return
	}
	if exceedsStorageQuota(used, 0, values.StorageQuotaBytes) {
		api.Error(c, http.StatusRequestEntityTooLarge, "content_too_large", "存储空间已用完，无法继续上传")
		return
	}

	// 即使客户端伪造 Content-Length，超限请求也会在读取时被中断。
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, values.MaxRequestBytes)

	fileHeader, err := c.FormFile("file")
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			api.Error(c, http.StatusRequestEntityTooLarge, "content_too_large", "文件超过大小限制")
			return
		}
		api.Error(c, http.StatusBadRequest, "invalid_request", "请选择要上传的文件")
		return
	}
	purpose := strings.TrimSpace(c.PostForm("purpose"))
	if purpose == "" {
		purpose = purposePost
	}
	if purpose != purposePost && purpose != purposeAvatar {
		api.Error(c, http.StatusBadRequest, "invalid_request", "purpose 只能是 post 或 avatar")
		return
	}
	// 存储配额包含逻辑删除记录，因此只按 uploader 的 size_bytes 总和判断。
	if exceedsStorageQuota(used, fileHeader.Size, values.StorageQuotaBytes) {
		api.Error(c, http.StatusRequestEntityTooLarge, "content_too_large", "附件总大小超过存储配额")
		return
	}

	source, err := fileHeader.Open()
	if err != nil {
		s.internalError(c, "读取上传内容失败", err)
		return
	}
	defer source.Close()

	// 先窥探前 512 字节用于种类判定，再把同一个 reader 交给 storage.Save，
	// 保证 kind 与 media_type 都来自实际内容而不是客户端声明。
	buffered := bufio.NewReaderSize(source, sniffBytes)
	header, _ := buffered.Peek(sniffBytes) // 不足 512 字节时返回可用部分，不影响判定
	kind, _, _ := storage.Detect(header)

	key, size, mediaType, err := s.store.Save(buffered, values.MaxUploadBytes, time.Now())
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrTooLarge):
			api.Error(c, http.StatusRequestEntityTooLarge, "content_too_large", "文件超过大小限制")
		case errors.Is(err, storage.ErrUnsupportedType):
			api.Error(c, http.StatusUnsupportedMediaType, "unsupported_media_type", "不支持的文件类型")
		default:
			s.internalError(c, "保存上传内容失败", err)
		}
		return
	}
	attachmentID, err := s.ids.Next()
	if err != nil {
		s.internalError(c, "生成附件 ID 失败", err)
		return
	}
	filename := sanitizeFilename(fileHeader.Filename)
	width, height := s.imageDimensions(key)
	createdAt := time.Now()

	tx, err := pool.Begin(ctx)
	if err != nil {
		s.internalError(c, "开启附件事务失败", err)
		return
	}
	// 提交成功后 Rollback 返回 ErrTxClosed，可安全忽略。
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		INSERT INTO attachments (
			id, uploader_id, post_id, kind, purpose, original_filename, media_type,
			size_bytes, storage_key, status, width, height, duration_ms
		) VALUES ($1, $2, NULL, $3, $4, $5, $6, $7, $8, 'available', $9, $10, NULL)`,
		attachmentID, u.ID, kind, purpose, filename, mediaType, size, key, width, height,
	); err != nil {
		s.internalError(c, "写入附件记录失败", err)
		return
	}

	derivatives := map[string]string{}
	// 无法解码的图片类型直接记为 canceled（不回退为失败，也不投递作业）。
	if kind == storage.KindImage && !decodableImageType(mediaType) {
		derivativeID, err := s.ids.Next()
		if err != nil {
			s.internalError(c, "生成衍生文件 ID 失败", err)
			return
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO attachment_derivatives (
				id, attachment_id, derivative_type, status, last_error
			) VALUES ($1, $2, $3, 'canceled', $4)`,
			derivativeID, attachmentID, model.DerivativeThumbnail,
			"该图片格式不支持生成缩略图，已回退为原图",
		); err != nil {
			s.internalError(c, "写入衍生文件记录失败", err)
			return
		}
		derivatives[model.DerivativeThumbnail] = "canceled"
	}
	if derivativeType := derivativeTypeForKind(kind); derivativeType != "" && derivatives[derivativeType] == "" {
		derivativeID, err := s.ids.Next()
		if err != nil {
			s.internalError(c, "生成衍生文件 ID 失败", err)
			return
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO attachment_derivatives (id, attachment_id, derivative_type, status)
			VALUES ($1, $2, $3, 'pending')`, derivativeID, attachmentID, derivativeType,
		); err != nil {
			s.internalError(c, "写入衍生文件记录失败", err)
			return
		}
		if err := s.jobs.Enqueue(ctx, tx, jobs.EnqueueParams{
			Type:           jobs.TypeMediaDerivative,
			RefType:        refTypeAttachment,
			RefID:          &attachmentID,
			IdempotencyKey: derivativeJobKey(attachmentID, derivativeType),
			Payload:        derivativePayload{AttachmentID: attachmentID, Type: derivativeType},
		}); err != nil {
			s.internalError(c, "投递衍生文件作业失败", err)
			return
		}
		derivatives[derivativeType] = model.DerivativePending
	}

	if purpose == purposeAvatar {
		avatarURL := model.Attachment{ID: attachmentID}.ContentURL()
		if _, err := tx.Exec(ctx,
			`UPDATE users SET avatar_url = $2, updated_at = now() WHERE id = $1`,
			u.ID, avatarURL,
		); err != nil {
			s.internalError(c, "更新头像地址失败", err)
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		s.internalError(c, "提交上传事务失败", err)
		return
	}

	att := model.Attachment{
		ID:               attachmentID,
		UploaderID:       u.ID,
		Kind:             kind,
		OriginalFilename: filename,
		MediaType:        mediaType,
		SizeBytes:        size,
		StorageKey:       key,
		Status:           model.AttachmentAvailable,
		Width:            width,
		Height:           height,
		CreatedAt:        createdAt,
	}
	c.JSON(http.StatusCreated, gin.H{"attachment": model.AttachmentResponseFor(att, derivatives)})
}

// imageDimensions 解析图片宽高；只依赖标准库解码器（jpeg/png/gif），
// 其它类型或解析失败时返回空值，不影响上传。
func (s *Service) imageDimensions(key string) (*int, *int) {
	file, err := s.store.Open(key)
	if err != nil {
		return nil, nil
	}
	defer file.Close()
	config, _, err := image.DecodeConfig(file)
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		return nil, nil
	}
	width, height := config.Width, config.Height
	return &width, &height
}

// sanitizeFilename 只保留原始文件名的安全形式：去掉路径分隔符与控制字符，
// 截断到 200 字符。返回值仅用于展示，绝不参与存储路径（docs/REQUIREMENTS.md 7.9）。
func sanitizeFilename(raw string) string {
	// 兼容 Windows 与 POSIX 客户端提交的文件名：先把反斜杠统一为斜杠再取最后一段。
	normalized := strings.ReplaceAll(raw, "\\", "/")
	base := path.Base(normalized)
	cleaned := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, base)
	cleaned = strings.TrimSpace(strings.Trim(cleaned, "/"))
	if cleaned == "" || cleaned == "." || cleaned == ".." {
		return defaultFilename
	}
	runes := []rune(cleaned)
	if len(runes) > maxOriginalFilenameRunes {
		cleaned = string(runes[:maxOriginalFilenameRunes])
	}
	return cleaned
}
