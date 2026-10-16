// Package model 定义跨模块复用的数据行结构与对外 DTO。
// 只放“表结构映射 + 与权限无关的序列化”，业务规则放在各业务包里。
package model

import (
	"time"

	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/auth"
)

// 帖子状态与可见性（与 migrations 中的 CHECK 约束一致）。
const (
	PostStatusDraft     = "draft"
	PostStatusScheduled = "scheduled"
	PostStatusPublished = "published"
	PostStatusDeleted   = "deleted"

	VisibilityPublic    = "public"
	VisibilityFollowers = "followers"
	VisibilityPrivate   = "private"
)

// 评论状态。
const (
	CommentStatusPublished = "published"
	CommentStatusDeleted   = "deleted"
)

// 附件与衍生文件状态。
const (
	AttachmentAvailable = "available"
	AttachmentDeleted   = "deleted"

	DerivativeThumbnail  = "thumbnail"
	DerivativeVideoCover = "video_cover"

	DerivativePending    = "pending"
	DerivativeProcessing = "processing"
	DerivativeReady      = "ready"
	DerivativeFailed     = "failed"
	DerivativeCanceled   = "canceled"
)

// 关注申请状态。
const (
	FollowRequestPending  = "pending"
	FollowRequestApproved = "approved"
	FollowRequestRejected = "rejected"
	FollowRequestCanceled = "canceled"
)

// 关注状态（面向视图）。
const (
	FollowStateNone            = "none"
	FollowStateFollowing       = "following"
	FollowStatePending         = "pending"
	FollowStateIncomingPending = "incoming_pending"
)

// Viewer 是一次请求的访问者身份。未登录时为游客。
type Viewer struct {
	ID            int64
	IsAdmin       bool
	IsAI          bool
	IsSuspended   bool
	Authenticated bool
}

// ViewerFrom 把登录账号映射为访问者；未登录使用 GuestViewer。
func ViewerFrom(u auth.User) Viewer {
	return Viewer{
		ID:            u.ID,
		IsAdmin:       u.Role == auth.RoleAdmin && !u.IsAI,
		IsAI:          u.IsAI,
		IsSuspended:   u.IsSuspended(),
		Authenticated: true,
	}
}

// GuestViewer 返回游客访问者。
func GuestViewer() Viewer { return Viewer{} }

// CanWrite 表示该访问者可以执行写操作（已登录、非 AI、未停用）。
func (v Viewer) CanWrite() bool { return v.Authenticated && !v.IsAI && !v.IsSuspended }

type Row interface {
	Scan(dest ...any) error
}

// UserBrief 是多处复用的作者摘要。
type UserBrief struct {
	ID          int64
	Username    string
	Nickname    string
	AvatarURL   string
	IsAI        bool
	IsSuspended bool
}

// UserBriefResponse 是作者摘要的 JSON 形态。
type UserBriefResponse struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	Nickname    string `json:"nickname"`
	AvatarURL   string `json:"avatar_url"`
	IsAI        bool   `json:"is_ai"`
	IsSuspended bool   `json:"is_suspended"`
}

func (b UserBrief) Response() UserBriefResponse {
	return UserBriefResponse{
		ID:          api.FormatID(b.ID),
		Username:    b.Username,
		Nickname:    b.Nickname,
		AvatarURL:   b.AvatarURL,
		IsAI:        b.IsAI,
		IsSuspended: b.IsSuspended,
	}
}

// UserBriefFromAuth 复用账号模型的字段。
func UserBriefFromAuth(u auth.User) UserBrief {
	return UserBrief{
		ID:          u.ID,
		Username:    u.Username,
		Nickname:    u.Nickname,
		AvatarURL:   u.AvatarURL,
		IsAI:        u.IsAI,
		IsSuspended: u.IsSuspended(),
	}
}

// Post 对应 timeline.posts。
type Post struct {
	ID               int64
	AuthorID         int64
	Status           string
	Visibility       string
	CurrentVersionID *int64
	QuotedPostID     *int64
	ScheduledAt      *time.Time
	PublishedAt      *time.Time
	DeletedAt        *time.Time
	LikeCount        int
	CommentCount     int
	RepostCount      int
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

const PostColumns = `id, author_id, status, visibility, current_version_id, quoted_post_id,
	scheduled_at, published_at, deleted_at, like_count, comment_count, repost_count,
	created_at, updated_at`

func ScanPost(row Row) (Post, error) {
	var p Post
	err := row.Scan(
		&p.ID, &p.AuthorID, &p.Status, &p.Visibility, &p.CurrentVersionID, &p.QuotedPostID,
		&p.ScheduledAt, &p.PublishedAt, &p.DeletedAt, &p.LikeCount, &p.CommentCount,
		&p.RepostCount, &p.CreatedAt, &p.UpdatedAt,
	)
	return p, err
}

// PostVersion 对应 timeline.post_versions，只追加不修改。
type PostVersion struct {
	ID                    int64
	PostID                int64
	VersionNumber         int
	ContentFormat         string
	ContentSource         string
	ContentHTML           string
	PlainText             string
	Visibility            string
	RestoredFromVersionID *int64
	CreatedBy             int64
	CreatedAt             time.Time
}

const PostVersionColumns = `id, post_id, version_number, content_format, content_source,
	content_html, plain_text, visibility, restored_from_version_id, created_by, created_at`

func ScanPostVersion(row Row) (PostVersion, error) {
	var v PostVersion
	err := row.Scan(
		&v.ID, &v.PostID, &v.VersionNumber, &v.ContentFormat, &v.ContentSource,
		&v.ContentHTML, &v.PlainText, &v.Visibility, &v.RestoredFromVersionID,
		&v.CreatedBy, &v.CreatedAt,
	)
	return v, err
}

// Comment 对应 timeline.comments。
type Comment struct {
	ID              int64
	PostID          int64
	AuthorID        int64
	ParentCommentID *int64
	RootCommentID   *int64
	ContentFormat   string
	ContentSource   string
	ContentHTML     string
	PlainText       string
	Status          string
	DeletedByAdmin  bool
	DeletedByAuthor bool
	LikeCount       int
	ReplyCount      int
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

const CommentColumns = `id, post_id, author_id, parent_comment_id, root_comment_id,
	content_format, content_source, content_html, plain_text, status,
	deleted_by_admin, deleted_by_author, like_count, reply_count, created_at, updated_at`

func ScanComment(row Row) (Comment, error) {
	var c Comment
	err := row.Scan(
		&c.ID, &c.PostID, &c.AuthorID, &c.ParentCommentID, &c.RootCommentID,
		&c.ContentFormat, &c.ContentSource, &c.ContentHTML, &c.PlainText, &c.Status,
		&c.DeletedByAdmin, &c.DeletedByAuthor, &c.LikeCount, &c.ReplyCount,
		&c.CreatedAt, &c.UpdatedAt,
	)
	return c, err
}

// Deleted 表示评论已逻辑删除。
func (c Comment) Deleted() bool { return c.Status == CommentStatusDeleted }

// Attachment 对应 timeline.attachments。物理文件永不删除，status 只做逻辑失效。
type Attachment struct {
	ID               int64
	UploaderID       int64
	PostID           *int64
	Kind             string
	OriginalFilename string
	MediaType        string
	SizeBytes        int64
	StorageKey       string
	Status           string
	Width            *int
	Height           *int
	DurationMS       *int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

const AttachmentColumns = `id, uploader_id, post_id, kind, original_filename, media_type,
	size_bytes, storage_key, status, width, height, duration_ms, created_at, updated_at`

func ScanAttachment(row Row) (Attachment, error) {
	var a Attachment
	err := row.Scan(
		&a.ID, &a.UploaderID, &a.PostID, &a.Kind, &a.OriginalFilename, &a.MediaType,
		&a.SizeBytes, &a.StorageKey, &a.Status, &a.Width, &a.Height, &a.DurationMS,
		&a.CreatedAt, &a.UpdatedAt,
	)
	return a, err
}

// Available 表示附件仍然有效。
func (a Attachment) Available() bool { return a.Status == AttachmentAvailable }

// ContentURL 是附件内容的鉴权访问路径。
func (a Attachment) ContentURL() string {
	return "/api/v1/attachments/" + api.FormatID(a.ID) + "/content"
}

// AttachmentResponse 是附件对外 DTO。
type AttachmentResponse struct {
	ID               string            `json:"id"`
	Kind             string            `json:"kind"`
	OriginalFilename string            `json:"original_filename"`
	MediaType        string            `json:"media_type"`
	SizeBytes        int64             `json:"size_bytes"`
	Width            *int              `json:"width"`
	Height           *int              `json:"height"`
	DurationMS       *int64            `json:"duration_ms"`
	ContentURL       string            `json:"content_url"`
	ThumbnailURL     string            `json:"thumbnail_url,omitempty"`
	Derivatives      map[string]string `json:"derivatives"`
	Status           string            `json:"status"`
	CreatedAt        string            `json:"created_at"`
}

// AttachmentResponseFor 组装附件 DTO；derivatives 是 derivative_type 到状态的映射。
func AttachmentResponseFor(a Attachment, derivatives map[string]string) AttachmentResponse {
	resp := AttachmentResponse{
		ID:               api.FormatID(a.ID),
		Kind:             a.Kind,
		OriginalFilename: a.OriginalFilename,
		MediaType:        a.MediaType,
		SizeBytes:        a.SizeBytes,
		Width:            a.Width,
		Height:           a.Height,
		DurationMS:       a.DurationMS,
		ContentURL:       a.ContentURL(),
		Derivatives:      derivatives,
		Status:           a.Status,
		CreatedAt:        api.FormatTime(a.CreatedAt),
	}
	if resp.Derivatives == nil {
		resp.Derivatives = map[string]string{}
	}
	if resp.Derivatives[DerivativeThumbnail] == DerivativeReady {
		resp.ThumbnailURL = "/api/v1/attachments/" + api.FormatID(a.ID) + "/derivatives/" + DerivativeThumbnail
	}
	return resp
}

// SelfPosted 判断访问者是否为该帖作者。
func (p Post) SelfPosted(viewer Viewer) bool {
	return viewer.Authenticated && viewer.ID == p.AuthorID
}
