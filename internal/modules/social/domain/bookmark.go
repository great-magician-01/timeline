// bookmark.go 实现收藏域的纯规则：收藏夹名称校验、上限常量、列表查询三态与行结构。
// 不访问数据库、不依赖 HTTP/Gin；错误统一使用 platform/apperr。
package domain

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
)

const (
	// MaxBookmarkFoldersPerUser 是每位用户可持有的生效收藏夹上限。
	MaxBookmarkFoldersPerUser = 20
	// MaxBookmarkFolderNameRunes 是收藏夹名称的最大 rune 数（去首尾空白后）。
	MaxBookmarkFolderNameRunes = 30
)

// BookmarkListQuery 是收藏列表的一批候选查询（bookmark 行 ID 倒序；AfterID=0 表示首页）。
// 夹过滤为三态，优先级 All > DefaultOnly > FolderID：All=true 返回全部；否则
// DefaultOnly=true 只看未分组（folder_id IS NULL）；否则按 FolderID 精确过滤。
// 不能用零值 FolderID 反向表示"全部"，否则过滤会静默退化（教训同关注时间线的 FollowingOnly）。
type BookmarkListQuery struct {
	UserID int64
	All    bool
	// DefaultOnly 表示只看未分组收藏；仅在 All=false 时生效。
	DefaultOnly bool
	FolderID    int64
	AfterID     int64
	Limit       int
}

// BookmarkRow 是一条收藏记录（repo 行结构）。
type BookmarkRow struct {
	ID        int64
	UserID    int64
	PostID    int64
	FolderID  *int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// BookmarkFolderRow 是一条收藏夹记录；BookmarkCount 由列表查询统计填充。
type BookmarkFolderRow struct {
	ID            int64
	UserID        int64
	Name          string
	BookmarkCount int
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// NormalizeBookmarkFolderName 去首尾空白后校验收藏夹名称为 1..30 rune。
func NormalizeBookmarkFolderName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", apperr.Validation("收藏夹名称不能为空")
	}
	if utf8.RuneCountInString(name) > MaxBookmarkFolderNameRunes {
		return "", apperr.Validation("收藏夹名称不能超过 30 个字符")
	}
	return name, nil
}
