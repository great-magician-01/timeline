package http

import (
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/admin/app"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// view.go 的纯 DTO 组装函数：不依赖 ReadModel，直接构造行结构断言字段映射。

func TestUserStatsViewMapsAllFields(t *testing.T) {
	view := userStatsView(app.UserStats{
		Posts: 1, Comments: 2, Followers: 3, Following: 4,
		LikesGiven: 5, LikesReceived: 6, Reposts: 7,
	})
	want := UserStatsView{
		Posts: 1, Comments: 2, Followers: 3, Following: 4,
		LikesGiven: 5, LikesReceived: 6, Reposts: 7,
	}
	if view != want {
		t.Fatalf("userStatsView = %+v，期望 %+v", view, want)
	}
}

func TestAdminCommentViewMapsFields(t *testing.T) {
	created := time.Date(2026, 10, 18, 1, 0, 0, 0, time.UTC)
	view := adminCommentView(app.CommentItem{
		ID: 11, PostID: 22,
		Author:      model.UserBrief{ID: 33, Username: "alice", Nickname: "Alice"},
		ContentHTML: "<p>评论</p>",
		Status:      model.CommentStatusPublished,
		CreatedAt:   created,
	})
	if view.ID != "11" || view.PostID != "22" {
		t.Fatalf("评论 ID 映射错误: %+v", view)
	}
	if view.Author.ID != "33" || view.Author.Username != "alice" {
		t.Fatalf("作者映射错误: %+v", view.Author)
	}
	if view.ContentHTML != "<p>评论</p>" || view.Status != model.CommentStatusPublished {
		t.Fatalf("正文/状态映射错误: %+v", view)
	}
	if view.CreatedAt != httpx.FormatTime(created) {
		t.Fatalf("CreatedAt = %q", view.CreatedAt)
	}
}

func TestPostDetailViewMapsNestedData(t *testing.T) {
	created := time.Date(2026, 10, 18, 1, 0, 0, 0, time.UTC)
	updated := created.Add(time.Hour)
	published := created.Add(2 * time.Hour)
	scheduled := created.Add(3 * time.Hour)
	deleted := created.Add(4 * time.Hour)
	restoredFrom := int64(77)
	postID := int64(9)

	detail := app.PostDetail{
		Post: app.PostItem{
			Post: model.Post{
				ID: 9, Status: model.PostStatusPublished, Visibility: model.VisibilityPublic,
				LikeCount: 3, CommentCount: 4, RepostCount: 5,
				CreatedAt: created, UpdatedAt: updated,
				PublishedAt: &published, ScheduledAt: &scheduled, DeletedAt: &deleted,
			},
			Author:      model.UserBrief{ID: 8, Username: "bob"},
			ContentHTML: "<p>正文</p>",
		},
		ContentHTML: "<p>详情正文</p>",
		Versions: []app.VersionItem{{
			ID: 70, VersionNumber: 2, Visibility: model.VisibilityFollowers,
			ContentHTML: "<p>v2</p>", CreatedAt: created, RestoredFromVersionID: &restoredFrom,
		}},
		Attachments: []app.AttachmentItem{{
			ID: 60, PostID: &postID, Kind: "image", Purpose: "post",
			OriginalFilename: "a.png", MediaType: "image/png", SizeBytes: 1024,
			Status: model.AttachmentAvailable, CreatedAt: created, UpdatedAt: updated,
		}},
		Comments:     []app.CommentItem{{ID: 50, PostID: 9, ContentHTML: "<p>c</p>", CreatedAt: created}},
		CommentCount: 4,
	}

	view := postDetailView(detail)
	if view.Post.ID != "9" || view.Post.Author.ID != "8" {
		t.Fatalf("帖子主体映射错误: %+v", view.Post)
	}
	if view.Post.ContentHTML != "<p>详情正文</p>" || view.Post.LikeCount != 3 || view.Post.RepostCount != 5 {
		t.Fatalf("帖子正文/计数映射错误: %+v", view.Post)
	}
	if view.Post.CreatedAt != httpx.FormatTime(created) || view.Post.UpdatedAt != httpx.FormatTime(updated) {
		t.Fatalf("帖子时间映射错误: %+v", view.Post)
	}
	if view.Post.PublishedAt == nil || *view.Post.PublishedAt != httpx.FormatTime(published) {
		t.Fatalf("published_at 映射错误: %v", view.Post.PublishedAt)
	}
	if view.Post.ScheduledAt == nil || *view.Post.ScheduledAt != httpx.FormatTime(scheduled) {
		t.Fatalf("scheduled_at 映射错误: %v", view.Post.ScheduledAt)
	}
	if view.Post.DeletedAt == nil || *view.Post.DeletedAt != httpx.FormatTime(deleted) {
		t.Fatalf("deleted_at 映射错误: %v", view.Post.DeletedAt)
	}
	if view.CommentCount != 4 || view.Post.CommentCount != 4 {
		t.Fatalf("评论计数映射错误: view=%d post=%d", view.CommentCount, view.Post.CommentCount)
	}
	if len(view.Versions) != 1 || view.Versions[0].ID != "70" || view.Versions[0].VersionNumber != 2 {
		t.Fatalf("历史版本映射错误: %+v", view.Versions)
	}
	if view.Versions[0].RestoredFromVersionID == nil || *view.Versions[0].RestoredFromVersionID != "77" {
		t.Fatalf("恢复来源映射错误: %v", view.Versions[0].RestoredFromVersionID)
	}
	if len(view.Attachments) != 1 || view.Attachments[0].ID != "60" ||
		view.Attachments[0].SizeBytes != 1024 || view.Attachments[0].Status != model.AttachmentAvailable {
		t.Fatalf("附件映射错误: %+v", view.Attachments)
	}
	if len(view.Comments) != 1 || view.Comments[0].ID != "50" {
		t.Fatalf("评论上下文映射错误: %+v", view.Comments)
	}
}

func TestPostDetailViewEmptySlicesAreNotNil(t *testing.T) {
	view := postDetailView(app.PostDetail{
		Post: app.PostItem{Post: model.Post{ID: 1, Status: model.PostStatusDraft, Visibility: model.VisibilityPrivate}},
	})
	if view.Versions == nil || view.Attachments == nil || view.Comments == nil {
		t.Fatalf("空详情应返回空数组而不是 null: %+v", view)
	}
	if view.CommentCount != 0 || view.Post.PublishedAt != nil {
		t.Fatalf("空详情字段不符: %+v", view)
	}
}

func TestAdminAttachmentViewMapsFields(t *testing.T) {
	created := time.Date(2026, 10, 18, 1, 0, 0, 0, time.UTC)
	updated := created.Add(time.Minute)
	postID := int64(9)
	width, height := 640, 480
	duration := int64(1500)
	view := adminAttachmentView(app.AttachmentItem{
		ID: 60, Uploader: model.UserBrief{ID: 8, Username: "bob"},
		PostID: &postID, Kind: "video", Purpose: "post",
		OriginalFilename: "clip.mp4", MediaType: "video/mp4", SizeBytes: 2048,
		Width: &width, Height: &height, DurationMS: &duration,
		Status: model.AttachmentAvailable, CreatedAt: created, UpdatedAt: updated,
	})
	if view.ID != "60" || view.Uploader.ID != "8" {
		t.Fatalf("附件主体映射错误: %+v", view)
	}
	if view.PostID == nil || *view.PostID != "9" {
		t.Fatalf("post_id 映射错误: %v", view.PostID)
	}
	if view.ContentURL != "/api/v1/attachments/60/content" {
		t.Fatalf("ContentURL = %q", view.ContentURL)
	}
	if view.Kind != "video" || view.Purpose != "post" || view.MediaType != "video/mp4" || view.SizeBytes != 2048 {
		t.Fatalf("附件元信息映射错误: %+v", view)
	}
	if view.Width == nil || *view.Width != 640 || view.Height == nil || *view.Height != 480 ||
		view.DurationMS == nil || *view.DurationMS != 1500 {
		t.Fatalf("附件尺寸映射错误: %+v", view)
	}
	if view.CreatedAt != httpx.FormatTime(created) || view.UpdatedAt != httpx.FormatTime(updated) {
		t.Fatalf("附件时间映射错误: %+v", view)
	}

	// 可空字段为空时输出 null。
	empty := adminAttachmentView(app.AttachmentItem{ID: 1})
	if empty.PostID != nil || empty.Width != nil || empty.Height != nil || empty.DurationMS != nil {
		t.Fatalf("可空字段为空时应输出 nil: %+v", empty)
	}
}

func TestJobViewFromTaskMapsFields(t *testing.T) {
	created := time.Date(2026, 10, 18, 1, 0, 0, 0, time.UTC)
	runAt := created.Add(time.Minute)
	refID := int64(88)
	view := jobViewFromTask(app.TaskItem{
		ID: 42, JobType: contracts.JobTypePostPublish, RefType: "post", RefID: &refID,
		Status: contracts.JobStatusPaused, RunAt: runAt, Attempts: 1, MaxAttempts: 3,
		LastError: "boom", CreatedAt: created, UpdatedAt: runAt,
	})
	if view.ID != "42" || view.JobType != contracts.JobTypePostPublish || view.RefType != "post" {
		t.Fatalf("作业基本信息映射错误: %+v", view)
	}
	if view.RefID == nil || *view.RefID != "88" {
		t.Fatalf("ref_id 映射错误: %v", view.RefID)
	}
	if view.Status != contracts.JobStatusPaused || view.Attempts != 1 || view.MaxAttempts != 3 || view.LastError != "boom" {
		t.Fatalf("作业状态映射错误: %+v", view)
	}
	if view.RunAt != httpx.FormatTime(runAt) || view.CreatedAt != httpx.FormatTime(created) {
		t.Fatalf("作业时间映射错误: %+v", view)
	}

	// RefID 为空时输出 nil。
	empty := jobViewFromTask(app.TaskItem{ID: 1})
	if empty.RefID != nil {
		t.Fatalf("ref_id 为空时应输出 nil: %v", empty.RefID)
	}
}

func TestAdminPostViewMapsPublishedFields(t *testing.T) {
	created := time.Date(2026, 10, 18, 1, 0, 0, 0, time.UTC)
	published := created.Add(time.Hour)
	view := adminPostView(app.PostItem{
		Post: model.Post{
			ID: 7, Status: model.PostStatusPublished, Visibility: model.VisibilityPublic,
			LikeCount: 3, CommentCount: 4, RepostCount: 5,
			CreatedAt: created, PublishedAt: &published,
		},
		Author:          model.UserBrief{ID: 9, Username: "alice"},
		ContentHTML:     "<p>x</p>",
		AttachmentCount: 2,
	})
	if view.Status != model.PostStatusPublished || view.Visibility != model.VisibilityPublic {
		t.Fatalf("帖子状态映射错误: %+v", view)
	}
	if view.ContentHTML != "<p>x</p>" || view.AttachmentCount != 2 {
		t.Fatalf("正文/附件数映射错误: %+v", view)
	}
	if view.LikeCount != 3 || view.CommentCount != 4 || view.RepostCount != 5 {
		t.Fatalf("互动计数映射错误: %+v", view)
	}
	if view.PublishedAt == nil || *view.PublishedAt != httpx.FormatTime(published) {
		t.Fatalf("published_at 映射错误: %v", view.PublishedAt)
	}
	if view.CreatedAt != httpx.FormatTime(created) {
		t.Fatalf("created_at 映射错误: %q", view.CreatedAt)
	}
}
