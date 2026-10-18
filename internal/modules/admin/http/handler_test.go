package http

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// ===== 通用参数解析（handler.go 的 pageParams/pathID/decodeJSON/actor） =====

func TestPageParamsValidation(t *testing.T) {
	cases := []struct {
		name string
		call func(h *Handler, c *gin.Context)
		path string
	}{
		{"用户列表 limit=0", func(h *Handler, c *gin.Context) { h.ListUsers(c) }, "/api/v1/admin/users?limit=0"},
		{"用户列表 limit=51", func(h *Handler, c *gin.Context) { h.ListUsers(c) }, "/api/v1/admin/users?limit=51"},
		{"用户列表 limit 非数字", func(h *Handler, c *gin.Context) { h.ListUsers(c) }, "/api/v1/admin/users?limit=abc"},
		{"任务列表 limit=0", func(h *Handler, c *gin.Context) { h.ListTasks(c) }, "/api/v1/admin/tasks?limit=0"},
		{"审计列表 limit=-1", func(h *Handler, c *gin.Context) { h.ListAuditLogs(c) }, "/api/v1/admin/audit-logs?limit=-1"},
		{"帖子列表 limit=100", func(h *Handler, c *gin.Context) { h.ListPosts(c) }, "/api/v1/admin/posts?limit=100"},
		{"评论列表 limit 非数字", func(h *Handler, c *gin.Context) { h.ListComments(c) }, "/api/v1/admin/comments?limit=x"},
		{"附件列表 limit=0", func(h *Handler, c *gin.Context) { h.ListAttachments(c) }, "/api/v1/admin/attachments?limit=0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t)
			c, recorder := newTestContext(http.MethodGet, tc.path)
			tc.call(env.handler, c)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("状态码 = %d，期望 400，body=%s", recorder.Code, recorder.Body.String())
			}
			if code := errorCode(t, recorder); code != "invalid_request" {
				t.Fatalf("错误码 = %q，期望 invalid_request", code)
			}
		})
	}
}

func TestPageCursorValidation(t *testing.T) {
	cases := []struct {
		name string
		call func(h *Handler, c *gin.Context)
	}{
		{"用户列表", func(h *Handler, c *gin.Context) { h.ListUsers(c) }},
		{"任务列表", func(h *Handler, c *gin.Context) { h.ListTasks(c) }},
		{"审计列表", func(h *Handler, c *gin.Context) { h.ListAuditLogs(c) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t)
			c, recorder := newTestContext(http.MethodGet, "/api/v1/admin/users?cursor=not-base64!")
			tc.call(env.handler, c)
			if recorder.Code != http.StatusBadRequest || errorCode(t, recorder) != "invalid_request" {
				t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestPathIDValidation(t *testing.T) {
	cases := []struct {
		name string
		call func(h *Handler, c *gin.Context)
	}{
		{"GetUser", func(h *Handler, c *gin.Context) { h.GetUser(c) }},
		{"GetPost", func(h *Handler, c *gin.Context) { h.GetPost(c) }},
		{"SuspendUser", func(h *Handler, c *gin.Context) { h.SuspendUser(c) }},
		{"UnsuspendUser", func(h *Handler, c *gin.Context) { h.UnsuspendUser(c) }},
		{"ResetPassword", func(h *Handler, c *gin.Context) { h.ResetPassword(c) }},
		{"DeletePost", func(h *Handler, c *gin.Context) { h.DeletePost(c) }},
		{"DeleteComment", func(h *Handler, c *gin.Context) { h.DeleteComment(c) }},
		{"DeleteAttachment", func(h *Handler, c *gin.Context) { h.DeleteAttachment(c) }},
		{"RetryTask", func(h *Handler, c *gin.Context) { h.RetryTask(c) }},
		{"CancelTask", func(h *Handler, c *gin.Context) { h.CancelTask(c) }},
	}
	for _, tc := range cases {
		for _, rawID := range []string{"abc", "0", "-3"} {
			t.Run(tc.name+"/"+rawID, func(t *testing.T) {
				env := newTestEnv(t)
				c, recorder := newTestContext(http.MethodPost, "/api/v1/admin/resource/"+rawID)
				setPathID(c, rawID)
				tc.call(env.handler, c)
				if recorder.Code != http.StatusBadRequest || errorCode(t, recorder) != "invalid_request" {
					t.Fatalf("ID=%q 状态码 = %d，body=%s", rawID, recorder.Code, recorder.Body.String())
				}
			})
		}
	}
}

func TestDecodeJSONValidation(t *testing.T) {
	cases := []struct {
		name string
		call func(h *Handler, c *gin.Context)
		path string
	}{
		{"重置密码", func(h *Handler, c *gin.Context) { h.ResetPassword(c) }, "/api/v1/admin/users/2/reset-password"},
		{"更新设置", func(h *Handler, c *gin.Context) { h.UpdateSettings(c) }, "/api/v1/admin/settings"},
		{"更新搜索设置", func(h *Handler, c *gin.Context) { h.UpdateWebSearchSettings(c) }, "/api/v1/admin/settings/web-search"},
	}
	bodies := []string{`{`, `not-json`, `{"unknown_field": 1}`, `{"a":1}{"b":2}`}
	for _, tc := range cases {
		for _, body := range bodies {
			t.Run(tc.name+"/"+body, func(t *testing.T) {
				env := newTestEnv(t)
				c, recorder := newJSONContext(http.MethodPost, tc.path, body)
				setPathID(c, "2")
				withViewer(c, adminViewer())
				tc.call(env.handler, c)
				if recorder.Code != http.StatusBadRequest || errorCode(t, recorder) != "invalid_request" {
					t.Fatalf("body=%s 状态码 = %d，body=%s", body, recorder.Code, recorder.Body.String())
				}
			})
		}
	}
}

func TestActorRequiredForWriteHandlers(t *testing.T) {
	cases := []struct {
		name string
		call func(h *Handler, c *gin.Context)
	}{
		{"SuspendUser", func(h *Handler, c *gin.Context) { h.SuspendUser(c) }},
		{"UnsuspendUser", func(h *Handler, c *gin.Context) { h.UnsuspendUser(c) }},
		{"ResetPassword", func(h *Handler, c *gin.Context) { h.ResetPassword(c) }},
		{"DeletePost", func(h *Handler, c *gin.Context) { h.DeletePost(c) }},
		{"DeleteComment", func(h *Handler, c *gin.Context) { h.DeleteComment(c) }},
		{"DeleteAttachment", func(h *Handler, c *gin.Context) { h.DeleteAttachment(c) }},
		{"RetryTask", func(h *Handler, c *gin.Context) { h.RetryTask(c) }},
		{"CancelTask", func(h *Handler, c *gin.Context) { h.CancelTask(c) }},
		{"UpdateSettings", func(h *Handler, c *gin.Context) { h.UpdateSettings(c) }},
		{"UpdateWebSearchSettings", func(h *Handler, c *gin.Context) { h.UpdateWebSearchSettings(c) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t)
			c, recorder := newJSONContext(http.MethodPost, "/api/v1/admin/resource/2", `{}`)
			setPathID(c, "2")
			tc.call(env.handler, c)
			if recorder.Code != http.StatusUnauthorized || errorCode(t, recorder) != "unauthenticated" {
				t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

// ===== 列表过滤参数：校验必须在触库前失败 =====

func TestListUsersRejectsInvalidFilters(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{"is_ai 非布尔", "is_ai=maybe"},
		{"status 非法", "status=deleted"},
		{"关键词超长", "q=" + strings.Repeat("搜", 101)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t)
			c, recorder := newTestContext(http.MethodGet, "/api/v1/admin/users?"+tc.query)
			env.handler.ListUsers(c)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("状态码 = %d，期望 400，body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestListPostsRejectsInvalidFilters(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{"visibility 非法", "visibility=secret"},
		{"status 非法", "status=archived"},
		{"关键词超长", "q=" + strings.Repeat("搜", 101)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t)
			c, recorder := newTestContext(http.MethodGet, "/api/v1/admin/posts?"+tc.query)
			env.handler.ListPosts(c)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("状态码 = %d，期望 400，body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestListCommentsRejectsOverlongKeyword(t *testing.T) {
	env := newTestEnv(t)
	c, recorder := newTestContext(http.MethodGet, "/api/v1/admin/comments?q="+strings.Repeat("评", 101))
	env.handler.ListComments(c)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d，期望 400，body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestListAttachmentsRejectsInvalidFilters(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{"uploader_id 非法", "uploader_id=abc"},
		{"post_id 为 0", "post_id=0"},
		{"purpose 非法", "purpose=video"},
		{"status 非法", "status=processing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t)
			c, recorder := newTestContext(http.MethodGet, "/api/v1/admin/attachments?"+tc.query)
			env.handler.ListAttachments(c)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("状态码 = %d，期望 400，body=%s", recorder.Code, recorder.Body.String())
			}
			if code := errorCode(t, recorder); code != "invalid_request" {
				t.Fatalf("错误码 = %q，期望 invalid_request", code)
			}
		})
	}
}
