package notice

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/great-magician-01/timeline/internal/access"
	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/auth"
	"github.com/great-magician-01/timeline/internal/model"
)

const previewRunes = 120

// notificationEntry 是列表内部结构：对外 DTO 加上用于权限复核的目标引用。
type notificationEntry struct {
	item      NotificationResponse
	postID    int64
	commentID *int64
}

// NotificationResponse 是通知列表项。preview 在目标不可见或已删除时为 null。
type NotificationResponse struct {
	ID         string                  `json:"id"`
	Type       string                  `json:"type"`
	Actor      model.UserBriefResponse `json:"actor"`
	TargetType string                  `json:"target_type"`
	TargetID   string                  `json:"target_id"`
	Preview    *string                 `json:"preview"`
	IsRead     bool                    `json:"is_read"`
	CreatedAt  string                  `json:"created_at"`
}

func (s *Service) List(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	viewer, ok := ensureAuth(c)
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
	filter := strings.TrimSpace(c.Query("filter"))
	if filter != "" && filter != "all" && filter != "unread" {
		api.Error(c, http.StatusBadRequest, "invalid_request", "filter 只能是 all 或 unread")
		return
	}

	where := []string{"n.recipient_id = $1"}
	args := []any{viewer.ID}
	if filter == "unread" {
		where = append(where, "NOT n.is_read")
	}
	if cursor > 0 {
		args = append(args, cursor)
		where = append(where, "n.id < $"+strconv.Itoa(len(args)))
	}
	args = append(args, limit+1)

	rows, err := pool.Query(c.Request.Context(), `
		SELECT n.id, n.type, n.target_type, n.target_id, n.post_id, n.comment_id,
		       n.is_read, n.created_at,
		       u.id, u.username, u.nickname, u.avatar_url, u.is_ai, u.status
		FROM notifications AS n
		JOIN users AS u ON u.id = n.actor_id
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY n.id DESC LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		s.internalError(c, "查询通知列表失败", err)
		return
	}
	defer rows.Close()

	entries := make([]notificationEntry, 0, limit+1)
	for rows.Next() {
		var item notificationEntry
		var id, targetID, actorID int64
		var createdAt time.Time
		var postID *int64
		var status string
		if err := rows.Scan(
			&id, &item.item.Type, &item.item.TargetType, &targetID, &postID, &item.commentID,
			&item.item.IsRead, &createdAt,
			&actorID, &item.item.Actor.Username, &item.item.Actor.Nickname,
			&item.item.Actor.AvatarURL, &item.item.Actor.IsAI, &status,
		); err != nil {
			s.internalError(c, "读取通知列表失败", err)
			return
		}
		item.item.ID = api.FormatID(id)
		item.item.TargetID = api.FormatID(targetID)
		item.item.Actor.ID = api.FormatID(actorID)
		item.item.Actor.IsSuspended = status != "active"
		item.item.CreatedAt = api.FormatTime(createdAt)
		if postID != nil {
			item.postID = *postID
		}
		entries = append(entries, item)
	}
	if err := rows.Err(); err != nil {
		s.internalError(c, "遍历通知列表失败", err)
		return
	}
	hasMore := len(entries) > limit
	if hasMore {
		entries = entries[:limit]
	}

	previews, err := s.previews(c.Request.Context(), pool, viewer, entries)
	if err != nil {
		s.internalError(c, "解析通知预览失败", err)
		return
	}
	items := make([]NotificationResponse, 0, len(entries))
	var lastID int64
	for i, item := range entries {
		item.item.Preview = previews[i]
		if parsed, err := strconv.ParseInt(item.item.ID, 10, 64); err == nil {
			lastID = parsed
		}
		items = append(items, item.item)
	}
	c.JSON(http.StatusOK, api.ListResponse(items, lastID, hasMore))
}

// previews 按当前权限解析每条通知的预览文本。
// 预览不复制正文权限：无权查看或已删除时返回 nil，前端显示占位。
func (s *Service) previews(
	ctx context.Context,
	q access.Querier,
	viewer model.Viewer,
	entries []notificationEntry,
) ([]*string, error) {
	result := make([]*string, len(entries))
	postIDs := map[int64]bool{}
	commentIDs := []int64{}
	for _, entry := range entries {
		if entry.postID > 0 {
			postIDs[entry.postID] = true
		}
		if entry.commentID != nil {
			commentIDs = append(commentIDs, *entry.commentID)
		}
	}
	if len(postIDs) == 0 && len(commentIDs) == 0 {
		return result, nil
	}

	ids := make([]int64, 0, len(postIDs))
	for id := range postIDs {
		ids = append(ids, id)
	}
	visible := map[int64]bool{}
	if len(ids) > 0 {
		following, err := access.FollowingSet(ctx, q, viewer.ID)
		if err != nil {
			return nil, err
		}
		rows, err := q.Query(ctx, `
			SELECT `+model.PostColumns+` FROM posts WHERE id = ANY($1)`, ids)
		if err != nil {
			return nil, err
		}
		posts := map[int64]model.Post{}
		for rows.Next() {
			post, scanErr := model.ScanPost(rows)
			if scanErr != nil {
				rows.Close()
				return nil, scanErr
			}
			posts[post.ID] = post
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		for id, post := range posts {
			visible[id] = access.CanViewPostWithFollowing(viewer, post, following)
		}
	}

	postTexts := map[int64]string{}
	if len(ids) > 0 {
		rows, err := q.Query(ctx, `SELECT post_id, plain_text FROM post_search WHERE post_id = ANY($1)`, ids)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var postID int64
			var text string
			if err := rows.Scan(&postID, &text); err != nil {
				rows.Close()
				return nil, err
			}
			postTexts[postID] = text
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	commentTexts := map[int64]string{}
	commentPosts := map[int64]int64{}
	if len(commentIDs) > 0 {
		rows, err := q.Query(ctx, `
			SELECT id, post_id, plain_text FROM comments
			WHERE id = ANY($1) AND status = 'published'`, commentIDs)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, postID int64
			var text string
			if err := rows.Scan(&id, &postID, &text); err != nil {
				rows.Close()
				return nil, err
			}
			commentTexts[id] = text
			commentPosts[id] = postID
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	for i, entry := range entries {
		switch {
		case entry.commentID != nil:
			text, ok := commentTexts[*entry.commentID]
			if !ok {
				continue
			}
			postID := commentPosts[*entry.commentID]
			if !visible[postID] {
				// 评论列表可能只带了评论未带发帖，单独复核一次帖子可见性
				ok, err := postVisible(ctx, q, viewer, postID, visible)
				if err != nil {
					return nil, err
				}
				if !ok {
					continue
				}
			}
			result[i] = truncate(text)
		case entry.postID > 0:
			if !visible[entry.postID] {
				continue
			}
			result[i] = truncate(postTexts[entry.postID])
		case entry.item.TargetType == "user":
			// 关注类通知不需要正文预览，前端使用 actor 信息展示
			continue
		}
	}
	return result, nil
}

// postVisible 处理未在批次内加载的帖子（例如评论通知只带了评论 ID）。
func postVisible(
	ctx context.Context,
	q access.Querier,
	viewer model.Viewer,
	postID int64,
	cache map[int64]bool,
) (bool, error) {
	if visible, ok := cache[postID]; ok {
		return visible, nil
	}
	post, err := model.ScanPost(q.QueryRow(ctx, `SELECT `+model.PostColumns+` FROM posts WHERE id = $1`, postID))
	if errors.Is(err, pgx.ErrNoRows) {
		cache[postID] = false
		return false, nil
	}
	if err != nil {
		return false, err
	}
	visible, err := access.CanViewPost(ctx, q, viewer, post)
	if err != nil {
		return false, err
	}
	cache[postID] = visible
	return visible, nil
}

func truncate(text string) *string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil
	}
	runes := []rune(trimmed)
	if len(runes) > previewRunes {
		trimmed = string(runes[:previewRunes]) + "…"
	}
	return &trimmed
}

func (s *Service) UnreadCount(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	viewer, ok := ensureAuth(c)
	if !ok {
		return
	}
	var count int64
	if err := pool.QueryRow(c.Request.Context(), `
		SELECT COUNT(*) FROM notifications WHERE recipient_id = $1 AND NOT is_read`, viewer.ID,
	).Scan(&count); err != nil {
		s.internalError(c, "统计未读通知失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"unread_count": count})
}

func (s *Service) MarkRead(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	viewer, ok := ensureAuth(c)
	if !ok {
		return
	}
	id, err := api.ParseID(c.Param("id"))
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	var isRead bool
	err = pool.QueryRow(c.Request.Context(), `
		UPDATE notifications SET is_read = TRUE
		WHERE id = $1 AND recipient_id = $2
		RETURNING is_read`, id, viewer.ID).Scan(&isRead)
	if errors.Is(err, pgx.ErrNoRows) {
		api.Error(c, http.StatusNotFound, "not_found", "通知不存在")
		return
	}
	if err != nil {
		s.internalError(c, "标记通知已读失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": api.FormatID(id), "is_read": isRead})
}

func (s *Service) MarkAllRead(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	viewer, ok := ensureAuth(c)
	if !ok {
		return
	}
	tag, err := pool.Exec(c.Request.Context(), `
		UPDATE notifications SET is_read = TRUE
		WHERE recipient_id = $1 AND NOT is_read`, viewer.ID)
	if err != nil {
		s.internalError(c, "全部标记已读失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"updated": tag.RowsAffected()})
}

func viewerOf(c *gin.Context) model.Viewer {
	u, ok := auth.CurrentUser(c)
	if !ok {
		return model.GuestViewer()
	}
	return model.ViewerFrom(u)
}

// ensureAuth 校验登录身份；通知接口全部要求登录。
func ensureAuth(c *gin.Context) (model.Viewer, bool) {
	viewer := viewerOf(c)
	if !viewer.Authenticated {
		api.Error(c, http.StatusUnauthorized, "unauthenticated", "请先登录")
		return viewer, false
	}
	return viewer, true
}
