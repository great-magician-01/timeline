package http

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/content/app"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/shared/page"
)

// 说明：以下测试锁定 content HTTP DTO 的 JSON 字段与旧实现（internal/content）一致，
// 防止结构改造后字段名、null/omitempty 语义或列表形状漂移。

func marshalMap(t *testing.T, value any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("JSON 序列化失败: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("JSON 解析失败: %v", err)
	}
	return decoded
}

func requireKeys(t *testing.T, got map[string]any, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if _, ok := got[key]; !ok {
			t.Errorf("缺少字段 %q: %v", key, got)
		}
	}
}

func rejectKeys(t *testing.T, got map[string]any, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if _, ok := got[key]; ok {
			t.Errorf("不应出现字段 %q: %v", key, got)
		}
	}
}

func testUserBrief() model.UserBrief {
	return model.UserBrief{ID: 2, Username: "alice", Nickname: "爱丽丝", AvatarURL: "/a.png", IsAI: false, IsSuspended: false}
}

func testAttachmentView() app.AttachmentView {
	return app.AttachmentView{
		Attachment: model.Attachment{
			ID:               42,
			Kind:             "image",
			OriginalFilename: "a.png",
			MediaType:        "image/png",
			SizeBytes:        10,
			Status:           model.AttachmentAvailable,
			CreatedAt:        time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC),
		},
		Derivatives: map[string]string{model.DerivativeThumbnail: model.DerivativeReady},
	}
}

func TestPostBriefJSONContract(t *testing.T) {
	published := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	brief := app.PostBrief{
		ID:           11,
		Kind:         "post",
		Author:       testUserBrief(),
		Visibility:   model.VisibilityPublic,
		ContentHTML:  "<p>正文</p>",
		PublishedAt:  &published,
		LikeCount:    1,
		CommentCount: 2,
		RepostCount:  3,
		Attachments:  []app.AttachmentView{testAttachmentView()},
		ViewerState:  app.ViewerState{Liked: true, Reposted: false},
	}
	got := marshalMap(t, briefResponseFor(brief))

	requireKeys(t, got, "id", "kind", "author", "visibility", "content_html", "published_at",
		"edited", "like_count", "comment_count", "repost_count", "attachments", "viewer_state")
	rejectKeys(t, got, "reposted_at", "repost_of", "quoted_post")

	if got["id"] != "11" {
		t.Errorf("id 必须是字符串: %v", got["id"])
	}
	if got["published_at"] != "2026-10-18T20:00:00+08:00" {
		t.Errorf("published_at 必须是 +08:00 RFC3339: %v", got["published_at"])
	}
	author := got["author"].(map[string]any)
	requireKeys(t, author, "id", "username", "nickname", "avatar_url", "is_ai", "is_suspended")
	viewerState := got["viewer_state"].(map[string]any)
	if viewerState["liked"] != true || viewerState["reposted"] != false {
		t.Errorf("viewer_state 错误: %v", viewerState)
	}
	attachments := got["attachments"].([]any)
	if len(attachments) != 1 {
		t.Fatalf("attachments 长度错误: %v", attachments)
	}
	attachment := attachments[0].(map[string]any)
	requireKeys(t, attachment, "id", "kind", "original_filename", "media_type", "size_bytes",
		"width", "height", "duration_ms", "content_url", "derivatives", "status", "created_at")
	if attachment["content_url"] != "/api/v1/attachments/42/content" {
		t.Errorf("附件 content_url 错误: %v", attachment["content_url"])
	}
	if attachment["thumbnail_url"] != "/api/v1/attachments/42/derivatives/thumbnail" {
		t.Errorf("缩略图 URL 错误: %v", attachment["thumbnail_url"])
	}
}

func TestPostBriefNullPublishedAtAndEmptyAttachments(t *testing.T) {
	got := marshalMap(t, briefResponseFor(app.PostBrief{ID: 1, Kind: "post", Author: testUserBrief()}))
	value, ok := got["published_at"]
	if !ok || value != nil {
		t.Errorf("未发布内容的 published_at 必须为 null，实际 %v", value)
	}
	attachments, ok := got["attachments"].([]any)
	if !ok || len(attachments) != 0 {
		t.Errorf("attachments 必须为空数组而不是 null: %v", got["attachments"])
	}
}

func TestPostDetailJSONOmitEmpty(t *testing.T) {
	detail := app.PostDetail{
		PostBrief:      app.PostBrief{ID: 1, Author: testUserBrief()},
		Status:         model.PostStatusDraft,
		ContentFormat:  "richtext",
		CanEdit:        true,
		CanDelete:      true,
		CanViewHistory: true,
	}
	got := marshalMap(t, detailResponseFor(detail))
	requireKeys(t, got, "id", "author", "status", "content_format", "can_edit", "can_delete", "can_view_history")
	// PostDetail 为内嵌结构，详情响应直接展开 PostBrief 字段，不使用 "post" 包装键。
	rejectKeys(t, got, "post", "scheduled_at", "revision", "version_number", "author_follow_state")
}

func TestPostDetailJSONWithOptionalFields(t *testing.T) {
	scheduled := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	detail := app.PostDetail{
		PostBrief:         app.PostBrief{ID: 1, Author: testUserBrief()},
		Status:            model.PostStatusScheduled,
		ContentFormat:     "markdown",
		ScheduledAt:       &scheduled,
		Revision:          2,
		VersionNumber:     3,
		CanViewHistory:    true,
		AuthorFollowState: model.FollowStateFollowing,
	}
	got := marshalMap(t, detailResponseFor(detail))
	if got["scheduled_at"] != "2026-10-19T18:00:00+08:00" {
		t.Errorf("scheduled_at 错误: %v", got["scheduled_at"])
	}
	if got["revision"] != float64(2) || got["version_number"] != float64(3) {
		t.Errorf("revision/version_number 错误: %v", got)
	}
	if got["author_follow_state"] != model.FollowStateFollowing {
		t.Errorf("author_follow_state 错误: %v", got)
	}
}

func TestNestedRepostAndQuotedJSON(t *testing.T) {
	reposted := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	inner := app.PostBrief{ID: 5, Kind: "post", Author: testUserBrief(), ContentHTML: "<p>原帖</p>"}
	outer := app.PostBrief{
		ID: 9, Kind: "repost", Author: testUserBrief(), RepostedAt: &reposted, RepostOf: &inner,
		QuotedPost: &app.QuotedPost{Available: false},
	}
	got := marshalMap(t, briefResponseFor(outer))
	if got["kind"] != "repost" {
		t.Errorf("kind 错误: %v", got["kind"])
	}
	repostOf, ok := got["repost_of"].(map[string]any)
	if !ok {
		t.Fatalf("repost_of 缺失: %v", got)
	}
	if repostOf["id"] != "5" || repostOf["content_html"] != "<p>原帖</p>" {
		t.Errorf("repost_of 内容错误: %v", repostOf)
	}
	quoted, ok := got["quoted_post"].(map[string]any)
	if !ok || quoted["available"] != false {
		t.Errorf("quoted_post 错误: %v", got["quoted_post"])
	}
	if _, ok := quoted["post"]; ok {
		t.Errorf("不可见引用不应带 post 字段: %v", quoted)
	}
	if got["reposted_at"] != "2026-10-18T17:00:00+08:00" {
		t.Errorf("reposted_at 错误: %v", got["reposted_at"])
	}
}

func TestVersionDTOsJSON(t *testing.T) {
	restoredFrom := int64(77)
	item := versionItemResponsesFor([]app.VersionItem{{
		ID: 100, VersionNumber: 2, Visibility: model.VisibilityPublic,
		CreatedAt:             time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC),
		RestoredFromVersionID: &restoredFrom, Summary: "摘要",
	}})[0]
	got := marshalMap(t, item)
	requireKeys(t, got, "id", "version_number", "visibility", "created_at", "restored_from_version_id", "summary")
	if got["id"] != "100" || got["restored_from_version_id"] != "77" || got["summary"] != "摘要" {
		t.Errorf("版本列表项字段错误: %v", got)
	}

	detail := versionDetailResponseFor(app.VersionDetail{
		ID: 100, VersionNumber: 2, ContentFormat: "richtext", ContentSource: "<p>x</p>",
		ContentHTML: "<p>x</p>", Visibility: model.VisibilityPublic,
		CreatedAt: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC), CreatedBy: 5,
	})
	detailMap := marshalMap(t, detail)
	requireKeys(t, detailMap, "id", "version_number", "content_format", "content_source",
		"content_html", "visibility", "created_at", "created_by", "attachments")
	if attachments, ok := detailMap["attachments"].([]any); !ok || len(attachments) != 0 {
		t.Errorf("attachments 必须为空数组: %v", detailMap["attachments"])
	}
	if detailMap["created_by"] != "5" {
		t.Errorf("created_by 必须是字符串 ID: %v", detailMap["created_by"])
	}
}

func TestDraftItemJSONContract(t *testing.T) {
	scheduled := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	failed := true
	message := "超时"
	items := draftItemResponsesFor([]app.DraftItem{{
		ID: 3, ContentHTML: "<p>正文</p>", PlainText: "正文", Visibility: model.VisibilityPublic,
		Revision: 2, Status: "failed", UpdatedAt: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC),
		ScheduledAt: &scheduled, AttachmentCount: 1, PublishFailed: &failed, PublishError: &message,
	}})
	got := marshalMap(t, items[0])
	requireKeys(t, got, "id", "content_html", "plain_text", "visibility", "revision", "status",
		"updated_at", "scheduled_at", "attachment_count", "publish_failed", "publish_error")

	// 草稿列表不返回失败/暂停字段（nil 指针 + omitempty）。
	draftItem := draftItemResponsesFor([]app.DraftItem{{
		ID: 4, ContentHTML: "<p>x</p>", PlainText: "x", Status: "draft",
		UpdatedAt: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC),
	}})[0]
	draftMap := marshalMap(t, draftItem)
	rejectKeys(t, draftMap, "scheduled_at", "publish_failed", "publish_error")
}

func TestCommentTimelineItemJSONContract(t *testing.T) {
	post := app.PostBrief{ID: 5, Kind: "post", Author: testUserBrief()}
	got := marshalMap(t, commentTimelineResponsesFor([]app.CommentTimelineItem{{
		Kind: "comment", ID: 8, Author: testUserBrief(), ContentHTML: "<p>回复</p>",
		CreatedAt: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC), Post: &post,
	}})[0])
	requireKeys(t, got, "kind", "id", "author", "content_html", "created_at", "post")
	if got["id"] != "8" || got["created_at"] != "2026-10-18T20:00:00+08:00" {
		t.Errorf("回复列表项字段错误: %v", got)
	}
	nested := got["post"].(map[string]any)
	if nested["id"] != "5" {
		t.Errorf("所属帖子字段错误: %v", nested)
	}
}

func TestTagSuggestionJSONContract(t *testing.T) {
	got := marshalMap(t, tagSuggestionResponsesFor([]app.TagSuggestion{{
		Name: "go", DisplayName: "Go语言", PostCount: 3,
	}})[0])
	requireKeys(t, got, "name", "display_name", "post_count")
	if got["post_count"] != float64(3) {
		t.Errorf("post_count 错误: %v", got["post_count"])
	}
}

func TestTimeListResponseCursorSemantics(t *testing.T) {
	empty := marshalMap(t, timeListResponse([]any{}, page.TimeCursor{}, false))
	if empty["has_more"] != false {
		t.Errorf("空页 has_more 必须为 false: %v", empty)
	}
	if value, ok := empty["next_cursor"]; !ok || value != nil {
		t.Errorf("无下一页时 next_cursor 必须为 null: %v", empty)
	}
	if items, ok := empty["items"].([]any); !ok || len(items) != 0 {
		t.Errorf("items 必须为空数组: %v", empty["items"])
	}

	at := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	full := marshalMap(t, timeListResponse([]any{map[string]any{"id": "1"}}, page.TimeCursor{Time: at, ID: 123}, true))
	cursor, ok := full["next_cursor"].(string)
	if !ok || cursor == "" || full["has_more"] != true {
		t.Fatalf("下一页游标缺失: %v", full)
	}
	decoded, err := page.DecodeTimeCursor(cursor)
	if err != nil || decoded.ID != 123 || !decoded.Time.Equal(at) {
		t.Fatalf("游标不可解析或值错误: %+v, %v", decoded, err)
	}
}
