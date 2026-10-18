package http

import (
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/ratelimit"
	"github.com/great-magician-01/timeline/internal/shared/format"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// interactionHandler 描述一个互动 handler 的调用方式。
type interactionHandler struct {
	name   string
	invoke func(*Handler, *gin.Context)
}

// interactionHandlers 返回点赞/转发六个接口的调用表。
func interactionHandlers() []interactionHandler {
	return []interactionHandler{
		{"Repost", func(h *Handler, c *gin.Context) { h.Repost(c) }},
		{"Unrepost", func(h *Handler, c *gin.Context) { h.Unrepost(c) }},
		{"LikePost", func(h *Handler, c *gin.Context) { h.LikePost(c) }},
		{"UnlikePost", func(h *Handler, c *gin.Context) { h.UnlikePost(c) }},
		{"LikeComment", func(h *Handler, c *gin.Context) { h.LikeComment(c) }},
		{"UnlikeComment", func(h *Handler, c *gin.Context) { h.UnlikeComment(c) }},
	}
}

// TestInteractionHandlersRequireLogin 验证互动接口都要求登录。
func TestInteractionHandlersRequireLogin(t *testing.T) {
	for _, h := range interactionHandlers() {
		t.Run(h.name, func(t *testing.T) {
			f := newFixture()
			c, recorder := newTestContext(http.MethodPost, "/api/v1/posts/20/like")
			withParam(c, "id", "20")
			h.invoke(f.handler(), c)
			requireStatus(t, recorder, http.StatusUnauthorized)
			requireErrorCode(t, recorder, "unauthenticated")
		})
	}
}

// TestInteractionHandlersInvalidID 验证互动接口的 ID 参数校验。
func TestInteractionHandlersInvalidID(t *testing.T) {
	for _, h := range interactionHandlers() {
		t.Run(h.name, func(t *testing.T) {
			f := newFixture()
			c, recorder := newTestContext(http.MethodPost, "/api/v1/posts/abc/like")
			withParam(c, "id", "abc")
			withViewer(c, viewerFor(uidViewer))
			h.invoke(f.handler(), c)
			requireStatus(t, recorder, http.StatusBadRequest)
			requireErrorCode(t, recorder, "invalid_request")
		})
	}
}

// TestLikePostHandler 验证帖子点赞/取消点赞成功响应与错误映射。
func TestLikePostHandler(t *testing.T) {
	t.Run("点赞成功", func(t *testing.T) {
		f := newFixture()
		f.posts.counter = 4
		c, recorder := newTestContext(http.MethodPost, "/api/v1/posts/20/like")
		withParam(c, "id", "20")
		withViewer(c, viewerFor(uidViewer))
		f.handler().LikePost(c)
		requireStatus(t, recorder, http.StatusOK)
		body := decodeBody[map[string]any](t, recorder)
		if body["liked"] != true || body["like_count"] != float64(4) {
			t.Fatalf("点赞响应 = %+v，期望 liked=true like_count=4", body)
		}
	})

	t.Run("取消点赞成功", func(t *testing.T) {
		f := newFixture()
		f.posts.counter = 2
		c, recorder := newTestContext(http.MethodPost, "/api/v1/posts/20/unlike")
		withParam(c, "id", "20")
		withViewer(c, viewerFor(uidViewer))
		f.handler().UnlikePost(c)
		requireStatus(t, recorder, http.StatusOK)
		body := decodeBody[map[string]any](t, recorder)
		if body["liked"] != false || body["like_count"] != float64(2) {
			t.Fatalf("取消点赞响应 = %+v", body)
		}
	})

	t.Run("帖子不可见", func(t *testing.T) {
		f := newFixture()
		f.posts.visibleErr = apperr.NotFound("内容不存在或无权访问")
		c, recorder := newTestContext(http.MethodPost, "/api/v1/posts/20/like")
		withParam(c, "id", "20")
		withViewer(c, viewerFor(uidViewer))
		f.handler().LikePost(c)
		requireStatus(t, recorder, http.StatusNotFound)
		requireErrorCode(t, recorder, "not_found")
	})

	t.Run("限频", func(t *testing.T) {
		f := newFixture()
		f.posts.counter = 1
		f.likeLimiter = ratelimit.New(1, time.Hour)
		f.likeLimiter.Consume("user:" + strconv.FormatInt(uidViewer, 10))
		c, recorder := newTestContext(http.MethodPost, "/api/v1/posts/20/like")
		withParam(c, "id", "20")
		withViewer(c, viewerFor(uidViewer))
		f.handler().LikePost(c)
		requireStatus(t, recorder, http.StatusTooManyRequests)
		requireErrorCode(t, recorder, "rate_limited")
		if recorder.Header().Get("Retry-After") == "" {
			t.Fatal("限频响应应带 Retry-After 头")
		}
	})
}

// TestLikeCommentHandler 验证评论点赞/取消点赞成功响应与错误映射。
func TestLikeCommentHandler(t *testing.T) {
	t.Run("点赞成功", func(t *testing.T) {
		f := newFixture()
		f.comments.comments[commentID] = sampleComment(commentID, uidOther, model.CommentStatusPublished)
		f.comments.likeCount = 9
		c, recorder := newTestContext(http.MethodPost, "/api/v1/comments/60/like")
		withParam(c, "id", "60")
		withViewer(c, viewerFor(uidViewer))
		f.handler().LikeComment(c)
		requireStatus(t, recorder, http.StatusOK)
		body := decodeBody[map[string]any](t, recorder)
		if body["liked"] != true || body["like_count"] != float64(9) {
			t.Fatalf("评论点赞响应 = %+v", body)
		}
	})

	t.Run("取消点赞成功", func(t *testing.T) {
		f := newFixture()
		f.comments.comments[commentID] = sampleComment(commentID, uidOther, model.CommentStatusPublished)
		f.comments.likeCount = 1
		c, recorder := newTestContext(http.MethodPost, "/api/v1/comments/60/unlike")
		withParam(c, "id", "60")
		withViewer(c, viewerFor(uidViewer))
		f.handler().UnlikeComment(c)
		requireStatus(t, recorder, http.StatusOK)
		body := decodeBody[map[string]any](t, recorder)
		if body["liked"] != false || body["like_count"] != float64(1) {
			t.Fatalf("取消评论点赞响应 = %+v", body)
		}
	})

	t.Run("评论不存在", func(t *testing.T) {
		f := newFixture()
		c, recorder := newTestContext(http.MethodPost, "/api/v1/comments/60/like")
		withParam(c, "id", "60")
		withViewer(c, viewerFor(uidViewer))
		f.handler().LikeComment(c)
		requireStatus(t, recorder, http.StatusNotFound)
		requireErrorCode(t, recorder, "not_found")
	})

	t.Run("评论已删除", func(t *testing.T) {
		f := newFixture()
		f.comments.comments[commentID] = sampleComment(commentID, uidOther, model.CommentStatusDeleted)
		c, recorder := newTestContext(http.MethodPost, "/api/v1/comments/60/like")
		withParam(c, "id", "60")
		withViewer(c, viewerFor(uidViewer))
		f.handler().LikeComment(c)
		requireStatus(t, recorder, http.StatusForbidden)
		requireErrorCode(t, recorder, "forbidden")
	})
}

// TestRepostHandler 验证纯转发/取消转发成功响应与错误映射。
func TestRepostHandler(t *testing.T) {
	t.Run("转发成功", func(t *testing.T) {
		f := newFixture()
		f.posts.counter = 6
		c, recorder := newTestContext(http.MethodPost, "/api/v1/posts/20/repost")
		withParam(c, "id", "20")
		withViewer(c, viewerFor(uidViewer))
		f.handler().Repost(c)
		requireStatus(t, recorder, http.StatusOK)
		body := decodeBody[map[string]any](t, recorder)
		if body["reposted"] != true || body["repost_count"] != float64(6) {
			t.Fatalf("转发响应 = %+v", body)
		}
		repost, ok := body["repost"].(map[string]any)
		if !ok {
			t.Fatalf("转发响应必须带 repost 对象: %+v", body)
		}
		// 首次转发生成 ID 101（测试 seqIDs 从 100 递增），时间来自应用时钟。
		if repost["id"] != format.FormatID(101) || repost["created_at"] != format.FormatTime(testNow) {
			t.Fatalf("repost 对象 = %+v，期望 id=101 created_at=%q", repost, format.FormatTime(testNow))
		}
	})

	t.Run("重复转发返回既有转发行", func(t *testing.T) {
		f := newFixture()
		f.posts.counter = 6
		f.reposts.activateChanged = false
		f.reposts.activateID = 900
		settledAt := testNow.Add(-time.Hour)
		f.reposts.activateAt = settledAt
		c, recorder := newTestContext(http.MethodPost, "/api/v1/posts/20/repost")
		withParam(c, "id", "20")
		withViewer(c, viewerFor(uidViewer))
		f.handler().Repost(c)
		requireStatus(t, recorder, http.StatusOK)
		body := decodeBody[map[string]any](t, recorder)
		repost, ok := body["repost"].(map[string]any)
		if !ok {
			t.Fatalf("幂等转发也必须返回 repost 对象: %+v", body)
		}
		if repost["id"] != format.FormatID(900) || repost["created_at"] != format.FormatTime(settledAt) {
			t.Fatalf("幂等 repost 对象 = %+v，期望既有行 id=900 created_at=%q", repost, format.FormatTime(settledAt))
		}
	})

	t.Run("取消转发成功", func(t *testing.T) {
		f := newFixture()
		f.posts.counter = 4
		c, recorder := newTestContext(http.MethodPost, "/api/v1/posts/20/unrepost")
		withParam(c, "id", "20")
		withViewer(c, viewerFor(uidViewer))
		f.handler().Unrepost(c)
		requireStatus(t, recorder, http.StatusOK)
		body := decodeBody[map[string]any](t, recorder)
		if body["reposted"] != false || body["repost_count"] != float64(4) {
			t.Fatalf("取消转发响应 = %+v", body)
		}
		if _, ok := body["repost"]; ok {
			t.Fatalf("取消转发响应不应包含 repost: %+v", body)
		}
	})

	t.Run("非公开内容禁止转发", func(t *testing.T) {
		f := newFixture()
		f.posts.visible.Visibility = model.VisibilityFollowers
		c, recorder := newTestContext(http.MethodPost, "/api/v1/posts/20/repost")
		withParam(c, "id", "20")
		withViewer(c, viewerFor(uidViewer))
		f.handler().Repost(c)
		requireStatus(t, recorder, http.StatusForbidden)
		requireErrorCode(t, recorder, "forbidden")
	})
}

// TestSetHandlersUnrecognizedError 验证无法识别的依赖错误统一映射为 500。
func TestSetHandlersUnrecognizedError(t *testing.T) {
	f := newFixture()
	f.likes.activateErr = errors.New("数据库连接中断")
	c, recorder := newTestContext(http.MethodPost, "/api/v1/posts/20/like")
	withParam(c, "id", "20")
	withViewer(c, viewerFor(uidViewer))
	f.handler().LikePost(c)
	requireStatus(t, recorder, http.StatusInternalServerError)
	requireErrorCode(t, recorder, "internal_error")
}
