package http

import (
	"time"

	"github.com/great-magician-01/timeline/internal/modules/social/app"
	"github.com/great-magician-01/timeline/internal/shared/format"
	"github.com/great-magician-01/timeline/internal/shared/view"
)

// CommentResponse 是评论与回复的对外 DTO（docs/API.md §8）。
// 已删除评论保留占位：正文为空、不能删除、不显示我点过赞。
type CommentResponse struct {
	ID              string                 `json:"id"`
	PostID          string                 `json:"post_id"`
	Author          view.UserBriefResponse `json:"author"`
	ContentHTML     string                 `json:"content_html"`
	Status          string                 `json:"status"`
	ParentCommentID *string                `json:"parent_comment_id,omitempty"`
	RootCommentID   *string                `json:"root_comment_id,omitempty"`
	ReplyCount      int                    `json:"reply_count"`
	LikeCount       int                    `json:"like_count"`
	LikedByMe       bool                   `json:"liked_by_me"`
	CanDelete       bool                   `json:"can_delete"`
	CreatedAt       string                 `json:"created_at"`
	ReplyPreview    []CommentResponse      `json:"reply_preview,omitempty"` // 仅列表接口填充，嵌套不递归
}

// commentResponseFor 组装单条评论 DTO。
func commentResponseFor(item app.Comment) CommentResponse {
	comment := item.Row
	resp := CommentResponse{
		ID:          format.FormatID(comment.ID),
		PostID:      format.FormatID(comment.PostID),
		Author:      view.UserBriefResponseFor(item.Author),
		ContentHTML: comment.ContentHTML,
		Status:      comment.Status,
		ReplyCount:  comment.ReplyCount,
		LikeCount:   comment.LikeCount,
		LikedByMe:   item.LikedByMe,
		CanDelete:   item.CanDelete,
		CreatedAt:   format.FormatTime(comment.CreatedAt),
	}
	if comment.ParentCommentID != nil {
		id := format.FormatID(*comment.ParentCommentID)
		resp.ParentCommentID = &id
	}
	if comment.RootCommentID != nil {
		id := format.FormatID(*comment.RootCommentID)
		resp.RootCommentID = &id
	}
	if comment.Deleted() {
		resp.ContentHTML = ""
		resp.LikedByMe = false
		resp.CanDelete = false
	}
	return resp
}

// commentResponsesFor 批量组装评论 DTO，保持输入顺序。
func commentResponsesFor(items []app.Comment) []CommentResponse {
	out := make([]CommentResponse, 0, len(items))
	for _, item := range items {
		out = append(out, commentResponseFor(item))
	}
	return out
}

// followRequestResponse 是关注申请列表项（docs/API.md §5）。
type followRequestResponse struct {
	ID         string                 `json:"id"`
	Status     string                 `json:"status"`
	Requester  view.UserBriefResponse `json:"requester"`
	Target     view.UserBriefResponse `json:"target"`
	CreatedAt  string                 `json:"created_at"`
	ResolvedAt *string                `json:"resolved_at"`
}

// followRequestResponseFor 组装关注申请 DTO。
func followRequestResponseFor(item app.RequestItem) followRequestResponse {
	return followRequestResponse{
		ID:         format.FormatID(item.ID),
		Status:     item.Status,
		Requester:  view.UserBriefResponseFor(item.Requester),
		Target:     view.UserBriefResponseFor(item.Target),
		CreatedAt:  format.FormatTime(item.CreatedAt),
		ResolvedAt: formatTimePtr(item.ResolvedAt),
	}
}

// repostBrief 是本次生效转发的行标识与转发时间（docs/API.md §6.1）。
type repostBrief struct {
	ID        string `json:"id"`
	CreatedAt string `json:"created_at"`
}

// repostResponse 是纯转发/取消转发的响应 DTO：
// repost 仅在有效转发时出现，供前端在当前列表即时插入转发项。
type repostResponse struct {
	Reposted    bool         `json:"reposted"`
	RepostCount int          `json:"repost_count"`
	Repost      *repostBrief `json:"repost,omitempty"`
}

// repostResponseFor 组装转发响应；取消转发不携带 repost 对象。
func repostResponseFor(result app.RepostResult) repostResponse {
	resp := repostResponse{Reposted: result.Reposted, RepostCount: result.Count}
	if result.Reposted {
		resp.Repost = &repostBrief{
			ID:        format.FormatID(result.RepostID),
			CreatedAt: format.FormatTime(result.RepostedAt),
		}
	}
	return resp
}

func formatTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	formatted := format.FormatTime(*t)
	return &formatted
}
