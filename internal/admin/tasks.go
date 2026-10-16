package admin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/audit"
	"github.com/great-magician-01/timeline/internal/auth"
	"github.com/great-magician-01/timeline/internal/jobs"
)

type jobResponse struct {
	ID          string `json:"id"`
	JobType     string `json:"job_type"`
	RefType     string `json:"ref_type"`
	RefID       string `json:"ref_id,omitempty"`
	Status      string `json:"status"`
	RunAt       string `json:"run_at"`
	Attempts    int    `json:"attempts"`
	MaxAttempts int    `json:"max_attempts"`
	LastError   string `json:"last_error"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// ListTasks 列出后台作业（定时发布、媒体处理、记忆整理、AI 任务），
// 支持按作业类型与状态筛选，用于排障入口（docs/DEPLOYMENT.md 10.3）。
func (s *Service) ListTasks(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	limit, err := api.PageLimit(c)
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	cursor, err := api.TrimPageCursor(c)
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	where := []string{"TRUE"}
	args := make([]any, 0, 4)
	addArg := func(value any) string {
		args = append(args, value)
		return "$" + strconv.Itoa(len(args))
	}
	if jobType := strings.TrimSpace(c.Query("type")); jobType != "" {
		where = append(where, "job_type = "+addArg(jobType))
	}
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		where = append(where, "status = "+addArg(status))
	}
	if cursor > 0 {
		where = append(where, "id < "+addArg(cursor))
	}
	args = append(args, limit+1)

	rows, err := pool.Query(c.Request.Context(), `
		SELECT id, job_type, COALESCE(ref_type, ''), ref_id, status, run_at,
		       attempts, max_attempts, last_error, created_at, updated_at
		FROM background_jobs
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY id DESC LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		s.internalError(c, "查询后台作业失败", err)
		return
	}
	defer rows.Close()

	items := make([]jobResponse, 0, limit+1)
	var lastID int64
	for rows.Next() {
		var item jobResponse
		var id int64
		var runAt, createdAt, updatedAt time.Time
		var refID *int64
		if err := rows.Scan(
			&id, &item.JobType, &item.RefType, &refID, &item.Status, &runAt,
			&item.Attempts, &item.MaxAttempts, &item.LastError, &createdAt, &updatedAt,
		); err != nil {
			s.internalError(c, "读取后台作业失败", err)
			return
		}
		item.ID = api.FormatID(id)
		item.RunAt = api.FormatTime(runAt)
		item.CreatedAt = api.FormatTime(createdAt)
		item.UpdatedAt = api.FormatTime(updatedAt)
		if refID != nil {
			item.RefID = api.FormatID(*refID)
		}
		lastID = id
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		s.internalError(c, "遍历后台作业失败", err)
		return
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	c.JSON(http.StatusOK, api.ListResponse(items, lastID, hasMore))
}

// RetryTask 重新排队一个失败作业；只重置状态与重试计数，不修改业务数据。
func (s *Service) RetryTask(c *gin.Context) {
	s.changeTaskStatus(c, "retry")
}

// CancelTask 取消一个待执行或运行中的作业。
func (s *Service) CancelTask(c *gin.Context) {
	s.changeTaskStatus(c, "cancel")
}

func (s *Service) changeTaskStatus(c *gin.Context, action string) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	id, err := api.ParseID(c.Param("id"))
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	adminUser, _ := auth.CurrentUser(c)
	tx, err := pool.Begin(c.Request.Context())
	if err != nil {
		s.internalError(c, "开启作业处置事务失败", err)
		return
	}
	defer func() { _ = tx.Rollback(c.Request.Context()) }()

	var current string
	if err := tx.QueryRow(c.Request.Context(),
		`SELECT status FROM background_jobs WHERE id = $1 FOR UPDATE`, id,
	).Scan(&current); errors.Is(err, pgx.ErrNoRows) {
		api.Error(c, http.StatusNotFound, "not_found", "作业不存在")
		return
	} else if err != nil {
		s.internalError(c, "读取待处置作业失败", err)
		return
	}

	var updated string
	var auditAction string
	switch action {
	case "retry":
		if current != jobs.StatusFailed {
			api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", "只有失败的作业可以重试", gin.H{
				"status": "当前状态为 " + current,
			})
			return
		}
		updated = jobs.StatusPending
		auditAction = audit.ActionTaskRetry
		if _, err := tx.Exec(c.Request.Context(), `
			UPDATE background_jobs
			SET status = $2, attempts = 0, last_error = '', run_at = now(), updated_at = now()
			WHERE id = $1`, id, updated); err != nil {
			s.internalError(c, "重试作业失败", err)
			return
		}
	default:
		if current != jobs.StatusPending && current != jobs.StatusRunning && current != jobs.StatusPaused {
			api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", "该作业当前不能取消", gin.H{
				"status": "当前状态为 " + current,
			})
			return
		}
		updated = jobs.StatusCanceled
		auditAction = audit.ActionTaskCancel
		if _, err := tx.Exec(c.Request.Context(), `
			UPDATE background_jobs SET status = $2, updated_at = now() WHERE id = $1`, id, updated); err != nil {
			s.internalError(c, "取消作业失败", err)
			return
		}
	}

	if err := s.insertAudit(c.Request.Context(), tx, adminUser.ID, auditAction, "background_job", &id, gin.H{
		"previous_status": current,
		"status":          updated,
	}); err != nil {
		s.internalError(c, "写入户处置审计失败", err)
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		s.internalError(c, "提交作业处置事务失败", err)
		return
	}

	var item jobResponse
	var runAt, createdAt, updatedAt time.Time
	var refID *int64
	if err := pool.QueryRow(c.Request.Context(), `
		SELECT id, job_type, COALESCE(ref_type, ''), ref_id, status, run_at,
		       attempts, max_attempts, last_error, created_at, updated_at
		FROM background_jobs WHERE id = $1`, id,
	).Scan(&item.ID, &item.JobType, &item.RefType, &refID, &item.Status, &runAt,
		&item.Attempts, &item.MaxAttempts, &item.LastError, &createdAt, &updatedAt); err != nil {
		s.internalError(c, "读取作业结果失败", err)
		return
	}
	item.RunAt = api.FormatTime(runAt)
	item.CreatedAt = api.FormatTime(createdAt)
	item.UpdatedAt = api.FormatTime(updatedAt)
	if refID != nil {
		item.RefID = api.FormatID(*refID)
	}
	c.JSON(http.StatusOK, gin.H{"job": item})
}
