package http

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/modules/admin/app"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// ===== 概览 =====

// GetOverview 处理 GET /api/v1/admin/overview。
func (h *Handler) GetOverview(c *gin.Context) {
	overview, err := h.service.GetOverview(c.Request.Context())
	if err != nil {
		h.fail(c, "读取管理概览失败", err)
		return
	}
	c.JSON(http.StatusOK, overviewView(overview))
}

// ===== 用户管理 =====

// ListUsers 处理 GET /api/v1/admin/users：支持 q/status/is_ai 过滤与游标分页。
func (h *Handler) ListUsers(c *gin.Context) {
	limit, cursor, ok := h.pageParams(c)
	if !ok {
		return
	}
	query := app.UsersQuery{
		Status:  strings.TrimSpace(c.Query("status")),
		Keyword: strings.TrimSpace(c.Query("q")),
		Cursor:  cursor,
		Limit:   limit,
	}
	if rawAI := strings.TrimSpace(c.Query("is_ai")); rawAI != "" {
		isAI, err := strconv.ParseBool(rawAI)
		if err != nil {
			httpx.Error(c, http.StatusBadRequest, "invalid_request", "is_ai 必须是布尔值")
			return
		}
		query.IsAI = &isAI
	}
	page, err := h.service.ListUsers(c.Request.Context(), query)
	if err != nil {
		h.fail(c, "查询管理用户列表失败", err)
		return
	}
	c.JSON(http.StatusOK, listResponse(page, func(u model.User) any { return userResponseFor(u) }))
}

// GetUser 处理 GET /api/v1/admin/users/:id。
func (h *Handler) GetUser(c *gin.Context) {
	id, ok := h.pathID(c, "id")
	if !ok {
		return
	}
	detail, err := h.service.GetUser(c.Request.Context(), id)
	if err != nil {
		h.fail(c, "读取管理用户详情失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"user":            userResponseFor(detail.User),
		"active_sessions": detail.ActiveSessions,
		"stats":           userStatsView(detail.Stats),
	})
}

// SuspendUser 处理 POST /api/v1/admin/users/:id/suspend。
func (h *Handler) SuspendUser(c *gin.Context) { h.changeUserStatus(c, true) }

// UnsuspendUser 处理 POST /api/v1/admin/users/:id/unsuspend。
func (h *Handler) UnsuspendUser(c *gin.Context) { h.changeUserStatus(c, false) }

func (h *Handler) changeUserStatus(c *gin.Context, suspend bool) {
	id, ok := h.pathID(c, "id")
	if !ok {
		return
	}
	actor, ok := h.actor(c)
	if !ok {
		return
	}
	var (
		updated model.User
		err     error
	)
	if suspend {
		updated, err = h.service.SuspendUser(c.Request.Context(), actor, id)
	} else {
		updated, err = h.service.UnsuspendUser(c.Request.Context(), actor, id)
	}
	if err != nil {
		h.fail(c, "更新账号状态失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": userResponseFor(updated)})
}

// ResetPassword 处理 POST /api/v1/admin/users/:id/reset-password。
func (h *Handler) ResetPassword(c *gin.Context) {
	id, ok := h.pathID(c, "id")
	if !ok {
		return
	}
	actor, ok := h.actor(c)
	if !ok {
		return
	}
	var in struct {
		NewPassword string `json:"new_password"`
	}
	if !h.decodeJSON(c, &in) {
		return
	}
	if err := h.service.ResetPassword(c.Request.Context(), actor, id, in.NewPassword); err != nil {
		h.fail(c, "管理员重置密码失败", err)
		return
	}
	noContent(c)
}

// ===== 内容审核 =====

// ListPosts 处理 GET /api/v1/admin/posts：支持 visibility/status/author/q 过滤。
func (h *Handler) ListPosts(c *gin.Context) {
	limit, cursor, ok := h.pageParams(c)
	if !ok {
		return
	}
	page, err := h.service.ListPosts(c.Request.Context(), app.PostsQuery{
		Visibility: strings.TrimSpace(c.Query("visibility")),
		Status:     strings.TrimSpace(c.Query("status")),
		Author:     strings.TrimSpace(c.Query("author")),
		Keyword:    strings.TrimSpace(c.Query("q")),
		Cursor:     cursor,
		Limit:      limit,
	})
	if err != nil {
		h.fail(c, "查询后台帖子列表失败", err)
		return
	}
	c.JSON(http.StatusOK, listResponse(page, func(item app.PostItem) any { return adminPostView(item) }))
}

// GetPost 处理 GET /api/v1/admin/posts/:id：正文、历史版本、附件与评论上下文。
func (h *Handler) GetPost(c *gin.Context) {
	id, ok := h.pathID(c, "id")
	if !ok {
		return
	}
	detail, err := h.service.GetPost(c.Request.Context(), id)
	if err != nil {
		h.fail(c, "读取后台帖子详情失败", err)
		return
	}
	c.JSON(http.StatusOK, postDetailView(detail))
}

// DeletePost 处理 POST /api/v1/admin/posts/:id/delete：逻辑删除并写审计（content 同事务）。
func (h *Handler) DeletePost(c *gin.Context) {
	id, ok := h.pathID(c, "id")
	if !ok {
		return
	}
	actor, ok := h.actor(c)
	if !ok {
		return
	}
	if err := h.service.DeletePost(c.Request.Context(), actor, id); err != nil {
		h.fail(c, "删除帖子失败", err)
		return
	}
	noContent(c)
}

// ListComments 处理 GET /api/v1/admin/comments：支持 author/q 过滤。
func (h *Handler) ListComments(c *gin.Context) {
	limit, cursor, ok := h.pageParams(c)
	if !ok {
		return
	}
	page, err := h.service.ListComments(c.Request.Context(), app.CommentsQuery{
		Author:  strings.TrimSpace(c.Query("author")),
		Keyword: strings.TrimSpace(c.Query("q")),
		Cursor:  cursor,
		Limit:   limit,
	})
	if err != nil {
		h.fail(c, "查询后台评论列表失败", err)
		return
	}
	c.JSON(http.StatusOK, listResponse(page, func(item app.CommentItem) any { return adminCommentView(item) }))
}

// DeleteComment 处理 POST /api/v1/admin/comments/:id/delete。
func (h *Handler) DeleteComment(c *gin.Context) {
	id, ok := h.pathID(c, "id")
	if !ok {
		return
	}
	actor, ok := h.actor(c)
	if !ok {
		return
	}
	if err := h.service.DeleteComment(c.Request.Context(), actor, id); err != nil {
		h.fail(c, "删除评论失败", err)
		return
	}
	noContent(c)
}

// ListAttachments 处理 GET /api/v1/admin/attachments：
// 支持 uploader_id/post_id/purpose/status/q（原文件名）过滤与游标分页。
func (h *Handler) ListAttachments(c *gin.Context) {
	limit, cursor, ok := h.pageParams(c)
	if !ok {
		return
	}
	query := app.AttachmentsQuery{
		Purpose: strings.TrimSpace(c.Query("purpose")),
		Status:  strings.TrimSpace(c.Query("status")),
		Keyword: strings.TrimSpace(c.Query("q")),
		Cursor:  cursor,
		Limit:   limit,
	}
	for _, filter := range []struct {
		name string
		dst  **int64
	}{
		{"uploader_id", &query.UploaderID},
		{"post_id", &query.PostID},
	} {
		raw := strings.TrimSpace(c.Query(filter.name))
		if raw == "" {
			continue
		}
		id, err := httpx.ParseID(raw)
		if err != nil {
			httpx.Error(c, http.StatusBadRequest, "invalid_request", filter.name+" 必须是有效的 ID")
			return
		}
		*filter.dst = &id
	}
	page, err := h.service.ListAttachments(c.Request.Context(), query)
	if err != nil {
		h.fail(c, "查询后台附件列表失败", err)
		return
	}
	c.JSON(http.StatusOK, listResponse(page, func(item app.AttachmentItem) any { return adminAttachmentView(item) }))
}

// DeleteAttachment 处理 POST /api/v1/admin/attachments/:id/delete。
func (h *Handler) DeleteAttachment(c *gin.Context) {
	id, ok := h.pathID(c, "id")
	if !ok {
		return
	}
	actor, ok := h.actor(c)
	if !ok {
		return
	}
	if err := h.service.DeleteAttachment(c.Request.Context(), actor, id); err != nil {
		h.fail(c, "删除附件失败", err)
		return
	}
	noContent(c)
}

// ===== 后台任务 =====

// ListTasks 处理 GET /api/v1/admin/tasks：支持 type/status 过滤。
func (h *Handler) ListTasks(c *gin.Context) {
	limit, cursor, ok := h.pageParams(c)
	if !ok {
		return
	}
	page, err := h.service.ListTasks(c.Request.Context(), app.TasksQuery{
		JobType: strings.TrimSpace(c.Query("type")),
		Status:  strings.TrimSpace(c.Query("status")),
		Cursor:  cursor,
		Limit:   limit,
	})
	if err != nil {
		h.fail(c, "查询后台作业失败", err)
		return
	}
	c.JSON(http.StatusOK, listResponse(page, func(item app.TaskItem) any { return jobViewFromTask(item) }))
}

// RetryTask 处理 POST /api/v1/admin/tasks/:id/retry。
func (h *Handler) RetryTask(c *gin.Context) { h.changeTaskStatus(c, "retry") }

// CancelTask 处理 POST /api/v1/admin/tasks/:id/cancel。
func (h *Handler) CancelTask(c *gin.Context) { h.changeTaskStatus(c, "cancel") }

func (h *Handler) changeTaskStatus(c *gin.Context, action string) {
	id, ok := h.pathID(c, "id")
	if !ok {
		return
	}
	actor, ok := h.actor(c)
	if !ok {
		return
	}
	var (
		snapshot app.JobSnapshot
		err      error
	)
	if action == "retry" {
		snapshot, err = h.service.RetryTask(c.Request.Context(), actor, id)
	} else {
		snapshot, err = h.service.CancelTask(c.Request.Context(), actor, id)
	}
	if err != nil {
		h.fail(c, "处置后台作业失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"job": jobViewFromSnapshot(snapshot)})
}

// ===== 审计日志 =====

// ListAuditLogs 处理 GET /api/v1/admin/audit-logs：支持 action_type/admin 过滤。
func (h *Handler) ListAuditLogs(c *gin.Context) {
	limit, cursor, ok := h.pageParams(c)
	if !ok {
		return
	}
	page, err := h.service.ListAuditLogs(c.Request.Context(), app.AuditQuery{
		ActionType: strings.TrimSpace(c.Query("action_type")),
		Admin:      strings.TrimSpace(c.Query("admin")),
		Cursor:     cursor,
		Limit:      limit,
	})
	if err != nil {
		h.fail(c, "查询管理审计日志失败", err)
		return
	}
	c.JSON(http.StatusOK, listResponse(page, func(item app.AuditItem) any { return auditLogView(item) }))
}

// ===== 系统设置 =====

// settingsPatchRequest 是管理后台提交的设置项；全部使用指针以支持部分更新。
type settingsPatchRequest struct {
	RegistrationEnabled *bool `json:"registration_enabled"`
	AIGlobalEnabled     *bool `json:"ai_global_enabled"`

	MaxUploadBytes               *int64 `json:"max_upload_bytes"`
	MaxRequestBytes              *int64 `json:"max_request_bytes"`
	StorageQuotaBytes            *int64 `json:"storage_quota_bytes"`
	PostRateLimit                *int   `json:"post_rate_limit"`
	PostRateWindowSeconds        *int   `json:"post_rate_window_seconds"`
	UploadRateLimit              *int   `json:"upload_rate_limit"`
	UploadRateWindowSeconds      *int   `json:"upload_rate_window_seconds"`
	TempAttachmentRetentionHours *int   `json:"temp_attachment_retention_hours"`
	CallLogMaxBytes              *int   `json:"call_log_max_bytes"`
	CallLogRetentionDays         *int   `json:"call_log_retention_days"`
}

func (r settingsPatchRequest) toApp() app.SettingsPatch {
	return app.SettingsPatch{
		RegistrationEnabled:          r.RegistrationEnabled,
		AIGlobalEnabled:              r.AIGlobalEnabled,
		MaxUploadBytes:               r.MaxUploadBytes,
		MaxRequestBytes:              r.MaxRequestBytes,
		StorageQuotaBytes:            r.StorageQuotaBytes,
		PostRateLimit:                r.PostRateLimit,
		PostRateWindowSeconds:        r.PostRateWindowSeconds,
		UploadRateLimit:              r.UploadRateLimit,
		UploadRateWindowSeconds:      r.UploadRateWindowSeconds,
		TempAttachmentRetentionHours: r.TempAttachmentRetentionHours,
		CallLogMaxBytes:              r.CallLogMaxBytes,
		CallLogRetentionDays:         r.CallLogRetentionDays,
	}
}

// GetSettings 处理 GET /api/v1/admin/settings。
func (h *Handler) GetSettings(c *gin.Context) {
	values, err := h.service.GetSettings(c.Request.Context())
	if err != nil {
		h.fail(c, "读取系统设置失败", err)
		return
	}
	c.JSON(http.StatusOK, settingsView(values))
}

// UpdateSettings 处理 POST /api/v1/admin/settings。
func (h *Handler) UpdateSettings(c *gin.Context) {
	actor, ok := h.actor(c)
	if !ok {
		return
	}
	var in settingsPatchRequest
	if !h.decodeJSON(c, &in) {
		return
	}
	values, err := h.service.UpdateSettings(c.Request.Context(), actor, in.toApp())
	if err != nil {
		h.fail(c, "更新系统设置失败", err)
		return
	}
	c.JSON(http.StatusOK, settingsView(values))
}

// webSearchPatchRequest 是全局网页搜索配置的部分更新请求。
type webSearchPatchRequest struct {
	BaseURL     *string `json:"base_url"`
	APIKey      *string `json:"api_key"`
	ClearAPIKey *bool   `json:"clear_api_key"`
}

// GetWebSearchSettings 处理 GET /api/v1/admin/settings/web-search。
func (h *Handler) GetWebSearchSettings(c *gin.Context) {
	snapshot, err := h.service.GetWebSearchSettings(c.Request.Context())
	if err != nil {
		h.fail(c, "读取网页搜索设置失败", err)
		return
	}
	c.JSON(http.StatusOK, webSearchView(snapshot))
}

// UpdateWebSearchSettings 处理 POST /api/v1/admin/settings/web-search。
func (h *Handler) UpdateWebSearchSettings(c *gin.Context) {
	actor, ok := h.actor(c)
	if !ok {
		return
	}
	var in webSearchPatchRequest
	if !h.decodeJSON(c, &in) {
		return
	}
	snapshot, err := h.service.UpdateWebSearchSettings(c.Request.Context(), actor, app.WebSearchPatch{
		BaseURL:     in.BaseURL,
		APIKey:      in.APIKey,
		ClearAPIKey: in.ClearAPIKey,
	})
	if err != nil {
		h.fail(c, "更新网页搜索设置失败", err)
		return
	}
	c.JSON(http.StatusOK, webSearchView(snapshot))
}
