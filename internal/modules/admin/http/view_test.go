package http

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/admin/app"
	"github.com/great-magician-01/timeline/internal/modules/admin/repo"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

func TestOverviewViewMapsCounts(t *testing.T) {
	view := overviewView(app.Overview{
		Counts: repo.OverviewCounts{
			UsersTotal: 10, UsersSuspended: 2, UsersAI: 3,
			PostsPublished: 20, CommentsPublished: 30, PostsDraftScheduled: 4,
			AIEnabled: 2, AITotal: 3,
			JobsPending: 5, JobsFailed: 1, JobsPaused: 2,
		},
		AIGlobalEnabled: true,
	})
	if view.Users.Total != 10 || view.Users.Suspended != 2 || view.Users.AI != 3 {
		t.Fatalf("users 映射错误: %+v", view.Users)
	}
	if view.Content.Posts != 20 || view.Content.Comments != 30 || view.Content.Drafts != 4 {
		t.Fatalf("content 映射错误: %+v", view.Content)
	}
	if !view.AI.GlobalEnabled || view.AI.Enabled != 2 || view.AI.Total != 3 {
		t.Fatalf("ai 映射错误: %+v", view.AI)
	}
	if view.Tasks.Pending != 5 || view.Tasks.Failed != 1 || view.Tasks.Paused != 2 {
		t.Fatalf("tasks 映射错误: %+v", view.Tasks)
	}
}

func TestAdminPostViewNilPublishedAt(t *testing.T) {
	created := time.Date(2026, 10, 18, 1, 0, 0, 0, time.UTC)
	view := adminPostView(app.PostItem{
		Post: model.Post{ID: 7, Status: model.PostStatusDraft, Visibility: model.VisibilityPrivate,
			CreatedAt: created},
		Author:      model.UserBrief{ID: 9, Username: "alice"},
		ContentHTML: "<p>x</p>",
	})
	if view.ID != "7" {
		t.Fatalf("ID = %q", view.ID)
	}
	if view.PublishedAt != nil {
		t.Fatalf("草稿不应有 published_at: %v", view.PublishedAt)
	}
	if view.Author.ID != "9" {
		t.Fatalf("Author.ID = %q", view.Author.ID)
	}
}

func TestAuditLogViewEmptyDetail(t *testing.T) {
	view := auditLogView(app.AuditItem{ID: 1, AdminID: 2, ActionType: "user_suspend", TargetType: "user"})
	if string(view.Detail) != "{}" {
		t.Fatalf("空 detail 应为 {}: %s", view.Detail)
	}
}

func TestJobViewFromSnapshot(t *testing.T) {
	at := time.Date(2026, 10, 18, 1, 0, 0, 0, time.UTC)
	refID := int64(88)
	view := jobViewFromSnapshot(contracts.JobSnapshot{
		ID: 5, Type: "post_publish", RefType: "post", RefID: &refID,
		Status: contracts.JobStatusPending, RunAt: at, Attempts: 1, MaxAttempts: 3,
		CreatedAt: at, UpdatedAt: at,
	})
	if view.ID != "5" || view.RefID == nil || *view.RefID != "88" {
		t.Fatalf("作业快照映射错误: %+v", view)
	}
	if view.Status != contracts.JobStatusPending {
		t.Fatalf("Status = %q", view.Status)
	}
}

func TestSettingsViewShape(t *testing.T) {
	view := settingsView(app.SettingsSnapshot{
		RegistrationEnabled:     true,
		PostRateLimit:           30,
		PostRateWindow:          10 * time.Minute,
		TempAttachmentRetention: 7 * 24 * time.Hour,
	})
	if view["post_rate_window_seconds"] != 600 {
		t.Fatalf("post_rate_window_seconds = %v", view["post_rate_window_seconds"])
	}
	if view["temp_attachment_retention_hours"] != 168 {
		t.Fatalf("temp_attachment_retention_hours = %v", view["temp_attachment_retention_hours"])
	}
	if view["registration_enabled"] != true {
		t.Fatalf("registration_enabled = %v", view["registration_enabled"])
	}
}

func TestOptionalID(t *testing.T) {
	if optionalID(nil) != nil {
		t.Fatal("nil 应输出 nil")
	}
	id := int64(42)
	got := optionalID(&id)
	if got == nil || *got != "42" {
		t.Fatalf("optionalID = %v", got)
	}
}

func TestWebSearchViewNeverExposesSecret(t *testing.T) {
	encoded, err := json.Marshal(webSearchView(app.WebSearchSnapshot{
		BaseURL: "https://api.example.com", Configured: true, Usable: true,
		APIKeyMasked: "••••••••",
	}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := decoded["api_key"]; ok {
		t.Fatal("响应不得包含 api_key 字段")
	}
	if decoded["api_key_masked"] == "" {
		t.Fatal("应返回掩码")
	}
}
