// Package http 是 agent 模块的 Gin 适配层：只做参数解析、调用 app 用例、
// 错误映射与 DTO 组装；不写 SQL、不出现 pgx/db，也不编排事务。
package http

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/modules/agent/app"
	"github.com/great-magician-01/timeline/internal/modules/agent/repo"
	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// Handler 实现 transport/router.AgentHandlers 的 12 个方法。
type Handler struct {
	svc *app.Service
	log *slog.Logger
}

// New 构造 agent HTTP handler。
func New(svc *app.Service, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{svc: svc, log: log}
}

// GlobalStart 打开全局 AI 开关。
func (h *Handler) GlobalStart(c *gin.Context) { h.setGlobal(c, true) }

// GlobalStop 关闭全局 AI 开关并暂停待执行任务。
func (h *Handler) GlobalStop(c *gin.Context) { h.setGlobal(c, false) }

func (h *Handler) setGlobal(c *gin.Context, enabled bool) {
	viewer, ok := httpx.RequireViewer(c)
	if !ok {
		return
	}
	if err := h.svc.SetGlobal(c.Request.Context(), viewer.ID, enabled); err != nil {
		h.fail(c, "设置全局 AI 开关失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ai_global_enabled": enabled})
}

// generateInput 是生成接口请求体。
type generateInput struct {
	ScheduledAt *string `json:"scheduled_at,omitempty"`
	Instruction string  `json:"instruction,omitempty"`
}

// GenerateDraft 生成 AI 草稿任务。
func (h *Handler) GenerateDraft(c *gin.Context) { h.generate(c, "draft") }

// PublishDirect 生成并立即发布任务。
func (h *Handler) PublishDirect(c *gin.Context) { h.generate(c, "direct") }

func (h *Handler) generate(c *gin.Context, mode string) {
	viewer, ok := httpx.RequireViewer(c)
	if !ok {
		return
	}
	aiID, ok := h.pathID(c, "id")
	if !ok {
		return
	}
	var in generateInput
	if c.Request.ContentLength > 0 {
		if err := httpx.DecodeJSON(c, &in); err != nil {
			httpx.Error(c, http.StatusBadRequest, "invalid_request", "请求体必须是有效的 JSON 对象")
			return
		}
	}
	generation := app.GenerationInput{Mode: mode, Instruction: in.Instruction}
	if in.ScheduledAt != nil && strings.TrimSpace(*in.ScheduledAt) != "" {
		at, err := time.Parse(time.RFC3339, strings.TrimSpace(*in.ScheduledAt))
		if err != nil {
			httpx.Error(c, http.StatusBadRequest, "validation_failed", "定时发布时间格式无效")
			return
		}
		generation.ScheduledAt = &at
	}
	var (
		view app.TaskView
		err  error
	)
	if mode == "direct" {
		view, err = h.svc.PublishDirect(c.Request.Context(), viewer.ID, aiID, generation)
	} else {
		view, err = h.svc.GenerateDraft(c.Request.Context(), viewer.ID, aiID, generation)
	}
	if err != nil {
		h.fail(c, "创建 AI 生成任务失败", err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"task": taskResponse(view)})
}

// ListAIDrafts 列出 AI 草稿。
func (h *Handler) ListAIDrafts(c *gin.Context) {
	aiID, ok := h.pathID(c, "id")
	if !ok {
		return
	}
	limit, err := httpx.PageLimit(c)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	cursor, err := httpx.PageCursor(c)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	views, hasMore, err := h.svc.ListAIDrafts(c.Request.Context(), aiID, limit, cursor)
	if err != nil {
		h.fail(c, "读取 AI 草稿列表失败", err)
		return
	}
	items := make([]gin.H, 0, len(views))
	for _, view := range views {
		items = append(items, gin.H{
			"id":             httpx.FormatID(view.Post.ID),
			"author_id":      httpx.FormatID(view.Post.AuthorID),
			"status":         view.Post.Status,
			"visibility":     view.Post.Visibility,
			"content_html":   view.ContentHTML,
			"updated_at":     httpx.FormatTime(view.Post.UpdatedAt),
			"publish_status": view.PublishStatus,
			"publish_error":  view.PublishError,
			"scheduled_at":   formatOptionalTime(view.PendingRunAt),
		})
	}
	var lastID int64
	if len(views) > 0 {
		lastID = views[len(views)-1].Post.ID
	}
	c.JSON(http.StatusOK, httpx.ListResponse(items, lastID, hasMore))
}

// UpdateAIDraft 管理员编辑 AI 草稿。
func (h *Handler) UpdateAIDraft(c *gin.Context) {
	viewer, ok := httpx.RequireViewer(c)
	if !ok {
		return
	}
	aiID, ok := h.pathID(c, "id")
	if !ok {
		return
	}
	postID, ok := h.pathID(c, "post_id")
	if !ok {
		return
	}
	var in struct {
		ContentFormat string `json:"content_format"`
		ContentSource string `json:"content_source"`
	}
	if err := httpx.DecodeJSON(c, &in); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "请求体必须是有效的 JSON 对象")
		return
	}
	if err := h.svc.UpdateAIDraft(c.Request.Context(), viewer.ID, aiID, postID,
		strings.TrimSpace(in.ContentFormat), in.ContentSource); err != nil {
		h.fail(c, "编辑 AI 草稿失败", err)
		return
	}
	c.Status(http.StatusNoContent)
}

// PublishAIDraft 管理员发布 AI 草稿。
func (h *Handler) PublishAIDraft(c *gin.Context) {
	viewer, ok := httpx.RequireViewer(c)
	if !ok {
		return
	}
	aiID, ok := h.pathID(c, "id")
	if !ok {
		return
	}
	postID, ok := h.pathID(c, "post_id")
	if !ok {
		return
	}
	if err := h.svc.PublishAIDraft(c.Request.Context(), viewer.ID, aiID, postID); err != nil {
		h.fail(c, "发布 AI 草稿失败", err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ListAITasks 列出 AI 任务。
func (h *Handler) ListAITasks(c *gin.Context) {
	aiID, ok := h.pathID(c, "id")
	if !ok {
		return
	}
	limit, err := httpx.PageLimit(c)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	cursor, err := httpx.PageCursor(c)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	views, hasMore, err := h.svc.ListAITasks(c.Request.Context(), repo.TaskFilter{
		AIID:        aiID,
		TriggerType: strings.TrimSpace(c.Query("trigger_type")),
		ActionType:  strings.TrimSpace(c.Query("action_type")),
		Status:      strings.TrimSpace(c.Query("status")),
		Cursor:      cursor,
		Limit:       limit,
	})
	if err != nil {
		h.fail(c, "读取 AI 任务列表失败", err)
		return
	}
	items := make([]gin.H, 0, len(views))
	for _, view := range views {
		items = append(items, taskResponse(view))
	}
	var lastID int64
	if len(views) > 0 {
		lastID = views[len(views)-1].Task.ID
	}
	c.JSON(http.StatusOK, httpx.ListResponse(items, lastID, hasMore))
}

// RetryAITask 重试失败任务。
func (h *Handler) RetryAITask(c *gin.Context) {
	viewer, ok := httpx.RequireViewer(c)
	if !ok {
		return
	}
	taskID, ok := h.pathID(c, "id")
	if !ok {
		return
	}
	task, err := h.svc.RetryAITask(c.Request.Context(), viewer.ID, taskID)
	if err != nil {
		h.fail(c, "重试 AI 任务失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"task": taskResponse(app.TaskView{Task: task})})
}

// CancelAITask 取消任务。
func (h *Handler) CancelAITask(c *gin.Context) {
	viewer, ok := httpx.RequireViewer(c)
	if !ok {
		return
	}
	taskID, ok := h.pathID(c, "id")
	if !ok {
		return
	}
	task, err := h.svc.CancelAITask(c.Request.Context(), viewer.ID, taskID)
	if err != nil {
		h.fail(c, "取消 AI 任务失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"task": taskResponse(app.TaskView{Task: task})})
}

// ListCallLogs 列出模型调用记录。
func (h *Handler) ListCallLogs(c *gin.Context) {
	limit, err := httpx.PageLimit(c)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	cursor, err := httpx.PageCursor(c)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	filter := repo.CallLogFilter{
		Status:    strings.TrimSpace(c.Query("status")),
		ErrorType: strings.TrimSpace(c.Query("error_type")),
		Cursor:    cursor,
		Limit:     limit,
	}
	if raw := strings.TrimSpace(c.Query("ai_id")); raw != "" {
		filter.AIID, err = httpx.ParseID(raw)
		if err != nil {
			httpx.Error(c, http.StatusBadRequest, "invalid_request", "ai_id 无效")
			return
		}
	}
	if raw := strings.TrimSpace(c.Query("provider_id")); raw != "" {
		filter.ProviderID, err = httpx.ParseID(raw)
		if err != nil {
			httpx.Error(c, http.StatusBadRequest, "invalid_request", "provider_id 无效")
			return
		}
	}
	if raw := strings.TrimSpace(c.Query("task_id")); raw != "" {
		filter.TaskID, err = httpx.ParseID(raw)
		if err != nil {
			httpx.Error(c, http.StatusBadRequest, "invalid_request", "task_id 无效")
			return
		}
	}
	from, to, err := parseTimeRange(strings.TrimSpace(c.Query("from")), strings.TrimSpace(c.Query("to")))
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "from/to 必须是 RFC3339 时间")
		return
	}
	filter.From, filter.To = from, to
	logs, hasMore, err := h.svc.ListCallLogs(c.Request.Context(), filter)
	if err != nil {
		h.fail(c, "读取调用记录失败", err)
		return
	}
	items := make([]gin.H, 0, len(logs))
	for _, entry := range logs {
		items = append(items, callLogResponse(entry))
	}
	var lastID int64
	if len(logs) > 0 {
		lastID = logs[len(logs)-1].ID
	}
	c.JSON(http.StatusOK, httpx.ListResponse(items, lastID, hasMore))
}

// GetCallLog 读取单条调用记录。
func (h *Handler) GetCallLog(c *gin.Context) {
	logID, ok := h.pathID(c, "id")
	if !ok {
		return
	}
	entry, err := h.svc.GetCallLog(c.Request.Context(), logID)
	if err != nil {
		h.fail(c, "读取调用记录失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"call_log": callLogResponse(entry)})
}

// ===== 工具 =====

func (h *Handler) pathID(c *gin.Context, name string) (int64, bool) {
	id, err := httpx.ParseID(c.Param(name))
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "资源 ID 无效")
		return 0, false
	}
	return id, true
}

func (h *Handler) fail(c *gin.Context, message string, err error) {
	if httpx.Respond(c, err) {
		return
	}
	h.log.Error(message, "err", err, "path", c.Request.URL.Path)
	httpx.Error(c, http.StatusInternalServerError, "internal_error", "服务暂时异常，请稍后重试")
}

func taskResponse(view app.TaskView) gin.H {
	task := view.Task
	refs := make([]gin.H, 0, len(view.MemoryRefs))
	for _, ref := range view.MemoryRefs {
		refs = append(refs, gin.H{"memory_id": httpx.FormatID(ref.MemoryID), "memory_type": ref.MemoryType})
	}
	return gin.H{
		"id":               httpx.FormatID(task.ID),
		"ai_id":            httpx.FormatID(task.AIID),
		"trigger_type":     task.TriggerType,
		"trigger_ref_type": task.TriggerRefType,
		"trigger_ref_id":   formatOptionalID(task.TriggerRefID),
		"action_type":      task.ActionType,
		"status":           task.Status,
		"decision_summary": task.DecisionSummary,
		"target_type":      task.TargetType,
		"target_id":        formatOptionalID(task.TargetID),
		"result_ref_type":  task.ResultRefType,
		"result_ref_id":    formatOptionalID(task.ResultRefID),
		"provider_id":      formatOptionalID(task.ProviderID),
		"model":            task.Model,
		"error":            task.Error,
		"attempts":         task.Attempts,
		"scheduled_at":     formatOptionalTime(task.ScheduledAt),
		"started_at":       formatOptionalTime(task.StartedAt),
		"finished_at":      formatOptionalTime(task.FinishedAt),
		"created_at":       httpx.FormatTime(task.CreatedAt),
		"memory_refs":      refs,
	}
}

func callLogResponse(entry repo.CallLog) gin.H {
	return gin.H{
		"id":                     httpx.FormatID(entry.ID),
		"task_id":                formatOptionalID(entry.TaskID),
		"ai_id":                  formatOptionalID(entry.AIID),
		"provider_id":            formatOptionalID(entry.ProviderID),
		"model":                  entry.Model,
		"params":                 string(entry.Params),
		"request_content":        entry.RequestContent,
		"response_content":       entry.ResponseContent,
		"status":                 entry.Status,
		"http_status":            entry.HTTPStatus,
		"error_type":             entry.ErrorType,
		"error_message":          entry.ErrorMessage,
		"usage_input_tokens":     entry.UsageInputTokens,
		"usage_output_tokens":    entry.UsageOutputTokens,
		"usage_reasoning_tokens": entry.UsageReasoningTokens,
		"attempt":                entry.Attempt,
		"latency_ms":             entry.LatencyMS,
		"truncated":              entry.Truncated,
		"started_at":             httpx.FormatTime(entry.StartedAt),
		"finished_at":            httpx.FormatTime(entry.FinishedAt),
		"created_at":             httpx.FormatTime(entry.CreatedAt),
	}
}

func formatOptionalID(value *int64) *string {
	if value == nil {
		return nil
	}
	formatted := httpx.FormatID(*value)
	return &formatted
}

func formatOptionalTime(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := httpx.FormatTime(*value)
	return &formatted
}

func parseTimeRange(from, to string) (*time.Time, *time.Time, error) {
	var (
		fromAt *time.Time
		toAt   *time.Time
	)
	if from != "" {
		parsed, err := time.Parse(time.RFC3339, from)
		if err != nil {
			return nil, nil, fmt.Errorf("from 时间格式无效")
		}
		fromAt = &parsed
	}
	if to != "" {
		parsed, err := time.Parse(time.RFC3339, to)
		if err != nil {
			return nil, nil, fmt.Errorf("to 时间格式无效")
		}
		toAt = &parsed
	}
	return fromAt, toAt, nil
}
