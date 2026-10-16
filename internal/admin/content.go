package admin

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
	"github.com/great-magician-01/timeline/internal/audit"
	"github.com/great-magician-01/timeline/internal/auth"
	"github.com/great-magician-01/timeline/internal/model"
	"github.com/great-magician-01/timeline/internal/richtext"
)

// AdminPostResponse 是后台帖子列表项：含全部可见性级别，便于审核。
type AdminPostResponse struct {
	ID              string                  `json:"id"`
	Author          model.UserBriefResponse `json:"author"`
	Status          string                  `json:"status"`
	Visibility      string                  `json:"visibility"`
	ContentHTML     string                  `json:"content_html"`
	PublishedAt     *string                 `json:"published_at"`
	CreatedAt       string                  `json:"created_at"`
	LikeCount       int                     `json:"like_count"`
	CommentCount    int                     `json:"comment_count"`
	RepostCount     int                     `json:"repost_count"`
	AttachmentCount int                     `json:"attachment_count"`
}

// AdminCommentResponse 是后台评论列表项。
type AdminCommentResponse struct {
	ID          string                  `json:"id"`
	PostID      string                  `json:"post_id"`
	Author      model.UserBriefResponse `json:"author"`
	ContentHTML string                  `json:"content_html"`
	Status      string                  `json:"status"`
	CreatedAt   string                  `json:"created_at"`
}

// ListPosts 查询全部可见性级别的帖子，供管理员审核（docs/API.md §12.2）。
func (s *Service) ListPosts(c *gin.Context) {
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
	args := make([]any, 0, 6)
	addArg := func(value any) string {
		args = append(args, value)
		return "$" + strconv.Itoa(len(args))
	}
	if visibility := strings.TrimSpace(c.Query("visibility")); visibility != "" {
		switch visibility {
		case model.VisibilityPublic, model.VisibilityFollowers, model.VisibilityPrivate:
			where = append(where, "p.visibility = "+addArg(visibility))
		default:
			api.Error(c, http.StatusBadRequest, "invalid_request", "visibility 取值非法")
			return
		}
	}
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		switch status {
		case model.PostStatusDraft, model.PostStatusScheduled, model.PostStatusPublished, model.PostStatusDeleted:
			where = append(where, "p.status = "+addArg(status))
		default:
			api.Error(c, http.StatusBadRequest, "invalid_request", "status 取值非法")
			return
		}
	}
	if author := strings.TrimSpace(c.Query("author")); author != "" {
		where = append(where, "LOWER(u.username) = LOWER("+addArg(author)+")")
	}
	if q := strings.TrimSpace(c.Query("q")); q != "" {
		if len([]rune(q)) > 100 {
			api.Error(c, http.StatusBadRequest, "invalid_request", "搜索关键词过长")
			return
		}
		placeholder := addArg(q)
		where = append(where, "STRPOS(LOWER(COALESCE(ps.plain_text, '')), LOWER("+placeholder+")) > 0")
	}
	if cursor > 0 {
		where = append(where, "p.id < "+addArg(cursor))
	}
	args = append(args, limit+1)

	rows, err := pool.Query(c.Request.Context(), `
		SELECT p.id, p.status, p.visibility, COALESCE(pv.content_html, ''), p.published_at, p.created_at,
		       p.like_count, p.comment_count, p.repost_count,
		       u.id, u.username, u.nickname, u.avatar_url, u.is_ai, u.status,
		       (SELECT COUNT(*) FROM attachments AS a
		        WHERE a.post_id = p.id AND a.status = 'available') AS attachment_count
		FROM posts AS p
		JOIN users AS u ON u.id = p.author_id
		LEFT JOIN post_versions AS pv ON pv.id = p.current_version_id
		LEFT JOIN post_search AS ps ON ps.post_id = p.id
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY p.id DESC LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		s.internalError(c, "查询后台帖子列表失败", err)
		return
	}
	defer rows.Close()

	items := make([]AdminPostResponse, 0, limit+1)
	var lastID int64
	for rows.Next() {
		var item AdminPostResponse
		var id, authorID int64
		var publishedAt *time.Time
		var createdAt time.Time
		var authorStatus string
		if err := rows.Scan(
			&id, &item.Status, &item.Visibility, &item.ContentHTML, &publishedAt, &createdAt,
			&item.LikeCount, &item.CommentCount, &item.RepostCount,
			&authorID, &item.Author.Username, &item.Author.Nickname, &item.Author.AvatarURL,
			&item.Author.IsAI, &authorStatus, &item.AttachmentCount,
		); err != nil {
			s.internalError(c, "读取后台帖子列表失败", err)
			return
		}
		item.ID = api.FormatID(id)
		item.Author.ID = api.FormatID(authorID)
		item.Author.IsSuspended = authorStatus != auth.StatusActive
		item.CreatedAt = api.FormatTime(createdAt)
		if publishedAt != nil {
			formatted := api.FormatTime(*publishedAt)
			item.PublishedAt = &formatted
		}
		lastID = id
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		s.internalError(c, "遍历后台帖子列表失败", err)
		return
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	c.JSON(http.StatusOK, api.ListResponse(items, lastID, hasMore))
}

// GetPost 返回后台帖子详情：正文、作者、历史版本与附件，供处置前确认。
func (s *Service) GetPost(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	id, err := api.ParseID(c.Param("id"))
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	ctx := c.Request.Context()

	post, err := model.ScanPost(pool.QueryRow(ctx, `SELECT `+model.PostColumns+` FROM posts WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		api.Error(c, http.StatusNotFound, "not_found", "帖子不存在")
		return
	}
	if err != nil {
		s.internalError(c, "读取后台帖子失败", err)
		return
	}
	author, err := access.UserBriefByID(ctx, pool, post.AuthorID)
	if err != nil {
		s.internalError(c, "读取帖子作者失败", err)
		return
	}

	var contentHTML string
	if post.CurrentVersionID != nil {
		if err := pool.QueryRow(ctx,
			`SELECT content_html FROM post_versions WHERE id = $1`, *post.CurrentVersionID,
		).Scan(&contentHTML); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			s.internalError(c, "读取帖子正文失败", err)
			return
		}
	}

	versions, err := s.postVersions(ctx, id)
	if err != nil {
		s.internalError(c, "读取帖子历史版本失败", err)
		return
	}
	attachments, err := s.postAttachments(ctx, id)
	if err != nil {
		s.internalError(c, "读取帖子附件失败", err)
		return
	}

	detail := gin.H{
		"id":            api.FormatID(post.ID),
		"author":        author.Response(),
		"status":        post.Status,
		"visibility":    post.Visibility,
		"content_html":  contentHTML,
		"created_at":    api.FormatTime(post.CreatedAt),
		"updated_at":    api.FormatTime(post.UpdatedAt),
		"like_count":    post.LikeCount,
		"comment_count": post.CommentCount,
		"repost_count":  post.RepostCount,
	}
	if post.PublishedAt != nil {
		detail["published_at"] = api.FormatTime(*post.PublishedAt)
	}
	if post.ScheduledAt != nil {
		detail["scheduled_at"] = api.FormatTime(*post.ScheduledAt)
	}
	if post.DeletedAt != nil {
		detail["deleted_at"] = api.FormatTime(*post.DeletedAt)
	}
	c.JSON(http.StatusOK, gin.H{"post": detail, "versions": versions, "attachments": attachments})
}

func (s *Service) postVersions(ctx context.Context, postID int64) ([]gin.H, error) {
	pool := s.pools.Pool()
	rows, err := pool.Query(ctx, `
		SELECT id, version_number, visibility, created_at, restored_from_version_id
		FROM post_versions WHERE post_id = $1 ORDER BY version_number DESC`, postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []gin.H{}
	for rows.Next() {
		var id int64
		var number int
		var visibility string
		var createdAt time.Time
		var restoredFrom *int64
		if err := rows.Scan(&id, &number, &visibility, &createdAt, &restoredFrom); err != nil {
			return nil, err
		}
		entry := gin.H{
			"id":             api.FormatID(id),
			"version_number": number,
			"visibility":     visibility,
			"created_at":     api.FormatTime(createdAt),
		}
		if restoredFrom != nil {
			entry["restored_from_version_id"] = api.FormatID(*restoredFrom)
		}
		items = append(items, entry)
	}
	return items, rows.Err()
}

func (s *Service) postAttachments(ctx context.Context, postID int64) ([]gin.H, error) {
	pool := s.pools.Pool()
	rows, err := pool.Query(ctx, `
		SELECT id, kind, original_filename, media_type, size_bytes, status, created_at
		FROM attachments WHERE post_id = $1 ORDER BY id`, postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []gin.H{}
	for rows.Next() {
		var id, size int64
		var kind, name, mediaType, status string
		var createdAt time.Time
		if err := rows.Scan(&id, &kind, &name, &mediaType, &size, &status, &createdAt); err != nil {
			return nil, err
		}
		items = append(items, gin.H{
			"id":                api.FormatID(id),
			"kind":              kind,
			"original_filename": name,
			"media_type":        mediaType,
			"size_bytes":        size,
			"status":            status,
			"created_at":        api.FormatTime(createdAt),
		})
	}
	return items, rows.Err()
}

// DeletePost 逻辑删除任意可见性级别的帖子并写审计（docs/API.md §12.2）。
func (s *Service) DeletePost(c *gin.Context) {
	if _, ok := s.pool(c); !ok {
		return
	}
	id, err := api.ParseID(c.Param("id"))
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	adminUser, _ := auth.CurrentUser(c)
	// 审计与删除在同一事务内提交：处置发生即一定有对应审计记录。
	_, err = s.content.DeletePostInternal(c.Request.Context(), adminUser.ID, id, true,
		func(ctx context.Context, tx pgx.Tx, postID, authorID int64) error {
			return audit.Write(ctx, tx, s.ids, adminUser.ID, audit.ActionPostDelete, "post", &postID, gin.H{
				"author_id": api.FormatID(authorID),
			})
		})
	if err != nil {
		if !api.Respond(c, err) {
			s.internalError(c, "删除帖子失败", err)
		}
		return
	}
	c.Status(http.StatusNoContent)
}

// ListComments 查询评论，供管理员审核。
func (s *Service) ListComments(c *gin.Context) {
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
	if author := strings.TrimSpace(c.Query("author")); author != "" {
		where = append(where, "LOWER(u.username) = LOWER("+addArg(author)+")")
	}
	if q := strings.TrimSpace(c.Query("q")); q != "" {
		if len([]rune(q)) > 100 {
			api.Error(c, http.StatusBadRequest, "invalid_request", "搜索关键词过长")
			return
		}
		where = append(where, "STRPOS(LOWER(c.plain_text), LOWER("+addArg(q)+")) > 0")
	}
	if cursor > 0 {
		where = append(where, "c.id < "+addArg(cursor))
	}
	args = append(args, limit+1)

	rows, err := pool.Query(c.Request.Context(), `
		SELECT c.id, c.post_id, c.content_html, c.status, c.created_at,
		       u.id, u.username, u.nickname, u.avatar_url, u.is_ai, u.status
		FROM comments AS c
		JOIN users AS u ON u.id = c.author_id
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY c.id DESC LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		s.internalError(c, "查询后台评论列表失败", err)
		return
	}
	defer rows.Close()

	items := make([]AdminCommentResponse, 0, limit+1)
	var lastID int64
	for rows.Next() {
		var item AdminCommentResponse
		var id, postID, authorID int64
		var createdAt time.Time
		var authorStatus string
		if err := rows.Scan(
			&id, &postID, &item.ContentHTML, &item.Status, &createdAt,
			&authorID, &item.Author.Username, &item.Author.Nickname, &item.Author.AvatarURL,
			&item.Author.IsAI, &authorStatus,
		); err != nil {
			s.internalError(c, "读取后台评论列表失败", err)
			return
		}
		item.ID = api.FormatID(id)
		item.PostID = api.FormatID(postID)
		item.Author.ID = api.FormatID(authorID)
		item.Author.IsSuspended = authorStatus != auth.StatusActive
		item.CreatedAt = api.FormatTime(createdAt)
		lastID = id
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		s.internalError(c, "遍历后台评论列表失败", err)
		return
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	c.JSON(http.StatusOK, api.ListResponse(items, lastID, hasMore))
}

// DeleteComment 逻辑删除任意评论并写审计。
func (s *Service) DeleteComment(c *gin.Context) {
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
		s.internalError(c, "开启评论处置事务失败", err)
		return
	}
	defer func() { _ = tx.Rollback(c.Request.Context()) }()

	if err := s.social.DeleteCommentFor(c.Request.Context(), tx, adminUser.ID, true, id); err != nil {
		if !api.Respond(c, err) {
			s.internalError(c, "删除评论失败", err)
		}
		return
	}
	if err := s.insertAudit(c.Request.Context(), tx, adminUser.ID, audit.ActionCommentDelete, "comment", &id, gin.H{}); err != nil {
		s.internalError(c, "写入评论删除审计失败", err)
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		s.internalError(c, "提交评论处置事务失败", err)
		return
	}
	c.Status(http.StatusNoContent)
}

// richtextSummary 供后台列表使用：把安全 HTML 转为纯文本摘要。
func richtextSummary(html string, limit int) string {
	text := []rune(richtext.PlainText(html))
	if len(text) <= limit {
		return string(text)
	}
	return string(text[:limit]) + "…"
}
