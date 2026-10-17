package http

import (
	"encoding/json"

	"github.com/great-magician-01/timeline/internal/modules/admin/app"
	"github.com/great-magician-01/timeline/internal/shared/format"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/shared/view"
	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// ===== 概览 =====

// OverviewView 是管理概览的 JSON 形状（与旧接口一致）。
type OverviewView struct {
	Users struct {
		Total     int64 `json:"total"`
		Suspended int64 `json:"suspended"`
		AI        int64 `json:"ai"`
	} `json:"users"`
	Content struct {
		Posts    int64 `json:"posts"`
		Comments int64 `json:"comments"`
		Drafts   int64 `json:"drafts"`
	} `json:"content"`
	AI struct {
		Enabled       int64 `json:"enabled"`
		Total         int64 `json:"total"`
		GlobalEnabled bool  `json:"global_enabled"`
	} `json:"ai"`
	Tasks struct {
		Pending int64 `json:"pending"`
		Failed  int64 `json:"failed"`
		Paused  int64 `json:"paused"`
	} `json:"tasks"`
}

func overviewView(overview app.Overview) OverviewView {
	var out OverviewView
	out.Users.Total = overview.Counts.UsersTotal
	out.Users.Suspended = overview.Counts.UsersSuspended
	out.Users.AI = overview.Counts.UsersAI
	out.Content.Posts = overview.Counts.PostsPublished
	out.Content.Comments = overview.Counts.CommentsPublished
	out.Content.Drafts = overview.Counts.PostsDraftScheduled
	out.AI.Enabled = overview.Counts.AIEnabled
	out.AI.Total = overview.Counts.AITotal
	out.AI.GlobalEnabled = overview.AIGlobalEnabled
	out.Tasks.Pending = overview.Counts.JobsPending
	out.Tasks.Failed = overview.Counts.JobsFailed
	out.Tasks.Paused = overview.Counts.JobsPaused
	return out
}

// ===== 用户 =====

// UserStatsView 是用户详情页的内容与关系统计。
type UserStatsView struct {
	Posts         int64 `json:"posts"`
	Comments      int64 `json:"comments"`
	Followers     int64 `json:"followers"`
	Following     int64 `json:"following"`
	LikesGiven    int64 `json:"likes_given"`
	LikesReceived int64 `json:"likes_received"`
	Reposts       int64 `json:"reposts"`
}

func userStatsView(stats app.UserStats) UserStatsView {
	return UserStatsView{
		Posts:         stats.Posts,
		Comments:      stats.Comments,
		Followers:     stats.Followers,
		Following:     stats.Following,
		LikesGiven:    stats.LikesGiven,
		LikesReceived: stats.LikesReceived,
		Reposts:       stats.Reposts,
	}
}

// userResponseFor 组装管理端可见的私有账号 DTO（含邮箱/角色/超管标记/状态）。
func userResponseFor(u model.User) view.UserResponse {
	return view.PrivateUserResponse(u)
}

// ===== 帖子审核 =====

// AdminPostView 是后台帖子列表项。
type AdminPostView struct {
	ID              string                 `json:"id"`
	Author          view.UserBriefResponse `json:"author"`
	Status          string                 `json:"status"`
	Visibility      string                 `json:"visibility"`
	ContentHTML     string                 `json:"content_html"`
	PublishedAt     *string                `json:"published_at"`
	CreatedAt       string                 `json:"created_at"`
	LikeCount       int                    `json:"like_count"`
	CommentCount    int                    `json:"comment_count"`
	RepostCount     int                    `json:"repost_count"`
	AttachmentCount int                    `json:"attachment_count"`
}

func adminPostView(item app.PostItem) AdminPostView {
	out := AdminPostView{
		ID:              httpx.FormatID(item.Post.ID),
		Author:          view.UserBriefResponseFor(item.Author),
		Status:          item.Post.Status,
		Visibility:      item.Post.Visibility,
		ContentHTML:     item.ContentHTML,
		CreatedAt:       httpx.FormatTime(item.Post.CreatedAt),
		LikeCount:       item.Post.LikeCount,
		CommentCount:    item.Post.CommentCount,
		RepostCount:     item.Post.RepostCount,
		AttachmentCount: item.AttachmentCount,
	}
	if item.Post.PublishedAt != nil {
		published := httpx.FormatTime(*item.Post.PublishedAt)
		out.PublishedAt = &published
	}
	return out
}

// AdminCommentView 是后台评论列表项。
type AdminCommentView struct {
	ID          string                 `json:"id"`
	PostID      string                 `json:"post_id"`
	Author      view.UserBriefResponse `json:"author"`
	ContentHTML string                 `json:"content_html"`
	Status      string                 `json:"status"`
	CreatedAt   string                 `json:"created_at"`
}

func adminCommentView(item app.CommentItem) AdminCommentView {
	return AdminCommentView{
		ID:          httpx.FormatID(item.ID),
		PostID:      httpx.FormatID(item.PostID),
		Author:      view.UserBriefResponseFor(item.Author),
		ContentHTML: item.ContentHTML,
		Status:      item.Status,
		CreatedAt:   httpx.FormatTime(item.CreatedAt),
	}
}

// PostDetailView 是帖子审核详情响应。
type PostDetailView struct {
	Post         PostDetailPostView     `json:"post"`
	Versions     []VersionView          `json:"versions"`
	Attachments  []DetailAttachmentView `json:"attachments"`
	Comments     []AdminCommentView     `json:"comments"`
	CommentCount int64                  `json:"comment_count"`
}

// PostDetailPostView 是详情中的帖子对象。
type PostDetailPostView struct {
	ID           string                 `json:"id"`
	Author       view.UserBriefResponse `json:"author"`
	Status       string                 `json:"status"`
	Visibility   string                 `json:"visibility"`
	ContentHTML  string                 `json:"content_html"`
	CreatedAt    string                 `json:"created_at"`
	UpdatedAt    string                 `json:"updated_at"`
	LikeCount    int                    `json:"like_count"`
	CommentCount int                    `json:"comment_count"`
	RepostCount  int                    `json:"repost_count"`
	PublishedAt  *string                `json:"published_at,omitempty"`
	ScheduledAt  *string                `json:"scheduled_at,omitempty"`
	DeletedAt    *string                `json:"deleted_at,omitempty"`
}

// VersionView 是帖子历史版本行。
type VersionView struct {
	ID                    string  `json:"id"`
	VersionNumber         int     `json:"version_number"`
	Visibility            string  `json:"visibility"`
	ContentHTML           string  `json:"content_html"`
	CreatedAt             string  `json:"created_at"`
	RestoredFromVersionID *string `json:"restored_from_version_id,omitempty"`
}

// DetailAttachmentView 是详情中的附件行（旧接口不含上传者）。
type DetailAttachmentView struct {
	ID               string `json:"id"`
	Kind             string `json:"kind"`
	OriginalFilename string `json:"original_filename"`
	MediaType        string `json:"media_type"`
	SizeBytes        int64  `json:"size_bytes"`
	Status           string `json:"status"`
	CreatedAt        string `json:"created_at"`
}

func postDetailView(detail app.PostDetail) PostDetailView {
	post := detail.Post
	out := PostDetailView{
		Post: PostDetailPostView{
			ID:           httpx.FormatID(post.Post.ID),
			Author:       view.UserBriefResponseFor(post.Author),
			Status:       post.Post.Status,
			Visibility:   post.Post.Visibility,
			ContentHTML:  detail.ContentHTML,
			CreatedAt:    httpx.FormatTime(post.Post.CreatedAt),
			UpdatedAt:    httpx.FormatTime(post.Post.UpdatedAt),
			LikeCount:    post.Post.LikeCount,
			CommentCount: int(detail.CommentCount),
			RepostCount:  post.Post.RepostCount,
		},
		Versions:     make([]VersionView, 0, len(detail.Versions)),
		Attachments:  make([]DetailAttachmentView, 0, len(detail.Attachments)),
		Comments:     make([]AdminCommentView, 0, len(detail.Comments)),
		CommentCount: detail.CommentCount,
	}
	if post.Post.PublishedAt != nil {
		value := httpx.FormatTime(*post.Post.PublishedAt)
		out.Post.PublishedAt = &value
	}
	if post.Post.ScheduledAt != nil {
		value := httpx.FormatTime(*post.Post.ScheduledAt)
		out.Post.ScheduledAt = &value
	}
	if post.Post.DeletedAt != nil {
		value := httpx.FormatTime(*post.Post.DeletedAt)
		out.Post.DeletedAt = &value
	}
	for _, item := range detail.Versions {
		version := VersionView{
			ID:            httpx.FormatID(item.ID),
			VersionNumber: item.VersionNumber,
			Visibility:    item.Visibility,
			ContentHTML:   item.ContentHTML,
			CreatedAt:     httpx.FormatTime(item.CreatedAt),
		}
		if item.RestoredFromVersionID != nil {
			id := httpx.FormatID(*item.RestoredFromVersionID)
			version.RestoredFromVersionID = &id
		}
		out.Versions = append(out.Versions, version)
	}
	for _, item := range detail.Attachments {
		out.Attachments = append(out.Attachments, DetailAttachmentView{
			ID:               httpx.FormatID(item.ID),
			Kind:             item.Kind,
			OriginalFilename: item.OriginalFilename,
			MediaType:        item.MediaType,
			SizeBytes:        item.SizeBytes,
			Status:           item.Status,
			CreatedAt:        httpx.FormatTime(item.CreatedAt),
		})
	}
	for _, item := range detail.Comments {
		out.Comments = append(out.Comments, adminCommentView(item))
	}
	return out
}

// ===== 附件审核 =====

// AdminAttachmentView 是后台附件列表项；含上传者与所属内容，便于审核处置。
type AdminAttachmentView struct {
	ID               string                 `json:"id"`
	Uploader         view.UserBriefResponse `json:"uploader"`
	PostID           *string                `json:"post_id"`
	Kind             string                 `json:"kind"`
	Purpose          string                 `json:"purpose"`
	OriginalFilename string                 `json:"original_filename"`
	MediaType        string                 `json:"media_type"`
	SizeBytes        int64                  `json:"size_bytes"`
	Width            *int                   `json:"width"`
	Height           *int                   `json:"height"`
	DurationMS       *int64                 `json:"duration_ms"`
	Status           string                 `json:"status"`
	ContentURL       string                 `json:"content_url"`
	CreatedAt        string                 `json:"created_at"`
	UpdatedAt        string                 `json:"updated_at"`
}

func adminAttachmentView(item app.AttachmentItem) AdminAttachmentView {
	return AdminAttachmentView{
		ID:               httpx.FormatID(item.ID),
		Uploader:         view.UserBriefResponseFor(item.Uploader),
		PostID:           optionalID(item.PostID),
		Kind:             item.Kind,
		Purpose:          item.Purpose,
		OriginalFilename: item.OriginalFilename,
		MediaType:        item.MediaType,
		SizeBytes:        item.SizeBytes,
		Width:            item.Width,
		Height:           item.Height,
		DurationMS:       item.DurationMS,
		Status:           item.Status,
		ContentURL:       format.AttachmentContentURL(item.ID),
		CreatedAt:        httpx.FormatTime(item.CreatedAt),
		UpdatedAt:        httpx.FormatTime(item.UpdatedAt),
	}
}

// ===== 后台作业 =====

// JobView 是后台作业的 JSON 形状。
type JobView struct {
	ID          string  `json:"id"`
	JobType     string  `json:"job_type"`
	RefType     string  `json:"ref_type"`
	RefID       *string `json:"ref_id,omitempty"`
	Status      string  `json:"status"`
	RunAt       string  `json:"run_at"`
	Attempts    int     `json:"attempts"`
	MaxAttempts int     `json:"max_attempts"`
	LastError   string  `json:"last_error"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

func jobViewFromTask(item app.TaskItem) JobView {
	return JobView{
		ID:          httpx.FormatID(item.ID),
		JobType:     item.JobType,
		RefType:     item.RefType,
		RefID:       optionalID(item.RefID),
		Status:      item.Status,
		RunAt:       httpx.FormatTime(item.RunAt),
		Attempts:    item.Attempts,
		MaxAttempts: item.MaxAttempts,
		LastError:   item.LastError,
		CreatedAt:   httpx.FormatTime(item.CreatedAt),
		UpdatedAt:   httpx.FormatTime(item.UpdatedAt),
	}
}

// jobViewFromSnapshot 把作业端口快照映射为 JSON 形状（人工处置结果）。
func jobViewFromSnapshot(snapshot app.JobSnapshot) JobView {
	return JobView{
		ID:          httpx.FormatID(snapshot.ID),
		JobType:     snapshot.Type,
		RefType:     snapshot.RefType,
		RefID:       optionalID(snapshot.RefID),
		Status:      snapshot.Status,
		RunAt:       httpx.FormatTime(snapshot.RunAt),
		Attempts:    snapshot.Attempts,
		MaxAttempts: snapshot.MaxAttempts,
		LastError:   snapshot.LastError,
		CreatedAt:   httpx.FormatTime(snapshot.CreatedAt),
		UpdatedAt:   httpx.FormatTime(snapshot.UpdatedAt),
	}
}

// ===== 审计日志 =====

// AuditAdminView 是审计日志中的管理员摘要。
type AuditAdminView struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Nickname string `json:"nickname"`
}

// AuditLogView 是审计日志列表项。
type AuditLogView struct {
	ID         string          `json:"id"`
	Admin      AuditAdminView  `json:"admin"`
	ActionType string          `json:"action_type"`
	TargetType string          `json:"target_type"`
	TargetID   string          `json:"target_id,omitempty"`
	Detail     json.RawMessage `json:"detail"`
	CreatedAt  string          `json:"created_at"`
}

func auditLogView(item app.AuditItem) AuditLogView {
	detail := json.RawMessage(item.Detail)
	if len(detail) == 0 {
		detail = json.RawMessage("{}")
	}
	out := AuditLogView{
		ID: httpx.FormatID(item.ID),
		Admin: AuditAdminView{
			ID:       httpx.FormatID(item.AdminID),
			Username: item.Username,
			Nickname: item.Nickname,
		},
		ActionType: item.ActionType,
		TargetType: item.TargetType,
		Detail:     detail,
		CreatedAt:  httpx.FormatTime(item.CreatedAt),
	}
	if item.TargetID != nil {
		out.TargetID = httpx.FormatID(*item.TargetID)
	}
	return out
}

// ===== 系统设置与网页搜索 =====

// settingsView 把设置快照转为管理接口字段（与旧 settings.Values.ToMap 形状一致）。
func settingsView(values app.SettingsSnapshot) map[string]any {
	return map[string]any{
		"registration_enabled":            values.RegistrationEnabled,
		"ai_global_enabled":               values.AIGlobalEnabled,
		"max_upload_bytes":                values.MaxUploadBytes,
		"max_request_bytes":               values.MaxRequestBytes,
		"storage_quota_bytes":             values.StorageQuotaBytes,
		"post_rate_limit":                 values.PostRateLimit,
		"post_rate_window_seconds":        int(values.PostRateWindow.Seconds()),
		"upload_rate_limit":               values.UploadRateLimit,
		"upload_rate_window_seconds":      int(values.UploadRateWindow.Seconds()),
		"temp_attachment_retention_hours": int(values.TempAttachmentRetention.Hours()),
		"call_log_max_bytes":              values.CallLogMaxBytes,
		"call_log_retention_days":         values.CallLogRetentionDays,
	}
}

// WebSearchView 是全局网页搜索配置的 JSON 形状；永不返回明文或密文。
type WebSearchView struct {
	BaseURL      string `json:"base_url"`
	Configured   bool   `json:"configured"`
	Usable       bool   `json:"usable"`
	APIKeyMasked string `json:"api_key_masked"`
}

func webSearchView(snapshot app.WebSearchSnapshot) WebSearchView {
	return WebSearchView{
		BaseURL:      snapshot.BaseURL,
		Configured:   snapshot.Configured,
		Usable:       snapshot.Usable,
		APIKeyMasked: snapshot.APIKeyMasked,
	}
}

// optionalID 把可空雪花 ID 序列化为字符串；空值输出 null。
func optionalID(value *int64) *string {
	if value == nil {
		return nil
	}
	formatted := httpx.FormatID(*value)
	return &formatted
}
