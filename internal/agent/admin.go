package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/great-magician-01/timeline/internal/access"
	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/audit"
	"github.com/great-magician-01/timeline/internal/auth"
	"github.com/great-magician-01/timeline/internal/content"
)

// 管理接口目标类型与默认值。
const (
	targetAITask       = "ai_task"
	targetSystemConfig = "system_settings"
	defaultDraftFormat = "markdown"
)

// ── AI 生成与草稿 ──

// GenerateDraft 处理 POST /api/v1/admin/ai/:id/generate：
// 入队一条 manual + original_post 任务，生成的内容落在 AI 草稿（仅管理员可见）。
func (s *Service) GenerateDraft(c *gin.Context) {
	s.createGenerationTask(c, manualRefDraft)
}

// PublishDirect 处理 POST /api/v1/admin/ai/:id/publish-direct：
// 入队一条 manual + original_post 任务，执行成功后直接发布为公开内容。
func (s *Service) PublishDirect(c *gin.Context) {
	s.createGenerationTask(c, manualRefDirect)
}

func (s *Service) createGenerationTask(c *gin.Context, mode string) {
	if _, ok := s.pool(c); !ok {
		return
	}
	aiID, ok := parsePathID(c, "id", "无效的 AI ID")
	if !ok {
		return
	}
	var in struct {
		Mode        string  `json:"mode"`
		ScheduledAt *string `json:"scheduled_at"`
	}
	if err := api.DecodeJSON(c, &in); err != nil && !errors.Is(err, io.EOF) {
		api.Error(c, http.StatusBadRequest, "invalid_request", "请求体必须是有效的 JSON 对象，且不能包含未知字段")
		return
	}
	if requested := strings.TrimSpace(in.Mode); requested != "" && requested != mode {
		api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", "mode 与接口不匹配",
			gin.H{"mode": "mode 只能是 draft 或 direct"})
		return
	}
	ctx := c.Request.Context()
	adminUser, _ := auth.CurrentUser(c)
	if !s.consumeGenerationQuota(c, adminUser.ID, mode) {
		return
	}
	var scheduledAt *time.Time
	if in.ScheduledAt != nil && strings.TrimSpace(*in.ScheduledAt) != "" {
		at, err := parseFutureScheduledAt(*in.ScheduledAt, time.Now())
		if err != nil {
			api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", err.Error(),
				gin.H{"scheduled_at": err.Error()})
			return
		}
		scheduledAt = &at
	}

	tx, ok := s.beginTx(c, "开启 AI 生成任务事务失败")
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := ensureAIUser(ctx, tx, aiID); err != nil {
		if !api.Respond(c, err) {
			s.internalError(c, "校验 AI 用户失败", err)
		}
		return
	}
	key := taskKey("manual", mode, aiID)
	taskID, reason, err := s.Enqueue(ctx, tx, EnqueueInput{
		AID:            aiID,
		TriggerType:    triggerManual,
		TriggerRefType: mode,
		TriggerRefID:   &aiID,
		ActionType:     actionOriginalPost,
		IdempotencyKey: key,
		ScheduledAt:    scheduledAt,
	})
	if err != nil {
		s.internalError(c, "创建 AI 生成任务失败", err)
		return
	}
	if taskID == 0 {
		api.ErrorWithDetails(c, http.StatusConflict, "invalid_state", "当前不能创建生成任务",
			gin.H{"reason": reason})
		return
	}
	auditDetail := gin.H{
		"mode":    mode,
		"task_id": api.FormatID(taskID),
	}
	if scheduledAt != nil {
		auditDetail["scheduled_at"] = api.FormatTime(*scheduledAt)
	}
	if err := s.writeAudit(ctx, tx, adminUser.ID, audit.ActionAIGenerate, targetAI, &aiID, auditDetail); err != nil {
		s.internalError(c, "写入 AI 生成审计失败", err)
		return
	}
	if !s.commitTx(c, tx, "提交 AI 生成任务事务失败") {
		return
	}
	response := gin.H{
		"task_id": api.FormatID(taskID),
		"status":  statusPending,
	}
	if scheduledAt != nil {
		response["scheduled_at"] = api.FormatTime(*scheduledAt)
	}
	c.JSON(http.StatusAccepted, response)
}

// parseFutureScheduledAt 解析管理端提交的定时发布时间：必须是带时区的 RFC3339 且位于未来。
func parseFutureScheduledAt(raw string, now time.Time) (time.Time, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return time.Time{}, errors.New("定时发布时间不能为空")
	}
	at, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return time.Time{}, errors.New("定时发布时间必须是 RFC3339 格式")
	}
	if !at.After(now.Add(time.Minute)) {
		return time.Time{}, errors.New("定时发布时间必须晚于当前时间至少 1 分钟")
	}
	return at, nil
}

// ListAIDrafts 处理 GET /api/v1/admin/ai/:id/drafts：AI 草稿列表（发布前仅管理员可见）。
func (s *Service) ListAIDrafts(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	aiID, ok := parsePathID(c, "id", "无效的 AI ID")
	if !ok {
		return
	}
	limit, cursor, ok := s.listParams(c)
	if !ok {
		return
	}
	if err := ensureAIUser(c.Request.Context(), pool, aiID); err != nil {
		if !api.Respond(c, err) {
			s.internalError(c, "校验 AI 用户失败", err)
		}
		return
	}

	args := []any{aiID}
	sql := `SELECT p.id, p.visibility, p.updated_at, COALESCE(d.content_html, ''),
			COALESCE(d.plain_text, ''), COALESCE(d.draft_revision, 0)
		FROM posts p
		LEFT JOIN post_drafts d ON d.post_id = p.id
		WHERE p.author_id = $1 AND p.status = 'draft'`
	if cursor > 0 {
		args = append(args, cursor)
		sql += " AND p.id < $" + strconv.Itoa(len(args))
	}
	args = append(args, limit+1)
	sql += " ORDER BY p.id DESC LIMIT $" + strconv.Itoa(len(args))

	rows, err := pool.Query(c.Request.Context(), sql, args...)
	if err != nil {
		s.internalError(c, "查询 AI 草稿失败", err)
		return
	}
	defer rows.Close()

	type draftItem struct {
		ID          string `json:"id"`
		Visibility  string `json:"visibility"`
		ContentHTML string `json:"content_html"`
		PlainText   string `json:"plain_text"`
		Revision    int    `json:"revision"`
		UpdatedAt   string `json:"updated_at"`
	}
	items := make([]draftItem, 0, limit+1)
	var lastID int64
	for rows.Next() {
		var (
			id          int64
			visibility  string
			updatedAt   time.Time
			contentHTML string
			plainText   string
			revision    int
		)
		if err := rows.Scan(&id, &visibility, &updatedAt, &contentHTML, &plainText, &revision); err != nil {
			s.internalError(c, "读取 AI 草稿失败", err)
			return
		}
		items = append(items, draftItem{
			ID:          api.FormatID(id),
			Visibility:  visibility,
			ContentHTML: contentHTML,
			PlainText:   truncateRunes(plainText, 120),
			Revision:    revision,
			UpdatedAt:   api.FormatTime(updatedAt),
		})
		lastID = id
	}
	if err := rows.Err(); err != nil {
		s.internalError(c, "遍历 AI 草稿失败", err)
		return
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	c.JSON(http.StatusOK, api.ListResponse(items, lastID, hasMore))
}

// UpdateAIDraft 处理 POST /api/v1/admin/ai/:id/drafts/:post_id：编辑 AI 草稿内容。
func (s *Service) UpdateAIDraft(c *gin.Context) {
	if _, ok := s.pool(c); !ok {
		return
	}
	aiID, ok := parsePathID(c, "id", "无效的 AI ID")
	if !ok {
		return
	}
	postID, ok := parsePathID(c, "post_id", "无效的草稿 ID")
	if !ok {
		return
	}
	var in struct {
		ContentSource string `json:"content_source"`
		ContentFormat string `json:"content_format"`
	}
	if err := api.DecodeJSON(c, &in); err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", "请求体必须是有效的 JSON 对象，且不能包含未知字段")
		return
	}
	format := strings.TrimSpace(in.ContentFormat)
	if format == "" {
		format = defaultDraftFormat
	}
	if strings.TrimSpace(in.ContentSource) == "" {
		api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", "正文不能为空",
			gin.H{"content_source": "正文不能为空"})
		return
	}
	ctx := c.Request.Context()
	adminUser, _ := auth.CurrentUser(c)

	tx, ok := s.beginTx(c, "开启 AI 草稿编辑事务失败")
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	status, err := s.draftPostStatus(ctx, tx, aiID, postID)
	if err != nil {
		if !api.Respond(c, err) {
			s.internalError(c, "读取 AI 草稿失败", err)
		}
		return
	}
	if status != "draft" {
		api.Error(c, http.StatusConflict, "invalid_state", "只有草稿状态的内容可以在此编辑")
		return
	}
	if err := s.content.UpdateDraftForActor(ctx, tx, postID, format, in.ContentSource); err != nil {
		if !api.Respond(c, err) {
			s.internalError(c, "更新 AI 草稿失败", err)
		}
		return
	}
	if err := s.writeAudit(ctx, tx, adminUser.ID, audit.ActionAIGenerate, targetPost, &postID, gin.H{
		"ai_id": api.FormatID(aiID),
		"mode":  "draft_update",
	}); err != nil {
		s.internalError(c, "写入 AI 草稿编辑审计失败", err)
		return
	}
	if !s.commitTx(c, tx, "提交 AI 草稿编辑事务失败") {
		return
	}
	c.JSON(http.StatusOK, gin.H{"post_id": api.FormatID(postID), "status": "draft"})
}

// PublishAIDraft 处理 POST /api/v1/admin/ai/:id/drafts/:post_id/publish：发布 AI 草稿。
func (s *Service) PublishAIDraft(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	aiID, ok := parsePathID(c, "id", "无效的 AI ID")
	if !ok {
		return
	}
	postID, ok := parsePathID(c, "post_id", "无效的草稿 ID")
	if !ok {
		return
	}
	ctx := c.Request.Context()
	adminUser, _ := auth.CurrentUser(c)

	tx, ok := s.beginTx(c, "开启 AI 草稿发布事务失败")
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	status, err := s.draftPostStatus(ctx, tx, aiID, postID)
	if err != nil {
		if !api.Respond(c, err) {
			s.internalError(c, "读取 AI 草稿失败", err)
		}
		return
	}
	if status != "draft" {
		api.Error(c, http.StatusConflict, "invalid_state", "只有草稿状态的内容可以发布")
		return
	}
	if err := s.content.PublishPostForActor(ctx, tx, postID, aiID); err != nil {
		if !api.Respond(c, err) {
			s.internalError(c, "发布 AI 草稿失败", err)
		}
		return
	}
	if err := s.writeAudit(ctx, tx, adminUser.ID, audit.ActionAIPublish, targetPost, &postID, gin.H{
		"ai_id": api.FormatID(aiID),
	}); err != nil {
		s.internalError(c, "写入 AI 草稿发布审计失败", err)
		return
	}
	if !s.commitTx(c, tx, "提交 AI 草稿发布事务失败") {
		return
	}
	post, err := content.PostByID(ctx, pool, postID)
	if err != nil {
		s.internalError(c, "读取已发布 AI 内容失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"post_id":    api.FormatID(post.ID),
		"status":     post.Status,
		"visibility": post.Visibility,
	})
}

// draftPostStatus 读取帖子状态并确认它属于该 AI；不存在或不属于该 AI 时返回 404。
func (s *Service) draftPostStatus(ctx context.Context, q access.Querier, aiID, postID int64) (string, error) {
	var (
		authorID int64
		status   string
	)
	err := q.QueryRow(ctx, `SELECT author_id, status FROM posts WHERE id = $1`, postID).
		Scan(&authorID, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", api.ErrNotFound("内容不存在或无权访问")
	}
	if err != nil {
		return "", err
	}
	if authorID != aiID {
		return "", api.ErrNotFound("内容不存在或无权访问")
	}
	return status, nil
}

// ── 任务 ──

// ListAITasks 处理 GET /api/v1/admin/ai/:id/tasks：单个 AI 的任务列表。
func (s *Service) ListAITasks(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	aiID, ok := parsePathID(c, "id", "无效的 AI ID")
	if !ok {
		return
	}
	limit, cursor, ok := s.listParams(c)
	if !ok {
		return
	}
	if err := ensureAIUser(c.Request.Context(), pool, aiID); err != nil {
		if !api.Respond(c, err) {
			s.internalError(c, "校验 AI 用户失败", err)
		}
		return
	}

	where := []string{"ai_id = $1"}
	args := []any{aiID}
	addFilter := func(column, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		args = append(args, strings.TrimSpace(value))
		where = append(where, column+" = $"+strconv.Itoa(len(args)))
	}
	addFilter("trigger_type", c.Query("trigger_type"))
	addFilter("action_type", c.Query("action_type"))
	addFilter("status", c.Query("status"))
	if cursor > 0 {
		args = append(args, cursor)
		where = append(where, "id < $"+strconv.Itoa(len(args)))
	}
	args = append(args, limit+1)

	rows, err := pool.Query(c.Request.Context(), `
		SELECT id, ai_id, trigger_type, COALESCE(trigger_ref_type, ''), trigger_ref_id,
		       action_type, status, decision_summary, COALESCE(target_type, ''), target_id,
		       COALESCE(result_ref_type, ''), result_ref_id, provider_id, model, error,
		       attempts, started_at, finished_at, created_at, scheduled_at
		FROM ai_tasks
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY id DESC LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		s.internalError(c, "查询 AI 任务失败", err)
		return
	}
	defer rows.Close()

	items := make([]taskResponse, 0, limit+1)
	taskIDs := make([]int64, 0, limit+1)
	var lastID int64
	for rows.Next() {
		item, err := scanTaskResponse(rows)
		if err != nil {
			s.internalError(c, "读取 AI 任务失败", err)
			return
		}
		lastID = item.taskID
		items = append(items, item.response)
		taskIDs = append(taskIDs, item.taskID)
	}
	if err := rows.Err(); err != nil {
		s.internalError(c, "遍历 AI 任务失败", err)
		return
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
		taskIDs = taskIDs[:limit]
	}
	// 任务列表附带本次决策注入的记忆（ai_task_memory_refs），便于后台追溯。
	memories, err := s.taskMemories(c.Request.Context(), pool, taskIDs)
	if err != nil {
		s.internalError(c, "读取任务注入记忆失败", err)
		return
	}
	for index := range items {
		if refs, ok := memories[taskIDs[index]]; ok {
			items[index].Memories = refs
		}
	}
	c.JSON(http.StatusOK, api.ListResponse(items, lastID, hasMore))
}

// taskMemories 批量读取任务注入的记忆，避免列表接口 N+1 查询。
func (s *Service) taskMemories(
	ctx context.Context,
	q access.Querier,
	taskIDs []int64,
) (map[int64][]taskMemoryResponse, error) {
	result := make(map[int64][]taskMemoryResponse, len(taskIDs))
	if len(taskIDs) == 0 {
		return result, nil
	}
	rows, err := q.Query(ctx, `
		SELECT r.task_id, m.id, m.memory_type, m.content, m.category
		FROM ai_task_memory_refs AS r
		JOIN ai_memories AS m ON m.id = r.memory_id
		WHERE r.task_id = ANY($1)
		ORDER BY r.task_id, m.id`, taskIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			taskID  int64
			memory  taskMemoryResponse
			id      int64
			rawType string
		)
		if err := rows.Scan(&taskID, &id, &rawType, &memory.Content, &memory.Category); err != nil {
			return nil, err
		}
		memory.ID = api.FormatID(id)
		memory.Type = rawType
		result[taskID] = append(result[taskID], memory)
	}
	return result, rows.Err()
}

// RetryAITask 处理 POST /api/v1/admin/ai-tasks/:id/retry：重试失败任务。
func (s *Service) RetryAITask(c *gin.Context) {
	if _, ok := s.pool(c); !ok {
		return
	}
	taskID, ok := parsePathID(c, "id", "无效的任务 ID")
	if !ok {
		return
	}
	ctx := c.Request.Context()
	adminUser, _ := auth.CurrentUser(c)

	tx, ok := s.beginTx(c, "开启任务重试事务失败")
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var current string
	if err := tx.QueryRow(ctx, `SELECT status FROM ai_tasks WHERE id = $1 FOR UPDATE`, taskID).
		Scan(&current); errors.Is(err, pgx.ErrNoRows) {
		api.Error(c, http.StatusNotFound, "not_found", "AI 任务不存在")
		return
	} else if err != nil {
		s.internalError(c, "读取待重试任务失败", err)
		return
	}
	if current != statusFailed && current != statusRejected {
		api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", "只有失败或已拒绝的任务可以重试",
			gin.H{"status": "当前状态为 " + current})
		return
	}
	if _, err := tx.Exec(ctx, `
		UPDATE ai_tasks
		SET status = 'pending', attempts = 0, error = '', started_at = NULL, finished_at = NULL
		WHERE id = $1`, taskID); err != nil {
		s.internalError(c, "重置任务状态失败", err)
		return
	}
	if err := s.reopenTaskJob(ctx, tx, taskID); err != nil {
		if !api.Respond(c, err) {
			s.internalError(c, "重新投递任务作业失败", err)
		}
		return
	}
	if err := s.writeAudit(ctx, tx, adminUser.ID, audit.ActionTaskRetry, targetAITask, &taskID, gin.H{
		"previous_status": current,
	}); err != nil {
		s.internalError(c, "写入任务重试审计失败", err)
		return
	}
	if !s.commitTx(c, tx, "提交任务重试事务失败") {
		return
	}
	c.JSON(http.StatusOK, gin.H{"task_id": api.FormatID(taskID), "status": statusPending})
}

// reopenTaskJob 重置或重新投递任务对应的后台作业。
func (s *Service) reopenTaskJob(ctx context.Context, q access.Querier, taskID int64) error {
	jobKey := aiTaskJobPrefix + api.FormatID(taskID)
	if _, err := q.Exec(ctx, `
		UPDATE background_jobs
		SET status = 'pending', attempts = 0, last_error = '', run_at = now(), updated_at = now()
		WHERE idempotency_key = $1 AND status NOT IN ('pending', 'running')`, jobKey); err != nil {
		return err
	}
	var blocking bool
	if err := q.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM background_jobs
			WHERE idempotency_key = $1 AND status IN ('pending', 'running')
		)`, jobKey).Scan(&blocking); err != nil {
		return err
	}
	if blocking {
		return api.ErrConflict("invalid_state", "任务对应的作业正在执行中，请稍后再试")
	}
	// 没有可复用的作业行时重新投递（幂等键冲突会被忽略）。
	return s.enqueueAIJob(ctx, q, taskID)
}

// CancelAITask 处理 POST /api/v1/admin/ai-tasks/:id/cancel：取消待执行或失败待重试的任务。
// 正在执行（running）的任务不在此处取消：动作可能已进入事务，只能由全局/单个 AI 停止
// 或执行前的二次校验拦截（docs/WORKFLOWS.md 7.5.3）。
func (s *Service) CancelAITask(c *gin.Context) {
	if _, ok := s.pool(c); !ok {
		return
	}
	taskID, ok := parsePathID(c, "id", "无效的任务 ID")
	if !ok {
		return
	}
	ctx := c.Request.Context()
	adminUser, _ := auth.CurrentUser(c)

	tx, ok := s.beginTx(c, "开启任务取消事务失败")
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var current string
	if err := tx.QueryRow(ctx, `SELECT status FROM ai_tasks WHERE id = $1 FOR UPDATE`, taskID).
		Scan(&current); errors.Is(err, pgx.ErrNoRows) {
		api.Error(c, http.StatusNotFound, "not_found", "AI 任务不存在")
		return
	} else if err != nil {
		s.internalError(c, "读取待取消任务失败", err)
		return
	}
	if current != statusPending && current != statusFailed {
		api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", "只有待执行或失败的任务可以取消",
			gin.H{"status": "当前状态为 " + current})
		return
	}
	if _, err := tx.Exec(ctx, `
		UPDATE ai_tasks SET status = 'canceled', finished_at = now() WHERE id = $1`, taskID); err != nil {
		s.internalError(c, "取消任务失败", err)
		return
	}
	if err := s.writeAudit(ctx, tx, adminUser.ID, audit.ActionTaskCancel, targetAITask, &taskID, gin.H{
		"previous_status": current,
	}); err != nil {
		s.internalError(c, "写入任务取消失败", err)
		return
	}
	if !s.commitTx(c, tx, "提交任务取消事务失败") {
		return
	}
	c.JSON(http.StatusOK, gin.H{"task_id": api.FormatID(taskID), "status": statusCanceled})
}

// ── 调用记录 ──

// ListCallLogs 处理 GET /api/v1/admin/ai-calls：调用记录列表（绝不返回 API Key）。
func (s *Service) ListCallLogs(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	limit, cursor, ok := s.listParams(c)
	if !ok {
		return
	}
	where := []string{"NOT is_deleted"}
	args := make([]any, 0, 8)
	addFilter := func(column, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		args = append(args, strings.TrimSpace(value))
		where = append(where, column+" = $"+strconv.Itoa(len(args)))
	}
	// ai_id / provider_id / task_id 是 BIGINT 列：先校验再传参，非法输入返回 400 而不是 500。
	for _, filter := range []struct{ column, query string }{
		{"ai_id", "ai_id"},
		{"provider_id", "provider_id"},
		{"task_id", "task_id"},
	} {
		raw := strings.TrimSpace(c.Query(filter.query))
		if raw == "" {
			continue
		}
		id, err := api.ParseID(raw)
		if err != nil {
			api.Error(c, http.StatusBadRequest, "invalid_request", filter.query+" 必须是有效的 ID")
			return
		}
		args = append(args, id)
		where = append(where, filter.column+" = $"+strconv.Itoa(len(args)))
	}
	addFilter("status", c.Query("status"))
	addFilter("error_type", c.Query("error_type"))
	if from := strings.TrimSpace(c.Query("from")); from != "" {
		at, err := time.Parse(time.RFC3339, from)
		if err != nil {
			api.Error(c, http.StatusBadRequest, "invalid_request", "from 必须是 RFC3339 时间")
			return
		}
		args = append(args, at)
		where = append(where, "started_at >= $"+strconv.Itoa(len(args)))
	}
	if to := strings.TrimSpace(c.Query("to")); to != "" {
		at, err := time.Parse(time.RFC3339, to)
		if err != nil {
			api.Error(c, http.StatusBadRequest, "invalid_request", "to 必须是 RFC3339 时间")
			return
		}
		args = append(args, at)
		where = append(where, "started_at <= $"+strconv.Itoa(len(args)))
	}
	if cursor > 0 {
		args = append(args, cursor)
		where = append(where, "id < $"+strconv.Itoa(len(args)))
	}
	args = append(args, limit+1)

	rows, err := pool.Query(c.Request.Context(), `
		SELECT id, task_id, ai_id, provider_id, model, status, http_status,
		       error_type, error_message, usage_input_tokens, usage_output_tokens,
		       attempt, latency_ms, truncated, started_at, finished_at
		FROM ai_call_logs
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY id DESC LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		s.internalError(c, "查询 AI 调用记录失败", err)
		return
	}
	defer rows.Close()

	items := make([]callLogResponse, 0, limit+1)
	var lastID int64
	for rows.Next() {
		var (
			item      callLogResponse
			id        int64
			taskID    *int64
			aiID      *int64
			provider  *int64
			httpCode  *int
			input     *int
			output    *int
			startedAt time.Time
			finished  time.Time
		)
		if err := rows.Scan(&id, &taskID, &aiID, &provider, &item.Model, &item.Status, &httpCode,
			&item.ErrorType, &item.ErrorMessage, &input, &output, &item.Attempt, &item.LatencyMS,
			&item.Truncated, &startedAt, &finished); err != nil {
			s.internalError(c, "读取 AI 调用记录失败", err)
			return
		}
		item.ID = api.FormatID(id)
		item.TaskID = formatOptionalID(taskID)
		item.AID = formatOptionalID(aiID)
		item.ProviderID = formatOptionalID(provider)
		item.HTTPStatus = httpCode
		item.UsageInputTokens = input
		item.UsageOutputTokens = output
		item.StartedAt = api.FormatTime(startedAt)
		item.FinishedAt = api.FormatTime(finished)
		items = append(items, item)
		lastID = id
	}
	if err := rows.Err(); err != nil {
		s.internalError(c, "遍历 AI 调用记录失败", err)
		return
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	c.JSON(http.StatusOK, api.ListResponse(items, lastID, hasMore))
}

// GetCallLog 处理 GET /api/v1/admin/ai-calls/:id：调用详情（绝不返回 API Key）。
func (s *Service) GetCallLog(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	callID, ok := parsePathID(c, "id", "无效的调用记录 ID")
	if !ok {
		return
	}
	var (
		item         callLogResponse
		id           int64
		taskID       *int64
		aiID         *int64
		provider     *int64
		params       []byte
		httpCode     *int
		input        *int
		output       *int
		reasoning    *int
		startedAt    time.Time
		finished     time.Time
		providerName string
	)
	err := pool.QueryRow(c.Request.Context(), `
		SELECT l.id, l.task_id, l.ai_id, l.provider_id, l.model, l.params,
		       l.request_content, l.response_content, l.status, l.http_status,
		       l.error_type, l.error_message, l.usage_input_tokens, l.usage_output_tokens,
		       l.usage_reasoning_tokens, l.attempt, l.latency_ms, l.truncated,
		       l.started_at, l.finished_at, COALESCE(p.name, '')
		FROM ai_call_logs l
		LEFT JOIN model_providers p ON p.id = l.provider_id
		WHERE l.id = $1 AND NOT l.is_deleted`, callID,
	).Scan(&id, &taskID, &aiID, &provider, &item.Model, &params,
		&item.RequestContent, &item.ResponseContent, &item.Status, &httpCode,
		&item.ErrorType, &item.ErrorMessage, &input, &output,
		&reasoning, &item.Attempt, &item.LatencyMS, &item.Truncated,
		&startedAt, &finished, &providerName)
	if errors.Is(err, pgx.ErrNoRows) {
		api.Error(c, http.StatusNotFound, "not_found", "调用记录不存在")
		return
	}
	if err != nil {
		s.internalError(c, "读取 AI 调用记录失败", err)
		return
	}
	item.ID = api.FormatID(id)
	item.TaskID = formatOptionalID(taskID)
	item.AID = formatOptionalID(aiID)
	item.ProviderID = formatOptionalID(provider)
	item.ProviderName = providerName
	item.Params = json.RawMessage(params)
	item.HTTPStatus = httpCode
	item.UsageInputTokens = input
	item.UsageOutputTokens = output
	item.UsageReasoningTokens = reasoning
	item.StartedAt = api.FormatTime(startedAt)
	item.FinishedAt = api.FormatTime(finished)
	c.JSON(http.StatusOK, gin.H{"call": item})
}

// ── 全局开关 ──

// GlobalStart 处理 POST /api/v1/admin/ai/global-start：解除全局 AI 停止。
func (s *Service) GlobalStart(c *gin.Context) { s.setGlobalAI(c, true) }

// GlobalStop 处理 POST /api/v1/admin/ai/global-stop：全局紧急停止。
// 待执行的 AI 任务与后台作业一并取消或暂停，运行中的任务会在实际动作前再次检查开关。
func (s *Service) GlobalStop(c *gin.Context) { s.setGlobalAI(c, false) }

func (s *Service) setGlobalAI(c *gin.Context, enabled bool) {
	ctx := c.Request.Context()
	adminUser, _ := auth.CurrentUser(c)

	tx, ok := s.beginTx(c, "开启全局 AI 开关事务失败")
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		INSERT INTO system_settings (key, value, updated_by, updated_at)
		VALUES ($1, to_jsonb($2::boolean), $3, now())
		ON CONFLICT (key) DO UPDATE
		SET value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = now()`,
		globalAIEnabledKey, enabled, adminUser.ID,
	); err != nil {
		s.internalError(c, "更新全局 AI 开关失败", err)
		return
	}
	if !enabled {
		if _, err := tx.Exec(ctx, `
			UPDATE ai_tasks SET status = 'canceled', finished_at = now()
			WHERE status = 'pending'`); err != nil {
			s.internalError(c, "取消待执行 AI 任务失败", err)
			return
		}
		// 全局停止要覆盖全部会调用模型或写入 AI 数据的前台/后台作业，
		// 包括记忆沉淀与调用记录清理之外的 AI 相关周期任务。
		if _, err := tx.Exec(ctx, `
			UPDATE background_jobs SET status = 'paused', updated_at = now()
			WHERE status = 'pending'
			  AND job_type IN ('ai_schedule', 'ai_task', 'memory_consolidation')`); err != nil {
			s.internalError(c, "暂停 AI 后台作业失败", err)
			return
		}
	}
	action := audit.ActionGlobalAIStart
	if !enabled {
		action = audit.ActionGlobalAIStop
	}
	if err := s.writeAudit(ctx, tx, adminUser.ID, action, targetSystemConfig, nil,
		gin.H{"ai_global_enabled": enabled}); err != nil {
		s.internalError(c, "写入全局 AI 开关审计失败", err)
		return
	}
	if !s.commitTx(c, tx, "提交全局 AI 开关事务失败") {
		return
	}
	c.JSON(http.StatusOK, gin.H{"ai_global_enabled": enabled})
}

// ── 响应组装与小工具 ──

// taskResponse 是任务的管理端 DTO。
type taskResponse struct {
	ID              string  `json:"id"`
	AID             string  `json:"ai_id"`
	TriggerType     string  `json:"trigger_type"`
	TriggerRefType  string  `json:"trigger_ref_type,omitempty"`
	TriggerRefID    *string `json:"trigger_ref_id,omitempty"`
	ActionType      string  `json:"action_type"`
	Status          string  `json:"status"`
	DecisionSummary string  `json:"decision_summary"`
	TargetType      string  `json:"target_type,omitempty"`
	TargetID        *string `json:"target_id,omitempty"`
	ResultRefType   string  `json:"result_ref_type,omitempty"`
	ResultRefID     *string `json:"result_ref_id,omitempty"`
	ProviderID      *string `json:"provider_id,omitempty"`
	Model           string  `json:"model"`
	Error           string  `json:"error"`
	Attempts        int     `json:"attempts"`
	StartedAt       *string `json:"started_at"`
	FinishedAt      *string `json:"finished_at"`
	CreatedAt       string  `json:"created_at"`
	ScheduledAt     *string `json:"scheduled_at,omitempty"`
	// Memories 是本次决策注入过的记忆（ai_task_memory_refs）。
	Memories []taskMemoryResponse `json:"memories"`
}

// taskMemoryResponse 是任务列表中的注入记忆摘要。
type taskMemoryResponse struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Content  string `json:"content"`
	Category string `json:"category"`
}

// taskResponseRow 附带原始任务 ID，便于生成游标。
type taskResponseRow struct {
	taskID   int64
	response taskResponse
}

func scanTaskResponse(row pgx.Row) (taskResponseRow, error) {
	var (
		item        taskResponse
		id          int64
		aiID        int64
		triggerRef  *int64
		targetID    *int64
		resultRefID *int64
		providerID  *int64
		startedAt   *time.Time
		finishedAt  *time.Time
		createdAt   time.Time
		scheduledAt *time.Time
	)
	if err := row.Scan(&id, &aiID, &item.TriggerType, &item.TriggerRefType, &triggerRef,
		&item.ActionType, &item.Status, &item.DecisionSummary, &item.TargetType, &targetID,
		&item.ResultRefType, &resultRefID, &providerID, &item.Model, &item.Error,
		&item.Attempts, &startedAt, &finishedAt, &createdAt, &scheduledAt); err != nil {
		return taskResponseRow{}, err
	}
	item.ID = api.FormatID(id)
	item.AID = api.FormatID(aiID)
	item.TriggerRefID = formatOptionalID(triggerRef)
	item.TargetID = formatOptionalID(targetID)
	item.ResultRefID = formatOptionalID(resultRefID)
	item.ProviderID = formatOptionalID(providerID)
	item.StartedAt = formatOptionalTime(startedAt)
	item.FinishedAt = formatOptionalTime(finishedAt)
	item.CreatedAt = api.FormatTime(createdAt)
	item.ScheduledAt = formatOptionalTime(scheduledAt)
	item.Memories = []taskMemoryResponse{}
	return taskResponseRow{taskID: id, response: item}, nil
}

// callLogResponse 是调用记录的管理端 DTO；不含任何 API Key 或请求头字段。
type callLogResponse struct {
	ID                   string          `json:"id"`
	TaskID               *string         `json:"task_id"`
	AID                  *string         `json:"ai_id"`
	ProviderID           *string         `json:"provider_id"`
	ProviderName         string          `json:"provider_name,omitempty"`
	Model                string          `json:"model"`
	Params               json.RawMessage `json:"params,omitempty"`
	RequestContent       string          `json:"request_content,omitempty"`
	ResponseContent      string          `json:"response_content,omitempty"`
	Status               string          `json:"status"`
	HTTPStatus           *int            `json:"http_status"`
	ErrorType            string          `json:"error_type"`
	ErrorMessage         string          `json:"error_message"`
	UsageInputTokens     *int            `json:"usage_input_tokens"`
	UsageOutputTokens    *int            `json:"usage_output_tokens"`
	UsageReasoningTokens *int            `json:"usage_reasoning_tokens,omitempty"`
	Attempt              int             `json:"attempt"`
	LatencyMS            int64           `json:"latency_ms"`
	Truncated            bool            `json:"truncated"`
	StartedAt            string          `json:"started_at"`
	FinishedAt           string          `json:"finished_at"`
}

// listParams 解析列表分页参数；失败时已经写出 400 响应。
func (s *Service) listParams(c *gin.Context) (int, int64, bool) {
	limit, err := api.PageLimit(c)
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return 0, 0, false
	}
	cursor, err := api.TrimPageCursor(c)
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return 0, 0, false
	}
	return limit, cursor, true
}

// parsePathID 解析路径参数中的雪花 ID；失败时已经写出 400 响应。
func parsePathID(c *gin.Context, name, message string) (int64, bool) {
	id, err := api.ParseID(c.Param(name))
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", message)
		return 0, false
	}
	return id, true
}

// ensureAIUser 校验目标账号是启用状态无关的 AI 用户；不存在或不是 AI 时返回 404。
func ensureAIUser(ctx context.Context, q access.Querier, aiID int64) error {
	var isAI bool
	err := q.QueryRow(ctx, `SELECT is_ai FROM users WHERE id = $1`, aiID).Scan(&isAI)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.ErrNotFound("AI 用户不存在")
	}
	if err != nil {
		return err
	}
	if !isAI {
		return api.ErrNotFound("AI 用户不存在")
	}
	return nil
}

// formatOptionalTime 把可选时间格式化为 RFC3339（+08:00）。
func formatOptionalTime(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := api.FormatTime(*value)
	return &formatted
}

// formatOptionalID 把可选雪花 ID 序列化为字符串。
func formatOptionalID(value *int64) *string {
	if value == nil {
		return nil
	}
	formatted := api.FormatID(*value)
	return &formatted
}

// taskKey 生成管理端手动任务的幂等键（每次请求唯一，允许重复点击生成多条任务）。
func taskKey(kind, mode string, aiID int64) string {
	return kind + ":" + mode + ":" + api.FormatID(aiID) + ":" + strconv.FormatInt(time.Now().UnixNano(), 10)
}
