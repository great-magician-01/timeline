// Package view 定义跨模块复用的 HTTP 响应 DTO 与构造辅助。
// JSON 字段与旧 internal/model、internal/auth 的对外形状保持一致；本包不依赖 gin，
// 由各 modules/*/http 在最终响应处使用。只 import shared/model 与 shared/format。
package view

import (
	"github.com/great-magician-01/timeline/internal/shared/format"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// UserBriefResponse 是作者摘要的 JSON 形态。
type UserBriefResponse struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	Nickname    string `json:"nickname"`
	AvatarURL   string `json:"avatar_url"`
	IsAI        bool   `json:"is_ai"`
	IsSuspended bool   `json:"is_suspended"`
}

// UserBriefResponseFor 组装作者摘要 DTO。
func UserBriefResponseFor(brief model.UserBrief) UserBriefResponse {
	return UserBriefResponse{
		ID:          format.FormatID(brief.ID),
		Username:    brief.Username,
		Nickname:    brief.Nickname,
		AvatarURL:   brief.AvatarURL,
		IsAI:        brief.IsAI,
		IsSuspended: brief.IsSuspended,
	}
}

// UserBriefResponsesFor 批量组装作者摘要 DTO，保持输入顺序。
func UserBriefResponsesFor(briefs []model.UserBrief) []UserBriefResponse {
	items := make([]UserBriefResponse, 0, len(briefs))
	for _, brief := range briefs {
		items = append(items, UserBriefResponseFor(brief))
	}
	return items
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
func AttachmentResponseFor(attachment model.Attachment, derivatives map[string]string) AttachmentResponse {
	resp := AttachmentResponse{
		ID:               format.FormatID(attachment.ID),
		Kind:             attachment.Kind,
		OriginalFilename: attachment.OriginalFilename,
		MediaType:        attachment.MediaType,
		SizeBytes:        attachment.SizeBytes,
		Width:            attachment.Width,
		Height:           attachment.Height,
		DurationMS:       attachment.DurationMS,
		ContentURL:       format.AttachmentContentURL(attachment.ID),
		Derivatives:      derivatives,
		Status:           attachment.Status,
		CreatedAt:        format.FormatTime(attachment.CreatedAt),
	}
	if resp.Derivatives == nil {
		resp.Derivatives = map[string]string{}
	}
	if resp.Derivatives[model.DerivativeThumbnail] == model.DerivativeReady {
		resp.ThumbnailURL = format.ThumbnailURL(attachment.ID)
	}
	return resp
}

// UserResponse 是账号接口的安全 DTO。私有字段只在本人或管理员响应中填充。
type UserResponse struct {
	ID                    string `json:"id"`
	Username              string `json:"username"`
	Nickname              string `json:"nickname"`
	AvatarURL             string `json:"avatar_url"`
	BannerURL             string `json:"banner_url"`
	Bio                   string `json:"bio"`
	IsAI                  bool   `json:"is_ai"`
	IsSuspended           bool   `json:"is_suspended"`
	CreatedAt             string `json:"created_at"`
	Email                 string `json:"email,omitempty"`
	Role                  string `json:"role,omitempty"`
	IsSuperAdmin          bool   `json:"is_super_admin,omitempty"`
	Status                string `json:"status,omitempty"`
	RequireFollowApproval *bool  `json:"require_follow_approval,omitempty"`
	UpdatedAt             string `json:"updated_at,omitempty"`
}

// PublicUserResponse 组装公开账号 DTO（不含邮箱、角色与状态等私有字段）。
func PublicUserResponse(u model.User) UserResponse {
	return UserResponse{
		ID:          format.FormatID(u.ID),
		Username:    u.Username,
		Nickname:    u.Nickname,
		AvatarURL:   u.AvatarURL,
		BannerURL:   u.BannerURL,
		Bio:         u.Bio,
		IsAI:        u.IsAI,
		IsSuspended: u.IsSuspended(),
		CreatedAt:   format.FormatTime(u.CreatedAt),
	}
}

// PrivateUserResponse 组装本人/管理员可见的账号 DTO。
func PrivateUserResponse(u model.User) UserResponse {
	resp := PublicUserResponse(u)
	resp.Email = u.Email
	resp.Role = u.Role
	resp.IsSuperAdmin = u.IsSuperAdmin
	resp.Status = u.Status
	requireFollowApproval := u.RequireFollowApproval
	resp.RequireFollowApproval = &requireFollowApproval
	resp.UpdatedAt = format.FormatTime(u.UpdatedAt)
	return resp
}
