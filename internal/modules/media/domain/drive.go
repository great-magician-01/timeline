package domain

import (
	"strings"
	"unicode/utf8"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
)

// 网盘文件公开级别与上传来源。
const (
	DriveVisibilityPrivate = "private"
	DriveVisibilityPublic  = "public"

	UploadSourcePost  = "post"
	UploadSourceDrive = "drive"

	PostFolderSystemKey = "post"
	PostFolderName      = "文章附件"
	MaxFolderNameRunes  = 50
)

// NormalizeDriveVisibility 校验文件/文件夹批量设置的公开级别。
func NormalizeDriveVisibility(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value != DriveVisibilityPrivate && value != DriveVisibilityPublic {
		return "", apperr.Invalid("visibility 只能是 private 或 public")
	}
	return value, nil
}

// NormalizeUploadSource 区分文章上传与网盘直传；空值保持旧客户端的 post 语义。
func NormalizeUploadSource(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return UploadSourcePost, nil
	}
	if value != UploadSourcePost && value != UploadSourceDrive {
		return "", apperr.Invalid("source 只能是 post 或 drive")
	}
	return value, nil
}

// NormalizeFolderName 校验一级文件夹名称；系统目录也允许改名，但不依赖名称识别。
func NormalizeFolderName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", apperr.Validation("文件夹名称不能为空").
			WithDetails(map[string]any{"name": "文件夹名称不能为空"})
	}
	if utf8.RuneCountInString(name) > MaxFolderNameRunes {
		return "", apperr.Validation("文件夹名称不能超过 50 个字符").
			WithDetails(map[string]any{"name": "文件夹名称不能超过 50 个字符"})
	}
	return name, nil
}

// ValidDriveKind 校验网盘列表的类型过滤值。
func ValidDriveKind(raw string) bool {
	return raw == "" || raw == KindImage || raw == KindVideo || raw == KindFile
}
