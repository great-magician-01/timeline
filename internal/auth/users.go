package auth

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/great-magician-01/timeline/internal/api"
)

// GetProfile 返回个人主页资料：公开资料、计数与访问者视角的关注状态
// （docs/API.md §4）。计数只统计已生效关系，待审批申请不计入。
func (s *Service) GetProfile(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	u, err := UserByUsername(c.Request.Context(), pool, c.Param("username"))
	if errors.Is(err, pgx.ErrNoRows) {
		api.Error(c, http.StatusNotFound, "not_found", "用户不存在")
		return
	}
	if err != nil {
		s.internalError(c, "读取用户资料失败", err)
		return
	}

	ctx := c.Request.Context()
	var postCount, followingCount, followerCount int64
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM posts WHERE author_id = $1 AND status = 'published'),
			(SELECT COUNT(*) FROM follows WHERE follower_id = $1 AND is_active),
			(SELECT COUNT(*) FROM follows WHERE followee_id = $1 AND is_active)`, u.ID,
	).Scan(&postCount, &followingCount, &followerCount); err != nil {
		s.internalError(c, "统计用户资料失败", err)
		return
	}

	followState := "none"
	viewer, loggedIn := CurrentUser(c)
	if loggedIn && viewer.ID != u.ID && !viewer.IsAI {
		if err := pool.QueryRow(ctx, `
			SELECT CASE
				WHEN EXISTS (SELECT 1 FROM follows WHERE follower_id = $1 AND followee_id = $2 AND is_active)
					THEN 'following'
				WHEN EXISTS (SELECT 1 FROM follow_requests WHERE requester_id = $1 AND target_id = $2 AND status = 'pending')
					THEN 'pending'
				WHEN EXISTS (SELECT 1 FROM follow_requests WHERE requester_id = $2 AND target_id = $1 AND status = 'pending')
					THEN 'incoming_pending'
				ELSE 'none'
			END`, viewer.ID, u.ID).Scan(&followState); err != nil {
			s.internalError(c, "读取关注状态失败", err)
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"user":            PublicUser(u),
		"bio":             u.Bio,
		"post_count":      postCount,
		"following_count": followingCount,
		"follower_count":  followerCount,
		"follow_state":    followState,
	})
}

func (s *Service) UpdateProfile(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	var in struct {
		Nickname  *string `json:"nickname"`
		AvatarURL *string `json:"avatar_url"`
		Bio       *string `json:"bio"`
	}
	if err := api.DecodeJSON(c, &in); err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", invalidInputError(err))
		return
	}
	if in.Nickname == nil && in.AvatarURL == nil && in.Bio == nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", "至少需要修改一项资料")
		return
	}
	if details := validateProfile(in.Nickname, in.Bio); details != nil {
		api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", validationMessage(details), details)
		return
	}
	current, _ := CurrentUser(c)
	if in.AvatarURL != nil && *in.AvatarURL != current.AvatarURL {
		api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", "头像需通过附件上传后设置", gin.H{
			"avatar_url": "头像上传将随附件模块开放",
		})
		return
	}

	var nickname, bio, avatarURL any
	if in.Nickname != nil {
		nickname = *in.Nickname
	}
	if in.Bio != nil {
		bio = *in.Bio
	}
	if in.AvatarURL != nil {
		avatarURL = *in.AvatarURL
	}
	u, err := ScanUser(pool.QueryRow(c.Request.Context(), `
		UPDATE users
		SET nickname = COALESCE($2::text, nickname),
		    bio = COALESCE($3::text, bio),
		    avatar_url = COALESCE($4::text, avatar_url),
		    updated_at = now()
		WHERE id = $1
		RETURNING `+UserColumns, current.ID, nickname, bio, avatarURL,
	))
	if err != nil {
		s.internalError(c, "更新用户资料失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": PrivateUser(u)})
}

// SearchUsers 按用户名或昵称搜索用户（docs/API.md §4）。
// 只返回公开资料字段；停用账号仍可被搜到（内容按原可见性只读保留），并带 is_suspended 标识。
func (s *Service) SearchUsers(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	keyword := strings.ToLower(strings.TrimSpace(c.Query("q")))
	if keyword == "" {
		api.Error(c, http.StatusBadRequest, "invalid_request", "请输入搜索关键词")
		return
	}
	if len([]rune(keyword)) > 100 {
		api.Error(c, http.StatusBadRequest, "invalid_request", "搜索关键词过长")
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

	args := []any{keyword}
	where := []string{"(STRPOS(LOWER(username), $1) > 0 OR STRPOS(LOWER(nickname), $1) > 0)"}
	if cursor > 0 {
		args = append(args, cursor)
		where = append(where, fmt.Sprintf("id < $%d", len(args)))
	}
	args = append(args, limit+1)

	rows, err := pool.Query(c.Request.Context(), `
		SELECT `+UserColumns+` FROM users
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY id DESC LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		s.internalError(c, "搜索用户失败", err)
		return
	}
	defer rows.Close()

	users := make([]User, 0, limit+1)
	for rows.Next() {
		u, scanErr := ScanUser(rows)
		if scanErr != nil {
			s.internalError(c, "读取搜索结果失败", scanErr)
			return
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		s.internalError(c, "遍历搜索结果失败", err)
		return
	}
	hasMore := len(users) > limit
	if hasMore {
		users = users[:limit]
	}
	items := make([]UserResponse, 0, len(users))
	for _, u := range users {
		items = append(items, PublicUser(u))
	}
	var lastID int64
	if len(users) > 0 {
		lastID = users[len(users)-1].ID
	}
	c.JSON(http.StatusOK, api.ListResponse(items, lastID, hasMore))
}

func (s *Service) FollowSettings(c *gin.Context) {
	u, _ := CurrentUser(c)
	c.JSON(http.StatusOK, gin.H{"require_follow_approval": u.RequireFollowApproval})
}

func (s *Service) UpdateFollowSettings(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	var in struct {
		RequireFollowApproval *bool `json:"require_follow_approval"`
	}
	if err := api.DecodeJSON(c, &in); err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", invalidInputError(err))
		return
	}
	if in.RequireFollowApproval == nil {
		api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", "缺少关注审批设置", gin.H{
			"require_follow_approval": "该字段不能为空",
		})
		return
	}
	current, _ := CurrentUser(c)
	var enabled bool
	err := pool.QueryRow(c.Request.Context(), `
		UPDATE users SET require_follow_approval = $2, updated_at = now()
		WHERE id = $1 AND NOT is_ai
		RETURNING require_follow_approval`, current.ID, *in.RequireFollowApproval,
	).Scan(&enabled)
	if err != nil {
		s.internalError(c, "更新关注审批设置失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"require_follow_approval": enabled})
}
