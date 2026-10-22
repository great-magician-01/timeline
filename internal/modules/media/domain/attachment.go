// Package domain 是 media 模块的纯规则层：附件用途/种类/状态、文件名净化、
// 存储配额、临时附件保留期与衍生文件映射等判断。不 import gin/pgx，不做 IO。
package domain

import (
	"errors"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// 附件用途（与 attachments.purpose 的 CHECK 约束一致）。
const (
	PurposePost   = "post"
	PurposeAvatar = "avatar"
	PurposeBanner = "banner"
)

// 附件种类（与 attachments.kind 的 CHECK 约束及 platform/storage 的嗅探结果一致）。
const (
	KindImage = "image"
	KindVideo = "video"
	KindFile  = "file"
)

// 附件类型判别与衍生文件参数。
const (
	// MaxOriginalFilenameRunes 是展示用原始文件名的最大长度。
	MaxOriginalFilenameRunes = 200
	// DefaultFilename 用于无法得到安全文件名的场景。
	DefaultFilename = "未命名文件"

	// ThumbnailMaxEdge 是缩略图最长边（不放大）。
	ThumbnailMaxEdge = 512
	// JPEGQuality 是缩略图与视频封面的编码质量。
	JPEGQuality = 82
	// VideoCoverMaxWidth 是视频封面最大宽度，高度按比例取偶数。
	VideoCoverMaxWidth = 640
	// DerivativeErrorMaxRunes 是 attachment_derivatives.last_error 的截断长度（字符）。
	DerivativeErrorMaxRunes = 500
	// DefaultTempRetention 是临时附件保留期的兜底（7 天，与系统设置默认值一致）。
	DefaultTempRetention = 7 * 24 * time.Hour

	// RefTypeAttachment 是 background_jobs.ref_type 的附件引用类型。
	RefTypeAttachment = "attachment"
)

// 上传接口对外的可识别错误。HTTP 状态码无法由 platform/apperr 的 Kind 表达
// （413/415），因此在 domain 定义哨兵错误，由 media/http 映射为既有错误码。
var (
	// ErrContentTooLarge 表示单文件超过上传上限（HTTP 413 content_too_large）。
	ErrContentTooLarge = errors.New("文件超过大小限制")
	// ErrStorageQuotaExhausted 表示用户存储配额已用完（HTTP 413 content_too_large）。
	ErrStorageQuotaExhausted = errors.New("存储空间已用完，无法继续上传")
	// ErrStorageQuotaExceeded 表示本次上传将超过存储配额（HTTP 413 content_too_large）。
	ErrStorageQuotaExceeded = errors.New("附件总大小超过存储配额")
	// ErrUnsupportedMediaType 表示实际内容类型不允许上传（HTTP 415 unsupported_media_type）。
	ErrUnsupportedMediaType = errors.New("不支持的文件类型")
)

// NormalizePurpose 归一化上传用途：空值回退 post，非法值返回 400 invalid_request。
func NormalizePurpose(raw string) (string, error) {
	purpose := strings.TrimSpace(raw)
	if purpose == "" {
		return PurposePost, nil
	}
	if purpose != PurposePost && purpose != PurposeAvatar && purpose != PurposeBanner {
		return "", apperr.Invalid("purpose 只能是 post、avatar 或 banner")
	}
	return purpose, nil
}

// ValidDerivativeType 判断衍生文件类型是否合法。
func ValidDerivativeType(derivativeType string) bool {
	switch derivativeType {
	case model.DerivativeThumbnail, model.DerivativeVideoCover:
		return true
	default:
		return false
	}
}

// DerivativeTypeForKind 把附件种类映射为需要生成的衍生文件类型；无需生成时返回空串。
func DerivativeTypeForKind(kind string) string {
	switch kind {
	case KindImage:
		return model.DerivativeThumbnail
	case KindVideo:
		return model.DerivativeVideoCover
	default:
		return ""
	}
}

// DerivativeJobKey 是衍生文件作业的幂等键，保证重试与并发不会重复生成。
func DerivativeJobKey(attachmentID int64, derivativeType string) string {
	return "media_derivative:" + strconv.FormatInt(attachmentID, 10) + ":" + derivativeType
}

// DecodableImageType 判断图片是否由本进程的标准库解码器支持（jpeg/png/gif）。
// 其他图片类型无法生成缩略图：上传时直接标记 canceled，避免投递注定失败的作业。
func DecodableImageType(mediaType string) bool {
	switch mediaType {
	case "image/jpeg", "image/png", "image/gif":
		return true
	default:
		return false
	}
}

// SanitizeFilename 只保留原始文件名的安全形式：去掉路径分隔符与控制字符，
// 截断到 200 字符。返回值仅用于展示，绝不参与存储路径。
func SanitizeFilename(raw string) string {
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
		return DefaultFilename
	}
	runes := []rune(cleaned)
	if len(runes) > MaxOriginalFilenameRunes {
		cleaned = string(runes[:MaxOriginalFilenameRunes])
	}
	return cleaned
}

// ExceedsStorageQuota 判断“已用空间 + 本次上传”是否超过配额。
// 已逻辑删除的记录也计入已用空间：物理文件仍然保留并占用磁盘。
func ExceedsStorageQuota(used, incoming, quota int64) bool {
	return used+incoming > quota
}

// CleanupRetention 返回临时附件保留期；非法值回退安全默认值。
func CleanupRetention(configured time.Duration) time.Duration {
	if configured <= 0 {
		return DefaultTempRetention
	}
	return configured
}

// CleanupCutoff 返回临时附件的失效截止时间：早于该时间且未关联帖子的附件应被标记失效。
// 由应用时钟计算后作为参数传给 SQL，数据库不做时间运算。
func CleanupCutoff(now time.Time, retention time.Duration) time.Time {
	return now.Add(-retention)
}

// TruncateRunes 按字符（而非字节）截断文本，避免截断 UTF-8 字符。
func TruncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
