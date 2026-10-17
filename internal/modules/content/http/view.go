package http

import (
	"github.com/great-magician-01/timeline/internal/modules/content/app"
	"github.com/great-magician-01/timeline/internal/shared/format"
	"github.com/great-magician-01/timeline/internal/shared/view"
)

// viewerStateResponse 是访问者对内容的互动状态（JSON 形状与旧实现一致）。
type viewerStateResponse struct {
	Liked    bool `json:"liked"`
	Reposted bool `json:"reposted"`
}

// quotedPostResponse 是引用转发的原帖占位。
type quotedPostResponse struct {
	Available bool               `json:"available"`
	Post      *postBriefResponse `json:"post,omitempty"`
}

// postBriefResponse 是时间线、搜索、标签聚合与嵌套引用共用的列表项 DTO。
type postBriefResponse struct {
	ID           string                    `json:"id"`
	Kind         string                    `json:"kind"`
	Author       view.UserBriefResponse    `json:"author"`
	Visibility   string                    `json:"visibility"`
	ContentHTML  string                    `json:"content_html"`
	PublishedAt  *string                   `json:"published_at"`
	RepostedAt   *string                   `json:"reposted_at,omitempty"`
	Edited       bool                      `json:"edited"`
	LikeCount    int                       `json:"like_count"`
	CommentCount int                       `json:"comment_count"`
	RepostCount  int                       `json:"repost_count"`
	RepostOf     *postBriefResponse        `json:"repost_of,omitempty"`
	QuotedPost   *quotedPostResponse       `json:"quoted_post,omitempty"`
	Attachments  []view.AttachmentResponse `json:"attachments"`
	ViewerState  viewerStateResponse       `json:"viewer_state"`
}

// postDetailResponse 是帖子详情 DTO：在列表项基础上补充作者操作与历史访问信息。
type postDetailResponse struct {
	postBriefResponse
	Status            string  `json:"status"`
	ContentFormat     string  `json:"content_format"`
	ScheduledAt       *string `json:"scheduled_at,omitempty"`
	Revision          int     `json:"revision,omitempty"`
	VersionNumber     int     `json:"version_number,omitempty"`
	CanEdit           bool    `json:"can_edit"`
	CanDelete         bool    `json:"can_delete"`
	CanViewHistory    bool    `json:"can_view_history"`
	AuthorFollowState string  `json:"author_follow_state,omitempty"`
}

// versionItemResponse 是历史版本列表项 DTO。
type versionItemResponse struct {
	ID                    string  `json:"id"`
	VersionNumber         int     `json:"version_number"`
	Visibility            string  `json:"visibility"`
	CreatedAt             string  `json:"created_at"`
	RestoredFromVersionID *string `json:"restored_from_version_id,omitempty"`
	Summary               string  `json:"summary"`
}

// versionDetailResponse 是历史版本详情 DTO。
type versionDetailResponse struct {
	ID                    string                    `json:"id"`
	VersionNumber         int                       `json:"version_number"`
	ContentFormat         string                    `json:"content_format"`
	ContentSource         string                    `json:"content_source"`
	ContentHTML           string                    `json:"content_html"`
	Visibility            string                    `json:"visibility"`
	CreatedAt             string                    `json:"created_at"`
	CreatedBy             string                    `json:"created_by"`
	RestoredFromVersionID *string                   `json:"restored_from_version_id,omitempty"`
	Attachments           []view.AttachmentResponse `json:"attachments"`
}

// draftItemResponse 是「我的草稿 / 我的定时发布」列表项 DTO。
type draftItemResponse struct {
	ID              string  `json:"id"`
	ContentHTML     string  `json:"content_html"`
	PlainText       string  `json:"plain_text"`
	Visibility      string  `json:"visibility"`
	Revision        int     `json:"revision"`
	Status          string  `json:"status"`
	UpdatedAt       string  `json:"updated_at"`
	ScheduledAt     *string `json:"scheduled_at,omitempty"`
	AttachmentCount int     `json:"attachment_count"`
	PublishFailed   *bool   `json:"publish_failed,omitempty"`
	PublishError    *string `json:"publish_error,omitempty"`
}

// commentTimelineItemResponse 是个人主页「回复」页签列表项 DTO。
type commentTimelineItemResponse struct {
	Kind        string                 `json:"kind"`
	ID          string                 `json:"id"`
	Author      view.UserBriefResponse `json:"author"`
	ContentHTML string                 `json:"content_html"`
	CreatedAt   string                 `json:"created_at"`
	Post        *postBriefResponse     `json:"post"`
}

// tagSuggestionResponse 是标签自动补全项 DTO。
type tagSuggestionResponse struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	PostCount   int    `json:"post_count"`
}

// ==== 映射函数 ====

func briefResponseFor(brief app.PostBrief) postBriefResponse {
	resp := postBriefResponse{
		ID:           format.FormatID(brief.ID),
		Kind:         brief.Kind,
		Author:       view.UserBriefResponseFor(brief.Author),
		Visibility:   brief.Visibility,
		ContentHTML:  brief.ContentHTML,
		PublishedAt:  format.FormatTimePtr(brief.PublishedAt),
		RepostedAt:   format.FormatTimePtr(brief.RepostedAt),
		Edited:       brief.Edited,
		LikeCount:    brief.LikeCount,
		CommentCount: brief.CommentCount,
		RepostCount:  brief.RepostCount,
		Attachments:  attachmentResponsesFor(brief.Attachments),
		ViewerState: viewerStateResponse{
			Liked:    brief.ViewerState.Liked,
			Reposted: brief.ViewerState.Reposted,
		},
	}
	if brief.RepostOf != nil {
		nested := briefResponseFor(*brief.RepostOf)
		resp.RepostOf = &nested
	}
	if brief.QuotedPost != nil {
		quoted := quotedPostResponse{Available: brief.QuotedPost.Available}
		if brief.QuotedPost.Post != nil {
			nested := briefResponseFor(*brief.QuotedPost.Post)
			quoted.Post = &nested
		}
		resp.QuotedPost = &quoted
	}
	return resp
}

// briefResponsesFor 批量映射列表项，返回空切片而不是 nil（JSON 输出 []）。
func briefResponsesFor(briefs []app.PostBrief) []postBriefResponse {
	items := make([]postBriefResponse, 0, len(briefs))
	for _, brief := range briefs {
		items = append(items, briefResponseFor(brief))
	}
	return items
}

func detailResponseFor(detail app.PostDetail) postDetailResponse {
	return postDetailResponse{
		postBriefResponse: briefResponseFor(detail.PostBrief),
		Status:            detail.Status,
		ContentFormat:     detail.ContentFormat,
		ScheduledAt:       format.FormatTimePtr(detail.ScheduledAt),
		Revision:          detail.Revision,
		VersionNumber:     detail.VersionNumber,
		CanEdit:           detail.CanEdit,
		CanDelete:         detail.CanDelete,
		CanViewHistory:    detail.CanViewHistory,
		AuthorFollowState: detail.AuthorFollowState,
	}
}

func versionItemResponsesFor(items []app.VersionItem) []versionItemResponse {
	out := make([]versionItemResponse, 0, len(items))
	for _, item := range items {
		out = append(out, versionItemResponse{
			ID:                    format.FormatID(item.ID),
			VersionNumber:         item.VersionNumber,
			Visibility:            item.Visibility,
			CreatedAt:             format.FormatTime(item.CreatedAt),
			RestoredFromVersionID: formatIDPtr(item.RestoredFromVersionID),
			Summary:               item.Summary,
		})
	}
	return out
}

func versionDetailResponseFor(detail app.VersionDetail) versionDetailResponse {
	return versionDetailResponse{
		ID:                    format.FormatID(detail.ID),
		VersionNumber:         detail.VersionNumber,
		ContentFormat:         detail.ContentFormat,
		ContentSource:         detail.ContentSource,
		ContentHTML:           detail.ContentHTML,
		Visibility:            detail.Visibility,
		CreatedAt:             format.FormatTime(detail.CreatedAt),
		CreatedBy:             format.FormatID(detail.CreatedBy),
		RestoredFromVersionID: formatIDPtr(detail.RestoredFromVersionID),
		Attachments:           attachmentResponsesFor(detail.Attachments),
	}
}

func draftItemResponsesFor(items []app.DraftItem) []draftItemResponse {
	out := make([]draftItemResponse, 0, len(items))
	for _, item := range items {
		out = append(out, draftItemResponse{
			ID:              format.FormatID(item.ID),
			ContentHTML:     item.ContentHTML,
			PlainText:       item.PlainText,
			Visibility:      item.Visibility,
			Revision:        item.Revision,
			Status:          item.Status,
			UpdatedAt:       format.FormatTime(item.UpdatedAt),
			ScheduledAt:     format.FormatTimePtr(item.ScheduledAt),
			AttachmentCount: item.AttachmentCount,
			PublishFailed:   item.PublishFailed,
			PublishError:    item.PublishError,
		})
	}
	return out
}

func commentTimelineResponsesFor(items []app.CommentTimelineItem) []commentTimelineItemResponse {
	out := make([]commentTimelineItemResponse, 0, len(items))
	for _, item := range items {
		resp := commentTimelineItemResponse{
			Kind:        item.Kind,
			ID:          format.FormatID(item.ID),
			Author:      view.UserBriefResponseFor(item.Author),
			ContentHTML: item.ContentHTML,
			CreatedAt:   format.FormatTime(item.CreatedAt),
		}
		if item.Post != nil {
			nested := briefResponseFor(*item.Post)
			resp.Post = &nested
		}
		out = append(out, resp)
	}
	return out
}

func tagSuggestionResponsesFor(items []app.TagSuggestion) []tagSuggestionResponse {
	out := make([]tagSuggestionResponse, 0, len(items))
	for _, item := range items {
		out = append(out, tagSuggestionResponse{
			Name:        item.Name,
			DisplayName: item.DisplayName,
			PostCount:   item.PostCount,
		})
	}
	return out
}

func attachmentResponsesFor(views []app.AttachmentView) []view.AttachmentResponse {
	out := make([]view.AttachmentResponse, 0, len(views))
	for _, attachment := range views {
		out = append(out, view.AttachmentResponseFor(attachment.Attachment, attachment.Derivatives))
	}
	return out
}

// formatIDPtr 把可空 int64 转为可空字符串 ID。
func formatIDPtr(id *int64) *string {
	if id == nil {
		return nil
	}
	formatted := format.FormatID(*id)
	return &formatted
}
