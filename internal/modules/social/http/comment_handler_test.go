package http

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/modules/social/app"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/ratelimit"
	"github.com/great-magician-01/timeline/internal/shared/format"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// sampleComment 构造一条指定状态的评论行。
func sampleComment(id, authorID int64, status string) model.Comment {
	return model.Comment{
		ID:            id,
		PostID:        pid,
		AuthorID:      authorID,
		ContentFormat: "richtext",
		ContentHTML:   "<p>评论</p>",
		PlainText:     "评论",
		Status:        status,
		ReplyCount:    2,
		LikeCount:     1,
		CreatedAt:     testNow.Add(-time.Minute),
		UpdatedAt:     testNow.Add(-time.Minute),
	}
}

// TestListCommentsHandlerParams 验证评论列表的参数解析与错误映射。
func TestListCommentsHandlerParams(t *testing.T) {
	cases := []struct {
		name       string
		target     string
		param      string
		wantStatus int
		wantCode   string
		setup      func(*fixture)
	}{
		{"ID 非法", "/api/v1/posts/abc/comments", "abc", http.StatusBadRequest, "invalid_request", nil},
		{"ID 为负", "/api/v1/posts/-1/comments", "-1", http.StatusBadRequest, "invalid_request", nil},
		{"limit 越界", "/api/v1/posts/20/comments?limit=0", "20", http.StatusBadRequest, "invalid_request", nil},
		{"limit 过大", "/api/v1/posts/20/comments?limit=51", "20", http.StatusBadRequest, "invalid_request", nil},
		{"游标非法", "/api/v1/posts/20/comments?cursor=!!!", "20", http.StatusBadRequest, "invalid_request", nil},
		{
			name:       "帖子不可见映射 404",
			target:     "/api/v1/posts/20/comments",
			param:      "20",
			wantStatus: http.StatusNotFound,
			wantCode:   "not_found",
			setup:      func(f *fixture) { f.posts.visibleErr = apperr.NotFound("内容不存在或无权访问") },
		},
		{
			name:       "未知错误映射 500",
			target:     "/api/v1/posts/20/comments",
			param:      "20",
			wantStatus: http.StatusInternalServerError,
			wantCode:   "internal_error",
			setup:      func(f *fixture) { f.posts.visibleErr = errors.New("数据库连接中断") },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			if tc.setup != nil {
				tc.setup(f)
			}
			c, recorder := newTestContext(http.MethodGet, tc.target)
			withParam(c, "id", tc.param)
			f.handler().ListComments(c)
			requireStatus(t, recorder, tc.wantStatus)
			requireErrorCode(t, recorder, tc.wantCode)
		})
	}
}

// TestListCommentsHandlerSuccess 验证评论列表 JSON：回复预览、已删除占位与分页字段。
func TestListCommentsHandlerSuccess(t *testing.T) {
	f := newFixture()
	first := sampleComment(101, uidOther, model.CommentStatusPublished)
	deleted := sampleComment(102, uidViewer, model.CommentStatusDeleted)
	reply := sampleComment(103, uidViewer, model.CommentStatusPublished)
	f.comments.topLevel = []model.Comment{first, deleted}
	f.comments.previews[first.ID] = []model.Comment{reply}
	f.likes.activeStates[first.ID] = true
	f.likes.activeStates[deleted.ID] = true
	c, recorder := newTestContext(http.MethodGet, "/api/v1/posts/20/comments?limit=2")
	withParam(c, "id", "20")
	withViewer(c, viewerFor(uidViewer))

	f.handler().ListComments(c)
	requireStatus(t, recorder, http.StatusOK)
	body := decodeBody[listBody[CommentResponse]](t, recorder)
	if len(body.Items) != 2 || body.HasMore {
		t.Fatalf("列表 = %d 条/hasMore=%v，期望 2 条且无更多", len(body.Items), body.HasMore)
	}
	if body.NextCursor != nil {
		t.Fatalf("无更多时 next_cursor 应为 null，实际 %q", *body.NextCursor)
	}
	item := body.Items[0]
	if item.ID != "101" || item.PostID != "20" {
		t.Fatalf("评论 ID/帖子 ID = (%q, %q)", item.ID, item.PostID)
	}
	if item.Author.Username != "carol" || item.Author.ID != "3" {
		t.Fatalf("作者摘要 = %+v，期望 carol", item.Author)
	}
	if !item.LikedByMe || item.CanDelete {
		t.Fatalf("访问者状态 = (liked=%v, canDelete=%v)，期望 (true, false)", item.LikedByMe, item.CanDelete)
	}
	if item.ContentHTML != "<p>评论</p>" || item.ReplyCount != 2 || item.LikeCount != 1 {
		t.Fatalf("评论字段 = %+v", item)
	}
	if want := format.FormatTime(first.CreatedAt); item.CreatedAt != want {
		t.Fatalf("创建时间 = %q，期望 %q", item.CreatedAt, want)
	}
	if len(item.ReplyPreview) != 1 || item.ReplyPreview[0].ID != "103" {
		t.Fatalf("回复预览 = %+v，期望包含回复 103", item.ReplyPreview)
	}

	masked := body.Items[1]
	if masked.ContentHTML != "" || masked.LikedByMe || masked.CanDelete {
		t.Fatalf("已删除评论应脱敏为占位: %+v", masked)
	}
	if masked.Status != model.CommentStatusDeleted {
		t.Fatalf("已删除评论状态 = %q", masked.Status)
	}
}

// TestListCommentsHandlerHasMore 验证多查一条时输出下一页游标。
func TestListCommentsHandlerHasMore(t *testing.T) {
	f := newFixture()
	f.comments.topLevel = []model.Comment{
		sampleComment(101, uidOther, model.CommentStatusPublished),
		sampleComment(102, uidOther, model.CommentStatusPublished),
	}
	c, recorder := newTestContext(http.MethodGet, "/api/v1/posts/20/comments?limit=1")
	withParam(c, "id", "20")
	f.handler().ListComments(c)
	requireStatus(t, recorder, http.StatusOK)
	body := decodeBody[listBody[CommentResponse]](t, recorder)
	if len(body.Items) != 1 || !body.HasMore {
		t.Fatalf("列表 = %d 条/hasMore=%v，期望 1 条且更多", len(body.Items), body.HasMore)
	}
	if body.NextCursor == nil || *body.NextCursor == "" {
		t.Fatal("有更多时应输出非空 next_cursor")
	}
}

// TestListRepliesHandler 验证回复列表的游标、DTO 与错误映射。
func TestListRepliesHandler(t *testing.T) {
	t.Run("成功", func(t *testing.T) {
		f := newFixture()
		f.comments.comments[commentID] = sampleComment(commentID, uidAuthor, model.CommentStatusPublished)
		f.comments.replies = []model.Comment{sampleComment(103, uidViewer, model.CommentStatusPublished)}
		c, recorder := newTestContext(http.MethodGet, "/api/v1/comments/60/replies")
		withParam(c, "id", strconv.FormatInt(commentID, 10))
		withViewer(c, viewerFor(uidViewer))

		f.handler().ListReplies(c)
		requireStatus(t, recorder, http.StatusOK)
		body := decodeBody[listBody[CommentResponse]](t, recorder)
		if len(body.Items) != 1 || body.Items[0].ID != "103" {
			t.Fatalf("回复列表 = %+v", body.Items)
		}
		if !body.Items[0].CanDelete {
			t.Fatal("回复作者本人应可删除")
		}
	})

	t.Run("参数非法", func(t *testing.T) {
		f := newFixture()
		c, recorder := newTestContext(http.MethodGet, "/api/v1/comments/abc/replies")
		withParam(c, "id", "abc")
		f.handler().ListReplies(c)
		requireStatus(t, recorder, http.StatusBadRequest)
		requireErrorCode(t, recorder, "invalid_request")
	})

	t.Run("评论不存在", func(t *testing.T) {
		f := newFixture()
		c, recorder := newTestContext(http.MethodGet, "/api/v1/comments/60/replies")
		withParam(c, "id", "60")
		f.handler().ListReplies(c)
		requireStatus(t, recorder, http.StatusNotFound)
		requireErrorCode(t, recorder, "not_found")
	})
}

// TestCreateCommentHandlerValidation 验证发表评论的鉴权与请求体校验。
func TestCreateCommentHandlerValidation(t *testing.T) {
	validBody := `{"content_format":"richtext","content_source":"<p>你好</p>"}`
	cases := []struct {
		name       string
		body       string
		viewer     bool
		param      string
		wantStatus int
		wantCode   string
		wantDetail string
	}{
		{"未登录", validBody, false, "20", http.StatusUnauthorized, "unauthenticated", ""},
		{"ID 非法", validBody, true, "abc", http.StatusBadRequest, "invalid_request", ""},
		{"JSON 非法", `{`, true, "20", http.StatusBadRequest, "invalid_request", ""},
		{"未知字段", `{"content_format":"richtext","unknown":1}`, true, "20", http.StatusBadRequest, "invalid_request", ""},
		{"格式非法", `{"content_format":"html"}`, true, "20", http.StatusBadRequest, "validation_failed", "content_format"},
		{"父评论 ID 非法", `{"content_format":"richtext","parent_comment_id":"abc"}`, true, "20", http.StatusBadRequest, "validation_failed", "parent_comment_id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			c, recorder := newJSONContext(http.MethodPost, "/api/v1/posts/20/comments", tc.body)
			withParam(c, "id", tc.param)
			if tc.viewer {
				withViewer(c, viewerFor(uidViewer))
			}
			f.handler().CreateComment(c)
			requireStatus(t, recorder, tc.wantStatus)
			requireErrorCode(t, recorder, tc.wantCode)
			if tc.wantDetail != "" {
				details := errorDetails(t, recorder)
				if _, ok := details[tc.wantDetail]; !ok {
					t.Fatalf("错误 details 缺少字段 %q: %+v", tc.wantDetail, details)
				}
			}
		})
	}
}

// TestCreateCommentHandlerSuccess 验证发表评论的成功响应与父评论参数。
func TestCreateCommentHandlerSuccess(t *testing.T) {
	t.Run("顶层评论", func(t *testing.T) {
		f := newFixture()
		c, recorder := newJSONContext(http.MethodPost, "/api/v1/posts/20/comments",
			`{"content_format":"richtext","content_source":"<p>你好</p>","parent_comment_id":""}`)
		withParam(c, "id", "20")
		withViewer(c, viewerFor(uidViewer))

		f.handler().CreateComment(c)
		requireStatus(t, recorder, http.StatusCreated)
		body := decodeBody[struct {
			Comment CommentResponse `json:"comment"`
		}](t, recorder)
		if body.Comment.ID != "101" || body.Comment.PostID != "20" {
			t.Fatalf("评论 ID/帖子 ID = (%q, %q)", body.Comment.ID, body.Comment.PostID)
		}
		if body.Comment.Author.Username != "alice" || !body.Comment.CanDelete {
			t.Fatalf("评论视图 = %+v", body.Comment)
		}
		if body.Comment.ContentHTML != "<p>你好</p>" {
			t.Fatalf("评论 HTML = %q", body.Comment.ContentHTML)
		}
		if body.Comment.ParentCommentID != nil || body.Comment.RootCommentID != nil {
			t.Fatalf("顶层评论不应有父/根评论: %+v", body.Comment)
		}
		if body.Comment.CreatedAt != format.FormatTime(testNow) {
			t.Fatalf("创建时间 = %q", body.Comment.CreatedAt)
		}
		if f.notices.created != 1 {
			t.Fatalf("通知写入次数 = %d，期望 1", f.notices.created)
		}
	})

	t.Run("回复评论", func(t *testing.T) {
		f := newFixture()
		f.comments.comments[55] = sampleComment(55, uidAuthor, model.CommentStatusPublished)
		c, recorder := newJSONContext(http.MethodPost, "/api/v1/posts/20/comments",
			`{"content_format":"richtext","content_source":"<p>回复</p>","parent_comment_id":"55"}`)
		withParam(c, "id", "20")
		withViewer(c, viewerFor(uidViewer))

		f.handler().CreateComment(c)
		requireStatus(t, recorder, http.StatusCreated)
		body := decodeBody[struct {
			Comment CommentResponse `json:"comment"`
		}](t, recorder)
		if body.Comment.ParentCommentID == nil || *body.Comment.ParentCommentID != "55" {
			t.Fatalf("父评论 ID = %v，期望 55", body.Comment.ParentCommentID)
		}
		if body.Comment.RootCommentID == nil || *body.Comment.RootCommentID != "55" {
			t.Fatalf("根评论 ID = %v，期望 55", body.Comment.RootCommentID)
		}
	})

	t.Run("帖子不可见", func(t *testing.T) {
		f := newFixture()
		f.posts.visibleErr = apperr.NotFound("内容不存在或无权访问")
		c, recorder := newJSONContext(http.MethodPost, "/api/v1/posts/20/comments",
			`{"content_format":"richtext","content_source":"<p>你好</p>"}`)
		withParam(c, "id", "20")
		withViewer(c, viewerFor(uidViewer))
		f.handler().CreateComment(c)
		requireStatus(t, recorder, http.StatusNotFound)
		requireErrorCode(t, recorder, "not_found")
	})

	t.Run("限频", func(t *testing.T) {
		f := newFixture()
		f.commentLimiter = ratelimit.New(1, time.Hour)
		f.commentLimiter.Consume("user:" + strconv.FormatInt(uidViewer, 10))
		c, recorder := newJSONContext(http.MethodPost, "/api/v1/posts/20/comments",
			`{"content_format":"richtext","content_source":"<p>你好</p>"}`)
		withParam(c, "id", "20")
		withViewer(c, viewerFor(uidViewer))
		f.handler().CreateComment(c)
		requireStatus(t, recorder, http.StatusTooManyRequests)
		requireErrorCode(t, recorder, "rate_limited")
		if recorder.Header().Get("Retry-After") == "" {
			t.Fatal("限频响应应带 Retry-After 头")
		}
	})
}

// TestDeleteCommentHandler 验证删除评论的鉴权、参数与成功响应。
func TestDeleteCommentHandler(t *testing.T) {
	t.Run("未登录", func(t *testing.T) {
		f := newFixture()
		c, recorder := newTestContext(http.MethodPost, "/api/v1/comments/60/delete")
		withParam(c, "id", "60")
		f.handler().DeleteComment(c)
		requireStatus(t, recorder, http.StatusUnauthorized)
		requireErrorCode(t, recorder, "unauthenticated")
	})

	t.Run("ID 非法", func(t *testing.T) {
		f := newFixture()
		c, recorder := newTestContext(http.MethodPost, "/api/v1/comments/abc/delete")
		withParam(c, "id", "abc")
		withViewer(c, viewerFor(uidViewer))
		f.handler().DeleteComment(c)
		requireStatus(t, recorder, http.StatusBadRequest)
		requireErrorCode(t, recorder, "invalid_request")
	})

	t.Run("评论不存在", func(t *testing.T) {
		f := newFixture()
		c, recorder := newTestContext(http.MethodPost, "/api/v1/comments/60/delete")
		withParam(c, "id", "60")
		withViewer(c, viewerFor(uidViewer))
		f.handler().DeleteComment(c)
		requireStatus(t, recorder, http.StatusNotFound)
		requireErrorCode(t, recorder, "not_found")
	})

	t.Run("作者删除成功返回 204", func(t *testing.T) {
		f := newFixture()
		f.comments.comments[commentID] = sampleComment(commentID, uidViewer, model.CommentStatusPublished)
		c, recorder := newTestContext(http.MethodPost, "/api/v1/comments/60/delete")
		withParam(c, "id", "60")
		withViewer(c, viewerFor(uidViewer))
		f.handler().DeleteComment(c)
		// 204 无响应体，直接调用 handler 时需手动落地 gin 的响应状态。
		c.Writer.WriteHeaderNow()
		requireStatus(t, recorder, http.StatusNoContent)
		if recorder.Body.Len() != 0 {
			t.Fatalf("204 响应不应带响应体，实际 %q", recorder.Body.String())
		}
	})
}

// TestCommentResponseMapping 验证 DTO 的父/根评论、删除占位与时间格式。
func TestCommentResponseMapping(t *testing.T) {
	parentID, rootID := int64(55), int64(54)
	comment := sampleComment(101, uidOther, model.CommentStatusPublished)
	comment.ParentCommentID = &parentID
	comment.RootCommentID = &rootID
	resp := commentResponseFor(app.Comment{Row: comment, Author: model.UserBrief{ID: uidOther, Username: "carol"}, LikedByMe: true, CanDelete: true})
	if resp.ID != "101" || resp.PostID != "20" || resp.ParentCommentID == nil || *resp.ParentCommentID != "55" ||
		resp.RootCommentID == nil || *resp.RootCommentID != "54" {
		t.Fatalf("DTO ID 字段 = %+v", resp)
	}
	if !resp.LikedByMe || !resp.CanDelete {
		t.Fatalf("访问者状态 = (liked=%v, canDelete=%v)", resp.LikedByMe, resp.CanDelete)
	}

	deleted := comment
	deleted.Status = model.CommentStatusDeleted
	deletedResp := commentResponseFor(app.Comment{Row: deleted, LikedByMe: true, CanDelete: true})
	if deletedResp.ContentHTML != "" || deletedResp.LikedByMe || deletedResp.CanDelete {
		t.Fatalf("已删除评论 DTO 应脱敏: %+v", deletedResp)
	}

	if got := commentResponsesFor(nil); len(got) != 0 {
		t.Fatalf("空列表应返回空切片，实际 %v", got)
	}
	if empty := decodeBody[[]CommentResponse](t, recorderWithJSON(t, commentResponsesFor(nil))); len(empty) != 0 {
		t.Fatalf("空列表 JSON 应为 []，实际 %v", empty)
	}
}

// TestFollowRequestResponseMapping 验证关注申请 DTO 的处理时间格式。
func TestFollowRequestResponseMapping(t *testing.T) {
	resolvedAt := testNow.Add(-time.Minute)
	item := app.RequestItem{
		ID:         700,
		Status:     model.FollowRequestPending,
		Requester:  model.UserBrief{ID: uidViewer, Username: "alice"},
		Target:     model.UserBrief{ID: uidAuthor, Username: "bob"},
		CreatedAt:  testNow.Add(-time.Hour),
		ResolvedAt: &resolvedAt,
	}
	resp := followRequestResponseFor(item)
	if resp.ID != "700" || resp.Status != model.FollowRequestPending {
		t.Fatalf("申请 DTO = %+v", resp)
	}
	if resp.Requester.Username != "alice" || resp.Target.Username != "bob" {
		t.Fatalf("申请双方 = (%q, %q)", resp.Requester.Username, resp.Target.Username)
	}
	if resp.ResolvedAt == nil || *resp.ResolvedAt != format.FormatTime(resolvedAt) {
		t.Fatalf("处理时间 = %v，期望 %q", resp.ResolvedAt, format.FormatTime(resolvedAt))
	}
	if formatTimePtr(nil) != nil {
		t.Fatal("nil 时间应输出 null")
	}
}

// recorderWithJSON 把任意值序列化为响应记录器，便于复用 decodeBody。
func recorderWithJSON(t *testing.T, value any) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.JSON(http.StatusOK, value)
	return recorder
}
