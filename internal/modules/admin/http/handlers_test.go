package http

import (
	"errors"
	"net/http"
	"testing"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// ===== 用户管理 =====

func TestSuspendUserHandler(t *testing.T) {
	t.Run("成功", func(t *testing.T) {
		env := newTestEnv(t)
		c, recorder := newTestContext(http.MethodPost, "/api/v1/admin/users/2/suspend")
		setPathID(c, "2")
		withViewer(c, adminViewer())

		env.handler.SuspendUser(c)
		if recorder.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，期望 200，body=%s", recorder.Code, recorder.Body.String())
		}
		body := decodeBody(t, recorder)
		user, ok := body["user"].(map[string]any)
		if !ok || user["status"] != "suspended" {
			t.Fatalf("响应 user 不符: %s", recorder.Body.String())
		}
		if env.auth.statusInput == nil || !env.auth.statusInput.Suspend || env.auth.statusInput.TargetID != 2 {
			t.Fatalf("auth 端口入参不符: %+v", env.auth.statusInput)
		}
		actions := env.audit.actions()
		if len(actions) != 1 || actions[0] != contracts.AuditActionUserSuspend {
			t.Fatalf("应只写一条 user_suspend 审计，实际: %v", actions)
		}
	})

	t.Run("普通管理员操作超管返回 403", func(t *testing.T) {
		env := newTestEnv(t)
		env.auth.user.Role = "admin"
		env.auth.user.IsSuperAdmin = true
		c, recorder := newTestContext(http.MethodPost, "/api/v1/admin/users/2/suspend")
		setPathID(c, "2")
		withViewer(c, adminViewer())

		env.handler.SuspendUser(c)
		if recorder.Code != http.StatusForbidden || errorCode(t, recorder) != "forbidden" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("停用自己返回 400", func(t *testing.T) {
		env := newTestEnv(t)
		env.auth.user.ID = adminViewer().ID
		c, recorder := newTestContext(http.MethodPost, "/api/v1/admin/users/1/suspend")
		setPathID(c, "1")
		withViewer(c, adminViewer())

		env.handler.SuspendUser(c)
		if recorder.Code != http.StatusBadRequest || errorCode(t, recorder) != "validation_failed" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("端口内部错误返回 500", func(t *testing.T) {
		env := newTestEnv(t)
		env.auth.statusErr = errors.New("更新状态失败")
		c, recorder := newTestContext(http.MethodPost, "/api/v1/admin/users/2/suspend")
		setPathID(c, "2")
		withViewer(c, adminViewer())

		env.handler.SuspendUser(c)
		if recorder.Code != http.StatusInternalServerError || errorCode(t, recorder) != "internal_error" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})
}

func TestUnsuspendUserHandler(t *testing.T) {
	env := newTestEnv(t)
	env.auth.user.Status = "suspended"
	c, recorder := newTestContext(http.MethodPost, "/api/v1/admin/users/2/unsuspend")
	setPathID(c, "2")
	withViewer(c, adminViewer())

	env.handler.UnsuspendUser(c)
	if recorder.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200，body=%s", recorder.Code, recorder.Body.String())
	}
	body := decodeBody(t, recorder)
	user, ok := body["user"].(map[string]any)
	if !ok || user["status"] != "active" {
		t.Fatalf("响应 user 不符: %s", recorder.Body.String())
	}
	if env.auth.statusInput == nil || env.auth.statusInput.Suspend {
		t.Fatalf("auth 端口应收到启用调用: %+v", env.auth.statusInput)
	}
}

func TestResetPasswordHandler(t *testing.T) {
	t.Run("成功返回 204", func(t *testing.T) {
		env := newTestEnv(t)
		c, recorder := newJSONContext(http.MethodPost, "/api/v1/admin/users/2/reset-password",
			`{"new_password":"new-password-123"}`)
		setPathID(c, "2")
		withViewer(c, adminViewer())

		env.handler.ResetPassword(c)
		// 直接调用 handler 时 gin 尚未把状态 flush 到 recorder，以 writer 状态为准。
		if c.Writer.Status() != http.StatusNoContent || recorder.Body.Len() != 0 {
			t.Fatalf("状态码 = %d，期望 204，body=%s", c.Writer.Status(), recorder.Body.String())
		}
		if env.auth.resetInput == nil || env.auth.resetInput.NewPassword != "new-password-123" ||
			env.auth.resetInput.TargetID != 2 || env.auth.resetInput.ActorID != adminViewer().ID {
			t.Fatalf("auth 端口入参不符: %+v", env.auth.resetInput)
		}
	})

	t.Run("AI 账号返回 400", func(t *testing.T) {
		env := newTestEnv(t)
		env.auth.user.IsAI = true
		c, recorder := newJSONContext(http.MethodPost, "/api/v1/admin/users/2/reset-password",
			`{"new_password":"new-password-123"}`)
		setPathID(c, "2")
		withViewer(c, adminViewer())

		env.handler.ResetPassword(c)
		if recorder.Code != http.StatusBadRequest || errorCode(t, recorder) != "validation_failed" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("非管理员返回 403", func(t *testing.T) {
		env := newTestEnv(t)
		c, recorder := newJSONContext(http.MethodPost, "/api/v1/admin/users/2/reset-password",
			`{"new_password":"new-password-123"}`)
		setPathID(c, "2")
		withViewer(c, model.Viewer{ID: 3, Authenticated: true})

		env.handler.ResetPassword(c)
		if recorder.Code != http.StatusForbidden || errorCode(t, recorder) != "forbidden" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("端口内部错误返回 500", func(t *testing.T) {
		env := newTestEnv(t)
		env.auth.resetErr = errors.New("重置密码失败")
		c, recorder := newJSONContext(http.MethodPost, "/api/v1/admin/users/2/reset-password",
			`{"new_password":"new-password-123"}`)
		setPathID(c, "2")
		withViewer(c, adminViewer())

		env.handler.ResetPassword(c)
		if recorder.Code != http.StatusInternalServerError || errorCode(t, recorder) != "internal_error" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})
}

// ===== 内容处置 =====

func TestDeletePostHandler(t *testing.T) {
	t.Run("成功返回 204", func(t *testing.T) {
		env := newTestEnv(t)
		c, recorder := newTestContext(http.MethodPost, "/api/v1/admin/posts/88/delete")
		setPathID(c, "88")
		withViewer(c, adminViewer())

		env.handler.DeletePost(c)
		// 直接调用 handler 时 gin 尚未把状态 flush 到 recorder，以 writer 状态为准。
		if c.Writer.Status() != http.StatusNoContent || recorder.Body.Len() != 0 {
			t.Fatalf("状态码 = %d，期望 204，body=%s", c.Writer.Status(), recorder.Body.String())
		}
		if len(env.content.deleted) != 1 || env.content.deleted[0].AdminID != adminViewer().ID ||
			env.content.deleted[0].PostID != 88 {
			t.Fatalf("content 端口入参不符: %+v", env.content.deleted)
		}
	})

	t.Run("不存在返回 404", func(t *testing.T) {
		env := newTestEnv(t)
		env.content.deleteErr = apperr.NotFound("帖子不存在")
		c, recorder := newTestContext(http.MethodPost, "/api/v1/admin/posts/88/delete")
		setPathID(c, "88")
		withViewer(c, adminViewer())

		env.handler.DeletePost(c)
		if recorder.Code != http.StatusNotFound || errorCode(t, recorder) != "not_found" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("未知错误返回 500", func(t *testing.T) {
		env := newTestEnv(t)
		env.content.deleteErr = errors.New("删除帖子失败")
		c, recorder := newTestContext(http.MethodPost, "/api/v1/admin/posts/88/delete")
		setPathID(c, "88")
		withViewer(c, adminViewer())

		env.handler.DeletePost(c)
		if recorder.Code != http.StatusInternalServerError || errorCode(t, recorder) != "internal_error" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})
}

func TestDeleteCommentHandler(t *testing.T) {
	t.Run("成功返回 204", func(t *testing.T) {
		env := newTestEnv(t)
		c, recorder := newTestContext(http.MethodPost, "/api/v1/admin/comments/66/delete")
		setPathID(c, "66")
		withViewer(c, adminViewer())

		env.handler.DeleteComment(c)
		// 直接调用 handler 时 gin 尚未把状态 flush 到 recorder，以 writer 状态为准。
		if c.Writer.Status() != http.StatusNoContent || recorder.Body.Len() != 0 {
			t.Fatalf("状态码 = %d，期望 204，body=%s", c.Writer.Status(), recorder.Body.String())
		}
		if len(env.social.deleted) != 1 || !env.social.deleted[0].ByAdmin ||
			env.social.deleted[0].CommentID != 66 || env.social.deleted[0].ActorID != adminViewer().ID {
			t.Fatalf("social 端口入参不符: %+v", env.social.deleted)
		}
	})

	t.Run("不存在返回 404", func(t *testing.T) {
		env := newTestEnv(t)
		env.social.deleteErr = apperr.NotFound("评论不存在")
		c, recorder := newTestContext(http.MethodPost, "/api/v1/admin/comments/66/delete")
		setPathID(c, "66")
		withViewer(c, adminViewer())

		env.handler.DeleteComment(c)
		if recorder.Code != http.StatusNotFound || errorCode(t, recorder) != "not_found" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})
}

func TestDeleteAttachmentHandler(t *testing.T) {
	t.Run("成功返回 204", func(t *testing.T) {
		env := newTestEnv(t)
		c, recorder := newTestContext(http.MethodPost, "/api/v1/admin/attachments/55/delete")
		setPathID(c, "55")
		withViewer(c, adminViewer())

		env.handler.DeleteAttachment(c)
		// 直接调用 handler 时 gin 尚未把状态 flush 到 recorder，以 writer 状态为准。
		if c.Writer.Status() != http.StatusNoContent || recorder.Body.Len() != 0 {
			t.Fatalf("状态码 = %d，期望 204，body=%s", c.Writer.Status(), recorder.Body.String())
		}
		if len(env.media.deleted) != 1 || env.media.deleted[0].AttachmentID != 55 ||
			env.media.deleted[0].AdminID != adminViewer().ID {
			t.Fatalf("media 端口入参不符: %+v", env.media.deleted)
		}
	})

	t.Run("不存在返回 404", func(t *testing.T) {
		env := newTestEnv(t)
		env.media.deleteErr = apperr.NotFound("附件不存在")
		c, recorder := newTestContext(http.MethodPost, "/api/v1/admin/attachments/55/delete")
		setPathID(c, "55")
		withViewer(c, adminViewer())

		env.handler.DeleteAttachment(c)
		if recorder.Code != http.StatusNotFound || errorCode(t, recorder) != "not_found" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})
}

// ===== 后台任务 =====

func TestRetryTaskHandler(t *testing.T) {
	newJobEnv := func(t *testing.T) *testEnv {
		t.Helper()
		env := newTestEnv(t)
		env.jobs.job = contracts.JobSnapshot{
			ID:       42,
			Type:     contracts.JobTypePostPublish,
			RefType:  "post",
			Status:   contracts.JobStatusFailed,
			Attempts: 2,
		}
		return env
	}

	t.Run("成功返回作业快照", func(t *testing.T) {
		env := newJobEnv(t)
		c, recorder := newTestContext(http.MethodPost, "/api/v1/admin/tasks/42/retry")
		setPathID(c, "42")
		withViewer(c, adminViewer())

		env.handler.RetryTask(c)
		if recorder.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，期望 200，body=%s", recorder.Code, recorder.Body.String())
		}
		body := decodeBody(t, recorder)
		job, ok := body["job"].(map[string]any)
		if !ok || job["id"] != "42" || job["status"] != contracts.JobStatusPending {
			t.Fatalf("响应 job 不符: %s", recorder.Body.String())
		}
		actions := env.audit.actions()
		if len(actions) != 1 || actions[0] != contracts.AuditActionTaskRetry {
			t.Fatalf("应写一条 task_retry 审计，实际: %v", actions)
		}
	})

	t.Run("状态非法返回 400 与详情", func(t *testing.T) {
		env := newJobEnv(t)
		env.jobs.job.Status = contracts.JobStatusRunning
		c, recorder := newTestContext(http.MethodPost, "/api/v1/admin/tasks/42/retry")
		setPathID(c, "42")
		withViewer(c, adminViewer())

		env.handler.RetryTask(c)
		if recorder.Code != http.StatusBadRequest || errorCode(t, recorder) != "validation_failed" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
		if details := errorDetails(t, recorder); details["status"] == "" {
			t.Fatalf("validation_failed 应带 status 详情: %v", details)
		}
	})

	t.Run("作业不存在返回 404", func(t *testing.T) {
		env := newJobEnv(t)
		env.jobs.getErr = apperr.NotFound("作业不存在")
		c, recorder := newTestContext(http.MethodPost, "/api/v1/admin/tasks/42/retry")
		setPathID(c, "42")
		withViewer(c, adminViewer())

		env.handler.RetryTask(c)
		if recorder.Code != http.StatusNotFound || errorCode(t, recorder) != "not_found" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("端口内部错误返回 500", func(t *testing.T) {
		env := newJobEnv(t)
		env.jobs.retryErr = errors.New("重试失败")
		c, recorder := newTestContext(http.MethodPost, "/api/v1/admin/tasks/42/retry")
		setPathID(c, "42")
		withViewer(c, adminViewer())

		env.handler.RetryTask(c)
		if recorder.Code != http.StatusInternalServerError || errorCode(t, recorder) != "internal_error" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})
}

func TestCancelTaskHandler(t *testing.T) {
	t.Run("成功返回作业快照", func(t *testing.T) {
		env := newTestEnv(t)
		env.jobs.job = contracts.JobSnapshot{ID: 42, Status: contracts.JobStatusPending}
		c, recorder := newTestContext(http.MethodPost, "/api/v1/admin/tasks/42/cancel")
		setPathID(c, "42")
		withViewer(c, adminViewer())

		env.handler.CancelTask(c)
		if recorder.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，期望 200，body=%s", recorder.Code, recorder.Body.String())
		}
		body := decodeBody(t, recorder)
		job, ok := body["job"].(map[string]any)
		if !ok || job["status"] != contracts.JobStatusCanceled {
			t.Fatalf("响应 job 不符: %s", recorder.Body.String())
		}
		if len(env.jobs.canceled) != 1 || env.jobs.canceled[0].JobID != 42 {
			t.Fatalf("作业端口入参不符: %+v", env.jobs.canceled)
		}
	})

	t.Run("终态任务返回 400", func(t *testing.T) {
		env := newTestEnv(t)
		env.jobs.job = contracts.JobSnapshot{ID: 42, Status: contracts.JobStatusFailed}
		c, recorder := newTestContext(http.MethodPost, "/api/v1/admin/tasks/42/cancel")
		setPathID(c, "42")
		withViewer(c, adminViewer())

		env.handler.CancelTask(c)
		if recorder.Code != http.StatusBadRequest || errorCode(t, recorder) != "validation_failed" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})
}

// ===== 系统设置 =====

func TestGetSettingsHandler(t *testing.T) {
	t.Run("成功返回设置字段", func(t *testing.T) {
		env := newTestEnv(t)
		c, recorder := newTestContext(http.MethodGet, "/api/v1/admin/settings")
		env.handler.GetSettings(c)
		if recorder.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，期望 200，body=%s", recorder.Code, recorder.Body.String())
		}
		body := decodeBody(t, recorder)
		if body["registration_enabled"] != true || body["post_rate_limit"] != float64(30) {
			t.Fatalf("设置字段不符: %s", recorder.Body.String())
		}
		if body["post_rate_window_seconds"] != float64(600) {
			t.Fatalf("时间字段应换算为秒: %s", recorder.Body.String())
		}
	})

	t.Run("读取失败返回 500", func(t *testing.T) {
		env := newTestEnv(t)
		env.settings.settingsErr = errors.New("读取设置失败")
		c, recorder := newTestContext(http.MethodGet, "/api/v1/admin/settings")
		env.handler.GetSettings(c)
		if recorder.Code != http.StatusInternalServerError || errorCode(t, recorder) != "internal_error" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})
}

func TestUpdateSettingsHandler(t *testing.T) {
	t.Run("成功更新注册开关", func(t *testing.T) {
		env := newTestEnv(t)
		c, recorder := newJSONContext(http.MethodPost, "/api/v1/admin/settings", `{"registration_enabled":false}`)
		withViewer(c, adminViewer())

		env.handler.UpdateSettings(c)
		if recorder.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，期望 200，body=%s", recorder.Code, recorder.Body.String())
		}
		body := decodeBody(t, recorder)
		if body["registration_enabled"] != false {
			t.Fatalf("响应设置未生效: %s", recorder.Body.String())
		}
		actions := env.audit.actions()
		if len(actions) != 1 || actions[0] != contracts.AuditActionRegistrationUpdate {
			t.Fatalf("应写一条注册开关审计，实际: %v", actions)
		}
	})

	t.Run("成功关闭全局 AI", func(t *testing.T) {
		env := newTestEnv(t)
		env.settings.values.AIGlobalEnabled = true
		c, recorder := newJSONContext(http.MethodPost, "/api/v1/admin/settings", `{"ai_global_enabled":false}`)
		withViewer(c, adminViewer())

		env.handler.UpdateSettings(c)
		if recorder.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，期望 200，body=%s", recorder.Code, recorder.Body.String())
		}
		if env.globalAI.haltCalls != 1 {
			t.Fatalf("应调用 agent 端口停用全局 AI 一次，实际 %d", env.globalAI.haltCalls)
		}
	})

	t.Run("空 patch 返回 400 与详情", func(t *testing.T) {
		env := newTestEnv(t)
		c, recorder := newJSONContext(http.MethodPost, "/api/v1/admin/settings", `{}`)
		withViewer(c, adminViewer())

		env.handler.UpdateSettings(c)
		if recorder.Code != http.StatusBadRequest || errorCode(t, recorder) != "validation_failed" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("非法设置值返回 400 与字段详情", func(t *testing.T) {
		env := newTestEnv(t)
		c, recorder := newJSONContext(http.MethodPost, "/api/v1/admin/settings", `{"post_rate_limit":0}`)
		withViewer(c, adminViewer())

		env.handler.UpdateSettings(c)
		if recorder.Code != http.StatusBadRequest || errorCode(t, recorder) != "validation_failed" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
		if details := errorDetails(t, recorder); details["post_rate_limit"] == nil {
			t.Fatalf("应带 post_rate_limit 字段详情: %v", details)
		}
	})

	t.Run("写库失败返回 500", func(t *testing.T) {
		env := newTestEnv(t)
		env.settings.setErr = errors.New("设置写库失败")
		c, recorder := newJSONContext(http.MethodPost, "/api/v1/admin/settings", `{"post_rate_limit":10}`)
		withViewer(c, adminViewer())

		env.handler.UpdateSettings(c)
		if recorder.Code != http.StatusInternalServerError || errorCode(t, recorder) != "internal_error" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})
}

// ===== 全局网页搜索设置 =====

func TestGetWebSearchSettingsHandler(t *testing.T) {
	t.Run("成功返回掩码且不泄露密钥", func(t *testing.T) {
		env := newTestEnv(t)
		env.settings.webSearch = contracts.WebSearchSettings{
			BaseURL: "https://api.example.com", APIKeyCipher: "v1:old-key",
		}
		c, recorder := newTestContext(http.MethodGet, "/api/v1/admin/settings/web-search")
		env.handler.GetWebSearchSettings(c)
		if recorder.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，期望 200，body=%s", recorder.Code, recorder.Body.String())
		}
		body := decodeBody(t, recorder)
		if body["api_key_masked"] == "" || body["configured"] != true || body["usable"] != true {
			t.Fatalf("搜索设置响应不符: %s", recorder.Body.String())
		}
		if _, ok := body["api_key"]; ok {
			t.Fatal("响应不得包含 api_key 字段")
		}
		if _, ok := body["api_key_cipher"]; ok {
			t.Fatal("响应不得包含密文字段")
		}
	})

	t.Run("读取失败返回 500", func(t *testing.T) {
		env := newTestEnv(t)
		env.settings.webSearchErr = errors.New("读取搜索设置失败")
		c, recorder := newTestContext(http.MethodGet, "/api/v1/admin/settings/web-search")
		env.handler.GetWebSearchSettings(c)
		if recorder.Code != http.StatusInternalServerError || errorCode(t, recorder) != "internal_error" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})
}

func TestUpdateWebSearchSettingsHandler(t *testing.T) {
	t.Run("成功保存 Base URL", func(t *testing.T) {
		env := newTestEnv(t)
		c, recorder := newJSONContext(http.MethodPost, "/api/v1/admin/settings/web-search",
			`{"base_url":" https://search.example.com/ "}`)
		withViewer(c, adminViewer())

		env.handler.UpdateWebSearchSettings(c)
		if recorder.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，期望 200，body=%s", recorder.Code, recorder.Body.String())
		}
		body := decodeBody(t, recorder)
		if body["base_url"] != "https://search.example.com" {
			t.Fatalf("Base URL 应归一化: %s", recorder.Body.String())
		}
		actions := env.audit.actions()
		if len(actions) != 1 || actions[0] != contracts.AuditActionSettingsUpdate {
			t.Fatalf("应写一条 settings_update 审计，实际: %v", actions)
		}
	})

	t.Run("非法 Base URL 返回 400", func(t *testing.T) {
		env := newTestEnv(t)
		c, recorder := newJSONContext(http.MethodPost, "/api/v1/admin/settings/web-search",
			`{"base_url":"ftp://search.example.com"}`)
		withViewer(c, adminViewer())

		env.handler.UpdateWebSearchSettings(c)
		if recorder.Code != http.StatusBadRequest || errorCode(t, recorder) != "validation_failed" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
		if details := errorDetails(t, recorder); details["base_url"] == nil {
			t.Fatalf("应带 base_url 字段详情: %v", details)
		}
	})

	t.Run("同时保存与清除密钥返回 400", func(t *testing.T) {
		env := newTestEnv(t)
		c, recorder := newJSONContext(http.MethodPost, "/api/v1/admin/settings/web-search",
			`{"api_key":"new-key","clear_api_key":true}`)
		withViewer(c, adminViewer())

		env.handler.UpdateWebSearchSettings(c)
		if recorder.Code != http.StatusBadRequest || errorCode(t, recorder) != "validation_failed" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("写库失败返回 500", func(t *testing.T) {
		env := newTestEnv(t)
		env.settings.setErr = errors.New("写库失败")
		c, recorder := newJSONContext(http.MethodPost, "/api/v1/admin/settings/web-search",
			`{"base_url":"https://search.example.com"}`)
		withViewer(c, adminViewer())

		env.handler.UpdateWebSearchSettings(c)
		if recorder.Code != http.StatusInternalServerError || errorCode(t, recorder) != "internal_error" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})
}
