package admin

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/auth"
)

func (s *Service) ListUsers(c *gin.Context) {
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
	args := make([]any, 0, 5)
	addArg := func(value any) string {
		args = append(args, value)
		return "$" + strconv.Itoa(len(args))
	}
	if q := strings.TrimSpace(c.Query("q")); q != "" {
		if len(q) > 100 {
			api.Error(c, http.StatusBadRequest, "invalid_request", "搜索关键词过长")
			return
		}
		placeholder := addArg(q)
		where = append(where, fmt.Sprintf(`(
			STRPOS(LOWER(username), LOWER(%s)) > 0 OR
			STRPOS(LOWER(nickname), LOWER(%s)) > 0 OR
			STRPOS(LOWER(COALESCE(email, '')), LOWER(%s)) > 0
		)`, placeholder, placeholder, placeholder))
	}
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		if status != auth.StatusActive && status != auth.StatusSuspended {
			api.Error(c, http.StatusBadRequest, "invalid_request", "status 只能是 active 或 suspended")
			return
		}
		where = append(where, "status = "+addArg(status))
	}
	if rawAI := strings.TrimSpace(c.Query("is_ai")); rawAI != "" {
		isAI, parseErr := strconv.ParseBool(rawAI)
		if parseErr != nil {
			api.Error(c, http.StatusBadRequest, "invalid_request", "is_ai 必须是布尔值")
			return
		}
		where = append(where, "is_ai = "+addArg(isAI))
	}
	if cursor > 0 {
		where = append(where, "id < "+addArg(cursor))
	}
	args = append(args, limit+1)
	limitPlaceholder := "$" + strconv.Itoa(len(args))

	rows, err := pool.Query(c.Request.Context(), `
		SELECT `+auth.UserColumns+` FROM users
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY id DESC LIMIT `+limitPlaceholder, args...)
	if err != nil {
		s.internalError(c, "查询管理用户列表失败", err)
		return
	}
	defer rows.Close()

	users := make([]auth.User, 0, limit+1)
	for rows.Next() {
		u, scanErr := auth.ScanUser(rows)
		if scanErr != nil {
			s.internalError(c, "读取管理用户列表失败", scanErr)
			return
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		s.internalError(c, "遍历管理用户列表失败", err)
		return
	}
	hasMore := len(users) > limit
	if hasMore {
		users = users[:limit]
	}
	items := make([]auth.UserResponse, 0, len(users))
	for _, u := range users {
		items = append(items, auth.PrivateUser(u))
	}
	var nextCursor any
	if hasMore && len(users) > 0 {
		nextCursor = api.EncodeCursor(users[len(users)-1].ID)
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "next_cursor": nextCursor, "has_more": hasMore})
}

func (s *Service) GetUser(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	id, err := api.ParseID(c.Param("id"))
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	u, err := auth.UserByID(c.Request.Context(), pool, id, false)
	if errors.Is(err, pgx.ErrNoRows) {
		api.Error(c, http.StatusNotFound, "not_found", "用户不存在")
		return
	}
	if err != nil {
		s.internalError(c, "读取管理用户详情失败", err)
		return
	}
	var activeSessions int64
	if err := pool.QueryRow(c.Request.Context(), `
		SELECT COUNT(*) FROM auth_sessions
		WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > now()`, id,
	).Scan(&activeSessions); err != nil {
		s.internalError(c, "统计用户会话失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": auth.PrivateUser(u), "active_sessions": activeSessions})
}
