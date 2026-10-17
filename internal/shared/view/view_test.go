package view

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/shared/model"
)

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("JSON 序列化失败: %v", err)
	}
	return string(data)
}

func TestUserBriefResponseJSONContract(t *testing.T) {
	resp := UserBriefResponseFor(model.UserBrief{
		ID: 9, Username: "alice", Nickname: "爱丽丝", AvatarURL: "/a.png", IsAI: false, IsSuspended: true,
	})
	want := `{"id":"9","username":"alice","nickname":"爱丽丝","avatar_url":"/a.png","is_ai":false,"is_suspended":true}`
	if got := mustJSON(t, resp); got != want {
		t.Fatalf("UserBriefResponse JSON 不兼容\n got: %s\nwant: %s", got, want)
	}
}

func TestUserBriefResponsesForKeepsOrderAndEmptySlice(t *testing.T) {
	got := UserBriefResponsesFor([]model.UserBrief{{ID: 1}, {ID: 2}})
	if len(got) != 2 || got[0].ID != "1" || got[1].ID != "2" {
		t.Fatalf("批量组装错误: %+v", got)
	}
	if empty := mustJSON(t, UserBriefResponsesFor(nil)); empty != "[]" {
		t.Fatalf("空列表应序列化为 []，实际 %s", empty)
	}
}

func TestAttachmentResponseJSONContract(t *testing.T) {
	attachment := model.Attachment{
		ID:               1,
		UploaderID:       5,
		Kind:             "image",
		OriginalFilename: "a.png",
		MediaType:        "image/png",
		SizeBytes:        10,
		Width:            intPtr(2),
		Height:           intPtr(3),
		Status:           model.AttachmentAvailable,
		CreatedAt:        time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC),
	}
	resp := AttachmentResponseFor(attachment, map[string]string{model.DerivativeThumbnail: model.DerivativeReady})
	want := `{"id":"1","kind":"image","original_filename":"a.png","media_type":"image/png","size_bytes":10,` +
		`"width":2,"height":3,"duration_ms":null,"content_url":"/api/v1/attachments/1/content",` +
		`"thumbnail_url":"/api/v1/attachments/1/derivatives/thumbnail",` +
		`"derivatives":{"thumbnail":"ready"},"status":"available","created_at":"2026-10-17T20:00:00+08:00"}`
	if got := mustJSON(t, resp); got != want {
		t.Fatalf("AttachmentResponse JSON 不兼容\n got: %s\nwant: %s", got, want)
	}
}

func TestAttachmentResponseThumbnailOnlyWhenReady(t *testing.T) {
	attachment := model.Attachment{ID: 2, CreatedAt: time.Unix(0, 0).UTC()}
	for _, state := range []string{model.DerivativePending, model.DerivativeProcessing, model.DerivativeFailed, model.DerivativeCanceled} {
		resp := AttachmentResponseFor(attachment, map[string]string{model.DerivativeThumbnail: state})
		if resp.ThumbnailURL != "" {
			t.Fatalf("衍生文件状态 %s 不应返回缩略图地址: %q", state, resp.ThumbnailURL)
		}
	}
	// 无衍生文件时 derivatives 必须是非 nil 空对象，保持旧响应形状。
	resp := AttachmentResponseFor(attachment, nil)
	if resp.Derivatives == nil {
		t.Fatalf("derivatives 不应为 nil")
	}
	if got := mustJSON(t, resp.Derivatives); got != "{}" {
		t.Fatalf("derivatives 空对象序列化异常: %s", got)
	}
}

func TestPublicUserResponseOmitsPrivateFields(t *testing.T) {
	user := model.User{
		ID:           3,
		Username:     "alice",
		Email:        "a@example.com",
		Nickname:     "爱丽丝",
		Bio:          "简介",
		Role:         model.RoleAdmin,
		IsSuperAdmin: true,
		Status:       model.StatusActive,
		CreatedAt:    time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC),
		UpdatedAt:    time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC),
	}
	raw := mustJSON(t, PublicUserResponse(user))
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("JSON 解析失败: %v", err)
	}
	for _, private := range []string{"email", "role", "is_super_admin", "status", "require_follow_approval", "updated_at"} {
		if _, ok := decoded[private]; ok {
			t.Errorf("公开用户 DTO 不应包含私有字段 %q: %s", private, raw)
		}
	}
	if decoded["created_at"] != "2026-10-17T20:00:00+08:00" {
		t.Errorf("created_at 格式异常: %v", decoded["created_at"])
	}
}

func TestPrivateUserResponseIncludesPrivateFields(t *testing.T) {
	user := model.User{
		ID:                    3,
		Username:              "alice",
		Email:                 "a@example.com",
		Role:                  model.RoleAdmin,
		IsSuperAdmin:          true,
		Status:                model.StatusSuspended,
		RequireFollowApproval: true,
		CreatedAt:             time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC),
		UpdatedAt:             time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC),
	}
	raw := mustJSON(t, PrivateUserResponse(user))
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("JSON 解析失败: %v", err)
	}
	for _, key := range []string{"email", "role", "is_super_admin", "status", "require_follow_approval", "updated_at"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("私有用户 DTO 缺少字段 %q: %s", key, raw)
		}
	}
	if decoded["email"] != "a@example.com" || decoded["role"] != model.RoleAdmin || decoded["status"] != model.StatusSuspended {
		t.Errorf("私有用户 DTO 字段值错误: %s", raw)
	}
	if decoded["require_follow_approval"] != true {
		t.Errorf("require_follow_approval 应为 true: %s", raw)
	}
}

func intPtr(value int) *int { return &value }
