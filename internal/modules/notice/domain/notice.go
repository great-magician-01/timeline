// Package domain 实现通知域的纯规则：类型/过滤参数校验、预览文本裁剪与来源目标映射。
// 不访问数据库、不依赖 HTTP；错误统一使用 platform/apperr。
package domain

import (
	"strings"
	"time"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// PreviewRunes 是通知预览文本的最大字符数（超出截断并追加省略号）。
const PreviewRunes = 120

// MentionRow 是 mentions 行读取结构。定义在 domain 使 repo 无需 import app
// （保持 repo → domain 的依赖方向，与 social 的 FollowRow/RequestRow 一致）。
type MentionRow struct {
	ID              int64
	MentionedUserID int64
	SourceType      string
	SourceID        int64
	CreatedAt       time.Time
}

// Types 是 notifications.type 的合法取值（与迁移 CHECK、shared/model 常量一致）。
var Types = []string{
	model.NoticeTypePostLike,
	model.NoticeTypeCommentLike,
	model.NoticeTypeComment,
	model.NoticeTypeReply,
	model.NoticeTypeRepost,
	model.NoticeTypeQuote,
	model.NoticeTypeFollow,
	model.NoticeTypeFollowRequest,
	model.NoticeTypeFollowApprove,
	model.NoticeTypeMention,
}

// ValidType 报告通知类型是否合法。
func ValidType(noticeType string) bool {
	for _, candidate := range Types {
		if noticeType == candidate {
			return true
		}
	}
	return false
}

// NormalizeType 校验 type 查询参数：空串表示不过滤，其余必须是合法通知类型。
func NormalizeType(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil
	}
	if !ValidType(value) {
		return "", apperr.Invalid("type 参数无效，只能是合法通知类型")
	}
	return value, nil
}

// NormalizeFilter 校验 filter 查询参数：空串与 all 表示全部，unread 表示未读。
func NormalizeFilter(raw string) (string, error) {
	switch strings.TrimSpace(raw) {
	case "", "all":
		return "all", nil
	case "unread":
		return "unread", nil
	default:
		return "", apperr.Invalid("filter 只能是 all 或 unread")
	}
}

// TargetTypeFor 把提及来源类型映射为通知目标类型。
func TargetTypeFor(sourceType string) string {
	if sourceType == model.NoticeSourceComment {
		return model.NoticeTargetComment
	}
	return model.NoticeTargetPost
}

// TruncatePreview 裁剪预览文本：空白返回 nil；超长截断并追加省略号。
func TruncatePreview(text string) *string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil
	}
	runes := []rune(trimmed)
	if len(runes) > PreviewRunes {
		trimmed = string(runes[:PreviewRunes]) + "…"
	}
	return &trimmed
}
