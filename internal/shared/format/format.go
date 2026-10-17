// Package format 提供跨层复用的纯格式化函数：雪花 ID 字符串化、北京时间输出与
// 附件访问路径。只依赖标准库，不感知 gin/http 请求对象。
package format

import (
	"strconv"
	"time"
)

// Beijing 是系统固定展示时区（Asia/Shanghai，无夏令时）。
// 这里固定偏移而不是依赖 time.Local，保证进程时区设置异常时输出仍然稳定。
var Beijing = time.FixedZone("CST", 8*60*60)

// FormatID 把雪花 ID 序列化为字符串，避免 JavaScript 数字精度丢失。
func FormatID(id int64) string { return strconv.FormatInt(id, 10) }

// FormatTime 以北京时间 +08:00 的 RFC3339 文本输出时间。
// 保持旧 internal/api.FormatTime 的输出语义（不做零值特殊处理）。
func FormatTime(t time.Time) string { return t.In(Beijing).Format(time.RFC3339) }

// FormatTimePtr 输出可空时间：nil 返回 nil，便于 DTO 的 *string + omitempty 字段。
func FormatTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	formatted := FormatTime(*t)
	return &formatted
}

// AttachmentContentURL 是附件内容的鉴权访问路径。
func AttachmentContentURL(id int64) string {
	return "/api/v1/attachments/" + FormatID(id) + "/content"
}

// AttachmentDerivativeURL 是衍生文件（缩略图/视频封面）的鉴权访问路径。
func AttachmentDerivativeURL(id int64, derivativeType string) string {
	return "/api/v1/attachments/" + FormatID(id) + "/derivatives/" + derivativeType
}

// ThumbnailURL 是图片缩略图的鉴权访问路径。
func ThumbnailURL(id int64) string {
	return AttachmentDerivativeURL(id, "thumbnail")
}
