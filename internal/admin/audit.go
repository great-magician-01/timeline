package admin

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/api"
)

type auditResponse struct {
	ID         string          `json:"id"`
	Admin      auditAdmin      `json:"admin"`
	ActionType string          `json:"action_type"`
	TargetType string          `json:"target_type"`
	TargetID   string          `json:"target_id,omitempty"`
	Detail     json.RawMessage `json:"detail"`
	CreatedAt  string          `json:"created_at"`
}

type auditAdmin struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Nickname string `json:"nickname"`
}

func (s *Service) ListAuditLogs(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	limit, err := api.PageLimit(c)
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	cursor, err := api.DecodeCursor(strings.TrimSpace(c.Query("cursor")))
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
	if action := strings.TrimSpace(c.Query("action_type")); action != "" {
		where = append(where, "l.action_type = "+addArg(action))
	}
	if adminName := strings.TrimSpace(c.Query("admin")); adminName != "" {
		where = append(where, "LOWER(u.username) = LOWER("+addArg(adminName)+")")
	}
	if cursor > 0 {
		where = append(where, "l.id < "+addArg(cursor))
	}
	args = append(args, limit+1)
	limitPlaceholder := "$" + strconv.Itoa(len(args))

	rows, err := pool.Query(c.Request.Context(), `
		SELECT l.id, l.action_type, l.target_type, l.target_id, l.detail, l.created_at,
		       u.id, u.username, u.nickname
		FROM admin_audit_logs AS l
		JOIN users AS u ON u.id = l.admin_id
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY l.id DESC LIMIT `+limitPlaceholder, args...)
	if err != nil {
		s.internalError(c, "查询管理审计日志失败", err)
		return
	}
	defer rows.Close()

	items := make([]auditResponse, 0, limit+1)
	for rows.Next() {
		var item auditResponse
		var id, adminID int64
		var targetID *int64
		var createdAt time.Time
		if err := rows.Scan(
			&id, &item.ActionType, &item.TargetType, &targetID, &item.Detail, &createdAt,
			&adminID, &item.Admin.Username, &item.Admin.Nickname,
		); err != nil {
			s.internalError(c, "读取管理审计日志失败", err)
			return
		}
		item.ID = api.FormatID(id)
		item.Admin.ID = api.FormatID(adminID)
		if targetID != nil {
			item.TargetID = api.FormatID(*targetID)
		}
		item.CreatedAt = api.FormatTime(createdAt)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		s.internalError(c, "遍历管理审计日志失败", err)
		return
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	var nextCursor any
	if hasMore && len(items) > 0 {
		lastID, _ := strconv.ParseInt(items[len(items)-1].ID, 10, 64)
		nextCursor = api.EncodeCursor(lastID)
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "next_cursor": nextCursor, "has_more": hasMore})
}
