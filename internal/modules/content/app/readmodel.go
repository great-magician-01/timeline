package app

import (
	"time"

	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/shared/page"
)

// AttachmentView 是附件行与衍生文件状态的组合，供 http 层组装 AttachmentResponse。
type AttachmentView struct {
	Attachment  model.Attachment
	Derivatives map[string]string
}

// ViewerState 是访问者对当前内容的互动状态。
type ViewerState struct {
	Liked    bool
	Reposted bool
}

// QuotedPost 是引用转发的原帖占位：原帖不可见或已删除时 Available 为 false 且不带 Post。
type QuotedPost struct {
	Available bool
	Post      *PostBrief
}

// PostBrief 是时间线、搜索、标签聚合与嵌套引用共用的列表项。
// 嵌套位置（RepostOf 与 QuotedPost.Post）不得再嵌套：其 RepostOf/QuotedPost 为 nil。
type PostBrief struct {
	ID           int64
	Kind         string
	Author       model.UserBrief
	Visibility   string
	ContentHTML  string
	PublishedAt  *time.Time
	RepostedAt   *time.Time
	Edited       bool
	LikeCount    int
	CommentCount int
	RepostCount  int
	RepostOf     *PostBrief
	QuotedPost   *QuotedPost
	Attachments  []AttachmentView
	ViewerState  ViewerState
}

// PostDetail 是帖子详情：在列表项基础上补充作者操作与历史访问信息。
type PostDetail struct {
	PostBrief
	Status            string
	ContentFormat     string
	ScheduledAt       *time.Time
	Revision          int
	VersionNumber     int
	CanEdit           bool
	CanDelete         bool
	CanViewHistory    bool
	AuthorFollowState string
}

// VersionItem 是历史版本列表项。
type VersionItem struct {
	ID                    int64
	VersionNumber         int
	Visibility            string
	CreatedAt             time.Time
	RestoredFromVersionID *int64
	Summary               string
}

// VersionDetail 是历史版本详情，包含该版本的正文与附件快照。
type VersionDetail struct {
	ID                    int64
	VersionNumber         int
	ContentFormat         string
	ContentSource         string
	ContentHTML           string
	Visibility            string
	CreatedAt             time.Time
	CreatedBy             int64
	RestoredFromVersionID *int64
	Attachments           []AttachmentView
}

// DraftItem 是「我的草稿 / 我的定时发布」列表项。
// ContentHTML 返回完整的安全 HTML，PlainText 为纯文本摘要；
// Status 默认取 posts.status，定时发布作业失败或暂停时展示 failed/paused。
type DraftItem struct {
	ID              int64
	ContentHTML     string
	PlainText       string
	Visibility      string
	Revision        int
	Status          string
	UpdatedAt       time.Time
	ScheduledAt     *time.Time
	AttachmentCount int
	PublishFailed   *bool
	PublishError    *string
}

// CommentTimelineItem 是个人主页「回复」页签的列表项。
type CommentTimelineItem struct {
	Kind        string
	ID          int64
	Author      model.UserBrief
	ContentHTML string
	CreatedAt   time.Time
	Post        *PostBrief
}

// TagSuggestion 是标签自动补全项。
type TagSuggestion struct {
	Name        string
	DisplayName string
	PostCount   int
}

// TimelinePage 是列表页结果：Posts 与 Replies 二选一，Next 为下一页时间游标。
// Kind 取 domain.TabPosts（posts/reposts 页签）或 domain.TabReplies（回复页签），
// 供 http 层显式区分页签；Replies 页签即使为空也始终返回非 nil 切片。
type TimelinePage struct {
	Kind    string
	Posts   []PostBrief
	Replies []CommentTimelineItem
	Next    page.TimeCursor
	HasMore bool
}

// VersionPage 是历史版本分页结果（ID 游标）。
type VersionPage struct {
	Items   []VersionItem
	NextID  int64
	HasMore bool
}

// DraftPage 是草稿/定时列表分页结果（ID 游标）。
type DraftPage struct {
	Items   []DraftItem
	NextID  int64
	HasMore bool
}
